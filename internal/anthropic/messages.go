package anthropic

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"

	"github.com/enrico9034/llmock/internal/engine"
)

type messagesRequest struct {
	Model     string          `json:"model"`
	Stream    bool            `json:"stream"`
	MaxTokens int             `json:"max_tokens"`
	System    json.RawMessage `json:"system"`
	Messages  []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

// plan decodes and validates a request and builds its plan; on failure it
// has already written the error response.
func plan(env *engine.Env, w http.ResponseWriter, r *http.Request) (req messagesRequest, p engine.Plan, ok bool) {
	if status, err := engine.DecodeJSON(w, r, env.MaxBody, &req); err != nil {
		WriteError(w, status, err.Error())
		return
	}
	switch {
	case req.Model == "":
		WriteError(w, 400, "model is required")
		return
	case len(req.Messages) == 0:
		WriteError(w, 400, "messages must not be empty")
		return
	}
	system := engine.ContentText(req.System)
	er := engine.Request{Header: r.Header.Get("X-Mock-Directives"), MaxTokens: req.MaxTokens, Input: []string{system}}
	var lastUser string
	for _, m := range req.Messages {
		text := engine.ContentText(m.Content)
		er.Input = append(er.Input, text)
		if m.Role == "user" {
			lastUser = text
		}
	}
	er.Scope = []string{system, lastUser}
	p, err := engine.BuildPlan(er, env.Layers)
	if err != nil {
		WriteError(w, 400, err.Error())
		return
	}
	return req, p, true
}

// Messages serves POST /v1/messages.
func Messages(env *engine.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, p, ok := plan(env, w, r)
		if !ok {
			return
		}
		env.Respond(r.Context(), w, p, &messagesEncoder{id: randID("msg_"), model: req.Model, promptTokens: p.PromptTokens}, req.Stream)
	}
}

// CountTokens serves POST /v1/messages/count_tokens.
func CountTokens(env *engine.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, p, ok := plan(env, w, r)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{"input_tokens": p.PromptTokens})
	}
}

func randID(prefix string) string {
	var b [12]byte
	for i := range b {
		b[i] = byte(rand.UintN(256))
	}
	return prefix + hex.EncodeToString(b[:])
}

type messagesEncoder struct {
	id, model    string
	promptTokens int
}

func stopReason(finish string) string {
	switch finish {
	case "length":
		return "max_tokens"
	case "content_filter":
		return "refusal"
	}
	return "end_turn"
}

func (e *messagesEncoder) WriteError(w http.ResponseWriter, status int, msg string) {
	WriteError(w, status, msg)
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type message struct {
	ID           string      `json:"id"`
	Type         string      `json:"type"`
	Role         string      `json:"role"`
	Model        string      `json:"model"`
	Content      []textBlock `json:"content"`
	StopReason   *string     `json:"stop_reason"`
	StopSequence *string     `json:"stop_sequence"`
	Usage        usage       `json:"usage"`
}

func (e *messagesEncoder) message(content []textBlock, stop *string, u usage) message {
	return message{e.id, "message", "assistant", e.model, content, stop, nil, u}
}

func (e *messagesEncoder) WriteBody(w http.ResponseWriter, text, finish string, u engine.Usage) {
	w.Header().Set("Content-Type", "application/json")
	sr := stopReason(finish)
	json.NewEncoder(w).Encode(e.message([]textBlock{{"text", text}}, &sr, usage{u.PromptTokens, u.CompletionTokens}))
}

func sse(w io.Writer, event string, data any) error {
	b, _ := json.Marshal(data)
	_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	return err
}

type ev struct {
	Type         string     `json:"type"`
	Message      *message   `json:"message,omitempty"`
	Index        *int       `json:"index,omitempty"`
	ContentBlock *textBlock `json:"content_block,omitempty"`
	Delta        any        `json:"delta,omitempty"`
	Usage        any        `json:"usage,omitempty"`
}

func (e *messagesEncoder) StreamStart(w io.Writer) error {
	zero := 0
	msg := e.message([]textBlock{}, nil, usage{InputTokens: e.promptTokens})
	for _, f := range []ev{
		{Type: "message_start", Message: &msg},
		{Type: "content_block_start", Index: &zero, ContentBlock: &textBlock{"text", ""}},
		{Type: "ping"},
	} {
		if err := sse(w, f.Type, f); err != nil {
			return err
		}
	}
	return nil
}

func (e *messagesEncoder) StreamChunk(w io.Writer, text string) error {
	zero := 0
	return sse(w, "content_block_delta", ev{Type: "content_block_delta", Index: &zero, Delta: textBlock{"text_delta", text}})
}

func (e *messagesEncoder) StreamEnd(w io.Writer, finish string, u engine.Usage) error {
	zero := 0
	if err := sse(w, "content_block_stop", ev{Type: "content_block_stop", Index: &zero}); err != nil {
		return err
	}
	delta := struct {
		StopReason   string  `json:"stop_reason"`
		StopSequence *string `json:"stop_sequence"`
	}{stopReason(finish), nil}
	if err := sse(w, "message_delta", ev{Type: "message_delta", Delta: delta, Usage: map[string]int{"output_tokens": u.CompletionTokens}}); err != nil {
		return err
	}
	return sse(w, "message_stop", ev{Type: "message_stop"})
}

func (e *messagesEncoder) StreamError(w io.Writer, status int, msg string) error {
	return sse(w, "error", errorFor(status, msg))
}
