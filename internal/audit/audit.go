package audit

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/malikimayzar/mcp-gateway/internal/config"
	"github.com/malikimayzar/mcp-gateway/internal/store"
)

func GenerateShareToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
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

// BuildReport runs the full audit pipeline and persists a report. Returns report id.
func BuildReport(ctx context.Context, claims []string, tier string) (string, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", "", err
	}

	verifyResp, verr := PostJSON(cfg.RAG.BaseURL, "/verify_claims", map[string]interface{}{"claims": claims, "top_k": 5}, cfg.RAG.Timeout)
	verifyResults := []interface{}{}
	if verr == nil {
		if r, ok := verifyResp["results"].([]interface{}); ok {
			verifyResults = r
		}
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
		ctx := ""
		for _, ev := range cd.Evidence {
			if em, ok := ev.(map[string]interface{}); ok {
				if t, ok := em["text"].(string); ok {
					ctx += t + " "
				}
			}
		}
		items = append(items, map[string]interface{}{"claim": cd.Claim, "context": strings.TrimSpace(ctx)})
	}
	evalResp, eerr := PostJSON(cfg.Evaluator.BaseURL, "/evaluate_batch", map[string]interface{}{"items": items}, cfg.Evaluator.Timeout)
	evalResults := []interface{}{}
	if eerr == nil {
		if r, ok := evalResp["results"].([]interface{}); ok {
			evalResults = r
		}
	}
	for i := range cds {
		if i < len(evalResults) {
			if er, ok := evalResults[i].(map[string]interface{}); ok {
				cds[i].Evaluation = er
			}
		}
	}

	claimMaps := make([]map[string]interface{}, len(cds))
	for i, cd := range cds {
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
	summary := map[string]interface{}{"claim_count": len(cds)}
	reportID, _, err := store.CreateAuditReport(ctx, tier, claimMaps, summary)
	if err != nil {
		return "", "", err
	}

	token, _ := GenerateShareToken()
	expires := time.Now().Add(30 * 24 * time.Hour)
	_ = store.SetShareToken(ctx, reportID, token, expires)

	return reportID, token, nil
}
