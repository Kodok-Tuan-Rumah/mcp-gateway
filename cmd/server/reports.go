package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jung-kurt/gofpdf"
	"github.com/malikimayzar/mcp-gateway/internal/audit"
	"github.com/malikimayzar/mcp-gateway/internal/middleware"
	"github.com/malikimayzar/mcp-gateway/internal/store"
)

var (
	// tenantClaimQuota limits the number of claims per tenant per report request.
	tenantClaimQuota = map[string]int{
		"free":       10,
		"pro":        50,
		"enterprise": 200,
	}
	tenantQuotaMu sync.RWMutex
)

// tenantClaimLimit returns the max claims allowed for a tenant tier.
func tenantClaimLimit(tier string) int {
	tenantQuotaMu.RLock()
	defer tenantQuotaMu.RUnlock()
	if v, ok := tenantClaimQuota[tier]; ok {
		return v
	}
	return tenantClaimQuota["free"]
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
	if len(req.Claims) > 50 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many claims (max 50)"})
		return
	}

	tier := middleware.TierFromContext(r.Context())
	if tier == "" {
		tier = "free"
	}

	// Per-tenant claim quota enforcement.
	tenantID := middleware.OrgIDFromContext(r.Context())
	if tenantID == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant context required"})
		return
	}
	claimLimit := tenantClaimLimit(tier)
	if len(req.Claims) > claimLimit {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   fmt.Sprintf("claim quota exceeded: %d claims exceed tier limit of %d for tier %q", len(req.Claims), claimLimit, tier),
			"tier":    tier,
			"limit":   claimLimit,
		})
		return
	}

	reportID, token, dossier, err := audit.BuildReport(r.Context(), req.Claims, tier)
	if err != nil {
		log.Printf("[reports] create failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if reportID == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "report persistence failed"})
		return
	}
	// BuildReport already sets status (completed/partial/failed) and only
	// issues a share token when the report contains usable evidence.
	status, _ := dossier["status"].(string)
	response := map[string]interface{}{
		"report_id":   reportID,
		"status":      status,
		"share_token": token,
	}
	if token != "" {
		response["share_url"] = "/r/" + token
	}
	writeJSON(w, http.StatusCreated, response)
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
	tenantID := middleware.OrgIDFromContext(r.Context())
	rep, err := store.GetAuditReport(r.Context(), id, tenantID)
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
	tenantID := middleware.OrgIDFromContext(r.Context())
	rep, err := store.GetAuditReport(r.Context(), id, tenantID)
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
	if strings.HasSuffix(r.URL.Path, ".pdf") {
		pdfBytes, err := buildReportPDF(rep)
		if err != nil {
			log.Printf("[reports] pdf render failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", "inline; filename=\"report.pdf\"")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pdfBytes)
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
	if orgID == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant context required"})
		return
	}
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

// buildReportPDF renders the persisted report as a PDF byte slice.
func buildReportPDF(rep map[string]interface{}) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetTitle(fmt.Sprintf("Audit Report %v", rep["report_id"]), true)
	pdf.AddPage()
	pdf.SetFont("Arial", "B", 16)
	pdf.Cell(0, 10, fmt.Sprintf("Audit Report %v", rep["report_id"]))
	pdf.Ln(12)
	pdf.SetFont("Arial", "", 11)
	pdf.Cell(0, 8, fmt.Sprintf("Status: %v", rep["status"]))
	pdf.Ln(6)
	pdf.Cell(0, 8, fmt.Sprintf("Total Claims: %v", rep["claim_count"]))
	pdf.Ln(6)
	if created, ok := rep["created_at"].(time.Time); ok && !created.IsZero() {
		pdf.Cell(0, 8, fmt.Sprintf("Created: %s", created.Format(time.RFC3339)))
		pdf.Ln(6)
	}
	if v, ok := rep["summary"].(string); ok && v != "" {
		pdf.Ln(4)
		pdf.SetFont("Arial", "B", 12)
		pdf.Cell(0, 8, "Summary")
		pdf.Ln(6)
		pdf.SetFont("Arial", "", 10)
		pdf.MultiCell(0, 6, v, "", "", false)
		pdf.Ln(4)
	}

	claims, _ := rep["claims"].([]map[string]interface{})
	if len(claims) == 0 {
		pdf.Cell(0, 8, "No claims.")
		var buf bytes.Buffer
		if err := pdf.Output(&buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	pdf.SetFont("Arial", "B", 12)
	pdf.Cell(0, 8, "Claims")
	pdf.Ln(8)

	for _, c := range claims {
		pdf.SetFont("Arial", "B", 11)
		pdf.Cell(0, 8, fmt.Sprintf("Claim %v", c["claim_index"]))
		pdf.Ln(6)
		pdf.SetFont("Arial", "", 10)
		if v, ok := c["claim"].(string); ok {
			pdf.MultiCell(0, 6, "Claim: "+v, "", "", false)
		}
		if v, ok := c["decision"].(string); ok {
			pdf.Cell(0, 6, "Decision: "+v)
			pdf.Ln(5)
		}
		if v, ok := c["confidence_zone"].(string); ok {
			pdf.Cell(0, 6, "Zone: "+v)
			pdf.Ln(5)
		}
		if v, ok := c["calibrated_confidence"].(float64); ok {
			pdf.Cell(0, 6, fmt.Sprintf("Confidence: %.2f", v))
			pdf.Ln(5)
		}
		if v, ok := c["failure_type"].(string); ok && v != "" && v != "none" {
			pdf.Cell(0, 6, "Failure Type: "+v)
			pdf.Ln(5)
		}
		if ev := c["evidence"]; ev != nil {
			pdf.SetFont("Arial", "B", 10)
			pdf.Cell(0, 6, "Evidence:")
			pdf.Ln(5)
			pdf.SetFont("Arial", "", 9)
			pdf.MultiCell(0, 5, fmt.Sprintf("%v", ev), "", "", false)
			pdf.Ln(3)
		}
		if sa := c["span_attribution"]; sa != nil {
			pdf.SetFont("Arial", "B", 10)
			pdf.Cell(0, 6, "Span Attribution:")
			pdf.Ln(5)
			pdf.SetFont("Arial", "", 9)
			pdf.MultiCell(0, 5, fmt.Sprintf("%v", sa), "", "", false)
			pdf.Ln(3)
		}
		if evl := c["evaluation"]; evl != nil {
			pdf.SetFont("Arial", "B", 10)
			pdf.Cell(0, 6, "Evaluation:")
			pdf.Ln(5)
			pdf.SetFont("Arial", "", 9)
			pdf.MultiCell(0, 5, fmt.Sprintf("%v", evl), "", "", false)
			pdf.Ln(3)
		}
		pdf.Ln(4)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
	tenantID := middleware.OrgIDFromContext(r.Context())
	if err := store.RevokeShareToken(r.Context(), id, tenantID); err != nil {
		if err == store.ErrNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		log.Printf("[reports] revoke failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"report_id": id, "revoked": "true"})
}
