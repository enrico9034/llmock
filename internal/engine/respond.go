package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/enrico9034/llmock/internal/metrics"
)

// Usage is the token accounting reported to the client.
type Usage struct{ PromptTokens, CompletionTokens int }

// Encoder renders engine results in a provider's wire format.
type Encoder interface {
	WriteError(w http.ResponseWriter, status int, msg string) // full error response, provider format
	WriteBody(w http.ResponseWriter, text, finish string, u Usage)
}

// StreamEncoder adds the SSE frames of a streaming response.
type StreamEncoder interface {
	Encoder
	StreamStart(w io.Writer) error                         // opening frames (role chunk / message_start…)
	StreamChunk(w io.Writer, text string) error            // one content frame
	StreamEnd(w io.Writer, finish string, u Usage) error   // closing frames incl. [DONE] / message_stop
	StreamError(w io.Writer, status int, msg string) error // in-stream error frame
}

// Env is the server-wide state every endpoint shares.
type Env struct {
	Layers   Layers
	Models   []string
	MaxBody  int64
	Stopping <-chan struct{} // closed when the server begins shutdown
}

// Respond plays plan p to w through enc: paced tokens, then the plan's ending
// (finish, error, abort or hang). Aborts panic with http.ErrAbortHandler.
func (e *Env) Respond(ctx context.Context, w http.ResponseWriter, p Plan, enc Encoder, stream bool) {
	w.Header().Set("X-LLMock-Plan", p.String())
	start := time.Now()
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	// wait sleeps until start+at on one reusable timer; false means ctx ended first.
	wait := func(at time.Duration) bool {
		d := time.Until(start.Add(at))
		if d <= 0 {
			return ctx.Err() == nil
		}
		if timer == nil {
			timer = time.NewTimer(d)
		} else {
			timer.Reset(d)
		}
		select {
		case <-timer.C:
			return true
		case <-ctx.Done():
			return false
		}
	}

	if p.Hang {
		e.hang(ctx)
	}
	if p.ErrorCode != 0 && p.ErrorAfter < 0 {
		if wait(p.Delay) {
			injectError(w, enc, p.ErrorCode)
		}
		return
	}

	n, end := p.Stop()
	sent := 0
	defer func() { metrics.Tokens.Add(float64(sent)) }()

	if !stream {
		if !wait(p.At(max(n, 1) - 1)) {
			return
		}
		switch end {
		case EndDone:
			metrics.TTFT.Observe(time.Since(start).Seconds())
			sent = n
			enc.WriteBody(w, string(AppendTokens(nil, 0, n, p.Seed, p.Seeded)), p.FinishReason, Usage{p.PromptTokens, n})
		case EndError:
			injectError(w, enc, p.ErrorCode)
		case EndAbort:
			abort()
		case EndHang:
			e.hang(ctx)
		}
		return
	}

	se := enc.(StreamEncoder)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if se.StreamStart(w) != nil || rc.Flush() != nil {
		return
	}

	var buf []byte
	for i := 0; i < n; i += p.Chunk {
		j := min(i+p.Chunk, n)
		due := p.At(j - 1)
		if p.Jitter > 0 && p.TPS > 0 && j > 1 { // global source on purpose: ignores seed
			due += time.Duration((rand.Float64() - 0.5) * p.Jitter / p.TPS * float64(time.Second))
		}
		if !wait(due) {
			return
		}
		buf = AppendTokens(buf[:0], i, j, p.Seed, p.Seeded)
		if se.StreamChunk(w, string(buf)) != nil || rc.Flush() != nil {
			return
		}
		if i == 0 {
			metrics.TTFT.Observe(time.Since(start).Seconds())
		}
		sent = j
	}
	if n == 0 && !wait(p.Delay) { // no tokens: the ending still comes after delay
		return
	}

	switch end {
	case EndDone:
		se.StreamEnd(w, p.FinishReason, Usage{p.PromptTokens, n})
	case EndError:
		metrics.Injected.WithLabelValues("error").Inc()
		se.StreamError(w, p.ErrorCode, InjectedMessage(p.ErrorCode))
	case EndAbort:
		abort()
	case EndHang:
		e.hang(ctx)
	}
	rc.Flush()
}

// hang holds the request until the client leaves or the server shuts down, then drops it.
func (e *Env) hang(ctx context.Context) {
	metrics.Injected.WithLabelValues("hang").Inc()
	select {
	case <-ctx.Done():
	case <-e.Stopping:
	}
	panic(http.ErrAbortHandler)
}

func abort() {
	metrics.Injected.WithLabelValues("abort").Inc()
	panic(http.ErrAbortHandler)
}

func injectError(w http.ResponseWriter, enc Encoder, code int) {
	if code == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "1")
	}
	metrics.Injected.WithLabelValues("error").Inc()
	enc.WriteError(w, code, InjectedMessage(code))
}

// InjectedMessage is the error message for an injected status code.
func InjectedMessage(code int) string {
	return strings.TrimSpace("llmock: injected " + strconv.Itoa(code) + " " + http.StatusText(code))
}

// DecodeJSON decodes r's body (capped at limit bytes) into v. It returns 413
// for an oversized body, 400 for invalid JSON, and 0, nil on success.
func DecodeJSON(w http.ResponseWriter, r *http.Request, limit int64, v any) (status int, err error) {
	err = json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v)
	var tooBig *http.MaxBytesError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &tooBig):
		return http.StatusRequestEntityTooLarge, err
	default:
		return http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err)
	}
}
