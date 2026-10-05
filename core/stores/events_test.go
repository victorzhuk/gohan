package stores

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestEventLog(t *testing.T) {
	t.Run("streams.monotonic-seq", func(t *testing.T) {
		now := time.Unix(1700000000, 0)
		clock := now
		s := NewMemoryEventLog(WithMemoryEventLogClock(func() time.Time { return clock }))

		const n = 5
		ctx := t.Context()
		for i := range n {
			payload := types.Event(types.LimitWarning{Limit: "tokens", Ratio: float64(i)})
			if i == n-1 {
				payload = types.AssistantMessage{Turn: i}
			}
			if err := s.Append(ctx, "run-1", Event{Payload: payload, Meta: types.EventMeta{SessionID: "s-1"}}); err != nil {
				t.Fatalf("append %d: %v", i, err)
			}
		}

		var seqs []int64
		var last Event
		for e, err := range s.Read(ctx, "run-1", 0) {
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if e.Meta.RunID != "run-1" {
				t.Fatalf("event %d meta run = %q, want run-1", e.Meta.Seq, e.Meta.RunID)
			}
			seqs = append(seqs, e.Meta.Seq)
			last = e
		}
		want := make([]int64, 0, n)
		for i := range n {
			want = append(want, int64(i+1))
		}
		if !slices.Equal(seqs, want) {
			t.Fatalf("seq values = %v, want %v", seqs, want)
		}
		if _, ok := last.Payload.(types.AssistantMessage); !ok {
			t.Fatalf("last payload = %T, want types.AssistantMessage", last.Payload)
		}
		if last.Meta.Seq != n {
			t.Fatalf("done log seq = %d, want %d", last.Meta.Seq, n)
		}

		// Ordered catch-up: a client resuming at its last Seq gets the
		// remaining events, no gap, no duplicate.
		var resumed []int64
		for e, err := range s.Read(ctx, "run-1", 2) {
			if err != nil {
				t.Fatalf("read after 2: %v", err)
			}
			resumed = append(resumed, e.Meta.Seq)
		}
		if !slices.Equal(resumed, []int64{3, 4, 5}) {
			t.Fatalf("resume after 2 = %v, want [3 4 5]", resumed)
		}

		// A second run keeps its own gapless sequence.
		if err := s.Append(ctx, "run-2", Event{Payload: types.LimitWarning{Limit: "tokens"}}); err != nil {
			t.Fatalf("append run-2: %v", err)
		}
		for e, err := range s.Read(ctx, "run-2", 0) {
			if err != nil {
				t.Fatalf("read run-2: %v", err)
			}
			if e.Meta.Seq != 1 {
				t.Fatalf("run-2 first seq = %d, want 1", e.Meta.Seq)
			}
		}

		// Expiry clears retained payloads but never restarts the run's
		// sequence: the next append for the same run keeps counting.
		clock = now.Add(time.Hour)
		if err := s.Expire(ctx, clock); err != nil {
			t.Fatalf("expire: %v", err)
		}
		// Nothing is retained after the expiry: a cursor below the next
		// sequence is refused instead of served an empty overlapping
		// window, and a cursor at the next sequence serves nothing.
		for _, err := range s.Read(ctx, "run-1", 0) {
			if !errors.Is(err, ErrStaleCursor) {
				t.Fatalf("read after expire at cursor 0 = %v, want ErrStaleCursor", err)
			}
			break
		}
		var remaining []int64
		for e, err := range s.Read(ctx, "run-1", n) {
			if err != nil {
				t.Fatalf("read after expire at cursor %d: %v", n, err)
			}
			remaining = append(remaining, e.Meta.Seq)
		}
		if len(remaining) != 0 {
			t.Fatalf("seqs after expiry = %v, want none", remaining)
		}

		if err := s.Append(ctx, "run-1", Event{Payload: types.LimitWarning{Limit: "tokens"}}); err != nil {
			t.Fatalf("append after expire: %v", err)
		}
		for e, err := range s.Read(ctx, "run-1", n) {
			if err != nil {
				t.Fatalf("read refilled: %v", err)
			}
			if e.Meta.Seq != n+1 {
				t.Fatalf("refilled seq = %d, want %d: expiry must not restart numbering", e.Meta.Seq, n+1)
			}
		}
	})
}
