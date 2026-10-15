package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

type ToolCall struct {
	ToolName string                 `json:"tool_name"`
	Params   map[string]interface{} `json:"params"`
	Reason   string                 `json:"reason"`
}

type Plan struct {
	Steps  []ToolCall `json:"steps"`
	Answer string     `json:"answer"`
}

var systemPrompt = `You are an AI research assistant orchestrator. Given a user query, select the available ArXiv answer tool.

Available tools:
1. search_arxiv — search ArXiv papers and return an answer with its sources.
2. evaluate_answer — evaluate the answer against the context returned by search_arxiv. If selected, it must be the last step.

Do not select retrieve_chunks or any other tool. The gateway currently serves only the ArXiv backend on this route.

Respond ONLY with valid JSON (no markdown, no code blocks) in this exact format:
{
  "steps": [
    {"tool_name": "search_arxiv", "params": {"query": "...", "top_k": 5}, "reason": "..."},
    {"tool_name": "evaluate_answer", "params": {}, "reason": "evaluate faithfulness"}
  ],
  "answer": ""
}`

func extractJSON(raw string) string {
	raw = strings.TrimSpace(raw)

	// Cari { pertama dan } terakhir — works untuk plain JSON maupun wrapped markdown
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start != -1 && end != -1 && end > start {
		return strings.TrimSpace(raw[start : end+1])
	}
	return raw
}

// GeneratePlan meminta rencana tool dari Groq.
//
// apiKey dan model diberikan pemanggil supaya orchestrator tidak membaca
// environment variable sendiri dan supaya jalur fallback bisa diuji tanpa
// kunci sungguhan.
//
// Model sengaja dikonfigurasi, bukan ditulis di sini: Groq mencabut model dari
// daftar akses secara berkala, dan nama yang hilang membuat setiap panggilan
// gagal dengan model_not_found, dan kegagalan itu hilang di balik fallback
// yang terlihat normal.
func GeneratePlan(ctx context.Context, query, apiKey, model string) (Plan, error) {
	if apiKey == "" {
		return Plan{}, fmt.Errorf("GROQ_API_KEY not set")
	}
	if model == "" {
		return Plan{}, fmt.Errorf("GROQ_MODEL not set")
	}

	config := openai.DefaultConfig(apiKey)
	config.BaseURL = "https://api.groq.com/openai/v1"
	client := openai.NewClientWithConfig(config)

	log.Printf("[groq] calling Groq model %s for query: %q", model, query)

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: query},
		},
		Temperature: 0.1,
	})
	if err != nil {
		log.Printf("[groq] API call failed: %v", err)
		return Plan{}, fmt.Errorf("groq call failed with model %s: %w", model, err)
	}

	if len(resp.Choices) == 0 {
		return Plan{}, fmt.Errorf("groq returned no completion choices")
	}
	raw := resp.Choices[0].Message.Content
	log.Printf("[groq] raw response: %s", raw)

	cleaned := extractJSON(raw)
	log.Printf("[groq] cleaned JSON: %s", cleaned)

	var plan Plan
	if err := json.Unmarshal([]byte(cleaned), &plan); err != nil {
		log.Printf("[groq] JSON parse failed: %v", err)
		return Plan{}, fmt.Errorf("failed to parse groq response: %w\nraw: %s", err, raw)
	}

	log.Printf("[groq] plan parsed OK — %d steps", len(plan.Steps))
	for i, step := range plan.Steps {
		log.Printf("[groq]   step[%d]: tool=%s reason=%q", i, step.ToolName, step.Reason)
	}

	return plan, nil
}
