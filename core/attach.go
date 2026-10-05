package gohan

import (
	"context"
	"iter"
	"time"
)

// Attach streams one run's events from afterSeq onward: the events the log
// already holds first, then live ones as the run records them, with no gap
// or duplicate. The sequence ends at the run's boundary: a Done, a
// Suspended event or a TerminalError record; a cancelled context surfaces
// as a final context.Canceled tuple.
func (c *conversation) Attach(ctx context.Context, runID string, afterSeq int64) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		seq := afterSeq
		// Local wakeups only reach same-instance waiters; the ticker makes
		// a second conversation over the shared event log catch up too.
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			wake, stop := c.subscribe(runID)
			terminal, advanced, ok := c.replay(ctx, runID, &seq, yield)
			if !ok || terminal {
				stop()
				return
			}
			// The run can complete between the last relay and this check;
			// replaying once more first keeps that tail from being lost.
			if !advanced && c.runEnded(runID) {
				stop()
				return
			}
			if advanced {
				stop()
				continue
			}
			// The subscription stays registered across the select, or a
			// notify arriving now would close a channel nobody waits on
			// and the sequence would sleep past the run's end.
			select {
			case <-wake:
				stop()
			case <-ticker.C:
				stop()
			case <-ctx.Done():
				stop()
				yield(nil, ctx.Err())
				return
			}
		}
	}
}

// replay delivers the events the log holds past *seq, advancing it. It
// reports whether a Done went out, whether anything was delivered at all,
// and whether the consumer kept taking events.
func (c *conversation) replay(ctx context.Context, runID string, seq *int64, yield func(Event, error) bool) (terminal, advanced, live bool) {
	for e, err := range c.events.Read(ctx, runID, *seq) {
		if err != nil {
			yield(nil, err)
			return false, advanced, false
		}
		if !yield(e.Payload, nil) {
			return false, advanced, false
		}
		*seq = e.Meta.Seq
		advanced = true
		switch e.Payload.(type) {
		case Done, Suspended, TerminalError:
			// A reattaching client stops at the run's boundary: after a
			// Done, a suspension or the failure terminal it must not be
			// handed records from beyond it in this invocation.
			return true, true, true
		}
	}
	return false, advanced, true
}

// subscribe registers a wake channel for the run; stop unregisters it.
// Subscribe before replaying, or an event recorded between the replay and
// the wait would sleep past its own delivery.
func (c *conversation) subscribe(runID string) (wake chan struct{}, stop func()) {
	ch := make(chan struct{})
	c.mu.Lock()
	if c.waiters == nil {
		c.waiters = map[string]map[chan struct{}]struct{}{}
	}
	set := c.waiters[runID]
	if set == nil {
		set = map[chan struct{}]struct{}{}
		c.waiters[runID] = set
	}
	set[ch] = struct{}{}
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		delete(c.waiters[runID], ch)
		c.mu.Unlock()
	}
}

// notify wakes every Attach waiting on the run.
func (c *conversation) notify(runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for ch := range c.waiters[runID] {
		close(ch)
	}
	delete(c.waiters, runID)
}
