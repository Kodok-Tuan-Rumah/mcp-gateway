package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type EvaluateAnswerRequest struct {
	Answer   string `json:"answer"`
	Context  string `json:"context"`
	Question string `json:"question"`
}

// EvaluateAnswerResponse matches the evaluator's HTTP response. EvaluatorVersion
// is optional during rolling upgrades where the old service may still be live.
type EvaluateAnswerResponse struct {
	FaithfulnessScore float64 `json:"faithfulness_score"`
	FailureMode       string  `json:"failure_mode"`
	HasFailure        bool    `json:"has_failure"`
	SupportedClaims   int     `json:"supported_claims"`
	TotalClaims       int     `json:"total_claims"`
	EvaluatorVersion  string  `json:"evaluator_version,omitempty"`
}

func validateEvaluateAnswerResponse(body []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("response must be a JSON object")
	}
	for _, name := range []string{
		"faithfulness_score", "failure_mode", "has_failure", "supported_claims", "total_claims",
	} {
		raw, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("required field %q is missing or null", name)
		}
	}
	return nil
}

func (r EvaluateAnswerResponse) ToolData() map[string]interface{} {
	data := map[string]interface{}{
		"faithfulness_score": r.FaithfulnessScore,
		"failure_mode":       r.FailureMode,
		"has_failure":        r.HasFailure,
		"supported_claims":   r.SupportedClaims,
		"total_claims":       r.TotalClaims,
	}
	if r.EvaluatorVersion != "" {
		data["evaluator_version"] = r.EvaluatorVersion
	}
	return data
}
