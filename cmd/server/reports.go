package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/malikimayzar/mcp-gateway/internal/config"
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
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if len(req.Claims) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "claims required"})
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config error"})
		return
	}

	// 1. Verify claims via rag-research
	verifyPayload := map[string]interface{}{"claims": req.Claims, "top_k": 5}
	verifyResp, verr := postJSONHTTP(cfg.RAG.BaseURL, "/verify_claims", verifyPayload, cfg.RAG.Timeout)
	verifyResults := []interface{}{}
	if verr == nil {
		if r, ok := verifyResp["results"].([]interface{}); ok {
			verifyResults = r
		}
	}

	// Build per-claim data
	type claimData struct {
		Claim             string
		Decision          string
		Zone              string
		CalibratedConf    float64
		FailureType       string
		Evidence          []interface{}
		SpanAttribution   []interface{}
		Evaluation        map[string]interface{}
	}
	claims := make([]claimData, len(req.Claims))
	for i, c := range req.Claims {
		cd := claimData{Claim: c, Decision: "abstain", Zone: "red", FailureType: "insufficient_context"}
		if i < len(verifyResults) {
			if vr, ok := verifyResults[i].(map[string]interface{}); ok {
				if d, ok := vr["decision"].(string); ok {
					cd.Decision = d
				}
				if z, ok := vr["confidence_zone"].(string); ok {
					cd.Zone = z
				}
				if cc, ok := vr["calibrated_confidence"].(float64); ok {
					cd.CalibratedConf = cc
				}
				if ft, ok := vr["failure_type"].(string); ok {
					cd.FailureType = ft
				}
				if ev, ok := vr["evidence"].([]interface{}); ok {
					cd.Evidence = ev
				}
				if sa, ok := vr["span_level_attribution"].([]interface{}); ok {
					cd.SpanAttribution = sa
				}
			}
		}
		claims[i] = cd
	}

	// 2. Enrich evidence source_ids via arxiv /papers/source/{id}
	for i := range claims {
		for _, ev := range claims[i].Evidence {
			if em, ok := ev.(map[string]interface{}); ok {
				src, _ := em["source_id"].(string)
				if strings.HasPrefix(src, "arxiv:") {
					id := strings.TrimPrefix(src, "arxiv:")
					path := "/papers/source/" + url.PathEscape(id)
					if meta, err := getJSON(cfg.Arxiv.BaseURL, path, cfg.Arxiv.Timeout); err == nil {
						em["source_metadata"] = meta
					}
				}
			}
		}
	}

	// 3. Evaluate batch via llm-eval: context = evidence text
	items := []map[string]interface{}{}
	for _, cd := range claims {
		ctx := ""
		for _, ev := range cd.Evidence {
			if em, ok := ev.(map[string]interface{}); ok {
				if t, ok := em["text"].(string); ok {
					ctx += t + " "
				}
			}
		}
		items = append(items, map[string]interface{}{
			"claim":   cd.Claim,
			"context": strings.TrimSpace(ctx),
		})
	}
	evalResp, eerr := postJSONHTTP(cfg.Evaluator.BaseURL, "/evaluate_batch", map[string]interface{}{"items": items}, cfg.Evaluator.Timeout)
	evalResults := []interface{}{}
	if eerr == nil {
		if r, ok := evalResp["results"].([]interface{}); ok {
			evalResults = r
		}
	}
	for i := range claims {
		if i < len(evalResults) {
			if er, ok := evalResults[i].(map[string]interface{}); ok {
				claims[i].Evaluation = er
			}
		}
	}

	// 4. Persist
	tier := r.Header.Get("X-API-Key-Tier")
	claimMaps := make([]map[string]interface{}, len(claims))
	for i, cd := range claims {
		claimMaps[i] = map[string]interface{}{
			"claim":                cd.Claim,
			"decision":             cd.Decision,
			"confidence_zone":      cd.Zone,
			"calibrated_confidence": cd.CalibratedConf,
			"failure_type":         cd.FailureType,
			"evidence":             cd.Evidence,
			"span_attribution":     cd.SpanAttribution,
			"evaluation":           cd.Evaluation,
		}
	}
	summary := map[string]interface{}{"claim_count": len(claims)}
	reportID, _, err := store.CreateAuditReport(r.Context(), tier, claimMaps, summary)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	token, _ := generateShareToken()
	expires := time.Now().Add(30 * 24 * time.Hour)
	_ = store.SetShareToken(r.Context(), reportID, token, expires)

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
