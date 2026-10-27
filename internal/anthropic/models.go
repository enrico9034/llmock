package anthropic

import (
	"encoding/json"
	"net/http"

	"github.com/enrico9034/llmock/internal/engine"
)

// Models serves GET /v1/models in Anthropic format.
func Models(env *engine.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		type model struct {
			Type        string `json:"type"`
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			CreatedAt   string `json:"created_at"`
		}
		data := make([]model, 0, len(env.Models))
		for _, m := range env.Models {
			data = append(data, model{"model", m, m, "2026-01-01T00:00:00Z"})
		}
		var first, last string
		if n := len(env.Models); n > 0 {
			first, last = env.Models[0], env.Models[n-1]
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": data, "has_more": false, "first_id": first, "last_id": last})
	}
}
