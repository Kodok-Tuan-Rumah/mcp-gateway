package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/malikimayzar/mcp-gateway/internal/audit"
	"github.com/malikimayzar/mcp-gateway/internal/middleware"
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

	tier := middleware.TierFromContext(r.Context())
	if tier == "" {
		tier = "free"
	}
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
	md := buildReportMarkdown(rep)
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(md))
}

// buildReportMarkdown renders full persisted report content: zona, evidence,
// sitasi (span_attribution), and blok evaluasi per claim.
func buildReportMarkdown(rep map[string]interface{}) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Audit Report %s\n\n", rep["report_id"]))
	b.WriteString(fmt.Sprintf("Status: %s\n\n", rep["status"]))
	if v, ok := rep["summary"].(string); ok && v != "" {
		b.WriteString(fmt.Sprintf("## Summary\n\n%s\n\n", v))
	}
	b.WriteString(fmt.Sprintf("Total Claims: %d\n\n", rep["claim_count"]))
	if created, ok := rep["created_at"].(time.Time); ok && !created.IsZero() {
		b.WriteString(fmt.Sprintf("Created: %s\n\n", created.Format(time.RFC3339)))
	}

	claims, _ := rep["claims"].([]map[string]interface{})
	if len(claims) == 0 {
		b.WriteString("No claims.\n")
		return b.String()
	}

	b.WriteString("## Claims\n\n")
	for _, c := range claims {
		b.WriteString(fmt.Sprintf("### Claim %v\n\n", c["claim_index"]))
		if v, ok := c["claim"].(string); ok {
			b.WriteString(fmt.Sprintf("**Claim:** %s\n\n", v))
		}
		if v, ok := c["decision"].(string); ok {
			b.WriteString(fmt.Sprintf("**Decision:** %s\n\n", v))
		}
		if v, ok := c["confidence_zone"].(string); ok {
			b.WriteString(fmt.Sprintf("**Zona:** %s\n\n", v))
		}
		if v, ok := c["calibrated_confidence"].(float64); ok {
			b.WriteString(fmt.Sprintf("**Confidence:** %.2f\n\n", v))
		}
		if v, ok := c["failure_type"].(string); ok && v != "" {
			b.WriteString(fmt.Sprintf("**Failure Type:** %s\n\n", v))
		}
		if ev, ok := c["evidence"].(string); ok && ev != "" && ev != "null" {
			b.WriteString("**Evidence:**\n\n")
			b.WriteString(fmt.Sprintf("%s\n\n", ev))
		}
		if sa, ok := c["span_attribution"].(string); ok && sa != "" && sa != "null" {
			b.WriteString("**Sitasi (span attribution):**\n\n")
			b.WriteString(fmt.Sprintf("%s\n\n", sa))
		}
		if evl, ok := c["evaluation"].(string); ok && evl != "" && evl != "null" {
			b.WriteString("**Blok evaluasi:**\n\n")
			b.WriteString(fmt.Sprintf("%s\n\n", evl))
		}
		b.WriteString("---\n\n")
	}
	return b.String()
}

// handleListReports returns reports for the caller's org (newest first).
func handleListReports(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgIDFromContext(r.Context())
	limit := 20
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		fmt.Sscanf(v, "%d", &offset)
	}
	items, err := store.ListAuditReports(r.Context(), orgID, limit, offset)
	if err != nil {
		log.Printf("[reports] list failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"reports": items, "count": len(items)})
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

func handleRevokeReport(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !reportIDPattern.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid report id"})
		return
	}
	if err := store.RevokeShareToken(r.Context(), id); err != nil {
		log.Printf("[reports] revoke failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"report_id": id, "revoked": "true"})
}
