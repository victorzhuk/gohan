package gohan

import (
	"context"
	"fmt"
	"sort"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

// explainHandle is the identity a native handle carries into Explain: the
// flow name of the resolved configuration it was constructed from.
type explainHandle interface{ explainFlow() string }

// explainFlow identifies the conversation by the definition Build resolved
// for it, so Explain reads the configuration execution reads.
func (c *conversation) explainFlow() string { return c.spec }

// Explain projects the resolved configuration of one native flow: the
// selected model profile, the strategy plan, the named chain steps, the
// tools, the final prompt set, and the limits. It resolves exactly the
// configuration execution resolves, invokes no provider, and spends no
// run budget.
func (s *Stack) Explain(f any) chains.Explanation {
	flow, ok := explainFlowName(f)
	if !ok {
		return chains.Explanation{}
	}
	cfg, ok := s.resolvedNative(flow)
	if !ok {
		return chains.Explanation{}
	}
	granularity := "effect"
	if newRT, foreign := s.resolvedForeign(flow); foreign {
		// A foreign flow reports its runtime's step granularity, not the
		// native effect boundary.
		if newRT().Granularity() == runtime.GranularityTurn {
			granularity = "turn"
		}
	}
	ex := chains.Explanation{
		Flow:      cfg.flow,
		Profile:   cfg.profile.Name,
		Steps:     stepInfos(cfg.modelChain, cfg.specs),
		ToolSteps: stepInfos(cfg.toolChain, cfg.specs),
		Strategies: chains.StrategyInfo{
			Structured:       entryName(cfg.plan.Structured),
			ParallelTools:    cfg.plan.ParallelTools,
			MaxParallelTools: cfg.plan.MaxParallelTools,
		},
		Fallback: cfg.request.Fallback,
		Limits:   cfg.limits,
		// The native runtime steps at effect boundaries.
		Granularity: granularity,
		Prompts:     chains.PromptFields(s.prompts),
		Skills:      map[string]string{},
		Release:     s.manifest.ID(),
	}
	if step, ok := cacheStep(cfg.modelChain); ok && s.allowCache {
		ex.Warnings = append(ex.Warnings,
			fmt.Sprintf("flow %q: cache step %q runs under AllowCache: the conversation and read-only refusals are waived", cfg.flow, step))
	}
	ex.Tools = toolInfos(cfg.specs)
	if req, err := s.nativeTurnConfig(cfg).assemble(context.Background(), types.AssembleInput{}); err == nil {
		ex.Sample = req
	}
	return ex
}

func explainFlowName(f any) (string, bool) {
	switch h := f.(type) {
	case string:
		return h, true
	case explainHandle:
		return h.explainFlow(), true
	}
	return "", false
}

// toolInfos projects the registered specs onto Explain's tool list,
// ordered by name so the projection is deterministic.
func toolInfos(specs []types.ToolSpec) []chains.ToolInfo {
	out := make([]chains.ToolInfo, 0, len(specs))
	for _, spec := range specs {
		out = append(out, chains.ToolInfo{Name: spec.Name, Executor: spec.Executor})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func stepInfos[M any](ch []chains.Step[M], specs []types.ToolSpec) []chains.StepInfo {
	out := make([]chains.StepInfo, 0, len(ch))
	for _, st := range ch {
		out = append(out, chains.StepInfo{
			Name:    st.Name,
			Kind:    st.Kind,
			Applies: appliesNames(st.Applies, specs),
		})
	}
	return out
}

// appliesNames evaluates one step's Applies predicate against the
// registered tools. A nil predicate governs every registered tool.
func appliesNames(applies func(types.ToolSpec) bool, specs []types.ToolSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		if applies == nil || applies(spec) {
			names = append(names, spec.Name)
		}
	}
	sort.Strings(names)
	return names
}
