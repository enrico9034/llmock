// Package metrics holds LLMock's Prometheus collectors, registered on the
// default registry.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	Requests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "llmock_requests_total",
		Help: "Requests served, by route and HTTP status.",
	}, []string{"route", "status"})
	Duration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "llmock_request_duration_seconds",
		Help:    "Request duration, by route.",
		Buckets: prometheus.ExponentialBuckets(0.005, 2, 16), // 5ms to ~164s: long streams included
	}, []string{"route"})
	InFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "llmock_in_flight_requests",
		Help: "Requests currently being served.",
	})
	TTFT = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "llmock_ttft_seconds",
		Help:    "Time to the first content token.",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 16),
	})
	Tokens = promauto.NewCounter(prometheus.CounterOpts{
		Name: "llmock_tokens_total",
		Help: "Completion tokens sent.",
	})
	Injected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "llmock_injected_total",
		Help: "Injected failures, by kind (error, abort, hang).",
	}, []string{"kind"})
)
