package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func newReportsRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Post("/reports", handleCreateReport)
	r.Get("/reports/{id}", handleGetReport)
	r.Get("/reports/{id}/export.md", handleExportReport)
	r.Get("/reports/{id}/export.json", handleExportReport)
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
