package gohan

import (
	"errors"
	"testing"
)

func TestStreamHelpers(t *testing.T) {
	t.Run("collect-generic-seam", func(t *testing.T) {
		chunks := []string{"a", "b"}
		seq := func(yield func(string, error) bool) {
			for _, c := range chunks {
				if !yield(c, nil) {
					return
				}
			}
		}
		got, err := Collect(seq)
		if err != nil || len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Fatalf("Collect = %v, %v; want [a b], nil", got, err)
		}
	})

	t.Run("early-break-sees-nothing-lost", func(t *testing.T) {
		// Breaking early must not make the producer buffer an error behind
		// the events already yielded: the prefix stands on its own.
		events, err := Collect(seqOf(errTuple{ev: TextDelta{Delta: "kept"}}))
		if err != nil || len(events) != 1 {
			t.Fatalf("prefix Collect = %v, %v; want one event, nil", events, err)
		}
	})

	t.Run("drain-generic-seam", func(t *testing.T) {
		wantErr := errors.New("boom")
		seq := func(yield func(int, error) bool) {
			yield(1, nil)
			yield(0, wantErr)
		}
		if err := Drain(seq); !errors.Is(err, wantErr) {
			t.Fatalf("Drain error = %v, want %v", err, wantErr)
		}
	})
}
