package gohan

import (
	"context"
	"encoding/json/jsontext"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// quorumPolicy resolves every ask to one shared policy, so a test can pin
// the scope and quorum the persisted approval carries.
type quorumPolicy struct {
	scope  string
	quorum int
}

func (p quorumPolicy) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	return permission.ApprovalPolicy{Scope: p.scope, Quorum: p.quorum}, nil
}

func persistedApproval(t *testing.T, stack *Stack, token types.ResumeToken) (checkpointEnvelope, runtime.State) {
	t.Helper()
	peeker, ok := stack.stores.Checkpoints.(stores.CheckpointResumer)
	if !ok {
		t.Fatal("checkpoints store does not support Peek")
	}
	cp, err := peeker.Peek(context.Background(), token)
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	env, st, err := decodeCheckpoint(cp, runtime.NewNative(), "chat")
	if err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}
	return env, st
}

// normElig treats an absent scope list and an empty one as the same
// eligibility: a checkpoint round-trip can turn nil slices into empty ones.
func normElig(e permission.Eligibility) permission.Eligibility {
	if len(e.Scopes) == 0 {
		e.Scopes = nil
	}
	if len(e.ExcludeSubjects) == 0 {
		e.ExcludeSubjects = nil
	}
	return e
}

func TestNativeAskSuspendsAsHumanApproval(t *testing.T) {
	model := &nrrModel{}
	book := &bookTool{}
	note := &nrrNoteTool{}
	stack, conv := nrrStack(t, model, book, note, nil, nil)
	model.reset(nrrBook("c1"))

	var susp types.Suspended
	for ev, err := range conv.Send(nrrCtx(), "s1", nlUser("go")) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			susp = s
		}
	}
	if susp.Token == "" {
		t.Fatal("Send: no suspension")
	}
	if susp.Reason != types.HumanApproval {
		t.Fatalf("reason = %v, want HumanApproval", susp.Reason)
	}
	req, ok := susp.Payload.(permission.ApprovalRequest)
	if !ok {
		t.Fatalf("payload %T, want permission.ApprovalRequest", susp.Payload)
	}
	if req.Call.ID != "c1" || req.Call.Name != "book" {
		t.Fatalf("payload call = %+v, want the asked book call c1", req.Call)
	}
	if req.Fingerprint != ToolFingerprint(req.Tool, []byte(req.Call.Args)) {
		t.Fatalf("fingerprint %q does not match the registered spec and call args", req.Fingerprint)
	}

	env, st := persistedApproval(t, stack, susp.Token)
	if len(env.Approvals) != 1 {
		t.Fatalf("persisted approvals = %d, want exactly one for the active ask", len(env.Approvals))
	}
	ap := env.Approvals[0]
	if !reflect.DeepEqual(ap.Call, req.Call) {
		t.Fatalf("persisted call %+v disagrees with payload call %+v", ap.Call, req.Call)
	}
	if ap.Fingerprint != req.Fingerprint {
		t.Fatalf("persisted fingerprint %q disagrees with payload %q", ap.Fingerprint, req.Fingerprint)
	}
	if ap.Risk != req.Risk {
		t.Fatalf("persisted risk %v disagrees with payload %v", ap.Risk, req.Risk)
	}
	if !reflect.DeepEqual(normElig(ap.Eligible), normElig(req.Eligible)) {
		t.Fatalf("persisted eligibility %+v disagrees with payload %+v", ap.Eligible, req.Eligible)
	}
	if len(st.Pending) != 1 || st.Pending[0].ID != "c1" {
		t.Fatalf("pending queue = %+v, want the one ask c1", st.Pending)
	}
	// The allow policy records no scope: none may become a requirement.
	if len(ap.Eligible.Scopes) != 0 {
		t.Fatalf("scopes = %v, want none for a policy without a scope", ap.Eligible.Scopes)
	}
	if ap.Eligible.Quorum != 0 {
		t.Fatalf("quorum = %d, want the policy's zero", ap.Eligible.Quorum)
	}
}

func TestNativeApprovedAskExecutesOnce(t *testing.T) {
	model := &nrrModel{}
	note := &nrrNoteTool{}
	probe := &naraProbeTool{}
	stack, conv := naraStack(t, model, note, probe, quorumPolicy{scope: "pay", quorum: 1})
	model.reset(types.ToolUse{ID: "p1", Name: "probe", Args: jsontext.Value(`{"id":"p1"}`)})

	var token types.ResumeToken
	for ev, err := range conv.Send(nrrCtx(), "s1", nlUser("go")) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	if token == "" {
		t.Fatal("Send: no suspension")
	}
	if ran := probe.ran(); len(ran) != 0 {
		t.Fatalf("tool executed before approval: %v", ran)
	}

	second, err := NewNativeConversation(stack, "chat",
		WithConversationRuns(stack.stores.Runs),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		WithConversationApprovalPolicy(quorumPolicy{scope: "pay", quorum: 1}),
	)
	if err != nil {
		t.Fatalf("second conversation: %v", err)
	}
	// An approver without the policy's scope refuses: nothing executes.
	refused := false
	for _, err := range second.Resume(types.WithPrincipal(context.Background(), nrrOwner), token, Approve()) {
		if err != nil {
			refused = true
		}
	}
	if !refused {
		t.Fatal("ineligible approver: Resume accepted the approval")
	}
	if ran := probe.ran(); len(ran) != 0 {
		t.Fatalf("tool executed on an ineligible approval: %v", ran)
	}
	var done bool
	for ev, err := range second.Resume(types.WithPrincipal(context.Background(), types.Principal{Subject: "owner", Tenant: "t1", Scopes: []string{"pay"}}), token, Approve()) {
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if _, ok := ev.(types.Done); ok {
			done = true
		}
	}
	if !done {
		t.Fatal("Resume: no Done event")
	}
	if ran := probe.ran(); len(ran) != 1 || ran[0] != `{"id":"p1"}` {
		t.Fatalf("probe executions after approval: %v, want [p1] exactly once", ran)
	}
}

// naraProbeTool is a ReadOnly tool the test's decider routes through an
// ask, so a mixed read-only turn can settle one call and ask for another.
type naraProbeTool struct {
	mu    sync.Mutex
	ranID []string
}

func (p *naraProbeTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "probe", Effect: types.ReadOnly}
}

func (p *naraProbeTool) Call(_ context.Context, args jsontext.Value) (types.ToolResult, error) {
	p.mu.Lock()
	p.ranID = append(p.ranID, string(args))
	p.mu.Unlock()
	return types.ToolResult{Content: []types.Block{types.Text{Text: "probed"}}, Outcome: types.Succeeded}, nil
}

func (p *naraProbeTool) ran() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.ranID...)
}

// naraAskDecider asks for the probe tool and allows everything else.
type naraAskDecider struct{}

func (naraAskDecider) Decide(_ context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	v := permission.Allow
	if inv.Spec.Name == "probe" {
		v = permission.Ask
	}
	return types.Decision[permission.Verdict]{Value: v, Confidence: 1}, nil
}

func naraStack(t *testing.T, model types.Model, note *nrrNoteTool, probe *naraProbeTool, pol permission.ApprovalPolicySource) (*Stack, Conversation) {
	t.Helper()
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	stack, err := Build(
		WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        runs,
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
		}),
		WithModels(model),
		WithNativeAgent(NativeSpec{
			Request: FlowRequest{Name: "chat"},
			Profile: "native",
			Tools:   []types.Tool{note, probe},
			Decider: naraAskDecider{},
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := NewNativeConversation(stack, "chat",
		WithConversationRuns(runs),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		WithConversationApprovalPolicy(pol),
	)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}
	return stack, conv
}

func TestNativeReadOnlyAskKeepsSettledResults(t *testing.T) {
	model := &nrrModel{}
	note := &nrrNoteTool{}
	probe := &naraProbeTool{}
	stack, conv := naraStack(t, model, note, probe, nrrAllowPolicy{})
	model.reset(nrrNote("n1"), types.ToolUse{ID: "p1", Name: "probe", Args: jsontext.Value(`{"id":"p1"}`)})

	var susp types.Suspended
	for ev, err := range conv.Send(nrrCtx(), "s1", nlUser("go")) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			susp = s
		}
	}
	if susp.Token == "" {
		t.Fatal("Send: no suspension")
	}
	if susp.Reason != types.HumanApproval {
		t.Fatalf("reason = %v, want HumanApproval", susp.Reason)
	}
	if ran := probe.ran(); len(ran) != 0 {
		t.Fatalf("asked read-only tool executed before approval: %v", ran)
	}
	if ran := note.ran(); len(ran) != 1 || ran[0] != "n1" {
		t.Fatalf("note executions: %v, want [n1] once", ran)
	}

	env, st := persistedApproval(t, stack, susp.Token)
	if len(st.Pending) != 1 || st.Pending[0].ID != "p1" {
		t.Fatalf("pending queue = %+v, want the asked probe call p1 as head", st.Pending)
	}
	if len(env.Approvals) != 1 || env.Approvals[0].Call.ID != "p1" {
		t.Fatalf("persisted approvals = %+v, want one for p1", env.Approvals)
	}
	hist, err := stack.stores.SessionLog.Load(nrrCtx(), "s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var sawAsst, sawNote bool
	for _, msg := range hist.Messages {
		for _, b := range msg.Blocks {
			switch bl := b.(type) {
			case types.ToolUse:
				if bl.ID == "n1" || bl.ID == "p1" {
					sawAsst = true
				}
			case types.ToolResult:
				if bl.ID == "n1" {
					sawNote = true
				}
				if bl.ID == "p1" {
					t.Fatalf("settled result persisted for the unresolved ask p1: %+v", bl)
				}
			}
		}
	}
	if !sawAsst {
		t.Fatal("assistant turn calls missing from the session log at the suspension point")
	}
	if !sawNote {
		t.Fatal("settled note result missing from the session log at the suspension point")
	}
}
