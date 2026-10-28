package main

import (
	"math"
	"strings"

	"github.com/malikimayzar/mcp-gateway/internal/api"
	"github.com/malikimayzar/mcp-gateway/internal/planner"
)

const evalSkippedPrefix = "skipped:"

func toResponse(req api.AskRequest, requestID, orchestratorName string, result planner.ExecutionResult) api.GatewayResponse {
	resp := api.GatewayResponse{
		RequestID: requestID,
		Query:     req.Query,
		BackendAnswer: api.BackendAnswer{
			Backend:     req.Backend,
			Answer:      result.Answer,
			Evidence:    result.Evidence,
			Model:       result.Model,
			Latency:     result.Latency,
			Decision:    api.DecisionAnswer,
			FailureType: api.FailureNone,
		},
		Orchestrator: orchestratorName,
		Steps:        convertSteps(result.Steps),
	}

	applyDecision(&resp, result)
	applyReflectionMetadata(&resp, result)
	applyEvaluation(&resp, result)
	applyCalibrationMetadata(&resp, result)
	applySpanAttribution(&resp, result)
	return resp
}

func applyDecision(resp *api.GatewayResponse, result planner.ExecutionResult) {
	answerStep := findAnswerStep(result.Steps, resp.Backend)

	switch {
	case answerStep == nil:
		resp.Decision = api.DecisionError
		resp.FailureType = api.FailureBackendError

	case !answerStep.Success:
		resp.Answer = ""
		resp.Decision = api.DecisionError
		resp.FailureType = classifyBackendError(answerStep.Error)

	case result.FailureType == api.FailureProviderError ||
		result.FailureType == api.FailureSanityCheck ||
		result.FailureType == api.FailureEmptyOutput:
		resp.Answer = ""
		resp.Decision = api.DecisionError
		resp.FailureType = result.FailureType
		resp.ProviderErrorKind = result.ProviderErrorKind

	case strings.TrimSpace(result.Answer) == "":
		resp.Answer = ""
		resp.Decision = api.DecisionAbstain
		resp.FailureType = api.FailureInsufficientContext

	case strings.TrimSpace(result.Answer) == api.ArxivAbstainText:
		resp.Decision = api.DecisionAbstain
		resp.FailureType = api.FailureInsufficientContext

	case result.Abstained:
		resp.Decision = api.DecisionAbstain
		resp.FailureType = answerFailureType(answerStep)
		if resp.FailureType == api.FailureNone {
			resp.FailureType = api.FailureInsufficientContext
		}

	default:
		resp.Decision = api.DecisionAnswer
		resp.FailureType = api.FailureNone
	}
}

func answerFailureType(step *planner.StepResult) string {
	if step == nil || step.Data == nil {
		return api.FailureNone
	}
	if failure, ok := step.Data["failure_type"].(string); ok && failure != "" {
		return failure
	}
	return api.FailureNone
}

func applyReflectionMetadata(resp *api.GatewayResponse, result planner.ExecutionResult) {
	step := findAnswerStep(result.Steps, resp.Backend)
	if step == nil || step.Data == nil {
		return
	}
	if value, ok := step.Data["reflection_passed"].(bool); ok {
		resp.ReflectionPassed = &value
	}
	if value, ok := step.Data["reflection_score"].(float64); ok {
		resp.ReflectionScore = &value
	}
	if value, ok := step.Data["unsupported_claims_count"].(int); ok && value >= 0 {
		resp.UnsupportedClaimsCount = &value
	} else if value, ok := step.Data["unsupported_claims_count"].(float64); ok && value >= 0 && value == math.Trunc(value) {
		count := int(value)
		resp.UnsupportedClaimsCount = &count
	}
}

func applyCalibrationMetadata(resp *api.GatewayResponse, result planner.ExecutionResult) {
	step := findAnswerStep(result.Steps, resp.Backend)
	if step == nil || step.Data == nil {
		return
	}
	if v, ok := step.Data["calibrated_confidence"].(float64); ok {
		cc := v
		resp.CalibratedConfidence = &cc
	}
	if v, ok := step.Data["calibrated_decision"].(string); ok && v != "" {
		resp.CalibratedDecision = v
	}
}

func applySpanAttribution(resp *api.GatewayResponse, result planner.ExecutionResult) {
	step := findAnswerStep(result.Steps, resp.Backend)
	if step == nil || step.Data == nil {
		return
	}
	raw, ok := step.Data["span_level_attribution"]
	if !ok {
		raw = step.Data["claim_attributions"]
	}
	items, ok := raw.([]interface{})
	if !ok {
		return
	}
	out := make([]api.SpanAttribution, 0, len(items))
	for _, it := range items {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		sa := api.SpanAttribution{}
		if v, ok := m["claim_span"].(string); ok {
			sa.ClaimSpan = v
		} else if v, ok := m["claim_text"].(string); ok {
			sa.ClaimSpan = v
		}
		if v, ok := m["chunk_id"].(string); ok {
			sa.ChunkID = v
		}
		if v, ok := m["doc_id"].(string); ok {
			sa.DocID = v
		}
		if v, ok := m["text"].(string); ok {
			sa.Text = v
		} else if v, ok := m["evidence_text"].(string); ok {
			sa.Text = v
		}
		if v, ok := m["score"].(float64); ok {
			score := v
			sa.Score = &score
		}
		if v, ok := m["unverified"].(bool); ok {
			b := v
			sa.Unverified = &b
		}
		out = append(out, sa)
	}
	if len(out) > 0 {
		resp.SpanLevelAttribution = out
	}
}

func findAnswerStep(steps []planner.StepResult, backend string) *planner.StepResult {
	answerTool := answerToolForBackend(backend)
	for i := range steps {
		if steps[i].ToolName == answerTool {
			return &steps[i]
		}
	}
	return nil
}

func answerToolForBackend(backend string) string {
	switch backend {
	case api.BackendArxiv:
		return toolSearchArxiv
	case api.BackendResearchRAG:
		return toolResearchRAG
	default:
		return ""
	}
}

func applyEvaluation(resp *api.GatewayResponse, result planner.ExecutionResult) {
	step, found := findStep(result.Steps, toolEvaluateAnswer)

	if !found {
		resp.EvaluationStatus = api.EvalSkipped
		return
	}

	if step.Skipped || strings.HasPrefix(step.Error, evalSkippedPrefix) {
		resp.EvaluationStatus = api.EvalSkipped
		return
	}

	if !step.Success {
		// Evaluator tidak tersedia. failure_type TIDAK diubah: jawaban backend
		// tetap sah, yang gagal hanya evaluasinya. Keadaan itu sudah
		// terwakili oleh evaluation_status.
		resp.EvaluationStatus = api.EvalUnavailable
		return
	}

	score, ok := evalScore(step.Data)
	if !ok {
		// Backend evaluasi bilang sukses tapi tidak mengirim skor yang bisa
		// dipercaya. Itu "tidak tersedia", bukan 0.
		resp.EvaluationStatus = api.EvalUnavailable
		return
	}
	s := score
	resp.EvaluationStatus = api.EvalCompleted
	resp.Evaluation = &api.EvaluationResult{Score: &s}

	if fm, ok := step.Data["failure_mode"].(string); ok {
		switch fm {
		case "none", "unsupported_claims", "insufficient_context":
			resp.Evaluation.FailureMode = fm
		}
	}
	if version, ok := step.Data["evaluator_version"].(string); ok && strings.TrimSpace(version) != "" {
		resp.Evaluation.Version = version
	}
	// Evaluator reports these alongside the score; dropping them made the
	// public contract advertise fields that /ask and /plan never populated.
	if n, ok := claimCount(step.Data, "supported_claims"); ok {
		resp.Evaluation.SupportedClaims = n
	}
	if n, ok := claimCount(step.Data, "total_claims"); ok {
		resp.Evaluation.TotalClaims = n
	}
}
func claimCount(data map[string]interface{}, key string) (int, bool) {
	switch v := data[key].(type) {
	case int:
		if v >= 0 {
			return v, true
		}
	case float64:
		if v >= 0 && v == math.Trunc(v) && v <= 1_000_000 {
			return int(v), true
		}
	}
	return 0, false
}

func evalScore(data map[string]interface{}) (float64, bool) {
	if data == nil {
		return 0, false
	}
	v, ok := data["faithfulness_score"].(float64)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
		return 0, false
	}
	return v, true
}

func classifyBackendError(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "deadline exceeded"),
		strings.Contains(m, "timeout"),
		strings.Contains(m, "context canceled"):
		return api.FailureBackendTimeout
	case strings.Contains(m, "parse"),
		strings.Contains(m, "malformed"),
		strings.Contains(m, "unexpected end of json"):
		return api.FailureMalformedResponse
	default:
		return api.FailureBackendError
	}
}

func findStep(steps []planner.StepResult, tool string) (planner.StepResult, bool) {
	for _, s := range steps {
		if s.ToolName == tool {
			return s, true
		}
	}
	return planner.StepResult{}, false
}

func convertSteps(in []planner.StepResult) []api.StepResult {
	if len(in) == 0 {
		return nil
	}
	out := make([]api.StepResult, 0, len(in))
	for _, s := range in {
		out = append(out, api.StepResult{
			ToolName: s.ToolName,
			Success:  s.Success,
			Error:    s.Error,
		})
	}
	return out
}
