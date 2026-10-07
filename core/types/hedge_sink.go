package types

import (
	"context"
	"sync"
)

// HedgeSink receives a hedged model call's outcome: the attempts that
// started and the winner's endpoint. The driver places one on the model
// call's context; the hedge step records into it. Core owns the type so
// the driver reads the outcome without importing the hedge package.
type HedgeSink struct {
	mu       sync.Mutex
	attempts int
	endpoint string
}

// Record notes one started attempt; a non-empty endpoint is the winner's.
func (s *HedgeSink) Record(attempts int, endpoint string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if attempts > s.attempts {
		s.attempts = attempts
	}
	if endpoint != "" {
		s.endpoint = endpoint
	}
}

// Outcome reports the recorded values.
func (s *HedgeSink) Outcome() (attempts int, endpoint string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts, s.endpoint
}

type hedgeSinkKey struct{}

// WithHedgeSink returns a context carrying the sink.
func WithHedgeSink(ctx context.Context, s *HedgeSink) context.Context {
	return context.WithValue(ctx, hedgeSinkKey{}, s)
}

// HedgeSinkFrom reports the sink on ctx, or ok == false when none.
func HedgeSinkFrom(ctx context.Context) (*HedgeSink, bool) {
	s, ok := ctx.Value(hedgeSinkKey{}).(*HedgeSink)
	return s, ok
}
