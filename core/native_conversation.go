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
// registered; an unknown name is refused before any invocation. The
// constructor binds the stack itself — shutdown admission, tenant, title
// profile and the frozen flag snapshot — before the option loop, so every
// run admits against the same stack. Every run gets its own runtime
// instance and its own accounting ledger, so
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
	c.bindStack(stack)
	c.log = stack.stores.SessionLog
	c.cps = stack.stores.Checkpoints
	c.creds = stack.credentials
	c.policySrc = stack.approvalPolicy
	c.approvalGrants = stack.approvalGrants
	c.allowAnonymous = stack.allowAnonymous
	c.telemetry = stack.telemetry
	if l, ok := stack.Limits(name); ok {
		c.wall = l.MaxWallClock
		c.stall = l.ConsumerStall
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
	c.evaluator = stack.evaluator
	c.newRun = func(ctx context.Context, sessionID string, lease stores.Lease, input []Message) (context.Context, runtime.AgentRun, error) {
		ledger := chains.NewLimitsState(chains.WithCostTelemetry(c.telemetry))
		ctx = chains.WithLimitsState(ctx, ledger)

		hist, err := c.load(ctx, sessionID)
		if err != nil {
			return ctx, runtime.AgentRun{}, err
		}

		tc := stack.nativeTurnConfig(cfg)
		tc.gate = nativeBatchGate(cfg, hist, c.approvalGrants)
		restored := false
		tc.reserve = func(ctx context.Context, n int) (context.Context, func(), error) {
			if !restored {
				restored = true
				if u, ok := admittedToolTotalFrom(ctx); ok && u > 0 {
					if _, _, err := ledger.ReserveBatch(ctx, types.RunLimits{}, u); err != nil {
						return ctx, func() {}, err
					}
				}
			}
			if n == 0 {
				return ctx, func() {}, nil
			}
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
		ledger := chains.NewLimitsStateSeeded(seed, chains.WithCostTelemetry(c.telemetry))
		ctx = chains.WithLimitsState(ctx, ledger)

		tc := stack.nativeTurnConfig(cfg)
		tc.gate = nativeBatchGate(cfg, hist, c.approvalGrants)
		restored := false
		tc.reserve = func(ctx context.Context, n int) (context.Context, func(), error) {
			if !restored {
				restored = true
				if u, ok := admittedToolTotalFrom(ctx); ok && u > 0 {
					if _, _, err := ledger.ReserveBatch(ctx, types.RunLimits{}, u); err != nil {
						return ctx, func() {}, err
					}
				}
			}
			if n == 0 {
				return ctx, func() {}, nil
			}
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
	if c.detached && stack.Notifier() == nil {
		stack.logger.Warn("detached flow has no notifier; notices stay in the runs outbox until a dispatcher delivers them", "flow", name)
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
func nativeBatchGate(cfg *resolvedNativeConfig, hist stores.History, grants ...permission.GrantStore) runtime.BatchGate {
	lookup := specLookup(cfg.specs)
	grant := receiptGrantCheck(hist)
	var scoped []func(context.Context, *permission.ToolInvocation) bool
	for _, g := range grants {
		if g != nil {
			scoped = append(scoped, scopeGrantCheck(g))
		}
	}
	return func(ctx context.Context, call types.ToolUse) runtime.BatchDecision {
		spec, ok := lookup(call.Name)
		if !ok {
			return runtime.BatchDecision{Outcome: runtime.BatchDeny, Reason: "unknown tool " + call.Name}
		}
		run, _ := RunInfoFrom(ctx)
		inv := &permission.ToolInvocation{Spec: spec, Call: call, Run: run}
		granted := func(ctx context.Context, candidate *permission.ToolInvocation) bool {
			if grant(ctx, candidate) {
				return true
			}
			for _, check := range scoped {
				if check(ctx, candidate) {
					return true
				}
			}
			return false
		}
		opts := []permission.Option{permission.WithGrantCheck(granted), permission.WithControlWait(cfg.controlWait)}
		if cfg.taintPolicy != nil {
			opts = append(opts, permission.WithTaintHook(cfg.taintPolicy))
		}
		if cfg.liveControl != nil {
			opts = append(opts, permission.WithLiveControl(cfg.liveControl))
		}
		dec := permission.DecideCall(ctx, cfg.decider, inv, opts...)
		switch dec.Verdict {
		case permission.Allow:
			return runtime.BatchDecision{Outcome: runtime.BatchAllow}
		case permission.DenyVerdict:
			return runtime.BatchDecision{Outcome: runtime.BatchDeny, Reason: dec.Reason}
		case permission.TaintDenied:
			return runtime.BatchDecision{Outcome: runtime.BatchTaintDenied, Reason: dec.Reason}
		case permission.ControlWait:
			return runtime.BatchDecision{Outcome: runtime.BatchAsk, Reason: permission.ControlWaitReason}
		default:
			return runtime.BatchDecision{Outcome: runtime.BatchAsk, Taints: dec.Taints}
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
