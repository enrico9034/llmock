package server

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func srv(t *testing.T, cfg Config) *httptest.Server {
	t.Helper()
	if cfg.MaxBody == 0 {
		cfg.MaxBody = 1 << 20
	}
	if cfg.Models == nil {
		cfg.Models = []string{"mock-gpt", "mock-claude"}
	}
	h, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func do(t *testing.T, s *httptest.Server, method, path, body string, hdr ...string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, s.URL+path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func readAll(r *http.Response) string { b, _ := io.ReadAll(r.Body); return string(b) }

const chat = `{"model":"m","messages":[{"role":"user","content":"hi [[mock tokens=2]]"}]}`
const msg = `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hi [[mock tokens=2]]"}]}`

func TestHealth(t *testing.T) {
	if r := do(t, srv(t, Config{}), "GET", "/health", ""); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
}

func TestRoutesWithAndWithoutV1(t *testing.T) {
	s := srv(t, Config{})
	for path, body := range map[string]string{"/v1/chat/completions": chat, "/chat/completions": chat, "/v1/messages": msg, "/messages": msg} {
		if r := do(t, s, "POST", path, body); r.StatusCode != 200 {
			t.Errorf("%s → %d", path, r.StatusCode)
		}
	}
}

func TestModelsDispatch(t *testing.T) {
	s := srv(t, Config{})
	if b := readAll(do(t, s, "GET", "/v1/models", "", "anthropic-version", "2023-06-01")); !strings.Contains(b, `"has_more"`) {
		t.Fatal(b)
	}
	if b := readAll(do(t, s, "GET", "/v1/models", "")); !strings.Contains(b, `"object":"list"`) {
		t.Fatal(b)
	}
}

func TestAuth(t *testing.T) {
	s := srv(t, Config{APIKeys: []string{"k1"}})
	if r := do(t, s, "POST", "/v1/chat/completions", chat); r.StatusCode != 401 || !strings.Contains(readAll(r), `"invalid_api_key"`) {
		t.Fatal(r.StatusCode)
	}
	if r := do(t, s, "POST", "/v1/chat/completions", chat, "Authorization", "Bearer k1"); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	if r := do(t, s, "POST", "/v1/messages", msg, "x-api-key", "k1"); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	if r := do(t, s, "POST", "/v1/messages", msg, "x-api-key", "nope"); r.StatusCode != 401 || !strings.Contains(readAll(r), `"authentication_error"`) {
		t.Fatal(r.StatusCode)
	}
}

func TestRateLimit(t *testing.T) {
	s := srv(t, Config{RateLimitRPS: 1})
	do(t, s, "POST", "/v1/chat/completions", chat)
	r := do(t, s, "POST", "/v1/chat/completions", chat)
	if r.StatusCode != 429 || r.Header.Get("Retry-After") == "" || r.Header.Get("x-ratelimit-remaining-requests") != "0" ||
		r.Header.Get("anthropic-ratelimit-requests-remaining") != "0" {
		t.Fatal(r.StatusCode, r.Header)
	}
}

func TestMaxConcurrency(t *testing.T) {
	s := srv(t, Config{MaxConcurrency: 1})
	done := make(chan struct{})
	go func() {
		defer close(done)
		r, _ := http.Post(s.URL+"/v1/chat/completions", "application/json",
			strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"[[mock delay=300ms]]"}]}`))
		io.ReadAll(r.Body)
		r.Body.Close()
	}()
	time.Sleep(100 * time.Millisecond)
	if r := do(t, s, "POST", "/v1/chat/completions", chat); r.StatusCode != 429 {
		t.Fatal(r.StatusCode)
	}
	<-done
	if r := do(t, s, "POST", "/v1/chat/completions", chat); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
}

func TestAbortReleasesConcurrencySlot(t *testing.T) {
	s := srv(t, Config{MaxConcurrency: 1})
	if r, err := http.Post(s.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"[[mock fail_after=0]]"}]}`)); err == nil {
		io.ReadAll(r.Body)
		r.Body.Close()
	}
	time.Sleep(50 * time.Millisecond)
	if r := do(t, s, "POST", "/v1/chat/completions", chat); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	if b := readAll(do(t, s, "GET", "/metrics", "")); !strings.Contains(b, "llmock_in_flight_requests 0") {
		t.Fatal("in-flight gauge not released")
	}
}

func TestStreamIsFlushedIncrementally(t *testing.T) {
	s := srv(t, Config{})
	t0 := time.Now()
	r := do(t, s, "POST", "/v1/chat/completions", `{"model":"m","stream":true,"messages":[{"role":"user","content":"[[mock tokens=5 tps=10]]"}]}`)
	sc := bufio.NewScanner(r.Body)
	found := false
	for !found && sc.Scan() {
		found = strings.Contains(sc.Text(), `"content":"Lorem"`)
	}
	if !found {
		t.Fatal("stream ended before the first token") // e.g. Flush cannot reach the real writer
	}
	if d := time.Since(t0); d > 150*time.Millisecond {
		t.Fatalf("first token after %v: stream is buffered", d)
	} // full stream takes 400ms
}

func TestOverridesApply(t *testing.T) {
	if r := do(t, srv(t, Config{Overrides: "error=503"}), "POST", "/v1/chat/completions", chat); r.StatusCode != 503 {
		t.Fatal(r.StatusCode)
	}
}

func TestNewRejectsBadDefaults(t *testing.T) {
	for _, d := range []string{"tokns=1", "tokens=-1"} {
		if _, err := New(Config{Defaults: d}, nil); err == nil {
			t.Errorf("%s accepted", d)
		}
	}
}

func TestMetricsEndpoint(t *testing.T) {
	s := srv(t, Config{})
	do(t, s, "POST", "/v1/chat/completions", chat)
	if b := readAll(do(t, s, "GET", "/metrics", "")); !strings.Contains(b, `llmock_requests_total{route="POST /v1/chat/completions",status="200"}`) {
		t.Fatal(b)
	}
}

func TestBodyLimit(t *testing.T) {
	r := do(t, srv(t, Config{MaxBody: 1024}), "POST", "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"`+strings.Repeat("x", 2048)+`"}]}`)
	if r.StatusCode != 413 {
		t.Fatal(r.StatusCode)
	}
}
