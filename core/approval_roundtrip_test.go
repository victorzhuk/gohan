package gohan

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// roundtripRT scripts the governed turn a real backend runs: the first step
// gates a pending SideEffect call and suspends with HumanApproval; after the
// resume it executes the tool once and finishes. The tool closure counts
// executions; no AgentRun.Tools reaches the runtime.
type roundtripRT struct {
	spec     types.ToolSpec
	executed int
	onStep1  func(*roundtripRT, runtime.State) (runtime.State, error)
}

func (r *roundtripRT) Name() string                         { return "roundtrip.test" }
func (r *roundtripRT) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }
func (r *roundtripRT) Start(_ context.Context, _ runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *roundtripRT) Step(_ context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if len(st.Pending) == 0 {
		next, err := r.onStep1(r, st)
		if err != nil {
			return st, nil, runtime.Continue, err
		}
		return next, nil, runtime.Continue, &types.SuspendError{Reason: types.HumanApproval}
	}
	r.executed++
	return runtime.State{Turn: st.Turn + 1, HistoryVersion: st.HistoryVersion + 1}, nil, runtime.DoneStatus, nil
}

type roundtripPolicy struct {
	quorum   int
	resolved int
}

func (p *roundtripPolicy) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	p.resolved++
	q := p.quorum
	if q == 0 {
		q = 1
	}
	return permission.ApprovalPolicy{Scope: "approve:tool", Quorum: q}, nil
}

type roundtripHarness struct {
	cps   *stores.MemoryCheckpoints
	log   *stores.MemorySessionLog
	runs  *stores.MemoryRuns
	rt    *roundtripRT
	pol   *roundtripPolicy
	specs map[string]types.ToolSpec
}

func newRoundtripHarness(t *testing.T, rt *roundtripRT, specs map[string]types.ToolSpec) *roundtripHarness {
	t.Helper()
	h := &roundtripHarness{
		cps:   stores.NewMemoryCheckpoints(),
		log:   stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
		runs:  stores.NewMemoryRuns(),
		rt:    rt,
		pol:   &roundtripPolicy{},
		specs: specs,
	}
	if _, err := h.log.Append(types.WithPrincipal(context.Background(), authOwner), "s1", 0, types.Message{
		Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "book it"}},
	}); err != nil {
		t.Fatal(err)
	}
	return h
}

// conv builds a conversation the public way: no AgentRun.Tools, the spec
// lookup and the policy source ride the conversation wiring.
func (h *roundtripHarness) conv(t *testing.T) Conversation {
	t.Helper()
	stack := &Stack{stores: stores.Stores{SessionLog: h.log}}
	conv, err := NewConversation(stack, "flights", h.rt,
		WithConversationRuns(h.runs),
		WithConversationEventLog(stores.NewMemoryEventLog()),
		WithConversationCheckpoints(h.cps),
		WithConversationCredentialSource(&authCreds{}),
		WithConversationApprovalPolicy(h.pol),
		WithConversationToolSpecs(func(name string) (types.ToolSpec, bool) {
			s, ok := h.specs[name]
			return s, ok
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return conv
}

func (h *roundtripHarness) send(t *testing.T) types.ResumeToken {
	t.Helper()
	var token types.ResumeToken
	for ev, err := range h.conv(t).Send(types.WithPrincipal(context.Background(), authOwner), "s1", types.Message{
		Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "book it"}},
	}) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	if token == "" {
		t.Fatal("Send: no Suspended token")
	}
	return token
}

func (h *roundtripHarness) envelope(t *testing.T, token types.ResumeToken) checkpointEnvelope {
	t.Helper()
	cp, err := h.cps.Peek(types.WithPrincipal(context.Background(), authOwner), token)
	if err != nil {
		t.Fatal(err)
	}
	env, _, err := decodeCheckpoint(cp, h.rt, "flights")
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestApprovalRoundtrip(t *testing.T) {
	ctx := types.WithPrincipal(context.Background(), authOp)

	t.Run("runtime.batch-ask-after-allowed", func(t *testing.T) {
		specs := map[string]types.ToolSpec{
			"confirm": {Name: "confirm", Risk: types.RiskHigh, Effect: types.SideEffect},
		}
		rt := &roundtripRT{spec: specs["confirm"], onStep1: func(r *roundtripRT, st runtime.State) (runtime.State, error) {
			st.Pending = []types.ToolUse{{ID: "c2", Name: "confirm", Args: json.RawMessage(`{"n":1}`)}}
			return st, nil
		}}
		h := newRoundtripHarness(t, rt, specs)
		token := h.send(t)
		if rt.executed != 0 {
			t.Fatalf("side effect executed before approval %d times", rt.executed)
		}
		env := h.envelope(t, token)
		if len(env.Approvals) != 1 || env.Approvals[0].Call.ID != "c2" {
			t.Fatalf("approvals: %+v, want one for c2", env.Approvals)
		}
		if env.Approvals[0].Reversible {
			t.Error("side effect recorded reversible")
		}
		if env.Approvals[0].Risk != types.RiskHigh {
			t.Errorf("risk: %v, want high", env.Approvals[0].Risk)
		}
	})

	t.Run("permission.rich-request", func(t *testing.T) {
		specs := map[string]types.ToolSpec{
			"confirm": {Name: "confirm", Risk: types.RiskHigh, Effect: types.SideEffect},
			"note":    {Name: "note", Risk: types.RiskLow, Effect: types.ReadOnly},
		}
		rt := &roundtripRT{spec: specs["confirm"], onStep1: func(r *roundtripRT, st runtime.State) (runtime.State, error) {
			st.Pending = []types.ToolUse{
				{ID: "c1", Name: "confirm", Args: json.RawMessage(`{"n":1}`)},
				{ID: "c2", Name: "note", Args: json.RawMessage(`{"t":"x"}`)},
			}
			return st, nil
		}}
		h := newRoundtripHarness(t, rt, specs)
		token := h.send(t)
		env := h.envelope(t, token)
		// Only the active ask, the head of the pending queue, is a
		// persisted approval; the queued tail asks when it becomes the
		// head.
		if len(env.Approvals) != 1 || env.Approvals[0].Call.ID != "c1" {
			t.Fatalf("approvals: %+v, want one for the active ask c1", env.Approvals)
		}
		if h.pol.resolved != 1 {
			t.Errorf("policy resolutions: %d, want 1", h.pol.resolved)
		}
		if env.Approvals[0].Reversible {
			t.Errorf("reversible: true, want false for the side effect")
		}
		if env.Approvals[0].Risk != types.RiskHigh {
			t.Errorf("risk: %v, want high", env.Approvals[0].Risk)
		}
		if env.Approvals[0].Eligible.Quorum != 1 {
			t.Errorf("quorum: %d, want 1", env.Approvals[0].Eligible.Quorum)
		}
		if env.Approvals[0].Fingerprint == "" {
			t.Error("fingerprint empty")
		}
	})

	t.Run("suspension.approve-on-another-pod", func(t *testing.T) {
		specs := map[string]types.ToolSpec{
			"confirm": {Name: "confirm", Risk: types.RiskHigh, Effect: types.SideEffect},
		}
		rt := &roundtripRT{spec: specs["confirm"], onStep1: func(r *roundtripRT, st runtime.State) (runtime.State, error) {
			st.Pending = []types.ToolUse{{ID: "c1", Name: "confirm", Args: json.RawMessage(`{"n":1}`)}}
			return st, nil
		}}
		h := newRoundtripHarness(t, rt, specs)
		token := h.send(t)
		var done bool
		for ev, err := range h.conv(t).Resume(ctx, token, Approve()) {
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			if _, ok := ev.(types.Done); ok {
				done = true
			}
		}
		if !done {
			t.Error("Resume: no Done event")
		}
		if rt.executed != 1 {
			t.Errorf("tool executions: %d, want 1", rt.executed)
		}
	})
}
