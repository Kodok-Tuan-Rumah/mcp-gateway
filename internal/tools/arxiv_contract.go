package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/malikimayzar/mcp-gateway/internal/api"
)

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

type ArxivQueryRequest struct {
	Query   string `json:"query"`
	TopK    int    `json:"top_k"`
	Model   string `json:"model,omitempty"`
	ArxivID string `json:"arxiv_id,omitempty"`
}

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
func (r ArxivQueryResponse) ModelName() string  { return r.Model }

type ArxivSource struct {
	Text    string  `json:"text"`
	Score   float64 `json:"score"`
	ArxivID string  `json:"arxiv_id"`
}

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

func (r ArxivQueryResponse) Latency() *api.StageLatency {
	return &api.StageLatency{
		Retrieval:  r.RetrievalMs,
		Generation: r.GenerationMs,
	}
}

func (r ArxivQueryResponse) IsAbstention() bool {
	if r.InsufficientContext {
		return true
	}
	return strings.TrimSpace(r.Answer) == api.ArxivAbstainText
}
