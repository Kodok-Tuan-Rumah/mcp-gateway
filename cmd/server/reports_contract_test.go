package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/malikimayzar/mcp-gateway/internal/config"
	"github.com/malikimayzar/mcp-gateway/internal/registry"
)

// newProdRouter builds the production router with default config.
// It is the same code path used by main.go, so tests here exercise real
// routing and middleware wiring rather than a hand-rolled router.
func newProdRouter() http.Handler {
	reg := registry.New()
	return newRouter(reg)
}

// newProdRouterWithKeys builds the production router with the given API keys
// and admin key, enabling auth middleware for testing auth/tenant behavior.
func newProdRouterWithKeys(apiKeys []string, adminKey string) http.Handler {
	reg := registry.New()
	cfg := config.Default()
	cfg.Auth.APIKeys = apiKeys
	cfg.Auth.AdminKey = adminKey
	cfg.Auth.RequireAuth = false // allow tests to run without GATEWAY_REQUIRE_AUTH
	return newRouterWithConfig(reg, cfg)
}

// allExpectedRoutes lists every route that must exist in the production router.
var allExpectedRoutes = []struct {
	method string
	path   string
}{
	{"GET", "/health"},
	{"GET", "/ready"},
	{"GET", "/metrics"},
	{"POST", "/tool"},
	{"POST", "/plan"},
	{"POST", "/ask"},
	{"POST", "/audit"},
	{"POST", "/reports"},
	{"GET", "/reports"},
	{"GET", "/reports/{id}"},
	{"GET", "/reports/{id}/export.md"},
	{"GET", "/reports/{id}/export.json"},
	{"GET", "/reports/{id}/export.pdf"},
	{"GET", "/r/{token}"},
	{"POST", "/reports/{id}/revoke"},
	{"GET", "/audit"},
	{"GET", "/audit/*"},
}

// TestReports_EmptyClaimsReturns400 tests that empty claims array is rejected.
func TestReports_EmptyClaimsReturns400(t *testing.T) {
	r := newProdRouter()
	body, _ := json.Marshal(map[string]interface{}{"claims": []string{}})
	req := httptest.NewRequest("POST", "/reports", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusBadRequest)
	}
}

// TestReports_InvalidJSONReturns400 tests that malformed JSON is rejected.
func TestReports_InvalidJSONReturns400(t *testing.T) {
	r := newProdRouter()
	req := httptest.NewRequest("POST", "/reports", bytes.NewReader([]byte("{bad")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusBadRequest)
	}
}

// TestReports_ShareUnknownTokenReturns404 tests that unknown share tokens return 404.
func TestReports_ShareUnknownTokenReturns404(t *testing.T) {
	r := newProdRouter()
	req := httptest.NewRequest("GET", "/r/doesnotexist", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusNotFound)
	}
}

// TestReports_InvalidReportIDReturns400 tests that invalid report IDs are rejected.
func TestReports_InvalidReportIDReturns400(t *testing.T) {
	r := newProdRouter()
	req := httptest.NewRequest("GET", "/reports/not-a-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestReports_RevokeWithoutTenantReturns404(t *testing.T) {
	r := newProdRouter()
	req := httptest.NewRequest("POST", "/reports/00000000-0000-0000-0000-000000000001/revoke", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusNotFound)
	}
}

func TestReports_PrivateReadsWithoutTenantReturn404(t *testing.T) {
	r := newProdRouter()
	paths := []string{
		"/reports/00000000-0000-0000-0000-000000000001",
		"/reports/00000000-0000-0000-0000-000000000001/export.md",
		"/reports/00000000-0000-0000-0000-000000000001/export.json",
		"/reports/00000000-0000-0000-0000-000000000001/export.pdf",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != http.StatusNotFound {
				t.Fatalf("GET %s status: got %d want %d", path, rr.Code, http.StatusNotFound)
			}
		})
	}
}

func TestReports_CreateWithoutTenantReturns403(t *testing.T) {
	r := newProdRouter()
	body, _ := json.Marshal(map[string]interface{}{"claims": []string{"claim"}})
	req := httptest.NewRequest("POST", "/reports", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusForbidden)
	}
	if !strings.Contains(rr.Body.String(), "tenant context required") {
		t.Fatalf("body = %q, want missing-tenant error", rr.Body.String())
	}
}

func TestReports_ListWithoutTenantReturns403(t *testing.T) {
	r := newProdRouter()
	req := httptest.NewRequest("GET", "/reports", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusForbidden)
	}
}

func TestReports_CreateWithoutPersistenceReturns500(t *testing.T) {
	t.Setenv("RAG_SERVICE_URL", "http://127.0.0.1:1")
	t.Setenv("EVAL_SERVICE_URL", "http://127.0.0.1:1")
	t.Setenv("RAG_TIMEOUT", "100ms")
	t.Setenv("EVAL_TIMEOUT", "100ms")

	r := newProdRouterWithKeys([]string{"api-key"}, "admin-key")
	body, _ := json.Marshal(map[string]interface{}{"claims": []string{"claim"}})
	req := httptest.NewRequest("POST", "/reports", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer api-key")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want %d; body=%s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"report_id"`) {
		t.Fatalf("failed persistence must not return a success-shaped report: %s", rr.Body.String())
	}
}

// TestReports_OverCapClaimsReturns400 tests that too many claims are rejected.
func TestReports_OverCapClaimsReturns400(t *testing.T) {
	r := newProdRouter()
	claims := make([]string, 51)
	body, _ := json.Marshal(map[string]interface{}{"claims": claims})
	req := httptest.NewRequest("POST", "/reports", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusBadRequest)
	}
}

// TestReports_MissingClaimsFieldReturns400 tests that missing claims field is rejected.
func TestReports_MissingClaimsFieldReturns400(t *testing.T) {
	r := newProdRouter()
	body, _ := json.Marshal(map[string]interface{}{"not_claims": []string{"x"}})
	req := httptest.NewRequest("POST", "/reports", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusBadRequest)
	}
}

// TestReports_NonArrayClaimsReturns400 tests that non-array claims value is rejected.
func TestReports_NonArrayClaimsReturns400(t *testing.T) {
	r := newProdRouter()
	body, _ := json.Marshal(map[string]interface{}{"claims": "not-an-array"})
	req := httptest.NewRequest("POST", "/reports", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusBadRequest)
	}
}

// TestExportMarkdownContainsFullContent tests markdown export renders all fields.
func TestExportMarkdownContainsFullContent(t *testing.T) {
	rep := map[string]interface{}{
		"report_id":   "00000000-0000-0000-0000-000000000001",
		"status":      "completed",
		"claim_count": 1,
		"summary":     "ringkasan laporan",
		"created_at":  time.Date(2026, 10, 29, 15, 56, 0, 0, time.UTC),
		"claims": []map[string]interface{}{
			{
				"claim_index":           0,
				"claim":                 "contoh klaim",
				"decision":              "SUPPORTS",
				"confidence_zone":       "green",
				"calibrated_confidence": 0.92,
				"failure_type":          "",
				"evidence":              "bukti yang tersedia",
				"span_attribution":      "dokumen A baris 3",
				"evaluation":            "faithfulness=0.9, robust=0.8",
			},
		},
	}
	md := buildReportMarkdown(rep)
	for _, want := range []string{
		"# Audit Report 00000000-0000-0000-0000-000000000001",
		"Status: completed",
		"## Summary",
		"ringkasan laporan",
		"Total Claims: 1",
		"### Claim 0",
		"**Claim:** contoh klaim",
		"**Decision:** SUPPORTS",
		"**Zona:** green",
		"**Confidence:** 0.92",
		"**Evidence:**",
		"bukti yang tersedia",
		"**Sitasi (span attribution):**",
		"dokumen A baris 3",
		"**Blok evaluasi:**",
		"faithfulness=0.9, robust=0.8",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}

// TestExportPDFRendersMagicBytes tests PDF export renders valid magic bytes.
func TestExportPDFRendersMagicBytes(t *testing.T) {
	rep := map[string]interface{}{
		"report_id":   "00000000-0000-0000-0000-000000000001",
		"status":      "completed",
		"claim_count": 1,
		"summary":     "ringkasan laporan",
		"created_at":  time.Date(2026, 10, 29, 15, 56, 0, 0, time.UTC),
		"claims": []map[string]interface{}{
			{
				"claim_index":           0,
				"claim":                 "contoh klaim",
				"decision":              "SUPPORTS",
				"confidence_zone":       "green",
				"calibrated_confidence": 0.92,
				"failure_type":          "",
				"evidence":              "bukti yang tersedia",
				"span_attribution":      "dokumen A baris 3",
				"evaluation":            "faithfulness=0.9, robust=0.8",
			},
		},
	}
	pdfBytes, err := buildReportPDF(rep)
	if err != nil {
		t.Fatalf("render pdf: %v", err)
	}
	if len(pdfBytes) == 0 {
		t.Fatal("pdf empty")
	}
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF-")) {
		t.Fatalf("pdf magic bytes missing: %q", string(pdfBytes[:8]))
	}
}

// TestReports_InvalidReportIDPDFReturns400 tests invalid PDF report ID.
func TestReports_InvalidReportIDPDFReturns400(t *testing.T) {
	r := newProdRouter()
	req := httptest.NewRequest("GET", "/reports/not-a-uuid/export.pdf", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusBadRequest)
	}
}

// TestRouteParity asserts every expected route exists in the production router.
// This guards against routes being present in a hand-built test router but
// missing from newRouter()/main.go production wiring.
func TestRouteParity(t *testing.T) {
	r := newProdRouter()

	for _, tc := range allExpectedRoutes {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		// 404 means the route is not registered at all.
		// 405 means the route exists but the method is wrong (unexpected here).
		// Anything else means the route exists and the handler ran.
		if rr.Code == http.StatusNotFound {
			t.Fatalf("route %s %s is NOT registered in production router", tc.method, tc.path)
		}
	}
}

// TestRouteParity_AuthEnsures401WithoutKey asserts that protected report routes
// require authentication when API keys are configured.
func TestRouteParity_AuthEnsures401WithoutKey(t *testing.T) {
	// Router WITH auth keys configured.
	r := newProdRouterWithKeys([]string{"sk-live-test-key-1"}, "admin-secret")

	protectedRoutes := []struct {
		method string
		path   string
	}{
		{"POST", "/reports"},
		{"GET", "/reports/{id}"},
	}

	// Replace path params with dummy values for testing.
	for _, tc := range protectedRoutes {
		path := strings.ReplaceAll(tc.path, "{id}", "00000000-0000-0000-0000-000000000001")

		req := httptest.NewRequest(tc.method, path, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		// Without any API key, auth middleware should reject with 401.
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without API key: expected 401, got %d", tc.method, path, rr.Code)
		}
	}

	// Share links are exempt from auth: /r/{token} with non-existent token
	// returns 404 (not found), not 401 (unauthorized).
	req := httptest.NewRequest("GET", "/r/abc123def456", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("GET /r/{token} without API key: expected 404 (token not found), got %d", rr.Code)
	}
}

// TestRouteParity_AuthAllowsWithValidKey asserts valid API key passes auth.
func TestRouteParity_AuthAllowsWithValidKey(t *testing.T) {
	r := newProdRouterWithKeys([]string{"sk-live-test-key-1"}, "admin-secret")

	req := httptest.NewRequest("POST", "/reports", bytes.NewBuffer([]byte(`{"claims":["test claim"]}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-live-test-key-1")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// BuildReport will fail (no RAG/evaluator running) but auth should pass.
	// Expect 500 (internal error from BuildReport) not 401 (auth failure).
	if rr.Code == http.StatusUnauthorized {
		t.Errorf("POST /reports with valid API key: got 401, expected handler to run (500)")
	}
}

// TestAdminKeyEmptyReturns503 asserts admin endpoints return 503 when no admin key configured.
func TestAdminKeyEmptyReturns503(t *testing.T) {
	// Admin routes live on the isolated admin router, not the public router.
	reg := registry.New()
	cfg := config.Default()
	cfg.Auth.AdminKey = "" // no admin key configured
	cfg.Auth.APIKeys = []string{"sk-live-test-key-1"}
	cfg.Auth.RequireAuth = false
	r := newAdminRouter(reg, cfg, nil)

	req := httptest.NewRequest("POST", "/admin/rotate-key", bytes.NewBuffer([]byte(`{"add_keys":["new-key"],"admin_key":"anything"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-live-test-key-1")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("POST /admin/rotate-key without admin key: expected 503, got %d", rr.Code)
	}
}

// TestAdminKeyWrongReturns401 asserts wrong admin key returns 401.
func TestAdminKeyWrongReturns401(t *testing.T) {
	reg := registry.New()
	cfg := config.Default()
	cfg.Auth.AdminKey = "admin-secret"
	cfg.Auth.APIKeys = []string{"sk-live-test-key-1"}
	cfg.Auth.RequireAuth = false
	r := newAdminRouter(reg, cfg, nil)

	req := httptest.NewRequest("POST", "/admin/rotate-key", bytes.NewBuffer([]byte(`{"add_keys":["new-key"],"admin_key":"wrong-key"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-live-test-key-1")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("POST /admin/rotate-key with wrong admin key: expected 401, got %d", rr.Code)
	}
}
