package gohan

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jsonv2 "encoding/json/v2"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// chainReq is the typed input every sub-flow in the scenario chain takes.
type chainReq struct {
	Ask string `json:"ask"`
}

// chainModel scripts one native conversation: one scripted call per tool
// result the history already carries, done after the last one is answered.
type chainModel struct {
	profile string
	steps   []types.ToolUse
	final   string
}

func (m *chainModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: m.profile, Caps: types.Caps{Tools: true}}
}

func (m *chainModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		answered := 0
		for _, msg := range req.Messages {
			for _, b := range msg.Blocks {
				if _, ok := b.(types.ToolResult); ok {
					answered++
				}
			}
		}
		if answered >= len(m.steps) {
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: m.final}, nil)
			yield(types.ModelChunk{Finish: types.FinishStop}, nil)
			return
		}
		call := m.steps[answered]
		yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &call}, nil)
		yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
	}
}

// chainLeafTool is the suspending leaf: the batch gate asks for its side
// effect before the first execution, so a call without a recorded approval
// parks the run and an approved replay completes it.
type chainLeafTool struct {
	mu      sync.Mutex
	asks    []string
	budgets []types.RunBudget
}

func (l *chainLeafTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "ask_leaf", Effect: types.SideEffect, Description: "asks the human"}
}

func (l *chainLeafTool) Call(ctx context.Context, args jsontext.Value) (types.ToolResult, error) {
	var in chainReq
	if err := jsonv2.Unmarshal(args, &in); err != nil {
		return types.ToolResult{}, err
	}
	l.mu.Lock()
	l.asks = append(l.asks, in.Ask)
	if b, ok := types.RunBudgetFrom(ctx); ok {
		l.budgets = append(l.budgets, b)
	}
	l.mu.Unlock()
	if _, ok := types.ApprovalFrom(ctx); !ok {
		return types.ToolResult{}, SuspendTool(types.AwaitingInput, map[string]string{"ask": in.Ask})
	}
	return types.ToolResult{Content: []types.Block{types.Text{Text: "leaf:" + in.Ask}}, Outcome: types.Succeeded}, nil
}

func (l *chainLeafTool) ran() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.asks...)
}

// chainFlow adapts one native conversation to the public Flow face: Send
// and Resume stream, a Suspended event becomes a SuspendError, a Done
// event completes with the accumulated text.
type chainFlow struct {
	stack  *Stack
	spec   string
	sessID atomic.Int64
}

func (f *chainFlow) newConv() (Conversation, error) {
	return NewNativeConversation(f.stack, f.spec,
		WithConversationRuns(f.stack.stores.Runs),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		WithConversationApprovalPolicy(nrrAllowPolicy{}),
	)
}

func (f *chainFlow) nextSession() string {
	return fmt.Sprintf("s-%s-%d", f.spec, f.sessID.Add(1))
}

// runSession names the session one parent run drives the child flow
// through: every call the parent makes in the session shares the child's
// history, so the scripted leaf advances across calls instead of
// restarting its script per call.
func (f *chainFlow) runSession(ctx context.Context) string {
	if info, ok := types.RunInfoFrom(ctx); ok && info.SessionID != "" {
		return info.SessionID + "-" + f.spec
	}
	return f.nextSession()
}

func (f *chainFlow) Invoke(ctx context.Context, in chainReq) (string, error) {
	conv, err := f.newConv()
	if err != nil {
		return "", err
	}
	sess := f.runSession(ctx)
	out, susp, done, err := streamChain(conv.Send(types.WithPrincipal(ctx, nrrOwner), sess, nlUser(in.Ask)))
	if err != nil {
		return "", err
	}
	return chainFlowSignal(f.spec, out, susp, done)
}

func (f *chainFlow) Resume(ctx context.Context, t types.ResumeToken, r stores.ResumeInput) (string, error) {
	conv, err := f.newConv()
	if err != nil {
		return "", err
	}
	out, susp, done, err := streamChain(conv.Resume(types.WithPrincipal(ctx, nrrOwner), t, r))
	if err != nil {
		return "", err
	}
	return chainFlowSignal(f.spec, out, susp, done)
}

// chainFlowSignal projects the streamed terminal state onto the Flow
// contract: a suspension crosses verbatim, a Done completes the flow.
func chainFlowSignal(spec, out string, susp *types.SuspendError, done bool) (string, error) {
	if susp != nil {
		return "", susp
	}
	if !done {
		return "", fmt.Errorf("subflow %s: stream ended without a terminal event", spec)
	}
	return out, nil
}

func streamChain(seq iter.Seq2[Event, error]) (out string, susp *types.SuspendError, done bool, err error) {
	for ev, serr := range seq {
		if serr != nil {
			return "", nil, false, serr
		}
		switch e := ev.(type) {
		case types.TextDelta:
			out += e.Delta
		case types.Suspended:
			susp = &types.SuspendError{Token: e.Token, Reason: e.Reason, Payload: e.Payload, WakeAt: e.WakeAt}
		case types.Done:
			done = true
		}
	}
	return out, susp, done, nil
}

// chainHarness wires the three native conversations over one shared store
// set: LEAF inside MIDDLE inside ROOT, each level exposing the next as a
// sub-flow tool. depth 1 builds the MIDDLE+LEAF pair alone; depth 2 adds
// ROOT and scripts the leaf for a second suspension.
type chainHarness struct {
	stores   stores.Stores
	cps      *stores.MemoryCheckpoints
	leaf     *chainLeafTool
	leafFlow *chainFlow
	midFlow  *chainFlow
	rootFlow *chainFlow
}

// chainHarnessOpts tunes one fixture build: the root level's limits and
// the budget bounds the leaf sub-flow call declares.
type chainHarnessOpts struct {
	rootLimits  func(*types.RunLimits)
	childLimits *types.ChildLimits
}

func newChainHarness(t *testing.T, depth int, opts ...func(*chainHarnessOpts)) *chainHarness {
	t.Helper()
	var o chainHarnessOpts
	for _, opt := range opts {
		opt(&o)
	}
	h := &chainHarness{leaf: &chainLeafTool{}}
	var runs stores.Runs = stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(types.RunInfoFrom),
	)
	cpOpts := []stores.MemoryCheckpointOption{
		stores.WithMemoryCheckpointClock(time.Now),
		stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom),
	}
	if cr, ok := runs.(stores.CheckpointRuns); ok {
		cpOpts = append(cpOpts, stores.WithMemoryCheckpointRuns(cr))
	}
	h.cps = stores.NewMemoryCheckpoints(cpOpts...)
	h.stores = stores.Stores{
		SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
		Runs:        runs,
		Checkpoints: h.cps,
	}
	limits := types.RunLimits{
		MaxTurns: 8, MaxToolCalls: 8, MaxWallClock: time.Minute,
		SoftRatio: 0.8, MaxDepth: 4, MaxParallelChildren: 2, MaxParallelTools: 2,
	}
	if o.rootLimits != nil {
		o.rootLimits(&limits)
	}
	base := []Option{
		WithStores(h.stores),
		WithLimits("root", limits),
		WithLimits("mid", limits),
		WithLimits("leaf", limits),
	}
	inner, err := Build(base...)
	if err != nil {
		t.Fatalf("build inner stack: %v", err)
	}
	// The flows bind their conversation stack after Build: the sub-flow
	// tools exist before the native definitions resolve.
	h.leafFlow = &chainFlow{spec: "leaf"}
	h.midFlow = &chainFlow{spec: "mid"}
	leafOpts := []SubflowOption{WithDescription("leaf flow")}
	if o.childLimits != nil {
		leafOpts = append(leafOpts, WithChildLimits(*o.childLimits))
	}
	leafTool, err := inner.FlowAsTool("leaf", h.leafFlow, leafOpts...)
	if err != nil {
		t.Fatalf("leaf flow as tool: %v", err)
	}
	midTool, err := inner.FlowAsTool("middle", h.midFlow, WithDescription("middle flow"))
	if err != nil {
		t.Fatalf("middle flow as tool: %v", err)
	}

	leafAsks := []string{"book"}
	if depth > 1 {
		leafAsks = append(leafAsks, "confirm")
	}
	leafSteps := make([]types.ToolUse, 0, len(leafAsks))
	for i, ask := range leafAsks {
		leafSteps = append(leafSteps, types.ToolUse{
			ID:   fmt.Sprintf("l%d", i+1),
			Name: "ask_leaf",
			Args: jsontext.Value(`{"ask":"` + ask + `"}`),
		})
	}
	// The middle level issues one sub-flow call; the leaf's second ask
	// happens inside that one call's leaf conversation.
	midSteps := []types.ToolUse{{
		ID:   "m1",
		Name: "leaf",
		Args: jsontext.Value(`{"ask":"` + leafAsks[0] + `"}`),
	}}
	leafModel := &chainModel{profile: "leaf-m", steps: leafSteps, final: "leaf done"}
	midModel := &chainModel{profile: "mid-m", steps: midSteps, final: "mid done"}
	buildOpts := append(append([]Option{}, base...), WithModels(leafModel, midModel),
		WithNativeAgent(NativeSpec{
			Request:  FlowRequest{Name: "leaf"},
			Profile:  "leaf-m",
			Tools:    []types.Tool{h.leaf},
			Assemble: chainAssemble,
		}),
		WithNativeAgent(NativeSpec{
			Request:  FlowRequest{Name: "mid"},
			Profile:  "mid-m",
			Tools:    []types.Tool{leafTool},
			Assemble: chainAssemble,
		}),
	)
	if depth > 1 {
		rootModel := &chainModel{
			profile: "root-m",
			steps:   []types.ToolUse{{ID: "r1", Name: "middle", Args: jsontext.Value(`{"ask":"go"}`)}},
			final:   "root done",
		}
		buildOpts = append(buildOpts, WithModels(rootModel), WithNativeAgent(NativeSpec{
			Request:  FlowRequest{Name: "root"},
			Profile:  "root-m",
			Tools:    []types.Tool{midTool},
			Assemble: chainAssemble,
		}))
		h.rootFlow = &chainFlow{spec: "root"}
	}
	outer, err := Build(buildOpts...)
	if err != nil {
		t.Fatalf("build outer stack: %v", err)
	}
	h.leafFlow.stack = outer
	h.midFlow.stack = outer
	if h.rootFlow != nil {
		h.rootFlow.stack = outer
	}
	return h
}

func (h *chainHarness) conv(t *testing.T, spec string) Conversation {
	t.Helper()
	conv, err := NewNativeConversation(h.convStack(), spec,
		WithConversationRuns(h.stores.Runs),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		WithConversationApprovalPolicy(nrrAllowPolicy{}),
	)
	if err != nil {
		t.Fatalf("new %s conversation: %v", spec, err)
	}
	return conv
}

func (h *chainHarness) convStack() *Stack {
	if h.rootFlow != nil {
		return h.rootFlow.stack
	}
	return h.midFlow.stack
}

func (h *chainHarness) peek(t *testing.T, tok ResumeToken) stores.Checkpoint {
	t.Helper()
	cp, err := h.cps.Peek(context.Background(), tok)
	if err != nil {
		t.Fatalf("peek checkpoint: %v", err)
	}
	return cp
}

func chainStream(t *testing.T, seq iter.Seq2[Event, error]) (out string, susp *types.SuspendError, done bool) {
	t.Helper()
	var err error
	out, susp, done, err = streamChain(seq)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	return out, susp, done
}

func chainAssemble(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
	return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
}

// mustResumeAndComplete resumes one level and asserts the chain settles it:
// the leaf completes, the level settles its sub-flow call, the run finishes.
func (h *chainHarness) mustResumeAndComplete(t *testing.T, spec string, tok ResumeToken) string {
	t.Helper()
	out, susp, done := chainStream(t, h.conv(t, spec).Resume(nrrCtx(), tok, Approve()))
	if susp != nil {
		t.Fatalf("resume surfaced a suspension with token %s", susp.Token)
	}
	if !done {
		t.Fatal("resume ended without Done")
	}
	return out
}

// mustRefuseConsumed presents a token the chain already consumed.
func (h *chainHarness) mustRefuseConsumed(t *testing.T, spec string, tok ResumeToken) {
	t.Helper()
	for _, err := range h.conv(t, spec).Resume(nrrCtx(), tok, Approve()) {
		if !errors.Is(err, types.ErrTokenConsumed) {
			t.Fatalf("resume of consumed token = %v, want ErrTokenConsumed", err)
		}
	}
}

func TestSubflowsChain(t *testing.T) {
	ctx := nrrCtx()

	var (
		h1                *chainHarness
		midToken, leafTk1 ResumeToken
	)
	t.Run("subflows.nested-approval", func(t *testing.T) {
		h1 = newChainHarness(t, 1)
		_, err := h1.midFlow.Invoke(ctx, chainReq{Ask: "book"})
		var susp *types.SuspendError
		if !errors.As(err, &susp) {
			t.Fatalf("invoke err = %v, want SuspendError", err)
		}
		if susp.Token == "" {
			t.Fatal("parent token is empty")
		}
		if susp.Reason != types.HumanApproval {
			t.Fatalf("reason = %v, want HumanApproval", susp.Reason)
		}
		if _, ok := susp.Payload.(permission.ApprovalRequest); !ok {
			t.Fatalf("payload type = %T, want ApprovalRequest", susp.Payload)
		}
		mid := h1.peek(t, susp.Token)
		if mid.Child == "" {
			t.Fatal("parent checkpoint carries no child token")
		}
		leafCp := h1.peek(t, mid.Child)
		if leafCp.Reason != types.HumanApproval {
			t.Fatalf("leaf reason = %v, want HumanApproval", leafCp.Reason)
		}
		if ran := h1.leaf.ran(); len(ran) != 0 {
			t.Fatalf("leaf executed %v before approval", ran)
		}
		midToken, leafTk1 = susp.Token, mid.Child
	})

	t.Run("subflows.nested-resume", func(t *testing.T) {
		h1.mustResumeAndComplete(t, "mid", midToken)
		if ran := h1.leaf.ran(); !slices.Equal(ran, []string{"book"}) {
			t.Fatalf("leaf executions = %v, want [book]", ran)
		}
	})

	t.Run("subflows.nested-tokens-single-use", func(t *testing.T) {
		h1.mustRefuseConsumed(t, "mid", leafTk1)
	})

	var (
		h2                      *chainHarness
		rootTk1, rootTk2        ResumeToken
		midTk1, midTk2, leafTk2 ResumeToken
	)
	t.Run("subflows.suspension-depth-two-chain", func(t *testing.T) {
		h2 = newChainHarness(t, 2)
		_, err := h2.rootFlow.Invoke(ctx, chainReq{Ask: "go"})
		var susp *types.SuspendError
		if !errors.As(err, &susp) {
			t.Fatalf("invoke err = %v, want SuspendError", err)
		}
		if susp.Token == "" || susp.Reason != types.HumanApproval || susp.Payload == nil {
			t.Fatalf("root suspension = %+v, want token, HumanApproval and payload", susp)
		}
		if _, ok := susp.Payload.(permission.ApprovalRequest); !ok {
			t.Fatalf("payload type = %T, want ApprovalRequest", susp.Payload)
		}
		rootTk1 = susp.Token
		midTk1 = h2.peek(t, rootTk1).Child
		leafTk1OfMid := h2.peek(t, midTk1).Child
		if midTk1 == "" || leafTk1OfMid == "" {
			t.Fatalf("chain tokens missing: mid %q leaf %q", midTk1, leafTk1OfMid)
		}
		if midTk1 == rootTk1 || leafTk1OfMid == midTk1 {
			t.Fatal("chain tokens are not distinct")
		}
		if ran := h2.leaf.ran(); len(ran) != 0 {
			t.Fatalf("leaf executed %v before any approval", ran)
		}
	})

	t.Run("subflows.resume-consumes-chain", func(t *testing.T) {
		// The first resume consumes the root token, settles the depth-2
		// leaf's first ask through the chain and stops at the leaf's second
		// ask: the parent re-suspends carrying the depth-1 token.
		_, susp, done := chainStream(t, h2.conv(t, "root").Resume(nrrCtx(), rootTk1, Approve()))
		if done {
			t.Fatal("first resume completed the chain")
		}
		if susp == nil || susp.Token == "" {
			t.Fatal("first resume surfaced no suspension")
		}
		rootTk2 = susp.Token
		if rootTk2 == rootTk1 {
			t.Fatal("re-suspension reused the consumed token")
		}
		midTk2 = h2.peek(t, rootTk2).Child
		leafTk2 = h2.peek(t, midTk2).Child
		if midTk2 == "" || leafTk2 == "" {
			t.Fatalf("second chain tokens missing: mid %q leaf %q", midTk2, leafTk2)
		}
		if ran := h2.leaf.ran(); !slices.Equal(ran, []string{"book"}) {
			t.Fatalf("leaf executions after first resume = %v, want [book]", ran)
		}
		// The second resume completes the depth-2 child, the depth-1 level
		// and the root in one drive.
		out := h2.mustResumeAndComplete(t, "root", rootTk2)
		if out != "root done" {
			t.Fatalf("root out = %q, want root done", out)
		}
		if ran := h2.leaf.ran(); !slices.Equal(ran, []string{"book", "confirm"}) {
			t.Fatalf("leaf executions = %v, want [book confirm]", ran)
		}
	})

	t.Run("subflows.inner-token-consumed-by-chain", func(t *testing.T) {
		h2.mustRefuseConsumed(t, "leaf", leafTk2)
	})

	t.Run("a resumed level keeps its recorded budget", func(t *testing.T) {
		h3 := newChainHarness(t, 2,
			func(o *chainHarnessOpts) {
				o.rootLimits = func(l *types.RunLimits) { l.MaxWallClock = 10 * time.Minute; l.MaxCost = 1.0 }
				o.childLimits = &types.ChildLimits{MaxWallClock: 4 * time.Minute, CostShare: types.ShareFixed, FixedCost: 0.2}
			},
		)
		out, err := h3.rootFlow.Invoke(ctx, chainReq{Ask: "go"})
		var susp *types.SuspendError
		if !errors.As(err, &susp) {
			t.Fatalf("invoke err = %v out = %q leafRan = %v", err, out, h3.leaf.ran())
		}
		// The first resume replays the leaf's approved ask: the resumed
		// depth-2 level must run under the budget the checkpoint recorded.
		// The leaf's second ask surfaces another suspension; the second
		// resume completes the chain.
		_, rSusp, _ := chainStream(t, h3.conv(t, "root").Resume(nrrCtx(), susp.Token, Approve()))
		if rSusp == nil || rSusp.Token == "" {
			t.Fatal("first resume surfaced no suspension")
		}
		finalOut := h3.mustResumeAndComplete(t, "root", rSusp.Token)
		if finalOut != "root done" {
			t.Fatalf("root out = %q, want root done", finalOut)
		}
		// The recorded budget is the derivation the sub-flow boundary
		// made for the leaf call: wall clock and cost from the declared
		// child limits, one level under its calling conversation.
		want := types.RunBudget{WallClock: 4 * time.Minute, Cost: 0.2, Depth: 1}
		h3.leaf.mu.Lock()
		got := append([]types.RunBudget(nil), h3.leaf.budgets...)
		h3.leaf.mu.Unlock()
		if len(got) == 0 {
			t.Fatal("resumed leaf drive observed no run budget")
		}
		for _, b := range got {
			if !reflect.DeepEqual(b, want) {
				t.Fatalf("resumed leaf budget = %+v, want %+v", b, want)
			}
		}
	})
}
