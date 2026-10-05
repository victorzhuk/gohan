package storetest

import (
	"context"
	"fmt"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
)

type fakeEventLog struct {
	mu    sync.Mutex
	runs  map[string][]Event
	clock func() time.Time
	cap   int
	next  map[string]int64
}

func newFakeEventLog(_ *testing.T, now func() time.Time, capacity int) EventLogStore {
	return &fakeEventLog{
		runs:  make(map[string][]Event),
		clock: now,
		cap:   capacity,
		next:  make(map[string]int64),
	}
}

func (f *fakeEventLog) Append(_ context.Context, runID string, e Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.next[runID]++
	e.Meta.Seq = f.next[runID]
	e.Meta.Time = f.clock()
	evts := append(f.runs[runID], e)
	if len(evts) > f.cap {
		evts = evts[len(evts)-f.cap:]
	}
	f.runs[runID] = evts
	return nil
}

func (f *fakeEventLog) Read(_ context.Context, runID string, afterSeq int64) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		f.mu.Lock()
		oldest := f.next[runID] + 1
		if len(f.runs[runID]) > 0 {
			oldest = f.runs[runID][0].Meta.Seq
		}
		evts := append([]Event(nil), f.runs[runID]...)
		f.mu.Unlock()
		if afterSeq < oldest-1 {
			yield(Event{}, fmt.Errorf("read from %d: %w", afterSeq, stores.ErrStaleCursor))
			return
		}
		for _, e := range evts {
			if e.Meta.Seq <= afterSeq {
				continue
			}
			if !yield(e, nil) {
				return
			}
		}
	}
}

func (f *fakeEventLog) Expire(_ context.Context, olderThan time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for runID, evts := range f.runs {
		kept := evts[:0]
		for _, e := range evts {
			if !e.Meta.Time.Before(olderThan) {
				kept = append(kept, e)
			}
		}
		f.runs[runID] = kept
	}
	return nil
}

func TestStoretestEventLog(t *testing.T) {
	EventLog(t, newFakeEventLog)
}
