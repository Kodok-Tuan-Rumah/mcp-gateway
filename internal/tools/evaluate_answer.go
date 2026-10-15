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

const maxEvaluateResponseBytes = config.DefaultEvaluatorMaxResponseSize

// NewEvaluateAnswer returns an adapter bound to startup configuration.
func NewEvaluateAnswer(service config.ServiceConfig) registry.HandlerFunc {
	return func(ctx context.Context, req registry.ToolRequest) registry.ToolResponse {
		return evaluateAnswer(ctx, req, service)
	}
}

// EvaluateAnswer is retained for direct callers. The production registry uses
// NewEvaluateAnswer so configuration is read once during startup.
func EvaluateAnswer(ctx context.Context, req registry.ToolRequest) registry.ToolResponse {
	cfg, err := config.Load()
	if err != nil {
		return errorResponse(req, fmt.Sprintf("invalid runtime configuration: %v", err))
	}
	return evaluateAnswer(ctx, req, cfg.Evaluator)
}

func evaluateAnswer(ctx context.Context, req registry.ToolRequest, service config.ServiceConfig) registry.ToolResponse {
	answer, ok := req.Params["answer"].(string)
	if !ok || answer == "" {
		return errorResponse(req, "param answer is required")
	}

	contextStr, ok := req.Params["context"].(string)
	if !ok || contextStr == "" {
		return errorResponse(req, "param context is required")
	}

	question, ok := req.Params["question"].(string)
	if !ok || question == "" {
		return errorResponse(req, "param question is required")
	}

	body, err := json.Marshal(EvaluateAnswerRequest{
		Answer: answer, Context: contextStr, Question: question,
	})
	if err != nil {
		return errorResponse(req, fmt.Sprintf("failed to encode evaluator request: %v", err))
	}

	// Beri evaluator batas waktu maksimum; deadline/cancel parent tetap berlaku.
	evalCtx, cancel := context.WithTimeout(ctx, service.Timeout)
	ctx = evalCtx
	defer cancel()

	endpoint := strings.TrimRight(service.BaseURL, "/") + "/evaluate"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(body))
	if err != nil {
		return errorResponse(req, fmt.Sprintf("failed to build request: %v", err))
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return errorResponse(req, fmt.Sprintf("evaluate_answer call failed: %v", err))
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, service.MaxResponseBytes+1))
	if err != nil {
		return errorResponse(req, fmt.Sprintf("failed to read evaluate_answer response: %v", err))
	}
	if int64(len(respBody)) > service.MaxResponseBytes {
		return errorResponse(req, fmt.Sprintf("evaluate_answer response exceeds %d bytes", service.MaxResponseBytes))
	}
	if resp.StatusCode != 200 {
		return errorResponse(req, fmt.Sprintf("evaluate_answer returned %d: %s", resp.StatusCode, string(respBody)))
	}

	if err := validateEvaluateAnswerResponse(respBody); err != nil {
		return errorResponse(req, fmt.Sprintf("malformed evaluate_answer response: %v", err))
	}
	var result EvaluateAnswerResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return errorResponse(req, fmt.Sprintf("malformed evaluate_answer response: %v", err))
	}

	return registry.ToolResponse{
		ToolName: req.ToolName,
		TraceID:  req.TraceID,
		Success:  true,
		Data:     result.ToolData(),
	}
}
