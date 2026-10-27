package bench

import (
	"math"
	"time"
)

const histBuckets = 2500

var logBase = math.Log(1.01)

// hist is a constant-memory latency histogram with ~1% bucket width.
// ponytail: log-bucket histogram, swap for HDR if sub-1% precision is ever needed
type hist struct {
	counts [histBuckets]uint64
	n      uint64
	max    time.Duration
}

func (h *hist) add(d time.Duration) {
	i := 0
	if us := d.Microseconds(); us >= 1 {
		i = min(int(math.Log(float64(us))/logBase)+1, histBuckets-1)
	}
	h.counts[i]++
	h.n++
	h.max = max(h.max, d)
}

func (h *hist) merge(o *hist) {
	for i, c := range o.counts {
		h.counts[i] += c
	}
	h.n += o.n
	h.max = max(h.max, o.max)
}

// quantile returns the lower bound of the bucket holding the q-th sample.
func (h *hist) quantile(q float64) time.Duration {
	if h.n == 0 {
		return 0
	}
	rank := max(uint64(math.Ceil(q*float64(h.n))), 1)
	var cum uint64
	for i, c := range h.counts {
		if cum += c; cum >= rank {
			if i == 0 {
				return 0
			}
			return time.Duration(math.Pow(1.01, float64(i-1)) * float64(time.Microsecond))
		}
	}
	return h.max
}

type Percentiles struct {
	P50 float64 `json:"p50_ms"`
	P90 float64 `json:"p90_ms"`
	P95 float64 `json:"p95_ms"`
	P99 float64 `json:"p99_ms"`
	Max float64 `json:"max_ms"`
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func percentiles(h *hist) Percentiles {
	return Percentiles{ms(h.quantile(0.5)), ms(h.quantile(0.9)), ms(h.quantile(0.95)), ms(h.quantile(0.99)), ms(h.max)}
}
