package anthropic

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModels(t *testing.T) {
	rec := httptest.NewRecorder()
	Models(env)(rec, httptest.NewRequest("GET", "/v1/models", nil))
	b := rec.Body.String()
	if !strings.Contains(b, `{"type":"model","id":"mock-gpt","display_name":"mock-gpt","created_at":"2026-01-01T00:00:00Z"}`) ||
		!strings.Contains(b, `"has_more":false`) || !strings.Contains(b, `"first_id":"mock-gpt"`) || !strings.Contains(b, `"last_id":"mock-claude"`) {
		t.Fatal(b)
	}
}
