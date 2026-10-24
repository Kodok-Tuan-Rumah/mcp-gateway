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

	"github.com/malikimayzar/mcp-gateway/internal/api"
	"github.com/malikimayzar/mcp-gateway/internal/registry"
)

func TestResearchRAG_ContractSuccess(t *testing.T) {
	gotRequest := make(chan ResearchRAGRequest, 1)
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/generate" {
			t.Errorf("request = %s %s, want POST /generate", r.Method, r.URL.Path)
		}
		if gotContentType := r.Header.Get("Content-Type"); gotContentType != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", gotContentType)
		}
		var got ResearchRAGRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode downstream request: %v", err)
		}
		gotRequest <- got
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"query": "what is retrieval",
			"answer": "Supported answer",
			"retrieved_chunks": [
				{"chunk_id": "c1", "doc_id": "d1", "text": "source text", "score": 0.75}
			],
			"retrieval_method": "rrf",
			"latency_retrieval_ms": 12,
			"latency_generation_ms": 34,
			"latency_total_ms": 46,
			"model": "phi3:mini",
			"confidence": 0.85,
			"decision": "GENERATE",
			"generator_status": "GENERATE",
			"generator_confidence": 0.85,
			"reflection_passed": true,
			"reflection_score": 0.9,
			"unsupported_claims_count": 0
		}`))
	}))
	defer downstream.Close()
	t.Setenv("RAG_SERVICE_URL", downstream.URL)

	resp := ResearchRAG(context.Background(), registry.ToolRequest{
		ToolName: "research_rag",
		TraceID:  "trace-adapter",
		Params: map[string]interface{}{
			"query":        "  what is retrieval  ",
			"top_k":        float64(7),
			"use_reranker": true,
		},
	})
	if !resp.Success {
		t.Fatalf("ResearchRAG failed: %s", resp.Error)
	}
	got := <-gotRequest
	if got.Query != "what is retrieval" || got.TopK != 7 || !got.UseReranker {
		t.Errorf("downstream request = %+v", got)
	}
	typed, ok := resp.TypedResult.(ResearchRAGResponse)
	if !ok {
		t.Fatalf("TypedResult has type %T, want ResearchRAGResponse", resp.TypedResult)
	}
	if typed.ReflectionPassed == nil || !*typed.ReflectionPassed ||
		typed.ReflectionScore == nil || *typed.ReflectionScore != 0.9 ||
		typed.UnsupportedClaimsCount == nil || *typed.UnsupportedClaimsCount != 0 {
		t.Errorf("reflection metadata not preserved: %+v", typed)
	}
	if resp.Data["unsupported_claims_count"] != 0 || resp.Data["reflection_passed"] != true {
		t.Errorf("reflection metadata missing from tool data: %#v", resp.Data)
	}
	if resp.Data["answer"] != "Supported answer" || resp.Data["model"] != "phi3:mini" {
		t.Errorf("unexpected tool data: %#v", resp.Data)
	}
	if evidence := resp.TypedResult.(ResearchRAGResponse).Evidence(); len(evidence) == 0 {
		t.Fatalf("Evidence() returned empty")
	}
}

func TestResearchRAG_RejectsInvalidParametersBeforeCallingDownstream(t *testing.T) {
	calls := 0
	downstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer downstream.Close()
	t.Setenv("RAG_SERVICE_URL", downstream.URL)

	for _, params := range []map[string]interface{}{
		{"query": "valid query", "top_k": "many"},
		{"query": "valid query", "top_k": float64(1.5)},
		{"query": "valid query", "top_k": float64(21)},
		{"query": "valid query", "use_reranker": "yes"},
	} {
		resp := ResearchRAG(context.Background(), registry.ToolRequest{ToolName: "research_rag", Params: params})
		if resp.Success || !strings.HasPrefix(resp.Error, "param ") {
			t.Errorf("params %#v: expected client parameter error, got %+v", params, resp)
		}
	}
	if calls != 0 {
		t.Errorf("downstream called %d times for invalid params", calls)
	}
}

func TestResearchRAG_RejectsMalformedOrIncompleteResponses(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: `{"answer":`},
		{name: "missing required field", body: `{"answer":"a","retrieved_chunks":[],"latency_total_ms":1,"model":"m"}`},
		{name: "missing confidence", body: `{"answer":"a","retrieved_chunks":[],"latency_total_ms":1,"model":"m","decision":"GENERATE"}`},
		{name: "trailing JSON", body: `{"answer":"a","retrieved_chunks":[],"latency_total_ms":1,"model":"m","confidence":0.5,"decision":"GENERATE"}{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer downstream.Close()
			t.Setenv("RAG_SERVICE_URL", downstream.URL)

			resp := ResearchRAG(context.Background(), validResearchRAGRequest())
			if resp.Success || !strings.Contains(resp.Error, "malformed") {
				t.Errorf("expected malformed-response tool error, got %+v", resp)
			}
		})
	}
}

func TestResearchRAG_Non2xxIsToolError(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer downstream.Close()
	t.Setenv("RAG_SERVICE_URL", downstream.URL)

	resp := ResearchRAG(context.Background(), validResearchRAGRequest())
	if resp.Success || !strings.Contains(resp.Error, "returned 503") {
		t.Errorf("expected downstream status error, got %+v", resp)
	}
}

func TestResearchRAG_TimeoutIsToolError(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{"answer":"late","retrieved_chunks":[],"latency_total_ms":0,"model":"m","confidence":0,"decision":"GENERATE"}`))
	}))
	defer downstream.Close()
	t.Setenv("RAG_SERVICE_URL", downstream.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	resp := ResearchRAG(ctx, validResearchRAGRequest())
	if resp.Success || !strings.Contains(resp.Error, "deadline exceeded") {
		t.Errorf("expected timeout tool error, got %+v", resp)
	}
}

func TestResearchRAG_RejectsOversizedResponse(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, strings.Repeat("x", maxRAGResponseBytes+1))
	}))
	defer downstream.Close()
	t.Setenv("RAG_SERVICE_URL", downstream.URL)

	resp := ResearchRAG(context.Background(), validResearchRAGRequest())
	if resp.Success || !strings.Contains(resp.Error, "exceeds") {
		t.Errorf("expected response size error, got %+v", resp)
	}
}

func TestResearchRAG_IsAbstention(t *testing.T) {
	reject := ResearchRAGResponse{Decision: "REJECT"}
	if !reject.IsAbstention() {
		t.Errorf("decision REJECT should be abstention")
	}
	generate := ResearchRAGResponse{Decision: "GENERATE"}
	if generate.IsAbstention() {
		t.Errorf("decision GENERATE should not be abstention")
	}
	// framing /generate saat rag-research absten: decision tetap GENERATE,
	// abstensi ditandai failure_type atau sentinel jawaban.
	genAbstainFailure := ResearchRAGResponse{Decision: "GENERATE", FailureType: api.FailureInsufficientContext}
	if !genAbstainFailure.IsAbstention() {
		t.Errorf("GENERATE + failure_type insufficient_context should be abstention")
	}
	genUnsupportedClaims := ResearchRAGResponse{Decision: "GENERATE", FailureType: api.FailureUnsupportedClaims}
	if !genUnsupportedClaims.IsAbstention() {
		t.Errorf("GENERATE + failure_type unsupported_claims should be abstention")
	}
	genAbstainSentinel := ResearchRAGResponse{Decision: "GENERATE", Answer: api.RAGAbstainText}
	if !genAbstainSentinel.IsAbstention() {
		t.Errorf("GENERATE + sentinel answer should be abstention")
	}
}

func validResearchRAGRequest() registry.ToolRequest {
	return registry.ToolRequest{
		ToolName: "research_rag",
		Params:   map[string]interface{}{"query": "valid query"},
	}
}
