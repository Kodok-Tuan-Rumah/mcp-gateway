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

const ArxivAbstainText = "Not found in context."
const RAGAbstainText = "INSUFFICIENT_CONTEXT"

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

const (
	ProviderErrorRateLimit     = "rate_limit"
	ProviderErrorConnection    = "connection"
	ProviderErrorAuth          = "auth"
	ProviderErrorInvalidJSON   = "invalid_json"
	ProviderErrorNotConfigured = "not_configured"
	ProviderErrorUnknown       = "provider"
)

const (
	EvalCompleted   = "completed"
	EvalUnavailable = "unavailable"
	EvalSkipped     = "skipped"
)

type AskRequest struct {
	Backend string `json:"backend"`
	Query   string `json:"query"`
	TopK    int    `json:"top_k"`
}

func (r *AskRequest) Normalize() {
	if r.Backend == "" {
		r.Backend = BackendArxiv
	}
	if r.TopK == 0 {
		r.TopK = DefaultTopK
	}
}

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

type ValidationError struct {
	Field       string `json:"field"`
	Message     string `json:"message"`
	FailureType string `json:"failure_type"`
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

type Evidence struct {
	Text     string   `json:"text"`
	SourceID string   `json:"source_id,omitempty"`
	ChunkID  string   `json:"chunk_id,omitempty"`
	URI      string   `json:"uri,omitempty"`
	Score    *float64 `json:"score,omitempty"`
}

// SpanAttribution memetakan span klaim ke chunk sumber (Phase 4 audit).
type SpanAttribution struct {
	ClaimSpan string   `json:"claim_span,omitempty"`
	ChunkID   string   `json:"chunk_id,omitempty"`
	DocID     string   `json:"doc_id,omitempty"`
	Score     *float64 `json:"score,omitempty"`
	Text      string   `json:"text,omitempty"`
}

type StageLatency struct {
	Retrieval  int64 `json:"retrieval,omitempty"`
	Generation int64 `json:"generation,omitempty"`
	Total      int64 `json:"total,omitempty"`
}

type BackendAnswer struct {
	Answer                 string        `json:"answer"`
	Evidence               []Evidence    `json:"evidence,omitempty"`
	Backend                string        `json:"backend"`
	Model                  string        `json:"model,omitempty"`
	Latency                *StageLatency `json:"latency_ms,omitempty"`
	Decision               string        `json:"decision"`
	FailureType            string        `json:"failure_type"`
	ReflectionPassed       *bool         `json:"reflection_passed,omitempty"`
	ReflectionScore        *float64      `json:"reflection_score,omitempty"`
	UnsupportedClaimsCount *int          `json:"unsupported_claims_count,omitempty"`
	ProviderErrorKind      string        `json:"provider_error_kind,omitempty"`
}

type EvaluationResult struct {
	Score           *float64 `json:"score,omitempty"`
	FailureMode     string   `json:"failure_mode,omitempty"`
	Version         string   `json:"version,omitempty"`
	SupportedClaims int      `json:"supported_claims,omitempty"`
	TotalClaims     int      `json:"total_claims,omitempty"`
}
type StepResult struct {
	ToolName string `json:"tool_name"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
}

type GatewayResponse struct {
	RequestID string `json:"request_id"`
	BackendAnswer
	EvaluationStatus string            `json:"evaluation_status"`
	Evaluation       *EvaluationResult `json:"evaluation,omitempty"`

	Query        string       `json:"query,omitempty"`
	Orchestrator string       `json:"orchestrator,omitempty"`
	Steps        []StepResult `json:"steps,omitempty"`

	// Audit / calibration (Phase 4)
	CalibratedConfidence *float64          `json:"calibrated_confidence,omitempty"`
	CalibratedDecision   string            `json:"calibrated_decision,omitempty"`
	SpanLevelAttribution []SpanAttribution `json:"span_level_attribution,omitempty"`
}
type ErrorResponse struct {
	RequestID   string `json:"request_id,omitempty"`
	FailureType string `json:"failure_type"`
	Message     string `json:"message,omitempty"`
	Field       string `json:"field,omitempty"`
}
