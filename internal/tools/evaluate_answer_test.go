package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/malikimayzar/mcp-gateway/internal/registry"
)

func TestEvaluateAnswer_ContractSuccess(t *testing.T) {
	var got EvaluateAnswerRequest
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/evaluate" {
			t.Errorf("request = %s %s, want POST /evaluate", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode evaluator request: %v", err)
		}
		_, _ = w.Write([]byte(`{"faithfulness_score":0,"failure_mode":"none","has_failure":false,"supported_claims":0,"total_claims":0,"evaluator_version":"1.2.1"}`))
	}))
	defer downstream.Close()
	t.Setenv("EVAL_SERVICE_URL", downstream.URL)

	resp := EvaluateAnswer(context.Background(), validEvaluateAnswerRequest())
	if !resp.Success {
		t.Fatalf("EvaluateAnswer failed: %s", resp.Error)
	}
	if got.Answer != "Short" || got.Context != "Short is in evidence" || got.Question != "question" {
		t.Errorf("downstream request = %+v", got)
	}
	if resp.Data["faithfulness_score"] != float64(0) || resp.Data["evaluator_version"] != "1.2.1" {
		t.Errorf("tool data = %#v", resp.Data)
	}
}

func TestEvaluateAnswer_RejectsMalformedResponses(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: `{"faithfulness_score":`},
		{name: "non-object", body: `[]`},
		{name: "missing required field", body: `{"faithfulness_score":0.5}`},
		{name: "wrong field type", body: `{"faithfulness_score":"0.5","failure_mode":"none","has_failure":false,"supported_claims":1,"total_claims":1}`},
		{name: "trailing JSON", body: `{"faithfulness_score":0.5,"failure_mode":"none","has_failure":false,"supported_claims":1,"total_claims":1}{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer downstream.Close()
			t.Setenv("EVAL_SERVICE_URL", downstream.URL)

			resp := EvaluateAnswer(context.Background(), validEvaluateAnswerRequest())
			if resp.Success || !strings.Contains(resp.Error, "malformed") {
				t.Errorf("expected malformed-response error, got %+v", resp)
			}
		})
	}
}

func TestEvaluateAnswer_Non2xxIsToolError(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "evaluation failed", status) }))
			defer downstream.Close()
			t.Setenv("EVAL_SERVICE_URL", downstream.URL)

			resp := EvaluateAnswer(context.Background(), validEvaluateAnswerRequest())
			if resp.Success || !strings.Contains(resp.Error, fmt.Sprintf("returned %d", status)) {
				t.Errorf("expected HTTP %d tool error, got %+v", status, resp)
			}
		})
	}
}

func TestEvaluateAnswer_TimeoutIsToolError(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{"faithfulness_score":0.5,"failure_mode":"none","has_failure":false,"supported_claims":1,"total_claims":1}`))
	}))
	defer downstream.Close()
	t.Setenv("EVAL_SERVICE_URL", downstream.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	resp := EvaluateAnswer(ctx, validEvaluateAnswerRequest())
	if resp.Success || !strings.Contains(resp.Error, "deadline exceeded") {
		t.Errorf("expected timeout tool error, got %+v", resp)
	}
}

func TestEvaluateAnswer_RejectsOversizedResponse(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, strings.Repeat("x", maxEvaluateResponseBytes+1))
	}))
	defer downstream.Close()
	t.Setenv("EVAL_SERVICE_URL", downstream.URL)

	resp := EvaluateAnswer(context.Background(), validEvaluateAnswerRequest())
	if resp.Success || !strings.Contains(resp.Error, "exceeds") {
		t.Errorf("expected response-size tool error, got %+v", resp)
	}
}

func TestEvaluateAnswer_RejectsMissingRequestParametersBeforeCallingDownstream(t *testing.T) {
	calls := 0
	downstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer downstream.Close()
	t.Setenv("EVAL_SERVICE_URL", downstream.URL)
	for _, params := range []map[string]interface{}{
		{"context": "evidence", "question": "question"},
		{"answer": "answer", "question": "question"},
		{"answer": "answer", "context": "evidence"},
	} {
		resp := EvaluateAnswer(context.Background(), registry.ToolRequest{ToolName: "evaluate_answer", Params: params})
		if resp.Success || !strings.HasPrefix(resp.Error, "param ") {
			t.Errorf("params %#v: expected parameter error, got %+v", params, resp)
		}
	}
	if calls != 0 {
		t.Errorf("downstream called %d times for invalid parameters", calls)
	}
}

func validEvaluateAnswerRequest() registry.ToolRequest {
	return registry.ToolRequest{
		ToolName: "evaluate_answer",
		TraceID:  "trace-eval",
		Params: map[string]interface{}{
			"answer": "Short", "context": "Short is in evidence", "question": "question",
		},
	}
}
