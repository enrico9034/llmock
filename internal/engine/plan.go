package engine

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultTokens = 100
	MaxTokens     = 1_000_000
)

// Request carries what BuildPlan needs from a decoded API request.
type Request struct {
	Header    string   // raw X-Mock-Directives header value
	Scope     []string // texts scanned for [[mock]] blocks, in precedence order (system first, last user message last)
	Input     []string // all input text, for the prompt-token estimate
	MaxTokens int      // native max_tokens cap; <= 0 means no cap
	Seed      *int64   // native body seed, used when there is no seed directive
}

// Layers are the server-side directive layers around the per-request ones.
type Layers struct{ Defaults, Overrides Directives }

// End says how a response stops after Stop() tokens.
type End int

const (
	EndDone  End = iota // all tokens emitted, normal finish
	EndError            // in-stream (or post-delay, non-stream) error after n tokens
	EndAbort            // fail_after: drop the connection
	EndHang             // hang_after: go silent until the client or server gives up
)

// Plan is the fully resolved behavior for one request.
type Plan struct {
	Tokens, PromptTokens             int
	Delay                            time.Duration
	TPS, Jitter                      float64
	Chunk                            int
	ErrorCode                        int // 0 = no error for this request (error_rate already rolled)
	ErrorAfter, FailAfter, HangAfter int // -1 = unset
	Hang                             bool
	FinishReason                     string // "stop" | "length" | "content_filter"
	Seed                             int64
	Seeded                           bool
}

// BuildPlan merges Defaults < header < scope blocks (in order) < Overrides
// and validates the result.
func BuildPlan(req Request, l Layers) (Plan, error) {
	d := l.Defaults
	h, err := ParseDirectives(req.Header)
	if err != nil {
		return Plan{}, err
	}
	d = d.Merge(h)
	for _, s := range req.Scope {
		sd, err := ExtractDirectives(s)
		if err != nil {
			return Plan{}, err
		}
		d = d.Merge(sd)
	}
	d = d.Merge(l.Overrides)

	p := Plan{Tokens: DefaultTokens, Chunk: 1, ErrorAfter: -1, FailAfter: -1, HangAfter: -1, FinishReason: "stop"}
	if req.Seed != nil {
		p.Seed, p.Seeded = *req.Seed, true
	}
	if v, ok := d["seed"]; ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return Plan{}, fmt.Errorf("seed: %q is not an integer", v)
		}
		p.Seed, p.Seeded = n, true
	}

	if v, ok := d["tokens"]; ok {
		lo, hi, err := parseTokens(v)
		if err != nil {
			return Plan{}, err
		}
		p.Tokens = lo
		if hi > lo {
			if p.Seeded {
				p.Tokens = lo + rand.New(rand.NewPCG(uint64(p.Seed), 0)).IntN(hi-lo+1)
			} else {
				p.Tokens = lo + rand.IntN(hi-lo+1)
			}
		}
	}
	if v, ok := d["prompt_tokens"]; ok {
		if p.PromptTokens, err = parseInt("prompt_tokens", v, 0, 1<<31-1); err != nil {
			return Plan{}, err
		}
	} else {
		p.PromptTokens = EstimateTokens(req.Input)
	}
	if v, ok := d["delay"]; ok {
		if p.Delay, err = time.ParseDuration(v); err != nil || p.Delay < 0 {
			return Plan{}, fmt.Errorf("delay: %q is not a non-negative duration", v)
		}
	}
	if v, ok := d["tps"]; ok {
		if p.TPS, err = parseFloat("tps", v, 0, 1e12); err != nil {
			return Plan{}, err
		}
	}
	if v, ok := d["jitter"]; ok {
		if p.Jitter, err = parseFloat("jitter", v, 0, 1); err != nil {
			return Plan{}, err
		}
	}
	if v, ok := d["chunk"]; ok {
		if p.Chunk, err = parseInt("chunk", v, 1, 1<<31-1); err != nil {
			return Plan{}, err
		}
	}
	for k, dst := range map[string]*int{"error_after": &p.ErrorAfter, "fail_after": &p.FailAfter, "hang_after": &p.HangAfter} {
		if v, ok := d[k]; ok {
			if *dst, err = parseInt(k, v, 0, 1<<31-1); err != nil {
				return Plan{}, err
			}
		}
	}
	_, p.Hang = d["hang"]
	if v, ok := d["finish_reason"]; ok {
		if v != "stop" && v != "length" && v != "content_filter" {
			return Plan{}, fmt.Errorf("finish_reason: %q must be stop, length or content_filter", v)
		}
		p.FinishReason = v
	}

	code := 500
	if v, ok := d["error"]; ok {
		if code, err = parseInt("error", v, 400, 599); err != nil {
			return Plan{}, err
		}
	}
	if v, ok := d["error_rate"]; ok {
		rate, err := parseFloat("error_rate", v, 0, 1)
		if err != nil {
			return Plan{}, err
		}
		if rand.Float64() < rate { // global source on purpose: ignores seed
			p.ErrorCode = code
		}
	} else if _, hasErr := d["error"]; hasErr || p.ErrorAfter >= 0 {
		p.ErrorCode = code
	}

	if req.MaxTokens > 0 && p.Tokens > req.MaxTokens {
		p.Tokens = req.MaxTokens
		if _, explicit := d["finish_reason"]; !explicit {
			p.FinishReason = "length"
		}
	}
	return p, nil
}

func parseTokens(v string) (lo, hi int, err error) {
	a, b, isRange := strings.Cut(v, "-")
	if lo, err = parseInt("tokens", a, 0, MaxTokens); err != nil {
		return
	}
	if !isRange {
		return lo, lo, nil
	}
	if hi, err = parseInt("tokens", b, lo, MaxTokens); err != nil {
		return
	}
	return
}

func parseInt(key, v string, lo, hi int) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("%s: %q must be an integer in %d..%d", key, v, lo, hi)
	}
	return n, nil
}

func parseFloat(key, v string, lo, hi float64) (float64, error) {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < lo || f > hi || f != f {
		return 0, fmt.Errorf("%s: %q must be a number in %g..%g", key, v, lo, hi)
	}
	return f, nil
}

// Stop returns how many tokens are emitted and how the response then ends.
func (p Plan) Stop() (n int, end End) {
	n, end = p.Tokens, EndDone
	for _, c := range []struct {
		v  int
		ok bool
		e  End
	}{{p.ErrorAfter, p.ErrorCode != 0, EndError}, {p.FailAfter, true, EndAbort}, {p.HangAfter, true, EndHang}} {
		if c.ok && c.v >= 0 && c.v <= p.Tokens && (end == EndDone || c.v < n) {
			n, end = c.v, c.e
		}
	}
	return n, end
}

// At returns the offset from request start at which token i (0-based) is due, without jitter.
func (p Plan) At(i int) time.Duration {
	if p.TPS == 0 {
		return p.Delay
	}
	return p.Delay + time.Duration(float64(i)/p.TPS*float64(time.Second))
}

// String renders the plan for the X-LLMock-Plan header.
func (p Plan) String() string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	parts := []string{
		"tokens=" + strconv.Itoa(p.Tokens),
		"prompt_tokens=" + strconv.Itoa(p.PromptTokens),
		"delay=" + p.Delay.String(),
		"tps=" + f(p.TPS),
	}
	add := func(cond bool, s string) {
		if cond {
			parts = append(parts, s)
		}
	}
	add(p.Jitter != 0, "jitter="+f(p.Jitter))
	add(p.Chunk != 1, "chunk="+strconv.Itoa(p.Chunk))
	add(p.ErrorCode != 0, "error="+strconv.Itoa(p.ErrorCode))
	add(p.ErrorAfter != -1, "error_after="+strconv.Itoa(p.ErrorAfter))
	add(p.FailAfter != -1, "fail_after="+strconv.Itoa(p.FailAfter))
	add(p.Hang, "hang")
	add(p.HangAfter != -1, "hang_after="+strconv.Itoa(p.HangAfter))
	add(p.FinishReason != "stop", "finish_reason="+p.FinishReason)
	add(p.Seeded, "seed="+strconv.FormatInt(p.Seed, 10))
	return strings.Join(parts, " ")
}

// EstimateTokens approximates tokens as ceil(runes/4) per text.
func EstimateTokens(texts []string) int {
	n := 0
	for _, t := range texts {
		n += (utf8.RuneCountInString(t) + 3) / 4
	}
	return n
}
