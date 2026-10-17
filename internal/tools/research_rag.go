package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/malikimayzar/mcp-gateway/internal/config"
	"github.com/malikimayzar/mcp-gateway/internal/registry"
)

const maxRAGResponseBytes = config.DefaultRAGMaxResponseBytes

// NewResearchRAG returns an adapter bound to startup configuration.
func NewResearchRAG(service config.ServiceConfig) registry.HandlerFunc {
	return func(ctx context.Context, req registry.ToolRequest) registry.ToolResponse {
		return researchRAG(ctx, req, service)
	}
}

// ResearchRAG is retained for direct callers. The production registry uses
// NewResearchRAG so configuration is read once during startup.
func ResearchRAG(ctx context.Context, req registry.ToolRequest) registry.ToolResponse {
	cfg, err := config.Load()
	if err != nil {
		return errorResponse(req, fmt.Sprintf("invalid runtime configuration: %v", err))
	}
	return researchRAG(ctx, req, cfg.RAG)
}

func researchRAG(ctx context.Context, req registry.ToolRequest, service config.ServiceConfig) registry.ToolResponse {
	query, ok := req.Params["query"].(string)
	query = strings.TrimSpace(query)
	if !ok || query == "" {
		return errorResponse(req, "param query is required and must be a non-empty string")
	}
	if utf8.RuneCountInString(query) > 1000 {
		return errorResponse(req, "param query must be at most 1000 characters")
	}

	topK := 5
	if value, exists := req.Params["top_k"]; exists {
		switch v := value.(type) {
		case float64:
			if v != float64(int(v)) {
				return errorResponse(req, "param top_k must be an integer between 1 and 20")
			}
			topK = int(v)
		case int:
			topK = v
		default:
			return errorResponse(req, "param top_k must be an integer between 1 and 20")
		}
		if topK < 1 || topK > 20 {
			return errorResponse(req, "param top_k must be an integer between 1 and 20")
		}
	}

	useReranker := false
	if value, exists := req.Params["use_reranker"]; exists {
		switch v := value.(type) {
		case bool:
			useReranker = v
		default:
			return errorResponse(req, "param use_reranker must be a boolean")
		}
	}

	body, err := json.Marshal(ResearchRAGRequest{Query: query, TopK: topK, UseReranker: useReranker})
	if err != nil {
		return errorResponse(req, fmt.Sprintf("failed to encode request: %v", err))
	}

	ctx, cancel := context.WithTimeout(ctx, service.Timeout)
	defer cancel()

	endpoint := strings.TrimRight(service.BaseURL, "/") + "/generate"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(body))
	if err != nil {
		return errorResponse(req, fmt.Sprintf("failed to build request: %v", err))
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return errorResponse(req, fmt.Sprintf("research_rag call failed: %v", err))
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, service.MaxResponseBytes+1))
	if err != nil {
		return errorResponse(req, fmt.Sprintf("failed to read research_rag response: %v", err))
	}
	if int64(len(respBody)) > service.MaxResponseBytes {
		return errorResponse(req, fmt.Sprintf("research_rag response exceeds %d bytes", service.MaxResponseBytes))
	}
	if resp.StatusCode != 200 {
		return errorResponse(req, fmt.Sprintf("research_rag returned %d: %s", resp.StatusCode, string(respBody)))
	}

	if err := validateResearchRAGResponse(respBody); err != nil {
		return errorResponse(req, fmt.Sprintf("malformed research_rag response: %v", err))
	}
	var result ResearchRAGResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return errorResponse(req, fmt.Sprintf("malformed research_rag response: %v", err))
	}

	return registry.ToolResponse{
		ToolName:    req.ToolName,
		TraceID:     req.TraceID,
		Success:     true,
		Data:        result.ToolData(),
		TypedResult: result,
	}
}
