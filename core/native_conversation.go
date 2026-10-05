package gohan

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// NewNativeConversation builds the streaming conversation for a registered
// native definition over stack. The name must resolve to a definition Build
// registered; an unknown name is refused before any invocation. Every run
// gets its own runtime instance and its own accounting ledger, so
// concurrent sessions share neither runtime state nor spend.
func NewNativeConversation(stack *Stack, name string, opts ...ConversationOption) (Conversation, error) {
	if stack == nil {
		return nil, fmt.Errorf("gohan: native conversation: no stack")
	}
	cfg, ok := stack.resolvedNative(name)
	if !ok {
		return nil, fmt.Errorf("gohan: flow %q is not a registered native definition", name)
	}
	c := &conversation{spec: name, live: map[string]string{}}
	c.log = stack.stores.SessionLog
	c.cps = stack.stores.Checkpoints
	c.creds = stack.credentials
	c.policySrc = stack.approvalPolicy
	c.allowAnonymous = stack.allowAnonymous
	if l, ok := stack.Limits(name); ok {
		c.wall = l.MaxWallClock
	}
	c.toolSpecs = specLookup(cfg.specs)
	c.newRun = func(ctx context.Context, sessionID string, lease stores.Lease, input []Message) (context.Context, runtime.AgentRun, error) {
		ledger := chains.NewLimitsState()
		ctx = chains.WithLimitsState(ctx, ledger)

		hist, err := c.load(ctx, sessionID)
		if err != nil {
			return ctx, runtime.AgentRun{}, err
		}

		tc := stack.nativeTurnConfig(cfg)
		tc.gate = nativeBatchGate(cfg, hist)
		tc.reserve = func(ctx context.Context, n int) (context.Context, func(), error) {
			return ledger.ReserveBatch(ctx, cfg.limits, n)
		}

		ag := nativeRun(tc, hist.Messages, nil)
		ag.Model = cfg.model
		ag.Tools = cfg.tools
		ag.Assemble = tc.assemble
		ag.History = hist
		ag.Input = input
		return ctx, ag, nil
	}
	c.newResumeRun = func(ctx context.Context, sessionID string, hist stores.History, seed float64) (context.Context, runtime.Runtime, runtime.AgentRun, *chains.LimitsState, error) {
		ledger := chains.NewLimitsStateSeeded(seed)
		ctx = chains.WithLimitsState(ctx, ledger)

		tc := stack.nativeTurnConfig(cfg)
		tc.gate = nativeBatchGate(cfg, hist)
		tc.reserve = func(ctx context.Context, n int) (context.Context, func(), error) {
			return ledger.ReserveBatch(ctx, cfg.limits, n)
		}

		ag := nativeRun(tc, hist.Messages, nil)
		ag.Model = cfg.model
		ag.Tools = cfg.tools
		ag.Assemble = tc.assemble
		ag.History = hist
		return ctx, runtime.NewNative(), ag, ledger, nil
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
	if c.events == nil {
		return nil, errConversationEvents
	}
	return c, nil
}

// nativeBatchGate wires the permission seam into the batch gate: every call
// is decided before the first one executes, an unknown tool denies, and the
// verdict projects onto the batch protocol's outcomes. A nil decider keeps
// the core default — read-only and idempotent calls pass, everything else
// asks; a decider error asks rather than widening permission.
func nativeBatchGate(cfg *resolvedNativeConfig, hist stores.History) runtime.BatchGate {
	lookup := specLookup(cfg.specs)
	grant := receiptGrantCheck(hist)
	return func(ctx context.Context, call types.ToolUse) runtime.BatchDecision {
		spec, ok := lookup(call.Name)
		if !ok {
			return runtime.BatchDecision{Outcome: runtime.BatchDeny, Reason: "unknown tool " + call.Name}
		}
		run, _ := RunInfoFrom(ctx)
		inv := &permission.ToolInvocation{Spec: spec, Call: call, Run: run}
		dec := permission.DecideCall(ctx, cfg.decider, inv, permission.WithGrantCheck(grant))
		switch dec.Verdict {
		case permission.Allow:
			return runtime.BatchDecision{Outcome: runtime.BatchAllow}
		case permission.DenyVerdict:
			return runtime.BatchDecision{Outcome: runtime.BatchDeny, Reason: dec.Reason}
		case permission.TaintDenied:
			return runtime.BatchDecision{Outcome: runtime.BatchTaintDenied, Reason: dec.Reason}
		default:
			return runtime.BatchDecision{Outcome: runtime.BatchAsk}
		}
	}
}

func specLookup(specs []types.ToolSpec) func(string) (types.ToolSpec, bool) {
	byName := make(map[string]types.ToolSpec, len(specs))
	for _, s := range specs {
		byName[s.Name] = s
	}
	return func(name string) (types.ToolSpec, bool) {
		s, ok := byName[name]
		return s, ok
	}
}
