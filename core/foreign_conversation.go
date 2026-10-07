package gohan

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// resolvedForeign returns the runtime factory Build resolved for the
// foreign flow, or false when the flow has no foreign definition.
func (s *Stack) resolvedForeign(flow string) (func() runtime.Runtime, bool) {
	newRT, ok := s.foreign[flow]
	return newRT, ok
}

// resolveForeign resolves every registered foreign definition through the
// native pipeline, so a foreign flow's model, tools and assembler carry
// exactly the governance a native definition gets: model by profile name,
// strategies, fidelity, egress clients and chain ordering. A name already
// registered native, a missing runtime factory, or a duplicate name fails
// the build.
func (s *Stack) resolveForeign(defs []ForeignSpec, profiles map[string]types.ModelProfile, redactor types.Redactor, redactionPolicy types.RedactionPolicy) error {
	if len(defs) == 0 {
		return nil
	}
	natives := make([]NativeSpec, len(defs))
	for i, spec := range defs {
		if spec.NewRuntime == nil {
			return fmt.Errorf("flow %q: foreign definition needs a runtime factory", spec.Request.Name)
		}
		if _, clash := s.native[spec.Request.Name]; clash {
			return fmt.Errorf("flow %q: already registered", spec.Request.Name)
		}
		natives[i] = NativeSpec{
			Request:      spec.Request,
			Profile:      spec.Profile,
			Instruction:  spec.Instruction,
			Tools:        spec.Tools,
			ModelOptions: spec.ModelOptions,
			Assemble:     spec.Assemble,
		}
	}
	if err := s.resolveNative(natives, profiles, redactor, redactionPolicy); err != nil {
		return err
	}
	for _, spec := range defs {
		s.foreign[spec.Request.Name] = spec.NewRuntime
	}
	return nil
}

// foreignAgentRun builds one foreign run's governed wiring: a run-scoped
// ledger seeded from what the run already spent, the governed model and
// tools, the assembler over the resolved configuration and a fresh backend
// runtime. The caller owns history loading and the checkpoint sink.
func (s *Stack) foreignAgentRun(name string, hist stores.History, seed float64, input []types.Message) (context.Context, runtime.Runtime, runtime.AgentRun, *chains.LimitsState, error) {
	cfg, ok := s.resolvedNative(name)
	if !ok {
		return nil, nil, runtime.AgentRun{}, nil, fmt.Errorf("gohan: flow %q is not a registered foreign definition", name)
	}
	newRT, ok := s.resolvedForeign(name)
	if !ok {
		return nil, nil, runtime.AgentRun{}, nil, fmt.Errorf("gohan: flow %q is not a registered foreign definition", name)
	}
	ledger := chains.NewLimitsStateSeeded(seed, chains.WithCostTelemetry(s.telemetry))
	ctx := chains.WithLimitsState(context.Background(), ledger)
	ag := runtime.AgentRun{
		Model:    foreignModel(cfg, s),
		Tools:    foreignTools(cfg),
		Assemble: nativeAssemble(s.prompts, cfg.assemble, cfg.redactor, cfg.redactionPolicy, s.stores.RedactionMaps, newBlobAssembly(cfg.profile.Name, cfg.model, s.stores)),
		History:  hist,
		Input:    input,
	}
	return ctx, newRT(), ag, ledger, nil
}

// foreignModel wraps the selected model in the same model chain, stream
// supervision and cost recording the native effect path composes, so a
// backend call into Model runs governed: middleware, successor routing and
// the run ledger all apply.
func foreignModel(cfg *resolvedNativeConfig, s *Stack) types.Model {
	gen := runModelChain(cfg.modelChain, deprecatedRoute(s.telemetry, cfg.profile, NewModelStream(cfg.model).Generate, s.successorModelFunc(cfg.profile)))
	return modelFuncModel{profile: cfg.profile, gen: recordRunCost(gen)}
}

// modelFuncModel adapts a composed ModelFunc onto the Model port a
// runtime.AgentRun carries.
type modelFuncModel struct {
	profile types.ModelProfile
	gen     types.ModelFunc
}

func (m modelFuncModel) Profile() types.ModelProfile { return m.profile }

func (m modelFuncModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return m.gen(ctx, req)
}

// foreignTools wraps every declared tool so a backend call runs the
// resolved tool chain with the call's identity, the same governed path the
// native effect boundary uses.
func foreignTools(cfg *resolvedNativeConfig) []types.Tool {
	exec := governedToolExec(cfg.toolChain, nativeToolset(cfg.tools), specLookup(cfg.specs), cfg.clients)
	out := make([]types.Tool, len(cfg.tools))
	for i, t := range cfg.tools {
		out[i] = governedTool{tool: t, exec: exec}
	}
	return out
}

type governedTool struct {
	tool types.Tool
	exec func(ctx context.Context, call types.ToolUse) (types.ToolResult, error)
}

func (g governedTool) Spec() types.ToolSpec { return g.tool.Spec() }

func (g governedTool) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	return g.exec(ctx, types.ToolUse{Name: g.tool.Spec().Name, Args: args})
}

// NewForeignConversation builds the streaming conversation for a registered
// foreign definition over stack. Every run gets a fresh backend runtime, a
// run-scoped ledger and the governed model, tools and assembler Build
// resolved for the flow.
func NewForeignConversation(stack *Stack, name string, opts ...ConversationOption) (Conversation, error) {
	if stack == nil {
		return nil, fmt.Errorf("gohan: foreign conversation: no stack")
	}
	if _, ok := stack.resolvedForeign(name); !ok {
		return nil, fmt.Errorf("gohan: flow %q is not a registered foreign definition", name)
	}
	newRT, _ := stack.resolvedForeign(name)
	c := &conversation{spec: name, rt: newRT(), live: map[string]string{}}
	c.log = stack.stores.SessionLog
	c.cps = stack.stores.Checkpoints
	c.creds = stack.credentials
	c.policySrc = stack.approvalPolicy
	c.allowAnonymous = stack.allowAnonymous
	c.telemetry = stack.telemetry
	if l, ok := stack.Limits(name); ok {
		c.wall = l.MaxWallClock
		c.stall = l.ConsumerStall
	}
	cfg, ok := stack.resolvedNative(name)
	if !ok {
		return nil, fmt.Errorf("gohan: flow %q is not a registered foreign definition", name)
	}
	c.toolSpecs = specLookup(cfg.specs)
	c.verifier = stack.verifier
	c.journal = stack.stores.Journal
	c.outputs = stack.stores.OutputStore
	c.redactionMaps = stack.stores.RedactionMaps
	c.redactor = cfg.redactor
	c.redactionPolicy = cfg.redactionPolicy
	c.consolidator = stack.consolidation(name)
	c.consolidationAudit = stack.stores.Audit
	c.foreignRun = func(ctx context.Context, sessionID string, hist stores.History, seed float64, input []Message) (context.Context, runtime.Runtime, runtime.AgentRun, *chains.LimitsState, error) {
		if hist.Version == 0 {
			h, err := c.load(ctx, sessionID)
			if err != nil {
				return nil, nil, runtime.AgentRun{}, nil, err
			}
			hist = h
		}
		return stack.foreignAgentRun(name, hist, seed, input)
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.runs == nil {
		return nil, errConversationRuns
	}
	if c.detached && c.events == nil {
		return nil, errDetachedNoLog
	}
	if c.detached && stack.Notifier() == nil {
		stack.logger.Warn("detached flow has no notifier; notices stay in the runs outbox until a dispatcher delivers them", "flow", name)
	}
	if c.events == nil {
		return nil, errConversationEvents
	}
	return c, nil
}
