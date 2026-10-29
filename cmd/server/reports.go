package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/malikimayzar/mcp-gateway/internal/audit"
	"github.com/malikimayzar/mcp-gateway/internal/store"
)

func handleCreateReport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Claims []string `json:"claims"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body failed"})
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if len(req.Claims) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "claims required"})
		return
	}
	if len(req.Claims) > 50 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many claims (max 50)"})
		return
	}

	tier := r.Header.Get("X-API-Key-Tier")
	reportID, token, _, err := audit.BuildReport(r.Context(), req.Claims, tier)
	if err != nil {
		log.Printf("[reports] create failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"report_id":   reportID,
		"status":      "completed",
		"share_token": token,
		"share_url":   "/r/" + token,
	})
}

// reportIDPattern matches canonical UUID form; prevents raw Postgres casts
// from turning malformed path segments into 500s.
var reportIDPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func handleGetReport(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !reportIDPattern.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid report id"})
		return
	}
	rep, err := store.GetAuditReport(r.Context(), id)
	if err != nil {
		if err == store.ErrNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		log.Printf("[reports] get failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func handleExportReport(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !reportIDPattern.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid report id"})
		return
	}
	rep, err := store.GetAuditReport(r.Context(), id)
	if err != nil {
		if err == store.ErrNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		log.Printf("[reports] export failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if strings.HasSuffix(r.URL.Path, ".json") {
		writeJSON(w, http.StatusOK, rep)
		return
	}
	md := fmt.Sprintf("# Audit Report %s\n\nStatus: %s\nClaims: %v\n", rep["report_id"], rep["status"], rep["claim_count"])
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(md))
}

func handleShareReport(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	rep, err := store.GetAuditReportByShareToken(r.Context(), token)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found or expired"})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}
