package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/enrico9034/llmock/internal/server"
)

type serveOptions struct {
	Addr            string
	Server          server.Config
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
}

// splitList splits a comma-separated list, trimming blanks; empty yields nil.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseServe reads flags; each flag's default comes from its LLMOCK_ env var,
// so flag > env > built-in default.
func parseServe(args []string, getenv func(string) string) (serveOptions, error) {
	var o serveOptions
	var errs []error
	// str returns the env value or def; env runs parse on it, collecting errors.
	str := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	env := func(key, def string, parse func(string) error) {
		if err := parse(str(key, def)); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
		}
	}
	var rps float64
	var conc int
	var maxBody int64
	var accessLog bool
	var level slog.Level
	var timeout time.Duration
	env("LLMOCK_RATE_LIMIT_RPS", "0", func(s string) (err error) { rps, err = strconv.ParseFloat(s, 64); return })
	env("LLMOCK_MAX_CONCURRENCY", "0", func(s string) (err error) { conc, err = strconv.Atoi(s); return })
	env("LLMOCK_MAX_BODY", "10485760", func(s string) (err error) { maxBody, err = strconv.ParseInt(s, 10, 64); return })
	env("LLMOCK_ACCESS_LOG", "false", func(s string) (err error) { accessLog, err = strconv.ParseBool(s); return })
	env("LLMOCK_LOG_LEVEL", "info", func(s string) error { return level.UnmarshalText([]byte(s)) })
	env("LLMOCK_SHUTDOWN_TIMEOUT", "30s", func(s string) (err error) { timeout, err = time.ParseDuration(s); return })
	if err := errors.Join(errs...); err != nil {
		return o, err
	}

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.StringVar(&o.Addr, "addr", str("LLMOCK_ADDR", ":8080"), "listen address")
	fs.StringVar(&o.Server.Defaults, "defaults", str("LLMOCK_DEFAULTS", "tokens=100"), "default directives")
	fs.StringVar(&o.Server.Overrides, "overrides", str("LLMOCK_OVERRIDES", ""), "override directives")
	keys := fs.String("api-keys", str("LLMOCK_API_KEYS", ""), "comma-separated accepted API keys (empty = accept anything)")
	models := fs.String("models", str("LLMOCK_MODELS", "mock-gpt,mock-claude"), "comma-separated model names")
	fs.Float64Var(&o.Server.RateLimitRPS, "rate-limit-rps", rps, "simulated rate limit, requests/second (0 = off)")
	fs.IntVar(&o.Server.MaxConcurrency, "max-concurrency", conc, "max concurrent requests (0 = off)")
	fs.Int64Var(&o.Server.MaxBody, "max-body", maxBody, "max request body in bytes")
	fs.BoolVar(&o.Server.AccessLog, "access-log", accessLog, "log every request")
	lvl := fs.String("log-level", str("LLMOCK_LOG_LEVEL", "info"), "log level: debug, info, warn, error")
	fs.DurationVar(&o.ShutdownTimeout, "shutdown-timeout", timeout, "graceful shutdown timeout")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.Server.MaxBody <= 0 {
		return o, fmt.Errorf("max-body: %d must be a positive number of bytes", o.Server.MaxBody)
	}
	o.Server.APIKeys, o.Server.Models = splitList(*keys), splitList(*models)
	if err := o.LogLevel.UnmarshalText([]byte(*lvl)); err != nil {
		return o, fmt.Errorf("log-level: %w", err)
	}
	return o, nil
}

func runServe(args []string) int {
	o, err := parseServe(args, os.Getenv)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "llmock:", err)
		}
		return 2
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: o.LogLevel})))

	stopping := make(chan struct{})
	h, err := server.New(o.Server, stopping)
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		return 2
	}
	srv := &http.Server{Addr: o.Addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("llmock listening", "addr", o.Addr, "version", version)

	select {
	case err := <-errc:
		slog.Error("server failed", "err", err)
		return 1
	case <-ctx.Done():
		stop() // a second signal now kills the process instead of waiting for the drain
	}
	slog.Info("shutting down")
	close(stopping)
	sctx, cancel := context.WithTimeout(context.Background(), o.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		slog.Warn("shutdown timed out, closing", "err", err)
		srv.Close()
	}
	return 0
}
