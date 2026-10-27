package anthropic

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/enrico9034/llmock/internal/engine"
)

var env = &engine.Env{MaxBody: 1 << 20, Models: []string{"mock-gpt", "mock-claude"}}

func post(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body)))
	return rec
}

type frame struct{ event, data string }

func frames(body string) []frame { // pairs each "event:" line with the following "data:" line
	var out []frame
	var ev string
	for _, l := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(l, "event: "); ok {
			ev = v
		}
		if v, ok := strings.CutPrefix(l, "data: "); ok {
			out = append(out, frame{ev, v})
		}
	}
	return out
}

func TestMessagesNonStream(t *testing.T) {
	rec := post(Messages(env), `{"model":"c","max_tokens":100,"messages":[{"role":"user","content":"hi [[mock tokens=2]]"}]}`)
	b := rec.Body.String()
	for _, want := range []string{`"type":"message"`, `"role":"assistant"`, `"content":[{"type":"text","text":"Lorem ipsum"}]`,
		`"stop_reason":"end_turn"`, `"stop_sequence":null`, `"input_tokens":5`, `"output_tokens":2`, `"id":"msg_`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
}

func TestMessagesStream(t *testing.T) {
	f := frames(post(Messages(env), `{"model":"c","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"[[mock tokens=2]]"}]}`).Body.String())
	var events []string
	for _, x := range f {
		events = append(events, x.event)
		if !strings.Contains(x.data, `"type":"`+x.event+`"`) {
			t.Errorf("event %s data %s", x.event, x.data)
		}
	}
	want := []string{"message_start", "content_block_start", "ping", "content_block_delta", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if !slices.Equal(events, want) {
		t.Fatal(events)
	}
	if !strings.Contains(f[0].data, `"output_tokens":0`) || !strings.Contains(f[3].data, `"text":"Lorem"`) || !strings.Contains(f[4].data, `"text":" ipsum"`) ||
		!strings.Contains(f[6].data, `"stop_reason":"end_turn"`) || !strings.Contains(f[6].data, `"output_tokens":2`) {
		t.Fatal(f)
	}
}

func TestMessagesMaxTokensStopReason(t *testing.T) {
	b := post(Messages(env), `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"[[mock tokens=3]]"}]}`).Body.String()
	if !strings.Contains(b, `"stop_reason":"max_tokens"`) || !strings.Contains(b, `"output_tokens":1`) {
		t.Fatal(b)
	}
}

func TestMessagesSystemBlocks(t *testing.T) {
	b := post(Messages(env), `{"model":"c","max_tokens":100,"system":[{"type":"text","text":"[[mock tokens=4]]","cache_control":{"type":"ephemeral"}}],
		"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`).Body.String()
	if !strings.Contains(b, `"output_tokens":4`) {
		t.Fatal(b)
	}
}

func TestMessagesErrors(t *testing.T) {
	cases := map[string]string{"error=529": "overloaded_error", "error=429": "rate_limit_error", "tokns=1": "invalid_request_error"}
	for d, typ := range cases {
		rec := post(Messages(env), `{"model":"c","max_tokens":10,"messages":[{"role":"user","content":"[[mock `+d+`]]"}]}`)
		if !strings.Contains(rec.Body.String(), `{"type":"error","error":{"type":"`+typ+`"`) {
			t.Errorf("%s → %d %s", d, rec.Code, rec.Body)
		}
	}
}

func TestMessagesInStreamError(t *testing.T) {
	f := frames(post(Messages(env), `{"model":"c","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"[[mock tokens=3 error=529 error_after=1]]"}]}`).Body.String())
	last := f[len(f)-1]
	if last.event != "error" || !strings.Contains(last.data, "overloaded_error") || f[len(f)-2].event != "content_block_delta" {
		t.Fatal(f)
	}
}

func TestMessagesIgnoresUnknownFields(t *testing.T) {
	rec := post(Messages(env), `{"model":"c","max_tokens":10,"temperature":1,"top_k":5,"metadata":{"user_id":"u"},
		"thinking":{"type":"enabled","budget_tokens":1024},"tool_choice":{"type":"auto"},
		"tools":[{"name":"f","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
}

func TestCountTokens(t *testing.T) {
	if b := post(CountTokens(env), `{"model":"c","messages":[{"role":"user","content":"hello world"}]}`).Body.String(); b != `{"input_tokens":3}`+"\n" && b != `{"input_tokens":3}` {
		t.Fatal(b)
	}
	if b := post(CountTokens(env), `{"model":"c","messages":[{"role":"user","content":"[[mock prompt_tokens=42]]"}]}`).Body.String(); !strings.Contains(b, `"input_tokens":42`) {
		t.Fatal(b)
	}
}
