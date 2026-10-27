package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

type Config struct {
	URL        string        `json:"url"`
	API        string        `json:"api"` // "openai" | "anthropic"
	Model      string        `json:"model"`
	APIKey     string        `json:"-"` // never written to reports
	Users      int           `json:"users"`
	Duration   time.Duration `json:"-"`
	Requests   int           `json:"requests"`
	Ramp       time.Duration `json:"-"`
	Stream     bool          `json:"stream"`
	Directives string        `json:"directives"`
	Prompt     string        `json:"prompt"`
	MaxTokens  int           `json:"max_tokens"`
	Seed       int64         `json:"seed"`
	Seeded     bool          `json:"seeded"`
	Timeout    time.Duration `json:"-"`
}

// MarshalJSON renders the durations as Go duration strings.
func (c Config) MarshalJSON() ([]byte, error) {
	type plain Config
	return json.Marshal(struct {
		plain
		Duration string `json:"duration"`
		Ramp     string `json:"ramp"`
		Timeout  string `json:"timeout"`
	}{plain(c), c.Duration.String(), c.Ramp.String(), c.Timeout.String()})
}

type Report struct {
	Config              Config         `json:"config"`
	StartedAt           time.Time      `json:"started_at"`
	ElapsedSeconds      float64        `json:"elapsed_seconds"`
	Requests            int            `json:"requests"`
	OK                  int            `json:"ok"`
	Failed              int            `json:"failed"`
	ErrorsByStatus      map[string]int `json:"errors_by_status"`
	ErrorsByKind        map[string]int `json:"errors_by_kind"` // http|connect|timeout|truncated|stream_error
	RPS                 float64        `json:"requests_per_second"`
	OutputTPS           float64        `json:"output_tokens_per_second"`
	Latency             Percentiles    `json:"latency"`
	TTFT                Percentiles    `json:"ttft"`
	ITL                 Percentiles    `json:"itl"`
	TokensMean          float64        `json:"tokens_per_request_mean"`
	TokensMin           int            `json:"tokens_per_request_min"`
	TokensMax           int            `json:"tokens_per_request_max"`
	IntegrityChecked    int            `json:"integrity_checked"`
	IntegrityMismatches int            `json:"integrity_mismatches"`
}

func (r *Report) Print(w io.Writer) {
	c := r.Config
	mode := "non-stream"
	if c.Stream {
		mode = "stream"
	}
	load := fmt.Sprintf("%d requests", c.Requests)
	if c.Duration > 0 {
		load = c.Duration.String()
	}
	fmt.Fprintf(w, "%d users, %s, %s, %s %s\n", c.Users, load, mode, c.API, c.URL)
	fmt.Fprintf(w, "Requests:     %d (%d ok, %d failed)\n", r.Requests, r.OK, r.Failed)
	fmt.Fprintf(w, "Throughput:   %.1f req/s, %.1f output tok/s\n", r.RPS, r.OutputTPS)
	if r.Failed > 0 {
		var errs []string
		for i, m := range []map[string]int{r.ErrorsByStatus, r.ErrorsByKind} {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if i == 1 && k == "http" { // already listed per status
					continue
				}
				errs = append(errs, fmt.Sprintf("%s: %d", k, m[k]))
			}
		}
		fmt.Fprintf(w, "Errors:       %s\n", strings.Join(errs, ", "))
	}
	pct := func(title string, p Percentiles) {
		fmt.Fprintf(w, "%-13s p50 %.1f  p90 %.1f  p95 %.1f  p99 %.1f  max %.1f\n", title+":", p.P50, p.P90, p.P95, p.P99, p.Max)
	}
	pct("Latency (ms)", r.Latency)
	if c.Stream {
		pct("TTFT (ms)", r.TTFT)
		pct("ITL (ms)", r.ITL)
	}
	fmt.Fprintf(w, "Tokens/req:   mean %.1f  min %d  max %d\n", r.TokensMean, r.TokensMin, r.TokensMax)
	if r.IntegrityChecked > 0 {
		fmt.Fprintf(w, "Integrity:    %d checked, %d mismatches\n", r.IntegrityChecked, r.IntegrityMismatches)
	}
}

func (r *Report) WriteJSON(path string) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
