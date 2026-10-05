package gohan

import (
	"context"
	"embed"
	"encoding/json"
	"iter"
	"math"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

//go:embed performance_baselines.json
var baselineFS embed.FS

// rawToolCall is the innermost call every tool-chain measurement wraps: a
// read-only tool that returns a fixed result. The chain-overhead budget is
// the delta between this and the same call under the chain.
func rawToolCall(_ context.Context, _ types.ToolUse) (types.ToolResult, error) {
	return types.ToolResult{ID: "bench"}, nil
}

// rawModelCall is the innermost model invocation: one chunk, no error.
func rawModelCall(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: "ok"}, nil)
	}
}

// readOnlyToolChain is the minimal chain a read-only tool call runs under:
// the limits step the driver installs and one pass-through guard step.
func readOnlyToolChain(st *chains.LimitsState) chains.ToolChain {
	return chains.ToolChain{
		{Name: "limits", Kind: chains.KindLimit, Use: chains.ToolLimits(types.RunLimits{MaxToolCalls: math.MaxInt64, MaxWallClock: time.Hour}, st)},
		{Name: "guard", Kind: chains.KindGuard, Use: nil},
	}
}

// composeModel mirrors the tool-side composition of RunToolChain for the
// model chain: step 0 outermost, the scripted call innermost.
func composeModel(ch chains.ModelChain, next types.ModelFunc) types.ModelFunc {
	for i := len(ch) - 1; i >= 0; i-- {
		step := ch[i]
		use := step.Use
		inner := next
		next = func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			if use == nil {
				return inner(ctx, req)
			}
			return use(inner)(ctx, req)
		}
	}
	return next
}

// modelChainForBench is the canonical model-side order minus the I/O steps
// the budget excludes: limits, hooks and one pass-through telemetry step.
func modelChainForBench(st *chains.LimitsState) chains.ModelChain {
	return chains.ModelChain{
		{Name: "hooks", Kind: chains.KindHooks, Use: nil},
		{Name: "limits", Kind: chains.KindLimit, Use: chains.Limits(types.RunLimits{MaxCost: 1e12, MaxWallClock: time.Hour}, types.Pricing{}, st)},
	}
}

var (
	benchResult types.ToolResult
	benchChunks int
)

func BenchmarkToolCall_Raw(b *testing.B) {
	ctx := context.Background()
	for range 3 {
		_, _ = rawToolCall(ctx, types.ToolUse{})
	}
	b.ReportAllocs()
	for b.Loop() {
		res, err := rawToolCall(ctx, types.ToolUse{})
		if err != nil {
			b.Fatal(err)
		}
		benchResult = res
	}
}

func BenchmarkToolChain_ReadOnly(b *testing.B) {
	ctx := context.Background()
	ch := readOnlyToolChain(chains.NewLimitsState())
	if err := chains.ValidateToolChain(ch); err != nil {
		b.Fatal(err)
	}
	for range 3 {
		_, _ = chains.RunToolChain(ctx, ch, rawToolCall)
	}
	b.ReportAllocs()
	for b.Loop() {
		res, err := chains.RunToolChain(ctx, ch, rawToolCall)
		if err != nil {
			b.Fatal(err)
		}
		benchResult = res
	}
}

func BenchmarkModelChain(b *testing.B) {
	ctx := context.Background()
	ch := modelChainForBench(chains.NewLimitsState())
	if err := chains.ValidateModelChain(ch); err != nil {
		b.Fatal(err)
	}
	call := composeModel(ch, rawModelCall)
	req := types.ModelRequest{}
	for range 3 {
		for _, err := range call(ctx, req) {
			_ = err
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		for chunk, err := range call(ctx, req) {
			if err != nil {
				b.Fatal(err)
			}
			if chunk.Delta != "" {
				benchChunks++
			}
		}
	}
}

func BenchmarkModelCall_Raw(b *testing.B) {
	ctx := context.Background()
	req := types.ModelRequest{}
	for range 3 {
		for _, err := range rawModelCall(ctx, req) {
			_ = err
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		for chunk, err := range rawModelCall(ctx, req) {
			if err != nil {
				b.Fatal(err)
			}
			if chunk.Delta != "" {
				benchChunks++
			}
		}
	}
}

type perfBaselines struct {
	RunnerClass struct {
		Name string `json:"name"`
		Note string `json:"note"`
	} `json:"runner_class"`
	ToolChain struct {
		ChainAllocsPerOp int `json:"chain_allocs_per_op"`
		RawAllocsPerOp   int `json:"raw_allocs_per_op"`
		OverheadAllocs   int `json:"overhead_allocs_per_op"`
	} `json:"tool_chain_read_only"`
	ModelChain struct {
		AllocsPerOp int `json:"allocs_per_op"`
	} `json:"model_chain"`
}

func loadBaselines(t testing.TB) perfBaselines {
	t.Helper()
	raw, err := baselineFS.ReadFile("performance_baselines.json")
	if err != nil {
		t.Fatalf("read baselines: %v", err)
	}
	var out perfBaselines
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse baselines: %v", err)
	}
	return out
}

// TestChainPerformanceBudget holds the machine-independent part of the
// performance contract: allocation counts. The absolute wall-clock budgets
// (≤ 20 µs tool-chain overhead, ≤ 50 µs model-chain overhead) belong to the
// reference runner gate on ubuntu-latest; locally they are advisory only.
func TestChainPerformanceBudget(t *testing.T) {
	t.Run("performance.chain-overhead-within-budget", func(t *testing.T) {
		bl := loadBaselines(t)
		ctx := context.Background()
		const samples = 1000
		rawAllocs := testing.AllocsPerRun(samples, func() {
			_, _ = rawToolCall(ctx, types.ToolUse{})
		})
		st := chains.NewLimitsState()
		ch := readOnlyToolChain(st)
		chainAllocs := testing.AllocsPerRun(samples, func() {
			_, _ = chains.RunToolChain(ctx, ch, rawToolCall)
		})
		mch := modelChainForBench(chains.NewLimitsState())
		call := composeModel(mch, rawModelCall)
		req := types.ModelRequest{}
		modelAllocs := testing.AllocsPerRun(samples, func() {
			for _, err := range call(ctx, req) {
				_ = err
			}
		})

		if overhead := int(chainAllocs) - int(rawAllocs); overhead > bl.ToolChain.OverheadAllocs {
			t.Errorf("tool-chain overhead %d allocs > frozen budget %d (chain %v vs raw %v)", overhead, bl.ToolChain.OverheadAllocs, chainAllocs, rawAllocs)
		}
		if total := int(modelAllocs); total > bl.ModelChain.AllocsPerOp {
			t.Errorf("model-chain %d allocs/op > frozen budget %d", total, bl.ModelChain.AllocsPerOp)
		}

		if !testing.Short() {
			start := time.Now()
			const wallSamples = 100000
			for range wallSamples {
				_, _ = chains.RunToolChain(ctx, ch, rawToolCall)
			}
			chainPerOp := time.Since(start) / wallSamples
			if chainPerOp > 20*time.Microsecond {
				t.Logf("ADVISORY: tool-chain per-op %v exceeds the 20µs reference-runner budget; local runs never fail", chainPerOp)
			}
		}
	})
}
