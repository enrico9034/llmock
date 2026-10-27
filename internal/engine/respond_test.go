package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeEnc struct{ calls []string }

func (f *fakeEnc) WriteError(w http.ResponseWriter, s int, _ string) {
	f.calls = append(f.calls, fmt.Sprintf("error:%d", s))
	w.WriteHeader(s)
}
func (f *fakeEnc) WriteBody(w http.ResponseWriter, text, fin string, u Usage) {
	f.calls = append(f.calls, fmt.Sprintf("body:%s|%s|%d/%d", text, fin, u.PromptTokens, u.CompletionTokens))
}
func (f *fakeEnc) StreamStart(io.Writer) error { f.calls = append(f.calls, "start"); return nil }
func (f *fakeEnc) StreamChunk(_ io.Writer, s string) error {
	f.calls = append(f.calls, "chunk:"+s)
	return nil
}
func (f *fakeEnc) StreamEnd(_ io.Writer, fin string, u Usage) error {
	f.calls = append(f.calls, fmt.Sprintf("end:%s|%d/%d", fin, u.PromptTokens, u.CompletionTokens))
	return nil
}
func (f *fakeEnc) StreamError(_ io.Writer, s int, _ string) error {
	f.calls = append(f.calls, fmt.Sprintf("serror:%d", s))
	return nil
}

// run calls Respond and reports whether it aborted with http.ErrAbortHandler.
func run(ctx context.Context, e *Env, p Plan, stream bool) (rec *httptest.ResponseRecorder, f *fakeEnc, aborted bool) {
	rec, f = httptest.NewRecorder(), &fakeEnc{}
	func() {
		defer func() {
			if r := recover(); r != nil {
				if r != http.ErrAbortHandler {
					panic(r)
				}
				aborted = true
			}
		}()
		e.Respond(ctx, rec, p, f, stream)
	}()
	return
}

func TestRespondNonStream(t *testing.T) {
	p := planOf(t, "tokens=3")
	rec, f, _ := run(context.Background(), &Env{}, p, false)
	if !slices.Equal(f.calls, []string{"body:Lorem ipsum dolor|stop|0/3"}) {
		t.Fatal(f.calls)
	}
	if rec.Header().Get("X-LLMock-Plan") != p.String() {
		t.Fatal("missing X-LLMock-Plan")
	}
}

func TestRespondNonStreamTiming(t *testing.T) {
	t0 := time.Now()
	run(context.Background(), &Env{}, planOf(t, "tokens=6 delay=50ms tps=100"), false)
	if d := time.Since(t0); d < 100*time.Millisecond || d > 200*time.Millisecond {
		t.Fatal(d)
	} // At(5) = 50ms + 5/100s
}

func TestRespondStreamSequence(t *testing.T) {
	rec, f, _ := run(context.Background(), &Env{}, planOf(t, "tokens=5 chunk=2"), true)
	want := []string{"start", "chunk:Lorem ipsum", "chunk: dolor sit", "chunk: amet,", "end:stop|0/5"}
	if !slices.Equal(f.calls, want) {
		t.Fatal(f.calls)
	}
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/event-stream" || rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatal(rec.Code, rec.Header())
	}
}

func TestRespondPreStreamError(t *testing.T) {
	t0 := time.Now()
	rec, f, _ := run(context.Background(), &Env{}, planOf(t, "error=503 delay=30ms"), true)
	if !slices.Equal(f.calls, []string{"error:503"}) || rec.Code != 503 || time.Since(t0) < 30*time.Millisecond {
		t.Fatal(f.calls, rec.Code)
	}
}

func TestRespond429SetsRetryAfter(t *testing.T) {
	rec, _, _ := run(context.Background(), &Env{}, planOf(t, "error=429"), false)
	if rec.Header().Get("Retry-After") != "1" {
		t.Fatal(rec.Header())
	}
}

func TestRespondErrorAfter(t *testing.T) {
	_, f, _ := run(context.Background(), &Env{}, planOf(t, "tokens=5 error=500 error_after=2"), true)
	if !slices.Equal(f.calls, []string{"start", "chunk:Lorem", "chunk: ipsum", "serror:500"}) {
		t.Fatal(f.calls)
	}
	_, f, _ = run(context.Background(), &Env{}, planOf(t, "tokens=5 error=500 error_after=2"), false)
	if !slices.Equal(f.calls, []string{"error:500"}) {
		t.Fatal(f.calls)
	}
}

func TestRespondFailAfterAborts(t *testing.T) {
	_, f, aborted := run(context.Background(), &Env{}, planOf(t, "tokens=5 fail_after=2"), true)
	if !aborted || !slices.Equal(f.calls, []string{"start", "chunk:Lorem", "chunk: ipsum"}) {
		t.Fatal(aborted, f.calls)
	}
}

func TestRespondHangWaitsForCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	_, f, aborted := run(ctx, &Env{}, planOf(t, "hang"), true)
	if !aborted || len(f.calls) != 0 || time.Since(t0) > 100*time.Millisecond {
		t.Fatal(aborted, f.calls)
	}
}

func TestRespondHangEndsOnStopping(t *testing.T) {
	stop := make(chan struct{})
	time.AfterFunc(30*time.Millisecond, func() { close(stop) })
	if _, _, aborted := run(context.Background(), &Env{Stopping: stop}, planOf(t, "hang"), false); !aborted {
		t.Fatal("not aborted")
	}
}

func TestRespondHangAfter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, f, aborted := run(ctx, &Env{}, planOf(t, "tokens=5 hang_after=1"), true)
	if !aborted || !slices.Equal(f.calls, []string{"start", "chunk:Lorem"}) {
		t.Fatal(aborted, f.calls)
	}
}

func TestRespondClientDisconnectStopsPromptly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	_, f, _ := run(ctx, &Env{}, planOf(t, "tokens=1000 tps=10"), true)
	if time.Since(t0) > 250*time.Millisecond || len(f.calls) > 4 {
		t.Fatal(time.Since(t0), len(f.calls))
	}
}

func TestRespondPacingNoDrift(t *testing.T) {
	t0 := time.Now()
	run(context.Background(), &Env{}, planOf(t, "tokens=50 tps=500"), true)
	if d := time.Since(t0); d < 98*time.Millisecond || d > 140*time.Millisecond {
		t.Fatal(d)
	} // At(49) = 98ms
}

func TestDecodeJSON(t *testing.T) {
	var v map[string]any
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"a":"`+strings.Repeat("x", 2048)+`"}`))
	if st, _ := DecodeJSON(httptest.NewRecorder(), r, 1024, &v); st != 413 {
		t.Fatal(st)
	}
	r = httptest.NewRequest("POST", "/", strings.NewReader(`{bad`))
	if st, _ := DecodeJSON(httptest.NewRecorder(), r, 1024, &v); st != 400 {
		t.Fatal(st)
	}
}

func TestDecodeJSONTypeErrorNamesField(t *testing.T) {
	var v struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	for body, want := range map[string]string{
		`{"model":"m","messages":"x"}`: `invalid type for "messages"`,
		`{"messages":[{"content":1}]}`: `invalid type for "messages.content"`,
		`["not","an","object"]`:        "request body must be a JSON object",
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		if st, err := DecodeJSON(httptest.NewRecorder(), r, 1024, &v); st != 400 || err == nil || err.Error() != want {
			t.Errorf("%s → %d %v", body, st, err)
		}
	}
}

func TestRespondZeroTokensStreamWaitsDelay(t *testing.T) {
	t0 := time.Now()
	_, f, _ := run(context.Background(), &Env{}, planOf(t, "tokens=0 delay=30ms"), true)
	if !slices.Equal(f.calls, []string{"start", "end:stop|0/0"}) || time.Since(t0) < 30*time.Millisecond {
		t.Fatal(f.calls, time.Since(t0))
	}
}

func TestRespondJitterStaysBounded(t *testing.T) {
	_, f, _ := run(context.Background(), &Env{}, planOf(t, "tokens=3 jitter=1"), true) // tps=0: jitter must not apply
	if len(f.calls) != 5 {
		t.Fatal(f.calls)
	}
	t0 := time.Now()
	run(context.Background(), &Env{}, planOf(t, "tokens=50 tps=500 jitter=1"), true)
	if d := time.Since(t0); d < 96*time.Millisecond || d > 140*time.Millisecond {
		t.Fatal(d)
	} // At(49) = 98ms, last token ±1ms
}
