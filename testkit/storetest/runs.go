// Package storetest holds conformance suites for store ports. Each suite
// drives an implementation through an injected factory and asserts the
// port contract, not any one implementation. The suite never imports a
// concrete store; implementations are bound from external tests.
package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// maxPendingSignals is the port's mailbox bound; the eleventh Signal on a
// run whose mailbox holds ten must fail with types.ErrMailboxFull.
const maxPendingSignals = 10

// RunState mirrors the port's run lifecycle states without naming a
// particular implementation's type.
type RunState int

const (
	RunRunning RunState = iota
	RunSuspended
	RunResuming
	RunFinished
	RunFailed
)

// SignalKind mirrors the port's signal kinds.
type SignalKind int

const (
	SignalCancel SignalKind = iota
	SignalSteer
)

// Lease is the store-minted proof that a caller drives a run.
type Lease struct {
	RunID      string
	Generation uint64
}

// RunRow is the port-level shape of a run row, carrying the fields the
// suite asserts.
type RunRow struct {
	SessionID   string
	RunID       string
	OperationID string
	State       RunState
	Heartbeat   time.Time
	Uncertain   []types.CallKey
	ResultRef   string
}

// Signal is one mailbox entry for a run. At is store time and
// informational; the store stamps it.
type Signal struct {
	Kind    SignalKind
	Message types.Message
}

// RunClock advances the store's own clock. The store clock is
// authoritative for lease expiry, staleness and mailbox stamps, so the
// suite drives time only through it.
type RunClock interface {
	Advance(d time.Duration)
}

// RunStore is the Runs port as the conformance suite sees it.
type RunStore interface {
	Start(ctx context.Context, r RunRow, ttl time.Duration) (Lease, error)
	Heartbeat(ctx context.Context, l Lease) (Lease, error)
	Suspend(ctx context.Context, l Lease, token types.ResumeToken) error
	Resuming(ctx context.Context, runID string, ttl time.Duration) (Lease, error)
	Finish(ctx context.Context, l Lease, state RunState, uncertain []types.CallKey, resultRef string) error
	ByOperation(ctx context.Context, tenant, operationID string) (RunRow, error)
	Stale(ctx context.Context, staleAfter time.Duration, limit int) ([]RunRow, error)
	Reclaim(ctx context.Context, r RunRow, ttl time.Duration) (Lease, error)
	Signal(ctx context.Context, runID string, s Signal) error
	Drain(ctx context.Context, l Lease) ([]Signal, error)
	Notices(ctx context.Context, limit int) ([]types.RunNotice, error)
	AckNotice(ctx context.Context, id string) error
}

// RunFactory returns a fresh, empty store with its own clock.
type RunFactory func(t *testing.T) (RunStore, RunClock)

// Runs runs the Runs conformance suite against the implementation
// produced by newRuns. It covers stores.lease-exclusivity,
// stores.reclaim-race, stores.stale-by-store-clock,
// stores.signal-cancel-cross-pod, stores.mailbox-full and
// recovery.no-double-run.
func Runs(t *testing.T, newRuns RunFactory) {
	t.Run("stores.lease-exclusivity", func(t *testing.T) {
		ctx := context.Background()
		s, _ := newRuns(t)
		l1, err := s.Start(ctx, RunRow{SessionID: "sess-a", RunID: "run-a1"}, time.Minute)
		if err != nil {
			t.Fatalf("first Start: %v", err)
		}
		if l1.RunID != "run-a1" {
			t.Fatalf("lease for run %q, want run-a1", l1.RunID)
		}
		_, err = s.Start(ctx, RunRow{SessionID: "sess-a", RunID: "run-a2"}, time.Minute)
		if !errors.Is(err, types.ErrRunActive) {
			t.Fatalf("second Start on leased session: %v, want ErrRunActive", err)
		}
	})

	t.Run("recovery.no-double-run", func(t *testing.T) {
		ctx := context.Background()
		s, _ := newRuns(t)
		if _, err := s.Start(ctx, RunRow{SessionID: "sess-b", RunID: "run-b1", OperationID: "op-1"}, time.Minute); err != nil {
			t.Fatalf("Start: %v", err)
		}
		// A client retry of the same operation sees the active lease
		// and no second run is created. The port does not fix which
		// sentinel a same-session duplicate reports.
		_, err := s.Start(ctx, RunRow{SessionID: "sess-b", RunID: "run-b2", OperationID: "op-1"}, time.Minute)
		if !errors.Is(err, types.ErrRunActive) && !errors.Is(err, types.ErrOperationExists) {
			t.Fatalf("retry with recorded operation: %v, want ErrRunActive or ErrOperationExists", err)
		}
		got, err := s.ByOperation(ctx, "", "op-1")
		if err != nil {
			t.Fatalf("ByOperation after duplicate: %v", err)
		}
		if got.RunID != "run-b1" {
			t.Fatalf("ByOperation returned run %q, want run-b1", got.RunID)
		}
		// The operation id stays taken even on a session without a
		// live lease.
		_, err = s.Start(ctx, RunRow{SessionID: "sess-b2", RunID: "run-b3", OperationID: "op-1"}, time.Minute)
		if !errors.Is(err, types.ErrOperationExists) {
			t.Fatalf("duplicate operation on other session: %v, want ErrOperationExists", err)
		}
	})

	t.Run("operation-idempotent-across-finish", func(t *testing.T) {
		ctx := context.Background()
		s, _ := newRuns(t)
		l, err := s.Start(ctx, RunRow{SessionID: "sess-c", RunID: "run-c1", OperationID: "op-2"}, time.Minute)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		key := types.CallKey{SessionID: "sess-c", CallID: "call-1"}
		if err := s.Finish(ctx, l, RunFinished, []types.CallKey{key}, "blob://out"); err != nil {
			t.Fatalf("Finish: %v", err)
		}
		got, err := s.ByOperation(ctx, "", "op-2")
		if err != nil {
			t.Fatalf("ByOperation after Finish: %v", err)
		}
		if got.State != RunFinished || got.ResultRef != "blob://out" {
			t.Fatalf("recorded run state %v resultRef %q, want finished blob://out", got.State, got.ResultRef)
		}
		if len(got.Uncertain) != 1 || got.Uncertain[0] != key {
			t.Fatalf("recorded uncertain %v, want [call-1]", got.Uncertain)
		}
		// A deliberate re-execution needs a new operation id: the old
		// one stays taken after Finish.
		_, err = s.Start(ctx, RunRow{SessionID: "sess-c", RunID: "run-c2", OperationID: "op-2"}, time.Minute)
		if !errors.Is(err, types.ErrOperationExists) {
			t.Fatalf("Start with finished operation id: %v, want ErrOperationExists", err)
		}
	})

	t.Run("stores.stale-by-store-clock", func(t *testing.T) {
		ctx := context.Background()
		s, clk := newRuns(t)
		if _, err := s.Start(ctx, RunRow{SessionID: "sess-d", RunID: "run-d1"}, time.Minute); err != nil {
			t.Fatalf("Start: %v", err)
		}
		clk.Advance(20 * time.Second)
		stale, err := s.Stale(ctx, 30*time.Second, 10)
		if err != nil {
			t.Fatalf("Stale at 20s: %v", err)
		}
		if len(stale) != 0 {
			t.Fatalf("Stale listed %d runs at 20s heartbeat age, want 0", len(stale))
		}
		clk.Advance(15 * time.Second)
		stale, err = s.Stale(ctx, 30*time.Second, 10)
		if err != nil {
			t.Fatalf("Stale at 35s: %v", err)
		}
		if len(stale) != 1 || stale[0].RunID != "run-d1" {
			t.Fatalf("Stale at 35s returned %v, want run-d1", stale)
		}
	})

	t.Run("stores.reclaim-race", func(t *testing.T) {
		ctx := context.Background()
		s, clk := newRuns(t)
		if _, err := s.Start(ctx, RunRow{SessionID: "sess-e", RunID: "run-e1"}, 10*time.Second); err != nil {
			t.Fatalf("Start: %v", err)
		}
		clk.Advance(20 * time.Second)
		stale, err := s.Stale(ctx, 10*time.Second, 10)
		if err != nil || len(stale) != 1 {
			t.Fatalf("Stale: %v rows=%d", err, len(stale))
		}
		var wg sync.WaitGroup
		results := make(chan error, 10)
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := s.Reclaim(ctx, stale[0], time.Minute)
				results <- err
			}()
		}
		wg.Wait()
		close(results)
		won, lost := 0, 0
		for err := range results {
			switch {
			case err == nil:
				won++
			case errors.Is(err, types.ErrRunNotActive):
				lost++
			default:
				t.Fatalf("Reclaim: unexpected error %v", err)
			}
		}
		if won != 1 || lost != 9 {
			t.Fatalf("reclaim race: %d won, %d lost, want 1 won 9 lost", won, lost)
		}
	})

	t.Run("stale-covers-resuming", func(t *testing.T) {
		ctx := context.Background()
		s, clk := newRuns(t)
		l, err := s.Start(ctx, RunRow{SessionID: "sess-f", RunID: "run-f1"}, 10*time.Second)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := s.Suspend(ctx, l, "tok-1"); err != nil {
			t.Fatalf("Suspend: %v", err)
		}
		// A suspended run is not recoverable until Resuming takes a
		// fresh lease.
		if _, err := s.Resuming(ctx, "run-f1", 10*time.Second); err != nil {
			t.Fatalf("Resuming: %v", err)
		}
		// A crash after Resuming leaves a Resuming run the reaper sees.
		clk.Advance(20 * time.Second)
		stale, err := s.Stale(ctx, 10*time.Second, 10)
		if err != nil {
			t.Fatalf("Stale: %v", err)
		}
		if len(stale) != 1 || stale[0].RunID != "run-f1" {
			t.Fatalf("Stale after Resuming returned %v, want run-f1", stale)
		}
	})

	t.Run("skewed-caller-cannot-reclaim", func(t *testing.T) {
		ctx := context.Background()
		s, clk := newRuns(t)
		l, err := s.Start(ctx, RunRow{SessionID: "sess-g", RunID: "run-g1"}, 10*time.Second)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		// The lease is live by the store's clock; no caller decision
		// shortens it, so reclaiming without a Stale listing fails.
		if _, err := s.Reclaim(ctx, RunRow{SessionID: "sess-g", RunID: "run-g1"}, time.Minute); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Reclaim on live lease: %v, want ErrRunNotActive", err)
		}
		// Once the store clock passes the ttl, every lease-holder call
		// is rejected and the run itself is left in place.
		clk.Advance(20 * time.Second)
		_, err = s.Heartbeat(ctx, l)
		if !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Heartbeat: %v, want ErrRunNotActive", err)
		}
	})

	t.Run("stores.signal-cancel-cross-pod", func(t *testing.T) {
		ctx := context.Background()
		s, _ := newRuns(t)
		l, err := s.Start(ctx, RunRow{SessionID: "sess-h", RunID: "run-h1"}, time.Minute)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		// The signal comes from another pod without a lease; the
		// holder sees it on the next drain.
		if err := s.Signal(ctx, "run-h1", Signal{Kind: SignalCancel}); err != nil {
			t.Fatalf("Signal: %v", err)
		}
		got, err := s.Drain(ctx, l)
		if err != nil {
			t.Fatalf("Drain: %v", err)
		}
		if len(got) != 1 || got[0].Kind != SignalCancel {
			t.Fatalf("Drain returned %v, want one cancel", got)
		}
		// Drained signals do not come back.
		got, err = s.Drain(ctx, l)
		if err != nil || len(got) != 0 {
			t.Fatalf("second Drain returned %v, %v", got, err)
		}
	})

	t.Run("stores.mailbox-full", func(t *testing.T) {
		ctx := context.Background()
		s, _ := newRuns(t)
		l, err := s.Start(ctx, RunRow{SessionID: "sess-i", RunID: "run-i1"}, time.Minute)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		for i := range maxPendingSignals {
			if err := s.Signal(ctx, "run-i1", Signal{Kind: SignalSteer}); err != nil {
				t.Fatalf("Signal %d: %v", i+1, err)
			}
		}
		if err := s.Signal(ctx, "run-i1", Signal{Kind: SignalSteer}); !errors.Is(err, types.ErrMailboxFull) {
			t.Fatalf("eleventh Signal: %v, want ErrMailboxFull", err)
		}
		got, err := s.Drain(ctx, l)
		if err != nil {
			t.Fatalf("Drain: %v", err)
		}
		if len(got) != maxPendingSignals {
			t.Fatalf("Drain returned %d signals, want %d", len(got), maxPendingSignals)
		}
		for i, sig := range got {
			if sig.Kind != SignalSteer {
				t.Fatalf("signal %d kind %v, want steer", i, sig.Kind)
			}
		}
	})

	t.Run("heartbeat-refresh-and-expiry", func(t *testing.T) {
		ctx := context.Background()
		s, clk := newRuns(t)
		l, err := s.Start(ctx, RunRow{SessionID: "sess-j", RunID: "run-j1"}, 10*time.Second)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		clk.Advance(6 * time.Second)
		l2, err := s.Heartbeat(ctx, l)
		if err != nil {
			t.Fatalf("Heartbeat: %v", err)
		}
		clk.Advance(6 * time.Second)
		// The refresh carried the lease past the first expiry; finish
		// still succeeds.
		if err := s.Finish(ctx, l2, RunFinished, nil, ""); err != nil {
			t.Fatalf("Finish after heartbeat: %v", err)
		}
		// A finished run holds no lease and is not reclaimable.
		if _, err := s.Reclaim(ctx, RunRow{SessionID: "sess-j", RunID: "run-j1"}, time.Minute); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Reclaim finished run: %v, want ErrRunNotActive", err)
		}
	})

	t.Run("suspend-releases-lease", func(t *testing.T) {
		ctx := context.Background()
		s, _ := newRuns(t)
		l, err := s.Start(ctx, RunRow{SessionID: "sess-k", RunID: "run-k1"}, time.Minute)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := s.Suspend(ctx, l, "tok-2"); err != nil {
			t.Fatalf("Suspend: %v", err)
		}
		// The suspended run holds no lease, so no lease-holder call
		// succeeds against it.
		if _, err := s.Heartbeat(ctx, l); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Heartbeat on suspended run: %v, want ErrRunNotActive", err)
		}
		// Resuming is only valid from suspended.
		if _, err := s.Resuming(ctx, "run-k1", time.Minute); err != nil {
			t.Fatalf("Resuming: %v", err)
		}
		if _, err := s.Resuming(ctx, "run-k1", time.Minute); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("second Resuming: %v, want ErrRunNotActive", err)
		}
		if _, err := s.Resuming(ctx, "run-missing", time.Minute); err == nil {
			t.Fatal("Resuming unknown run succeeded")
		}
	})

	t.Run("notices-outbox", func(t *testing.T) {
		ctx := context.Background()
		s, _ := newRuns(t)
		l, err := s.Start(ctx, RunRow{SessionID: "sess-l", RunID: "run-l1"}, time.Minute)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := s.Suspend(ctx, l, "tok-3"); err != nil {
			t.Fatalf("Suspend: %v", err)
		}
		notices, err := s.Notices(ctx, 10)
		if err != nil {
			t.Fatalf("Notices: %v", err)
		}
		if len(notices) != 1 || notices[0].RunID != "run-l1" || notices[0].SessionID != "sess-l" || notices[0].ID == "" {
			t.Fatalf("Notices returned %+v, want one notice for run-l1", notices)
		}
		if err := s.AckNotice(ctx, notices[0].ID); err != nil {
			t.Fatalf("AckNotice: %v", err)
		}
		notices, err = s.Notices(ctx, 10)
		if err != nil || len(notices) != 0 {
			t.Fatalf("Notices after ack returned %v, %v", notices, err)
		}
		if err := s.AckNotice(ctx, "nt-missing"); err == nil {
			t.Fatal("AckNotice unknown id succeeded")
		}
	})

	t.Run("stale-generation-refusals", func(t *testing.T) {
		ctx := context.Background()
		s, clk := newRuns(t)
		l, err := s.Start(ctx, RunRow{SessionID: "sess-n", RunID: "run-n1"}, 10*time.Second)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		// Once the store clock passes the ttl, the lease is a stale
		// generation: every lease-holder call refuses it and the run
		// stays in place for a reaper.
		clk.Advance(20 * time.Second)
		if _, err := s.Heartbeat(ctx, l); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Heartbeat on stale lease: %v, want ErrRunNotActive", err)
		}
		if err := s.Finish(ctx, l, RunFinished, nil, ""); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Finish on stale lease: %v, want ErrRunNotActive", err)
		}
		if err := s.Suspend(ctx, l, "tok-n1"); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Suspend on stale lease: %v, want ErrRunNotActive", err)
		}
		if _, err := s.Drain(ctx, l); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Drain on stale lease: %v, want ErrRunNotActive", err)
		}
		// Reclaim mints the next generation; the new lease works while
		// the old one, whose expiry the clock already passed, stays
		// refused.
		l2, err := s.Reclaim(ctx, RunRow{SessionID: "sess-n", RunID: "run-n1"}, time.Minute)
		if err != nil {
			t.Fatalf("Reclaim: %v", err)
		}
		if _, err := s.Heartbeat(ctx, l2); err != nil {
			t.Fatalf("Heartbeat on reclaimed lease: %v", err)
		}
		if _, err := s.Drain(ctx, l); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Drain on stale lease after reclaim: %v, want ErrRunNotActive", err)
		}
		if err := s.Finish(ctx, l2, RunFinished, nil, ""); err != nil {
			t.Fatalf("Finish on reclaimed lease: %v", err)
		}
	})

	t.Run("signal-unknown-run", func(t *testing.T) {
		ctx := context.Background()
		s, _ := newRuns(t)
		err := s.Signal(ctx, "run-missing", Signal{Kind: SignalCancel})
		if err == nil {
			t.Fatal("Signal unknown run succeeded")
		}
		if _, err := s.Drain(ctx, Lease{RunID: "run-missing"}); err == nil {
			t.Fatal("Drain unknown lease succeeded")
		}
	})
}
