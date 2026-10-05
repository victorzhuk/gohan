package gohan

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// turnLog counts Append calls and keeps the messages each one carried.
type turnLog struct {
	stores.SessionLog
	mu      sync.Mutex
	appends [][]types.Message
}

func (l *turnLog) Append(ctx context.Context, sessionID string, expected int64, msgs ...types.Message) (int64, error) {
	l.mu.Lock()
	l.appends = append(l.appends, append([]types.Message(nil), msgs...))
	l.mu.Unlock()
	return l.SessionLog.Append(ctx, sessionID, expected, msgs...)
}

func (l *turnLog) snapshot() [][]types.Message {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([][]types.Message(nil), l.appends...)
}

// turnAppRecorder stands in for the history appender and records the
// order of appends against marks the test places.
type turnAppRecorder struct {
	mu     sync.Mutex
	order  []string
	blocks [][]string
}

func (r *turnAppRecorder) mark(m string) {
	r.mu.Lock()
	r.order = append(r.order, m)
	r.mu.Unlock()
}

func (r *turnAppRecorder) Append(_ context.Context, expected int64, msgs ...types.Message) (int64, error) {
	r.mu.Lock()
	if len(msgs) > 0 && msgs[0].Role == types.RoleAssistant {
		r.order = append(r.order, "calls")
	} else {
		r.order = append(r.order, "results")
	}
	var ids []string
	for _, msg := range msgs {
		for _, b := range msg.Blocks {
			switch blk := b.(type) {
			case types.ToolUse:
				ids = append(ids, blk.ID)
			case types.ToolResult:
				ids = append(ids, blk.ID)
			}
		}
	}
	r.blocks = append(r.blocks, ids)
	r.mu.Unlock()
	return expected + int64(len(msgs)), nil
}

func (r *turnAppRecorder) state() (string, [][]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.order, ","), append([][]string(nil), r.blocks...)
}

// turnObsTool reports to the test from inside the governed call.
type turnObsTool struct {
	spec    types.ToolSpec
	observe func()
}

func (t *turnObsTool) Spec() types.ToolSpec { return t.spec }

func (t *turnObsTool) Call(context.Context, jsontext.Value) (types.ToolResult, error) {
	t.observe()
	return types.ToolResult{Outcome: types.Succeeded}, nil
}

func turnStack(t *testing.T, log stores.SessionLog, model types.Model, tools ...types.Tool) Conversation {
	t.Helper()
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	stack, err := Build(
		WithStores(stores.Stores{SessionLog: log, Runs: runs}),
		WithModels(model),
		WithNativeAgent(NativeSpec{
			Request: FlowRequest{Name: "chat"},
			Profile: "native",
			Tools:   tools,
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
		WithConversationApprovalPolicy(nrrAllowPolicy{}),
	)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}
	return conv
}

// A read-only turn reaches the session log in one append: the assistant
// message and its results share it, next to the run's input append.
func TestNativeReadOnlyTurnSingleAppend(t *testing.T) {
	log := &turnLog{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}
	model := &nrrModel{}
	model.reset(nrrNote("n1"))
	conv := turnStack(t, log, model, &bookTool{}, &nrrNoteTool{})

	for ev, err := range conv.Send(nrrCtx(), "s1", nlUser("go")) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if d, ok := ev.(types.Done); ok && d.Reason != types.StopCompleted {
			t.Fatalf("done %v, want completed", d.Reason)
		}
	}

	got := log.snapshot()
	// The final assistant answer appends on its own; the tool turn must
	// be exactly one append between the input and that answer.
	if len(got) != 3 {
		t.Fatalf("appends %d, want input, one turn append, final answer", len(got))
	}
	var turnAppend []types.Message
	for _, msgs := range got[1:] {
		for _, msg := range msgs {
			if msg.ID == assistantID(1) {
				turnAppend = msgs
			}
		}
	}
	if len(turnAppend) != 2 {
		t.Fatalf("turn append carries %d messages, want the assistant and the results together", len(turnAppend))
	}
	if turnAppend[0].Role != types.RoleAssistant {
		t.Fatalf("first message role %v, want assistant", turnAppend[0].Role)
	}
	cu, ok := turnAppend[0].Blocks[0].(types.ToolUse)
	if !ok || cu.ID != "n1" {
		t.Fatalf("first block %v, want the ToolUse", turnAppend[0].Blocks[0])
	}
	res, ok := turnAppend[1].Blocks[0].(types.ToolResult)
	if !ok || res.ID != "n1" {
		t.Fatalf("second message %v, want the ToolResult", turnAppend[1])
	}
}

// A side-effect call is durable before it executes: the assistant message
// with its pending calls appends first, the results append after the
// execution finished.
func TestNativeSideEffectTurnPersistsCallsBeforeExecution(t *testing.T) {
	rec := &turnAppRecorder{}
	tool := &turnObsTool{
		spec: types.ToolSpec{Name: "push", Effect: types.SideEffect},
		observe: func() {
			rec.mark("exec")
			_, blocks := rec.state()
			for _, ids := range blocks {
				for _, id := range ids {
					if id == "c1" {
						return
					}
				}
			}
			t.Error("side effect ran before its call was durable")
		},
	}
	stack := nativeStack(chains.PromptSet{})
	bound, _ := nativeConfig(stack, tool)
	env := &turnEnv{c: bound, turn: 1, calls: []types.ToolUse{
		{ID: "c1", Name: "push", Args: jsontext.Value(`{}`)},
	}}
	ctx := withHistoryAppender(types.WithSink(withTurnEnv(context.Background(), env), sinkInto(nil)), rec)
	if _, _, _, err := batchEffect(ctx, runtime.State{}); err != nil {
		t.Fatalf("batch: %v", err)
	}
	order, blocks := rec.state()
	if order != "calls,exec,results" {
		t.Fatalf("order %q, want calls, exec, results", order)
	}
	if strings.Join(blocks[0], ",") != "c1" || strings.Join(blocks[1], ",") != "c1" {
		t.Fatalf("appended ids %v, want c1 before and after execution", blocks)
	}
}

// The run stops at MaxTurns model calls: the last batch settled, the
// terminal is one Done with the limit reason, and no further model call
// happens.
func TestNativeMaxTurnsStopsPublicRun(t *testing.T) {
	model := &nrrModel{}
	model.reset(nrrNote("n1"), nrrNote("n2"), nrrNote("n3"), nrrNote("n4"), nrrNote("n5"))
	note := &nrrNoteTool{}
	limits := nativeConvLimits(50)
	limits.MaxTurns = 3
	_, conv := nrrStack(t, model, &bookTool{}, note, nil, nil, WithLimits("chat", limits))

	var dones []types.Done
	for ev, err := range conv.Send(nrrCtx(), "s1", nlUser("go")) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if d, ok := ev.(types.Done); ok {
			dones = append(dones, d)
		}
	}
	if len(dones) != 1 {
		t.Fatalf("done events %d, want one", len(dones))
	}
	if dones[0].Reason != types.StopLimit {
		t.Fatalf("reason %v, want %v", dones[0].Reason, types.StopLimit)
	}
	model.mu.Lock()
	calls := model.calls
	model.mu.Unlock()
	if calls != 3 {
		t.Fatalf("model calls %d, want 3", calls)
	}
	if ran := note.ran(); len(ran) != 3 {
		t.Fatalf("tool ran %v, want the final batch settled with 3 calls", ran)
	}
}

// A drive entered with the state already at MaxTurns ends before the
// model call: the restored turn count is the one the bound applies to.
func TestNativeTurnRestoredFromState(t *testing.T) {
	called := false
	stack := nativeStack(chains.PromptSet{})
	bound, _ := nativeConfig(stack, &echoTool{})
	bound.maxTurns = 3
	bound.model = func(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		called = true
		return func(yield func(types.ModelChunk, error) bool) {
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: "nope"}, nil)
		}
	}
	env := &turnEnv{c: bound}
	ctx := types.WithSink(withTurnEnv(context.Background(), env), sinkInto(nil))
	st, evs, status, err := modelEffect(ctx, runtime.State{Turn: 3})
	if err != nil {
		t.Fatalf("model effect: %v", err)
	}
	if status != runtime.DoneStatus {
		t.Fatalf("status %v, want done", status)
	}
	if len(evs) != 1 {
		t.Fatalf("events %v, want one terminal", evs)
	}
	done, ok := evs[0].(types.Done)
	if !ok || done.Reason != types.StopLimit {
		t.Fatalf("event %v, want Done{StopLimit}", evs[0])
	}
	if st.Turn != 3 {
		t.Fatalf("turn %d, want the bound", st.Turn)
	}
	if called {
		t.Fatal("model called past the bound")
	}
}

// A batch wider than the remaining tool-call budget refuses at the driver
// boundary as a MaxToolCalls limit error, before any call executes.
func TestBatchOverrunTranslatesToLimitExceeded(t *testing.T) {
	execs := 0
	stack := nativeStack(chains.PromptSet{})
	bound, _ := nativeConfig(stack, &echoTool{})
	bound.limits.MaxToolCalls = 2
	bound.exec = func(context.Context, types.ToolUse) (types.ToolResult, error) {
		execs++
		return types.ToolResult{Outcome: types.Succeeded}, nil
	}
	env := &turnEnv{c: bound, turn: 1, calls: []types.ToolUse{
		{ID: "c1", Name: "echo", Args: jsontext.Value(`{}`)},
		{ID: "c2", Name: "echo", Args: jsontext.Value(`{}`)},
		{ID: "c3", Name: "echo", Args: jsontext.Value(`{}`)},
	}}
	ctx := types.WithSink(withTurnEnv(context.Background(), env), sinkInto(nil))
	_, _, _, err := batchEffect(ctx, runtime.State{})
	var over *types.LimitExceededError
	if !errors.As(err, &over) {
		t.Fatalf("error %v, want *LimitExceededError", err)
	}
	if over.Limit != "MaxToolCalls" || over.Value != 3 {
		t.Fatalf("limit %s value %v, want MaxToolCalls 3", over.Limit, over.Value)
	}
	if execs != 0 {
		t.Fatalf("execs %d, want none", execs)
	}
}
