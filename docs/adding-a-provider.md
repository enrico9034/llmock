# Adding a provider

LLMock separates *what* happens from *how it looks on the wire*. The
provider-neutral engine (`internal/engine`) turns directives into a plan and
plays it: pacing, delays, errors, aborts, hangs, the `X-LLMock-Plan` header
and the TTFT, token and injected-failure metrics. A provider adapter only
decodes its request format and encodes the engine's output in its own wire
format. Adding an API (say, Gemini) is one new package plus a few lines in
`server.New`; the engine, directives, auth, rate limiting, metrics and the
other providers do not change.

The steps below build a toy provider `acme` with one endpoint,
`POST /v1/generate` (snippets omit imports). `internal/anthropic` is the
reference to copy from.

## 1. Decode the request into `engine.Request`

Decode with `engine.DecodeJSON`, which caps the body at `env.MaxBody` and
returns `413` or `400` on failure. Never use `DisallowUnknownFields`: real
clients send fields LLMock does not know, and they must be ignored.

Fill `engine.Request`:

| Field | What to put in it |
|-------|-------------------|
| `Header` | the raw `X-Mock-Directives` header value |
| `Scope` | the texts scanned for `[[mock ...]]` blocks, lowest precedence first: the system prompt(s), then the **last user message** |
| `Input` | every input text, for the prompt-token estimate |
| `MaxTokens` | the native output cap (`0` = none) |
| `Seed` | the native body seed, if the API has one (`nil` otherwise) |

`engine.ContentText` reads a content field that is either a string or an
array of `{"type":"text"}` parts or blocks. Then call `engine.BuildPlan`;
its error is a directive error and becomes a `400`.

```go
// Package acme implements the Acme-compatible endpoints.
package acme

type generateRequest struct {
	Model     string          `json:"model"`
	Stream    bool            `json:"stream"`
	MaxTokens int             `json:"max_tokens"`
	System    json.RawMessage `json:"system"`
	Messages  []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

// Generate serves POST /v1/generate.
func Generate(env *engine.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req generateRequest
		if status, err := engine.DecodeJSON(w, r, env.MaxBody, &req); err != nil {
			WriteError(w, status, err.Error())
			return
		}
		if req.Model == "" {
			WriteError(w, 400, "model is required")
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
		plan, err := engine.BuildPlan(er, env.Layers)
		if err != nil {
			WriteError(w, 400, err.Error())
			return
		}
		env.Respond(r.Context(), w, plan, &encoder{model: req.Model}, req.Stream)
	}
}
```

## 2. Implement `engine.StreamEncoder`

`env.Respond` drives the encoder. It does all the timing; the encoder only
writes bytes.

```go
type Encoder interface {
	WriteError(w http.ResponseWriter, status int, msg string) // full error response, provider format
	WriteBody(w http.ResponseWriter, text, finish string, u Usage)
}

type StreamEncoder interface {
	Encoder
	StreamStart(w io.Writer) error                         // opening frames (role chunk / message_start…)
	StreamChunk(w io.Writer, text string) error            // one content frame
	StreamEnd(w io.Writer, finish string, u Usage) error   // closing frames incl. [DONE] / message_stop
	StreamError(w io.Writer, status int, msg string) error // in-stream error frame
}
```

- `WriteBody` writes the whole non-streaming response: status 200, headers
  and body. `text` is the full completion.
- For a stream, `Respond` sets `Content-Type: text/event-stream`, sends the
  200 headers, then calls `StreamStart` once, `StreamChunk` once per frame
  (`chunk` tokens of text, the first without a leading space), and finally
  either `StreamEnd` or `StreamError`. It flushes after each call. Return
  write errors: that is how `Respond` notices a client that went away.
- `finish` is `stop`, `length` or `content_filter`; map it to the
  provider's names (Anthropic: `end_turn`, `max_tokens`, `refusal`).
- `Usage` carries `PromptTokens` and `CompletionTokens`; report them in the
  provider's usage fields.
- `StreamChunk` is the hot path, called once per frame for every stream:
  keep it free of per-call setup (the OpenAI chat encoder pre-renders the
  frame prefix once per request).
- An endpoint that never streams (like OpenAI embeddings) only needs
  `Encoder` and always passes `stream=false` to `Respond`.

```go
type encoder struct{ model string }

func (e *encoder) WriteError(w http.ResponseWriter, status int, msg string) {
	WriteError(w, status, msg)
}

func (e *encoder) WriteBody(w http.ResponseWriter, text, finish string, u engine.Usage) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"model": e.model, "text": text, "finish_reason": finish,
		"usage": map[string]int{"input_tokens": u.PromptTokens, "output_tokens": u.CompletionTokens}})
}

func sse(w io.Writer, v any) error {
	b, _ := json.Marshal(v)
	_, err := fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}

func (e *encoder) StreamStart(w io.Writer) error { return sse(w, map[string]string{"model": e.model}) }

func (e *encoder) StreamChunk(w io.Writer, text string) error {
	return sse(w, map[string]string{"text": text})
}

func (e *encoder) StreamEnd(w io.Writer, finish string, u engine.Usage) error {
	return sse(w, map[string]any{"finish_reason": finish, "output_tokens": u.CompletionTokens})
}

func (e *encoder) StreamError(w io.Writer, status int, msg string) error {
	return sse(w, errorBody(status, msg))
}
```

## 3. Provide `WriteError`

Every error a client of this provider sees must use the provider's error
format: request validation, body-size and directive errors, injected errors,
and the guard's `401` and `429` responses. Export a
`WriteError(w http.ResponseWriter, status int, msg string)` that maps the
status to the provider's error type, sets the status and writes the body.
`server.New` hands it to the auth and rate-limit guard.

```go
func errorBody(status int, msg string) map[string]any {
	typ := "invalid_request"
	switch {
	case status == 401:
		typ = "unauthenticated"
	case status == 429:
		typ = "rate_limited"
	case status >= 500:
		typ = "internal"
	}
	return map[string]any{"error": map[string]any{"code": status, "type": typ, "message": msg}}
}

// WriteError writes an Acme-format error response.
func WriteError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorBody(status, msg))
}
```

## 4. Register the routes in `server.New`

Wrap each handler with the guard, passing the provider's `WriteError`, and
register it with `api`, which adds the metrics and the access log. Register
every route with and without the `/v1` prefix:

```go
acm := func(h http.Handler) http.Handler { return g.wrap(acme.WriteError, h) }
...
for _, p := range []string{"/v1", ""} {
	...
	api("POST "+p+"/generate", acm(acme.Generate(env)))
}
```

If the new API shares a path with an existing one, dispatch on something the
clients always send, as `GET /v1/models` does with the `anthropic-version`
header.

## 5. Add wire-format tests

Model them on `internal/anthropic/messages_test.go`: call the handler
directly with `httptest.NewRecorder` and a test `engine.Env`, and assert on
the exact bytes a client parses. Cover at least:

- the non-streaming body: every field a client reads, the usage and the
  finish reason;
- the full stream: every frame in order, the first content frame without a
  leading space, the closing frames and the usage;
- `max_tokens` capping (`[[mock tokens=3]]` with `max_tokens: 1`) and the
  mapped finish reason;
- error bodies for a directive error (`[[mock tokns=1]]` → 400), a few
  injected statuses (`[[mock error=429]]`, `[[mock error=503]]`) and an
  in-stream error (`[[mock tokens=3 error_after=1]]`);
- a request full of fields LLMock does not know, which must still get a 200.

```go
var env = &engine.Env{MaxBody: 1 << 20}

func TestGenerateStream(t *testing.T) {
	rec := httptest.NewRecorder()
	Generate(env)(rec, httptest.NewRequest("POST", "/v1/generate",
		strings.NewReader(`{"model":"m","stream":true,"messages":[{"role":"user","content":"[[mock tokens=2]]"}]}`)))
	want := `data: {"model":"m"}` + "\n\n" + `data: {"text":"Lorem"}` + "\n\n" + `data: {"text":" ipsum"}` + "\n\n" +
		`data: {"finish_reason":"stop","output_tokens":2}` + "\n\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("got %q", got)
	}
}
```

Finally, if the provider has an official SDK, prove compatibility with it,
as [`examples/sdk/smoke.py`](../examples/sdk/smoke.py) does for OpenAI and
Anthropic. `llmock bench` speaks only the OpenAI and Anthropic APIs;
teaching it a new one is a separate change in `internal/bench`.
