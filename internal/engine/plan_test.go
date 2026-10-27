package engine

import (
	"strings"
	"testing"
	"time"
)

// planOf builds a plan from a single [[mock ...]] block; shared by later engine tests.
func planOf(t *testing.T, d string) Plan {
	t.Helper()
	p, err := BuildPlan(Request{Scope: []string{"[[mock " + d + "]]"}}, Layers{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildPlanFallbacks(t *testing.T) {
	p, err := BuildPlan(Request{}, Layers{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Tokens != 100 || p.Chunk != 1 || p.TPS != 0 || p.Delay != 0 || p.ErrorCode != 0 ||
		p.ErrorAfter != -1 || p.FailAfter != -1 || p.HangAfter != -1 || p.Hang || p.FinishReason != "stop" || p.Seeded {
		t.Fatalf("%+v", p)
	}
}

func TestBuildPlanPrecedence(t *testing.T) {
	l := Layers{Defaults: Directives{"tokens": "1", "tps": "1", "delay": "1s"}, Overrides: Directives{"delay": "9s"}}
	req := Request{Header: "tokens=2 tps=2", Scope: []string{"sys [[mock tokens=3]]", "user [[mock tokens=4]]"}}
	p, err := BuildPlan(req, l)
	if err != nil {
		t.Fatal(err)
	}
	if p.Tokens != 4 || p.TPS != 2 || p.Delay != 9*time.Second {
		t.Fatalf("%+v", p)
	}
}

func TestBuildPlanMaxTokensCaps(t *testing.T) {
	p, _ := BuildPlan(Request{Scope: []string{"[[mock tokens=500]]"}, MaxTokens: 200}, Layers{})
	if p.Tokens != 200 || p.FinishReason != "length" {
		t.Fatalf("%+v", p)
	}
	p, _ = BuildPlan(Request{Scope: []string{"[[mock tokens=500 finish_reason=stop]]"}, MaxTokens: 200}, Layers{})
	if p.Tokens != 200 || p.FinishReason != "stop" {
		t.Fatalf("%+v", p)
	}
	p, _ = BuildPlan(Request{Scope: []string{"[[mock tokens=50]]"}, MaxTokens: 0}, Layers{})
	if p.Tokens != 50 || p.FinishReason != "stop" {
		t.Fatalf("%+v", p)
	}
}

func TestBuildPlanRange(t *testing.T) {
	a, b := planOf(t, "tokens=10-20 seed=7"), planOf(t, "tokens=10-20 seed=7")
	if a.Tokens != b.Tokens || a.Tokens < 10 || a.Tokens > 20 {
		t.Fatalf("%d %d", a.Tokens, b.Tokens)
	}
	seen := map[int]bool{}
	for range 200 {
		seen[planOf(t, "tokens=10-20").Tokens] = true
	}
	if len(seen) < 2 {
		t.Fatal("unseeded range always picked the same value")
	}
}

func TestBuildPlanBodySeed(t *testing.T) {
	five := int64(5)
	p, _ := BuildPlan(Request{Seed: &five}, Layers{})
	if !p.Seeded || p.Seed != 5 {
		t.Fatalf("%+v", p)
	}
	p, _ = BuildPlan(Request{Seed: &five, Scope: []string{"[[mock seed=6]]"}}, Layers{})
	if p.Seed != 6 {
		t.Fatalf("%+v", p)
	}
}

func TestBuildPlanErrors(t *testing.T) {
	cases := map[string]int{"error=429": 429, "error_rate=1": 500, "error_rate=0 error=503": 0, "error_after=3": 500}
	for d, want := range cases {
		if got := planOf(t, d).ErrorCode; got != want {
			t.Errorf("%s: ErrorCode=%d want %d", d, got, want)
		}
	}
	if planOf(t, "error_after=3").ErrorAfter != 3 {
		t.Error("ErrorAfter not set")
	}
}

func TestBuildPlanErrorRateIgnoresSeed(t *testing.T) {
	n := 0
	for range 2000 {
		if planOf(t, "error_rate=0.5 seed=1").ErrorCode != 0 {
			n++
		}
	}
	if n < 850 || n > 1150 {
		t.Fatalf("%d/2000 errors with constant seed", n)
	}
}

func TestBuildPlanRejectsInvalid(t *testing.T) {
	bad := []string{"tokens=-1", "tokens=abc", "tokens=1000001", "tokens=100000000", "tokens=20-10",
		"prompt_tokens=-3", "delay=fast", "delay=-1s", "tps=-1", "jitter=1.5", "jitter=5", "chunk=0",
		"error=200", "error=600", "error_rate=2", "error_after=-1", "fail_after=-1", "hang_after=x",
		"finish_reason=done", "seed=x"}
	for _, d := range bad {
		_, err := BuildPlan(Request{Scope: []string{"[[mock " + d + "]]"}}, Layers{})
		key, _, _ := strings.Cut(d, "=")
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s: err = %v", d, err)
		}
	}
	if _, err := BuildPlan(Request{Header: "bogus=1"}, Layers{}); err == nil {
		t.Error("bad header accepted")
	}
}

func TestStop(t *testing.T) {
	cases := []struct {
		d   string
		n   int
		end End
	}{
		{"tokens=10", 10, EndDone},
		{"tokens=10 error=500 error_after=3 fail_after=5", 3, EndError},
		{"tokens=10 fail_after=10", 10, EndAbort},
		{"tokens=10 hang_after=12", 10, EndDone},
		{"tokens=10 error=500 error_after=4 fail_after=4 hang_after=4", 4, EndError},
		{"tokens=10 fail_after=4 hang_after=4", 4, EndAbort},
		{"tokens=10 error_rate=0 error_after=2", 10, EndDone},
	}
	for _, c := range cases {
		if n, end := planOf(t, c.d).Stop(); n != c.n || end != c.end {
			t.Errorf("%s: got %d,%v", c.d, n, end)
		}
	}
}

func TestAt(t *testing.T) {
	p := planOf(t, "delay=1s tps=10")
	if p.At(0) != time.Second || p.At(10) != 2*time.Second {
		t.Fatal(p.At(0), p.At(10))
	}
	if planOf(t, "delay=1s").At(5) != time.Second {
		t.Fatal("tps=0 must not add time")
	}
}

func TestPlanString(t *testing.T) {
	if s := planOf(t, "tokens=500 delay=2s tps=20 seed=7").String(); s != "tokens=500 prompt_tokens=0 delay=2s tps=20 seed=7" {
		t.Fatal(s)
	}
}

func TestEstimateTokens(t *testing.T) {
	if n := EstimateTokens([]string{"hello", "abcdefghi"}); n != 5 {
		t.Fatal(n)
	}
	if n := EstimateTokens([]string{"héllo"}); n != 2 {
		t.Fatal(n)
	}
	if n := EstimateTokens(nil); n != 0 {
		t.Fatal(n)
	}
}
