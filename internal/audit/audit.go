package audit

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/malikimayzar/mcp-gateway/internal/circuitbreaker"
	"github.com/malikimayzar/mcp-gateway/internal/config"
	"github.com/malikimayzar/mcp-gateway/internal/middleware"
	"github.com/malikimayzar/mcp-gateway/internal/store"
)

func GenerateShareToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

const shareTokenTTL = 30 * 24 * time.Hour

func shareTokenExpiration(now time.Time) time.Time {
	return now.Add(shareTokenTTL)
}

func hasGroundedEvidence(evidence []interface{}) bool {
	for _, item := range evidence {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		text := ""
		for _, key := range []string{"text", "content", "snippet", "excerpt", "passage"} {
			if value, ok := entry[key].(string); ok && strings.TrimSpace(value) != "" {
				text = value
				break
			}
		}
		source := ""
		for _, key := range []string{"source_id", "source", "document_id", "doc_id"} {
			if value, ok := entry[key].(string); ok && strings.TrimSpace(value) != "" {
				source = value
				break
			}
		}
		if text != "" && source != "" {
			return true
		}
	}
	return false
}

func PostJSON(baseURL, path string, payload interface{}, timeout time.Duration) (map[string]interface{}, error) {
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

func GetJSON(baseURL, path string, timeout time.Duration) (map[string]interface{}, error) {
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
		return nil, err
	}
	return out, nil
}

// reportStatus describes the lifecycle state of an audit report.
type reportStatus string

const (
	// ReportCompleted — both RAG verification and evaluator succeeded.
	ReportCompleted reportStatus = "completed"
	// ReportPartial — RAG succeeded but evaluator failed (evidence exists,
	// but no faithfulness evaluation).
	ReportPartial reportStatus = "partial"
	// ReportFailed — RAG verification failed entirely; no usable evidence.
	ReportFailed reportStatus = "failed"
)

// maxRAGRetries is the maximum number of retry attempts for a downstream
// call (AGENTS.md §Production Readiness: 1 retry max).
const maxRAGRetries = 1

// postJSONWithRetry calls PostJSON, retrying up to maxRetries times with a
// short backoff between attempts. It respects ctx cancellation.
func postJSONWithRetry(ctx context.Context, baseURL, path string, payload interface{}, timeout time.Duration, maxRetries int) (map[string]interface{}, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 250 * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		result, err := PostJSON(baseURL, path, payload, timeout)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// callWithBreakerAndRetry runs fn through an optional circuit breaker with
// retry. Returns ErrCircuitOpen if the breaker is open.
func callWithBreakerAndRetry(b *circuitbreaker.Breaker, fn func() (map[string]interface{}, error)) (map[string]interface{}, error) {
	var result map[string]interface{}
	var err error
	if b != nil {
		berr := b.Execute(func() error {
			result, err = fn()
			return err
		})
		if berr != nil {
			return nil, berr
		}
	} else {
		result, err = fn()
	}
	return result, err
}

// BuildReport runs the full audit pipeline and persists a report.
// Returns report id, share token (only when status permits), and dossier.
func BuildReport(ctx context.Context, claims []string, tier string) (string, string, map[string]interface{}, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", "", nil, err
	}

	// degradedReasons captures why the report is not fully evaluated, so the
	// dossier is never mistaken for a complete audit when a dependency failed.
	var degradedReasons []map[string]string
	addDegraded := func(service, errMsg string) {
		degradedReasons = append(degradedReasons, map[string]string{"service": service, "error": errMsg})
	}

	// ── Step 1: RAG /verify_claims (retry + circuit breaker) ───────────────
	verifyResp, verr := callWithBreakerAndRetry(circuitbreaker.GetBreaker("rag_verify_claims"), func() (map[string]interface{}, error) {
		return postJSONWithRetry(ctx, cfg.RAG.BaseURL, "/verify_claims", map[string]interface{}{"claims": claims, "top_k": 5}, cfg.RAG.Timeout, maxRAGRetries)
	})
	verifyResults := []interface{}{}
	ragFailed := false
	if verr != nil {
		ragFailed = true
		addDegraded("rag", verr.Error())
	} else if r, ok := verifyResp["results"].([]interface{}); ok {
		verifyResults = r
	}

	type claimData struct {
		Claim           string
		Decision        string
		Zone            string
		CalibratedConf  float64
		FailureType     string
		Evidence        []interface{}
		SpanAttribution []interface{}
		Evaluation      map[string]interface{}
	}
	cds := make([]claimData, len(claims))
	for i, c := range claims {
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
		if cd.Decision != "abstain" && !hasGroundedEvidence(cd.Evidence) {
			cd.Decision = "abstain"
			cd.Zone = "red"
			cd.FailureType = "insufficient_context"
		}
		cds[i] = cd
	}

	for i := range cds {
		for _, ev := range cds[i].Evidence {
			if em, ok := ev.(map[string]interface{}); ok {
				src, _ := em["source_id"].(string)
				if strings.HasPrefix(src, "arxiv:") {
					id := strings.TrimPrefix(src, "arxiv:")
					path := "/papers/source/" + url.PathEscape(id)
					if meta, err := GetJSON(cfg.Arxiv.BaseURL, path, cfg.Arxiv.Timeout); err == nil {
						em["source_metadata"] = meta
					}
				}
			}
		}
	}

	items := []map[string]interface{}{}
	for _, cd := range cds {
		ctxStr := ""
		for _, ev := range cd.Evidence {
			if em, ok := ev.(map[string]interface{}); ok {
				if t, ok := em["text"].(string); ok {
					ctxStr += t + " "
				}
			}
		}
		items = append(items, map[string]interface{}{"claim": cd.Claim, "context": strings.TrimSpace(ctxStr)})
	}

	// ── Step 2: evaluator /evaluate_batch (retry + circuit breaker) ────────
	evalResp, eerr := callWithBreakerAndRetry(circuitbreaker.GetBreaker("evaluate_batch"), func() (map[string]interface{}, error) {
		return postJSONWithRetry(ctx, cfg.Evaluator.BaseURL, "/evaluate_batch", map[string]interface{}{"items": items}, cfg.Evaluator.Timeout, maxRAGRetries)
	})
	evalResults := []interface{}{}
	evalFailed := false
	if eerr != nil {
		evalFailed = true
		addDegraded("evaluator", eerr.Error())
	} else if r, ok := evalResp["results"].([]interface{}); ok {
		evalResults = r
	}
	for i := range cds {
		if i < len(evalResults) {
			if er, ok := evalResults[i].(map[string]interface{}); ok {
				cds[i].Evaluation = er
			}
		}
	}

	// ── Step 3: determine report status ────────────────────────────────────
	// RAG failure = no usable evidence → failed.
	// Evaluator failure = evidence but no faithfulness eval → partial.
	// Both succeeded → completed.
	status := ReportCompleted
	if ragFailed {
		status = ReportFailed
	} else if evalFailed {
		status = ReportPartial
	}

	claimMaps := make([]map[string]interface{}, len(cds))
	abstained := 0
	supportedDecisions := 0
	for i, cd := range cds {
		if cd.Decision == "abstain" {
			abstained++
		} else {
			supportedDecisions++
		}
		claimMaps[i] = map[string]interface{}{
			"claim":                 cd.Claim,
			"decision":              cd.Decision,
			"confidence_zone":       cd.Zone,
			"calibrated_confidence": cd.CalibratedConf,
			"failure_type":          cd.FailureType,
			"evidence":              cd.Evidence,
			"span_attribution":      cd.SpanAttribution,
			"evaluation":            cd.Evaluation,
		}
	}
	summary := map[string]interface{}{
		"claim_count":      len(cds),
		"status":           string(status),
		"degraded_reasons": degradedReasons,
	}

	// metrics from evaluator summary if available
	metrics := map[string]interface{}{
		"claim_count":       len(cds),
		"supported_decisions": supportedDecisions,
		"abstained":           abstained,
		"status":              string(status),
		"degraded_reasons":    degradedReasons,
	}
	if evalResp != nil {
		if s, ok := evalResp["summary"].(map[string]interface{}); ok {
			metrics["avg_faithfulness"] = s["avg_faithfulness"]
			metrics["supported_pct"] = s["supported_pct"]
			metrics["partial_pct"] = s["partial_pct"]
			metrics["unsupported_pct"] = s["unsupported_pct"]
		}
	}

	orgID := middleware.OrgIDFromContext(ctx)
	reportID, _, perr := store.CreateAuditReport(ctx, orgID, tier, string(status), claimMaps, summary)
	if perr != nil {
		log.Printf("[audit] CreateAuditReport failed: %v", perr)
	}

	// Share token is only issued when the report contains usable evidence or
	// evaluation. A fully failed report (no RAG evidence) must not be
	// shareable, because it would look like a valid audit of nothing.
	var token string
	if perr == nil && (status == ReportCompleted || status == ReportPartial) {
		generatedToken, tokenErr := GenerateShareToken()
		if tokenErr != nil {
			log.Printf("[audit] share token generation failed: %v", tokenErr)
		} else if err := store.SetShareToken(ctx, reportID, generatedToken, shareTokenExpiration(time.Now())); err != nil {
			log.Printf("[audit] share token persistence failed: %v", err)
		} else {
			token = generatedToken
		}
	}

	dossier := map[string]interface{}{
		"report_id":         reportID,
		"report_url":        "/reports/" + reportID,
		"status":            string(status),
		"claim_count":       len(cds),
		"metrics":           metrics,
		"claims":            claimMaps,
		"summary":           summary,
		"degraded_reasons":  degradedReasons,
	}
	if token != "" {
		dossier["share_token"] = token
		dossier["share_url"] = "/r/" + token
	}

	return reportID, token, dossier, nil
}
