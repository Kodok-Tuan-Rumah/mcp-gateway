package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/malikimayzar/mcp-gateway/internal/audit"
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

func postJSONHTTP(baseURL, path string, payload interface{}, timeout time.Duration) (map[string]interface{}, error) {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", strings.TrimRight(baseURL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(raw))
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func getJSON(baseURL, path string, timeout time.Duration) (map[string]interface{}, error) {
	req, err := http.NewRequest("GET", strings.TrimRight(baseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(raw))
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		// maybe array
		return nil, err
	}
	return out, nil
}

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

	tier := r.Header.Get("X-API-Key-Tier")
	reportID, token, err := audit.BuildReport(r.Context(), req.Claims, tier)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"report_id":   reportID,
		"status":      "completed",
		"share_token": token,
		"share_url":   "/r/" + token,
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
	rep, err := store.GetAuditReport(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
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

var _ = time.Now
