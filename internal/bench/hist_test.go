package bench

import (
	"math"
	"testing"
	"time"
)

func TestHistQuantiles(t *testing.T) {
	var h hist
	for i := 1; i <= 1000; i++ {
		h.add(time.Duration(i) * time.Millisecond)
	}
	within := func(got, want time.Duration) bool { return math.Abs(float64(got-want)) <= 0.011*float64(want) }
	if !within(h.quantile(0.5), 500*time.Millisecond) || !within(h.quantile(0.99), 990*time.Millisecond) {
		t.Fatal(h.quantile(0.5), h.quantile(0.99))
	}
	if p := percentiles(&h); p.Max != 1000 {
		t.Fatal(p.Max)
	}
	var empty hist
	if empty.quantile(0.5) != 0 || percentiles(&empty).Max != 0 {
		t.Fatal("empty hist not zero")
	}
}

func TestHistMerge(t *testing.T) {
	var a, b, all hist
	for i := 1; i <= 100; i++ {
		d := time.Duration(i) * time.Millisecond
		all.add(d)
		if i%2 == 0 {
			a.add(d)
		} else {
			b.add(d)
		}
	}
	a.merge(&b)
	if a.quantile(0.9) != all.quantile(0.9) || a.max != all.max || a.n != all.n {
		t.Fatal("merge differs")
	}
}
