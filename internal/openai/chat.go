package openai

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/enrico9034/llmock/internal/engine"
)

type chatRequest struct {
	Model               string `json:"model"`
	Stream              bool   `json:"stream"`
	N                   int    `json:"n"`
	MaxTokens           int    `json:"max_tokens"`
	MaxCompletionTokens int    `json:"max_completion_tokens"`
	Seed                *int64 `json:"seed"`
	StreamOptions       struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

// Chat serves POST /v1/chat/completions.
func Chat(env *engine.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
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
		case req.N > 1:
			WriteError(w, 400, "n > 1 is not supported")
			return
		}

		er := engine.Request{Header: r.Header.Get("X-Mock-Directives"), MaxTokens: req.MaxCompletionTokens, Seed: req.Seed}
		if er.MaxTokens <= 0 {
			er.MaxTokens = req.MaxTokens
		}
		var lastUser string
		for _, m := range req.Messages {
			text := engine.ContentText(m.Content)
			er.Input = append(er.Input, text)
			switch m.Role {
			case "system", "developer":
				er.Scope = append(er.Scope, text)
			case "user":
				lastUser = text
			}
		}
		er.Scope = append(er.Scope, lastUser)

		plan, err := engine.BuildPlan(er, env.Layers)
		if err != nil {
			WriteError(w, 400, err.Error())
			return
		}
		env.Respond(r.Context(), w, plan, newChatEncoder(req.Model, req.StreamOptions.IncludeUsage), req.Stream)
	}
}

type chatEncoder struct {
	id, model    string
	created      int64
	includeUsage bool
	base         string // chunk header up to and including system_fingerprint
	head         string // base plus the "usage":null placeholder when usage is requested
	prefix       []byte // bytes before the content string of a content chunk
}

const contentSuffix = `},"logprobs":null,"finish_reason":null}]}` + "\n\n"

func randID(prefix string) string {
	var b [12]byte
	for i := range b {
		b[i] = byte(rand.UintN(256))
	}
	return prefix + hex.EncodeToString(b[:])
}

func newChatEncoder(model string, includeUsage bool) *chatEncoder {
	e := &chatEncoder{id: randID("chatcmpl-"), model: model, created: time.Now().Unix(), includeUsage: includeUsage}
	mj, _ := json.Marshal(model)
	e.base = fmt.Sprintf(`{"id":%q,"object":"chat.completion.chunk","created":%d,"model":%s,"system_fingerprint":"fp_llmock"`, e.id, e.created, mj)
	e.head = e.base
	if includeUsage {
		e.head += `,"usage":null`
	}
	e.prefix = []byte("data: " + e.head + `,"choices":[{"index":0,"delta":{"content":`)
	return e
}

func (e *chatEncoder) WriteError(w http.ResponseWriter, status int, msg string) {
	WriteError(w, status, msg)
}

func (e *chatEncoder) WriteBody(w http.ResponseWriter, text, finish string, u engine.Usage) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id": e.id, "object": "chat.completion", "created": e.created, "model": e.model, "system_fingerprint": "fp_llmock",
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": text, "refusal": nil},
			"logprobs":      nil,
			"finish_reason": finish,
		}},
		"usage": usageJSON(u),
	})
}

func usageJSON(u engine.Usage) map[string]int {
	return map[string]int{"prompt_tokens": u.PromptTokens, "completion_tokens": u.CompletionTokens, "total_tokens": u.PromptTokens + u.CompletionTokens}
}

func (e *chatEncoder) frame(w io.Writer, tail string) error {
	_, err := io.WriteString(w, "data: "+e.head+tail+"\n\n")
	return err
}

func (e *chatEncoder) StreamStart(w io.Writer) error {
	return e.frame(w, `,"choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}`)
}

func (e *chatEncoder) StreamChunk(w io.Writer, text string) error {
	buf := append(make([]byte, 0, len(e.prefix)+len(text)+len(contentSuffix)+8), e.prefix...)
	tj, _ := json.Marshal(text)
	buf = append(buf, tj...)
	buf = append(buf, contentSuffix...)
	_, err := w.Write(buf)
	return err
}

func (e *chatEncoder) StreamEnd(w io.Writer, finish string, u engine.Usage) error {
	fj, _ := json.Marshal(finish)
	if err := e.frame(w, `,"choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":`+string(fj)+`}]}`); err != nil {
		return err
	}
	if e.includeUsage {
		uj, _ := json.Marshal(usageJSON(u))
		if _, err := io.WriteString(w, "data: "+e.base+`,"choices":[],"usage":`+string(uj)+"}\n\n"); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "data: [DONE]\n\n")
	return err
}

func (e *chatEncoder) StreamError(w io.Writer, status int, msg string) error {
	b, _ := json.Marshal(map[string]apiError{"error": errorFor(status, msg)})
	_, err := io.WriteString(w, "data: "+string(b)+"\n\n")
	return err
}
