package engine

import (
	"encoding/json"
	"strings"
)

// ContentText extracts the text of a message content field: a JSON string, or
// an array of parts/blocks of which only {"type":"text"} items are kept.
// It serves both OpenAI content parts and Anthropic content blocks.
func ContentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}
