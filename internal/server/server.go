// Package server wires the provider endpoints into one HTTP handler with
// auth, simulated rate limiting and Prometheus metrics.
package server

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/enrico9034/llmock/internal/anthropic"
	"github.com/enrico9034/llmock/internal/engine"
	"github.com/enrico9034/llmock/internal/metrics"
	"github.com/enrico9034/llmock/internal/openai"
)

// Config is the server configuration.
type Config struct {
	Defaults, Overrides string   // directive syntax
	APIKeys             []string // empty = accept anything
	RateLimitRPS        float64  // 0 = off
	MaxConcurrency      int      // 0 = off
	Models              []string
	MaxBody             int64
	AccessLog           bool
}

// New builds the handler serving every endpoint. stopping is closed when the
// server begins shutdown; it releases hanging requests.
func New(cfg Config, stopping <-chan struct{}) (http.Handler, error) {
	defaults, err := engine.ParseDirectives(cfg.Defaults)
	if err != nil {
		return nil, fmt.Errorf("defaults: %w", err)
	}
	overrides, err := engine.ParseDirectives(cfg.Overrides)
	if err != nil {
		return nil, fmt.Errorf("overrides: %w", err)
	}
	layers := engine.Layers{Defaults: defaults, Overrides: overrides}
	if _, err := engine.BuildPlan(engine.Request{}, layers); err != nil {
		return nil, fmt.Errorf("defaults/overrides: %w", err)
	}
	env := &engine.Env{Layers: layers, Models: cfg.Models, MaxBody: cfg.MaxBody, Stopping: stopping}

	g := &guard{keys: cfg.APIKeys, maxConc: int64(cfg.MaxConcurrency)}
	if cfg.RateLimitRPS > 0 {
		g.bucket = newBucket(cfg.RateLimitRPS)
	}
	oai := func(h http.Handler) http.Handler { return g.wrap(openai.WriteError, h) }
	ant := func(h http.Handler) http.Handler { return g.wrap(anthropic.WriteError, h) }
	oaiModels, antModels := oai(openai.Models(env)), ant(anthropic.Models(env))
	models := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("anthropic-version") != "" {
			antModels.ServeHTTP(w, r)
			return
		}
		oaiModels.ServeHTTP(w, r)
	})

	mux := http.NewServeMux()
	api := func(pattern string, h http.Handler) { mux.Handle(pattern, observe(h, cfg.AccessLog)) }
	for _, p := range []string{"/v1", ""} {
		api("POST "+p+"/chat/completions", oai(openai.Chat(env)))
		api("POST "+p+"/completions", oai(openai.Completions(env)))
		api("POST "+p+"/embeddings", oai(openai.Embeddings(env)))
		api("POST "+p+"/messages", ant(anthropic.Messages(env)))
		api("POST "+p+"/messages/count_tokens", ant(anthropic.CountTokens(env)))
		api("GET "+p+"/models", models)
	}
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux, nil
}

// observe records the in-flight gauge, request count and duration, and the
// optional access log. A handler that panics (http.ErrAbortHandler for
// injected aborts) is recorded as "aborted"; the panic is not recovered, so
// it still reaches net/http unchanged.
func observe(next http.Handler, accessLog bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		metrics.InFlight.Inc()
		returned := false
		defer func() {
			d := time.Since(start)
			status := "aborted"
			if returned {
				status = strconv.Itoa(sw.code)
			}
			metrics.InFlight.Dec()
			metrics.Requests.WithLabelValues(r.Pattern, status).Inc()
			metrics.Duration.WithLabelValues(r.Pattern).Observe(d.Seconds())
			if accessLog {
				slog.Info("request", "method", r.Method, "path", r.URL.Path, "status", status, "duration", d)
			}
		}()
		next.ServeHTTP(sw, r)
		returned = true
	})
}

// statusWriter captures the response status. Unwrap lets
// http.ResponseController reach the real writer to flush streams.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
