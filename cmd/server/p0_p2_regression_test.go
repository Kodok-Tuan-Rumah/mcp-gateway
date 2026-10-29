package main

import (
	"testing"

	"github.com/malikimayzar/mcp-gateway/internal/api"
)

// maskDSN must never leak the password value from a key=value DSN.
func TestMaskDSN_HidesPassword(t *testing.T) {
	dsn := "host=db port=5432 user=u password=supersecret dbname=d sslmode=disable"
	got := maskDSN(dsn)
	want := "host=db port=5432 user=u password=*** dbname=d sslmode=disable"
	if got != want {
		t.Fatalf("maskDSN leaked password: got %q want %q", got, want)
	}
	if got == dsn {
		t.Fatal("maskDSN returned input unchanged")
	}
}

func TestMaskDSN_Empty(t *testing.T) {
	if got := maskDSN(""); got != "" {
		t.Fatalf("maskDSN(\"\") = %q, want \"\"", got)
	}
}

// A RAG backend that abstains via its sentinel answer text must be reported as
// abstain/insufficient_context by the gateway, not as a successful answer.
func TestToResponse_RAGSentinelAnswerIsAbstention(t *testing.T) {
	sentinel := "The available information is not sufficient to provide a reliable answer."
	result := execResult(sentinel,
		okStep(toolResearchRAG, map[string]interface{}{
			"answer":       sentinel,
			"failure_type": "insufficient_context",
		}),
	)
	// Do NOT set result.Abstained: detection must come from the answer text.

	req := askReq()
	req.Backend = api.BackendResearchRAG
	resp := toResponse(req, "req-1", orchestratorRuleBased, result)

	if resp.Decision != api.DecisionAbstain {
		t.Errorf("decision: got %q want %q", resp.Decision, api.DecisionAbstain)
	}
	if resp.FailureType != api.FailureInsufficientContext {
		t.Errorf("failure_type: got %q want %q", resp.FailureType, api.FailureInsufficientContext)
	}
}

// Even when the backend only signals via the sentinel answer text (no explicit
// failure_type), the gateway must still classify it as abstention.
func TestToResponse_RAGSentinelAnswerTextOnlyIsAbstention(t *testing.T) {
	sentinel := api.RAGAbstainAnswerText
	result := execResult(sentinel,
		okStep(toolResearchRAG, map[string]interface{}{
			"answer": sentinel,
		}),
	)

	req := askReq()
	req.Backend = api.BackendResearchRAG
	resp := toResponse(req, "req-1", orchestratorRuleBased, result)

	if resp.Decision != api.DecisionAbstain {
		t.Errorf("decision: got %q want %q", resp.Decision, api.DecisionAbstain)
	}
}
