package telemetry

import (
	"sort"
	"sync"
	"time"
)

// DefaultLatencySamples is the ring size the rolling latency window keeps
// per endpoint.
const DefaultLatencySamples = 128

// LatencyWindow keeps a bounded ring of time-to-first-chunk samples per
// endpoint and reports the rolling P90 the hedge default reads. The ring
// size is fixed at construction; samples overwrite oldest-first.
type LatencyWindow struct {
	mu         sync.Mutex
	samples    int
	byEndpoint map[string][]time.Duration
}

// NewLatencyWindow returns a window keeping n samples per endpoint. A
// non-positive n takes the default.
func NewLatencyWindow(n int) *LatencyWindow {
	if n <= 0 {
		n = DefaultLatencySamples
	}
	return &LatencyWindow{samples: n, byEndpoint: make(map[string][]time.Duration)}
}

// Observe records one time-to-first-chunk sample for the endpoint.
func (w *LatencyWindow) Observe(endpoint string, ttft time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	ring := append(w.byEndpoint[endpoint], ttft)
	if len(ring) > w.samples {
		ring = ring[len(ring)-w.samples:]
	}
	w.byEndpoint[endpoint] = ring
}

// P90 reports the endpoint's rolling P90 time-to-first-chunk. An endpoint
// with no samples reports false.
func (w *LatencyWindow) P90(endpoint string) (time.Duration, bool) {
	w.mu.Lock()
	ring := w.byEndpoint[endpoint]
	if len(ring) == 0 {
		w.mu.Unlock()
		return 0, false
	}
	sorted := make([]time.Duration, len(ring))
	copy(sorted, ring)
	w.mu.Unlock()
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[(len(sorted)*9+9)/10-1], true
}
