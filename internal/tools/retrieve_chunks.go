package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/malikimayzar/mcp-gateway/internal/config"
	"github.com/malikimayzar/mcp-gateway/internal/registry"
)

// NewRetrieveChunks returns the legacy retrieval adapter bound to startup
// configuration. It remains available only to the low-level /tool endpoint.
func NewRetrieveChunks(service config.ServiceConfig) registry.HandlerFunc {
	return func(ctx context.Context, req registry.ToolRequest) registry.ToolResponse {
		return retrieveChunks(ctx, req, service)
	}
}

// RetrieveChunks is retained for direct callers. The production registry uses
// NewRetrieveChunks so configuration is read once during startup.
func RetrieveChunks(ctx context.Context, req registry.ToolRequest) registry.ToolResponse {
	cfg, err := config.Load()
	if err != nil {
		return errorResponse(req, fmt.Sprintf("invalid runtime configuration: %v", err))
	}
	return retrieveChunks(ctx, req, cfg.RAG)
}

func retrieveChunks(ctx context.Context, req registry.ToolRequest, service config.ServiceConfig) registry.ToolResponse {
	query, ok := req.Params["query"].(string)
	if !ok || query == "" {
		return errorResponse(req, "param query is required")
	}

	topK := 5
	if v, ok := req.Params["top_k"].(float64); ok {
		topK = int(v)
	}

	method := "bm25"
	if v, ok := req.Params["method"].(string); ok && v != "" {
		method = v
	}

	body, _ := json.Marshal(map[string]interface{}{
		"query":  query,
		"top_k":  topK,
		"method": method,
	})

	ctx, cancel := context.WithTimeout(ctx, service.Timeout)
	defer cancel()

	endpoint := strings.TrimRight(service.BaseURL, "/") + "/retrieve"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(body))
	if err != nil {
		return errorResponse(req, fmt.Sprintf("failed to build request: %v", err))
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return errorResponse(req, fmt.Sprintf("retrieve_chunks call failed: %v", err))
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, service.MaxResponseBytes+1))
	if err != nil {
		return errorResponse(req, fmt.Sprintf("failed to read retrieve_chunks response: %v", err))
	}
	if int64(len(respBody)) > service.MaxResponseBytes {
		return errorResponse(req, fmt.Sprintf("retrieve_chunks response exceeds %d bytes", service.MaxResponseBytes))
	}
	if resp.StatusCode != 200 {
		return errorResponse(req, fmt.Sprintf("retrieve_chunks returned %d: %s", resp.StatusCode, string(respBody)))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return errorResponse(req, fmt.Sprintf("failed to parse response: %v", err))
	}

	return registry.ToolResponse{
		ToolName: req.ToolName,
		TraceID:  req.TraceID,
		Success:  true,
		Data:     result,
	}
}
