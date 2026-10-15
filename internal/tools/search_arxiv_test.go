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

func TestSearchArxiv_ContractSuccess(t *testing.T) {
	gotRequest := make(chan ArxivQueryRequest, 1)
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/query" {
			t.Errorf("request = %s %s, want POST /query", r.Method, r.URL.Path)
		}
		if gotContentType := r.Header.Get("Content-Type"); gotContentType != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", gotContentType)
		}
		var got ArxivQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode downstream request: %v", err)
		}
		gotRequest <- got
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answer":"Supported answer","sources":[{"text":"source text","score":0.75,"arxiv_id":"2401.01234"}],"retrieval_ms":12,"generation_ms":34,"model":"phi3:mini"}`))
	}))
	defer downstream.Close()
	t.Setenv("ARXIV_SERVICE_URL", downstream.URL)

	resp := SearchArxiv(context.Background(), registry.ToolRequest{
		ToolName: "search_arxiv",
		TraceID:  "trace-adapter",
		Params: map[string]interface{}{
			"query": "  paper about retrieval  ",
			"top_k": float64(7),
		},
	})
	if !resp.Success {
		t.Fatalf("SearchArxiv failed: %s", resp.Error)
	}
	got := <-gotRequest
	if got.Query != "paper about retrieval" || got.TopK != 7 {
		t.Errorf("downstream request = %+v", got)
	}
	if _, ok := resp.TypedResult.(ArxivQueryResponse); !ok {
		t.Fatalf("TypedResult has type %T, want ArxivQueryResponse", resp.TypedResult)
	}
	if resp.Data["answer"] != "Supported answer" || resp.Data["model"] != "phi3:mini" {
		t.Errorf("unexpected tool data: %#v", resp.Data)
	}
}

func TestSearchArxiv_RejectsInvalidParametersBeforeCallingDownstream(t *testing.T) {
	calls := 0
	downstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer downstream.Close()
	t.Setenv("ARXIV_SERVICE_URL", downstream.URL)

	for _, params := range []map[string]interface{}{
		{"query": "valid query", "top_k": "many"},
		{"query": "valid query", "top_k": float64(1.5)},
		{"query": "valid query", "top_k": float64(21)},
	} {
		resp := SearchArxiv(context.Background(), registry.ToolRequest{ToolName: "search_arxiv", Params: params})
		if resp.Success || !strings.HasPrefix(resp.Error, "param ") {
			t.Errorf("params %#v: expected client parameter error, got %+v", params, resp)
		}
	}
	if calls != 0 {
		t.Errorf("downstream called %d times for invalid params", calls)
	}
}

func TestSearchArxiv_RejectsMalformedOrIncompleteResponses(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: `{"answer":`},
		{name: "missing required field", body: `{"answer":"a","sources":[],"retrieval_ms":1,"generation_ms":2}`},
		{name: "wrong field type", body: `{"answer":"a","sources":"not-an-array","retrieval_ms":1,"generation_ms":2,"model":"m"}`},
		{name: "trailing JSON", body: `{"answer":"a","sources":[],"retrieval_ms":1,"generation_ms":2,"model":"m"}{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer downstream.Close()
			t.Setenv("ARXIV_SERVICE_URL", downstream.URL)

			resp := SearchArxiv(context.Background(), validArxivRequest())
			if resp.Success || !strings.Contains(resp.Error, "malformed") {
				t.Errorf("expected malformed-response tool error, got %+v", resp)
			}
		})
	}
}

func TestSearchArxiv_Non2xxIsToolError(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer downstream.Close()
	t.Setenv("ARXIV_SERVICE_URL", downstream.URL)

	resp := SearchArxiv(context.Background(), validArxivRequest())
	if resp.Success || !strings.Contains(resp.Error, "returned 503") {
		t.Errorf("expected downstream status error, got %+v", resp)
	}
}

func TestSearchArxiv_TimeoutIsToolError(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{"answer":"late","sources":[],"retrieval_ms":0,"generation_ms":0,"model":"m"}`))
	}))
	defer downstream.Close()
	t.Setenv("ARXIV_SERVICE_URL", downstream.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	resp := SearchArxiv(ctx, validArxivRequest())
	if resp.Success || !strings.Contains(resp.Error, "deadline exceeded") {
		t.Errorf("expected timeout tool error, got %+v", resp)
	}
}

func TestSearchArxiv_RejectsOversizedResponse(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, strings.Repeat("x", maxArxivResponseBytes+1))
	}))
	defer downstream.Close()
	t.Setenv("ARXIV_SERVICE_URL", downstream.URL)

	resp := SearchArxiv(context.Background(), validArxivRequest())
	if resp.Success || !strings.Contains(resp.Error, "exceeds") {
		t.Errorf("expected response size error, got %+v", resp)
	}
}

func validArxivRequest() registry.ToolRequest {
	return registry.ToolRequest{
		ToolName: "search_arxiv",
		Params:   map[string]interface{}{"query": "valid query"},
	}
}
