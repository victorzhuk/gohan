package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"

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
			Request:       spec.Request,
			Profile:       spec.Profile,
			Instruction:   spec.Instruction,
			Tools:         spec.Tools,
			ModelOptions:  spec.ModelOptions,
			Assemble:      spec.Assemble,
			ToolChain:     spec.ToolChain,
			Decider:       spec.Decider,
			ContextPolicy: spec.ContextPolicy,
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
// runtime. Every tool call the backend makes runs the shared per-call path
// the native batch effect uses: the approval gate over the resolved
// configuration and history, one ReserveBatch slot on the run ledger, and
// the composed tool chain as the only ordinary-tool journal wrapper. A
// backend call that binds no call id fails closed before the gate, the
// reservation, the journal and the tool run. The run context derives from
// the caller's ctx, so the caller's principal, credential, run info,
// deadline and cancellation reach every component call. The caller owns
// history loading and the checkpoint sink.
func (s *Stack) foreignAgentRun(ctx context.Context, sessionID string, name string, hist stores.History, seed float64, input []types.Message) (context.Context, runtime.Runtime, runtime.AgentRun, *chains.LimitsState, error) {
	cfg, ok := s.resolvedNative(name)
	if !ok {
		return nil, nil, runtime.AgentRun{}, nil, fmt.Errorf("gohan: flow %q is not a registered foreign definition", name)
	}
	newRT, ok := s.resolvedForeign(name)
	if !ok {
		return nil, nil, runtime.AgentRun{}, nil, fmt.Errorf("gohan: flow %q is not a registered foreign definition", name)
	}
	ledger := chains.NewLimitsStateSeeded(seed, chains.WithCostTelemetry(s.telemetry))
	ctx = chains.WithLimitsState(ctx, ledger)
	ctx = withHistoryVersion(ctx, hist.Version)
	// The gate, the journal keys and the approval receipts key on the
	// session: a run whose caller context carries no session id adopts the
	// one the conversation resolved.
	if run, ok := types.RunInfoFrom(ctx); !ok {
		ctx = types.WithRunInfo(ctx, types.RunInfo{SessionID: sessionID})
	} else if run.SessionID == "" {
		run.SessionID = sessionID
		ctx = types.WithRunInfo(ctx, run)
	}
	ri, _ := types.RunInfoFrom(ctx)
	fs := newForeignRunState(ri.RunID, hist.Version)
	if p, ok := foreignProgressFrom(ctx); ok {
		if err := restoreForeignRun(fs, p); err != nil {
			return nil, nil, runtime.AgentRun{}, nil, err
		}
	}
	if err := scanForeignAdmissions(fs, hist); err != nil {
		return nil, nil, runtime.AgentRun{}, nil, err
	}
	gate := nativeBatchGate(cfg, hist, s.approvalGrants)
	env := &turnEnv{}
	// The compaction boundary binds before the runtime starts: the stage
	// and the reconciliation reload the durable session themselves, so a
	// backend Generate never depends on an appender installed after
	// rt.Start. This env stays separate from the tool-ask env above,
	// whose zero config the governed ask path relies on.
	tc := s.nativeTurnConfig(cfg)
	if s.stores.SessionLog == nil {
		tc.compactionLoad = nil
	}
	cenv := &turnEnv{c: tc, msgs: slices.Clone(hist.Messages)}
	cst := &runtime.State{HistoryVersion: hist.Version}
	// Admission is identity-aware. A cumulative restore happens once and
	// its reservation context is discarded: the restored charge must never
	// become the first tool's prepaid slot. An identity this run already
	// admitted replays on a prepaid slot without charging the budget
	// again. A new identity reserves exactly one slot under the flow
	// limits and appends its durable admission record before the gate; on
	// append failure the slot refunds and the storage error propagates
	// before authorization or execution.
	restored := false
	reserve := func(ctx context.Context, n int) (context.Context, func(), error) {
		call, ok := types.ToolUseFrom(ctx)
		if !ok || call.ID == "" {
			return ctx, func() {}, types.ErrForeignCallID
		}
		if fs.admitted(call.ID) {
			return ledger.ReplayReservation(ctx, 1), func() {}, nil
		}
		if !restored {
			restored = true
			if total := fs.totalCalls(); total > 0 {
				if _, _, err := ledger.ReserveBatch(ctx, types.RunLimits{}, total); err != nil {
					return ctx, func() {}, err
				}
			}
		}
		rctx, refund, err := ledger.ReserveBatch(ctx, cfg.limits, 1)
		if err != nil {
			return ctx, func() {}, err
		}
		rec := foreignAdmission{RunID: fs.runID, AdmittedTotal: fs.totalCalls() + 1, CallID: call.ID}
		msg, merr := foreignAdmissionMessage(rec)
		if merr != nil {
			refund()
			return ctx, func() {}, merr
		}
		if h, ok := historyAppenderFrom(ctx); ok {
			v, aerr := h.Append(ctx, fs.currentVersion(), msg)
			if aerr != nil {
				refund()
				return ctx, func() {}, fmt.Errorf("record foreign admission: %w", aerr)
			}
			fs.setVersion(v)
		}
		fs.record(call.ID, rec.AdmittedTotal)
		return rctx, refund, nil
	}
	ag := runtime.AgentRun{
		Model:    modelFuncModel{profile: cfg.profile, gen: governedForeignGenerate(sessionID, tc, cenv, cst, fs.setVersion, tc.model)},
		Tools:    foreignTools(cfg, gate, reserve, env),
		Assemble: tc.assemble,
		History:  hist,
		Input:    input,
	}
	return ctx, foreignAccountingRuntime{rt: newRT(), fs: fs}, ag, ledger, nil
}

// restoreForeignRun adopts checkpoint accounting onto a fresh run state.
func restoreForeignRun(fs *foreignRunState, p *runtime.ForeignProgress) error {
	return fs.restore(p)
}

// governedForeignGenerate binds the compaction persistence stage and the
// publication reconciliation to one foreign run's provider calls. Every
// Generate reloads the durable session, runs the stage and reconciles the
// publications the loaded history carries before the composed model chain
// enters, so a backend call that never reaches harness assembly stays
// covered and the chain cannot issue a provider call past a refused
// boundary. A reload, validation, sink or stage failure yields the error
// instead of invoking the provider.
func governedForeignGenerate(sessionID string, tc turnConfig, env *turnEnv, st *runtime.State, committed func(int64), gen types.ModelFunc) types.ModelFunc {
	return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			if s, ok := types.SinkFrom(ctx); ok {
				env.sink = s
			}
			if tc.compactionLoad != nil {
				msgs, v, err := tc.compactionLoad(ctx, sessionID)
				if err != nil {
					yield(types.ModelChunk{}, fmt.Errorf("gohan: compaction reload: %w", err))
					return
				}
				env.msgs = msgs
				st.HistoryVersion = v
			}
			req, _, err := runCompactionStage(ctx, st, env, req)
			if err != nil {
				yield(types.ModelChunk{}, err)
				return
			}
			// Every actual provider invocation reconciles the durable
			// compaction publications the loaded history carries,
			// whatever the flow's policy.
			if err := reconcileCompactionPublications(ctx, tc, env.msgs); err != nil {
				yield(types.ModelChunk{}, err)
				return
			}
			committed(st.HistoryVersion)
			for chunk, err := range gen(ctx, req) {
				if !yield(chunk, err) {
					return
				}
			}
		}
	}
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

// governedForeignCall runs one foreign tool call through the same per-call
// governed path the native batch effect uses. The call passes the approval
// gate on the native terms, holds one ReserveBatch slot on the run ledger
// for its execution, and the composed tool chain stays the only ordinary
// tool journal wrapper: the chain's journal and shield steps reserve,
// complete and shield exactly as they do for a native call. A call that
// carries no backend id never reaches the gate, the reservation, the
// journal or the tool: governedTool rejects it fail-closed before this
// helper runs. A denial renders the not_executed permanent result without
// execution, and an ask parks the run as a human-approval suspension.
func governedForeignCall(
	gate runtime.BatchGate,
	reserve func(ctx context.Context, n int) (context.Context, func(), error),
	env *turnEnv,
	exec func(context.Context, types.ToolUse) (types.ToolResult, error),
) func(context.Context, types.ToolUse) (types.ToolResult, error) {
	return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
		rctx, refund, rerr := reserve(ctx, 1)
		if rerr != nil {
			return types.ToolResult{
				ID:      call.ID,
				Outcome: types.Failed,
				Error:   &types.ToolError{Kind: types.Permanent, Message: runtime.NotExecutedPrefix + rerr.Error()},
			}, nil
		}
		report, err := runtime.Batch{
			Calls:  []types.ToolUse{call},
			Limits: types.RunLimits{MaxToolCalls: 1},
			Gate:   gate,
			Exec: func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
				return governedAskExec(ctx, env, exec, call)
			},
		}.Run(rctx)
		if err != nil {
			if errors.Is(err, types.ErrBatchOverrun) {
				refund()
				return types.ToolResult{
					ID:      call.ID,
					Outcome: types.Failed,
					Error:   &types.ToolError{Kind: types.Permanent, Message: runtime.NotExecutedPrefix + err.Error()},
				}, nil
			}
			return types.ToolResult{}, err
		}
		if report.Suspend != nil {
			return types.ToolResult{}, &types.SuspendError{Reason: types.HumanApproval, Payload: report.Suspend.Pending}
		}
		if len(report.Results) == 0 {
			return types.ToolResult{}, fmt.Errorf("gohan: governed foreign call %q produced no result", call.ID)
		}
		res := report.Results[0].Result
		res.ID = call.ID
		return res, nil
	}
}

// foreignTools wraps every declared tool so a backend call runs the
// resolved tool chain under the shared per-call governed path: the gate,
// the run ledger reservation and the chain's journal and shield steps are
// the same machinery the native effect boundary drives.
func foreignTools(cfg *resolvedNativeConfig, gate runtime.BatchGate, reserve func(ctx context.Context, n int) (context.Context, func(), error), env *turnEnv) []types.Tool {
	exec := governedForeignCall(gate, reserve, env, governedToolExec(cfg.toolChain, nativeToolset(cfg.tools), specLookup(cfg.specs), cfg.clients))
	out := make([]types.Tool, len(cfg.tools))
	for i, t := range cfg.tools {
		out[i] = governedTool{tool: t, governed: exec}
	}
	return out
}

type governedTool struct {
	tool types.Tool
	// governed is the shared per-call path: gate, reservation, chain.
	governed func(ctx context.Context, call types.ToolUse) (types.ToolResult, error)
}

func (g governedTool) Spec() types.ToolSpec { return g.tool.Spec() }

func (g governedTool) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	carried, ok := types.ToolUseFrom(ctx)
	if !ok || carried.ID == "" {
		return types.ToolResult{
			ID:      "",
			Outcome: types.Failed,
			Error:   &types.ToolError{Kind: types.Permanent, Message: runtime.NotExecutedPrefix + types.ErrForeignCallID.Error()},
		}, nil
	}
	return g.governed(ctx, types.ToolUse{ID: carried.ID, Name: g.tool.Spec().Name, Args: args})
}

// NewForeignConversation builds the streaming conversation for a registered
// foreign definition over stack. The constructor binds the stack itself —
// shutdown admission, tenant, title profile and the frozen flag snapshot —
// before the option loop, so every run admits against the same stack.
// Every run gets a fresh backend runtime, a
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
	c.evaluator = stack.evaluator
	c.foreignRun = func(ctx context.Context, sessionID string, hist stores.History, seed float64, input []Message) (context.Context, runtime.Runtime, runtime.AgentRun, *chains.LimitsState, error) {
		if hist.Version == 0 {
			h, err := c.load(ctx, sessionID)
			if err != nil {
				return nil, nil, runtime.AgentRun{}, nil, err
			}
			hist = h
		}
		return stack.foreignAgentRun(ctx, sessionID, name, hist, seed, input)
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
