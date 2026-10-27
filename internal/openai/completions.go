package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/enrico9034/llmock/internal/engine"
)

type completionRequest struct {
	Model         string          `json:"model"`
	Prompt        json.RawMessage `json:"prompt"`
	Stream        bool            `json:"stream"`
	N             int             `json:"n"`
	MaxTokens     int             `json:"max_tokens"`
	Seed          *int64          `json:"seed"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

// Completions serves POST /v1/completions (legacy text completions).
func Completions(env *engine.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req completionRequest
		if status, err := engine.DecodeJSON(w, r, env.MaxBody, &req); err != nil {
			WriteError(w, status, err.Error())
			return
		}
		prompts, ok := stringOrStrings(req.Prompt)
		switch {
		case req.Model == "":
			WriteError(w, 400, "model is required")
			return
		case !ok || len(prompts) == 0:
			WriteError(w, 400, "prompt must be a string or an array of strings")
			return
		case req.N > 1:
			WriteError(w, 400, "n > 1 is not supported")
			return
		}

		plan, err := engine.BuildPlan(engine.Request{
			Header: r.Header.Get("X-Mock-Directives"), Scope: prompts, Input: prompts, MaxTokens: req.MaxTokens, Seed: req.Seed,
		}, env.Layers)
		if err != nil {
			WriteError(w, 400, err.Error())
			return
		}
		enc := &completionEncoder{id: randID("cmpl-"), model: req.Model, created: time.Now().Unix(), includeUsage: req.StreamOptions.IncludeUsage}
		env.Respond(r.Context(), w, plan, enc, req.Stream)
	}
}

// stringOrStrings decodes a JSON string or array of strings.
func stringOrStrings(raw json.RawMessage) ([]string, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}, true
	}
	var ss []string
	return ss, json.Unmarshal(raw, &ss) == nil
}

type completionEncoder struct {
	id, model    string
	created      int64
	includeUsage bool
}

func (e *completionEncoder) WriteError(w http.ResponseWriter, status int, msg string) {
	WriteError(w, status, msg)
}

func (e *completionEncoder) envelope(choices []any) map[string]any {
	return map[string]any{"id": e.id, "object": "text_completion", "created": e.created, "model": e.model, "choices": choices}
}

func choice(text string, finish any) []any {
	return []any{map[string]any{"text": text, "index": 0, "logprobs": nil, "finish_reason": finish}}
}

func (e *completionEncoder) WriteBody(w http.ResponseWriter, text, finish string, u engine.Usage) {
	b := e.envelope(choice(text, finish))
	b["usage"] = usageJSON(u)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(b)
}

func (e *completionEncoder) frame(w io.Writer, v any) error {
	b, _ := json.Marshal(v)
	_, err := io.WriteString(w, "data: "+string(b)+"\n\n")
	return err
}

func (e *completionEncoder) StreamStart(io.Writer) error { return nil }

func (e *completionEncoder) StreamChunk(w io.Writer, text string) error {
	return e.frame(w, e.envelope(choice(text, nil)))
}

func (e *completionEncoder) StreamEnd(w io.Writer, finish string, u engine.Usage) error {
	if err := e.frame(w, e.envelope(choice("", finish))); err != nil {
		return err
	}
	if e.includeUsage {
		b := e.envelope([]any{})
		b["usage"] = usageJSON(u)
		if err := e.frame(w, b); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "data: [DONE]\n\n")
	return err
}

func (e *completionEncoder) StreamError(w io.Writer, status int, msg string) error {
	return e.frame(w, map[string]apiError{"error": errorFor(status, msg)})
}
