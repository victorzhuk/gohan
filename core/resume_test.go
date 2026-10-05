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

// resumeRT is a scripted stepper: one function per Step call, Done when the
// script runs out.
type resumeRT struct {
	steps []func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error)
	i     int
}

func (r *resumeRT) Name() string                         { return "resume.test" }
func (r *resumeRT) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }
func (r *resumeRT) Start(_ context.Context, _ runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *resumeRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if r.i >= len(r.steps) {
		return st, nil, runtime.DoneStatus, nil
	}
	fn := r.steps[r.i]
	r.i++
	return fn(ctx, st)
}

// resumeCreds records the credential issues and answers with a fresh token.
type resumeCreds struct {
	calls int
	subj  string
}

func (f *resumeCreds) Credentials(_ context.Context, p types.Principal) (types.Credential, error) {
	f.calls++
	f.subj = p.Subject
	return types.Credential{Token: "fresh"}, nil
}

func TestApprovalResume(t *testing.T) {
	ctx := context.Background()

	t.Run("suspension.approve-on-another-pod", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		var order []string
		creds := &resumeCreds{}
		h.rt.steps = []func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error){
			func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
				order = append(order, "tool")
				p, ok := PrincipalFrom(ctx)
				if !ok || p.Subject != authOwner.Subject || p.Tenant != authOwner.Tenant {
					t.Errorf("tool principal: got %+v ok=%v, want the originator %+v", p, ok, authOwner)
				}
				ap, ok := types.ApprovalFrom(ctx)
				if !ok || ap.Approver.Subject != authOp.Subject || ap.Approver.Tenant != authOp.Tenant {
					t.Errorf("ApprovalFrom: got %+v ok=%v, want approver op", ap, ok)
				}
				cred, ok := CredentialFrom(ctx)
				if !ok || cred.Token != "fresh" {
					t.Errorf("CredentialFrom: got %+v ok=%v, want the re-issued credential", cred, ok)
				}
				return st, nil, runtime.DoneStatus, nil
			},
		}
		// CredentialSource must run before the tool executes.
		credsWrap := &credsOrder{src: creds, order: &order}
		conv := h.conv(t, &authPolicySource{}, credsWrap)
		var done bool
		for ev, err := range conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()) {
			if err != nil {
				t.Fatalf("Resume: unexpected error %v", err)
			}
			if _, isDone := ev.(types.Done); isDone {
				done = true
			}
		}
		if !done {
			t.Error("Resume: no Done event")
		}
		if len(order) != 2 || order[0] != "credential" || order[1] != "tool" {
			t.Errorf("order: got %v, want [credential tool]", order)
		}
		if creds.subj != authOwner.Subject {
			t.Errorf("credential subject: got %q, want the originator %q", creds.subj, authOwner.Subject)
		}
		if h.rt.i != 1 {
			t.Errorf("tool executions: got %d, want 1", h.rt.i)
		}
		if _, err := h.cps.Consume(ctx, token, Approve()); !errors.Is(err, types.ErrTokenConsumed) {
			t.Errorf("second consume: got %v, want ErrTokenConsumed", err)
		}
	})

	t.Run("suspension.reject-and-edit", func(t *testing.T) {
		h := newAuthHarness(t)
		state := func(pending []types.ToolUse) runtime.State {
			return runtime.State{Turn: 1, HistoryVersion: 1, Pending: pending}
		}
		rejectToken := h.seed(t, "run-reject", types.HumanApproval, state([]types.ToolUse{{ID: "call-1", Name: "create_booking", Args: json.RawMessage(`{}`)}}), []checkpointApproval{{
			Call:     types.ToolUse{ID: "call-1", Name: "create_booking", Args: json.RawMessage(`{}`)},
			Risk:     types.RiskHigh,
			Eligible: defaultEligibility(1),
		}})
		editToken := h.seed(t, "run-edit", types.HumanApproval, state([]types.ToolUse{{ID: "call-1", Name: "create_booking", Args: json.RawMessage(`{}`)}}), []checkpointApproval{{
			Call:     types.ToolUse{ID: "call-1", Name: "create_booking", Args: json.RawMessage(`{}`)},
			Risk:     types.RiskHigh,
			Eligible: defaultEligibility(1),
		}})

		// Reject: the model sees the marker as a failed tool result and no
		// tool executes.
		if errs := h.errors(t, h.conv(t, &authPolicySource{}, &authCreds{}).Resume(types.WithPrincipal(ctx, authOp), rejectToken, Reject("no"))); len(errs) != 0 {
			t.Fatalf("Resume reject: unexpected error %v", errs)
		}
		hst, err := h.log.Load(types.WithPrincipal(ctx, authOp), "s1")
		if err != nil {
			t.Fatal(err)
		}
		last := hst.Messages[len(hst.Messages)-1]
		res, ok := last.Blocks[len(last.Blocks)-1].(types.ToolResult)
		if !ok || res.Outcome != types.Failed || res.Error == nil ||
			res.Error.Message != runtime.NotExecutedPrefix+"rejected by "+authOp.Subject {
			t.Fatalf("reject result: got %+v, want the prefixed rejection marker", last.Blocks)
		}
		if res.ID != "call-1" {
			t.Errorf("reject result id: got %q, want call-1", res.ID)
		}

		// EditArgs: the pending call replays with the edited arguments.
		h.rt.steps = []func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error){
			func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
				if len(st.Pending) != 1 || string(st.Pending[0].Args) != `{"nights":3}` {
					t.Errorf("edited args: got %+v, want the pending call with the edited arguments", st.Pending)
				}
				return st, nil, runtime.DoneStatus, nil
			},
		}
		if errs := h.errors(t, h.conv(t, &authPolicySource{}, &authCreds{}).Resume(types.WithPrincipal(ctx, authOp), editToken, EditArgs(json.RawMessage(`{"nights":3}`)))); len(errs) != 0 {
			t.Fatalf("Resume edit: unexpected error %v", errs)
		}
		if h.rt.i != 1 {
			t.Errorf("edit step executions: got %d, want 1", h.rt.i)
		}
	})

	t.Run("identity.credentials-on-resume", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.seed(t, "run-wake", types.Preempted, runtime.State{Turn: 1, HistoryVersion: 1}, nil)
		creds := &resumeCreds{}
		h.rt.steps = []func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error){
			func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
				if cred, ok := CredentialFrom(ctx); !ok || cred.Token != "fresh" {
					t.Errorf("CredentialFrom: got %+v ok=%v, want the re-issued credential", cred, ok)
				}
				return st, nil, runtime.DoneStatus, nil
			},
		}
		conv := h.conv(t, &authPolicySource{}, creds)
		if errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, Continue())); len(errs) != 0 {
			t.Fatalf("Resume: unexpected error %v", errs)
		}
		if creds.calls != 1 || creds.subj != authOwner.Subject {
			t.Errorf("credential source: got %d calls for %q, want 1 call for the originator", creds.calls, creds.subj)
		}
	})

	t.Run("constructors match the store verdicts", func(t *testing.T) {
		cps := stores.NewMemoryCheckpoints()
		token, err := cps.Put(types.WithPrincipal(ctx, authOwner), stores.Checkpoint{SessionID: "s1"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cps.Consume(ctx, token, Reject("later")); err != nil {
			t.Fatalf("Consume(Reject): %v", err)
		}
		_, in, err := cps.PendingInput(ctx, "")
		if err == nil {
			t.Fatalf("PendingInput without a run id: got input %+v, want an error", in)
		}
	})

	t.Run("resume without checkpoints is refused", func(t *testing.T) {
		conv, err := NewConversation(nil, "flights", &resumeRT{},
			WithConversationRuns(stores.NewMemoryRuns()),
			WithConversationEventLog(stores.NewMemoryEventLog()),
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, err := range conv.Resume(types.WithPrincipal(ctx, authOp), "t", Approve()) {
			if !errors.Is(err, errResumeCheckpointsRequired) {
				t.Fatalf("Resume: got %v, want errResumeCheckpointsRequired", err)
			}
		}
	})
}

// credsOrder records the credential issue in the shared order trace.
type credsOrder struct {
	src   types.CredentialSource
	order *[]string
}

func (w *credsOrder) Credentials(ctx context.Context, p types.Principal) (types.Credential, error) {
	*w.order = append(*w.order, "credential")
	return w.src.Credentials(ctx, p)
}
