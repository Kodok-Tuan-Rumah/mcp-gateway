package main

import (
	"math"
	"strings"

	"github.com/malikimayzar/mcp-gateway/internal/api"
	"github.com/malikimayzar/mcp-gateway/internal/planner"
)

// evalSkippedPrefix menandai langkah evaluasi yang dilewati karena tidak ada
// jawaban atau konteks untuk dinilai.
const evalSkippedPrefix = "skipped:"

// toResponse memetakan hasil eksekusi ke kontrak publik.
//
// Prinsipnya: jawaban dan evidence dipetakan dari hasil backend yang sama.
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
	applyEvaluation(&resp, result)
	return resp
}

// applyDecision menentukan decision dan failure_type berdasarkan apa yang
// benar-benar terjadi selama eksekusi.
func applyDecision(resp *api.GatewayResponse, result planner.ExecutionResult) {
	answerStep := findAnswerStep(result.Steps, resp.Backend)

	switch {
	case answerStep == nil:
		// Langkah menjawab tidak ada sama sekali — tidak terjadi apa-apa.
		resp.Decision = api.DecisionError
		resp.FailureType = api.FailureBackendError

	case !answerStep.Success:
		// Backend gagal: tidak dijangkau, timeout, atau balas error.
		resp.Answer = ""
		resp.Decision = api.DecisionError
		resp.FailureType = classifyBackendError(answerStep.Error)

	case strings.TrimSpace(result.Answer) == "":
		// Response tanpa jawaban dianggap abstain; backend saat ini juga
		// memakai sentinel eksplisit untuk keadaan yang sama.
		resp.Answer = ""
		resp.Decision = api.DecisionAbstain
		resp.FailureType = api.FailureInsufficientContext

	case strings.TrimSpace(result.Answer) == api.ArxivAbstainText:
		// Abstensi backend dipertahankan apa adanya.
		resp.Decision = api.DecisionAbstain
		resp.FailureType = api.FailureInsufficientContext

	default:
		resp.Decision = api.DecisionAnswer
		resp.FailureType = api.FailureNone
	}
}

// findAnswerStep menemukan langkah yang menghasilkan jawaban untuk backend
// tertentu. Setiap backend punya tool jawabannya sendiri (search_arxiv untuk
// arxiv, research_rag untuk research_rag), jadi kita harus mencari tool yang
// benar bukan selalu search_arxiv.
func findAnswerStep(steps []planner.StepResult, backend string) *planner.StepResult {
	answerTool := answerToolForBackend(backend)
	for i := range steps {
		if steps[i].ToolName == answerTool {
			return &steps[i]
		}
	}
	return nil
}

// answerToolForBackend mengembalikan nama tool yang menghasilkan jawaban untuk
// backend tertentu.
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

// applyEvaluation mengisi evaluation_status. Status ini selalu ada dan tidak
// pernah disimpulkan dari score: score 0 yang sah dan "tidak dinilai" adalah
// dua keadaan yang berbeda.
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
}

// evalScore membaca skor dari data langkah evaluasi. Mengembalikan ok=false
// bila tidak ada atau tipenya salah — supaya tidak pernah diam-diam jadi 0.
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

// classifyBackendError memetakan pesan error langkah ke failure_type yang
// lebih berguna daripada satu kategori "error".
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

// findStep mencari hasil satu tool di dalam daftar langkah.
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
