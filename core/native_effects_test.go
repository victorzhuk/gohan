package gohan

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

func nativeStack(prompts chains.PromptSet) *Stack {
	return &Stack{prompts: prompts}
}

func nativeConfig(stack *Stack, tools ...types.Tool) (turnConfig, *resolvedNativeConfig) {
	cfg := &resolvedNativeConfig{
		model:    (&scriptTurns{}).nativeModel(),
		plan:     StrategyPlan{ParallelTools: true, MaxParallelTools: 2},
		tools:    tools,
		assemble: turnAssemble(nil),
		limits:   types.RunLimits{MaxTurns: 4, MaxToolCalls: 8},
	}
	return stack.nativeTurnConfig(cfg), cfg
}

// nativeModel adapts scriptTurns to the Model interface the resolved
// configuration carries.
func (m *scriptTurns) nativeModel() types.Model {
	return nativeModelFunc(m.model())
}

type nativeModelFunc types.ModelFunc

func (f nativeModelFunc) Profile() types.ModelProfile { return types.ModelProfile{Name: "native"} }
func (f nativeModelFunc) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return types.ModelFunc(f)(ctx, req)
}

func TestNativeTurnConfigBinding(t *testing.T) {
	stack := nativeStack(chains.PromptSet{FenceOpen: "<data>", FenceClose: "</data>", Version: "v1"})
	tc, _ := nativeConfig(stack, &echoTool{})

	if !tc.scheduler.Parallel || tc.scheduler.MaxParallel != 2 {
		t.Fatalf("scheduler = %+v, want parallel with cap 2", tc.scheduler)
	}
	if tc.scheduler.EffectOf == nil {
		t.Fatal("EffectOf must come from the resolved tools")
	}
	call := types.ToolUse{ID: "c1", Name: "echo"}
	if got := tc.scheduler.EffectOf(call); got != types.ReadOnly {
		t.Fatalf("EffectOf(echo) = %v, want ReadOnly", got)
	}
	if tc.exec == nil {
		t.Fatal("governed exec must wrap the resolved tool chain")
	}
	if tc.maxTurns != 4 || tc.limits.MaxToolCalls != 8 {
		t.Fatalf("limits not bound: turns %d, tools %d", tc.maxTurns, tc.limits.MaxToolCalls)
	}

	req, err := tc.assemble(context.Background(), types.AssembleInput{})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(req.System) != 1 {
		t.Fatalf("system blocks %d, want 1", len(req.System))
	}
	txt, ok := req.System[0].(types.Text)
	if !ok || !strings.Contains(txt.Text, "<data>") {
		t.Fatalf("prompt block %v, want fenced text", req.System[0])
	}
	if strings.Contains(txt.Text, "v1") {
		t.Fatal("Version is hashed, never shown to the model")
	}
	if txt.Origin.Kind != types.OriginSystem {
		t.Fatalf("origin %v, want system", txt.Origin)
	}

	empty := nativeStack(chains.PromptSet{Version: "v1"})
	tcEmpty, _ := nativeConfig(empty, &echoTool{})
	reqEmpty, err := tcEmpty.assemble(context.Background(), types.AssembleInput{})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(reqEmpty.System) != 0 {
		t.Fatalf("all-empty set inserted %d blocks, want none", len(reqEmpty.System))
	}
}

func TestNativeRunTurnSequence(t *testing.T) {
	m := &scriptTurns{turns: [][]types.ModelChunk{
		{toolCall("c1", "echo"), {Finish: types.FinishToolUse}},
		{turnTextChunk("done")},
	}}
	stack := nativeStack(chains.PromptSet{})
	tc, _ := nativeConfig(stack, &echoTool{})
	tc.model = m.model()
	run := nativeRun(tc, nil, []types.Message{{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}})

	ctx := types.WithSink(context.Background(), sinkFunc(func(context.Context, types.Event) {}))
	st, _, status, err := run.ModelEffect(ctx, runtime.State{})
	if err != nil || status != runtime.Continue {
		t.Fatalf("first model effect: status %v err %v", status, err)
	}
	if len(m.reqs) != 1 || len(m.reqs[0].Messages) != 1 {
		t.Fatalf("input message missing from first request: %+v", m.reqs)
	}
	st, _, status, err = run.BatchEffect(ctx, st)
	if err != nil || status != runtime.Continue {
		t.Fatalf("batch effect: status %v err %v", status, err)
	}
	_, evs, status, err := run.ModelEffect(ctx, st)
	if err != nil || status != runtime.DoneStatus {
		t.Fatalf("final model effect: status %v err %v", status, err)
	}
	if len(evs) != 1 {
		t.Fatalf("events %v, want only the AssistantMessage", evs)
	}
	if _, ok := evs[0].(types.AssistantMessage); !ok {
		t.Fatalf("first event %v, want AssistantMessage", evs[0])
	}
}

func TestNativeToolIdentityPreserved(t *testing.T) {
	var mu sync.Mutex
	var seen []types.ToolUse
	chain := chains.ToolChain{{
		Name: "note", Kind: chains.KindUser,
		Use: func(next chains.ToolFunc) chains.ToolFunc {
			return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
				mu.Lock()
				seen = append(seen, call)
				mu.Unlock()
				return next(ctx, call)
			}
		},
	}}
	stack := nativeStack(chains.PromptSet{})
	tc, cfg := nativeConfig(stack, &echoTool{})
	cfg.toolChain = chain
	tc.exec = governedToolExec(chain, nativeToolset(cfg.tools))

	m := &scriptTurns{turns: [][]types.ModelChunk{
		{toolCall("c1", "echo"), {Finish: types.FinishToolUse}},
	}}
	tc.model = m.model()
	run := nativeRun(tc, nil, nil)
	ctx := context.Background()
	st, _, _, err := run.ModelEffect(ctx, runtime.State{})
	if err != nil {
		t.Fatalf("model effect: %v", err)
	}
	if _, _, _, err := run.BatchEffect(ctx, st); err != nil {
		t.Fatalf("batch effect: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("chain saw %d calls, want 1", len(seen))
	}
	got := seen[0]
	if got.ID != "c1" || got.Name != "echo" || string(got.Args) != "{}" {
		t.Fatalf("chain saw %+v, want the original ToolUse", got)
	}
}

func TestNativeControlErrorsStayControlErrors(t *testing.T) {
	limitChain := chains.ToolChain{{
		Name: "limit", Kind: chains.KindLimit,
		Use: func(next chains.ToolFunc) chains.ToolFunc {
			return func(context.Context, types.ToolUse) (types.ToolResult, error) {
				return types.ToolResult{}, &types.LimitExceededError{Limit: "MaxToolCalls", Value: 3}
			}
		},
	}}
	cases := []struct {
		name  string
		chain chains.ToolChain
		tool  types.Tool
		match func(error) bool
	}{
		{"abort", nil, &errTool{err: &types.AbortError{Reason: "user"}}, func(err error) bool {
			var e *types.AbortError
			return errors.As(err, &e)
		}},
		{"suspend", nil, &errTool{err: &types.SuspendError{Reason: "ask"}}, func(err error) bool {
			var e *types.SuspendError
			return errors.As(err, &e)
		}},
		{"limit", limitChain, &echoTool{}, func(err error) bool {
			var e *types.LimitExceededError
			return errors.As(err, &e)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stack := nativeStack(chains.PromptSet{})
			bound, cfg := nativeConfig(stack, tc.tool)
			cfg.toolChain = tc.chain
			bound.exec = governedToolExec(tc.chain, nativeToolset(cfg.tools))

			m := &scriptTurns{turns: [][]types.ModelChunk{
				{toolCall("c1", tc.tool.Spec().Name), {Finish: types.FinishToolUse}},
			}}
			bound.model = m.model()
			run := nativeRun(bound, nil, nil)
			ctx := context.Background()
			st, _, _, err := run.ModelEffect(ctx, runtime.State{})
			if err != nil {
				t.Fatalf("model effect: %v", err)
			}
			_, _, _, err = run.BatchEffect(ctx, st)
			if err == nil || !tc.match(err) {
				t.Fatalf("error %v, want a preserved control error", err)
			}
		})
	}
}

func TestNativeReservationOrderAndRefund(t *testing.T) {
	var mu sync.Mutex
	var order []string
	tool := &gatedTool{spec: types.ToolSpec{Name: "echo", Effect: types.ReadOnly}}

	stack := nativeStack(chains.PromptSet{})
	bound, _ := nativeConfig(stack, tool)
	bound.reserve = func(ctx context.Context, n int) (context.Context, func(), error) {
		mu.Lock()
		order = append(order, "reserve")
		mu.Unlock()
		return ctx, func() {
			mu.Lock()
			order = append(order, "refund")
			mu.Unlock()
		}, nil
	}
	bound.exec = func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
		mu.Lock()
		order = append(order, "exec")
		mu.Unlock()
		return types.ToolResult{Outcome: types.Succeeded}, nil
	}

	env := &turnEnv{c: bound, turn: 1, calls: []types.ToolUse{
		{ID: "c1", Name: "echo", Args: jsontext.Value(`{}`)}, {ID: "c2", Name: "echo", Args: jsontext.Value(`{}`)},
	}}
	ctx := types.WithSink(withTurnEnv(context.Background(), env), sinkInto(nil))
	if _, _, _, err := batchEffect(ctx, runtime.State{}); err != nil {
		t.Fatalf("batch: %v", err)
	}
	mu.Lock()
	got := strings.Join(order, ",")
	mu.Unlock()
	if !strings.HasPrefix(got, "reserve,exec") {
		t.Fatalf("order %q, want reserve before the first exec", got)
	}

	order = nil
	env2 := &turnEnv{c: bound, turn: 1, used: 7, calls: []types.ToolUse{
		{ID: "c1", Name: "echo", Args: jsontext.Value(`{}`)}, {ID: "c2", Name: "echo", Args: jsontext.Value(`{}`)},
	}}
	bound.limits.MaxToolCalls = 8
	ctx2 := types.WithSink(withTurnEnv(context.Background(), env2), sinkInto(nil))
	if _, _, _, err := batchEffect(ctx2, runtime.State{}); !errors.Is(err, types.ErrBatchOverrun) {
		t.Fatalf("error %v, want ErrBatchOverrun", err)
	}
	mu.Lock()
	got = strings.Join(order, ",")
	mu.Unlock()
	if got != "reserve,refund" {
		t.Fatalf("refused batch order %q, want reserve then refund and no exec", got)
	}
}

func TestNativeSideEffectAppendsBeforeExecution(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	tool := &gatedTool{
		spec:    types.ToolSpec{Name: "push", Effect: types.SideEffect},
		entered: entered,
		release: release,
	}
	stack := nativeStack(chains.PromptSet{})
	bound, _ := nativeConfig(stack, tool)

	env := &turnEnv{c: bound, turn: 1, calls: []types.ToolUse{
		{ID: "c1", Name: "push", Args: jsontext.Value(`{}`)},
	}}
	ctx := types.WithSink(withTurnEnv(context.Background(), env), sinkInto(nil))

	type outcome struct {
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		_, _, _, err := batchEffect(ctx, runtime.State{})
		done <- outcome{err: err}
	}()
	<-entered

	if len(env.msgs) != 1 {
		t.Fatalf("history holds %d messages at execution time, want the pending assistant", len(env.msgs))
	}
	asst := env.msgs[0]
	if asst.Role != types.RoleAssistant || len(asst.Blocks) != 1 {
		t.Fatalf("pending message %+v, want assistant with its call", asst)
	}
	if cu, ok := asst.Blocks[0].(types.ToolUse); !ok || cu.ID != "c1" {
		t.Fatalf("pending block %v, want the ToolUse", asst.Blocks[0])
	}
	close(release)
	if o := <-done; o.err != nil {
		t.Fatalf("batch: %v", o.err)
	}
}

func TestNativeBatchResultsRetainCallOrder(t *testing.T) {
	var evs []types.Event
	var mu sync.Mutex
	sink := sinkFunc(func(_ context.Context, e types.Event) {
		mu.Lock()
		evs = append(evs, e)
		mu.Unlock()
	})

	tools := make([]types.Tool, 0, 3)
	releases := make([]chan struct{}, 3)
	for i, name := range []string{"s1", "s2", "s3"} {
		releases[i] = make(chan struct{})
		tools = append(tools, &gatedTool{
			spec:    types.ToolSpec{Name: name, Effect: types.ReadOnly},
			entered: make(chan struct{}, 1),
			release: releases[i],
		})
	}
	stack := nativeStack(chains.PromptSet{})
	bound, _ := nativeConfig(stack, tools...)
	bound.scheduler.Parallel = true
	bound.scheduler.MaxParallel = 3

	env := &turnEnv{c: bound, turn: 1, calls: []types.ToolUse{
		{ID: "c1", Name: "s1", Args: jsontext.Value(`{}`)}, {ID: "c2", Name: "s2", Args: jsontext.Value(`{}`)}, {ID: "c3", Name: "s3", Args: jsontext.Value(`{}`)},
	}}
	mu.Lock()
	env.sink = sink
	mu.Unlock()
	ctx := types.WithSink(withTurnEnv(context.Background(), env), sink)

	done := make(chan error, 1)
	go func() {
		_, _, _, err := batchEffect(ctx, runtime.State{})
		done <- err
	}()
	for _, tool := range tools[:3] {
		<-tool.(*gatedTool).entered
	}
	// Settle out of call order: the results must still append in call order.
	close(releases[2])
	close(releases[1])
	close(releases[0])
	if err := <-done; err != nil {
		t.Fatalf("batch: %v", err)
	}

	var finished []string
	for _, e := range evs {
		if fin, ok := e.(types.ToolFinished); ok {
			finished = append(finished, fin.Result.ID)
		}
	}
	if strings.Join(finished, ",") != "c1,c2,c3" {
		t.Fatalf("finished order %v, want c1,c2,c3", finished)
	}
	var order []string
	for _, b := range env.msgs[len(env.msgs)-1].Blocks {
		if res, ok := b.(types.ToolResult); ok {
			order = append(order, res.ID)
		}
	}
	if strings.Join(order, ",") != "c1,c2,c3" {
		t.Fatalf("appended order %v, want c1,c2,c3", order)
	}
}

// gatedTool blocks in Call until release closes, so tests can observe the
// batch while it is still executing.
type gatedTool struct {
	spec    types.ToolSpec
	entered chan struct{}
	release <-chan struct{}
}

func (t *gatedTool) Spec() types.ToolSpec { return t.spec }

func (t *gatedTool) Call(context.Context, jsontext.Value) (types.ToolResult, error) {
	t.entered <- struct{}{}
	<-t.release
	return types.ToolResult{Outcome: types.Succeeded}, nil
}

// errTool fails every call with a fixed error.
type errTool struct {
	err error
}

func (t *errTool) Spec() types.ToolSpec { return types.ToolSpec{Name: "boom", Effect: types.ReadOnly} }

func (t *errTool) Call(context.Context, jsontext.Value) (types.ToolResult, error) {
	return types.ToolResult{}, t.err
}
