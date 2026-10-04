package gohan

import (
	"errors"
	"iter"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

type errTuple struct {
	ev  Event
	err error
}

func seqOf(ts ...errTuple) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		for _, t := range ts {
			if !yield(t.ev, t.err) {
				return
			}
		}
	}
}

func TestStreamErrorProtocol(t *testing.T) {
	t.Run("streams.error-tuple-terminal", func(t *testing.T) {
		wantErr := errors.New("model call failed permanently")
		failure := Terminal(wantErr)
		seq := func(yield func(Event, error) bool) {
			for i := range 3 {
				if !yield(TextDelta{Turn: 0, Delta: string(rune('a' + i))}, nil) {
					return
				}
			}
			for ev, err := range failure {
				if !yield(ev, err) {
					return
				}
			}
		}
		var tuples int
		var lastErr error
		for ev, err := range seq {
			tuples++
			if err != nil {
				lastErr = err
				if ev != nil {
					t.Fatalf("error tuple %d carries event %T, want nil", tuples, ev)
				}
			}
		}
		if tuples != 4 {
			t.Fatalf("got %d tuples, want 3 events plus one terminal tuple", tuples)
		}
		if !errors.Is(lastErr, wantErr) {
			t.Fatalf("terminal error = %v, want %v", lastErr, wantErr)
		}
	})

	t.Run("streams.preflight-error-sole-tuple", func(t *testing.T) {
		var tuples int
		for ev, err := range Preflight(types.ErrRunActive) {
			tuples++
			if ev != nil {
				t.Fatalf("tuple %d carries event %T, want nil", tuples, ev)
			}
			if !errors.Is(err, types.ErrRunActive) {
				t.Fatalf("tuple %d error = %v, want ErrRunActive", tuples, err)
			}
		}
		if tuples != 1 {
			t.Fatalf("got %d tuples, want exactly one", tuples)
		}
	})

	t.Run("streams.collect-helpers", func(t *testing.T) {
		t.Run("last-returns-done", func(t *testing.T) {
			done := Done{Reason: types.StopCompleted, Seq: 2}
			got, err := Last(seqOf(
				errTuple{ev: TextDelta{Delta: "hi"}},
				errTuple{ev: done},
			))
			if err != nil {
				t.Fatalf("Last on a successful stream: %v", err)
			}
			if got.Seq != done.Seq || got.Reason != done.Reason {
				t.Fatalf("Done = %+v, want %+v", got, done)
			}
		})

		t.Run("last-without-done-is-interrupted", func(t *testing.T) {
			if _, err := Last(seqOf(errTuple{ev: TextDelta{Delta: "hi"}})); !errors.Is(err, ErrStreamInterrupted) {
				t.Fatalf("Last on a Done-less stream: %v, want ErrStreamInterrupted", err)
			}
		})

		t.Run("collect-returns-prefix-and-error", func(t *testing.T) {
			wantErr := errors.New("step failed")
			evs, err := Collect(seqOf(
				errTuple{ev: TextDelta{Delta: "a"}},
				errTuple{ev: AssistantMessage{Turn: 0}},
				errTuple{err: wantErr},
			))
			if !errors.Is(err, wantErr) {
				t.Fatalf("Collect error = %v, want %v", err, wantErr)
			}
			if len(evs) != 2 {
				t.Fatalf("Collect returned %d events before the failure, want 2", len(evs))
			}
		})

		t.Run("drain-returns-only-the-error", func(t *testing.T) {
			wantErr := errors.New("step failed")
			if err := Drain(seqOf(errTuple{ev: TextDelta{Delta: "a"}}, errTuple{err: wantErr})); !errors.Is(err, wantErr) {
				t.Fatalf("Drain error = %v, want %v", err, wantErr)
			}
			if err := Drain(seqOf(errTuple{ev: TextDelta{Delta: "a"}})); err != nil {
				t.Fatalf("Drain on a successful stream: %v", err)
			}
		})
	})
}
