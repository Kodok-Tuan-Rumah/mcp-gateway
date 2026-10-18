package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/malikimayzar/mcp-gateway/internal/api"
)

// validateResearchRAGResponse checks required wire fields before decoding into
// the typed response. Unknown fields remain allowed for backend compatibility.
func validateResearchRAGResponse(body []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("response must be a JSON object")
	}
	for _, name := range []string{"answer", "retrieved_chunks", "latency_total_ms", "model", "confidence", "decision"} {
		raw, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("required field %q is missing or null", name)
		}
	}
	return nil
}

// ResearchRAGRequest is the verified wire contract accepted by rag-research's
// POST /generate endpoint.
type ResearchRAGRequest struct {
	Query       string `json:"query"`
	TopK        int    `json:"top_k,omitempty"`
	UseReranker bool   `json:"use_reranker,omitempty"`
}

// ResearchRAGResponse is the verified response from POST /generate.
type ResearchRAGResponse struct {
	Query                string             `json:"query"`
	Answer               string             `json:"answer"`
	RetrievedChunks      []ResearchRAGChunk `json:"retrieved_chunks"`
	RetrievalMethod      string             `json:"retrieval_method"`
	LatencyRetrievalMs   float64            `json:"latency_retrieval_ms"`
	LatencyGenerationMs  float64            `json:"latency_generation_ms"`
	LatencyTotalMs       float64            `json:"latency_total_ms"`
	Model                string             `json:"model"`
	Confidence           float64            `json:"confidence"`
	Decision             string             `json:"decision"`
	FailureType          string             `json:"failure_type,omitempty"`
	GeneratorStatus      string             `json:"generator_status,omitempty"`
	GeneratorConfidence  *float64           `json:"generator_confidence,omitempty"`
}

// ResearchRAGChunk is one retrieved chunk returned by rag-research.
type ResearchRAGChunk struct {
	ChunkID  string  `json:"chunk_id"`
	DocID    string  `json:"doc_id"`
	Text     string  `json:"text"`
	Score    float64 `json:"score"`
}

// AnswerText returns the backend's answer text.
func (r ResearchRAGResponse) AnswerText() string { return r.Answer }

// ModelName returns the generation model used by the backend.
func (r ResearchRAGResponse) ModelName() string { return r.Model }

// Evidence converts backend retrieved chunks to the gateway's canonical
// evidence shape. An empty chunk ID remains absent; score is widened without
// changing its value.
func (r ResearchRAGResponse) Evidence() []api.Evidence {
	if len(r.RetrievedChunks) == 0 {
		return nil
	}

	evidence := make([]api.Evidence, 0, len(r.RetrievedChunks))
	for _, chunk := range r.RetrievedChunks {
		score := chunk.Score
		evidence = append(evidence, api.Evidence{
			Text:     chunk.Text,
			SourceID: chunk.DocID,
			ChunkID:  chunk.ChunkID,
			Score:    &score,
		})
	}
	return evidence
}

// ToolData preserves the backend's wire field names in the existing /tool
// result envelope. Internal execution uses the typed response directly.
func (r ResearchRAGResponse) ToolData() map[string]interface{} {
	chunks := make([]interface{}, 0, len(r.RetrievedChunks))
	for _, chunk := range r.RetrievedChunks {
		chunks = append(chunks, map[string]interface{}{
			"chunk_id": chunk.ChunkID,
			"doc_id":   chunk.DocID,
			"text":     chunk.Text,
			"score":    float64(chunk.Score),
		})
	}

	data := map[string]interface{}{
		"answer":               r.Answer,
		"retrieved_chunks":     chunks,
		"retrieval_method":     r.RetrievalMethod,
		"latency_retrieval_ms": r.LatencyRetrievalMs,
		"latency_generation_ms": r.LatencyGenerationMs,
		"latency_total_ms":     r.LatencyTotalMs,
		"model":                r.Model,
		"confidence":           r.Confidence,
		"decision":             r.Decision,
	}
	if r.FailureType != "" {
		data["failure_type"] = r.FailureType
	}
	if r.GeneratorStatus != "" {
		data["generator_status"] = r.GeneratorStatus
	}
	if r.GeneratorConfidence != nil {
		data["generator_confidence"] = *r.GeneratorConfidence
	}
	return data
}

// Latency maps only durations reported by the backend.
func (r ResearchRAGResponse) Latency() *api.StageLatency {
	return &api.StageLatency{
		Retrieval:  int64(r.LatencyRetrievalMs),
		Generation: int64(r.LatencyGenerationMs),
		Total:      int64(r.LatencyTotalMs),
	}
}

// IsAbstention reports whether the backend decided the context is insufficient
// to answer. rag-research frames abstention differently depending on the
// endpoint: POST /ask answers with decision=="REJECT", while POST /generate
// may keep decision=="GENERATE" and instead signal abstention through the
// sentinel answer plus failure_type=="insufficient_context".
func (r ResearchRAGResponse) IsAbstention() bool {
	if strings.TrimSpace(r.Decision) == "REJECT" {
		return true
	}
	if r.FailureType == api.FailureInsufficientContext {
		return true
	}
	return strings.TrimSpace(r.Answer) == api.RAGAbstainText
}
