package engine

import (
	"strings"
	"testing"
)

func TestAppendTokensLoopsPassage(t *testing.T) {
	if s := string(AppendTokens(nil, 0, 3, 0, false)); s != "Lorem ipsum dolor" {
		t.Fatal(s)
	}
	if s := string(AppendTokens(nil, 1, 2, 0, false)); s != " ipsum" {
		t.Fatal(s)
	}
	if s := string(AppendTokens(nil, 69, 70, 0, false)); s != " Lorem" { // the passage has 69 words
		t.Fatal(s)
	}
}

func TestAppendTokensSeeded(t *testing.T) {
	a := string(AppendTokens(nil, 0, 50, 1, true))
	if a != string(AppendTokens(nil, 0, 50, 1, true)) {
		t.Fatal("same seed, different text")
	}
	if a == string(AppendTokens(nil, 0, 50, 2, true)) {
		t.Fatal("different seeds, same text")
	}
	if n := len(strings.Fields(a)); n != 50 {
		t.Fatalf("%d words", n)
	}
}

func TestAppendTokensNoAllocs(t *testing.T) {
	buf := make([]byte, 0, 4096)
	if n := testing.AllocsPerRun(100, func() { AppendTokens(buf[:0], 0, 200, 3, true) }); n != 0 {
		t.Fatalf("%v allocs", n)
	}
}
