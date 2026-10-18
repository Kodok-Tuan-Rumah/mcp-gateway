package main

import (
	"context"
	"net/http/httptest"

	"github.com/malikimayzar/mcp-gateway/internal/registry"
)

// stubRegistry memakai router PRODUCTION (bukan salinan), supaya test benar-benar
// menguji wiring yang berjalan di server.
//
// Bentuk data stub sengaja mengikuti schema backend yang nyata:
//
//	search_arxiv    → answer + sources[].text/score/arxiv_id  (arxiv-research-assistant)
//	research_rag    → answer + retrieved_chunks[].text/score   (rag-research)
//	evaluate_answer → faithfulness_score + failure_mode       (llm-eval-framework)
//
// Stub lama memakai key "chunks" dan "faithfulness", sedangkan executor membaca
// "results" dan "faithfulness_score" — akibatnya evaluate selalu dilewati dan
// score selalu 0, jadi assertion lama tidak membuktikan apa pun.
func newTestRegistry() *registry.Registry {
	reg := registry.New()

	reg.Register(toolSearchArxiv, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName,
			TraceID:  req.TraceID,
			Success:  true,
			Data: map[string]interface{}{
				"answer": "RAG menggabungkan retrieval dengan generation.",
				"sources": []interface{}{
					map[string]interface{}{"text": "chunk A", "score": 0.82, "arxiv_id": "2101.00001"},
					map[string]interface{}{"text": "chunk B", "arxiv_id": "2101.00002"},
				},
				"model":         "phi3:mini",
				"retrieval_ms":  120,
				"generation_ms": 3400,
			},
		}
	})

	reg.Register(toolResearchRAG, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName,
			TraceID:  req.TraceID,
			Success:  true,
			Data: map[string]interface{}{
				"answer": "RAG adalah sistem yang menggabungkan retrieval dengan generation.",
				"retrieved_chunks": []interface{}{
					map[string]interface{}{"chunk_id": "c1", "doc_id": "d1", "text": "chunk A", "score": 0.82},
					map[string]interface{}{"chunk_id": "c2", "doc_id": "d2", "text": "chunk B", "score": 0.70},
				},
				"model":                 "phi3:mini",
				"confidence":            0.85,
				"decision":              "GENERATE",
				"latency_retrieval_ms":  120,
				"latency_generation_ms": 3400,
				"latency_total_ms":      3520,
			},
		}
	})

	reg.Register(toolRetrieveChunks, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName,
			TraceID:  req.TraceID,
			Success:  true,
			Data: map[string]interface{}{
				"results": []interface{}{map[string]interface{}{"text": "chunk lokal"}},
			},
		}
	})

	reg.Register(toolEvaluateAnswer, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName,
			TraceID:  req.TraceID,
			Success:  true,
			Data: map[string]interface{}{
				"faithfulness_score": 0.86,
				"failure_mode":       "none",
			},
		}
	})

	return reg
}

// newTestServer menyalakan router production di atas stub registry.
func newTestServer(reg *registry.Registry) *httptest.Server {
	return httptest.NewServer(newRouter(reg))
}
