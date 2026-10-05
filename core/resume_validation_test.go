package gohan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestResumeValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("suspension.mismatch", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		var ran int
		h.rt.steps = []func(context.Context, runtime.State) (runtime.State, []types.Event, runtime.Status, error){
			func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
				ran++
				return st, nil, runtime.DoneStatus, nil
			},
		}
		stack := &Stack{stores: stores.Stores{SessionLog: h.log}}
		conv, err := NewConversation(stack, "chat", h.rt,
			WithConversationRuns(h.runs),
			WithConversationEventLog(stores.NewMemoryEventLog()),
			WithConversationCheckpoints(h.cps),
			WithConversationApprovalPolicy(&authPolicySource{}),
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, err := range conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()) {
			if !errors.Is(err, types.ErrTokenMismatch) {
				t.Errorf("Resume on the wrong flow: got %v, want ErrTokenMismatch", err)
			}
		}
		if ran != 0 {
			t.Errorf("component executions after a mismatch: got %d, want 0", ran)
		}
		if _, err := h.cps.Peek(ctx, token); err != nil {
			t.Errorf("token after the mismatch: %v", err)
		}
	})

	t.Run("suspension.token-reuse", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		conv := h.conv(t, &authPolicySource{}, &authCreds{})
		for _, err := range conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()) {
			if err != nil {
				t.Fatalf("first Resume: unexpected error %v", err)
			}
		}
		if h.rt.i != 1 {
			t.Fatalf("tool executions after the first resume: got %d, want 1", h.rt.i)
		}
		for _, err := range conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()) {
			if !errors.Is(err, types.ErrTokenConsumed) {
				t.Errorf("second Resume: got %v, want ErrTokenConsumed", err)
			}
		}
		if h.rt.i != 1 {
			t.Errorf("tool executions after the reuse: got %d, want 1", h.rt.i)
		}
	})

	t.Run("identity.resume-inside-run-refused", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		conv := h.conv(t, &authPolicySource{}, &authCreds{})
		runCtx := types.WithRunInfo(types.WithPrincipal(ctx, authOp), types.RunInfo{RunID: "r1"})
		for _, err := range conv.Resume(runCtx, token, Approve()) {
			if !errors.Is(err, types.ErrResumeInsideRun) {
				t.Errorf("Resume inside a run: got %v, want ErrResumeInsideRun", err)
			}
		}
		if _, err := h.cps.Peek(ctx, token); err != nil {
			t.Errorf("refusal consumed the token: %v", err)
		}
	})

	t.Run("refused resume leaves the token consumable afterwards", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		conv := h.conv(t, &authPolicySource{}, &authCreds{})
		runCtx := types.WithRunInfo(types.WithPrincipal(ctx, authOp), types.RunInfo{RunID: "r1"})
		for _, err := range conv.Resume(runCtx, token, Approve()) {
			if !errors.Is(err, types.ErrResumeInsideRun) {
				t.Errorf("Resume inside a run: got %v, want ErrResumeInsideRun", err)
			}
		}
		errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()))
		if len(errs) != 0 {
			t.Fatalf("Resume after the refusal: got %v", errs)
		}
		if h.rt.i != 1 {
			t.Errorf("tool executions after the refusal: got %d, want 1", h.rt.i)
		}
	})

	t.Run("identity.approver-from-transport-only", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		conv := h.conv(t, &authPolicySource{}, &authCreds{})
		in := Approve()
		in.Approver = &authForeign
		if errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, in)); len(errs) != 0 {
			t.Fatalf("Resume with a forged approver: got %v", errs)
		}
		hst, err := h.log.Load(types.WithPrincipal(ctx, authOp), "s1")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, msg := range hst.Messages {
			raw, ok := msg.Meta[ApprovalReceiptKey]
			if !ok {
				continue
			}
			rc, err := decodeReceipt(raw)
			if err != nil {
				t.Fatal(err)
			}
			found = true
			if len(rc.Approvers) != 1 || rc.Approvers[0].Subject != authOp.Subject {
				t.Errorf("receipt approvers: %+v, want the transport principal", rc.Approvers)
			}
		}
		if !found {
			t.Error("no approval receipt recorded")
		}
	})

	t.Run("identity.no-principal", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		conv := h.conv(t, &authPolicySource{}, &authCreds{})
		for _, err := range conv.Resume(ctx, token, Approve()) {
			if !errors.Is(err, types.ErrNoPrincipal) {
				t.Errorf("Resume without a principal: got %v, want ErrNoPrincipal", err)
			}
		}
		if _, err := h.cps.Peek(ctx, token); err != nil {
			t.Errorf("refusal consumed the token: %v", err)
		}
	})

	t.Run("human handoff is not resumable", func(t *testing.T) {
		h := newAuthHarness(t)
		env := checkpointEnvelope{
			Run:        types.RunInfo{Flow: "flights", SessionID: "s1", RunID: "run-2"},
			Generation: 1,
			State:      runtime.State{Turn: 1, HistoryVersion: 1},
		}
		data, err := encodeCheckpoint(env)
		if err != nil {
			t.Fatal(err)
		}
		token, err := h.cps.Put(types.WithPrincipal(context.Background(), authOwner), stores.Checkpoint{
			RunID:         "run-2",
			SchemaVersion: stores.CurrentSchemaVersion,
			SessionID:     "s1",
			Flow:          "flights",
			Backend:       h.rt.Name(),
			Reason:        types.HumanHandoff,
			Originator:    authOwner,
			Data:          data,
		})
		if err != nil {
			t.Fatal(err)
		}
		conv := h.conv(t, &authPolicySource{}, &authCreds{})
		for _, err := range conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()) {
			if !errors.Is(err, types.ErrNotSuspendable) {
				t.Errorf("human handoff resume: got %v, want ErrNotSuspendable", err)
			}
		}
		if _, err := h.cps.Peek(ctx, token); err != nil {
			t.Errorf("refusal consumed the token: %v", err)
		}
	})

	t.Run("legacy approval checkpoint is refused", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		cp, err := h.cps.Peek(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		cp.Data = []byte(`{"Turn":1,"HistoryVersion":1}`)
		env, _, err := decodeCheckpoint(cp, h.rt, "flights")
		if err == nil {
			t.Fatalf("legacy HumanApproval checkpoint decoded: %+v", env)
		}
		if !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Errorf("legacy HumanApproval: got %v, want ErrCheckpointIncompatible", err)
		}
		_ = time.Second
	})
}
