package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/enrico9034/llmock/internal/engine"
)

func post(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
	return rec
}

// sseData returns the payload of every "data: " line, in order.
func sseData(body string) []string {
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if p, ok := strings.CutPrefix(l, "data: "); ok {
			out = append(out, p)
		}
	}
	return out
}

var env = &engine.Env{MaxBody: 1 << 20}

func TestChatNonStream(t *testing.T) {
	rec := post(Chat(env), `{"model":"m","messages":[{"role":"user","content":"hi [[mock tokens=3]]"}]}`)
	var r struct {
		ID, Object, Model string
		Choices           []struct {
			Message      struct{ Role, Content string }
			FinishReason string `json:"finish_reason"`
		}
		Usage struct {
			Prompt     int `json:"prompt_tokens"`
			Completion int `json:"completion_tokens"`
			Total      int `json:"total_tokens"`
		}
	}
	json.Unmarshal(rec.Body.Bytes(), &r)
	if rec.Code != 200 || !strings.HasPrefix(r.ID, "chatcmpl-") || len(r.ID) != 33 || r.Object != "chat.completion" || r.Model != "m" ||
		r.Choices[0].Message.Role != "assistant" || r.Choices[0].Message.Content != "Lorem ipsum dolor" ||
		r.Choices[0].FinishReason != "stop" || r.Usage.Prompt != 5 || r.Usage.Completion != 3 || r.Usage.Total != 8 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestChatStream(t *testing.T) {
	d := sseData(post(Chat(env), `{"model":"m","stream":true,"stream_options":{"include_usage":true},
		"messages":[{"role":"user","content":"[[mock tokens=2]]"}]}`).Body.String())
	if len(d) != 6 || d[5] != "[DONE]" {
		t.Fatal(d)
	}
	for _, c := range d[:4] {
		if !strings.Contains(c, `"object":"chat.completion.chunk"`) || !strings.Contains(c, `"usage":null`) {
			t.Fatal(c)
		}
	}
	if !strings.Contains(d[0], `"delta":{"role":"assistant","content":""}`) {
		t.Fatal(d[0])
	}
	if !strings.Contains(d[1], `"delta":{"content":"Lorem"}`) || !strings.Contains(d[2], `"delta":{"content":" ipsum"}`) {
		t.Fatal(d[1:3])
	}
	if !strings.Contains(d[3], `"finish_reason":"stop"`) {
		t.Fatal(d[3])
	}
	if !strings.Contains(d[4], `"choices":[]`) || !strings.Contains(d[4], `"completion_tokens":2`) {
		t.Fatal(d[4])
	}
	for _, c := range d[:5] {
		if !json.Valid([]byte(c)) {
			t.Fatal("invalid JSON: " + c)
		}
	}
}

func TestChatStreamWithoutUsage(t *testing.T) {
	d := sseData(post(Chat(env), `{"model":"m","stream":true,"messages":[{"role":"user","content":"[[mock tokens=1]]"}]}`).Body.String())
	if len(d) != 4 || d[3] != "[DONE]" || strings.Contains(strings.Join(d, ""), "usage") {
		t.Fatal(d)
	}
}

func TestChatDirectiveScope(t *testing.T) {
	rec := post(Chat(env), `{"model":"m","messages":[{"role":"system","content":"[[mock tokens=4]]"},
		{"role":"user","content":"[[mock hang]]"},{"role":"assistant","content":"x"},{"role":"user","content":"go"}]}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"completion_tokens":4`) {
		t.Fatal(rec.Body)
	}
}

func TestChatMaxTokens(t *testing.T) {
	rec := post(Chat(env), `{"model":"m","max_tokens":9,"max_completion_tokens":2,"messages":[{"role":"user","content":"[[mock tokens=5]]"}]}`)
	if !strings.Contains(rec.Body.String(), `"completion_tokens":2`) || !strings.Contains(rec.Body.String(), `"finish_reason":"length"`) {
		t.Fatal(rec.Body)
	}
}

func TestChatContentParts(t *testing.T) {
	rec := post(Chat(env), `{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}},{"type":"text","text":"[[mock tokens=2]]"}]}]}`)
	if !strings.Contains(rec.Body.String(), `"completion_tokens":2`) {
		t.Fatal(rec.Body)
	}
}

func TestChatIgnoresUnknownFields(t *testing.T) {
	rec := post(Chat(env), `{"model":"m","temperature":0.2,"top_p":1,"user":"u","metadata":{"a":"b"},"parallel_tool_calls":true,
		"response_format":{"type":"json_object"},"tools":[{"type":"function","function":{"name":"f","parameters":{}}}],
		"messages":[{"role":"user","content":"hi","name":"bob"}]}`)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
}

func TestChatRejects(t *testing.T) {
	for _, body := range []string{
		`{"model":"m","n":2,"messages":[{"role":"user","content":"hi"}]}`,
		`{"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","messages":[]}`,
		`{"model":"m","messages":[{"role":"user","content":"[[mock tokns=1]]"}]}`,
		`{bad`,
	} {
		rec := post(Chat(env), body)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"type":"invalid_request_error"`) {
			t.Errorf("%s → %d %s", body, rec.Code, rec.Body)
		}
	}
}

func TestChatInjectedError(t *testing.T) {
	rec := post(Chat(env), `{"model":"m","messages":[{"role":"user","content":"[[mock error=429]]"}]}`)
	if rec.Code != 429 || !strings.Contains(rec.Body.String(), `"code":"rate_limit_exceeded"`) || !strings.Contains(rec.Body.String(), `"type":"requests"`) {
		t.Fatal(rec.Body)
	}
}

func TestChatInStreamError(t *testing.T) {
	d := sseData(post(Chat(env), `{"model":"m","stream":true,"messages":[{"role":"user","content":"[[mock tokens=3 error=500 error_after=1]]"}]}`).Body.String())
	if len(d) != 3 || !strings.HasPrefix(d[2], `{"error":`) || slices.Contains(d, "[DONE]") {
		t.Fatal(d)
	}
}
