package gohan

import (
	"context"
	"fmt"
	"net/http"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// resolvedNativeConfig is the immutable resolution of one NativeSpec: the
// constructor reads this instead of resolving a profile or a strategy again.
type resolvedNativeConfig struct {
	flow            string
	model           types.Model
	profile         types.ModelProfile
	plan            StrategyPlan
	request         FlowRequest
	instruction     []types.Block
	tools           []types.Tool
	specs           []types.ToolSpec
	clients         map[string]*http.Client
	assemble        func(ctx context.Context, in types.AssembleInput) (types.ModelRequest, error)
	modelChain      chains.ModelChain
	toolChain       chains.ToolChain
	limits          types.RunLimits
	decider         types.Decider[*permission.ToolInvocation, permission.Verdict]
	taintPolicy     types.TaintHook
	liveDeny        func(context.Context, *permission.ToolInvocation) (string, bool)
	verifier        types.Verifier
	redactor        types.Redactor
	redactionPolicy types.RedactionPolicy
	memoryPolicy    stores.MemoryPolicy
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
func (s *Stack) resolveNative(defs []NativeSpec, profiles map[string]types.ModelProfile, redactor types.Redactor, redactionPolicy types.RedactionPolicy) error {
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
		if err := checkSuccessorOptions(name, spec.ModelOptions, profiles[spec.Profile], profiles); err != nil {
			return err
		}
		req := spec.Request
		if key, ok := governedExtraKey(spec.ModelOptions.Extra); ok {
			return fmt.Errorf("flow %q: %w", name, types.ErrExtraGovernedKey{Key: key})
		}
		req.Tools = toolSpecs(spec.Tools)
		clients := make(map[string]*http.Client)
		for i := range req.Tools {
			toolSpec := req.Tools[i]
			if err := types.CheckEgress(toolSpec, toolSpec.Network); err != nil {
				return fmt.Errorf("flow %q: %w", name, err)
			}
			toolSpec.Capabilities.Exfil = types.DeriveExfil(toolSpec)
			req.Tools[i] = toolSpec
			if toolSpec.Egress != nil {
				if s.egressClient == nil {
					return fmt.Errorf("flow %q: %w", name, types.ErrEgressClientRequired{Name: toolSpec.Name})
				}
				clients[toolSpec.Name] = s.egressClient(*toolSpec.Egress)
			}
		}
		plan, err := s.ResolveStrategies(req, profiles, spec.Profile)
		if err != nil {
			return err
		}
		if err := s.CheckFidelity(req, profiles, spec.Profile); err != nil {
			return err
		}
		assemble := spec.Assemble
		if staticOptionsSet(spec.ModelOptions) {
			opts := spec.ModelOptions
			assemble = func(ctx context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				req, err := spec.Assemble(ctx, in)
				if err != nil {
					return types.ModelRequest{}, err
				}
				mergeStaticOptions(&req.Options, opts)
				return req, nil
			}
		}
		if err := chains.ValidateModelChain(spec.ModelChain); err != nil {
			return fmt.Errorf("flow %q: model chain: %w", name, err)
		}
		if err := chains.ValidateToolChain(spec.ToolChain); err != nil {
			return fmt.Errorf("flow %q: tool chain: %w", name, err)
		}
		if step, ok := cacheStep(spec.ModelChain); ok && !s.allowCache {
			if reason := cacheRefusal(req.Tools); reason != "" {
				return fmt.Errorf("flow %q: cache step %q: %s", name, step, reason)
			}
		}
		limits, _ := s.Limits(name)
		modelChain := modelChainWithMiddleware(spec.ModelChain, s.middleware)
		var exfil, privateRead, untrusted bool
		for _, toolSpec := range req.Tools {
			exfil = exfil || toolSpec.Capabilities.Exfil
			privateRead = privateRead || toolSpec.Capabilities.PrivateRead
			// Until provider assembly can declare trust, untrusted tools are the untrusted path.
			untrusted = untrusted || toolSpec.Trust == types.Untrusted
		}
		if s.telemetry != nil {
			state := int64(0)
			if exfil && privateRead && untrusted {
				state = 1
			}
			s.telemetry.Count(context.Background(), "gohan.build.trifecta", state, types.String("flow", name))
		}
		if exfil && privateRead && untrusted && spec.TaintPolicy == nil {
			return types.ErrTaintTrifecta{Flow: name, Exfil: true, PrivateRead: true, Untrusted: true}
		}
		verifier := spec.Verifier
		if verifier == nil {
			verifier = s.verifier
		}
		if spec.MemoryPolicy.Schema != nil || spec.MemoryPolicy.MaxEntries != 0 || spec.MemoryPolicy.TTL != 0 {
			if _, ok := s.stores.Notes.(stores.MemoryStore); !ok {
				return fmt.Errorf("flow %q: memory policy: %w", name, types.ErrMemoryStoreRequired)
			}
		}
		resolved[name] = &resolvedNativeConfig{
			flow:            name,
			model:           model,
			profile:         profiles[spec.Profile],
			plan:            plan,
			request:         req,
			instruction:     cloneBlocks(spec.Instruction),
			tools:           cloneTools(spec.Tools),
			specs:           req.Tools,
			clients:         clients,
			assemble:        assemble,
			modelChain:      cloneModelChain(modelChain),
			toolChain:       cloneToolChain(spec.ToolChain),
			limits:          limits,
			decider:         spec.Decider,
			taintPolicy:     spec.TaintPolicy,
			liveDeny:        spec.LiveDeny,
			verifier:        verifier,
			redactor:        redactor,
			redactionPolicy: redactionPolicy,
			memoryPolicy:    spec.MemoryPolicy,
		}
	}

	s.native = resolved
	return nil
}

// cacheStep names the first cache step of a chain: the outer slot the
// cache contract governs.
func cacheStep[M any](ch []chains.Step[M]) (string, bool) {
	for _, s := range ch {
		if s.Kind == chains.KindCache {
			return s.Name, true
		}
	}
	return "", false
}

// cacheRefusal names the rule a CacheStep trips. Every registered native
// definition constructs a conversation, so the conversation rule applies
// first; a registered tool with a non-read-only effect names itself.
func cacheRefusal(specs []types.ToolSpec) string {
	for _, spec := range specs {
		if spec.Effect != types.ReadOnly {
			return fmt.Sprintf("refused on tool %q with a non-read-only effect", spec.Name)
		}
	}
	return "refused on a conversation flow"
}

func governedExtraKey(extra map[string]any) (string, bool) {
	for _, key := range types.GovernedModelOptionKeys() {
		if _, ok := extra[key]; ok {
			return key, true
		}
	}
	return "", false
}

func staticOptionsSet(o types.ModelOptions) bool {
	return o.MaxTokens != 0 || o.Temperature != nil || o.ToolChoice != "" ||
		len(o.ResponseSchema) > 0 || o.Priority != 0 || o.AffinityKey != "" ||
		len(o.Extra) > 0
}

// mergeStaticOptions fills the assembled request's unset fields from the
// flow's static options; a field the assembler set wins.
func mergeStaticOptions(dst *types.ModelOptions, src types.ModelOptions) {
	if dst.MaxTokens == 0 {
		dst.MaxTokens = src.MaxTokens
	}
	if dst.Temperature == nil {
		dst.Temperature = src.Temperature
	}
	if dst.ToolChoice == "" {
		dst.ToolChoice = src.ToolChoice
	}
	if len(dst.ResponseSchema) == 0 {
		dst.ResponseSchema = src.ResponseSchema
	}
	if dst.Priority == 0 {
		dst.Priority = src.Priority
	}
	if dst.AffinityKey == "" {
		dst.AffinityKey = src.AffinityKey
	}
	for k, v := range src.Extra {
		if _, ok := dst.Extra[k]; !ok {
			if dst.Extra == nil {
				dst.Extra = make(map[string]any, len(src.Extra))
			}
			dst.Extra[k] = v
		}
	}
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

// cloneTools, cloneToolChain, and cloneModelChain give the resolved
// configuration its own slice containers so caller-side element mutation
// after Build cannot alter execution or Explain.
func cloneTools(tools []types.Tool) []types.Tool {
	if tools == nil {
		return nil
	}
	out := make([]types.Tool, len(tools))
	copy(out, tools)
	return out
}

func cloneToolChain(ch chains.ToolChain) chains.ToolChain {
	if ch == nil {
		return nil
	}
	out := make(chains.ToolChain, len(ch))
	copy(out, ch)
	return out
}

func cloneModelChain(ch chains.ModelChain) chains.ModelChain {
	if ch == nil {
		return nil
	}
	out := make(chains.ModelChain, len(ch))
	copy(out, ch)
	return out
}
