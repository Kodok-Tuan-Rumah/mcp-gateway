package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/malikimayzar/mcp-gateway/internal/admin"
	"github.com/malikimayzar/mcp-gateway/internal/api"
	"github.com/malikimayzar/mcp-gateway/internal/circuitbreaker"
	"github.com/malikimayzar/mcp-gateway/internal/config"
	"github.com/malikimayzar/mcp-gateway/internal/mcp"
	"github.com/malikimayzar/mcp-gateway/internal/middleware"
	"github.com/malikimayzar/mcp-gateway/internal/planner"
	"github.com/malikimayzar/mcp-gateway/internal/registry"
	"github.com/malikimayzar/mcp-gateway/internal/store"
	"github.com/malikimayzar/mcp-gateway/internal/tracing"
)

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_http_requests_total",
			Help: "Total HTTP requests processed by mcp-gateway",
		},
		[]string{"method", "path", "status"},
	)
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_http_request_duration_seconds",
			Help:    "HTTP request latency in seconds for mcp-gateway",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)
)

func init() {
	// Register metrics safely (ignore registration errors if already registered in tests)
	_ = prometheus.Register(httpRequestsTotal)
	_ = prometheus.Register(httpRequestDuration)
}

func promMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		duration := time.Since(start).Seconds()

		// Use chi route pattern as label to avoid unbounded cardinality from
		// raw paths (e.g. /r/{token}, /reports/{id}) and to prevent share
		// tokens / report IDs leaking into Prometheus labels.
		path := chi.RouteContext(r.Context()).RoutePattern()
		if path == "" {
			path = r.URL.Path
		}
		method := r.Method
		statusStr := strconv.Itoa(ww.Status())

		httpRequestsTotal.WithLabelValues(method, path, statusStr).Inc()
		httpRequestDuration.WithLabelValues(method, path).Observe(duration)
	})
}

// maxBodyBytes membatasi ukuran body request supaya endpoint publik tidak bisa
// dipakai menghabiskan memori.
const maxBodyBytes = 1 << 20 // 1 MiB

// newRouter membangun HTTP handler. Dipisah dari main() supaya seluruh wiring
// — termasuk validasi kontrak — bisa diuji lewat httptest tanpa menjalankan
// proses server.
func newRouter(reg *registry.Registry) http.Handler {
	return newRouterWithConfig(reg, config.Default())
}

// newRouterWithConfig builds the production router from one startup
// configuration. newRouter remains a default-config helper for focused tests.
func newRouterWithConfig(reg *registry.Registry, cfg config.Config) http.Handler {
	return newRouterWithConfigAndMCP(reg, cfg, nil, nil)
}

// newRouterWithConfigAndMCP builds the router with optional MCP server support
func newRouterWithConfigAndMCP(reg *registry.Registry, cfg config.Config, mcpServer *mcp.Server, keyStore *middleware.APIKeyStore) http.Handler {
	r := chi.NewRouter()

	// RequestID harus paling awal: middleware dijalankan berurutan, jadi kalau
	// placed setelah Logger, access log tidak punya request ID.
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.Logger)
	r.Use(promMiddleware)

	// Tracing middleware (disabled if OTEL_ENABLED != true)
	tracingCfg := tracing.LoadConfig()
	if tracingCfg.Enabled {
		r.Use(tracing.HTTPMiddleware)
	}

	// Auth middleware (API key) - uses shared key store for runtime rotation
	if keyStore == nil {
		keyStore = newKeyStore(cfg)
	}
	admin.SetGlobalKeyStore(keyStore)
	r.Use(middleware.AuthWithStore(keyStore, cfg.Auth.HeaderName, cfg.Auth.HeaderPrefix))

	// Rate limiting middleware (per-key tier)
	// Rate limiting middleware (per-key tier)
	defaultTier := middleware.TierFree
	if cfg.RateLimit.RequestsPerMinute > 0 {
		defaultTier = middleware.TierConfig{
			Name:              "default",
			RequestsPerMinute: cfg.RateLimit.RequestsPerMinute,
			Burst:             cfg.RateLimit.Burst,
		}
	}
	trustedProxies := middleware.ParseTrustedProxies(cfg.GatewayTrustedProxies)
	r.Use(middleware.PerKeyRateLimit(keyStore, defaultTier, cfg.Auth.HeaderName, cfg.Auth.HeaderPrefix, trustedProxies, 5*time.Minute, 10000))

	r.Get("/health", handleHealth(reg))
	r.Get("/ready", handleReady(cfg))
	r.Handle("/metrics", promhttp.Handler())
	r.Post("/tool", handleTool(reg, cfg))
	r.Post("/plan", handlePlan(reg, cfg))
	r.Post("/ask", handleAsk(reg, cfg))
	r.Post("/audit", handleAsk(reg, cfg))

	// Audit reports (Vertical 1 P0)
	r.Post("/reports", handleCreateReport)
	r.Get("/reports", handleListReports)
	r.Get("/reports/{id}", handleGetReport)
	r.Get("/reports/{id}/export.md", handleExportReport)
	r.Get("/reports/{id}/export.json", handleExportReport)
	r.Get("/reports/{id}/export.pdf", handleExportReport)
	r.Get("/r/{token}", handleShareReport)
	r.Post("/reports/{id}/revoke", handleRevokeReport)

	// Static audit dossier UI
	auditDir := "web/audit"
	if envDir := os.Getenv("AUDIT_UI_DIR"); envDir != "" {
		auditDir = envDir
	}
	fs := http.StripPrefix("/audit/", http.FileServer(http.Dir(auditDir)))
	r.Get("/audit", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/audit/", http.StatusMovedPermanently)
	})
	r.Get("/audit/*", fs.ServeHTTP)

	// Add MCP endpoint if server is provided
	if mcpServer != nil {
		r.Handle("/mcp", mcp.HandleMCP(mcpServer))
	}

	return r
}

// newKeyStore builds the shared API key store from configuration. Extracted so
// both the public router and the isolated admin router use identical key
// material and tier assignments.
func newKeyStore(cfg config.Config) *middleware.APIKeyStore {
	secret := cfg.InternalServiceToken
	if secret == "" {
		secret = cfg.Auth.AdminKey
	}
	keyStore := middleware.NewAPIKeyStoreWithSecret(cfg.Auth.APIKeys, secret)
	if cfg.Auth.KeyTiers != nil {
		keyStore = middleware.NewAPIKeyStoreWithTiersAndSecret(cfg.Auth.APIKeys, cfg.Auth.KeyTiers, secret)
	}
	return keyStore
}

// newAdminRouter builds the isolated admin router. It only exposes /admin/*
// endpoints and is intended to be served on a separate listener (e.g.
// 127.0.0.1:8091) that is not reachable from the public network.
func newAdminRouter(reg *registry.Registry, cfg config.Config, keyStore *middleware.APIKeyStore) http.Handler {
	r := chi.NewRouter()

	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.Logger)
	r.Use(promMiddleware)

	if keyStore == nil {
		keyStore = newKeyStore(cfg)
	}
	admin.SetGlobalKeyStore(keyStore)

	r.Post("/admin/rotate-key", admin.RotateKeyHandler(admin.GlobalKeyStore(), cfg.Auth.AdminKey))
	r.Get("/admin/key-status", admin.KeyStatusHandler(admin.GlobalKeyStore(), cfg.Auth.AdminKey))

	return r
}

// ─────────────────────────────── handlers ───────────────────────────────

// handleHealth melaporkan liveness proses dan tool yang terdaftar.
//
// Ini liveness, BUKAN readiness: status "ok" tidak berarti downstream bisa
// dihubungi. Gunakan /ready untuk pengecekan dependency.
func handleHealth(reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		tools := reg.List()
		sort.Strings(tools) // urutan map acak; dibuat deterministik

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "ok",
			"check":  "liveness",
			"tools":  tools,
		})
	}
}

// handleReady melaporkan kesiapan tiap backend dan status evaluator.
//
// Probes run concurrently with an individual deadline. Running them
// sequentially under one shared context made the last dependency look down
// whenever an earlier probe consumed the whole readiness budget. Evaluator
// availability is optional for answering; at least one answer backend must
// be reachable for this endpoint to return HTTP 200.
func handleReady(cfg config.Config) http.HandlerFunc {
	type dependency struct {
		name   string
		apiURL string
		path   string
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		dependencies := []dependency{
			{"arxiv", cfg.Arxiv.BaseURL, "/health"},
			{"evaluator", cfg.Evaluator.BaseURL, "/ready"},
			{"rag", cfg.RAG.BaseURL, "/health"},
		}

		checks := make(map[string]string, len(dependencies))
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, dep := range dependencies {
			dep := dep
			wg.Add(1)
			go func() {
				defer wg.Done()
				probeCtx, probeCancel := context.WithTimeout(ctx, 3*time.Second)
				defer probeCancel()
				status := checkService(probeCtx, dep.apiURL, dep.path)
				mu.Lock()
				checks[dep.name] = status
				mu.Unlock()
			}()
		}
		wg.Wait()

		allServicesUp := true
		for _, status := range checks {
			if status != "up" {
				allServicesUp = false
				break
			}
		}
		arxivReady := checks["arxiv"] == "up"
		ragReady := checks["rag"] == "up"
		evaluatorReady := checks["evaluator"] == "up"
		answersReady := arxivReady || ragReady

		status := http.StatusOK
		if !answersReady {
			status = http.StatusServiceUnavailable
		}

		bodyStatus := "ready"
		if !answersReady {
			bodyStatus = "not_ready"
		} else if !allServicesUp {
			bodyStatus = "degraded"
		}

		cbStates := map[string]string{}
		if cbm := circuitbreaker.GlobalManager(); cbm != nil {
			cbStates = cbm.States()
		}

		writeJSON(w, status, map[string]interface{}{
			"status":   bodyStatus,
			"check":    "readiness",
			"services": checks,
			"capabilities": map[string]bool{
				"answers":      answersReady,
				"evaluation":   evaluatorReady,
				"arxiv":        arxivReady,
				"research_rag": ragReady,
			},
			"circuit_breakers": cbStates,
		})
	}
}

func checkService(ctx context.Context, baseURL, path string) string {
	endpoint := strings.TrimRight(baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return "down"
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "down"
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return "up"
	}
	return "degraded"
}

// handleTool adalah passthrough mentah ke registry.
//
// Bentuk JSON-nya berbeda dari GatewayResponse dengan sengaja — lihat
// docs/api-contract.md §2. Endpoint ini di luar kontrak jawaban.
func handleTool(reg *registry.Registry, cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var toolReq registry.ToolRequest
		if !decodeJSONBody(w, req, &toolReq) {
			return
		}
		if toolReq.TraceID == "" {
			toolReq.TraceID = chimiddleware.GetReqID(req.Context())
		}

		ctx, cancel := context.WithTimeout(req.Context(), cfg.RequestTimeout)
		defer cancel()

		resp := reg.Execute(ctx, toolReq)
		writeJSON(w, toolStatus(resp), resp)
	}
}

// toolStatus memetakan hasil tool ke status HTTP.
//
// Kegagalan tidak boleh dipoles jadi 400: 400 artinya kesalahan client, padahal
// "backend tidak terjangkau" adalah kegagalan dependency yang layak 502.
// Yang jadi 400 hanya kasus yang benar-benar salah dari sisi client —
// tool yang tidak dikenal, atau parameter yang tidak valid — yang dilaporkan tool lewat
// Success=false dengan pesan yang bisa dikenali.
func toolStatus(resp registry.ToolResponse) int {
	if resp.Success {
		return http.StatusOK
	}
	if isClientFault(resp.Error) {
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}

// isClientFault mengenali pesan error yang berasal dari kesalahan client.
func isClientFault(msg string) bool {
	return strings.Contains(msg, "not found in registry") ||
		strings.Contains(msg, "is required") ||
		strings.HasPrefix(msg, "param ")
}

// handlePlan adalah alias kompatibilitas untuk endpoint eksekusi /ask.
func handlePlan(reg *registry.Registry, cfg config.Config) http.HandlerFunc {
	return handleBackendRequest(reg, cfg)
}

// handleAsk menjalankan satu plan deterministik untuk backend terpilih.
func handleAsk(reg *registry.Registry, cfg config.Config) http.HandlerFunc {
	return handleBackendRequest(reg, cfg)
}

// handleBackendRequest membagikan validasi, rencana, dan eksekusi yang sama
// pada /ask dan /plan agar kedua endpoint memiliki perilaku yang konsisten.
func handleBackendRequest(reg *registry.Registry, cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		askReq, ok := decodeAskRequest(w, req)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(req.Context(), cfg.RequestTimeout)
		defer cancel()

		plan := planForBackend(*askReq)
		plan.TraceID = requestID(req)

		start := time.Now()
		result := planner.Execute(ctx, reg, plan)
		elapsed := time.Since(start)

		resp := toResponse(*askReq, requestID(req), orchestratorRuleBased, result)
		respond(w, req, resp)
		logEval(req, resp, elapsed, cfg.StoreLogTimeout)

		evidenceJSON, _ := json.Marshal(resp.Evidence)
		spanJSON, _ := json.Marshal(resp.SpanLevelAttribution)
		auditCtx, auditCancel := context.WithTimeout(context.Background(), cfg.StoreLogTimeout)
		go func() {
			defer auditCancel()
		store.LogAudit(auditCtx, store.AuditLog{
			RequestID:            resp.RequestID,
			Backend:              resp.Backend,
			Claim:                resp.Query,
			Decision:             resp.Decision,
			CalibratedConfidence: resp.CalibratedConfidence,
			CalibratedDecision:   resp.CalibratedDecision,
			EvidenceJSON:         string(evidenceJSON),
			SpanJSON:             string(spanJSON),
			TenantID:             middleware.OrgIDFromContext(req.Context()),
		})
		}()
	}
}

const orchestratorRuleBased = "rule-based"

// ────────────────────────── request / response ──────────────────────────

// decodeAskRequest membaca, menormalkan, dan memvalidasi body /ask dan /plan.
//
// Validasi di sini yang menegakkan docs/api-contract.md §2: backend yang tidak
// dikenal atau belum tersedia ditolak dengan 400 sebelum plan apa pun dijalankan.
func decodeAskRequest(w http.ResponseWriter, req *http.Request) (*api.AskRequest, bool) {
	var askReq api.AskRequest
	if !decodeJSONBody(w, req, &askReq) {
		return nil, false
	}

	askReq.Normalize()

	if err := askReq.Validate(); err != nil {
		var ve *api.ValidationError
		if errors.As(err, &ve) {
			writeJSON(w, http.StatusBadRequest, api.ErrorResponse{
				RequestID:   requestID(req),
				FailureType: ve.FailureType,
				Field:       ve.Field,
				Message:     ve.Message,
			})
			return nil, false
		}
		writeJSON(w, http.StatusBadRequest, api.ErrorResponse{
			RequestID:   requestID(req),
			FailureType: api.FailureInvalidRequest,
			Message:     err.Error(),
		})
		return nil, false
	}

	return &askReq, true
}

// respond menulis GatewayResponse dengan status yang sesuai.
//
// Backend yang gagal total → 502, karena itu kegagalan dependency, bukan
// kesalahan client. Jawaban yang berhasil dinilai atau dilewati tetap 200.
func respond(w http.ResponseWriter, _ *http.Request, resp api.GatewayResponse) {
	status := http.StatusOK
	if resp.Decision == api.DecisionError {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, resp)
}

// logEval menyimpan satu request ke tabel gateway.
//
// Setiap request dicatat, bukan hanya yang dinilai: EvaluationStatus
// membedakan completed / unavailable / skipped, dan Faithfulness *float64
// membuat "tidak dinilai" tersimpan sebagai NULL, bukan 0. Membuang request
// yang tidak dinilai akan mengisi tabel hanya dengan kasus sukses, dan itu
// membuat tren faithfulness terlihat lebih baik dari kenyataannya.
func logEval(req *http.Request, resp api.GatewayResponse, elapsed, timeout time.Duration) {
	var score *float64
	if resp.Evaluation != nil && resp.Evaluation.Score != nil {
		score = resp.Evaluation.Score
	}

	var failureMode string
	if resp.Evaluation != nil {
		failureMode = resp.Evaluation.FailureMode
	}

	logEvalAsyncWithTimeout(req.Context(), store.EvalLog{
		RequestID:        resp.RequestID,
		Query:            resp.Query,
		Answer:           resp.Answer,
		Backend:          resp.Backend,
		BackendModel:     resp.Model,
		Orchestrator:     resp.Orchestrator,
		Decision:         resp.Decision,
		FailureType:      resp.FailureType,
		EvaluationStatus: resp.EvaluationStatus,
		Faithfulness:     score,
		FailureMode:      failureMode,
		DurationMs:       int(elapsed.Milliseconds()),
	}, timeout, store.LogEval)
}

// ────────────────────────────────── util ──────────────────────────────────

// requestID mengambil ID request dari middleware chi.
func requestID(req *http.Request) string {
	return chimiddleware.GetReqID(req.Context())
}

// decodeJSONBody membaca body dengan batas ukuran, lalu menormalkan error decode.
func decodeJSONBody(w http.ResponseWriter, req *http.Request, dst interface{}) bool {
	req.Body = http.MaxBytesReader(w, req.Body, maxBodyBytes)

	dec := json.NewDecoder(req.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, api.ErrorResponse{
			FailureType: api.FailureInvalidRequest,
			Message:     "invalid request body: " + err.Error(),
		})
		return false
	}
	var trailing interface{}
	if err := dec.Decode(&trailing); err != io.EOF {
		message := "request body must contain exactly one JSON value"
		if err != nil {
			message = "invalid request body: " + err.Error()
		}
		writeJSON(w, http.StatusBadRequest, api.ErrorResponse{
			FailureType: api.FailureInvalidRequest,
			Message:     message,
		})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[http] gagal menulis response: %v", err)
	}
}
