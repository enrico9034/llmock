package engine

import (
	"maps"
	"strings"
	"testing"
)

func TestParseDirectives(t *testing.T) {
	d, err := ParseDirectives("tokens=500  tps=10 hang")
	if err != nil {
		t.Fatal(err)
	}
	want := Directives{"tokens": "500", "tps": "10", "hang": ""}
	if !maps.Equal(d, want) {
		t.Fatalf("got %v want %v", d, want)
	}
}

func TestParseDirectivesRejectsUnknownKey(t *testing.T) {
	_, err := ParseDirectives("tokns=500")
	if err == nil || !strings.Contains(err.Error(), `"tokns"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseDirectivesRejectsMissingValue(t *testing.T) {
	for _, s := range []string{"tokens", "tokens="} {
		if _, err := ParseDirectives(s); err == nil {
			t.Errorf("%q: expected error", s)
		}
	}
}

func TestParseDirectivesRejectsValueOnFlag(t *testing.T) {
	if _, err := ParseDirectives("hang=1"); err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractDirectivesMergesBlocksInOrder(t *testing.T) {
	d, err := ExtractDirectives("a [[mock tokens=5 tps=1]] b [[mock tokens=7]]")
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(d, Directives{"tokens": "7", "tps": "1"}) {
		t.Fatalf("got %v", d)
	}
}

func TestExtractDirectivesIgnoresNonBlocks(t *testing.T) {
	d, err := ExtractDirectives("hi [[other x=1]] [mock tokens=1] [[MOCK tokens=1]] [[mock]]")
	if err != nil || len(d) != 0 {
		t.Fatalf("got %v, %v", d, err)
	}
}

func TestExtractDirectivesRejectsMalformedBlocks(t *testing.T) {
	for _, s := range []string{"hi [[mock tokens=5]", "[[mock:tokens=5]]", "[[mocktokens=5]]", "[[mock a=]x]]", "[[mock tokens=1]] [[mock"} {
		if _, err := ExtractDirectives(s); err == nil || err.Error() != "malformed [[mock ...]] block" {
			t.Errorf("%q: err = %v", s, err)
		}
	}
}

func TestMergeLaterWins(t *testing.T) {
	a := Directives{"tokens": "1", "tps": "2"}
	got := a.Merge(Directives{"tokens": "3"})
	if !maps.Equal(got, Directives{"tokens": "3", "tps": "2"}) {
		t.Fatalf("got %v", got)
	}
	if a["tokens"] != "1" {
		t.Fatal("Merge mutated receiver")
	}
}
