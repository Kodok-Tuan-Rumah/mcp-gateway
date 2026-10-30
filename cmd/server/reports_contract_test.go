package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func newReportsRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Post("/reports", handleCreateReport)
	r.Get("/reports/{id}", handleGetReport)
	r.Get("/reports/{id}/export.md", handleExportReport)
	r.Get("/reports/{id}/export.json", handleExportReport)
	r.Get("/reports/{id}/export.pdf", handleExportReport)
	r.Get("/r/{token}", handleShareReport)
	return r
}

func TestReports_EmptyClaimsReturns400(t *testing.T) {
	r := newReportsRouter()
	body, _ := json.Marshal(map[string]interface{}{"claims": []string{}})
	req := httptest.NewRequest("POST", "/reports", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestReports_InvalidJSONReturns400(t *testing.T) {
	r := newReportsRouter()
	req := httptest.NewRequest("POST", "/reports", bytes.NewReader([]byte("{bad")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestReports_ShareUnknownTokenReturns404(t *testing.T) {
	r := newReportsRouter()
	req := httptest.NewRequest("GET", "/r/doesnotexist", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusNotFound)
	}
}

func TestReports_InvalidReportIDReturns400(t *testing.T) {
	r := newReportsRouter()
	req := httptest.NewRequest("GET", "/reports/not-a-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestReports_OverCapClaimsReturns400(t *testing.T) {
	r := newReportsRouter()
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

func TestReports_MissingClaimsFieldReturns400(t *testing.T) {
	r := newReportsRouter()
	body, _ := json.Marshal(map[string]interface{}{"not_claims": []string{"x"}})
	req := httptest.NewRequest("POST", "/reports", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestReports_NonArrayClaimsReturns400(t *testing.T) {
	r := newReportsRouter()
	body, _ := json.Marshal(map[string]interface{}{"claims": "not-an-array"})
	req := httptest.NewRequest("POST", "/reports", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusBadRequest)
	}
}

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

func TestReports_InvalidReportIDPDFReturns400(t *testing.T) {
	r := newReportsRouter()
	req := httptest.NewRequest("GET", "/reports/not-a-uuid/export.pdf", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusBadRequest)
	}
}
