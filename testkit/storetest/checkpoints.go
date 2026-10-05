package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// ApprovalVerdict mirrors the verdict the port's Consume records.
type ApprovalVerdict int

const (
	VerdictApprove ApprovalVerdict = iota
	VerdictReject
	VerdictEdit
)

// ResumeInput mirrors the recorded resume decision. The approver is set by
// transport, so the suite treats it as opaque data the store must preserve.
type ResumeInput struct {
	Approver *types.Principal
	Verdict  ApprovalVerdict
	Args     json.RawMessage
	Data     json.RawMessage
	Reason   string
}

// Checkpoint mirrors the suspended-run record the port stores.
type Checkpoint struct {
	SessionID      string
	Flow           string
	Backend        string
	BackendVersion string
	Reason         types.SuspendReason
	Originator     types.Principal
	Data           []byte
	Child          types.ResumeToken
	Workspace      string
	ExpiresAt      time.Time
}

// Checkpoints is the port under test. It mirrors the consumer-owned
// interface the stores declare; implementations adapt to it in their own
// binding tests.
type CheckpointStore interface {
	Put(ctx context.Context, cp Checkpoint) (types.ResumeToken, error)
	Consume(ctx context.Context, t types.ResumeToken, in ResumeInput) (Checkpoint, error)
	PendingInput(ctx context.Context, runID string) (Checkpoint, ResumeInput, error)
}

// CheckpointsFactory builds a fresh, empty Checkpoints per test. runID is
// the run id the store must associate with checkpoints put under the
// factory-supplied context, so PendingInput can find them.
type CheckpointsFactory func(ctx context.Context, runID string) (CheckpointStore, error)

// Checkpoints runs the port conformance suite against one implementation.
func Checkpoints(t *testing.T, factory CheckpointsFactory) {
	t.Helper()
	t.Run("put_mints_token_and_roundtrips", func(t *testing.T) {
		ctx := context.Background()
		s := openCheckpoints(t, factory)
		in := sampleCheckpoint()
		tok, err := s.Put(ctx, in)
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		if tok == "" {
			t.Fatal("Put returned an empty token")
		}
		got, input, err := s.PendingInput(ctx, runID)
		if err != nil {
			t.Fatalf("PendingInput: %v", err)
		}
		if !equalCheckpoints(got, in) {
			t.Fatalf("PendingInput checkpoint = %+v, want %+v", got, in)
		}
		if !reflect.DeepEqual(input, ResumeInput{}) {
			t.Fatalf("PendingInput before Consume recorded input %+v, want zero", input)
		}
	})

	t.Run("consume_returns_checkpoint_and_records_input", func(t *testing.T) {
		ctx := context.Background()
		s := openCheckpoints(t, factory)
		in := sampleCheckpoint()
		tok, err := s.Put(ctx, in)
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		want := ResumeInput{
			Approver: &types.Principal{Subject: "appr-1", Tenant: "t1"},
			Verdict:  VerdictEdit,
			Args:     json.RawMessage(`{"x":1}`),
			Data:     json.RawMessage(`{"y":2}`),
			Reason:   "ok",
		}
		got, err := s.Consume(ctx, tok, want)
		if err != nil {
			t.Fatalf("Consume: %v", err)
		}
		if !equalCheckpoints(got, in) {
			t.Fatalf("Consume checkpoint = %+v, want %+v", got, in)
		}
		_, rec, err := s.PendingInput(ctx, runID)
		if err != nil {
			t.Fatalf("PendingInput: %v", err)
		}
		if !reflect.DeepEqual(rec, want) {
			t.Fatalf("recorded input = %+v, want %+v", rec, want)
		}
	})

	t.Run("unknown_token_is_mismatch", func(t *testing.T) {
		ctx := context.Background()
		s := openCheckpoints(t, factory)
		_, err := s.Consume(ctx, types.ResumeToken("cp_missing"), ResumeInput{})
		if !errors.Is(err, types.ErrTokenMismatch) {
			t.Fatalf("Consume unknown token error = %v, want ErrTokenMismatch", err)
		}
	})

	t.Run("consume_is_single_use", func(t *testing.T) {
		ctx := context.Background()
		s := openCheckpoints(t, factory)
		tok, err := s.Put(ctx, sampleCheckpoint())
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		if _, err := s.Consume(ctx, tok, ResumeInput{Verdict: VerdictApprove}); err != nil {
			t.Fatalf("first Consume: %v", err)
		}
		_, err = s.Consume(ctx, tok, ResumeInput{Verdict: VerdictApprove})
		if !errors.Is(err, types.ErrTokenConsumed) {
			t.Fatalf("second Consume error = %v, want ErrTokenConsumed", err)
		}
	})

	t.Run("concurrent_consume", func(t *testing.T) {
		ctx := context.Background()
		s := openCheckpoints(t, factory)
		tok, err := s.Put(ctx, sampleCheckpoint())
		if err != nil {
			t.Fatalf("Put: %v", err)
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
				_, errs[i] = s.Consume(ctx, tok, ResumeInput{Verdict: VerdictApprove})
			}()
		}
		close(start)
		wg.Wait()
		won := 0
		for _, err := range errs {
			switch {
			case err == nil:
				won++
			case errors.Is(err, types.ErrTokenConsumed):
			default:
				t.Fatalf("Consume error = %v, want nil or ErrTokenConsumed", err)
			}
		}
		if won != 1 {
			t.Fatalf("got %d successful Consumes, want exactly 1", won)
		}
	})

	t.Run("expired_token_by_store_clock", func(t *testing.T) {
		ctx := context.Background()
		s := openCheckpoints(t, factory)
		cp := sampleCheckpoint()
		cp.ExpiresAt = time.Now().Add(-time.Minute)
		tok, err := s.Put(ctx, cp)
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		_, err = s.Consume(ctx, tok, ResumeInput{Verdict: VerdictApprove})
		if !errors.Is(err, types.ErrTokenExpired) {
			t.Fatalf("Consume expired error = %v, want ErrTokenExpired", err)
		}
	})

	t.Run("no_secrets_stored", func(t *testing.T) {
		ctx := context.Background()
		s := openCheckpoints(t, factory)
		credentialValue := strings.Join([]string{"tok", "secret", "5f3a9c"}, "_")
		cred := types.Credential{Token: credentialValue, ExpiresAt: time.Now().Add(time.Hour)}
		p := types.Principal{Subject: "u1", Tenant: "t1", Scopes: []string{"flow:run"}}
		in := sampleCheckpoint()
		in.Originator = p
		tok, err := s.Put(ctx, in)
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, err := s.Consume(ctx, tok, ResumeInput{Verdict: VerdictApprove})
		if err != nil {
			t.Fatalf("Consume: %v", err)
		}
		pending, _, err := s.PendingInput(ctx, runID)
		if err != nil {
			t.Fatalf("PendingInput: %v", err)
		}
		for name, stored := range map[string]Checkpoint{"consumed": got, "pending": pending} {
			if !reflect.DeepEqual(stored.Originator, p) {
				t.Fatalf("%s: Originator = %+v, want %+v", name, stored.Originator, p)
			}
			blob, err := json.Marshal(stored)
			if err != nil {
				t.Fatalf("%s: marshal checkpoint: %v", name, err)
			}
			if strings.Contains(string(blob), cred.Token) {
				t.Fatalf("%s: stored checkpoint leaks the caller token", name)
			}
		}
	})

	t.Run("put_mints_distinct_tokens", func(t *testing.T) {
		ctx := context.Background()
		s := openCheckpoints(t, factory)
		a, err := s.Put(ctx, sampleCheckpoint())
		if err != nil {
			t.Fatalf("first Put: %v", err)
		}
		b, err := s.Put(ctx, sampleCheckpoint())
		if err != nil {
			t.Fatalf("second Put: %v", err)
		}
		if a == b {
			t.Fatal("Put minted the same token twice")
		}
	})
}

// CheckpointResumerStore mirrors the optional CheckpointResumer surface the
// stores declare; implementations adapt to it in their own binding tests.
type CheckpointResumerStore interface {
	CheckpointStore
	Peek(ctx context.Context, t types.ResumeToken) (Checkpoint, error)
	ConsumeIf(ctx context.Context, t types.ResumeToken, expected Checkpoint, in ResumeInput) (Checkpoint, error)
	UpdatePending(ctx context.Context, t types.ResumeToken, expected, next Checkpoint) error
}

// CheckpointResumerFactory builds a fresh, empty resumer-capable store per
// test, associated with runID like CheckpointsFactory.
type CheckpointResumerFactory func(ctx context.Context, runID string) (CheckpointResumerStore, error)

// CheckpointsResumer runs the conditional-resume conformance suite against
// one implementation: owned snapshots, one-winner ConsumeIf, stale-snapshot
// conflicts across every field and Data-only UpdatePending.
func CheckpointsResumer(t *testing.T, factory CheckpointResumerFactory) {
	t.Helper()
	open := func(t *testing.T) CheckpointResumerStore {
		t.Helper()
		s, err := factory(context.Background(), runID)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		return s
	}

	t.Run("peek_returns_owned_snapshot_and_changes_nothing", func(t *testing.T) {
		ctx := context.Background()
		s := open(t)
		tok, err := s.Put(ctx, sampleCheckpoint())
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
		if !equalCheckpoints(first, second) {
			t.Fatalf("Peek snapshots differ: %+v vs %+v", first, second)
		}
		if !equalCheckpoints(first, sampleCheckpoint()) {
			t.Fatalf("Peek = %+v, want the stored checkpoint", first)
		}
		first.Data[0] = 'X'
		first.Originator.Scopes[0] = "mutated"
		third, err := s.Peek(ctx, tok)
		if err != nil {
			t.Fatalf("Peek after caller mutation: %v", err)
		}
		if !equalCheckpoints(third, sampleCheckpoint()) {
			t.Fatalf("caller mutated store-owned bytes: Peek = %+v, want %+v", third, sampleCheckpoint())
		}
		if _, input, err := s.PendingInput(ctx, runID); err != nil || !reflect.DeepEqual(input, ResumeInput{}) {
			t.Fatalf("Peek changed token state: input = %+v err = %v, want zero and nil", input, err)
		}
	})

	t.Run("peek_unknown_token_is_mismatch", func(t *testing.T) {
		ctx := context.Background()
		s := open(t)
		if _, err := s.Peek(ctx, types.ResumeToken("cp_missing")); !errors.Is(err, types.ErrTokenMismatch) {
			t.Fatalf("Peek unknown token error = %v, want ErrTokenMismatch", err)
		}
	})

	t.Run("stale_snapshot_conflicts_across_every_field", func(t *testing.T) {
		ctx := context.Background()
		s := open(t)
		tok, err := s.Put(ctx, sampleCheckpoint())
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		live, err := s.Peek(ctx, tok)
		if err != nil {
			t.Fatalf("Peek: %v", err)
		}
		moved := live
		moved.Data = append([]byte(nil), live.Data...)
		moved.Data = append(moved.Data, []byte(` `)...)
		if err := s.UpdatePending(ctx, tok, live, moved); err != nil {
			t.Fatalf("UpdatePending: %v", err)
		}
		mutations := map[string]func(*Checkpoint){
			"session":         func(c *Checkpoint) { c.SessionID = "other" },
			"flow":            func(c *Checkpoint) { c.Flow = "other" },
			"backend":         func(c *Checkpoint) { c.Backend = "other" },
			"backend version": func(c *Checkpoint) { c.BackendVersion = "other" },
			"reason":          func(c *Checkpoint) { c.Reason = types.SuspendReason("other") },
			"originator scope": func(c *Checkpoint) {
				c.Originator.Scopes = append(append([]string(nil), c.Originator.Scopes...), "extra")
			},
			"data":      func(c *Checkpoint) { c.Data = []byte(`{"state":"v9"}`) },
			"workspace": func(c *Checkpoint) { c.Workspace = "other" },
			"child":     func(c *Checkpoint) { c.Child = types.ResumeToken("cp_other") },
			"expiry":    func(c *Checkpoint) { c.ExpiresAt = c.ExpiresAt.Add(time.Minute) },
		}
		for name, mutate := range mutations {
			stale := moved
			stale.Originator = clonePrincipal(moved.Originator)
			stale.Data = append([]byte(nil), moved.Data...)
			mutate(&stale)
			if _, err := s.ConsumeIf(ctx, tok, stale, ResumeInput{Verdict: VerdictApprove}); !errors.Is(err, types.ErrVersionConflict) {
				t.Fatalf("%s: ConsumeIf with stale snapshot error = %v, want ErrVersionConflict", name, err)
			}
			if err := s.UpdatePending(ctx, tok, stale, moved); !errors.Is(err, types.ErrVersionConflict) {
				t.Fatalf("%s: UpdatePending with stale snapshot error = %v, want ErrVersionConflict", name, err)
			}
		}
		if _, input, err := s.PendingInput(ctx, runID); err != nil || !reflect.DeepEqual(input, ResumeInput{}) {
			t.Fatalf("conflicted calls recorded input %+v, want zero", input)
		}
		if _, err := s.Consume(ctx, tok, ResumeInput{Verdict: VerdictApprove}); err != nil {
			t.Fatalf("Consume after refused conditional calls: %v, want the token still unconsumed", err)
		}
	})

	t.Run("consume_if_records_input_and_consumes", func(t *testing.T) {
		ctx := context.Background()
		s := open(t)
		tok, err := s.Put(ctx, sampleCheckpoint())
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
		if !equalCheckpoints(got, sampleCheckpoint()) {
			t.Fatalf("ConsumeIf checkpoint = %+v, want %+v", got, sampleCheckpoint())
		}
		if _, err := s.Consume(ctx, tok, want); !errors.Is(err, types.ErrTokenConsumed) {
			t.Fatalf("second Consume error = %v, want ErrTokenConsumed", err)
		}
		_, input, err := s.PendingInput(ctx, runID)
		if err != nil {
			t.Fatalf("PendingInput: %v", err)
		}
		if !reflect.DeepEqual(input, want) {
			t.Fatalf("recorded input = %+v, want %+v", input, want)
		}
	})

	t.Run("concurrent_consume_if_has_one_winner", func(t *testing.T) {
		ctx := context.Background()
		s := open(t)
		tok, err := s.Put(ctx, sampleCheckpoint())
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
		ctx := context.Background()
		s := open(t)
		tok, err := s.Put(ctx, sampleCheckpoint())
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		expected, err := s.Peek(ctx, tok)
		if err != nil {
			t.Fatalf("Peek: %v", err)
		}
		next := expected
		next.Data = []byte(`{"state":"v2","approvals":["appr-1"]}`)
		tampered := next
		tampered.SessionID = "tampered"
		tampered.Flow = "tampered"
		tampered.Backend = "tampered"
		tampered.BackendVersion = "tampered"
		tampered.Reason = types.SuspendReason("tampered")
		tampered.Originator = types.Principal{Subject: "tampered", Tenant: "tampered"}
		tampered.Workspace = "tampered"
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
		want := sampleCheckpoint()
		want.Data = []byte(`{"state":"v2","approvals":["appr-1"]}`)
		if !equalCheckpoints(stored, want) {
			t.Fatalf("UpdatePending changed more than Data: stored = %+v, want %+v", stored, want)
		}
	})

	t.Run("update_pending_after_consumption_refuses", func(t *testing.T) {
		ctx := context.Background()
		s := open(t)
		tok, err := s.Put(ctx, sampleCheckpoint())
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
		next := expected
		next.Data = append(next.Data, []byte(` `)...)
		err = s.UpdatePending(ctx, tok, expected, next)
		if err == nil {
			t.Fatal("UpdatePending on a consumed token succeeded, want an error")
		}
		if !errors.Is(err, types.ErrTokenConsumed) && !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("UpdatePending on consumed token error = %v, want ErrTokenConsumed or ErrVersionConflict", err)
		}
		_, input, err := s.PendingInput(ctx, runID)
		if err != nil {
			t.Fatalf("PendingInput: %v", err)
		}
		if !reflect.DeepEqual(input, want) {
			t.Fatalf("refused UpdatePending overwrote input: got %+v, want %+v", input, want)
		}
	})
}

func clonePrincipal(p types.Principal) types.Principal {
	out := p
	out.Scopes = append([]string(nil), p.Scopes...)
	return out
}

const runID = "run-storetest"

func openCheckpoints(t *testing.T, factory CheckpointsFactory) CheckpointStore {
	t.Helper()
	s, err := factory(context.Background(), runID)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return s
}

func sampleCheckpoint() Checkpoint {
	return Checkpoint{
		SessionID:      "sess-1",
		Flow:           "approve",
		Backend:        "native",
		BackendVersion: "1",
		Reason:         types.SuspendReason("awaiting_input"),
		Originator:     types.Principal{Subject: "u1", Tenant: "t1", Scopes: []string{"flow:run"}},
		Data:           []byte(`{"state":"v1"}`),
		Child:          "",
		Workspace:      "ws-1",
	}
}

func equalCheckpoints(a, b Checkpoint) bool {
	a.Data = append([]byte(nil), a.Data...)
	b.Data = append([]byte(nil), b.Data...)
	return reflect.DeepEqual(a, b)
}
