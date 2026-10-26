package mcp

import (
	"context"
	"fmt"

	"github.com/malikimayzar/mcp-gateway/internal/config"
	"github.com/malikimayzar/mcp-gateway/internal/registry"
	"github.com/malikimayzar/mcp-gateway/internal/tools"
)

// RegisterGatewayTools registers gateway tools as MCP tools
func RegisterGatewayTools(server *Server, cfg config.Config) {
	// Tool 1: search_scientific_literature → maps to search_arxiv
	server.registry.RegisterTool(Tool{
		Name:        "search_scientific_literature",
		Description: "Search arXiv for scientific papers based on a query",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search query for arXiv papers",
				},
				"top_k": map[string]interface{}{
					"type":        "integer",
					"description": "Number of results to return (1-20)",
					"minimum":     1,
					"maximum":     20,
				},
			},
			"required": []string{"query"},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			// Get trace ID from context
			traceID := GetTraceID(ctx)
			
			// Convert to registry.ToolRequest
			req := registryToolRequest("search_arxiv", params, traceID)
			
			// Call existing search_arxiv tool
			handler := tools.NewSearchArxiv(cfg.Arxiv)
			resp := handler(ctx, req)
			
			if !resp.Success {
				return nil, fmt.Errorf("search_arxiv failed: %s", resp.Error)
			}
			
			return resp.Data, nil
		},
	})

	// Tool 2: verify_factual_claim → maps to research_rag
	server.registry.RegisterTool(Tool{
		Name:        "verify_factual_claim",
		Description: "Verify a factual claim against the RAG corpus with span-level attribution",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Claim or question to verify",
				},
				"top_k": map[string]interface{}{
					"type":        "integer",
					"description": "Number of evidence chunks to retrieve (1-20)",
					"minimum":     1,
					"maximum":     20,
				},
				"use_reranker": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether to use cross-encoder reranking",
				},
			},
			"required": []string{"query"},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			traceID := GetTraceID(ctx)
			req := registryToolRequest("research_rag", params, traceID)
			handler := tools.NewResearchRAG(cfg.RAG)
			resp := handler(ctx, req)
			if !resp.Success {
				return nil, fmt.Errorf("research_rag failed: %s", resp.Error)
			}
			if resp.Data != nil && len(resp.Data) > 0 {
				if attributions, ok := resp.Data["claim_attributions"]; ok {
					resp.Data["span_level_attribution"] = attributions
				}
			}
			return resp.Data, nil
		},
	})

	// Tool 3: generate_audit_dossier → maps to evaluate_answer
	server.registry.RegisterTool(Tool{
		Name:        "generate_audit_dossier",
		Description: "Generate an audit dossier by evaluating an answer against evidence",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"question": map[string]interface{}{
					"type":        "string",
					"description": "Original question or claim",
				},
				"answer": map[string]interface{}{
					"type":        "string",
					"description": "Answer to evaluate",
				},
				"context": map[string]interface{}{
					"type":        "string",
					"description": "Evidence context used to generate the answer",
				},
			},
			"required": []string{"question", "answer", "context"},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			// Get trace ID from context
			traceID := GetTraceID(ctx)
			
			// Convert to registry.ToolRequest
			req := registryToolRequest("evaluate_answer", params, traceID)
			
			// Call existing evaluate_answer tool
			handler := tools.NewEvaluateAnswer(cfg.Evaluator)
			resp := handler(ctx, req)
			
			if !resp.Success {
				return nil, fmt.Errorf("evaluate_answer failed: %s", resp.Error)
			}
			
			return resp.Data, nil
		},
	})
}

// registryToolRequest converts MCP params to registry.ToolRequest
func registryToolRequest(toolName string, params map[string]interface{}, traceID string) registry.ToolRequest {
	// This is a simplified conversion - in production, you'd want proper type handling
	// For now, we'll pass the params directly since the existing tools already handle map[string]interface{}
	return registry.ToolRequest{
		ToolName: toolName,
		TraceID:  traceID,
		Params:   params,
	}
}
