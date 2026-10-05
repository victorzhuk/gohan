package main

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func newTestTrip() *Trip {
	return NewTrip(NewLiveFlags(map[string]any{"booking.online": true}))
}

func TestTemporalTravelOffline(t *testing.T) {
	ctx := context.Background()

	t.Run("engines.crash-after-consume", func(t *testing.T) {
		trip := newTestTrip()
		if err := trip.Run(ctx); err != nil {
			t.Fatalf("run to suspension: %v", err)
		}
		if _, err := trip.Signal(ctx); err != nil {
			t.Fatalf("signal: %v", err)
		}
		trip.crash()
		searches := trip.rt.searches
		if err := trip.RecoverReplay(ctx); err != nil {
			t.Fatalf("recover: %v", err)
		}
		if trip.rt.bookings != 1 {
			t.Fatalf("bookings after recovery = %d, want exactly 1", trip.rt.bookings)
		}
		if trip.rt.searches != searches {
			t.Fatalf("search re-executed after recovery, want journal replay")
		}
		if _, err := trip.Signal(ctx); !errors.Is(err, types.ErrTokenConsumed) {
			t.Fatalf("second consume = %v, want ErrTokenConsumed", err)
		}
		if err := trip.RecoverReplay(ctx); err != nil {
			t.Fatalf("second recover: %v", err)
		}
		if trip.rt.bookings != 1 {
			t.Fatalf("bookings after second recovery = %d, want still 1", trip.rt.bookings)
		}
	})

	t.Run("engines.frozen-flag-on-replay", func(t *testing.T) {
		trip := newTestTrip()
		if err := trip.Run(ctx); err != nil {
			t.Fatalf("run to suspension: %v", err)
		}
		trip.flags.Set("booking.online", false)
		if _, err := trip.Signal(ctx); err != nil {
			t.Fatalf("signal: %v", err)
		}
		trip.crash()
		if err := trip.RecoverReplay(ctx); err != nil {
			t.Fatalf("recover under flipped flag: %v", err)
		}
		if trip.rt.bookings != 1 {
			t.Fatalf("bookings = %d, want 1 on the snapshotted flag value", trip.rt.bookings)
		}
	})

	t.Run("engines.replay-is-step-re-execution", func(t *testing.T) {
		trip := newTestTrip()
		if err := trip.Run(ctx); err != nil {
			t.Fatalf("run to suspension: %v", err)
		}
		execSearches := trip.rt.searches
		match, err := trip.ReplayStep(ctx)
		if err != nil {
			t.Fatalf("replay: %v", err)
		}
		if !match {
			t.Fatal("replayed State sequence differs from the first execution")
		}
		if trip.rt.searches != execSearches {
			t.Fatalf("replay executed %d new searches, want the journal to answer", trip.rt.searches-execSearches)
		}
		if len(trip.rt.steps) < 2 {
			t.Fatalf("recorded %d step states, want at least the Continue and suspension states", len(trip.rt.steps))
		}
	})
}
