package main

import (
	"testing"
	"time"
)

func TestParseBench(t *testing.T) {
	c, out, err := parseBench([]string{"--url", "http://x"})
	if err != nil || out != "" || c.API != "openai" || c.Model != "mock-gpt" || c.Users != 10 || c.Prompt != "Hello" ||
		c.Timeout != 120*time.Second || c.Seeded {
		t.Fatalf("%+v %q %v", c, out, err)
	}
	c, out, _ = parseBench([]string{"--seed", "0", "--out", "r.json"})
	if !c.Seeded || c.Seed != 0 || out != "r.json" {
		t.Fatalf("%+v %q", c, out)
	}
}
