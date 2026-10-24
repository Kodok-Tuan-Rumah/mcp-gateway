package planner

import (
	"context"
	"testing"

	"github.com/malikimayzar/mcp-gateway/internal/registry"
)

func TestEvidenceFromToolData_MapsResearchRAGChunks(t *testing.T) {
	evidence := evidenceFromToolData(map[string]interface{}{
		"retrieved_chunks": []interface{}{
			map[string]interface{}{
				"chunk_id": "c1",
				"doc_id":   "d1",
				"text":     "supporting evidence",
				"score":    float64(0.75),
			},
		},
	})

	if len(evidence) != 1 {
		t.Fatalf("evidence = %+v, want one RAG chunk", evidence)
	}
	if evidence[0].Text != "supporting evidence" || evidence[0].SourceID != "d1" || evidence[0].ChunkID != "c1" {
		t.Errorf("evidence = %+v", evidence[0])
	}
	if evidence[0].Score == nil || *evidence[0].Score != 0.75 {
		t.Errorf("score = %v, want 0.75", evidence[0].Score)
	}
}

func TestExecute_RetrievedChunksCannotBecomeAnswer(t *testing.T) {
	reg := registry.New()
	reg.Register("example_answer", func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName,
			Success:  true,
			Data: map[string]interface{}{
				"results": []interface{}{map[string]interface{}{"text": "retrieved chunk"}},
			},
		}
	})
	reg.Register("evaluate_answer", func(_ context.Context, _ registry.ToolRequest) registry.ToolResponse {
		t.Fatal("evaluator must be skipped when no answer backend ran")
		return registry.ToolResponse{}
	})

	result := Execute(context.Background(), reg, Plan{
		Query: "question",
		Steps: []Step{
			{ToolName: "example_answer"},
			{ToolName: "evaluate_answer", Params: map[string]interface{}{}},
		},
	})

	if result.Answer != "" || result.Context != "" || len(result.Evidence) != 0 {
		t.Fatalf("retrieved chunks became answer/evidence: %+v", result)
	}
	if len(result.Steps) != 2 || !result.Steps[1].Skipped {
		t.Errorf("evaluation step = %+v, want skipped", result.Steps)
	}
}

func TestExecute_RetrievedChunksCannotMixWithAnswerEvidence(t *testing.T) {
	var evaluatorInput [2]string
	reg := registry.New()
	reg.Register("search_arxiv", func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName,
			Success:  true,
			Data: map[string]interface{}{
				"answer":  "answer from ArXiv",
				"sources": []interface{}{map[string]interface{}{"text": "ArXiv evidence"}},
			},
		}
	})
	reg.Register("example_answer", func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName,
			Success:  true,
			Data: map[string]interface{}{
				"results": []interface{}{map[string]interface{}{"text": "unrelated RAG chunk"}},
			},
		}
	})
	reg.Register("evaluate_answer", func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		evaluatorInput = [2]string{req.Params["answer"].(string), req.Params["context"].(string)}
		return registry.ToolResponse{ToolName: req.ToolName, Success: true, Data: map[string]interface{}{"faithfulness_score": 1.0}}
	})

	result := Execute(context.Background(), reg, Plan{
		Query: "question",
		Steps: []Step{
			{ToolName: "search_arxiv", Params: map[string]interface{}{}},
			{ToolName: "example_answer", Params: map[string]interface{}{}},
			{ToolName: "evaluate_answer", Params: map[string]interface{}{}},
		},
	})

	if result.Answer != "answer from ArXiv" || result.Context != "ArXiv evidence" {
		t.Errorf("result answer/context = %q/%q", result.Answer, result.Context)
	}
	if evaluatorInput != [2]string{"answer from ArXiv", "ArXiv evidence"} {
		t.Errorf("evaluator input = %q/%q, mixed backend evidence", evaluatorInput[0], evaluatorInput[1])
	}
}

func TestExecute_UnsupportedClaimsSkipsEvaluation(t *testing.T) {
	reg := registry.New()
	reg.Register("research_rag", func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName,
			Success:  true,
			Data: map[string]interface{}{
				"answer":       "INSUFFICIENT_CONTEXT",
				"decision":     "GENERATE",
				"failure_type": "unsupported_claims",
				"retrieved_chunks": []interface{}{
					map[string]interface{}{"text": "retrieved source context", "doc_id": "d1"},
				},
			},
		}
	})
	reg.Register("evaluate_answer", func(_ context.Context, _ registry.ToolRequest) registry.ToolResponse {
		t.Fatal("evaluator must be skipped after unsupported-claims abstention")
		return registry.ToolResponse{}
	})

	result := Execute(context.Background(), reg, Plan{
		Query: "question",
		Steps: []Step{
			{ToolName: "research_rag"},
			{ToolName: "evaluate_answer", Params: map[string]interface{}{}},
		},
	})

	if !result.Abstained {
		t.Fatal("unsupported_claims response should be treated as abstention")
	}
	if len(result.Steps) != 2 || !result.Steps[1].Skipped {
		t.Errorf("evaluation step = %+v, want skipped", result.Steps)
	}
}
