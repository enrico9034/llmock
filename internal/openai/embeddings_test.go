package openai

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"slices"
	"testing"
)

type embResp struct {
	Data []struct {
		Index     int
		Embedding json.RawMessage
	}
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	}
}

func embed(t *testing.T, body string) (int, embResp) {
	rec := post(Embeddings(env), body)
	var r embResp
	json.Unmarshal(rec.Body.Bytes(), &r)
	return rec.Code, r
}

func floats(t *testing.T, raw json.RawMessage) []float32 {
	var v []float32
	json.Unmarshal(raw, &v)
	return v
}

func TestEmbeddingsDeterministicUnitVectors(t *testing.T) {
	_, a := embed(t, `{"model":"e","input":"hello"}`)
	_, b := embed(t, `{"model":"e","input":"hello"}`)
	_, c := embed(t, `{"model":"e","input":"world"}`)
	va := floats(t, a.Data[0].Embedding)
	if len(va) != 1536 || !slices.Equal(va, floats(t, b.Data[0].Embedding)) || slices.Equal(va, floats(t, c.Data[0].Embedding)) {
		t.Fatal(len(va))
	}
	var sum float64
	for _, x := range va {
		sum += float64(x) * float64(x)
	}
	if math.Abs(sum-1) > 1e-5 {
		t.Fatal(sum)
	}
}

func TestEmbeddingsDimensionsAndBase64(t *testing.T) {
	_, f := embed(t, `{"model":"e","input":"hi","dimensions":8}`)
	_, b := embed(t, `{"model":"e","input":"hi","dimensions":8,"encoding_format":"base64"}`)
	var s string
	json.Unmarshal(b.Data[0].Embedding, &s)
	raw, _ := base64.StdEncoding.DecodeString(s)
	want := floats(t, f.Data[0].Embedding)
	if len(want) != 8 || len(raw) != 32 {
		t.Fatal(len(want), len(raw))
	}
	for i := range want {
		if math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:])) != want[i] {
			t.Fatal(i)
		}
	}
}

func TestEmbeddingsInputForms(t *testing.T) {
	_, r := embed(t, `{"model":"e","input":["a","b"]}`)
	if len(r.Data) != 2 || r.Data[1].Index != 1 {
		t.Fatal(r)
	}
	_, r = embed(t, `{"model":"e","input":[[1,2,3]]}`)
	if len(r.Data) != 1 || r.Usage.PromptTokens != 3 {
		t.Fatal(r)
	}
	_, r = embed(t, `{"model":"e","input":[1,2]}`)
	if len(r.Data) != 1 || r.Usage.PromptTokens != 2 {
		t.Fatal(r)
	}
	if code, _ := embed(t, `{"model":"e","input":"x [[mock error=503]]"}`); code != 503 {
		t.Fatal(code)
	}
}
