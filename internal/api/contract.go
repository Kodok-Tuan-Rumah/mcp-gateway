// Package api mendefinisikan kontrak publik gateway: bentuk request dan response
// yang dipakai di luar proses.
//
// Kontrak ini adalah sumber kebenaran tunggal. Types di package lain
// (planner.ExecutionResult, registry.ToolResponse) adalah record internal dan
// BUKAN bentuk publik — jangan dipakai sebagai respons API.
//
// Aturan yang dipegang di sini, sesuai docs/api-contract.md:
//
//   - Semua key JSON lowercase dan stabil.
//   - Metadata yang tidak diberikan backend TIDAK boleh dikarang. Field yang
//     tidak terisi harus kosong atau absen, bukan diisi nilai tebakan.
//   - "Belum dievaluasi" harus dapat dibedakan dari "skor 0". Karena itu
//     EvaluationStatus selalu ada sebagai string terpisah, dan Score memakai
//     *float64 + omitempty sehingga nil (tidak dinilai) tidak pernah
//     ter-serialize menjadi 0.
package api

import (
	"strconv"
	"unicode/utf8"
)

// Backend yang dikenali.
const (
	BackendArxiv       = "arxiv"
	BackendResearchRAG = "research_rag"
)

// Batas dan default request. Diturunkan dari GenerateRequest di rag-research
// supaya gateway tidak meneruskan parameter yang akan ditolak downstream.
const (
	DefaultTopK = 5
	MinTopK     = 1
	MaxTopK     = 20
	MinQueryLen = 3
	MaxQueryLen = 1000
)

// Decision: apa yang terjadi pada jawaban.
const (
	DecisionAnswer  = "answer"
	DecisionAbstain = "abstain"
	DecisionError   = "error"
)

// ArxivAbstainText is the sentinel returned by the ArXiv backend when its
// indexed context cannot answer a query. Keep it centralized so execution and
// response mapping agree on when evaluation must be skipped.
const ArxivAbstainText = "Not found in context."

// RAGAbstainText is the sentinel rag-research writes into the answer field when
// its generator (or evidence gate) decides the retrieved context is not enough.
// Note: rag-research POST /generate keeps decision=="GENERATE" on abstention,
// so detection must combine the sentinel with failure_type — see
// ResearchRAGResponse.IsAbstention.
const RAGAbstainText = "INSUFFICIENT_CONTEXT"

// FailureType: kategorisasi kegagalan. "none" berarti tidak ada kegagalan;
// nilai selain itu harus selalu bisa dipertahankan sampai ke response.
const (
	FailureNone                  = "none"
	FailureInvalidRequest        = "invalid_request"
	FailureBackendNotAvailable   = "backend_not_available"
	FailureBackendTimeout        = "backend_timeout"
	FailureBackendError          = "backend_error"
	FailureMalformedResponse     = "malformed_response"
	FailureInsufficientContext   = "insufficient_context"
	FailureUnsupportedClaims     = "unsupported_claims"
	FailureProviderError         = "provider_error"
	FailureSanityCheck           = "sanity_check"
	FailureEmptyOutput           = "empty_output"
	FailureEvaluationUnavailable = "evaluation_unavailable"
)

// ProviderErrorKind adalah penyebab spesifik di balik failure_type=provider_error.
// Nilainya dipetakan apa adanya dari taksonomi rag-research; gateway tidak
// menafsirkannya, hanya meneruskan.
const (
	ProviderErrorRateLimit     = "rate_limit"
	ProviderErrorConnection    = "connection"
	ProviderErrorAuth          = "auth"
	ProviderErrorInvalidJSON   = "invalid_json"
	ProviderErrorNotConfigured = "not_configured"
	ProviderErrorUnknown       = "provider"
)

// EvaluationStatus: apakah jawaban benar-benar dinilai, dan kalau tidak,
// kenapa. Field ini wajib ada di setiap response.
const (
	EvalCompleted   = "completed"
	EvalUnavailable = "unavailable"
	EvalSkipped     = "skipped"
)

// AskRequest adalah body POST /ask dan POST /plan.
//
// Backend opsional: kalau kosong akan diisi BackendArxiv oleh Normalize().
// research_rag sekarang tersedia sebagai backend alternatif yang dirutekan
// ke adapter rag-research via POST /generate.
type AskRequest struct {
	Backend string `json:"backend"`
	Query   string `json:"query"`
	TopK    int    `json:"top_k"`
}

// Normalize mengisi default untuk field yang kosong. Tidak memvalidasi;
// panggil setelah Normalize.
func (r *AskRequest) Normalize() {
	if r.Backend == "" {
		r.Backend = BackendArxiv
	}
	if r.TopK == 0 {
		r.TopK = DefaultTopK
	}
}

// Validate memeriksa batas request dan ketersediaan backend.
// Selalu panggil setelah Normalize.
func (r *AskRequest) Validate() error {
	queryLength := utf8.RuneCountInString(r.Query)
	if queryLength < MinQueryLen {
		return &ValidationError{
			Field:       "query",
			Message:     "query must be at least " + strconv.Itoa(MinQueryLen) + " characters",
			FailureType: FailureInvalidRequest,
		}
	}
	if queryLength > MaxQueryLen {
		return &ValidationError{
			Field:       "query",
			Message:     "query must be at most " + strconv.Itoa(MaxQueryLen) + " characters",
			FailureType: FailureInvalidRequest,
		}
	}
	if r.TopK < MinTopK || r.TopK > MaxTopK {
		return &ValidationError{
			Field:       "top_k",
			Message:     "top_k must be between " + strconv.Itoa(MinTopK) + " and " + strconv.Itoa(MaxTopK),
			FailureType: FailureInvalidRequest,
		}
	}

	switch r.Backend {
	case BackendArxiv:
		return nil
	case BackendResearchRAG:
		return nil
	default:
		return &ValidationError{
			Field:       "backend",
			Message:     "unknown backend " + r.Backend + ": must be one of arxiv, research_rag",
			FailureType: FailureInvalidRequest,
		}
	}
}

// ValidationError adalah error validasi request yang membawa failure_type
// supaya router bisa membangun response error yang konsisten.
type ValidationError struct {
	Field       string `json:"field"`
	Message     string `json:"message"`
	FailureType string `json:"failure_type"`
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

// Evidence adalah satu potongan bukti yang mendukung jawaban.
//
// SourceID dan ChunkID hanya diisi kalau backend benar-benar mengirimkannya;
// kalau tidak, field dibiarkan kosong — bukan diisi placeholder.
type Evidence struct {
	Text     string   `json:"text"`
	SourceID string   `json:"source_id,omitempty"`
	ChunkID  string   `json:"chunk_id,omitempty"`
	URI      string   `json:"uri,omitempty"`
	Score    *float64 `json:"score,omitempty"`
}

// StageLatency adalah latensi per tahap yang dilaporkan backend.
// Field yang tidak dilaporkan backend dibiarkan kosong, bukan diisi 0.
type StageLatency struct {
	Retrieval  int64 `json:"retrieval,omitempty"`
	Generation int64 `json:"generation,omitempty"`
	Total      int64 `json:"total,omitempty"`
}

// BackendAnswer adalah bentuk ternormalisasi dari satu backend.
//
// Answer dan Evidence sengaja berada dalam satu struct: keduanya harus berasal
// dari backend yang sama. Memisahkan keduanya di tingkat tipe membuat
// jawaban dari satu backend bisa dipasangkan dengan bukti backend lain.
type BackendAnswer struct {
	Answer      string        `json:"answer"`
	Evidence    []Evidence    `json:"evidence,omitempty"`
	Backend     string        `json:"backend"`
	Model       string        `json:"model,omitempty"`
	Latency     *StageLatency `json:"latency_ms,omitempty"`
	Decision    string        `json:"decision"`
	FailureType string        `json:"failure_type"`
	// Reflection metadata is optional because other backends and older RAG
	// responses do not report whether lexical reflection ran.
	ReflectionPassed       *bool    `json:"reflection_passed,omitempty"`
	ReflectionScore        *float64 `json:"reflection_score,omitempty"`
	UnsupportedClaimsCount *int     `json:"unsupported_claims_count,omitempty"`
	// ProviderErrorKind hanya terisi bila FailureType == provider_error
	// (rate_limit | connection | auth | invalid_json | not_configured |
	// provider). Backends lain dan response RAG lama tidak melaporkannya,
	// sehingga field ini absent dari JSON sampai backend mengirimnya.
	ProviderErrorKind string `json:"provider_error_kind,omitempty"`
}

// EvaluationResult adalah hasil evaluasi opsional.
//
// Score adalah *float64 dengan omitempty: nil berarti "tidak dinilai" dan akan
// hilang dari JSON, bukan muncul sebagai 0.
type EvaluationResult struct {
	Score           *float64 `json:"score,omitempty"`
	FailureMode     string   `json:"failure_mode,omitempty"`
	Version         string   `json:"version,omitempty"`
	SupportedClaims int      `json:"supported_claims,omitempty"`
	TotalClaims     int      `json:"total_claims,omitempty"`
}

// StepResult adalah catatan satu langkah eksekusi, untuk diagnostik.
type StepResult struct {
	ToolName string `json:"tool_name"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
}

// GatewayResponse adalah respons publik untuk /ask dan /plan.
//
// BackendAnswer di-embed (tanpa tag) sehingga field-nya rata di JSON:
// answer, evidence, backend, model, latency_ms, decision, failure_type.
type GatewayResponse struct {
	RequestID string `json:"request_id"`

	BackendAnswer

	// EvaluationStatus wajib ada di setiap response: completed, unavailable,
	// atau skipped. Inilah yang membuat "tidak ada skor" tidak pernah
	// ditampilkan sebagai "skor 0".
	EvaluationStatus string            `json:"evaluation_status"`
	Evaluation       *EvaluationResult `json:"evaluation,omitempty"`

	Query        string       `json:"query,omitempty"`
	Orchestrator string       `json:"orchestrator,omitempty"`
	Steps        []StepResult `json:"steps,omitempty"`
}

// ErrorResponse adalah badan respons untuk error yang tidak punya jawaban.
type ErrorResponse struct {
	RequestID   string `json:"request_id,omitempty"`
	FailureType string `json:"failure_type"`
	Message     string `json:"message,omitempty"`
	Field       string `json:"field,omitempty"`
}
