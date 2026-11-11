package gohan

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// evalSeamHook stands in for the online evaluation engine: it counts the
// Done-seam invocations, keeps the last completion snapshot, and models
// admission by handing the snapshot to a buffered slot it returns from
// without waiting for any consumer. When wired with an error, every
// admission fails.
type evalSeamHook struct {
	mu    sync.Mutex
	calls int
	slots chan RunCompletion
	err   error
}

func newEvalSeamHook() *evalSeamHook {
	return &evalSeamHook{slots: make(chan RunCompletion, 16)}
}

func (h *evalSeamHook) Evaluate(_ context.Context, c RunCompletion) error {
	h.mu.Lock()
	h.calls++
	h.mu.Unlock()
	select {
	case h.slots <- c:
	default:
	}
	return h.err
}

func (h *evalSeamHook) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

func evalSeamStack(t *testing.T, hook *evalSeamHook, withEvaluator bool) *Stack {
	t.Helper()
	stack, err := Build(
		AllowAnonymous(),
		WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom)),
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
		}),
		WithModels(gohantest.NewScriptedModel(types.ModelProfile{Name: "primary"}, gohantest.Text("acknowledged"))),
		WithNativeAgent(NativeSpec{
			Request: FlowRequest{Name: "chat"},
			Profile: "primary",
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
		func(c *config) error {
			if withEvaluator {
				return WithRunEvaluator(hook)(c)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return stack
}

func evalSeamConv(t *testing.T, stack *Stack) Conversation {
	t.Helper()
	conv, err := NewNativeConversation(stack, "chat",
		WithConversationRuns(stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
	)
	if err != nil {
		t.Fatalf("NewNativeConversation: %v", err)
	}
	return conv
}

// evalSeamSend drives one real conversation turn to its terminal Done and
// returns the emitted Done payload.
func evalSeamSend(t *testing.T, conv Conversation, sessionID string) types.Done {
	t.Helper()
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
	var done types.Done
	for ev, err := range conv.Send(ctx, sessionID, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "remember the booking"}}}) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if d, ok := ev.(types.Done); ok {
			done = d
		}
	}
	if done.Reason != types.StopCompleted {
		t.Fatalf("terminal Done reason %v, want completed", done.Reason)
	}
	return done
}

func TestLifecycleEvaluation(t *testing.T) {
	t.Run("seam.evaluate-once-per-terminal-completion", func(t *testing.T) {
		hook := newEvalSeamHook()
		done := evalSeamSend(t, evalSeamConv(t, evalSeamStack(t, hook, true)), "s-eval")

		if got := hook.count(); got != 1 {
			t.Fatalf("Evaluate calls = %d, want 1", got)
		}
		snap := <-hook.slots
		if snap.Info.SessionID != "s-eval" {
			t.Errorf("snapshot session = %q, want s-eval", snap.Info.SessionID)
		}
		if snap.Info.RunID == "" {
			t.Error("snapshot RunID empty")
		}
		if snap.Info.Mode != types.Primary {
			t.Errorf("snapshot mode = %v, want primary", snap.Info.Mode)
		}
		if !reflect.DeepEqual(snap.Done, done) {
			t.Errorf("snapshot Done = %+v, want the emitted terminal Done %+v", snap.Done, done)
		}
		if snap.Version <= 0 {
			t.Errorf("snapshot Version = %d, want the terminal history version", snap.Version)
		}
		hctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
		hist, err := snap.History(hctx)
		if err != nil {
			t.Fatalf("snapshot History: %v", err)
		}
		if len(hist.Messages) < 2 {
			t.Errorf("history messages = %d, want the user turn and the reply", len(hist.Messages))
		}
	})

	t.Run("seam.admission-nonblocking", func(t *testing.T) {
		hook := newEvalSeamHook()
		// Admission hands the completion to the engine and returns; the
		// admitted job is consumed only after the turn finished. The Done
		// must already be delivered while the job still sits unconsumed in
		// the engine slot.
		hook.slots = make(chan RunCompletion, 1)
		done := evalSeamSend(t, evalSeamConv(t, evalSeamStack(t, hook, true)), "s-nb")

		select {
		case snap := <-hook.slots:
			if !reflect.DeepEqual(snap.Done, done) {
				t.Errorf("admitted Done = %+v, want %+v", snap.Done, done)
			}
		default:
			t.Error("admission never handed off the completion")
		}
		if got := hook.count(); got != 1 {
			t.Errorf("Evaluate calls = %d, want 1", got)
		}
	})

	t.Run("seam.evaluator-error-never-fails-done", func(t *testing.T) {
		hook := newEvalSeamHook()
		hook.err = errors.New("gohan/evals: admission exploded")
		conv := evalSeamConv(t, evalSeamStack(t, hook, true))
		ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
		var done bool
		for ev, err := range conv.Send(ctx, "s-err", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}) {
			if err != nil {
				t.Fatalf("Send: %v, want the admission error absorbed by the seam", err)
			}
			if _, ok := ev.(types.Done); ok {
				done = true
			}
		}
		if !done {
			t.Fatal("Send never reached Done after an evaluator error")
		}
		if got := hook.count(); got != 1 {
			t.Errorf("Evaluate calls = %d, want 1", got)
		}
	})

	t.Run("seam.no-evaluator-behaves-unchanged", func(t *testing.T) {
		hook := newEvalSeamHook()
		done := evalSeamSend(t, evalSeamConv(t, evalSeamStack(t, hook, false)), "s-none")
		if done.Reason != types.StopCompleted {
			t.Fatalf("Done reason %v, want completed", done.Reason)
		}
		if got := hook.count(); got != 0 {
			t.Errorf("Evaluate calls = %d, want 0 without WithRunEvaluator", got)
		}
	})
}
