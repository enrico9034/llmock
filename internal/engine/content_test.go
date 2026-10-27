package engine

import (
	"encoding/json"
	"testing"
)

func TestContentText(t *testing.T) {
	cases := map[string]string{
		`"plain"`: "plain",
		`[{"type":"text","text":"a"},{"type":"image_url","image_url":{"url":"x"}},{"type":"text","text":"b"}]`: "a\nb",
		`[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}]`:                                  "sys",
		`null`: "", `123`: "", ``: "",
	}
	for in, want := range cases {
		if got := ContentText(json.RawMessage(in)); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}
