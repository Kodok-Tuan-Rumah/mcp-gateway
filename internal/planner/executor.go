package planner

import (
	"context"
	"strings"

	"github.com/malikimayzar/mcp-gateway/internal/api"
	"github.com/malikimayzar/mcp-gateway/internal/registry"
)

// ExecutionResult adalah record internal eksekusi plan.
//
// BUKAN bentuk JSON publik. Respons publik adalah api.GatewayResponse
// (internal/api/contract.go) — lihat docs/api-contract.md. Field di sini masih
// PascalCase; jawaban dan metadata backend dipetakan ke kontrak publik melalui
// api.GatewayResponse sebelum dikirim.
type ExecutionResult struct {
	Query     string
	Steps     []StepResult
	Answer    string
	Context   string
	Evidence  []api.Evidence
	Model     string
	Latency   *api.StageLatency
	Score     float64
	Abstained bool
}

type StepResult struct {
	ToolName string
	Success  bool
	Skipped  bool
	Error    string
	Data     map[string]interface{}
}

func Execute(ctx context.Context, reg *registry.Registry, plan Plan) ExecutionResult {
	result := ExecutionResult{Query: plan.Query}
	var answer string
	var contextStr string

	for _, step := range plan.Steps {
		req := registry.ToolRequest{
			ToolName: step.ToolName,
			Params:   step.Params,
			TraceID:  plan.TraceID,
		}

		// inject context dari step sebelumnya ke evaluate_answer
		if step.ToolName == "evaluate_answer" {
			if answer == "" || contextStr == "" {
				result.Steps = append(result.Steps, StepResult{
					ToolName: step.ToolName,
					Success:  false,
					Skipped:  true,
					Error:    "skipped: no answer or context available",
				})
				continue
			}
			switch {
			case result.Abstained, strings.TrimSpace(answer) == api.ArxivAbstainText:
				result.Steps = append(result.Steps, StepResult{
					ToolName: step.ToolName,
					Success:  false,
					Skipped:  true,
					Error:    "skipped: backend abstained",
				})
				continue
			}
			if req.Params == nil {
				req.Params = make(map[string]interface{})
			}
			req.Params["answer"] = answer
			req.Params["context"] = contextStr
			req.Params["question"] = plan.Query
		}

		resp := reg.Execute(ctx, req)
		stepResult := StepResult{
			ToolName: step.ToolName,
			Success:  resp.Success,
			Error:    resp.Error,
			Data:     resp.Data,
		}
		result.Steps = append(result.Steps, stepResult)

		if !resp.Success {
			continue
		}

		// extract answer dan context dari hasil tool
		// search_arxiv dan research_rag sama-sama menghasilkan jawaban akhir;
		// keduanya punya kontrak titik-ketik yang sama (AnswerText/ModelName/
		// Evidence/Latency/IsAbstention).
		if step.ToolName == "search_arxiv" || step.ToolName == "research_rag" {
			if typed, ok := resp.TypedResult.(interface {
				AnswerText() string
				ModelName() string
				Evidence() []api.Evidence
				Latency() *api.StageLatency
				IsAbstention() bool
			}); ok {
				answer = typed.AnswerText()
				result.Model = typed.ModelName()
				result.Evidence = typed.Evidence()
				result.Latency = typed.Latency()
				result.Abstained = typed.IsAbstention()
			} else {
				// Keep compatibility with registry handlers that still return the
				// wire-shaped Data map, while the production adapter passes its
				// typed response through TypedResult.
				if a, ok := resp.Data["answer"].(string); ok {
					answer = a
				}
				if model, ok := resp.Data["model"].(string); ok {
					result.Model = model
				}
				result.Evidence = evidenceFromToolData(resp.Data)
				// Deteksi abstensi sama dengan ResearchRAGResponse.IsAbstention:
				// decision REJECT (kontrak /ask) atau framing /generate
				// (sentinel jawaban + failure_type insufficient_context).
				if decision, ok := resp.Data["decision"].(string); ok && decision == "REJECT" {
					result.Abstained = true
				}
				if failure, ok := resp.Data["failure_type"].(string); ok && failure == api.FailureInsufficientContext {
					result.Abstained = true
				}
				if ans, ok := resp.Data["answer"].(string); ok && ans == api.RAGAbstainText {
					result.Abstained = true
				}
			}
			contextStr = evidenceContext(result.Evidence)
		}

		if step.ToolName == "evaluate_answer" {
			if score, ok := resp.Data["faithfulness_score"].(float64); ok {
				result.Score = score
			}
		}
	}

	result.Answer = answer
	result.Context = contextStr
	return result
}

func evidenceContext(evidence []api.Evidence) string {
	parts := make([]string, 0, len(evidence))
	for _, item := range evidence {
		if item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, " ")
}

func evidenceFromToolData(data map[string]interface{}) []api.Evidence {
	sources, ok := data["sources"].([]interface{})
	if !ok {
		return nil
	}
	evidence := make([]api.Evidence, 0, len(sources))
	for _, source := range sources {
		fields, ok := source.(map[string]interface{})
		if !ok {
			continue
		}
		text, ok := fields["text"].(string)
		if !ok {
			continue
		}
		item := api.Evidence{Text: text}
		if sourceID, ok := fields["arxiv_id"].(string); ok {
			item.SourceID = sourceID
		}
		if score, ok := fields["score"].(float64); ok {
			item.Score = &score
		}
		evidence = append(evidence, item)
	}
	return evidence
}
