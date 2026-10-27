package engine

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strings"
)

// Directives holds parsed mock directives; bare flags have the value "".
type Directives map[string]string

var knownKeys = map[string]bool{
	"tokens": true, "prompt_tokens": true, "delay": true, "tps": true,
	"jitter": true, "chunk": true, "error": true, "error_rate": true,
	"error_after": true, "fail_after": true, "hang": true, "hang_after": true,
	"finish_reason": true, "seed": true,
}

// flagKeys take no value; every other key requires one.
var flagKeys = map[string]bool{"hang": true}

var blockRe = regexp.MustCompile(`\[\[mock(\s[^\]]*)?\]\]`)

// ParseDirectives parses "k=v k=v flag". It checks key names and the
// flag/value shape only; value validation happens elsewhere.
func ParseDirectives(s string) (Directives, error) {
	d := Directives{}
	for _, f := range strings.Fields(s) {
		k, v, hasEq := strings.Cut(f, "=")
		if !knownKeys[k] {
			return nil, fmt.Errorf("unknown mock directive %q", k)
		}
		if flagKeys[k] {
			if hasEq {
				return nil, fmt.Errorf("mock directive %q takes no value", k)
			}
		} else if v == "" {
			return nil, fmt.Errorf("mock directive %q needs a value", k)
		}
		d[k] = v
	}
	return d, nil
}

// ExtractDirectives parses every [[mock ...]] block in text, merging in order.
// A "[[mock" that does not open a well-formed block is an error, so a typo is
// never silently ignored.
func ExtractDirectives(text string) (Directives, error) {
	out := Directives{}
	blocks := blockRe.FindAllStringSubmatch(text, -1)
	if strings.Count(text, "[[mock") != len(blocks) {
		return nil, errors.New("malformed [[mock ...]] block")
	}
	for _, m := range blocks {
		d, err := ParseDirectives(m[1])
		if err != nil {
			return nil, err
		}
		out = out.Merge(d)
	}
	return out, nil
}

// Merge returns a new map in which keys of o override those of d.
func (d Directives) Merge(o Directives) Directives {
	out := maps.Clone(d)
	if out == nil {
		out = Directives{}
	}
	maps.Copy(out, o)
	return out
}
