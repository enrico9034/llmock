package bench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReportPrint(t *testing.T) {
	r := &Report{Config: Config{API: "openai", URL: "http://x", Users: 2, Stream: true}, Requests: 10, OK: 7, Failed: 3,
		ErrorsByStatus: map[string]int{"429": 3}, ErrorsByKind: map[string]int{"http": 3}, IntegrityChecked: 7}
	var b strings.Builder
	r.Print(&b)
	for _, want := range []string{"Requests", "Throughput", "429: 3", "TTFT (ms)", "p50", "Integrity", "7 checked, 0 mismatches"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in\n%s", want, b.String())
		}
	}
}

func TestReportWriteJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.json")
	r := &Report{Config: Config{APIKey: "sk-secret", Duration: time.Minute, Users: 3}, Requests: 1}
	if err := r.WriteJSON(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "sk-secret") || !strings.Contains(string(b), `"duration":"1m0s"`) || !strings.Contains(string(b), `"requests":1`) {
		t.Fatal(string(b))
	}
}
