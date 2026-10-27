package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/enrico9034/llmock/internal/bench"
)

// parseBench reads the bench flags; it returns the run config and the --out path.
func parseBench(args []string) (bench.Config, string, error) {
	var c bench.Config
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.StringVar(&c.URL, "url", "", "base URL of an OpenAI/Anthropic-compatible server")
	fs.StringVar(&c.API, "api", "openai", "API flavour: openai or anthropic")
	fs.StringVar(&c.Model, "model", "mock-gpt", "model name")
	fs.StringVar(&c.APIKey, "api-key", "", "API key")
	fs.IntVar(&c.Users, "users", 10, "concurrent virtual users")
	fs.DurationVar(&c.Duration, "duration", 0, "run time (exclusive with --requests)")
	fs.IntVar(&c.Requests, "requests", 0, "total requests (exclusive with --duration)")
	fs.DurationVar(&c.Ramp, "ramp", 0, "linear ramp-up time of the users")
	fs.BoolVar(&c.Stream, "stream", false, "stream responses")
	fs.StringVar(&c.Directives, "directives", "", `mock directives appended to the prompt, e.g. "tokens=500 tps=20"`)
	fs.StringVar(&c.Prompt, "prompt", "Hello", "user prompt")
	fs.IntVar(&c.MaxTokens, "max-tokens", 0, "max_tokens to send (0 = omit; Anthropic then sends 4096)")
	fs.Int64Var(&c.Seed, "seed", 0, "base seed: request i uses seed+i (unset = unseeded)")
	fs.DurationVar(&c.Timeout, "timeout", 120*time.Second, "per-request timeout")
	out := fs.String("out", "", "write the JSON report to this file")
	if err := fs.Parse(args); err != nil {
		return c, "", err
	}
	fs.Visit(func(f *flag.Flag) { c.Seeded = c.Seeded || f.Name == "seed" })
	return c, *out, nil
}

func runBench(args []string) int {
	cfg, out, err := parseBench(args)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "llmock:", err)
		}
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	r, err := bench.Run(ctx, cfg, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "llmock:", err)
		return 1
	}
	r.Print(os.Stdout)
	if out != "" {
		if err := r.WriteJSON(out); err != nil {
			fmt.Fprintln(os.Stderr, "llmock:", err)
			return 1
		}
	}
	return 0
}
