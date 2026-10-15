package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/malikimayzar/mcp-gateway/internal/registry"
)

// ─────────────────────────────── /health ───────────────────────────────

func TestHealth(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	resp := get(t, ts, "/health")
	body := decodeJSON(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", body["status"])
	}
	tools, ok := body["tools"].([]interface{})
	if !ok || len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %v", body["tools"])
	}
}

// Urutan tools harus deterministik. Iterasi map di Go acak, jadi tanpa sort
// response /health berubah-ubah antar request.
func TestHealth_ToolOrderIsDeterministic(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	var first []interface{}
	for i := 0; i < 20; i++ {
		body := decodeJSON(t, get(t, ts, "/health"))
		tools, _ := body["tools"].([]interface{})
		if first == nil {
			first = tools
			continue
		}
		if len(tools) != len(first) {
			t.Fatalf("panjang tools berubah: %v vs %v", tools, first)
		}
		for j := range tools {
			if tools[j] != first[j] {
				t.Fatalf("urutan tools tidak deterministik: %v vs %v", tools, first)
			}
		}
	}
}

// Penting: /health melaporkan liveness proses, bukan kesiapan downstream.
// Status "ok" tidak boleh dibaca sebagai "semua service hidup".
func TestHealth_IsLivenessNotReadiness(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	body := decodeJSON(t, get(t, ts, "/health"))

	if body["check"] != "liveness" {
		t.Errorf("expected check=liveness, got %v", body["check"])
	}
	if has(body, "ready") {
		t.Error("readiness check belum ada — jangan sampai diklaim ada")
	}
}

// ──────────────────────────────── /tool ────────────────────────────────

func TestTool_AcceptsSnakeCasePayload(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	resp := postJSON(t, ts, "/tool", map[string]interface{}{
		"tool_name": toolSearchArxiv,
		"trace_id":  "trace-integ-001",
		"params":    map[string]interface{}{"query": "hallucination in RAG"},
	})
	body := decodeJSON(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	if body["success"] != true {
		t.Errorf("expected success=true, got %v", body["success"])
	}
	if body["trace_id"] != "trace-integ-001" {
		t.Errorf("expected trace_id propagated, got %v", body["trace_id"])
	}
}

func TestTool_NotFound(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	resp := postJSON(t, ts, "/tool", map[string]interface{}{"tool_name": "nonexistent_tool"})
	body := decodeJSON(t, resp)

	// Tool tidak dikenal adalah kesalahan client → 400.
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	if body["success"] == true {
		t.Error("expected success=false untuk unknown tool")
	}
	if body["error"] == "" {
		t.Error("expected non-empty error message")
	}
}

// Param yang tidak valid juga kesalahan client → 400.
func TestTool_MissingRequiredParamIsBadRequest(t *testing.T) {
	reg := registry.New()
	reg.Register(toolSearchArxiv, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		if _, ok := req.Params["query"]; !ok {
			return registry.ToolResponse{
				ToolName: req.ToolName, TraceID: req.TraceID, Success: false,
				Error: "param query is required",
			}
		}
		return registry.ToolResponse{ToolName: req.ToolName, Success: true}
	})

	ts := newTestServer(reg)
	defer ts.Close()

	resp := postJSON(t, ts, "/tool", map[string]interface{}{
		"tool_name": toolSearchArxiv,
		"params":    map[string]interface{}{},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 untuk param wajib yang hilang, got %d", resp.StatusCode)
	}
}

// Kegagalan dependency BUKAN kesalahan client. Kalau ini dipoles jadi 400,
// client akan mengira request-nya yang salah.
func TestTool_DownstreamFailureIsBadGateway(t *testing.T) {
	reg := registry.New()
	reg.Register(toolSearchArxiv, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName, TraceID: req.TraceID, Success: false,
			Error: `search_arxiv call failed: Post "http://arxiv-go-backend:8080/query": dial tcp: lookup arxiv-go-backend: server misbehaving`,
		}
	})

	ts := newTestServer(reg)
	defer ts.Close()

	resp := postJSON(t, ts, "/tool", map[string]interface{}{
		"tool_name": toolSearchArxiv,
		"params":    map[string]interface{}{"query": "apa itu RAG?"},
	})
	body := decodeJSON(t, resp)

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("expected 502 untuk kegagalan downstream, got %d", resp.StatusCode)
	}
	if body["success"] != false {
		t.Errorf("success harus false, got %v", body["success"])
	}
	if body["error"] == "" {
		t.Error("error harus menjelaskan penyebabnya")
	}
}

func TestTool_InvalidBody(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	resp := postRaw(t, ts, "/tool", "not-json")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

// Field tak dikenal ditolak supaya salah ketik tidak diam-diam diabaikan.
func TestTool_RejectsUnknownField(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	resp := postRaw(t, ts, "/tool", `{"tool_name":"search_arxiv","params":{"query":"x"},"ngawur":1}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 untuk field tak dikenal, got %d", resp.StatusCode)
	}
}

func TestTool_RejectsOversizedBody(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	resp := postRaw(t, ts, "/tool",
		`{"tool_name":"search_arxiv","params":{"query":"`+bigString(maxBodyBytes+1024)+`"}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 untuk body melebihi batas, got %d", resp.StatusCode)
	}
}

// ──────────────────────── routing / validasi input ────────────────────────

func TestPlan_RejectsResearchRAG(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	resp := postJSON(t, ts, "/plan", map[string]interface{}{
		"backend": "research_rag",
		"query":   "apa itu RAG?",
	})
	body := decodeJSON(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%v)", resp.StatusCode, body)
	}
	// Penolakan harus eksplisit dan bisa dibedakan dari request tidak valid.
	if body["failure_type"] != "backend_not_available" {
		t.Errorf("expected failure_type=backend_not_available, got %v", body["failure_type"])
	}
	if body["field"] != "backend" {
		t.Errorf("expected field=backend, got %v", body["field"])
	}
	if body["request_id"] == "" {
		t.Error("error response harus tetap membawa request_id")
	}
}

func TestAsk_RejectsResearchRAG(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	resp := postJSON(t, ts, "/ask", map[string]interface{}{
		"backend": "research_rag",
		"query":   "apa itu RAG?",
	})
	body := decodeJSON(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%v)", resp.StatusCode, body)
	}
	if body["failure_type"] != "backend_not_available" {
		t.Errorf("expected backend_not_available, got %v", body["failure_type"])
	}
}

func TestPlan_RejectsUnknownBackend(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	resp := postJSON(t, ts, "/plan", map[string]interface{}{
		"backend": "bukan_backend",
		"query":   "apa itu RAG?",
	})
	body := decodeJSON(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	// Backend ngawur adalah request tidak valid, bukan backend tidak tersedia.
	if body["failure_type"] != "invalid_request" {
		t.Errorf("expected failure_type=invalid_request, got %v", body["failure_type"])
	}
}

func TestPlan_ValidatesQueryAndTopK(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	cases := []struct {
		name string
		body map[string]interface{}
	}{
		{"query kosong", map[string]interface{}{"query": ""}},
		{"query terlalu pendek", map[string]interface{}{"query": "ab"}},
		{"top_k negatif", map[string]interface{}{"query": "apa itu RAG?", "top_k": -1}},
		{"top_k melebihi batas", map[string]interface{}{"query": "apa itu RAG?", "top_k": 999}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := postJSON(t, ts, "/plan", tc.body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("expected 400, got %d", resp.StatusCode)
			}
		})
	}
}

func TestPlan_InvalidBody(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	resp := postRaw(t, ts, "/plan", "{bad json}")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAsk_RejectsOversizedBody(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	resp := postRaw(t, ts, "/ask", `{"query":"`+bigString(maxBodyBytes+1024)+`"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 untuk body melebihi batas, got %d", resp.StatusCode)
	}
}

// ───────────────────── kontrak response dari /plan ─────────────────────

func TestPlan_ReturnsGatewayContract(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	resp := postJSON(t, ts, "/plan", map[string]interface{}{
		"backend": "arxiv",
		"query":   "apa itu RAG?",
		"top_k":   3,
	})
	body := decodeJSON(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}

	// Semua key wajib dari docs/api-contract.md §3 harus ada.
	for _, k := range []string{
		"request_id", "answer", "backend", "decision", "failure_type", "evaluation_status",
	} {
		if !has(body, k) {
			t.Errorf("field %q wajib ada; dapat: %v", k, keys(body))
		}
	}

	if body["backend"] != "arxiv" {
		t.Errorf("expected backend=arxiv, got %v", body["backend"])
	}
	if body["decision"] != "answer" {
		t.Errorf("expected decision=answer, got %v", body["decision"])
	}
	if body["failure_type"] != "none" {
		t.Errorf("expected failure_type=none, got %v", body["failure_type"])
	}
	if body["evaluation_status"] != "completed" {
		t.Errorf("expected evaluation_status=completed, got %v", body["evaluation_status"])
	}
	if body["orchestrator"] != "rule-based" {
		t.Errorf("expected orchestrator=rule-based, got %v", body["orchestrator"])
	}
}

// Ini yang sebelumnya hampa: stub lama mengembalikan "chunks" dan
// "faithfulness" sementara executor membaca "results" dan
// "faithfulness_score" — sehingga evaluate selalu dilewati dan score selalu 0.
func TestPlan_EvaluationIsActuallyWired(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	body := decodeJSON(t, postJSON(t, ts, "/plan", map[string]interface{}{"query": "apa itu RAG?"}))

	if body["evaluation_status"] != "completed" {
		t.Fatalf("expected evaluation_status=completed, got %v", body["evaluation_status"])
	}
	eval, ok := body["evaluation"].(map[string]interface{})
	if !ok {
		t.Fatalf("evaluation harus ada; dapat: %v", keys(body))
	}
	score, ok := eval["score"].(float64)
	if !ok {
		t.Fatalf("score harus float, dapat %T", eval["score"])
	}
	if score != 0.86 {
		t.Errorf("expected 0.86 dari stub, got %v", score)
	}
}

// Plan hanya boleh menyentuh satu backend. Query dengan kata "research"/"study"
// dulu memicu search_arxiv dan retrieve_chunks sekaligus, lalu answer ArXiv
// dinilai terhadap chunk RAG.
func TestPlan_UsesOnlyOneBackend(t *testing.T) {
	var called []string
	reg := registry.New()
	for _, name := range []string{toolSearchArxiv, toolRetrieveChunks, toolEvaluateAnswer} {
		n := name
		reg.Register(n, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
			called = append(called, n)
			return registry.ToolResponse{
				ToolName: n, TraceID: req.TraceID, Success: true,
				Data: map[string]interface{}{
					"answer":             "a",
					"sources":            []interface{}{map[string]interface{}{"text": "t"}},
					"faithfulness_score": 0.9,
				},
			}
		})
	}

	ts := newTestServer(reg)
	defer ts.Close()

	resp := postJSON(t, ts, "/plan", map[string]interface{}{
		"query": "compare research on RAG papers and transformers",
	})
	body := decodeJSON(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}

	for _, c := range called {
		if c == toolRetrieveChunks {
			t.Errorf("retrieve_chunks tidak boleh dipanggil di jalur jawaban; calls=%v", called)
		}
	}
	if len(called) == 0 {
		t.Error("tidak ada tool yang dipanggil — test ini tidak membuktikan apa pun")
	}
}

// ──────────────── kegagalan backend terlihat, bukan disamarkan ────────────────

func TestPlan_BackendFailureIsVisible(t *testing.T) {
	reg := registry.New()
	reg.Register(toolSearchArxiv, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName, TraceID: req.TraceID, Success: false,
			Error: `search_arxiv call failed: dial tcp: lookup arxiv-go-backend: no such host`,
		}
	})
	reg.Register(toolEvaluateAnswer, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{ToolName: req.ToolName, Success: true}
	})

	ts := newTestServer(reg)
	defer ts.Close()

	resp := postJSON(t, ts, "/plan", map[string]interface{}{"query": "apa itu RAG?"})
	body := decodeJSON(t, resp)

	// 502: kegagalan dependency, bukan kesalahan client.
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", resp.StatusCode)
	}
	if body["decision"] != "error" {
		t.Errorf("expected decision=error, got %v", body["decision"])
	}
	if body["failure_type"] != "backend_error" {
		t.Errorf("expected failure_type=backend_error, got %v", body["failure_type"])
	}
	if a, ok := body["answer"].(string); ok && a != "" {
		t.Errorf("answer harus kosong saat backend gagal, got %q", a)
	}
	if body["evaluation_status"] == "completed" {
		t.Error("tidak boleh melaporkan evaluasi sukses saat tidak ada jawaban")
	}
}

func TestPlan_TimeoutClassifiedSeparately(t *testing.T) {
	reg := registry.New()
	reg.Register(toolSearchArxiv, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName, TraceID: req.TraceID, Success: false,
			Error: "search_arxiv call failed: context deadline exceeded",
		}
	})

	ts := newTestServer(reg)
	defer ts.Close()

	body := decodeJSON(t, postJSON(t, ts, "/plan", map[string]interface{}{"query": "apa itu RAG?"}))

	if body["failure_type"] != "backend_timeout" {
		t.Errorf("expected backend_timeout, got %v", body["failure_type"])
	}
}

// ───────────────────── abstensi backend dipertahankan ─────────────────────

func TestPlan_AbstentionIsPreserved(t *testing.T) {
	reg := registry.New()
	reg.Register(toolSearchArxiv, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName, TraceID: req.TraceID, Success: true,
			Data: map[string]interface{}{
				"answer":  "Not found in context.",
				"sources": []interface{}{},
			},
		}
	})

	ts := newTestServer(reg)
	defer ts.Close()

	resp := postJSON(t, ts, "/plan", map[string]interface{}{"query": "hasil GPT-5 terbaru?"})
	body := decodeJSON(t, resp)

	// Abstensi bukan error server.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 untuk abstensi, got %d", resp.StatusCode)
	}
	if body["decision"] != "abstain" {
		t.Errorf("expected decision=abstain, got %v", body["decision"])
	}
	if body["failure_type"] != "insufficient_context" {
		t.Errorf("expected insufficient_context, got %v", body["failure_type"])
	}
	if body["evaluation_status"] != "skipped" {
		t.Errorf("expected evaluation_status=skipped, got %v", body["evaluation_status"])
	}
}

// ────────────── evaluator mati ≠ skor 0 dan ≠ jawaban hilang ──────────────

func TestPlan_EvaluatorDownKeepsAnswerAndOmitsScore(t *testing.T) {
	reg := registry.New()
	reg.Register(toolSearchArxiv, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName, TraceID: req.TraceID, Success: true,
			Data: map[string]interface{}{
				"answer":  "jawaban tetap sah dengan bukti yang cukup",
				"sources": []interface{}{map[string]interface{}{"text": "t"}},
			},
		}
	})
	reg.Register(toolEvaluateAnswer, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName, TraceID: req.TraceID, Success: false,
			Error: "evaluate_answer call failed: no such host",
		}
	})

	ts := newTestServer(reg)
	defer ts.Close()

	resp := postJSON(t, ts, "/plan", map[string]interface{}{"query": "apa itu BM25?"})
	body := decodeJSON(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("jawaban backend tetap harus 200, got %d", resp.StatusCode)
	}
	if body["answer"] != "jawaban tetap sah dengan bukti yang cukup" {
		t.Errorf("jawaban backend tidak boleh hilang: %v", body["answer"])
	}
	if body["decision"] != "answer" {
		t.Errorf("kegagalan evaluator tidak boleh mengubah keputusan jawaban: %v", body["decision"])
	}
	if body["failure_type"] != "none" {
		t.Errorf("failure_type=%v; kegagalan evaluator tidak boleh menutupi status jawaban", body["failure_type"])
	}
	if body["evaluation_status"] != "unavailable" {
		t.Errorf("expected evaluation_status=unavailable, got %v", body["evaluation_status"])
	}
	if eval, ok := body["evaluation"].(map[string]interface{}); ok && has(eval, "score") {
		t.Error("score tidak boleh muncul saat evaluator tidak tersedia")
	}
}

// Evaluator bilang sukses tapi skor tidak bisa dibaca → tetap unavailable.
func TestPlan_EvaluatorWithoutUsableScore(t *testing.T) {
	reg := registry.New()
	reg.Register(toolSearchArxiv, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName, TraceID: req.TraceID, Success: true,
			Data: map[string]interface{}{
				"answer":  "jawaban yang cukup panjang untuk evaluasi",
				"sources": []interface{}{map[string]interface{}{"text": "t"}},
			},
		}
	})
	reg.Register(toolEvaluateAnswer, func(_ context.Context, req registry.ToolRequest) registry.ToolResponse {
		return registry.ToolResponse{
			ToolName: req.ToolName, TraceID: req.TraceID, Success: true,
			Data: map[string]interface{}{"unexpected": "shape"},
		}
	})

	ts := newTestServer(reg)
	defer ts.Close()

	body := decodeJSON(t, postJSON(t, ts, "/plan", map[string]interface{}{"query": "apa itu BM25?"}))

	if body["evaluation_status"] != "unavailable" {
		t.Errorf("skor tak terbaca harus jadi unavailable, bukan 0; got %v", body["evaluation_status"])
	}
	if eval, ok := body["evaluation"].(map[string]interface{}); ok && has(eval, "score") {
		t.Error("score tidak boleh muncul")
	}
}

// Default diterapkan saat field tidak dikirim, dan request tetap valid.
func TestPlan_AppliesDefaults(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	body := decodeJSON(t, postJSON(t, ts, "/plan", map[string]interface{}{
		"query": "apa itu RAG?", // backend dan top_k tidak dikirim
	}))

	if body["backend"] != "arxiv" {
		t.Errorf("backend default harus arxiv, got %v", body["backend"])
	}
	// top_k=5 diteruskan ke search_arxiv; stub menerimanya tanpa error.
	if body["decision"] != "answer" {
		t.Errorf("request tanpa backend/top_k harus tetap diproses, got decision=%v", body["decision"])
	}
}

// Hanya search_arxiv boleh dipanggil lewat /plan. RetrieveChunks dan
// EvaluateAnswer tetap bisa dipanggil langsung lewat /tool.
func TestTool_RetrieveChunksDirectly(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	resp := postJSON(t, ts, "/tool", map[string]interface{}{
		"tool_name": toolRetrieveChunks,
		"params":    map[string]interface{}{"query": "attention mechanism", "top_k": 3},
	})
	body := decodeJSON(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	if body["success"] != true {
		t.Errorf("expected success=true, got %v", body["success"])
	}
}

func TestTool_EvaluateAnswerDirectly(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	resp := postJSON(t, ts, "/tool", map[string]interface{}{
		"tool_name": toolEvaluateAnswer,
		"params": map[string]interface{}{
			"answer":   "RAG mengurangi halusinasi dengan grounding pada konteks.",
			"context":  "chunk A chunk B",
			"question": "Bagaimana RAG mengurangi halusinasi?",
		},
	})
	body := decodeJSON(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", resp.StatusCode, body)
	}
	if body["success"] != true {
		t.Errorf("expected success=true, got %v", body["success"])
	}
}

// ──────────────────────────────── /ask ────────────────────────────────

// Tanpa GROQ_API_KEY, /ask harus jatuh ke plan deterministik dan tetap
// memenuhi kontrak yang sama dengan /plan.
func TestAsk_FallsBackWithoutGroq(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "")
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	resp := postJSON(t, ts, "/ask", map[string]interface{}{"query": "apa itu RAG?"})
	body := decodeJSON(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 dari fallback, got %d (%v)", resp.StatusCode, body)
	}
	if body["orchestrator"] != "rule-based" {
		t.Errorf("expected orchestrator=rule-based, got %v", body["orchestrator"])
	}
	if body["answer"] != "RAG menggabungkan retrieval dengan generation." {
		t.Errorf("fallback harus tetap menjawab, got %v", body["answer"])
	}
	if body["evaluation_status"] != "completed" {
		t.Errorf("expected evaluation_status=completed, got %v", body["evaluation_status"])
	}
	if body["backend"] != "arxiv" {
		t.Errorf("expected backend=arxiv, got %v", body["backend"])
	}
}

func TestAsk_ReturnsSameContractAsPlan(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "")
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	body := decodeJSON(t, postJSON(t, ts, "/ask", map[string]interface{}{"query": "apa itu RAG?"}))

	for _, k := range []string{
		"request_id", "answer", "backend", "decision", "failure_type", "evaluation_status",
	} {
		if !has(body, k) {
			t.Errorf("field %q wajib ada; dapat: %v", k, keys(body))
		}
	}
}

// ─────────────────────── request id di semua response ───────────────────────

func TestRequestID_PresentInEveryResponse(t *testing.T) {
	ts := newTestServer(newTestRegistry())
	defer ts.Close()

	for _, path := range []string{"/plan", "/ask"} {
		body := decodeJSON(t, postJSON(t, ts, path, map[string]interface{}{"query": "apa itu RAG?"}))
		if id, _ := body["request_id"].(string); id == "" {
			t.Errorf("%s: request_id kosong", path)
		}
	}
}
