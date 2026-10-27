package openai

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"net/http"

	"github.com/enrico9034/llmock/internal/engine"
)

const (
	defaultDimensions = 1536
	maxDimensions     = 65536
)

type embeddingRequest struct {
	Model          string          `json:"model"`
	Input          json.RawMessage `json:"input"`
	Dimensions     int             `json:"dimensions"`
	EncodingFormat string          `json:"encoding_format"`
}

// Embeddings serves POST /v1/embeddings with deterministic unit vectors.
func Embeddings(env *engine.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req embeddingRequest
		if status, err := engine.DecodeJSON(w, r, env.MaxBody, &req); err != nil {
			WriteError(w, status, err.Error())
			return
		}
		if req.Dimensions == 0 {
			req.Dimensions = defaultDimensions
		}
		// items are the byte strings each vector is seeded from; texts are the
		// string inputs (the only ones scanned for directives).
		var items [][]byte
		var texts []string
		tokens := 0
		if ss, ok := stringOrStrings(req.Input); ok {
			texts = ss
			for _, s := range ss {
				items = append(items, []byte(s))
			}
		} else if ids, ok := tokenArrays(req.Input); ok {
			for _, a := range ids {
				tokens += len(a)
				b, _ := json.Marshal(a)
				items = append(items, b)
			}
		}
		switch {
		case req.Model == "":
			WriteError(w, 400, "model is required")
			return
		case len(items) == 0:
			WriteError(w, 400, "input must be a string, an array of strings, or token arrays")
			return
		case req.Dimensions < 1 || req.Dimensions > maxDimensions:
			WriteError(w, 400, "dimensions out of range")
			return
		}

		plan, err := engine.BuildPlan(engine.Request{Header: r.Header.Get("X-Mock-Directives"), Scope: texts, Input: texts}, env.Layers)
		if err != nil {
			WriteError(w, 400, err.Error())
			return
		}
		plan.Tokens = 0
		if texts == nil {
			plan.PromptTokens = tokens
		}
		env.Respond(r.Context(), w, plan, &embeddingEncoder{model: req.Model, items: items, dims: req.Dimensions, b64: req.EncodingFormat == "base64"}, false)
	}
}

// tokenArrays decodes []int (one array) or [][]int.
func tokenArrays(raw json.RawMessage) ([][]int, bool) {
	var flat []int
	if json.Unmarshal(raw, &flat) == nil && len(flat) > 0 {
		return [][]int{flat}, true
	}
	var nested [][]int
	return nested, json.Unmarshal(raw, &nested) == nil
}

type embeddingEncoder struct {
	model string
	items [][]byte
	dims  int
	b64   bool
}

func (e *embeddingEncoder) WriteError(w http.ResponseWriter, status int, msg string) {
	WriteError(w, status, msg)
}

func (e *embeddingEncoder) WriteBody(w http.ResponseWriter, _, _ string, u engine.Usage) {
	data := make([]any, len(e.items))
	for i, in := range e.items {
		v := vector(in, e.dims)
		var emb any = v
		if e.b64 {
			buf := make([]byte, 4*len(v))
			for j, x := range v {
				binary.LittleEndian.PutUint32(buf[j*4:], math.Float32bits(x))
			}
			emb = base64.StdEncoding.EncodeToString(buf)
		}
		data[i] = map[string]any{"object": "embedding", "index": i, "embedding": emb}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"object": "list", "data": data, "model": e.model,
		"usage": map[string]int{"prompt_tokens": u.PromptTokens, "total_tokens": u.PromptTokens},
	})
}

// vector derives a unit-length vector from the input bytes.
func vector(in []byte, dims int) []float32 {
	h := fnv.New64a()
	h.Write(in)
	rng := rand.New(rand.NewPCG(h.Sum64(), 0))
	f := make([]float64, dims)
	var sum float64
	for i := range f {
		f[i] = rng.Float64()*2 - 1
		sum += f[i] * f[i]
	}
	norm := math.Sqrt(sum)
	out := make([]float32, dims)
	for i, x := range f {
		out[i] = float32(x / norm)
	}
	return out
}
