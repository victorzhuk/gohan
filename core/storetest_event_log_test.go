package gohan

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/testkit/storetest"
)

// eventLogAdapter binds the memory EventLogStore to the conformance suite's
// port-level interface without letting the suite import core/stores.
type eventLogAdapter struct {
	l *stores.MemoryEventLog
}

func (a eventLogAdapter) Append(ctx context.Context, runID string, e storetest.Event) error {
	return a.l.Append(ctx, runID, stores.Event{Payload: e.Payload, Meta: e.Meta})
}

func (a eventLogAdapter) Read(ctx context.Context, runID string, afterSeq int64) iter.Seq2[storetest.Event, error] {
	return func(yield func(storetest.Event, error) bool) {
		for e, err := range a.l.Read(ctx, runID, afterSeq) {
			if !yield(storetest.Event{Payload: e.Payload, Meta: e.Meta}, err) {
				return
			}
		}
	}
}

func (a eventLogAdapter) Expire(ctx context.Context, olderThan time.Time) error {
	return a.l.Expire(ctx, olderThan)
}

func newConformanceEventLog(t *testing.T, now func() time.Time, capacity int) storetest.EventLogStore {
	t.Helper()
	return eventLogAdapter{l: stores.NewMemoryEventLog(
		stores.WithMemoryEventLogClock(now),
		stores.WithMemoryEventLogCapacity(capacity),
	)}
}

func TestStoretestEventLogBind(t *testing.T) {
	storetest.EventLog(t, newConformanceEventLog)
}
