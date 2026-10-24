package main

import (
	"github.com/malikimayzar/mcp-gateway/internal/api"
	"github.com/malikimayzar/mcp-gateway/internal/planner"
)

// Nama tool yang terdaftar di registry.
const (
	toolSearchArxiv    = "search_arxiv"
	toolResearchRAG    = "research_rag"
	toolEvaluateAnswer = "evaluate_answer"
)

// planForBackend menyusun plan deterministik: satu langkah menjawab, lalu
// evaluasi.
func planForBackend(req api.AskRequest) planner.Plan {
	switch req.Backend {
	case api.BackendArxiv:
		return planner.Plan{
			Query: req.Query,
			Steps: []planner.Step{
				{
					ToolName: toolSearchArxiv,
					Params: map[string]interface{}{
						"query": req.Query,
						"top_k": float64(req.TopK),
					},
				},
				{
					ToolName: toolEvaluateAnswer,
					Params:   map[string]interface{}{},
				},
			},
		}
	case api.BackendResearchRAG:
		return planner.Plan{
			Query: req.Query,
			Steps: []planner.Step{
				{
					ToolName: toolResearchRAG,
					Params: map[string]interface{}{
						"query":        req.Query,
						"top_k":        float64(req.TopK),
						"use_reranker": false,
					},
				},
				{
					ToolName: toolEvaluateAnswer,
					Params:   map[string]interface{}{},
				},
			},
		}
	default:
		return planner.Plan{}
	}
}
