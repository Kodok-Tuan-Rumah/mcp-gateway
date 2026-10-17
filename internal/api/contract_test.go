package api

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func f64(v float64) *float64 { return &v }

// marshalHelper untuk test.
func marshalJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// assertGolden membandingkan JSON aktual dengan file golden di testdata/.
// Diperbarui dengan: go test ./internal/api/ -run Golden -update
func assertGolden(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("baca golden %s: %v (jalankan dengan -update)", path, err)
	}
	if got+"\n" != string(want) {
		t.Errorf("JSON tidak cocok dengan kontrak.\n--- want (%s) ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

var update = flag.Bool("update", false, "perbarui file golden")

// ---------------------------------------------------------------------------
// Golden: bentuk response
// ---------------------------------------------------------------------------

func TestGolden_AnsweredWithEvidence(t *testing.T) {
	resp := GatewayResponse{
		RequestID: "req-abc123",
		BackendAnswer: BackendAnswer{
			Answer: "RAG menggabungkan retrieval dengan generation.",
			Evidence: []Evidence{
				{Text: "Retrieval augmented generation...", SourceID: "2101.00001", Score: f64(0.82)},
				{Text: "Chunk kedua...", ChunkID: "chunk-7"},
			},
			Backend:     BackendArxiv,
			Model:       "phi3:mini",
			Latency:     &StageLatency{Retrieval: 120, Generation: 3400, Total: 3520},
			Decision:    DecisionAnswer,
			FailureType: FailureNone,
		},
		EvaluationStatus: EvalCompleted,
		Evaluation:       &EvaluationResult{Score: f64(0.86), FailureMode: "none", SupportedClaims: 3, TotalClaims: 3},
		Query:            "apa itu RAG?",
		Orchestrator:     "groq",
	}
	assertGolden(t, "answered", marshalJSON(t, resp))
}

func TestGolden_AbstainPreserved(t *testing.T) {
	resp := GatewayResponse{
		RequestID: "req-abc124",
		BackendAnswer: BackendAnswer{
			Answer:      "Not found in context.",
			Backend:     BackendArxiv,
			Decision:    DecisionAbstain,
			FailureType: FailureInsufficientContext,
		},
		EvaluationStatus: EvalSkipped,
		Query:            "hasil GPT-5 terbaru?",
	}
	assertGolden(t, "abstain", marshalJSON(t, resp))
}

func TestGolden_EvaluatorUnavailable(t *testing.T) {
	// Kasus inti: evaluator mati. Jawaban backend tetap dikembalikan,
	// evaluation_status="unavailable", dan TIDAK ADA key "score".
	resp := GatewayResponse{
		RequestID: "req-abc125",
		BackendAnswer: BackendAnswer{
			Answer:      "Jawaban tetap valid.",
			Backend:     BackendArxiv,
			Decision:    DecisionAnswer,
			FailureType: FailureNone,
		},
		EvaluationStatus: EvalUnavailable,
		Query:            "apa itu BM25?",
	}
	assertGolden(t, "evaluator_unavailable", marshalJSON(t, resp))
}

func TestGolden_ErrorResponse(t *testing.T) {
	assertGolden(t, "error", marshalJSON(t, ErrorResponse{
		RequestID:   "req-abc126",
		FailureType: FailureBackendNotAvailable,
		Field:       "backend",
		Message:     "backend research_rag is not available in v1: no adapter implemented",
	}))
}

// ---------------------------------------------------------------------------
// Kontrak yang harus upheld — ini yang mengunci Phase 2
// ---------------------------------------------------------------------------

// Skor 0 yang sah harus tetap muncul di JSON. 0 adalah nilai yang mungkin dan
// tidak boleh hilang — hanya "belum dinilai" (nil) yang boleh absen.
func TestScore_ZeroIsSerialized(t *testing.T) {
	resp := GatewayResponse{
		BackendAnswer:    BackendAnswer{Answer: "a", Backend: BackendArxiv, Decision: DecisionAnswer, FailureType: FailureNone},
		EvaluationStatus: EvalCompleted,
		Evaluation:       &EvaluationResult{Score: f64(0)},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	eval, ok := m["evaluation"].(map[string]interface{})
	if !ok {
		t.Fatal("evaluation tidak ada")
	}
	score, ok := eval["score"]
	if !ok {
		t.Fatal("score 0 yang sah hilang dari JSON — omitempty kelewat")
	}
	if score.(float64) != 0 {
		t.Errorf("score: ingin 0, dapat %v", score)
	}
}

// Score nil → key "score" harus absen, bukan 0.
func TestScore_NilIsAbsentNotZero(t *testing.T) {
	resp := GatewayResponse{
		BackendAnswer:    BackendAnswer{Answer: "a", Backend: BackendArxiv, Decision: DecisionAnswer, FailureType: FailureNone},
		EvaluationStatus: EvalUnavailable,
		Evaluation:       &EvaluationResult{FailureMode: "evaluator_unreachable"},
	}
	b, _ := json.Marshal(resp)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)

	eval := m["evaluation"].(map[string]interface{})
	if _, present := eval["score"]; present {
		t.Error("score harus absen saat belum dinilai, tapi muncul")
	}
	if eval["failure_mode"] != "evaluator_unreachable" {
		t.Errorf("failure_mode: %v", eval["failure_mode"])
	}
}

// evaluation_status wajib ada di setiap response, apa pun kondisinya.
func TestEvaluationStatus_AlwaysPresent(t *testing.T) {
	cases := []struct {
		name string
		resp GatewayResponse
		want string
	}{
		{"completed", GatewayResponse{EvaluationStatus: EvalCompleted}, EvalCompleted},
		{"unavailable", GatewayResponse{EvaluationStatus: EvalUnavailable}, EvalUnavailable},
		{"skipped", GatewayResponse{EvaluationStatus: EvalSkipped}, EvalSkipped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(tc.resp)
			var m map[string]interface{}
			_ = json.Unmarshal(b, &m)
			got, ok := m["evaluation_status"]
			if !ok {
				t.Fatal("evaluation_status wajib ada di setiap response")
			}
			if got != tc.want {
				t.Errorf("evaluation_status: ingin %q, dapat %v", tc.want, got)
			}
		})
	}
}

// failure_type dan decision harus selalu ada, termasuk "none".
func TestDecisionAndFailureType_AlwaysPresent(t *testing.T) {
	resp := GatewayResponse{
		BackendAnswer:    BackendAnswer{Answer: "a", Backend: BackendArxiv, Decision: DecisionAnswer, FailureType: FailureNone},
		EvaluationStatus: EvalSkipped,
	}
	b, _ := json.Marshal(resp)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)

	for _, k := range []string{"decision", "failure_type", "backend", "answer", "evaluation_status"} {
		if _, ok := m[k]; !ok {
			t.Errorf("field %q wajib ada di setiap response", k)
		}
	}
}

// Field yang backend tidak laporkan harus absen, bukan 0 / "" .
func TestBackendAnswer_UnreportedFieldsAreAbsent(t *testing.T) {
	resp := GatewayResponse{
		BackendAnswer: BackendAnswer{
			Answer:      "a",
			Backend:     BackendArxiv,
			Decision:    DecisionAnswer,
			FailureType: FailureNone,
			// Tidak ada Model, tidak ada Latency, tidak ada Evidence.
		},
		EvaluationStatus: EvalSkipped,
	}
	b, _ := json.Marshal(resp)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)

	for _, k := range []string{"model", "latency_ms", "evidence"} {
		if _, present := m[k]; present {
			t.Errorf("field %q tidak dilaporkan backend — harus absen, bukan null/0", k)
		}
	}
}

// ---------------------------------------------------------------------------
// Request
// ---------------------------------------------------------------------------

// Body dari client harus bisa dibaca dengan key snake_case.
func TestAskRequest_UnmarshalsSnakeCase(t *testing.T) {
	raw := `{"backend":"arxiv","query":"apa itu RAG?","top_k":3}`
	var r AskRequest
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Backend != "arxiv" || r.Query != "apa itu RAG?" || r.TopK != 3 {
		t.Errorf("request tidak terparse benar: %+v", r)
	}
}

func TestAskRequest_NormalizeAppliesDefaults(t *testing.T) {
	r := AskRequest{Query: "apa itu RAG?"}
	r.Normalize()
	if r.Backend != BackendArxiv {
		t.Errorf("backend default: ingin %q, dapat %q", BackendArxiv, r.Backend)
	}
	if r.TopK != DefaultTopK {
		t.Errorf("top_k default: ingin %d, dapat %d", DefaultTopK, r.TopK)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("request default harus valid: %v", err)
	}
}

func TestAskRequest_Validate(t *testing.T) {
	cases := []struct {
		name        string
		req         AskRequest
		wantErr     bool
		wantFailure string
		wantField   string
	}{
		{"valid arxiv", AskRequest{Backend: BackendArxiv, Query: "apa itu RAG?", TopK: 5}, false, "", ""},
		{"query kosong", AskRequest{Backend: BackendArxiv, Query: "", TopK: 5}, true, FailureInvalidRequest, "query"},
		{"query terlalu pendek", AskRequest{Backend: BackendArxiv, Query: "ab", TopK: 5}, true, FailureInvalidRequest, "query"},
		{"query terlalu panjang", AskRequest{Backend: BackendArxiv, Query: string(make([]byte, MaxQueryLen+1)), TopK: 5}, true, FailureInvalidRequest, "query"},
		{"top_k 0", AskRequest{Backend: BackendArxiv, Query: "apa itu RAG?", TopK: 0}, true, FailureInvalidRequest, "top_k"},
		{"top_k negatif", AskRequest{Backend: BackendArxiv, Query: "apa itu RAG?", TopK: -1}, true, FailureInvalidRequest, "top_k"},
		{"top_k melebihi max", AskRequest{Backend: BackendArxiv, Query: "apa itu RAG?", TopK: MaxTopK + 1}, true, FailureInvalidRequest, "top_k"},
		{"research_rag diterima", AskRequest{Backend: BackendResearchRAG, Query: "apa itu RAG?", TopK: 5}, false, "", ""},
		{"backend ngawur", AskRequest{Backend: "bukan_backend", Query: "apa itu RAG?", TopK: 5}, true, FailureInvalidRequest, "backend"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate()
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("harus valid, dapat error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("harus error, dapat nil")
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("harus *ValidationError, dapat %T", err)
			}
			if ve.FailureType != tc.wantFailure {
				t.Errorf("failure_type: ingin %q, dapat %q", tc.wantFailure, ve.FailureType)
			}
			if ve.Field != tc.wantField {
				t.Errorf("field: ingin %q, dapat %q", tc.wantField, ve.Field)
			}
		})
	}
}

// Top_k tepat di batas harus diterima.
func TestAskRequest_BoundaryTopKAccepted(t *testing.T) {
	for _, k := range []int{MinTopK, MaxTopK} {
		r := AskRequest{Backend: BackendArxiv, Query: "apa itu RAG?", TopK: k}
		if err := r.Validate(); err != nil {
			t.Errorf("top_k=%d harus valid: %v", k, err)
		}
	}
}

// Panjang query tepat di batas harus diterima.
func TestAskRequest_BoundaryQueryAccepted(t *testing.T) {
	for _, n := range []int{MinQueryLen, MaxQueryLen} {
		r := AskRequest{Backend: BackendArxiv, Query: string(make([]byte, n)), TopK: 5}
		if err := r.Validate(); err != nil {
			t.Errorf("query len=%d harus valid: %v", n, err)
		}
	}
}
