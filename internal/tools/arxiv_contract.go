package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/malikimayzar/mcp-gateway/internal/api"
)

// validateArxivResponse checks required wire fields before decoding into the
// typed response. Unknown fields remain allowed for backend compatibility.
// retrieval_ms/generation_ms are optional: the backend's insufficient-context
// response (insufficient_context: true) omits both latencies.
func validateArxivResponse(body []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("response must be a JSON object")
	}
	for _, name := range []string{"answer", "sources", "model"} {
		raw, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("required field %q is missing or null", name)
		}
	}
	return nil
}

// ArxivQueryRequest is the verified wire contract accepted by
// arxiv-research-assistant's POST /query endpoint.
type ArxivQueryRequest struct {
	Query   string `json:"query"`
	TopK    int    `json:"top_k"`
	Model   string `json:"model,omitempty"`
	ArxivID string `json:"arxiv_id,omitempty"`
}

// ArxivQueryResponse is the verified response from POST /query. LogID is
// included because the backend declares it, though it may be omitted.
type ArxivQueryResponse struct {
	Answer              string        `json:"answer"`
	Sources             []ArxivSource `json:"sources"`
	RetrievalMs         int64         `json:"retrieval_ms"`
	GenerationMs        int64         `json:"generation_ms"`
	Model               string        `json:"model"`
	LogID               string        `json:"log_id,omitempty"`
	InsufficientContext bool          `json:"insufficient_context,omitempty"`
}

func (r ArxivQueryResponse) AnswerText() string { return r.Answer }

func (r ArxivQueryResponse) ModelName() string { return r.Model }

// ArxivSource is one source passage returned by the ArXiv backend. The source
// API has no chunk ID or URI, so the gateway must not invent either field.
type ArxivSource struct {
	Text    string  `json:"text"`
	Score   float64 `json:"score"`
	ArxivID string  `json:"arxiv_id"`
}

// Evidence converts backend sources to the gateway's canonical evidence
// shape. An empty ArxivID remains absent; score is widened without changing
// its value.
func (r ArxivQueryResponse) Evidence() []api.Evidence {
	if len(r.Sources) == 0 {
		return nil
	}

	evidence := make([]api.Evidence, 0, len(r.Sources))
	for _, source := range r.Sources {
		score := source.Score
		evidence = append(evidence, api.Evidence{
			Text:     source.Text,
			SourceID: source.ArxivID,
			Score:    &score,
		})
	}
	return evidence
}

// ToolData preserves the backend's wire field names in the existing /tool
// result envelope. Internal execution uses the typed response directly.
func (r ArxivQueryResponse) ToolData() map[string]interface{} {
	sources := make([]interface{}, 0, len(r.Sources))
	for _, source := range r.Sources {
		sources = append(sources, map[string]interface{}{
			"text":     source.Text,
			"score":    float64(source.Score),
			"arxiv_id": source.ArxivID,
		})
	}

	data := map[string]interface{}{
		"answer":        r.Answer,
		"sources":       sources,
		"retrieval_ms":  r.RetrievalMs,
		"generation_ms": r.GenerationMs,
		"model":         r.Model,
	}
	if r.LogID != "" {
		data["log_id"] = r.LogID
	}
	return data
}

// Latency maps only durations reported by the backend. The API does not
// report an end-to-end total, so Total is intentionally left unset.
func (r ArxivQueryResponse) Latency() *api.StageLatency {
	return &api.StageLatency{
		Retrieval:  r.RetrievalMs,
		Generation: r.GenerationMs,
	}
}

// IsAbstention reports the ArXiv backend's abstention: either the legacy
// sentinel answer or the newer explicit insufficient_context response (which
// carries the answer "No relevant chunks found.").
func (r ArxivQueryResponse) IsAbstention() bool {
	if r.InsufficientContext {
		return true
	}
	return strings.TrimSpace(r.Answer) == api.ArxivAbstainText
}
