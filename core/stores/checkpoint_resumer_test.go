package stores

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// Regression tests for the conditional-resume surface of MemoryCheckpoints:
// owned snapshots, one-winner ConsumeIf, stale-snapshot conflicts across
// every field and Data-only UpdatePending (openspec/specs/stores/spec.md,
// "Conditional resume").
func TestMemoryCheckpointsResumer(t *testing.T) {
	t.Parallel()

	newStore := func(t *testing.T, now time.Time) *MemoryCheckpoints {
		t.Helper()
		clock := now
		return NewMemoryCheckpoints(
			WithMemoryCheckpointClock(func() time.Time { return clock }),
			WithMemoryCheckpointRunInfo(func(context.Context) (types.RunInfo, bool) {
				return types.RunInfo{RunID: "run-resumer"}, true
			}),
		)
	}

	sample := func() Checkpoint {
		return Checkpoint{
			SchemaVersion:  CurrentSchemaVersion,
			SessionID:      "s-1",
			Flow:           "booking",
			Backend:        "eino",
			BackendVersion: "adapter-x",
			Reason:         types.HumanApproval,
			Originator:     types.Principal{Subject: "u1", Tenant: "t1", Scopes: []string{"flow:run"}},
			Data:           []byte(`{"state":"v1"}`),
		}
	}

	clone := func(cp Checkpoint) Checkpoint {
		cp.Data = append([]byte(nil), cp.Data...)
		cp.Originator.Scopes = append([]string(nil), cp.Originator.Scopes...)
		return cp
	}

	ctx := t.Context()
	now := time.Unix(1_000_000, 0)

	t.Run("peek_returns_owned_snapshot_and_changes_nothing", func(t *testing.T) {
		t.Parallel()
		s := newStore(t, now)
		tok, err := s.Put(ctx, sample())
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		first, err := s.Peek(ctx, tok)
		if err != nil {
			t.Fatalf("Peek: %v", err)
		}
		second, err := s.Peek(ctx, tok)
		if err != nil {
			t.Fatalf("second Peek: %v", err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("Peek snapshots differ: %+v vs %+v", first, second)
		}
		first.Data[0] = 'X'
		first.Originator.Scopes[0] = "mutated"
		third, err := s.Peek(ctx, tok)
		if err != nil {
			t.Fatalf("Peek after caller mutation: %v", err)
		}
		if !reflect.DeepEqual(third, sample()) {
			t.Fatalf("caller mutated store-owned bytes: Peek = %+v", third)
		}
		if _, input, err := s.PendingInput(ctx, "run-resumer"); err != nil || !reflect.DeepEqual(input, ResumeInput{}) {
			t.Fatalf("Peek changed token state: input = %+v err = %v", input, err)
		}
	})

	t.Run("peek_unknown_token_is_mismatch", func(t *testing.T) {
		t.Parallel()
		s := newStore(t, now)
		if _, err := s.Peek(ctx, types.ResumeToken("cp_missing")); !errors.Is(err, types.ErrTokenMismatch) {
			t.Fatalf("Peek unknown token error = %v, want ErrTokenMismatch", err)
		}
	})

	t.Run("stale_snapshot_conflicts_across_every_field", func(t *testing.T) {
		t.Parallel()
		s := newStore(t, now)
		tok, err := s.Put(ctx, sample())
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		live, err := s.Peek(ctx, tok)
		if err != nil {
			t.Fatalf("Peek: %v", err)
		}
		moved := clone(live)
		moved.Data = []byte(`{"state":"v2"}`)
		if err := s.UpdatePending(ctx, tok, live, moved); err != nil {
			t.Fatalf("UpdatePending: %v", err)
		}
		mutations := map[string]func(*Checkpoint){
			"session":          func(c *Checkpoint) { c.SessionID = "other" },
			"flow":             func(c *Checkpoint) { c.Flow = "other" },
			"backend":          func(c *Checkpoint) { c.Backend = "other" },
			"backend version":  func(c *Checkpoint) { c.BackendVersion = "other" },
			"reason":           func(c *Checkpoint) { c.Reason = types.SuspendReason("other") },
			"originator scope": func(c *Checkpoint) { c.Originator.Scopes = append(c.Originator.Scopes, "extra") },
			"data":             func(c *Checkpoint) { c.Data = []byte(`{"state":"v9"}`) },
			"child":            func(c *Checkpoint) { c.Child = types.ResumeToken("cp_other") },
			"expiry":           func(c *Checkpoint) { c.ExpiresAt = c.ExpiresAt.Add(time.Minute) },
		}
		for name, mutate := range mutations {
			stale := clone(moved)
			mutate(&stale)
			if _, err := s.ConsumeIf(ctx, tok, stale, ResumeInput{Verdict: VerdictApprove}); !errors.Is(err, types.ErrVersionConflict) {
				t.Fatalf("%s: ConsumeIf with stale snapshot error = %v, want ErrVersionConflict", name, err)
			}
			if err := s.UpdatePending(ctx, tok, stale, moved); !errors.Is(err, types.ErrVersionConflict) {
				t.Fatalf("%s: UpdatePending with stale snapshot error = %v, want ErrVersionConflict", name, err)
			}
		}
		if _, input, err := s.PendingInput(ctx, "run-resumer"); err != nil || !reflect.DeepEqual(input, ResumeInput{}) {
			t.Fatalf("conflicted calls recorded input %+v, want zero", input)
		}
		if _, err := s.Consume(ctx, tok, ResumeInput{Verdict: VerdictApprove}); err != nil {
			t.Fatalf("Consume after refused conditional calls: %v, want the token still unconsumed", err)
		}
	})

	t.Run("consume_if_records_input_and_consumes", func(t *testing.T) {
		t.Parallel()
		s := newStore(t, now)
		tok, err := s.Put(ctx, sample())
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		expected, err := s.Peek(ctx, tok)
		if err != nil {
			t.Fatalf("Peek: %v", err)
		}
		want := ResumeInput{
			Approver: &types.Principal{Subject: "appr-1", Tenant: "t1"},
			Verdict:  VerdictApprove,
			Reason:   "ok",
		}
		got, err := s.ConsumeIf(ctx, tok, expected, want)
		if err != nil {
			t.Fatalf("ConsumeIf: %v", err)
		}
		if wantCP := sample(); !reflect.DeepEqual(got, wantCP) {
			t.Fatalf("ConsumeIf checkpoint = %+v, want %+v", got, wantCP)
		}
		if _, err := s.Consume(ctx, tok, want); !errors.Is(err, types.ErrTokenConsumed) {
			t.Fatalf("second Consume error = %v, want ErrTokenConsumed", err)
		}
		if _, input, err := s.PendingInput(ctx, "run-resumer"); err != nil || !reflect.DeepEqual(input, want) {
			t.Fatalf("recorded input = %+v err = %v, want %+v", input, err, want)
		}
	})

	t.Run("concurrent_consume_if_has_one_winner", func(t *testing.T) {
		t.Parallel()
		s := newStore(t, now)
		tok, err := s.Put(ctx, sample())
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		expected, err := s.Peek(ctx, tok)
		if err != nil {
			t.Fatalf("Peek: %v", err)
		}
		const n = 10
		errs := make([]error, n)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, errs[i] = s.ConsumeIf(ctx, tok, expected, ResumeInput{Verdict: VerdictApprove})
			}()
		}
		close(start)
		wg.Wait()
		won := 0
		for _, err := range errs {
			switch {
			case err == nil:
				won++
			case errors.Is(err, types.ErrTokenConsumed), errors.Is(err, types.ErrVersionConflict):
			default:
				t.Fatalf("ConsumeIf error = %v, want nil, ErrTokenConsumed or ErrVersionConflict", err)
			}
		}
		if won != 1 {
			t.Fatalf("got %d successful ConsumeIf calls, want exactly 1", won)
		}
	})

	t.Run("update_pending_changes_only_data", func(t *testing.T) {
		t.Parallel()
		s := newStore(t, now)
		tok, err := s.Put(ctx, sample())
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		expected, err := s.Peek(ctx, tok)
		if err != nil {
			t.Fatalf("Peek: %v", err)
		}
		next := clone(expected)
		next.Data = []byte(`{"state":"v2","approvals":["appr-1"]}`)
		tampered := clone(next)
		tampered.SessionID = "tampered"
		tampered.Flow = "tampered"
		tampered.Backend = "tampered"
		tampered.BackendVersion = "tampered"
		tampered.Reason = types.SuspendReason("tampered")
		tampered.Originator = types.Principal{Subject: "tampered", Tenant: "tampered"}
		tampered.Child = types.ResumeToken("cp_tampered")
		tampered.ExpiresAt = expected.ExpiresAt.Add(time.Hour)
		if err := s.UpdatePending(ctx, tok, expected, tampered); !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("UpdatePending with immutable-field changes error = %v, want ErrVersionConflict", err)
		}
		if err := s.UpdatePending(ctx, tok, expected, next); err != nil {
			t.Fatalf("UpdatePending: %v", err)
		}

		stored, err := s.Peek(ctx, tok)
		if err != nil {
			t.Fatalf("Peek after UpdatePending: %v", err)
		}
		want := sample()
		want.Data = []byte(`{"state":"v2","approvals":["appr-1"]}`)
		if !reflect.DeepEqual(stored, want) {
			t.Fatalf("UpdatePending changed more than Data: stored = %+v, want %+v", stored, want)
		}
	})

	t.Run("update_pending_after_consumption_refuses", func(t *testing.T) {
		t.Parallel()
		s := newStore(t, now)
		tok, err := s.Put(ctx, sample())
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		expected, err := s.Peek(ctx, tok)
		if err != nil {
			t.Fatalf("Peek: %v", err)
		}
		want := ResumeInput{Approver: &types.Principal{Subject: "appr-1"}, Verdict: VerdictApprove}
		if _, err := s.ConsumeIf(ctx, tok, expected, want); err != nil {
			t.Fatalf("ConsumeIf: %v", err)
		}
		next := clone(expected)
		next.Data = append(next.Data, []byte(` `)...)
		err = s.UpdatePending(ctx, tok, expected, next)
		if err == nil {
			t.Fatal("UpdatePending on a consumed token succeeded, want an error")
		}
		if !errors.Is(err, types.ErrTokenConsumed) && !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("UpdatePending on consumed token error = %v, want ErrTokenConsumed or ErrVersionConflict", err)
		}
		if _, input, err := s.PendingInput(ctx, "run-resumer"); err != nil || !reflect.DeepEqual(input, want) {
			t.Fatalf("refused UpdatePending overwrote input: got %+v err = %v, want %+v", input, err, want)
		}
	})
}
