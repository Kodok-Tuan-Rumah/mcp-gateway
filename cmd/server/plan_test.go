package main

import (
	"testing"

	"github.com/malikimayzar/mcp-gateway/internal/api"
	"github.com/malikimayzar/mcp-gateway/internal/orchestrator"
	"github.com/malikimayzar/mcp-gateway/internal/planner"
)

func askReq() api.AskRequest {
	return api.AskRequest{Backend: api.BackendArxiv, Query: "apa itu RAG?", TopK: 5}
}

// ───────────────────── plan deterministik per backend ─────────────────────

// Plan default hanya boleh menyentuh search_arxiv + evaluate_answer.
// retrieve_chunks tidak boleh muncul di jalur jawaban pada v1.
func TestPlanForBackend_UsesOnlyBackendTools(t *testing.T) {
	plan := planForBackend(askReq())

	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(plan.Steps))
	}
	for _, step := range plan.Steps {
		if !toolAllowed(api.BackendArxiv, step.ToolName) {
			t.Errorf("step %q bukan tool milik backend %s", step.ToolName, api.BackendArxiv)
		}
	}
	if plan.Steps[0].ToolName != toolSearchArxiv {
		t.Errorf("langkah pertama harus search_arxiv, got %s", plan.Steps[0].ToolName)
	}
	if plan.Steps[1].ToolName != toolEvaluateAnswer {
		t.Errorf("langkah terakhir harus evaluate_answer, got %s", plan.Steps[1].ToolName)
	}
}

func TestPlanForBackend_QueryAndTopKPropagated(t *testing.T) {
	plan := planForBackend(askReq())

	if plan.Steps[0].Params["query"] != "apa itu RAG?" {
		t.Errorf("query tidak diteruskan: %v", plan.Steps[0].Params["query"])
	}
	if plan.Steps[0].Params["top_k"] != float64(5) {
		t.Errorf("top_k harus float64, got %T(%v)", plan.Steps[0].Params["top_k"], plan.Steps[0].Params["top_k"])
	}
}

// retrieve_chunks tidak boleh diizinkan di backend mana pun pada v1 —
// adapter-nya masih menembak endpoint yang belum ada.
func TestBackendTools_ExcludeRetrieveChunks(t *testing.T) {
	for backend, list := range backendTools {
		for _, tool := range list {
			if tool == toolRetrieveChunks {
				t.Errorf("backend %s tidak boleh memakai %s di v1", backend, tool)
			}
		}
	}
}

func TestToolsForBackend_UnknownBackendIsNil(t *testing.T) {
	if toolsForBackend("nonsense") != nil {
		t.Error("backend tak dikenal harus punya daftar tool kosong")
	}
}

// ─────────────────────── plan dari Groq dibatasi ───────────────────────

// Groq boleh menyusun langkah dari backend terpilih, tapi tidak boleh memilih backend.
func TestPlanFromGroq_DropsForeignTools(t *testing.T) {
	orcPlan := orchestrator.Plan{Steps: []orchestrator.ToolCall{
		{ToolName: toolSearchArxiv, Params: map[string]interface{}{}},
		{ToolName: toolRetrieveChunks, Params: map[string]interface{}{}}, // tidak diizinkan
		{ToolName: "search_papers", Params: map[string]interface{}{}},    // tidak dikenal
	}}

	plan := planFromGroq(orcPlan, askReq())

	for _, step := range plan.Steps {
		if step.ToolName == toolRetrieveChunks {
			t.Error("retrieve_chunks harus dibuang")
		}
		if step.ToolName == "search_papers" {
			t.Error("tool yang tidak dikenal harus dibuang")
		}
	}
	if len(plan.Steps) != 1 || plan.Steps[0].ToolName != toolSearchArxiv {
		t.Errorf("expected hanya search_arxiv, got %+v", plan.Steps)
	}
}

// Plan Groq tanpa langkah pembawa jawaban harus ditolak supaya pemanggil
// jatuh ke plan deterministik.
func TestPlanFromGroq_EmptyWhenNoAnswerStep(t *testing.T) {
	orcPlan := orchestrator.Plan{Steps: []orchestrator.ToolCall{
		{ToolName: toolEvaluateAnswer, Params: map[string]interface{}{}},
	}}

	if plan := planFromGroq(orcPlan, askReq()); len(plan.Steps) != 0 {
		t.Errorf("plan tanpa langkah menjawab harus kosong, got %+v", plan.Steps)
	}
}

func TestPlanFromGroq_FillsMissingQueryAndTopK(t *testing.T) {
	orcPlan := orchestrator.Plan{Steps: []orchestrator.ToolCall{
		{ToolName: toolSearchArxiv, Params: map[string]interface{}{}},
	}}

	plan := planFromGroq(orcPlan, askReq())
	step := plan.Steps[0]

	if step.Params["query"] != "apa itu RAG?" {
		t.Errorf("query harus diisi dari request, got %v", step.Params["query"])
	}
	if step.Params["top_k"] != float64(5) {
		t.Errorf("top_k harus diisi dari request, got %v", step.Params["top_k"])
	}
}

// Params dari LLM tidak boleh bocor ke plan lain.
func TestPlanFromGroq_CopiesParams(t *testing.T) {
	shared := map[string]interface{}{"query": "dari LLM"}
	orcPlan := orchestrator.Plan{Steps: []orchestrator.ToolCall{
		{ToolName: toolSearchArxiv, Params: shared},
	}}

	plan := planFromGroq(orcPlan, askReq())
	plan.Steps[0].Params["query"] = "diubah"

	if shared["query"] != "dari LLM" {
		t.Error("params LLM harus di-copy, bukan dipakai langsung")
	}
}

// evaluate_answer tidak diberi query — executor yang mengisinya.
func TestPlanFromGroq_SkipsQueryForEvaluate(t *testing.T) {
	orcPlan := orchestrator.Plan{Steps: []orchestrator.ToolCall{
		{ToolName: toolSearchArxiv, Params: map[string]interface{}{}},
		{ToolName: toolEvaluateAnswer, Params: map[string]interface{}{}},
	}}

	plan := planFromGroq(orcPlan, askReq())

	for _, step := range plan.Steps {
		if step.ToolName == toolEvaluateAnswer {
			if _, has := step.Params["query"]; has {
				t.Error("evaluate_answer tidak boleh diberi query")
			}
		}
	}
}

// ─────────────────────────── pemetaan response ───────────────────────────

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
