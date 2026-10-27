package server

import (
	"crypto/subtle"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// errorWriter writes an error response in one provider's format.
type errorWriter func(w http.ResponseWriter, status int, msg string)

// guard enforces auth, the simulated rate limit and the concurrency limit.
// One guard is shared by every API route.
type guard struct {
	keys    []string // empty = accept anything
	bucket  *bucket  // nil = no rate limit
	maxConc int64    // 0 = no limit
	conc    atomic.Int64
}

func (g *guard) wrap(writeErr errorWriter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.authorized(r) {
			writeErr(w, http.StatusUnauthorized, "llmock: invalid or missing API key")
			return
		}
		if g.bucket != nil {
			if retry, ok := g.bucket.take(w.Header()); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(retry))
				writeErr(w, http.StatusTooManyRequests, "llmock: rate limit exceeded")
				return
			}
		}
		if g.maxConc > 0 {
			if g.conc.Add(1) > g.maxConc {
				g.conc.Add(-1)
				w.Header().Set("Retry-After", "1")
				writeErr(w, http.StatusTooManyRequests, "llmock: too many concurrent requests")
				return
			}
			defer g.conc.Add(-1) // deferred: aborts panic out of next
		}
		next.ServeHTTP(w, r)
	})
}

func (g *guard) authorized(r *http.Request) bool {
	if len(g.keys) == 0 {
		return true
	}
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		bearer = ""
	}
	for _, c := range []string{bearer, r.Header.Get("x-api-key")} {
		for _, k := range g.keys {
			if c != "" && subtle.ConstantTimeCompare([]byte(c), []byte(k)) == 1 {
				return true
			}
		}
	}
	return false
}

// bucket is a token bucket refilled at rate tokens/s up to burst tokens.
type bucket struct {
	mu          sync.Mutex
	rate, burst float64
	tokens      float64
	last        time.Time
}

func newBucket(rps float64) *bucket {
	burst := math.Ceil(rps)
	return &bucket{rate: rps, burst: burst, tokens: burst, last: time.Now()}
}

// take spends one token if one is available and sets both providers'
// rate-limit headers on h. retry is the whole seconds until the next token
// (at least 1), meaningful when ok is false.
func (b *bucket) take(h http.Header) (retry int, ok bool) {
	b.mu.Lock()
	now := time.Now()
	b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	if ok = b.tokens >= 1; ok {
		b.tokens--
	}
	tokens := b.tokens
	b.mu.Unlock()

	wait := func(n float64) time.Duration { return time.Duration(n / b.rate * float64(time.Second)) }
	full := wait(b.burst - tokens)
	limit, left := strconv.Itoa(int(b.burst)), strconv.Itoa(int(tokens))
	h.Set("x-ratelimit-limit-requests", limit)
	h.Set("x-ratelimit-remaining-requests", left)
	h.Set("x-ratelimit-reset-requests", full.Round(time.Millisecond).String())
	h.Set("anthropic-ratelimit-requests-limit", limit)
	h.Set("anthropic-ratelimit-requests-remaining", left)
	// RFC 3339 has whole seconds: round up so the reset is never early.
	h.Set("anthropic-ratelimit-requests-reset", now.Add(full+time.Second-1).Truncate(time.Second).UTC().Format(time.RFC3339))
	return max(1, int(math.Ceil(wait(1-tokens).Seconds()))), ok
}
