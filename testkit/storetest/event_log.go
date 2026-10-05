package storetest

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// EventLogStore is the EventLog port as the conformance suite sees it.
type EventLogStore interface {
	Append(ctx context.Context, runID string, e Event) error
	Read(ctx context.Context, runID string, afterSeq int64) iter.Seq2[Event, error]
	Expire(ctx context.Context, olderThan time.Time) error
}

// Event is the port-level shape of a stored stream record.
type Event struct {
	Payload types.Event
	Meta    types.EventMeta
}

// EventLogFactory returns a fresh, empty log holding at most capacity events
// per run. The store clock is now: instants the log persists and expiry are
// judged by it, never by the wall clock.
type EventLogFactory func(t *testing.T, now func() time.Time, capacity int) EventLogStore

// EventLogSuiteName is the subtest prefix shared by the EventLog suite.
const EventLogSuiteName = "stores.event-log"

// EventLog runs the EventLog conformance suite against the implementation
// produced by newLog. It covers the streams contract: a run's Seq values are
// exactly 1..N in delivery order, Read delivers catch-up from any afterSeq
// with no gap or duplicate, a reader that starts mid-run sees only what it
// asked for, Expire drops by store time across runs, and a full per-run log
// evicts its oldest events while Seq keeps growing.
func EventLog(t *testing.T, newLog EventLogFactory) {
	t.Run(EventLogSuiteName+".gapless-seq", func(t *testing.T) {
		start := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		clock := start
		log := newLog(t, func() time.Time { return clock }, 8)

		ctx := context.Background()
		const perRun = 5
		for i := range perRun {
			for _, run := range []string{"run-a", "run-b"} {
				if err := log.Append(ctx, run, Event{Payload: types.TextDelta{Delta: "d"}}); err != nil {
					t.Fatalf("Append(%q) #%d: %v", run, i+1, err)
				}
			}
		}

		for _, run := range []string{"run-a", "run-b"} {
			seq := int64(0)
			for e, err := range log.Read(ctx, run, 0) {
				if err != nil {
					t.Fatalf("Read(%q): %v", run, err)
				}
				seq++
				if e.Meta.Seq != seq {
					t.Fatalf("Read(%q) event %d: Seq = %d, want %d", run, seq, e.Meta.Seq, seq)
				}
				if e.Meta.Time.IsZero() || e.Meta.Time.Before(start) {
					t.Fatalf("Read(%q) event %d: Time = %v, want a store-clock instant", run, seq, e.Meta.Time)
				}
				delta, ok := e.Payload.(types.TextDelta)
				if !ok || delta.Delta != "d" {
					t.Fatalf("Read(%q) event %d: payload %#v, want the appended TextDelta", run, seq, e.Payload)
				}
			}
			if seq != perRun {
				t.Fatalf("Read(%q) delivered %d events, want %d", run, seq, perRun)
			}
		}
	})

	t.Run(EventLogSuiteName+".read-catch-up", func(t *testing.T) {
		log := newLog(t, time.Now, 8)

		ctx := context.Background()
		const total = 5
		for i := range total {
			if err := log.Append(ctx, "run", Event{Payload: types.TextDelta{Turn: i}}); err != nil {
				t.Fatalf("Append #%d: %v", i+1, err)
			}
		}

		for _, tc := range []struct {
			after   int64
			wantSeq []int64
		}{
			{after: 0, wantSeq: []int64{1, 2, 3, 4, 5}},
			{after: 2, wantSeq: []int64{3, 4, 5}},
			{after: 5, wantSeq: nil},
			{after: 99, wantSeq: nil},
		} {
			var got []int64
			for e, err := range log.Read(ctx, "run", tc.after) {
				if err != nil {
					t.Fatalf("Read(after=%d): %v", tc.after, err)
				}
				got = append(got, e.Meta.Seq)
			}
			if len(got) != len(tc.wantSeq) {
				t.Fatalf("Read(after=%d) delivered %v, want %v", tc.after, got, tc.wantSeq)
			}
			for i, seq := range tc.wantSeq {
				if got[i] != seq {
					t.Fatalf("Read(after=%d) delivered %v, want %v", tc.after, got, tc.wantSeq)
				}
			}
		}
	})

	t.Run(EventLogSuiteName+".mid-run-reader", func(t *testing.T) {
		log := newLog(t, time.Now, 8)

		ctx := context.Background()
		for i := range 4 {
			if err := log.Append(ctx, "run", Event{Payload: types.TextDelta{Turn: i}}); err != nil {
				t.Fatalf("Append #%d: %v", i+1, err)
			}
		}

		// A reader attaching after the run already delivered Seq 2 catches up
		// from 2 and sees exactly 3 and 4, once each.
		var got []int64
		for e, err := range log.Read(ctx, "run", 2) {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			got = append(got, e.Meta.Seq)
		}
		if len(got) != 2 || got[0] != 3 || got[1] != 4 {
			t.Fatalf("mid-run reader got %v, want [3 4]", got)
		}

		// Reading again from the same point repeats the same window, never a
		// partial or shifted one.
		var again []int64
		for e, err := range log.Read(ctx, "run", 2) {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			again = append(again, e.Meta.Seq)
		}
		if len(again) != 2 || again[0] != 3 || again[1] != 4 {
			t.Fatalf("second mid-run read got %v, want [3 4]", again)
		}
	})

	t.Run(EventLogSuiteName+".read-break-stops", func(t *testing.T) {
		log := newLog(t, time.Now, 8)

		ctx := context.Background()
		for i := range 5 {
			if err := log.Append(ctx, "run", Event{Payload: types.TextDelta{Turn: i}}); err != nil {
				t.Fatalf("Append #%d: %v", i+1, err)
			}
		}

		var got []int64
		for e, err := range log.Read(ctx, "run", 0) {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			got = append(got, e.Meta.Seq)
			if len(got) == 2 {
				break
			}
		}
		if len(got) != 2 || got[0] != 1 || got[1] != 2 {
			t.Fatalf("broken read got %v, want [1 2]", got)
		}
	})

	t.Run(EventLogSuiteName+".expire-by-store-clock", func(t *testing.T) {
		start := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		clock := start
		log := newLog(t, func() time.Time { return clock }, 64)

		ctx := context.Background()
		for _, run := range []string{"run-a", "run-b"} {
			for i := range 2 {
				if err := log.Append(ctx, run, Event{Payload: types.TextDelta{Turn: i}}); err != nil {
					t.Fatalf("Append(%q) early #%d: %v", run, i+1, err)
				}
			}
		}

		clock = start.Add(time.Minute)
		for _, run := range []string{"run-a", "run-b"} {
			for i := range 2 {
				if err := log.Append(ctx, run, Event{Payload: types.TextDelta{Turn: 10 + i}}); err != nil {
					t.Fatalf("Append(%q) late #%d: %v", run, i+1, err)
				}
			}
		}

		if err := log.Expire(ctx, start.Add(time.Second)); err != nil {
			t.Fatalf("Expire: %v", err)
		}

		// Only the events recorded at the later store time survive, in both
		// runs, and each keeps its original Seq.
		for _, run := range []string{"run-a", "run-b"} {
			want := []int64{3, 4}
			var got []int64
			for e, err := range log.Read(ctx, run, 0) {
				if err != nil {
					t.Fatalf("Read(%q): %v", run, err)
				}
				delta, ok := e.Payload.(types.TextDelta)
				if !ok || delta.Turn < 10 {
					t.Fatalf("Read(%q): event %#v survived expiry, want only late events", run, e.Payload)
				}
				got = append(got, e.Meta.Seq)
			}
			if len(got) != len(want) {
				t.Fatalf("Read(%q) after Expire got %v, want %v", run, got, want)
			}
			for i, seq := range want {
				if got[i] != seq {
					t.Fatalf("Read(%q) after Expire got %v, want %v", run, got, want)
				}
			}
		}

		// An expiry horizon after every recorded instant drops everything.
		if err := log.Expire(ctx, clock.Add(time.Second)); err != nil {
			t.Fatalf("Expire: %v", err)
		}
		for _, run := range []string{"run-a", "run-b"} {
			for range log.Read(ctx, run, 0) {
				t.Fatalf("Read(%q) after full expiry delivered an event", run)
			}
		}
	})

	t.Run(EventLogSuiteName+".ring-evicts-oldest", func(t *testing.T) {
		clock := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		log := newLog(t, func() time.Time { return clock }, 4)

		ctx := context.Background()
		const total = 6
		for i := range total {
			if err := log.Append(ctx, "run", Event{Payload: types.TextDelta{Turn: i}}); err != nil {
				t.Fatalf("Append #%d: %v", i+1, err)
			}
		}

		// The two oldest are gone; what remains is ordered and gapless within
		// the retained window, and the surviving Seq values show the store
		// never restarted numbering.
		var got []int64
		for e, err := range log.Read(ctx, "run", 0) {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			delta, ok := e.Payload.(types.TextDelta)
			if !ok || delta.Turn != int(e.Meta.Seq)-1 {
				t.Fatalf("Read: event %d carries payload %#v, want Turn %d", e.Meta.Seq, e.Payload, int(e.Meta.Seq)-1)
			}
			got = append(got, e.Meta.Seq)
		}
		if len(got) != 4 || got[0] != 3 || got[3] != 6 {
			t.Fatalf("Read after eviction got %v, want [3 4 5 6]", got)
		}

		if err := log.Append(ctx, "run", Event{Payload: types.TextDelta{Turn: total}}); err != nil {
			t.Fatalf("Append after eviction: %v", err)
		}
		last := int64(0)
		for e, err := range log.Read(ctx, "run", total-1) {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			last = e.Meta.Seq
		}
		if last != total+1 {
			t.Fatalf("Seq after eviction = %d, want %d: numbering must keep growing", last, total+1)
		}
	})

	t.Run(EventLogSuiteName+".ring-window-capacity-three", func(t *testing.T) {
		log := newLog(t, time.Now, 3)

		ctx := context.Background()
		for i := range 5 {
			if err := log.Append(ctx, "run", Event{Payload: types.TextDelta{Turn: i}}); err != nil {
				t.Fatalf("Append #%d: %v", i+1, err)
			}
		}

		readSeqs := func(after int64) []int64 {
			t.Helper()
			var got []int64
			for e, err := range log.Read(ctx, "run", after) {
				if err != nil {
					t.Fatalf("Read(after=%d): %v", after, err)
				}
				delta, ok := e.Payload.(types.TextDelta)
				if !ok || delta.Turn != int(e.Meta.Seq)-1 {
					t.Fatalf("Read(after=%d): event %d carries %#v, want Turn %d", after, e.Meta.Seq, e.Payload, int(e.Meta.Seq)-1)
				}
				got = append(got, e.Meta.Seq)
			}
			return got
		}

		if got := readSeqs(0); len(got) != 3 || got[0] != 3 || got[1] != 4 || got[2] != 5 {
			t.Fatalf("Read(after=0) after saturation got %v, want [3 4 5]", got)
		}
		if got := readSeqs(4); len(got) != 1 || got[0] != 5 {
			t.Fatalf("Read(after=4) after saturation got %v, want [5]", got)
		}

		// Numbering keeps growing past the evicted window.
		if err := log.Append(ctx, "run", Event{Payload: types.TextDelta{Turn: 5}}); err != nil {
			t.Fatalf("Append after saturation: %v", err)
		}
		if got := readSeqs(5); len(got) != 1 || got[0] != 6 {
			t.Fatalf("Read(after=5) got %v, want [6]: seq must keep growing across eviction", got)
		}
	})

	t.Run(EventLogSuiteName+".runs-are-isolated", func(t *testing.T) {
		log := newLog(t, time.Now, 8)

		ctx := context.Background()
		if err := log.Append(ctx, "run-a", Event{Payload: types.TextDelta{Delta: "a"}}); err != nil {
			t.Fatalf("Append(run-a): %v", err)
		}
		if err := log.Append(ctx, "run-b", Event{Payload: types.TextDelta{Delta: "b"}}); err != nil {
			t.Fatalf("Append(run-b): %v", err)
		}

		for range log.Read(ctx, "run-c", 0) {
			t.Fatal("Read of an unknown run delivered an event")
		}

		var b []string
		for e, err := range log.Read(ctx, "run-b", 0) {
			if err != nil {
				t.Fatalf("Read(run-b): %v", err)
			}
			if e.Meta.Seq != 1 {
				t.Fatalf("Read(run-b): Seq = %d, want 1: numbering is run-local", e.Meta.Seq)
			}
			delta, ok := e.Payload.(types.TextDelta)
			if !ok {
				t.Fatalf("Read(run-b): payload %#v, want a TextDelta", e.Payload)
			}
			b = append(b, delta.Delta)
		}
		if len(b) != 1 || b[0] != "b" {
			t.Fatalf("Read(run-b) delivered %v, want only run-b's own event", b)
		}
	})
}
