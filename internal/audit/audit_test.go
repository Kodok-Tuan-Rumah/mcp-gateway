package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGenerateShareToken_Is128Bit(t *testing.T) {
	token, err := GenerateShareToken()
	if err != nil {
		t.Fatalf("GenerateShareToken: %v", err)
	}
	if len(token) != 32 {
		t.Fatalf("token length: got %d want 32 (16 bytes = 128 bits hex-encoded)", len(token))
	}
	for _, c := range token {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("token contains non-hex char: %q", c)
		}
	}
}

func TestHasGroundedEvidenceRequiresSourceAndText(t *testing.T) {
	tests := []struct {
		name     string
		evidence []interface{}
		want     bool
	}{
		{name: "empty"},
		{name: "text without source", evidence: []interface{}{map[string]interface{}{"text": "passage"}}},
		{name: "source without text", evidence: []interface{}{map[string]interface{}{"source_id": "paper-1"}}},
		{name: "whitespace only", evidence: []interface{}{map[string]interface{}{"source_id": "paper-1", "text": "  "}}},
		{name: "grounded", evidence: []interface{}{map[string]interface{}{"source_id": "paper-1", "text": "passage"}}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasGroundedEvidence(test.evidence); got != test.want {
				t.Fatalf("hasGroundedEvidence() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestShareTokenExpirationIsThirtyDays(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	if got, want := shareTokenExpiration(now), now.Add(30*24*time.Hour); !got.Equal(want) {
		t.Fatalf("share token expiry = %s, want %s", got, want)
	}
}

// envGuard saves and restores process environment variables around a test so
// that config.Load() picks up test-specific service URLs and timeouts.
type envGuard struct {
	orig map[string]string
	keys []string
}

func newEnvGuard(keys ...string) *envGuard {
	eg := &envGuard{orig: make(map[string]string), keys: keys}
	for _, k := range keys {
		eg.orig[k], _ = os.LookupEnv(k)
	}
	return eg
}

func (eg *envGuard) set(k, v string) {
	os.Setenv(k, v)
}

func (eg *envGuard) restore() {
	for _, k := range eg.keys {
		if v, ok := eg.orig[k]; ok {
			os.Setenv(k, v)
		} else {
			os.Unsetenv(k)
		}
	}
}

// TestBuildReport_RAGDown_ReturnsFailedNoShareToken verifies P1-6: when the
// RAG service is unreachable, the report must have status "failed" (not
// "completed") and must NOT issue a share token, because there is no usable
// evidence to share.
func TestBuildReport_RAGDown_ReturnsFailedNoShareToken(t *testing.T) {
	eg := newEnvGuard("RAG_SERVICE_URL", "EVAL_SERVICE_URL", "RAG_TIMEOUT", "EVAL_TIMEOUT")
	defer eg.restore()

	// RAG is down: closed port with short timeout → connection refused.
	eg.set("RAG_SERVICE_URL", "http://127.0.0.1:1")
	eg.set("RAG_TIMEOUT", "1s")

	// Evaluator succeeds (irrelevant when RAG has no evidence).
	evalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []interface{}{},
			"summary": map[string]interface{}{},
		})
	}))
	defer evalSrv.Close()
	eg.set("EVAL_SERVICE_URL", evalSrv.URL)
	eg.set("EVAL_TIMEOUT", "5s")

	_, _, dossier, err := BuildReport(context.Background(), []string{"The sky is blue."}, "free")
	if err != nil {
		t.Fatalf("BuildReport should not error, got: %v", err)
	}
	if status, _ := dossier["status"].(string); status != "failed" {
		t.Errorf("expected status 'failed' when RAG is down, got %q", status)
	}
	if _, ok := dossier["share_token"]; ok {
		t.Error("expected no share_token when report is failed")
	}
	reasons, ok := dossier["degraded_reasons"].([]map[string]string)
	if !ok || len(reasons) == 0 {
		t.Errorf("expected degraded_reasons to be non-empty when RAG is down, got %#v", dossier["degraded_reasons"])
	}
	foundRAG := false
	for _, r := range reasons {
		if r["service"] == "rag" {
			foundRAG = true
		}
	}
	if !foundRAG {
		t.Error("expected degraded_reasons to include a 'rag' entry")
	}
}

// TestBuildReport_EvaluatorDown_ReturnsPartial verifies that when RAG
// succeeds but the evaluator fails, the report has status "partial" and still
// issues a share token (evidence exists, just no faithfulness evaluation).
func TestBuildReport_EvaluatorDown_ReturnsPartial(t *testing.T) {
	eg := newEnvGuard("RAG_SERVICE_URL", "EVAL_SERVICE_URL", "RAG_TIMEOUT", "EVAL_TIMEOUT")
	defer eg.restore()

	// RAG succeeds with one supported claim.
	ragSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []interface{}{
				map[string]interface{}{
					"decision":               "supported",
					"confidence_zone":        "green",
					"calibrated_confidence":  0.95,
					"failure_type":           "",
					"evidence": []interface{}{
						map[string]interface{}{"source_id": "paper-1", "text": "The sky appears blue."},
					},
					"span_level_attribution": []interface{}{},
				},
			},
		})
	}))
	defer ragSrv.Close()
	eg.set("RAG_SERVICE_URL", ragSrv.URL)
	eg.set("RAG_TIMEOUT", "5s")

	// Evaluator is down: closed port.
	eg.set("EVAL_SERVICE_URL", "http://127.0.0.1:1")
	eg.set("EVAL_TIMEOUT", "1s")

	_, _, dossier, err := BuildReport(context.Background(), []string{"The sky is blue."}, "free")
	if err != nil {
		t.Fatalf("BuildReport should not error, got: %v", err)
	}
	if status, _ := dossier["status"].(string); status != "partial" {
		t.Errorf("expected status 'partial' when evaluator is down, got %q", status)
	}
	// Share token is issued for partial reports when the report is persisted
	// successfully (requires a DB). In this test environment there is no DB,
	// so token issuance is skipped; the status is the asserted behavior.
	if _, ok := dossier["share_token"]; ok {
		t.Error("unexpected share_token without a persisted report")
	}
}

// TestBuildReport_BothUp_ReturnsCompleted verifies the happy path: both RAG
// and evaluator succeed → status "completed" with a share token.
func TestBuildReport_BothUp_ReturnsCompleted(t *testing.T) {
	eg := newEnvGuard("RAG_SERVICE_URL", "EVAL_SERVICE_URL", "RAG_TIMEOUT", "EVAL_TIMEOUT")
	defer eg.restore()

	ragSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []interface{}{
				map[string]interface{}{
					"decision":               "supported",
					"confidence_zone":        "green",
					"calibrated_confidence":  0.95,
					"failure_type":           "",
					"evidence": []interface{}{
						map[string]interface{}{"source_id": "paper-1", "text": "The sky appears blue."},
					},
					"span_level_attribution": []interface{}{},
				},
			},
		})
	}))
	defer ragSrv.Close()
	eg.set("RAG_SERVICE_URL", ragSrv.URL)
	eg.set("RAG_TIMEOUT", "5s")

	evalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []interface{}{},
			"summary": map[string]interface{}{},
		})
	}))
	defer evalSrv.Close()
	eg.set("EVAL_SERVICE_URL", evalSrv.URL)
	eg.set("EVAL_TIMEOUT", "5s")

	_, _, dossier, err := BuildReport(context.Background(), []string{"The sky is blue."}, "free")
	if err != nil {
		t.Fatalf("BuildReport should not error, got: %v", err)
	}
	if status, _ := dossier["status"].(string); status != "completed" {
		t.Errorf("expected status 'completed' when both services are up, got %q", status)
	}
	// Share token is issued for completed reports when the report is persisted
	// successfully (requires a DB). In this test environment there is no DB,
	// so token issuance is skipped; the status is the asserted behavior.
	if _, ok := dossier["share_token"]; ok {
		t.Error("unexpected share_token without a persisted report")
	}
}

func TestBuildReportDemotesSupportedClaimWithoutGroundedEvidence(t *testing.T) {
	eg := newEnvGuard("RAG_SERVICE_URL", "EVAL_SERVICE_URL", "RAG_TIMEOUT", "EVAL_TIMEOUT")
	defer eg.restore()

	ragSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []interface{}{
				map[string]interface{}{
					"decision":               "supported",
					"confidence_zone":        "green",
					"calibrated_confidence":  0.99,
					"evidence":               []interface{}{map[string]interface{}{"text": "passage without source"}},
					"span_level_attribution": []interface{}{},
				},
			},
		})
	}))
	defer ragSrv.Close()
	eg.set("RAG_SERVICE_URL", ragSrv.URL)
	eg.set("RAG_TIMEOUT", "5s")

	evalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []interface{}{},
			"summary": map[string]interface{}{},
		})
	}))
	defer evalSrv.Close()
	eg.set("EVAL_SERVICE_URL", evalSrv.URL)
	eg.set("EVAL_TIMEOUT", "5s")

	_, _, dossier, err := BuildReport(context.Background(), []string{"The sky is blue."}, "free")
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	claims, ok := dossier["claims"].([]map[string]interface{})
	if !ok || len(claims) != 1 {
		t.Fatalf("unexpected claims payload: %#v", dossier["claims"])
	}
	if claims[0]["decision"] != "abstain" || claims[0]["confidence_zone"] != "red" {
		t.Fatalf("ungrounded answer was not demoted: %#v", claims[0])
	}
	if claims[0]["failure_type"] != "insufficient_context" {
		t.Fatalf("failure_type = %v, want insufficient_context", claims[0]["failure_type"])
	}
}
