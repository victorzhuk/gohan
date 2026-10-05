package gohan

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/types"
)

// resolvedNativeConfig is the immutable resolution of one NativeSpec: the
// constructor reads this instead of resolving a profile or a strategy again.
type resolvedNativeConfig struct {
	flow        string
	model       types.Model
	profile     types.ModelProfile
	plan        StrategyPlan
	request     FlowRequest
	instruction []types.Block
	tools       []types.Tool
	specs       []types.ToolSpec
	assemble    func(ctx context.Context, in types.AssembleInput) (types.ModelRequest, error)
	modelChain  chains.ModelChain
	toolChain   chains.ToolChain
	limits      types.RunLimits
	decider     types.Decider[*permission.ToolInvocation, permission.Verdict]
}

// resolvedNative returns the resolved configuration Build computed for the
// flow, or false when no such definition was registered.
func (s *Stack) resolvedNative(flow string) (*resolvedNativeConfig, bool) {
	c, ok := s.native[flow]
	return c, ok
}

// resolveNative resolves every registered definition once: model by profile
// name, strategies, fidelity, then chain ordering. A rejected definition
// fails the build before any provider call happens.
func (s *Stack) resolveNative(defs []NativeSpec, profiles map[string]types.ModelProfile) error {
	resolved := make(map[string]*resolvedNativeConfig, len(defs))
	for _, spec := range defs {
		name := spec.Request.Name
		if _, dup := resolved[name]; dup {
			return fmt.Errorf("flow %q: already registered", name)
		}
		model, ok := modelForProfile(s.models, spec.Profile)
		if !ok {
			return fmt.Errorf("flow %q: profile %q not configured", name, spec.Profile)
		}
		req := spec.Request
		req.Tools = toolSpecs(spec.Tools)
		plan, err := s.ResolveStrategies(req, profiles, spec.Profile)
		if err != nil {
			return err
		}
		if err := s.CheckFidelity(req, profiles, spec.Profile); err != nil {
			return err
		}
		if err := chains.ValidateModelChain(spec.ModelChain); err != nil {
			return fmt.Errorf("flow %q: model chain: %w", name, err)
		}
		if err := chains.ValidateToolChain(spec.ToolChain); err != nil {
			return fmt.Errorf("flow %q: tool chain: %w", name, err)
		}
		limits, _ := s.Limits(name)
		resolved[name] = &resolvedNativeConfig{
			flow:        name,
			model:       model,
			profile:     profiles[spec.Profile],
			plan:        plan,
			request:     req,
			instruction: cloneBlocks(spec.Instruction),
			tools:       spec.Tools,
			specs:       req.Tools,
			assemble:    spec.Assemble,
			modelChain:  modelChainWithMiddleware(spec.ModelChain, s.middleware),
			toolChain:   spec.ToolChain,
			limits:      limits,
			decider:     spec.Decider,
		}
	}
	s.native = resolved
	return nil
}

// modelChainWithMiddleware appends the stack middleware as named KindUser
// steps after the registered chain, in option order, so the first entry
// stays the outermost of the middleware.
func modelChainWithMiddleware(ch chains.ModelChain, mw []types.ModelMiddleware) chains.ModelChain {
	if len(mw) == 0 {
		return ch
	}
	out := make(chains.ModelChain, 0, len(ch)+len(mw))
	out = append(out, ch...)
	for i, m := range mw {
		out = append(out, chains.Step[types.ModelMiddleware]{
			Name: fmt.Sprintf("model-middleware-%d", i+1),
			Kind: chains.KindUser,
			Use:  m,
		})
	}
	return out
}

func modelForProfile(models []types.Model, profile string) (types.Model, bool) {
	for _, m := range models {
		if m.Profile().Name == profile {
			return m, true
		}
	}
	return nil, false
}

func toolSpecs(tools []types.Tool) []types.ToolSpec {
	specs := make([]types.ToolSpec, len(tools))
	for i, t := range tools {
		specs[i] = t.Spec()
	}
	return specs
}

func cloneBlocks(blocks []types.Block) []types.Block {
	if blocks == nil {
		return nil
	}
	out := make([]types.Block, len(blocks))
	copy(out, blocks)
	return out
}
