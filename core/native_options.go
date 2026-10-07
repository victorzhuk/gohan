package gohan

import (
	"context"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// NativeSpec is the driver-supplied definition of one native flow: what it
// requests at build time, which profile it runs on, and the chains it
// executes. The flow name is Request.Name. The tool list has one source:
// Request.Tools is derived from Tools during resolution, never supplied
// independently.
type NativeSpec struct {
	Request      FlowRequest
	Profile      string
	Instruction  []types.Block
	Tools        []types.Tool
	ModelOptions types.ModelOptions
	Assemble     func(ctx context.Context, in types.AssembleInput) (types.ModelRequest, error)
	ModelChain   chains.ModelChain
	ToolChain    chains.ToolChain
	// TaintPolicy declares this flow's taint hook. A nil hook disables taint enforcement.
	TaintPolicy types.TaintHook
	// Verifier reconciles this flow's Unknown journal entries at the
	// verification triggers. A nil verifier falls back to the stack-level
	// one; a nil stack-level one leaves unknown keys to the uncertainty set.
	Verifier types.Verifier
	// LiveControl declares this flow's live-control check: it can deny,
	// force Ask or allow, and suspends on a stale or default evaluation.
	// A nil check adds no live control.
	LiveControl func(ctx context.Context, inv *permission.ToolInvocation) (types.TaintAction, string)
	// MemoryPolicy declares this flow's subject-memory policy. A zero
	// value declares no subject memory: the flow never consolidates.
	MemoryPolicy stores.MemoryPolicy
	// ContextPolicy declares this flow's context projection policy. A zero
	// value keeps the flow on the plain assembly path: no projection runs
	// and no compactor is consulted.
	ContextPolicy stores.ContextPolicy
	// Decider states the flow's own tool policy. The batch gate consults
	// it before any call executes; a nil Decider keeps the core defaults
	// (read-only and idempotent calls pass, side effects ask).
	Decider types.Decider[*permission.ToolInvocation, permission.Verdict]
}

// WithNativeAgent registers a native flow definition before Build runs, so
// Build resolves it with everything else. Registering two definitions with
// the same flow name fails the build.
func WithNativeAgent(spec NativeSpec) Option {
	return func(c *config) error {
		c.native = append(c.native, spec)
		return nil
	}
}

// ContextPolicy returns the context policy the flow resolved with, and
// whether the flow is a registered native definition.
func (s *Stack) ContextPolicy(flow string) (stores.ContextPolicy, bool) {
	cfg, ok := s.resolvedNative(flow)
	if !ok {
		return stores.ContextPolicy{}, false
	}
	return cfg.contextPolicy, true
}

// ForeignSpec is the driver-supplied definition of one foreign-runtime
// flow: the backend runtime that drives its turns, and everything Build
// resolves for it exactly as it would for a native definition. The flow
// name is Request.Name. NewRuntime runs once per run, so concurrent
// sessions share no backend state.
type ForeignSpec struct {
	Request      FlowRequest
	Profile      string
	NewRuntime   func() runtime.Runtime
	Instruction  []types.Block
	Tools        []types.Tool
	ModelOptions types.ModelOptions
	Assemble     func(ctx context.Context, in types.AssembleInput) (types.ModelRequest, error)
}

// WithForeignAgent registers a foreign-runtime flow definition before Build
// runs, so Build resolves it with everything else. Registering two
// definitions with the same flow name, native or foreign, fails the build.
func WithForeignAgent(spec ForeignSpec) Option {
	return func(c *config) error {
		c.foreign = append(c.foreign, spec)
		return nil
	}
}
