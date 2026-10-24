package main

import (
	"testing"

	"github.com/malikimayzar/mcp-gateway/internal/api"
	"github.com/malikimayzar/mcp-gateway/internal/planner"
)

func askReq() api.AskRequest {
	return api.AskRequest{Backend: api.BackendArxiv, Query: "apa itu RAG?", TopK: 5}
}

// Deterministic plans make the selected backend call and then evaluate its answer.
func TestPlanForBackend_ArxivBuildsAnswerAndEvaluation(t *testing.T) {
	req := askReq()
	plan := planForBackend(req)

	if plan.Query != req.Query || len(plan.Steps) != 2 {
		t.Fatalf("plan = %+v, want query and two steps", plan)
	}
	if plan.Steps[0].ToolName != toolSearchArxiv || plan.Steps[1].ToolName != toolEvaluateAnswer {
		t.Fatalf("steps = %+v", plan.Steps)
	}
	if plan.Steps[0].Params["query"] != req.Query || plan.Steps[0].Params["top_k"] != float64(req.TopK) {
		t.Errorf("search params = %v", plan.Steps[0].Params)
	}
}

func TestPlanForBackend_ResearchRAGBuildsAnswerAndEvaluation(t *testing.T) {
	req := askReq()
	req.Backend = api.BackendResearchRAG
	plan := planForBackend(req)

	if plan.Query != req.Query || len(plan.Steps) != 2 {
		t.Fatalf("plan = %+v, want query and two steps", plan)
	}
	answer := plan.Steps[0]
	if answer.ToolName != toolResearchRAG {
		t.Fatalf("answer tool = %q", answer.ToolName)
	}
	if answer.Params["query"] != req.Query || answer.Params["top_k"] != float64(req.TopK) || answer.Params["use_reranker"] != false {
		t.Errorf("research_rag params = %v", answer.Params)
	}
	if plan.Steps[1].ToolName != toolEvaluateAnswer {
		t.Errorf("last step = %q, want %q", plan.Steps[1].ToolName, toolEvaluateAnswer)
	}
}

func TestPlanForBackend_UnknownBackendIsEmpty(t *testing.T) {
	req := askReq()
	req.Backend = "unknown"
	if plan := planForBackend(req); len(plan.Steps) != 0 {
		t.Errorf("unknown backend plan = %+v", plan)
	}
}

func execResult(answer string, steps ...planner.StepResult) planner.ExecutionResult {
	return planner.ExecutionResult{Query: "apa itu RAG?", Answer: answer, Steps: steps}
}

func okStep(tool string, data map[string]interface{}) planner.StepResult {
	return planner.StepResult{ToolName: tool, Success: true, Data: data}
}

func failStep(tool, errMsg string) planner.StepResult {
	return planner.StepResult{ToolName: tool, Success: false, Error: errMsg}
}

func TestToResponse_NormalAnswer(t *testing.T) {
	result := execResult("jawaban sah",
		okStep(toolSearchArxiv, map[string]interface{}{"answer": "jawaban sah"}),
		okStep(toolEvaluateAnswer, map[string]interface{}{"faithfulness_score": 0.86, "failure_mode": "none"}),
	)

	resp := toResponse(askReq(), "req-1", orchestratorRuleBased, result)

	if resp.Decision != api.DecisionAnswer {
		t.Errorf("decision: %v", resp.Decision)
	}
	if resp.FailureType != api.FailureNone {
		t.Errorf("failure_type: %v", resp.FailureType)
	}
	if resp.EvaluationStatus != api.EvalCompleted {
		t.Errorf("evaluation_status: %v", resp.EvaluationStatus)
	}
	if resp.Evaluation == nil || resp.Evaluation.Score == nil {
		t.Fatal("score harus terisi")
	}
	if *resp.Evaluation.Score != 0.86 {
		t.Errorf("score: %v", *resp.Evaluation.Score)
	}
	if resp.RequestID != "req-1" {
		t.Errorf("request_id: %v", resp.RequestID)
	}
}

func TestToResponse_BackendFailure(t *testing.T) {
	result := execResult("", failStep(toolSearchArxiv, `search_arxiv call failed: no such host`))

	resp := toResponse(askReq(), "req-1", orchestratorRuleBased, result)

	if resp.Decision != api.DecisionError {
		t.Errorf("decision harus error, got %v", resp.Decision)
	}
	if resp.FailureType != api.FailureBackendError {
		t.Errorf("failure_type: %v", resp.FailureType)
	}
	if resp.Answer != "" {
		t.Errorf("answer harus kosong, got %q", resp.Answer)
	}
}

func TestToResponse_Abstention(t *testing.T) {
	result := execResult(api.ArxivAbstainText,
		okStep(toolSearchArxiv, map[string]interface{}{"answer": api.ArxivAbstainText}),
	)

	resp := toResponse(askReq(), "req-1", orchestratorRuleBased, result)

	if resp.Decision != api.DecisionAbstain {
		t.Errorf("decision harus abstain, got %v", resp.Decision)
	}
	if resp.FailureType != api.FailureInsufficientContext {
		t.Errorf("failure_type: %v", resp.FailureType)
	}
}

func TestToResponse_EmptyAnswerIsAbstention(t *testing.T) {
	result := execResult("   ", okStep(toolSearchArxiv, map[string]interface{}{}))

	resp := toResponse(askReq(), "req-1", orchestratorRuleBased, result)

	if resp.Decision != api.DecisionAbstain {
		t.Errorf("jawaban kosong harus jadi abstensi, bukan answer; got %v", resp.Decision)
	}
}

func TestToResponse_UnsupportedClaimsPreservesReflectionMetadata(t *testing.T) {
	passed := false
	score := 0.5
	unsupported := 2
	req := askReq()
	req.Backend = api.BackendResearchRAG
	result := execResult(api.RAGAbstainText,
		okStep(toolResearchRAG, map[string]interface{}{
			"answer":                   api.RAGAbstainText,
			"failure_type":             api.FailureUnsupportedClaims,
			"reflection_passed":        passed,
			"reflection_score":         score,
			"unsupported_claims_count": unsupported,
		}),
	)
	result.Abstained = true

	resp := toResponse(req, "req-1", orchestratorRuleBased, result)

	if resp.Decision != api.DecisionAbstain {
		t.Errorf("decision: got %q", resp.Decision)
	}
	if resp.FailureType != api.FailureUnsupportedClaims {
		t.Errorf("failure_type: got %q", resp.FailureType)
	}
	if resp.ReflectionPassed == nil || *resp.ReflectionPassed {
		t.Errorf("reflection_passed: got %v, want false", resp.ReflectionPassed)
	}
	if resp.ReflectionScore == nil || *resp.ReflectionScore != score {
		t.Errorf("reflection_score: got %v, want %v", resp.ReflectionScore, score)
	}
	if resp.UnsupportedClaimsCount == nil || *resp.UnsupportedClaimsCount != unsupported {
		t.Errorf("unsupported_claims_count: got %v, want %d", resp.UnsupportedClaimsCount, unsupported)
	}
}

// Gagalnya evaluator tidak boleh mengubah status jawaban maupun failure_type.
func TestToResponse_EvaluatorFailureKeepsAnswer(t *testing.T) {
	result := execResult("jawaban sah",
		okStep(toolSearchArxiv, map[string]interface{}{"answer": "jawaban sah"}),
		failStep(toolEvaluateAnswer, "evaluate_answer call failed: no such host"),
	)

	resp := toResponse(askReq(), "req-1", orchestratorRuleBased, result)

	if resp.Decision != api.DecisionAnswer {
		t.Errorf("decision harus tetap answer, got %v", resp.Decision)
	}
	if resp.FailureType != api.FailureNone {
		t.Errorf("failure_type harus none; kegagalan evaluator bukan kegagalan jawaban, got %v", resp.FailureType)
	}
	if resp.EvaluationStatus != api.EvalUnavailable {
		t.Errorf("evaluation_status: %v", resp.EvaluationStatus)
	}
	if resp.Evaluation != nil && resp.Evaluation.Score != nil {
		t.Error("score tidak boleh ada saat evaluator gagal")
	}
}

func TestToResponse_EvalScoreZeroIsValid(t *testing.T) {
	result := execResult("a",
		okStep(toolSearchArxiv, map[string]interface{}{}),
		okStep(toolEvaluateAnswer, map[string]interface{}{"faithfulness_score": float64(0)}),
	)

	resp := toResponse(askReq(), "req-1", orchestratorRuleBased, result)

	if resp.EvaluationStatus != api.EvalCompleted {
		t.Errorf("skor 0 yang sah harus completed, got %v", resp.EvaluationStatus)
	}
	if resp.Evaluation == nil || resp.Evaluation.Score == nil {
		t.Fatal("skor 0 harus tetap ada")
	}
	if *resp.Evaluation.Score != 0 {
		t.Errorf("score: %v", *resp.Evaluation.Score)
	}
}

// Skor dengan tipe salah tidak boleh diam-dimen jadi 0.
func TestToResponse_EvalScoreWrongTypeIsUnavailable(t *testing.T) {
	result := execResult("a",
		okStep(toolSearchArxiv, map[string]interface{}{}),
		okStep(toolEvaluateAnswer, map[string]interface{}{"faithfulness_score": "0.86"}),
	)

	resp := toResponse(askReq(), "req-1", orchestratorRuleBased, result)

	if resp.EvaluationStatus != api.EvalUnavailable {
		t.Errorf("tipe salah harus unavailable, bukan completed; got %v", resp.EvaluationStatus)
	}
}

func TestToResponse_EvalStepMissingIsSkipped(t *testing.T) {
	resp := toResponse(askReq(), "req-1", orchestratorRuleBased,
		execResult("a", okStep(toolSearchArxiv, map[string]interface{}{})))

	if resp.EvaluationStatus != api.EvalSkipped {
		t.Errorf("langkah evaluasi absen harus skipped, got %v", resp.EvaluationStatus)
	}
}

func TestToResponse_EvalStepSkippedPrefix(t *testing.T) {
	result := execResult("a",
		okStep(toolSearchArxiv, map[string]interface{}{}),
		planner.StepResult{ToolName: toolEvaluateAnswer, Success: false, Error: "skipped: no answer or context available"},
	)

	resp := toResponse(askReq(), "req-1", orchestratorRuleBased, result)

	if resp.EvaluationStatus != api.EvalSkipped {
		t.Errorf("harus skipped, bukan unavailable; got %v", resp.EvaluationStatus)
	}
}

// Klasifikasi error backend harus membedakan penyebabnya.
func TestClassifyBackendError(t *testing.T) {
	cases := []struct {
		msg  string
		want string
	}{
		{"search_arxiv call failed: context deadline exceeded", api.FailureBackendTimeout},
		{"context canceled", api.FailureBackendTimeout},
		{"failed to parse response: unexpected end of json input", api.FailureMalformedResponse},
		{"search_arxiv returned 500: internal error", api.FailureBackendError},
		{"dial tcp: no such host", api.FailureBackendError},
	}
	for _, tc := range cases {
		if got := classifyBackendError(tc.msg); got != tc.want {
			t.Errorf("classify(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

func TestConvertSteps_EmptyIsNil(t *testing.T) {
	if convertSteps(nil) != nil {
		t.Error("daftar langkah kosong harus jadi nil supaya absen dari JSON")
	}
}
