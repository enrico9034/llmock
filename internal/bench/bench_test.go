package bench

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/enrico9034/llmock/internal/server"
)

func mock(t *testing.T, overrides string) string {
	t.Helper()
	h, err := server.New(server.Config{Overrides: overrides, MaxBody: 1 << 20, Models: []string{"m"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s.URL
}

func runOK(t *testing.T, cfg Config) *Report {
	t.Helper()
	if cfg.Model == "" {
		cfg.Model = "m"
	}
	if cfg.Prompt == "" {
		cfg.Prompt = "Hello"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	r, err := Run(context.Background(), cfg, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBenchOpenAIStream(t *testing.T) {
	r := runOK(t, Config{URL: mock(t, ""), API: "openai", Users: 4, Requests: 20, Stream: true, Directives: "tokens=10 tps=1000"})
	if r.Requests != 20 || r.OK != 20 || r.IntegrityChecked != 20 || r.IntegrityMismatches != 0 || r.TokensMean != 10 {
		t.Fatalf("%+v", r)
	}
	if r.TTFT.P50 <= 0 || r.ITL.P50 < 0.5 || r.ITL.P50 > 5 {
		t.Fatalf("ttft %v itl %v", r.TTFT, r.ITL)
	}
}

func TestBenchAnthropicNonStream(t *testing.T) {
	r := runOK(t, Config{URL: mock(t, "") + "/v1/", API: "anthropic", Users: 2, Requests: 10, Directives: "tokens=10"})
	if r.OK != 10 || r.TokensMean != 10 || r.IntegrityMismatches != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestBenchCountsErrors(t *testing.T) {
	r := runOK(t, Config{URL: mock(t, ""), API: "openai", Users: 2, Requests: 10, Directives: "error=429"})
	if r.Failed != 10 || r.ErrorsByStatus["429"] != 10 || r.ErrorsByKind["http"] != 10 {
		t.Fatalf("%+v", r)
	}
}

func TestBenchDetectsTruncation(t *testing.T) {
	r := runOK(t, Config{URL: mock(t, ""), API: "openai", Users: 2, Requests: 6, Stream: true, Directives: "tokens=10 fail_after=5"})
	if r.ErrorsByKind["truncated"] != 6 {
		t.Fatalf("%+v", r)
	}
}

func TestBenchStreamError(t *testing.T) {
	r := runOK(t, Config{URL: mock(t, ""), API: "anthropic", Users: 2, Requests: 6, Stream: true, Directives: "tokens=10 error=529 error_after=2"})
	if r.ErrorsByKind["stream_error"] != 6 {
		t.Fatalf("%+v", r)
	}
}

func TestBenchIntegrityMismatch(t *testing.T) {
	r := runOK(t, Config{URL: mock(t, "tokens=9"), API: "openai", Users: 2, Requests: 6, Stream: true, Directives: "tokens=10"})
	if r.IntegrityChecked != 6 || r.IntegrityMismatches != 6 {
		t.Fatalf("%+v", r)
	}
}

func TestBenchDurationMode(t *testing.T) {
	r := runOK(t, Config{URL: mock(t, ""), API: "openai", Users: 2, Duration: 300 * time.Millisecond, Directives: "tokens=1"})
	if r.Requests == 0 || r.ElapsedSeconds < 0.3 || r.ElapsedSeconds > 1 {
		t.Fatalf("%+v", r)
	}
}

func TestBenchCancelReturnsPartial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	r, err := Run(ctx, Config{URL: mock(t, ""), API: "openai", Model: "m", Prompt: "x", Users: 3, Duration: 10 * time.Second,
		Directives: "hang", Timeout: time.Minute}, io.Discard)
	if err != nil || r == nil || r.Requests != 0 || time.Since(t0) > time.Second {
		t.Fatal(err, r, time.Since(t0))
	}
}

func TestBenchRejectsBadConfig(t *testing.T) {
	bad := []Config{
		{URL: "http://x", API: "x", Users: 1, Requests: 1},
		{URL: "http://x", API: "openai", Users: 1, Requests: 1, Duration: time.Second},
		{URL: "http://x", API: "openai", Users: 1},
		{URL: "http://x", API: "openai", Users: 0, Requests: 1},
		{URL: "http://x", API: "openai", Users: 1, Requests: 1, Directives: "tokns=1"},
	}
	for _, c := range bad {
		if _, err := Run(context.Background(), c, io.Discard); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
}

func TestEndpointAndSuffix(t *testing.T) {
	cases := map[string]string{"http://h:4000": "http://h:4000/v1/chat/completions", "http://h:4000/v1/": "http://h:4000/v1/chat/completions"}
	for in, want := range cases {
		if got := endpoint(Config{URL: in, API: "openai"}); got != want {
			t.Errorf("%s → %s", in, got)
		}
	}
	if got := endpoint(Config{URL: "http://h", API: "anthropic"}); got != "http://h/v1/messages" {
		t.Error(got)
	}
	if s := directiveSuffix(Config{Directives: "tokens=5", Seed: 42, Seeded: true}, 3); s != " [[mock tokens=5 seed=45]]" {
		t.Error(s)
	}
	if s := directiveSuffix(Config{}, 3); s != "" {
		t.Error(s)
	}
}

func TestBenchRampStopsWhenRequestsRunOut(t *testing.T) {
	r := runOK(t, Config{URL: mock(t, ""), API: "openai", Users: 10, Requests: 1, Ramp: 5 * time.Second, Directives: "tokens=1"})
	if r.Requests != 1 || r.ElapsedSeconds >= 1 {
		t.Fatalf("%+v", r)
	}
}

func TestBenchIntegrityCountsDeliveredWords(t *testing.T) {
	// 9 words delivered, but usage claims the 10 that were asked for.
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for range 9 {
			io.WriteString(w, `data: {"choices":[{"delta":{"content":" lorem"}}]}`+"\n\n")
		}
		io.WriteString(w, `data: {"choices":[],"usage":{"completion_tokens":10}}`+"\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(s.Close)
	r := runOK(t, Config{URL: s.URL, API: "openai", Users: 2, Requests: 4, Stream: true, Directives: "tokens=10"})
	if r.OK != 4 || r.IntegrityChecked != 4 || r.IntegrityMismatches != 4 {
		t.Fatalf("%+v", r)
	}
}

func TestBenchAnthropicStreamIntegrity(t *testing.T) {
	r := runOK(t, Config{URL: mock(t, ""), API: "anthropic", Users: 2, Requests: 6, Stream: true, Directives: "tokens=10 tps=1000"})
	if r.OK != 6 || r.IntegrityChecked != 6 || r.IntegrityMismatches != 0 || r.TokensMean != 10 {
		t.Fatalf("%+v", r)
	}
}

func TestWordCount(t *testing.T) {
	var w wordCount
	for _, c := range []string{`"Lor"`, `"em ipsum\n"`, `""`, `" "`, `"dolor\tsit"`, `null`, `" \"amet\""`} {
		var tx text
		if err := tx.UnmarshalJSON([]byte(c)); err != nil {
			t.Fatal(err)
		}
		w.add(tx)
	}
	if w.n != 5 {
		t.Fatal(w.n)
	}
}
