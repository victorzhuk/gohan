package gohan

import (
	"context"
	"sync"
	"time"
)

// HealthProbe reports one dependency a readiness check covers. A nil error
// marks the check OK; the wrapped sentinel names the failure, e.g.
// types.ErrSchemaTooOld for a store schema older than this release
// supports.
type HealthProbe func(ctx context.Context) error

// Check is one named readiness result.
type Check struct {
	OK      bool
	Detail  string
	Latency time.Duration
}

// HealthReport is what Health answers with.
type HealthReport struct {
	Ready  bool
	Checks map[string]Check
}

var (
	probeMu sync.Mutex
	probes  = map[*Stack]map[string]HealthProbe{}
)

// RegisterHealthProbe binds a named readiness probe to the stack, e.g. the
// store schema check the postgres adapter owns. A later registration for
// the same name replaces the earlier probe.
func (s *Stack) RegisterHealthProbe(name string, p HealthProbe) {
	probeMu.Lock()
	defer probeMu.Unlock()
	m, ok := probes[s]
	if !ok {
		m = make(map[string]HealthProbe)
		probes[s] = m
	}
	m[name] = p
}

// Ready reports whether the stack accepts new runs: it is false once
// Shutdown has begun or a probe fails.
func (s *Stack) Ready() bool {
	return s.Health(context.Background()).Ready
}

// Health runs the shutdown check and every registered probe. It is safe to
// call before and after Shutdown.
func (s *Stack) Health(ctx context.Context) HealthReport {
	checks := make(map[string]Check)

	start := time.Now()
	down := s.shuttingDown()
	detail := "accepting runs"
	if down {
		detail = "shutting down"
	}
	checks["shutdown"] = Check{OK: !down, Detail: detail, Latency: time.Since(start)}

	probeMu.Lock()
	own := make(map[string]HealthProbe, len(probes[s]))
	for name, p := range probes[s] {
		own[name] = p
	}
	probeMu.Unlock()

	for name, p := range own {
		start := time.Now()
		err := p(ctx)
		var detail string
		if err != nil {
			detail = err.Error()
		}
		checks[name] = Check{OK: err == nil, Detail: detail, Latency: time.Since(start)}
	}

	ready := true
	for _, c := range checks {
		ready = ready && c.OK
	}
	return HealthReport{Ready: ready, Checks: checks}
}
