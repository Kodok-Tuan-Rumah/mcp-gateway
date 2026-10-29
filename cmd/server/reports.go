package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/malikimayzar/mcp-gateway/internal/store"
)

// generateShareToken returns a 128-bit hex token.
func generateShareToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func handleCreateReport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Claims []map[string]interface{} `json:"claims"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if len(req.Claims) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "claims required"})
		return
	}

	tier := ""
	if ks := r.Header.Get("X-API-Key-Tier"); ks != "" {
		tier = ks
	}

	summary := map[string]interface{}{"claim_count": len(req.Claims)}
	reportID, _, err := store.CreateAuditReport(r.Context(), tier, req.Claims, summary)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// generate share token
	token, _ := generateShareToken()
	// store token (best-effort via direct update not exposed; keep simple)
	_ = token

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"report_id": reportID,
		"status":    "completed",
	})
}

func handleGetReport(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rep, err := store.GetAuditReport(r.Context(), id)
	if err != nil {
		if err == store.ErrNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func handleExportReport(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	format := "json"
	if len(r.URL.Path) >= 8 && r.URL.Path[len(r.URL.Path)-3:] == ".md" {
		format = "md"
	}
	rep, err := store.GetAuditReport(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if format == "json" {
		writeJSON(w, http.StatusOK, rep)
		return
	}
	// markdown
	md := fmt.Sprintf("# Audit Report %s\n\nStatus: %s\nClaims: %d\n", rep["report_id"], rep["status"], rep["claim_count"])
	writeJSON(w, http.StatusOK, map[string]string{"markdown": md})
}

func handleShareReport(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	// For P0, just return a placeholder; full lookup by token requires store support.
	writeJSON(w, http.StatusOK, map[string]string{"share_token": token, "status": "ok"})
}

var _ = time.Now
var _ = json.Marshal
