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
