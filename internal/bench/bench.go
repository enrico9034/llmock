// Package bench is a closed-loop load generator for OpenAI and Anthropic APIs.
package bench

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/enrico9034/llmock/internal/engine"
)

// stats is one virtual user's private tally, merged into the Report at the end.
type stats struct {
	latency, ttft, itl     hist
	ok, failed             int
	byStatus, byKind       map[string]int
	tokens, tokMin, tokMax int
	checked, mismatches    int

	// scratch for the request in flight
	gaps []time.Duration
	buf  bytes.Buffer
}

func newStats() *stats {
	return &stats{byStatus: map[string]int{}, byKind: map[string]int{}, tokMin: math.MaxInt}
}

func (s *stats) record(r result, expected int) {
	if r.kind != "" {
		s.failed++
		s.byKind[r.kind]++
		if r.kind == "http" {
			s.byStatus[strconv.Itoa(r.status)]++
		}
		return
	}
	s.ok++
	s.latency.add(r.latency)
	if r.ttft > 0 {
		s.ttft.add(r.ttft)
	}
	for _, g := range s.gaps {
		s.itl.add(g)
	}
	s.tokens += r.tokens
	s.tokMin, s.tokMax = min(s.tokMin, r.tokens), max(s.tokMax, r.tokens)
	if expected >= 0 { // check what was delivered, and what was claimed
		s.checked++
		if r.words != expected || r.usage && r.tokens != expected {
			s.mismatches++
		}
	}
}

func (s *stats) merge(o *stats) {
	s.latency.merge(&o.latency)
	s.ttft.merge(&o.ttft)
	s.itl.merge(&o.itl)
	s.ok += o.ok
	s.failed += o.failed
	for k, v := range o.byStatus {
		s.byStatus[k] += v
	}
	for k, v := range o.byKind {
		s.byKind[k] += v
	}
	s.tokens += o.tokens
	s.tokMin, s.tokMax = min(s.tokMin, o.tokMin), max(s.tokMax, o.tokMax)
	s.checked += o.checked
	s.mismatches += o.mismatches
}

// expectedTokens is the count a fixed tokens=N directive promises after the
// max_tokens cap, or -1 when the directives fix none.
func expectedTokens(cfg Config, d engine.Directives) int {
	n, err := strconv.Atoi(d["tokens"]) // fails when absent or a range
	if err != nil {
		return -1
	}
	if m := maxTokens(cfg); m > 0 {
		n = min(n, m)
	}
	return n
}

// Run drives cfg.Users closed-loop virtual users until cfg.Requests have been
// sent or cfg.Duration has passed, writing a progress line to progress every
// second. Cancelling ctx ends the run early: requests in flight are dropped
// and the report covers the completed ones.
func Run(ctx context.Context, cfg Config, progress io.Writer) (*Report, error) {
	switch {
	case cfg.URL == "":
		return nil, errors.New("bench: url is required")
	case cfg.API != "openai" && cfg.API != "anthropic":
		return nil, fmt.Errorf("bench: api %q must be openai or anthropic", cfg.API)
	case cfg.Users < 1:
		return nil, errors.New("bench: users must be at least 1")
	case (cfg.Duration > 0) == (cfg.Requests > 0):
		return nil, errors.New("bench: set exactly one of duration and requests")
	}
	d, err := engine.ParseDirectives(cfg.Directives)
	if err != nil {
		return nil, fmt.Errorf("bench: directives: %w", err)
	}

	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns, tr.MaxIdleConnsPerHost, tr.MaxConnsPerHost, tr.DisableCompression = cfg.Users, cfg.Users, 0, true
	defer tr.CloseIdleConnections()
	rn := &runner{cfg: cfg, client: &http.Client{Transport: tr}, url: endpoint(cfg), expected: expectedTokens(cfg, d)}

	var next, ok, failed, active atomic.Int64
	start := time.Now()
	deadline := start.Add(cfg.Duration)
	// stop releases users still waiting to ramp up once the run is over.
	stop := make(chan struct{})
	halt := sync.OnceFunc(func() { close(stop) })
	if cfg.Duration > 0 {
		defer time.AfterFunc(cfg.Duration, halt).Stop()
	}
	// ponytail: ~60 KB of histograms per user (600 MB virtual at 10k users); shard stats if that bites
	users := make([]*stats, cfg.Users)
	var wg sync.WaitGroup
	for u := range users {
		s := newStats()
		users[u] = s
		wg.Go(func() {
			select {
			case <-time.After(time.Duration(u) * cfg.Ramp / time.Duration(cfg.Users)):
			case <-ctx.Done():
				return
			case <-stop:
				return
			}
			active.Add(1)
			defer active.Add(-1)
			for ctx.Err() == nil {
				idx := int(next.Add(1) - 1)
				if cfg.Requests > 0 && idx >= cfg.Requests || cfg.Duration > 0 && !time.Now().Before(deadline) {
					halt()
					return
				}
				r, counted := rn.do(ctx, idx, s)
				if !counted {
					return
				}
				s.record(r, rn.expected)
				if r.kind == "" {
					ok.Add(1)
				} else {
					failed.Add(1)
				}
			}
		})
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastT, lastN := start, int64(0)
	for running := true; running; {
		select {
		case <-done:
			running = false
		case now := <-tick.C:
			o, f := ok.Load(), failed.Load()
			fmt.Fprintf(progress, "[%4ds] users=%d rps=%.0f ok=%d failed=%d\n", int(now.Sub(start).Round(time.Second).Seconds()),
				active.Load(), float64(o+f-lastN)/now.Sub(lastT).Seconds(), o, f)
			lastT, lastN = now, o+f
		}
	}
	elapsed := time.Since(start).Seconds()

	total := newStats()
	for _, s := range users {
		total.merge(s)
	}
	r := &Report{Config: cfg, StartedAt: start, ElapsedSeconds: elapsed,
		Requests: total.ok + total.failed, OK: total.ok, Failed: total.failed,
		ErrorsByStatus: total.byStatus, ErrorsByKind: total.byKind,
		Latency: percentiles(&total.latency), TTFT: percentiles(&total.ttft), ITL: percentiles(&total.itl),
		IntegrityChecked: total.checked, IntegrityMismatches: total.mismatches}
	if elapsed > 0 {
		r.RPS, r.OutputTPS = float64(r.Requests)/elapsed, float64(total.tokens)/elapsed
	}
	if total.ok > 0 {
		r.TokensMean, r.TokensMin, r.TokensMax = float64(total.tokens)/float64(total.ok), total.tokMin, total.tokMax
	}
	return r, nil
}
