package main

import (
	"slices"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestParseServeDefaults(t *testing.T) {
	o, err := parseServe(nil, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if o.Addr != ":8080" || o.Server.Defaults != "tokens=100" || !slices.Equal(o.Server.Models, []string{"mock-gpt", "mock-claude"}) ||
		o.Server.MaxBody != 10485760 || o.ShutdownTimeout != 30*time.Second || o.Server.APIKeys != nil {
		t.Fatalf("%+v", o)
	}
}

func TestParseServeEnvAndFlagPrecedence(t *testing.T) {
	o, _ := parseServe(nil, env(map[string]string{"LLMOCK_ADDR": ":9", "LLMOCK_API_KEYS": "a,b", "LLMOCK_RATE_LIMIT_RPS": "5"}))
	if o.Addr != ":9" || !slices.Equal(o.Server.APIKeys, []string{"a", "b"}) || o.Server.RateLimitRPS != 5 {
		t.Fatalf("%+v", o)
	}
	o, _ = parseServe([]string{"--addr", ":7"}, env(map[string]string{"LLMOCK_ADDR": ":9"}))
	if o.Addr != ":7" {
		t.Fatal(o.Addr)
	}
}

func TestParseServeBadEnv(t *testing.T) {
	if _, err := parseServe(nil, env(map[string]string{"LLMOCK_MAX_CONCURRENCY": "lots"})); err == nil {
		t.Fatal("accepted")
	}
}

func TestSplitList(t *testing.T) {
	if splitList(" , ") != nil || !slices.Equal(splitList(" a, ,b "), []string{"a", "b"}) {
		t.Fatal("splitList")
	}
}
