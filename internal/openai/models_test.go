package openai

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enrico9034/llmock/internal/engine"
)

func TestModels(t *testing.T) {
	rec := httptest.NewRecorder()
	Models(&engine.Env{Models: []string{"a", "b"}})(rec, httptest.NewRequest("GET", "/v1/models", nil))
	if !strings.Contains(rec.Body.String(), `"object":"list"`) || !strings.Contains(rec.Body.String(), `{"id":"b","object":"model","created":0,"owned_by":"llmock"}`) {
		t.Fatal(rec.Body)
	}
}
