package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/malikimayzar/mcp-gateway/internal/registry"
	"github.com/malikimayzar/mcp-gateway/internal/tools"
)

type arxivTestRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}

func TestAcceptance_ArxivAdapterThroughAskAndPlan(t *testing.T) {
	for _, route := range []string{"/plan", "/ask"} {
		t.Run(route, func(t *testing.T) {
			downstreamRequests := make(chan arxivTestRequest, 1)
			arxiv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/query" || r.Method != http.MethodPost {
					t.Errorf("unexpected downstream request: %s %s", r.Method, r.URL.Path)
				}
				var downstreamRequest arxivTestRequest
				if err := json.NewDecoder(r.Body).Decode(&downstreamRequest); err != nil {
					t.Errorf("decode ArXiv request: %v", err)
				}
				downstreamRequests <- downstreamRequest
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"answer":"This answer is grounded in the returned research evidence.","sources":[{"text":"evidence alpha","score":0.8,"arxiv_id":"2401.00001"},{"text":"evidence beta","score":0.6,"arxiv_id":"2401.00002"}],"retrieval_ms":11,"generation_ms":22,"model":"phi3:mini"}`))
			}))
			defer arxiv.Close()
			t.Setenv("ARXIV_SERVICE_URL", arxiv.URL)

			evalInputs := make(chan [3]string, 1)
			reg := registry.New()
			reg.Register(toolSearchArxiv, tools.SearchArxiv)
			reg.Register(toolEvaluateAnswer, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
				answer, _ := req.Params["answer"].(string)
				contextText, _ := req.Params["context"].(string)
				question, _ := req.Params["question"].(string)
				evalInputs <- [3]string{answer, contextText, question}
				return registry.ToolResponse{
					ToolName: req.ToolName,
					Success:  true,
					Data: map[string]interface{}{
						"faithfulness_score": 0.9,
						"failure_mode":       "none",
					},
				}
			})

			ts := newTestServer(reg)
			defer ts.Close()
			const query = "explain this research evidence"
			body := decodeJSON(t, postJSON(t, ts, route, map[string]interface{}{"query": query, "top_k": 2}))
			inputs := <-evalInputs
			downstreamRequest := <-downstreamRequests
			if body["decision"] != "answer" || body["failure_type"] != "none" || body["evaluation_status"] != "completed" {
				t.Fatalf("unexpected response status fields: %#v", body)
			}
			if downstreamRequest.Query != query || downstreamRequest.TopK != 2 {
				t.Errorf("ArXiv request = %+v", downstreamRequest)
			}
			if inputs[0] != body["answer"] || inputs[2] != query {
				t.Errorf("evaluator inputs answer=%q question=%q", inputs[0], inputs[2])
			}
			if inputs[1] != "evidence alpha evidence beta" {
				t.Errorf("evaluator context = %q", inputs[1])
			}

			evidence, ok := body["evidence"].([]interface{})
			if !ok || len(evidence) != 2 {
				t.Fatalf("response evidence = %#v, want two sources", body["evidence"])
			}
			first, _ := evidence[0].(map[string]interface{})
			second, _ := evidence[1].(map[string]interface{})
			if first["text"] != "evidence alpha" || first["source_id"] != "2401.00001" || first["score"] != 0.8 {
				t.Errorf("first public evidence = %#v", first)
			}
			if second["text"] != "evidence beta" || second["source_id"] != "2401.00002" || second["score"] != 0.6 {
				t.Errorf("second public evidence = %#v", second)
			}
			if body["model"] != "phi3:mini" {
				t.Errorf("model = %v", body["model"])
			}
			latency, _ := body["latency_ms"].(map[string]interface{})
			if latency["retrieval"] != float64(11) || latency["generation"] != float64(22) {
				t.Errorf("latency_ms = %#v", latency)
			}
		})
	}
}

func TestAcceptance_ShortAnswerIsEvaluatedAgainstEvidence(t *testing.T) {
	reg := registry.New()
	reg.Register(toolSearchArxiv, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName,
			TraceID:  req.TraceID,
			Success:  true,
			Data: map[string]interface{}{
				"answer":  "RAG",
				"sources": []interface{}{map[string]interface{}{"text": "RAG"}},
			},
		}
	})
	evaluatorCalled := make(chan [2]string, 1)
	reg.Register(toolEvaluateAnswer, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		answer, _ := req.Params["answer"].(string)
		contextText, _ := req.Params["context"].(string)
		evaluatorCalled <- [2]string{answer, contextText}
		return registry.ToolResponse{
			ToolName: req.ToolName,
			TraceID:  req.TraceID,
			Success:  true,
			Data:     map[string]interface{}{"faithfulness_score": 1.0, "failure_mode": "none"},
		}
	})

	ts := newTestServer(reg)
	defer ts.Close()
	body := decodeJSON(t, postJSON(t, ts, "/plan", map[string]interface{}{"query": "define RAG"}))

	inputs := <-evaluatorCalled
	if inputs != [2]string{"RAG", "RAG"} {
		t.Fatalf("evaluator received answer/context = %q/%q, want short answer and matching evidence", inputs[0], inputs[1])
	}
	if body["answer"] != "RAG" || body["evaluation_status"] != "completed" {
		t.Errorf("short-answer response = %#v", body)
	}
}

func TestAcceptance_DownstreamFailureAndAbstentionRemainDistinct(t *testing.T) {
	t.Run("malformed downstream response", func(t *testing.T) {
		arxiv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"answer":`))
		}))
		defer arxiv.Close()
		t.Setenv("ARXIV_SERVICE_URL", arxiv.URL)
		reg := registry.New()
		reg.Register(toolSearchArxiv, tools.SearchArxiv)
		ts := newTestServer(reg)
		defer ts.Close()

		resp := postJSON(t, ts, "/plan", map[string]interface{}{"query": "malformed research result"})
		body := decodeJSON(t, resp)
		if resp.StatusCode != http.StatusBadGateway || body["decision"] != "error" || body["failure_type"] != "malformed_response" {
			t.Errorf("malformed response: HTTP %d %#v", resp.StatusCode, body)
		}
	})

	t.Run("downstream failure", func(t *testing.T) {
		arxiv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "index unavailable", http.StatusServiceUnavailable)
		}))
		defer arxiv.Close()
		t.Setenv("ARXIV_SERVICE_URL", arxiv.URL)
		reg := registry.New()
		reg.Register(toolSearchArxiv, tools.SearchArxiv)
		var evaluatorCalled atomic.Bool
		reg.Register(toolEvaluateAnswer, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
			evaluatorCalled.Store(true)
			return registry.ToolResponse{ToolName: req.ToolName, Success: true, Data: map[string]interface{}{"faithfulness_score": 1.0}}
		})

		ts := newTestServer(reg)
		defer ts.Close()
		resp := postJSON(t, ts, "/plan", map[string]interface{}{"query": "research backend status"})
		body := decodeJSON(t, resp)
		if resp.StatusCode != http.StatusBadGateway || body["decision"] != "error" || body["failure_type"] != "backend_error" {
			t.Errorf("downstream failure response: HTTP %d %#v", resp.StatusCode, body)
		}
		if body["evaluation_status"] != "skipped" {
			t.Errorf("evaluation_status = %v, want skipped", body["evaluation_status"])
		}
		if evaluatorCalled.Load() {
			t.Error("evaluator must not run after backend failure")
		}
	})

	t.Run("evaluator failure", func(t *testing.T) {
		arxiv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"answer":"This answer remains available when evaluation fails.","sources":[{"text":"supporting evidence","score":0.8,"arxiv_id":"2401.00001"}],"retrieval_ms":3,"generation_ms":7,"model":"phi3:mini"}`))
		}))
		defer arxiv.Close()
		t.Setenv("ARXIV_SERVICE_URL", arxiv.URL)
		reg := registry.New()
		reg.Register(toolSearchArxiv, tools.SearchArxiv)
		reg.Register(toolEvaluateAnswer, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
			return registry.ToolResponse{ToolName: req.ToolName, Success: false, Error: "evaluate_answer call failed: connection refused"}
		})

		ts := newTestServer(reg)
		defer ts.Close()
		resp := postJSON(t, ts, "/plan", map[string]interface{}{"query": "evaluation should not discard answer"})
		body := decodeJSON(t, resp)
		if resp.StatusCode != http.StatusOK || body["decision"] != "answer" || body["failure_type"] != "none" {
			t.Errorf("evaluator failure changed answer status: HTTP %d %#v", resp.StatusCode, body)
		}
		if body["evaluation_status"] != "unavailable" || body["answer"] != "This answer remains available when evaluation fails." {
			t.Errorf("evaluator failure response: %#v", body)
		}
		if evaluation, ok := body["evaluation"].(map[string]interface{}); ok && has(evaluation, "score") {
			t.Error("unavailable evaluation must not return a score")
		}
	})

	t.Run("backend abstention", func(t *testing.T) {
		arxiv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"answer":"Not found in context.","sources":[],"retrieval_ms":3,"generation_ms":0,"model":"phi3:mini"}`))
		}))
		defer arxiv.Close()
		t.Setenv("ARXIV_SERVICE_URL", arxiv.URL)
		reg := registry.New()
		reg.Register(toolSearchArxiv, tools.SearchArxiv)
		var evaluatorCalled atomic.Bool
		reg.Register(toolEvaluateAnswer, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
			evaluatorCalled.Store(true)
			return registry.ToolResponse{ToolName: req.ToolName, Success: true, Data: map[string]interface{}{"faithfulness_score": 1.0}}
		})

		ts := newTestServer(reg)
		defer ts.Close()
		resp := postJSON(t, ts, "/plan", map[string]interface{}{"query": "recent research result"})
		body := decodeJSON(t, resp)
		if resp.StatusCode != http.StatusOK || body["decision"] != "abstain" || body["failure_type"] != "insufficient_context" {
			t.Errorf("abstention response: HTTP %d %#v", resp.StatusCode, body)
		}
		if body["evaluation_status"] != "skipped" {
			t.Errorf("evaluation_status = %v, want skipped", body["evaluation_status"])
		}
		if evaluatorCalled.Load() {
			t.Error("evaluator must not run after abstention")
		}
	})
}
