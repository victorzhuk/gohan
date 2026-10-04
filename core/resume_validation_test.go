package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// newValidationConv builds a conversation over its own checkpoint store so
// that a token put for one flow is unknown to another.
func newValidationConv(t *testing.T, rt runtime.Runtime, cps stores.Checkpoints) Conversation {
	t.Helper()
	conv, err := NewConversation(nil, "flights", rt,
		WithConversationRuns(stores.NewMemoryRuns()),
		WithConversationEventLog(stores.NewMemoryEventLog()),
		WithConversationCheckpoints(cps),
	)
	if err != nil {
		t.Fatal(err)
	}
	return conv
}

// validationToken puts a suspended-approval checkpoint and returns its
// single-use token.
func validationToken(t *testing.T, cps stores.Checkpoints, p types.Principal) types.ResumeToken {
	t.Helper()
	pending, err := json.Marshal(runtime.State{
		Turn:           1,
		HistoryVersion: 1,
		Pending:        []types.ToolUse{{ID: "call-1", Name: "create_booking"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := cps.Put(types.WithPrincipal(context.Background(), p), stores.Checkpoint{
		SchemaVersion: stores.CurrentSchemaVersion,
		SessionID:     "s1",
		Backend:       "resume.test",
		Reason:        types.HumanApproval,
		Originator:    p,
		Data:          pending,
	})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// doneStep counts one tool execution and records the approval the resumed
// run sees.
func doneStep(t *testing.T, ran *int, approver *types.Principal) func(context.Context, runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	return func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
		*ran++
		if ap, ok := types.ApprovalFrom(ctx); ok {
			*approver = ap.Approver
		}
		return st, nil, runtime.DoneStatus, nil
	}
}

func TestResumeValidation(t *testing.T) {
	alice := types.Principal{Tenant: "acme", Subject: "alice"}
	op := types.Principal{Tenant: "acme", Subject: "op"}
	mallory := types.Principal{Tenant: "acme", Subject: "mallory"}

	t.Run("suspension.mismatch", func(t *testing.T) {
		aStore := stores.NewMemoryCheckpoints()
		token := validationToken(t, aStore, alice)

		// Flow B has its own checkpoint store: the token from flow A is
		// unknown there, so the mismatch surfaces before any component runs.
		var ran int
		conv := newValidationConv(t, &resumeRT{steps: []func(context.Context, runtime.State) (runtime.State, []types.Event, runtime.Status, error){
			func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
				ran++
				return st, nil, runtime.DoneStatus, nil
			},
		}}, stores.NewMemoryCheckpoints())
		for _, err := range conv.Resume(types.WithPrincipal(context.Background(), op), token, Approve()) {
			if !errors.Is(err, types.ErrTokenMismatch) {
				t.Errorf("Resume on the wrong flow: got %v, want ErrTokenMismatch", err)
			}
		}
		if ran != 0 {
			t.Errorf("component executions after a mismatch: got %d, want 0", ran)
		}
		// The token still belongs to flow A and is untouched.
		if _, err := aStore.Consume(context.Background(), token, Approve()); err != nil {
			t.Errorf("Consume after the mismatch: %v", err)
		}
	})

	t.Run("suspension.token-reuse", func(t *testing.T) {
		cps := stores.NewMemoryCheckpoints()
		token := validationToken(t, cps, alice)
		var ran int
		var approver types.Principal
		conv := newValidationConv(t, &resumeRT{steps: []func(context.Context, runtime.State) (runtime.State, []types.Event, runtime.Status, error){
			doneStep(t, &ran, &approver),
		}}, cps)
		for _, err := range conv.Resume(types.WithPrincipal(context.Background(), op), token, Approve()) {
			if err != nil {
				t.Fatalf("first Resume: unexpected error %v", err)
			}
		}
		if ran != 1 {
			t.Fatalf("tool executions after the first resume: got %d, want 1", ran)
		}
		// The same approval delivered a second time finds the token consumed
		// and the tool is not executed again.
		for _, err := range conv.Resume(types.WithPrincipal(context.Background(), op), token, Approve()) {
			if !errors.Is(err, types.ErrTokenConsumed) {
				t.Errorf("second Resume: got %v, want ErrTokenConsumed", err)
			}
		}
		if ran != 1 {
			t.Errorf("tool executions after the reuse: got %d, want 1", ran)
		}
	})

	t.Run("identity.resume-inside-run-refused", func(t *testing.T) {
		cps := stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom))
		pending, err := json.Marshal(runtime.State{Turn: 1, HistoryVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		token, err := cps.Put(types.WithRunInfo(types.WithPrincipal(context.Background(), alice), types.RunInfo{RunID: "r1"}), stores.Checkpoint{
			SchemaVersion: stores.CurrentSchemaVersion,
			SessionID:     "s1",
			Backend:       "resume.test",
			Reason:        types.HumanApproval,
			Originator:    alice,
			Data:          pending,
		})
		if err != nil {
			t.Fatal(err)
		}
		conv := newValidationConv(t, &resumeRT{}, cps)
		runCtx := types.WithRunInfo(types.WithPrincipal(context.Background(), op), types.RunInfo{RunID: "r1"})
		for _, err := range conv.Resume(runCtx, token, Approve()) {
			if !errors.Is(err, types.ErrResumeInsideRun) {
				t.Errorf("Resume inside a run: got %v, want ErrResumeInsideRun", err)
			}
		}
		// The refusal ran before Consume, so the token is still pending.
		if _, _, err := cps.PendingInput(context.Background(), "r1"); err != nil {
			t.Errorf("PendingInput after the refusal: %v", err)
		}
	})

	t.Run("refused resume leaves the token consumable afterwards", func(t *testing.T) {
		cps := stores.NewMemoryCheckpoints()
		token := validationToken(t, cps, alice)
		conv := newValidationConv(t, &resumeRT{}, cps)
		runCtx := types.WithRunInfo(types.WithPrincipal(context.Background(), op), types.RunInfo{RunID: "r1"})
		for _, err := range conv.Resume(runCtx, token, Approve()) {
			if !errors.Is(err, types.ErrResumeInsideRun) {
				t.Errorf("Resume inside a run: got %v, want ErrResumeInsideRun", err)
			}
		}
		var ran int
		var approver types.Principal
		good := newValidationConv(t, &resumeRT{steps: []func(context.Context, runtime.State) (runtime.State, []types.Event, runtime.Status, error){
			doneStep(t, &ran, &approver),
		}}, cps)
		for _, err := range good.Resume(types.WithPrincipal(context.Background(), op), token, Approve()) {
			if err != nil {
				t.Fatalf("Resume after the refusal: unexpected error %v", err)
			}
		}
		if ran != 1 {
			t.Errorf("tool executions after the refusal: got %d, want 1", ran)
		}
	})

	t.Run("identity.approver-from-transport-only", func(t *testing.T) {
		cps := stores.NewMemoryCheckpoints()
		token := validationToken(t, cps, alice)
		var ran int
		var approver types.Principal
		conv := newValidationConv(t, &resumeRT{steps: []func(context.Context, runtime.State) (runtime.State, []types.Event, runtime.Status, error){
			doneStep(t, &ran, &approver),
		}}, cps)
		// The payload names mallory; the transport carries op. Only the
		// transport principal may become the approver.
		in := stores.ResumeInput{Verdict: stores.VerdictApprove, Approver: &mallory}
		for _, err := range conv.Resume(types.WithPrincipal(context.Background(), op), token, in) {
			if err != nil {
				t.Fatalf("Resume: unexpected error %v", err)
			}
		}
		if approver.Subject != op.Subject {
			t.Errorf("approver: got %+v, want the transport principal %+v", approver, op)
		}
	})

	t.Run("an approver supplied in the payload is ignored", func(t *testing.T) {
		_, in, err := validateResume(types.WithPrincipal(context.Background(), op),
			stores.ResumeInput{Verdict: stores.VerdictApprove, Approver: &mallory})
		if err != nil {
			t.Fatalf("validateResume: unexpected error %v", err)
		}
		if in.Approver == nil || in.Approver.Subject != op.Subject {
			t.Errorf("input approver: got %+v, want the transport principal %+v", in.Approver, op)
		}
		if _, ok := types.ApprovalFrom(context.Background()); ok {
			t.Error("validateResume must not write the approval into the caller's ctx")
		}
	})
}
