package bench

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// endpoint returns the chat URL for cfg.API; cfg.URL may or may not end in /v1.
func endpoint(cfg Config) string {
	u := strings.TrimSuffix(cfg.URL, "/")
	if !strings.HasSuffix(u, "/v1") {
		u += "/v1"
	}
	if cfg.API == "anthropic" {
		return u + "/messages"
	}
	return u + "/chat/completions"
}

// directiveSuffix is the [[mock ...]] block appended to request idx's prompt.
func directiveSuffix(cfg Config, idx int) string {
	d := strings.TrimSpace(cfg.Directives)
	if cfg.Seeded {
		d = strings.TrimSpace(d + " seed=" + strconv.FormatInt(cfg.Seed+int64(idx), 10))
	}
	if d == "" {
		return ""
	}
	return " [[mock " + d + "]]"
}

// maxTokens is the max_tokens bench sends (0 = omitted); Anthropic requires one.
func maxTokens(cfg Config) int {
	if cfg.API == "anthropic" {
		return cmp.Or(cfg.MaxTokens, 4096)
	}
	return cfg.MaxTokens
}

func requestBody(cfg Config, idx int) []byte {
	b := map[string]any{
		"model":    cfg.Model,
		"messages": []map[string]string{{"role": "user", "content": cfg.Prompt + directiveSuffix(cfg, idx)}},
		"stream":   cfg.Stream,
	}
	if cfg.API == "openai" && cfg.Stream {
		b["stream_options"] = map[string]bool{"include_usage": true}
	}
	if n := maxTokens(cfg); n > 0 {
		b["max_tokens"] = n
	}
	out, _ := json.Marshal(b) // strings, ints and bools always marshal
	return out
}

// frame holds the only fields bench reads from a JSON body or SSE data line, for either API.
type frame struct {
	Type string `json:"type"` // Anthropic event type
	// OpenAI; an array, so decoding doesn't allocate a slice per frame
	Choices [1]struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		CompletionTokens int `json:"completion_tokens"` // OpenAI
		OutputTokens     int `json:"output_tokens"`     // Anthropic
	} `json:"usage"`
	Error *struct{} `json:"error"`
}

func (f *frame) usageTokens(api string) (int, bool) {
	switch {
	case f.Usage == nil:
		return 0, false
	case api == "anthropic":
		return f.Usage.OutputTokens, true
	}
	return f.Usage.CompletionTokens, true
}

// result is the outcome of one request.
type result struct {
	kind    string // "" when OK, else an ErrorsByKind key
	status  int    // HTTP status, for kind "http"
	latency time.Duration
	ttft    time.Duration // streams only; 0 when no content arrived
	tokens  int
}

// runner holds what every virtual user shares.
type runner struct {
	cfg      Config
	client   *http.Client
	url      string
	expected int // token count every OK response must carry; -1 = unchecked
}

var (
	dataPrefix = []byte("data:")
	doneMarker = []byte("[DONE]")
)

// do sends request idx, leaving its stream ITL gaps in s.gaps and using s.buf
// as scratch. It returns false when ctx was cancelled before the request completed.
func (rn *runner) do(ctx context.Context, idx int, s *stats) (result, bool) {
	s.gaps = s.gaps[:0]
	rctx, cancel := ctx, context.CancelFunc(func() {})
	if rn.cfg.Timeout > 0 { // 0 = no per-request timeout
		rctx, cancel = context.WithTimeout(ctx, rn.cfg.Timeout)
	}
	defer cancel()
	fail := func(kind string) (result, bool) {
		if ctx.Err() != nil {
			return result{}, false
		}
		if errors.Is(rctx.Err(), context.DeadlineExceeded) {
			kind = "timeout"
		}
		return result{kind: kind}, true
	}

	req, err := http.NewRequestWithContext(rctx, http.MethodPost, rn.url, bytes.NewReader(requestBody(rn.cfg, idx)))
	if err != nil {
		return fail("connect")
	}
	req.Header.Set("Content-Type", "application/json")
	if rn.cfg.API == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
		if rn.cfg.APIKey != "" {
			req.Header.Set("x-api-key", rn.cfg.APIKey)
		}
	} else if rn.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+rn.cfg.APIKey)
	}

	start := time.Now()
	resp, err := rn.client.Do(req)
	if err != nil {
		return fail("connect")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		io.Copy(io.Discard, resp.Body) // read to EOF so the connection is reused
		return result{kind: "http", status: resp.StatusCode}, true
	}

	var f frame
	if !rn.cfg.Stream {
		s.buf.Reset()
		if _, err := s.buf.ReadFrom(resp.Body); err != nil || json.Unmarshal(s.buf.Bytes(), &f) != nil {
			return fail("truncated")
		}
		n, _ := f.usageTokens(rn.cfg.API)
		return result{latency: time.Since(start), tokens: n}, true
	}

	var r result
	var last time.Time
	frames, usage, done, streamErr := 0, -1, false, false
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(nil, 1<<20) // grows from 4 KB up to 1 MB lines
	// Read to EOF even after the end marker, so the connection is reused.
	for sc.Scan() {
		data, ok := bytes.CutPrefix(sc.Bytes(), dataPrefix)
		if !ok {
			continue
		}
		if data = bytes.TrimSpace(data); bytes.Equal(data, doneMarker) {
			done = true
			continue
		}
		if f = (frame{}); json.Unmarshal(data, &f) != nil {
			continue
		}
		switch {
		case f.Error != nil:
			streamErr = true
		case f.Type == "message_stop":
			done = true
		case f.Type == "content_block_delta" || f.Choices[0].Delta.Content != "":
			now := time.Now()
			if frames == 0 {
				r.ttft = now.Sub(start)
			} else {
				s.gaps = append(s.gaps, now.Sub(last))
			}
			last = now
			frames++
		}
		if n, ok := f.usageTokens(rn.cfg.API); ok {
			usage = n
		}
	}
	switch {
	case streamErr:
		return result{kind: "stream_error"}, true
	case sc.Err() != nil || !done:
		return fail("truncated")
	}
	r.latency = time.Since(start)
	r.tokens = frames // fallback when the stream carried no usage
	if usage >= 0 {
		r.tokens = usage
	}
	return r, true
}
