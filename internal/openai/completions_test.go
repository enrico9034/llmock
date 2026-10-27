package openai

import (
	"strings"
	"testing"
)

func TestCompletionsNonStream(t *testing.T) {
	rec := post(Completions(env), `{"model":"m","prompt":"[[mock tokens=2]]"}`)
	if !strings.Contains(rec.Body.String(), `"object":"text_completion"`) || !strings.Contains(rec.Body.String(), `"text":"Lorem ipsum"`) ||
		!strings.Contains(rec.Body.String(), `"completion_tokens":2`) {
		t.Fatal(rec.Body)
	}
}

func TestCompletionsStream(t *testing.T) {
	d := sseData(post(Completions(env), `{"model":"m","prompt":["[[mock tokens=2]]"],"stream":true,"stream_options":{"include_usage":true}}`).Body.String())
	if len(d) != 5 || !strings.Contains(d[0], `"text":"Lorem"`) || !strings.Contains(d[2], `"finish_reason":"stop"`) ||
		!strings.Contains(d[3], `"completion_tokens":2`) || d[4] != "[DONE]" {
		t.Fatal(d)
	}
}
