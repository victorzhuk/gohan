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
	alice := types.Principal{Tenant: "acme", Subject: "alice"}
	op := types.Principal{Tenant: "acme", Subject: "op"}

	t.Run("suspension.approve-on-another-pod", func(t *testing.T) {
		cps := stores.NewMemoryCheckpoints()
		pending, err := json.Marshal(runtime.State{
			Turn:           1,
			HistoryVersion: 1,
			Pending:        []types.ToolUse{{ID: "call-1", Name: "create_booking"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		token, err := cps.Put(types.WithPrincipal(context.Background(), alice), stores.Checkpoint{
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
		var order []string
		creds := &resumeCreds{}
		rt := &resumeRT{steps: []func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error){
			func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
				order = append(order, "tool")
				p, ok := PrincipalFrom(ctx)
				if !ok || p.Subject != alice.Subject || p.Tenant != alice.Tenant {
					t.Errorf("tool principal: got %+v ok=%v, want the originator %+v", p, ok, alice)
				}
				ap, ok := types.ApprovalFrom(ctx)
				if !ok || ap.Approver.Subject != op.Subject || ap.Approver.Tenant != op.Tenant {
					t.Errorf("ApprovalFrom: got %+v ok=%v, want approver op", ap, ok)
				}
				cred, ok := CredentialFrom(ctx)
				if !ok || cred.Token != "fresh" {
					t.Errorf("CredentialFrom: got %+v ok=%v, want the re-issued credential", cred, ok)
				}
				return st, nil, runtime.DoneStatus, nil
			},
		}}
		// CredentialSource must run before the tool executes.
		credsWrap := &credsOrder{src: creds, order: &order}
		conv, err := NewConversation(nil, "flights", rt,
			WithConversationRuns(stores.NewMemoryRuns()),
			WithConversationEventLog(stores.NewMemoryEventLog()),
			WithConversationCheckpoints(cps),
			WithConversationCredentialSource(credsWrap),
		)
		if err != nil {
			t.Fatal(err)
		}
		var done bool
		for ev, err := range conv.Resume(types.WithPrincipal(context.Background(), op), token, Approve()) {
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
		if creds.subj != alice.Subject {
			t.Errorf("credential subject: got %q, want the originator %q", creds.subj, alice.Subject)
		}
		if rt.i != 1 {
			t.Errorf("tool executions: got %d, want 1", rt.i)
		}
		if _, err := cps.Consume(context.Background(), token, Approve()); !errors.Is(err, types.ErrTokenConsumed) {
			t.Errorf("second consume: got %v, want ErrTokenConsumed", err)
		}
	})

	t.Run("suspension.reject-and-edit", func(t *testing.T) {
		cps := stores.NewMemoryCheckpoints()
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(func(ctx context.Context) (types.Principal, bool) { return types.PrincipalFrom(ctx) }))
		if _, err := log.Append(types.WithPrincipal(context.Background(), alice), "s1", 0, types.Message{
			Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "book it"}},
		}); err != nil {
			t.Fatal(err)
		}
		put := func(data []byte) types.ResumeToken {
			t.Helper()
			token, err := cps.Put(types.WithPrincipal(context.Background(), alice), stores.Checkpoint{
				SchemaVersion: stores.CurrentSchemaVersion,
				SessionID:     "s1",
				Backend:       "resume.test",
				Originator:    alice,
				Data:          data,
			})
			if err != nil {
				t.Fatal(err)
			}
			return token
		}
		state := func(pending []types.ToolUse) []byte {
			t.Helper()
			raw, err := json.Marshal(runtime.State{Turn: 1, HistoryVersion: 1, Pending: pending})
			if err != nil {
				t.Fatal(err)
			}
			return raw
		}
		newConv := func(rt *resumeRT) Conversation {
			t.Helper()
			stack := &Stack{stores: stores.Stores{SessionLog: log}}
			conv, err := NewConversation(stack, "flights", rt,
				WithConversationRuns(stores.NewMemoryRuns()),
				WithConversationEventLog(stores.NewMemoryEventLog()),
				WithConversationCheckpoints(cps),
			)
			if err != nil {
				t.Fatal(err)
			}
			return conv
		}

		// Reject: the model sees the reason as a failed tool result and no
		// tool executes.
		for _, err := range newConv(&resumeRT{}).Resume(types.WithPrincipal(context.Background(), op), put(state([]types.ToolUse{{ID: "call-1", Name: "create_booking"}})), Reject("no")) {
			if err != nil {
				t.Fatalf("Resume reject: unexpected error %v", err)
			}
		}
		h, err := log.Load(types.WithPrincipal(context.Background(), alice), "s1")
		if err != nil {
			t.Fatal(err)
		}
		last := h.Messages[len(h.Messages)-1]
		res, ok := last.Blocks[len(last.Blocks)-1].(types.ToolResult)
		if !ok || res.Outcome != types.Failed || res.Error == nil || res.Error.Message != "no" {
			t.Fatalf("reject result: got %+v, want a failed tool result with reason \"no\"", last.Blocks)
		}
		if res.ID != "call-1" {
			t.Errorf("reject result id: got %q, want call-1", res.ID)
		}

		// EditArgs: the pending call replays with the edited arguments.
		rt := &resumeRT{steps: []func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error){
			func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
				if len(st.Pending) != 1 || string(st.Pending[0].Args) != `{"nights":3}` {
					t.Errorf("edited args: got %+v, want the pending call with the edited arguments", st.Pending)
				}
				return st, nil, runtime.DoneStatus, nil
			},
		}}
		for _, err := range newConv(rt).Resume(types.WithPrincipal(context.Background(), op), put(state([]types.ToolUse{{ID: "call-1", Name: "create_booking"}})), EditArgs(json.RawMessage(`{"nights":3}`))) {
			if err != nil {
				t.Fatalf("Resume edit: unexpected error %v", err)
			}
		}
		if rt.i != 1 {
			t.Errorf("edit step executions: got %d, want 1", rt.i)
		}
	})

	t.Run("identity.credentials-on-resume", func(t *testing.T) {
		cps := stores.NewMemoryCheckpoints()
		token, err := cps.Put(types.WithPrincipal(context.Background(), alice), stores.Checkpoint{
			SchemaVersion: stores.CurrentSchemaVersion,
			SessionID:     "s1",
			Backend:       "resume.test",
			Originator:    alice,
		})
		if err != nil {
			t.Fatal(err)
		}
		creds := &resumeCreds{}
		rt := &resumeRT{steps: []func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error){
			func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
				if cred, ok := CredentialFrom(ctx); !ok || cred.Token != "fresh" {
					t.Errorf("CredentialFrom: got %+v ok=%v, want the re-issued credential", cred, ok)
				}
				return st, nil, runtime.DoneStatus, nil
			},
		}}
		conv, err := NewConversation(nil, "flights", rt,
			WithConversationRuns(stores.NewMemoryRuns()),
			WithConversationEventLog(stores.NewMemoryEventLog()),
			WithConversationCheckpoints(cps),
			WithConversationCredentialSource(creds),
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, err := range conv.Resume(types.WithPrincipal(context.Background(), op), token, Continue()) {
			if err != nil {
				t.Fatalf("Resume: unexpected error %v", err)
			}
		}
		if creds.calls != 1 || creds.subj != alice.Subject {
			t.Errorf("credential source: got %d calls for %q, want 1 call for the originator", creds.calls, creds.subj)
		}
	})

	t.Run("constructors match the store verdicts", func(t *testing.T) {
		cps := stores.NewMemoryCheckpoints()
		token, err := cps.Put(types.WithPrincipal(context.Background(), alice), stores.Checkpoint{SessionID: "s1"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cps.Consume(context.Background(), token, Reject("later")); err != nil {
			t.Fatalf("Consume(Reject): %v", err)
		}
		_, in, err := cps.PendingInput(context.Background(), "")
		if err == nil {
			t.Fatalf("PendingInput without a run id: got input %+v, want an error", in)
		}
	})

	t.Run("approval from ctx feeds ApprovalFrom", func(t *testing.T) {
		fn := func(ctx context.Context, _ string) (string, error) {
			if ap, ok := types.ApprovalFrom(ctx); ok && ap.Approver.Subject == "op" {
				return "ok", nil
			}
			return "plain", nil
		}
		f := FlowFunc("flights", fn)
		if out, err := f.Invoke(context.Background(), "in"); err != nil || out != "plain" {
			t.Fatalf("Invoke: got %q, %v; want \"plain\", nil", out, err)
		}
		// A plain function flow cannot suspend, so FlowFunc.Resume refuses
		// (flow.not-suspendable); the guards and the approval travel through
		// Conversation.Resume, which owns the resume entry point.
		if _, err := f.Resume(context.Background(), "t", Approve()); !errors.Is(err, types.ErrNotSuspendable) {
			t.Errorf("FlowFunc Resume: got %v, want ErrNotSuspendable", err)
		}
		cps := stores.NewMemoryCheckpoints()
		pending, err := json.Marshal(runtime.State{Turn: 1, HistoryVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		token, err := cps.Put(types.WithPrincipal(context.Background(), alice), stores.Checkpoint{
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
		rt := &resumeRT{steps: []func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error){
			func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
				ap, ok := types.ApprovalFrom(ctx)
				if !ok || ap.Approver.Subject != op.Subject || ap.Approver.Tenant != op.Tenant {
					t.Errorf("ApprovalFrom: got %+v ok=%v, want approver op", ap, ok)
				}
				return st, nil, runtime.DoneStatus, nil
			},
		}}
		conv, err := NewConversation(nil, "flights", rt,
			WithConversationRuns(stores.NewMemoryRuns()),
			WithConversationEventLog(stores.NewMemoryEventLog()),
			WithConversationCheckpoints(cps),
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, err := range conv.Resume(context.Background(), token, Approve()) {
			if !errors.Is(err, types.ErrNoPrincipal) {
				t.Errorf("Resume without a principal: got %v, want ErrNoPrincipal", err)
			}
		}
		runCtx := types.WithRunInfo(context.Background(), types.RunInfo{RunID: "r1"})
		for _, err := range conv.Resume(types.WithPrincipal(runCtx, op), token, Approve()) {
			if !errors.Is(err, types.ErrResumeInsideRun) {
				t.Errorf("Resume inside a run: got %v, want ErrResumeInsideRun", err)
			}
		}
		for _, err := range conv.Resume(types.WithPrincipal(context.Background(), op), token, Approve()) {
			if err != nil {
				t.Errorf("Resume: unexpected error %v", err)
			}
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
		for _, err := range conv.Resume(types.WithPrincipal(context.Background(), op), "t", Approve()) {
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
