package gohan

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

// nativeTurnConfig binds one resolved native configuration into the per-turn
// wiring the effects consume: the composed model chain around the selected
// model, the assembler with the resolved PromptSet as the single system
// insertion point, the tool registry, the governed call path through the
// resolved tool chain, the run limits and the batch scheduler strategy.
// The approval gate and the batch reservation hook stay unwired: the
// conversation wiring supplies both per run.
func (s *Stack) nativeTurnConfig(cfg *resolvedNativeConfig) turnConfig {
	toolset := nativeToolset(cfg.tools)
	return turnConfig{
		model:    runModelChain(cfg.modelChain, cfg.model.Generate),
		assemble: nativeAssemble(s.prompts, cfg.assemble),
		tools:    toolset,
		maxTurns: cfg.limits.MaxTurns,
		limits:   cfg.limits,
		scheduler: runtime.SchedulerConfig{
			EffectOf:    nativeEffectOf(cfg.tools),
			Parallel:    cfg.plan.ParallelTools,
			MaxParallel: cfg.plan.MaxParallelTools,
		},
		exec: governedToolExec(cfg.toolChain, toolset),
	}
}

// nativeRun creates one run's effect pair over the bound configuration. The
// two callbacks share the per-run scope, so a turn's pending batch crosses
// from the model effect into the batch effect the way the runtime
// alternates the phases.
func nativeRun(c turnConfig, history, input []types.Message) runtime.AgentRun {
	env := &turnEnv{c: c, msgs: slices.Clone(history)}
	if len(input) > 0 {
		env.msgs = append(env.msgs, input...)
	}
	return runtime.AgentRun{
		Input: input,
		ModelEffect: func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
			return modelEffect(env.effectCtx(ctx), st)
		},
		BatchEffect: func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
			return batchEffect(env.effectCtx(ctx), st)
		},
	}
}

// effectCtx installs the per-run scope the effects share. The lifecycle
// steps one effect at a time, so the lazy sink lookup never races.
func (env *turnEnv) effectCtx(ctx context.Context) context.Context {
	if env.sink == nil {
		env.sink, _ = types.SinkFrom(ctx)
	}
	return withTurnEnv(ctx, env)
}

// runModelChain composes the chain around the model: step 0 outermost, the
// model innermost. A step panic becomes a StepError, matching the tool side.
func runModelChain(ch chains.ModelChain, next types.ModelFunc) types.ModelFunc {
	for i := len(ch) - 1; i >= 0; i-- {
		step := ch[i]
		use := step.Use
		inner := next
		next = func(ctx context.Context, req types.ModelRequest) (out iter.Seq2[types.ModelChunk, error]) {
			defer func() {
				r := recover()
				if r == nil {
					return
				}
				var err error
				if e, isErr := r.(error); isErr {
					err = &chains.StepError{Step: step.Name, Err: e}
				} else {
					err = &chains.StepError{Step: step.Name, Err: fmt.Errorf("%v", r)}
				}
				out = func(yield func(types.ModelChunk, error) bool) {
					yield(types.ModelChunk{}, err)
				}
			}()
			if use == nil {
				return inner(ctx, req)
			}
			return use(inner)(ctx, req)
		}
	}
	return next
}

// governedToolExec runs one call through the resolved tool chain with the
// call's original identity: the chain steps and CallTool never see a zero
// ToolUse.
func governedToolExec(ch chains.ToolChain, set ToolSet) func(context.Context, types.ToolUse) (types.ToolResult, error) {
	return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
		next := func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			return CallTool(ctx, set, call.Name, call.Args)
		}
		for i := len(ch) - 1; i >= 0; i-- {
			step := ch[i]
			use := step.Use
			inner := next
			next = func(ctx context.Context, call types.ToolUse) (res types.ToolResult, err error) {
				defer func() {
					r := recover()
					if r == nil {
						return
					}
					if e, isErr := r.(error); isErr {
						err = &chains.StepError{Step: step.Name, Err: e}
						return
					}
					err = &chains.StepError{Step: step.Name, Err: fmt.Errorf("%v", r)}
				}()
				if use == nil {
					return inner(ctx, call)
				}
				return use(inner)(ctx, call)
			}
		}
		return next(ctx, call)
	}
}

// nativeAssemble applies the resolved assembler and prepends the PromptSet
// block. This is the single prompt insertion point of the native path; a
// set without one model-facing string inserts nothing.
func nativeAssemble(ps chains.PromptSet, assemble func(context.Context, types.AssembleInput) (types.ModelRequest, error)) func(context.Context, types.AssembleInput) (types.ModelRequest, error) {
	return func(ctx context.Context, in types.AssembleInput) (types.ModelRequest, error) {
		req, err := assemble(ctx, in)
		if err != nil {
			return types.ModelRequest{}, err
		}
		if block, ok := promptBlock(ps); ok {
			req.System = append([]types.Block{block}, req.System...)
		}
		return req, nil
	}
}

// promptBlock renders the model-facing PromptSet strings as one system
// block. Version names the set for hashing, not the model, and stays out.
func promptBlock(ps chains.PromptSet) (types.Text, bool) {
	facing := []string{
		ps.FenceOpen,
		ps.FenceClose,
		ps.DataNotInstructions,
		ps.OutcomeUnknown,
		ps.ReadBackHint,
		ps.OutputRefHint,
		ps.RepairInstruction,
		ps.NotesPreamble,
		ps.OperatorTurn,
	}
	parts := make([]string, 0, len(facing))
	for _, s := range facing {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return types.Text{}, false
	}
	return types.Text{
		BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginSystem}},
		Text:      strings.Join(parts, "\n"),
	}, true
}

func nativeToolset(tools []types.Tool) ToolSetFunc {
	return func(name string) (types.Tool, bool) {
		for _, t := range tools {
			if t.Spec().Name == name {
				return t, true
			}
		}
		return nil, false
	}
}

// nativeEffectOf resolves a call's effect from the registered tools. An
// unknown name resolves to the zero effect, so the scheduler keeps it
// sequential.
func nativeEffectOf(tools []types.Tool) func(types.ToolUse) types.Effect {
	effects := make(map[string]types.Effect, len(tools))
	for _, t := range tools {
		effects[t.Spec().Name] = t.Spec().Effect
	}
	return func(call types.ToolUse) types.Effect {
		return effects[call.Name]
	}
}
