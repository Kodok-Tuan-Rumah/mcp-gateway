package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/malikimayzar/mcp-gateway/internal/api"
	"github.com/malikimayzar/mcp-gateway/internal/config"
	"github.com/malikimayzar/mcp-gateway/internal/orchestrator"
	"github.com/malikimayzar/mcp-gateway/internal/planner"
	"github.com/malikimayzar/mcp-gateway/internal/registry"
	"github.com/malikimayzar/mcp-gateway/internal/store"
)

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
	r := chi.NewRouter()

	// RequestID harus paling awal: middleware dijalankan berurutan, jadi kalau
	// placed setelah Logger, access log tidak punya request ID.
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)

	r.Get("/health", handleHealth(reg))
	r.Post("/tool", handleTool(reg, cfg))
	r.Post("/plan", handlePlan(reg, cfg))
	r.Post("/ask", handleAsk(reg, cfg))

	return r
}

// ─────────────────────────────── handlers ───────────────────────────────

// handleHealth melaporkan liveness proses dan tool yang terdaftar.
//
// Ini liveness, BUKAN readiness: status "ok" tidak berarti downstream bisa
// dihubungi. Readiness per-dependensi dikerjakan di Phase 4.
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
			toolReq.TraceID = middleware.GetReqID(req.Context())
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

// handlePlan menjalankan plan deterministik untuk backend terpilih.
func handlePlan(reg *registry.Registry, cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		askReq, ok := decodeAskRequest(w, req)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(req.Context(), cfg.RequestTimeout)
		defer cancel()

		plan := planForBackend(*askReq)
		plan.TraceID = requestID(req)
		result := planner.Execute(ctx, reg, plan)
		respond(w, req, toResponse(*askReq, requestID(req), orchestratorRuleBased, result))
		logEval(req, *askReq, result, orchestratorRuleBased, cfg.StoreLogTimeout)
	}
}

// handleAsk mencoba orkestrasi LLM lebih dulu, lalu jatuh ke plan deterministik.
//
// Groq tidak memilih backend: langkah yang tool-nya bukan milik backend
// terpilih dibuang, dan bila tidak ada langkah pembawa jawaban tersisa, plan
// deterministik yang dipakai.
func handleAsk(reg *registry.Registry, cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		askReq, ok := decodeAskRequest(w, req)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(req.Context(), cfg.RequestTimeout)
		defer cancel()

		result, used := runWithGroq(ctx, reg, *askReq, requestID(req), cfg.GroqAPIKey, cfg.GroqModel)
		respond(w, req, toResponse(*askReq, requestID(req), used, result))
		logEval(req, *askReq, result, used, cfg.StoreLogTimeout)
	}
}

// runWithGroq menjalankan plan dari LLM, dengan fallback ke plan deterministik
// bila Groq gagal atau menghasilkan plan yang tidak bisa dipakai.
func runWithGroq(ctx context.Context, reg *registry.Registry, askReq api.AskRequest, traceID, groqAPIKey, groqModel string) (planner.ExecutionResult, string) {
	orcPlan, err := orchestrator.GeneratePlan(ctx, askReq.Query, groqAPIKey, groqModel)
	if err != nil {
		log.Printf("[ask] Groq gagal (%v), fallback ke plan deterministik", err)
		plan := planForBackend(askReq)
		plan.TraceID = traceID
		return planner.Execute(ctx, reg, plan), orchestratorRuleBased
	}

	plan := planFromGroq(orcPlan, askReq)
	if len(plan.Steps) == 0 {
		log.Printf("[ask] plan Groq tidak punya langkah valid untuk backend %s, fallback", askReq.Backend)
		fallback := planForBackend(askReq)
		fallback.TraceID = traceID
		return planner.Execute(ctx, reg, fallback), orchestratorRuleBased
	}

	plan.TraceID = traceID
	log.Printf("[ask] memakai plan Groq (%d langkah, backend %s)", len(plan.Steps), askReq.Backend)
	return planner.Execute(ctx, reg, plan), orchestratorGroq
}

const (
	orchestratorGroq      = "groq"
	orchestratorRuleBased = "rule-based"
)

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

func logEval(req *http.Request, askReq api.AskRequest, result planner.ExecutionResult, orchestratorName string, timeout time.Duration) {
	// Score default float64 di ExecutionResult adalah 0. Pastikan hanya hasil
	// evaluasi yang benar-benar mengirim skor (termasuk skor sah bernilai 0)
	// yang disimpan, supaya evaluator yang dilewati tidak tercatat sebagai 0.
	if _, ok := evalScore(evalData(result)); !ok {
		return
	}

	logEvalAsyncWithTimeout(req.Context(), store.EvalLog{
		Query:             askReq.Query,
		Answer:            result.Answer,
		Faithfulness:      result.Score,
		FailureMode:       store.ExtractFailureMode(evalData(result)),
		RetrievalStrategy: "hybrid",
		Orchestrator:      orchestratorName,
	}, timeout, store.LogEval)
}

// ────────────────────────────────── util ──────────────────────────────────

// requestID mengambil ID request dari middleware chi.
func requestID(req *http.Request) string {
	return middleware.GetReqID(req.Context())
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
