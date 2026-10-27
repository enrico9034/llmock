package openai

import (
	"encoding/json"
	"net/http"

	"github.com/enrico9034/llmock/internal/engine"
)

// Models serves GET /v1/models.
func Models(env *engine.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		type model struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int    `json:"created"`
			OwnedBy string `json:"owned_by"`
		}
		data := make([]model, 0, len(env.Models))
		for _, m := range env.Models {
			data = append(data, model{m, "model", 0, "llmock"})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	}
}
