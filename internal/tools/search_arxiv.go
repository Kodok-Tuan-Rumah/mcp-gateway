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

const maxArxivResponseBytes = config.DefaultArxivMaxResponseBytes

func errorResponse(req registry.ToolRequest, msg string) registry.ToolResponse {
	return registry.ToolResponse{
		ToolName: req.ToolName,
		TraceID:  req.TraceID,
		Success:  false,
		Error:    msg,
	}
}

// NewSearchArxiv returns an adapter bound to startup configuration.
func NewSearchArxiv(service config.ServiceConfig) registry.HandlerFunc {
	return func(ctx context.Context, req registry.ToolRequest) registry.ToolResponse {
		return searchArxiv(ctx, req, service)
	}
}

// SearchArxiv is retained for direct callers. The production registry uses
// NewSearchArxiv so configuration is read once during startup.
func SearchArxiv(ctx context.Context, req registry.ToolRequest) registry.ToolResponse {
	cfg, err := config.Load()
	if err != nil {
		return errorResponse(req, fmt.Sprintf("invalid runtime configuration: %v", err))
	}
	return searchArxiv(ctx, req, cfg.Arxiv)
}

func searchArxiv(ctx context.Context, req registry.ToolRequest, service config.ServiceConfig) registry.ToolResponse {
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

	body, err := json.Marshal(ArxivQueryRequest{Query: query, TopK: topK})
	if err != nil {
		return errorResponse(req, fmt.Sprintf("failed to encode request: %v", err))
	}

	ctx, cancel := context.WithTimeout(ctx, service.Timeout)
	defer cancel()

	endpoint := strings.TrimRight(service.BaseURL, "/") + "/query"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(body))
	if err != nil {
		return errorResponse(req, fmt.Sprintf("failed to build request: %v", err))
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return errorResponse(req, fmt.Sprintf("search_arxiv call failed: %v", err))
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, service.MaxResponseBytes+1))
	if err != nil {
		return errorResponse(req, fmt.Sprintf("failed to read search_arxiv response: %v", err))
	}
	if int64(len(respBody)) > service.MaxResponseBytes {
		return errorResponse(req, fmt.Sprintf("search_arxiv response exceeds %d bytes", service.MaxResponseBytes))
	}
	if resp.StatusCode != 200 {
		return errorResponse(req, fmt.Sprintf("search_arxiv returned %d: %s", resp.StatusCode, string(respBody)))
	}

	if err := validateArxivResponse(respBody); err != nil {
		return errorResponse(req, fmt.Sprintf("malformed search_arxiv response: %v", err))
	}
	var result ArxivQueryResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return errorResponse(req, fmt.Sprintf("malformed search_arxiv response: %v", err))
	}

	return registry.ToolResponse{
		ToolName:    req.ToolName,
		TraceID:     req.TraceID,
		Success:     true,
		Data:        result.ToolData(),
		TypedResult: result,
	}
}
