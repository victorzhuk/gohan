// Package adkgo adapts the gohan runtime port to google.golang.org/adk/v2.
package adkgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/workflow"
	"google.golang.org/genai"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Runtime drives one adk invocation through the gohan runtime port. One
// Step is one runner invocation whose agent body performs exactly one
// governed model call plus the tool batch that response carries. The
// runtime counts no turns and reads no limits: the driver owns both.
type Runtime struct {
	streaming bool
	run       runtime.AgentRun
	msgs      []types.Message
	pending   *turnResult
	ag        agent.Agent
	rn        *runner.Runner
	// paused reports a live native pause in rn's session: only then can
	// Step resume through adk's runner instead of the replay fallback.
	paused bool
	// interrupt is the interrupt id of the live pause; it lands in the
	// checkpoint envelope so a resume can match the FunctionResponse.
	interrupt string
	// ckptInterrupts are the interrupt ids a restored envelope carries.
	ckptInterrupts []string
	// suspendID and suspendPayload carry the governed suspension signal
	// from the turn body to drain's typed conversion; suspendErr holds
	// the governed signal itself so drain returns the same Reason,
	// Token and Payload the component produced.
	suspendID      string
	suspendPayload any
	suspendErr     *types.SuspendError
	// resume activation wiring: Step primes these before the runner
	// dispatches the FunctionResponse answer to the paused node.
	resumeID    string
	resumeCalls []types.ToolUse
	resumeDone  bool
}

const backendCheckpointVersion = "adkgo-v2"

type backendCheckpoint struct {
	Version      string          `json:"version"`
	Msgs         []types.Message `json:"msgs"`
	Turn         int             `json:"turn"`
	InterruptIDs []string        `json:"interrupt_ids,omitempty"`
}

// Option configures a Runtime at construction.
type Option func(*config)

type config struct {
	streaming bool
}

// WithStreaming declares the streaming preference for adk invocations.
func WithStreaming(on bool) Option {
	return func(c *config) { c.streaming = on }
}

// New wires the runtime configuration. A nil model fails Start.
func New(_ context.Context, opts ...Option) (*Runtime, error) {
	var cfg config
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Runtime{streaming: cfg.streaming}, nil
}

func (r *Runtime) Name() string { return "adkgo" }

// BackendVersion reports the checkpoint envelope version this runtime
// writes into State.Backend.
func (r *Runtime) BackendVersion() string { return backendCheckpointVersion }

// FlowKind declares the agent-flow resume kind: an adk-go agent flow
// replays from the journal across an adapter upgrade.
func (r *Runtime) FlowKind() stores.FlowKind { return stores.FlowAgent }

// FallbackMode names how a backend covers a capability gap.
type FallbackMode string

// FallbackReplay covers a gap by replaying the recorded journal.
const FallbackReplay FallbackMode = "replay"

// Fallback names one declared capability gap and its coverage mode.
type Fallback struct {
	Capability string
	Mode       FallbackMode
}

// DeclaredFallbacks lists every capability gap adk v2.7.1 carries no
// mechanism for, each covered by replay.
func DeclaredFallbacks() []Fallback {
	return []Fallback{
		{Capability: "journal-fingerprint", Mode: FallbackReplay},
		{Capability: "step-notices", Mode: FallbackReplay},
		{Capability: "block-origin-sequence", Mode: FallbackReplay},
		{Capability: "cache-breakpoints", Mode: FallbackReplay},
		// adk routes a resumed interrupt input only to llmagent roots
		// (runner/runner.go:581); a custom agent body cannot receive it,
		// so the parked calls replay from the journal instead.
		{Capability: "native-pause-resume", Mode: FallbackReplay},
	}
}

// ErrUndeclaredFallback reports a capability gap no declared fallback
// covers.
var ErrUndeclaredFallback = errors.New("adkgo: capability gap without a declared fallback")

// errResumeUnavailable reports that the native resume carried no answer
// for the parked interrupt: the step falls back to the replay path
// instead of failing.
var errResumeUnavailable = errors.New("adkgo runtime: native resume carries no answer")

// requireFallback reports whether a capability gap has a declared
// fallback; an undeclared gap fails closed.
func requireFallback(capability string) error {
	for _, f := range DeclaredFallbacks() {
		if f.Capability == capability {
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrUndeclaredFallback, capability)
}

// withCheckpoint serializes the working history into State.Backend, the
// opaque carrier the checkpoint envelope persists and a resume restores.
func (r *Runtime) withCheckpoint(st runtime.State) runtime.State {
	msgs := r.msgs
	if r.pending != nil {
		msgs = append(append([]types.Message(nil), r.msgs...), r.pending.asMessage())
	}
	b, err := json.Marshal(backendCheckpoint{
		Version:      backendCheckpointVersion,
		Msgs:         msgs,
		Turn:         st.Turn,
		InterruptIDs: interruptIDs(r.interrupt),
	})
	if err != nil {
		return st
	}
	st.Backend = b
	return st
}

// restore rebuilds the working history from a checkpointed State.Backend
// ahead of a re-entry Step. An adkgo-v1 envelope predates native resume:
// it falls back to the declared replay path, keeping the run history from
// Start. A mismatching journal fingerprint falls back the same way.
func (r *Runtime) restore(st runtime.State) {
	r.ckptInterrupts = nil
	if len(st.Backend) == 0 {
		return
	}
	var cp backendCheckpoint
	if json.Unmarshal(st.Backend, &cp) != nil || cp.Version != backendCheckpointVersion {
		_ = requireFallback("journal-fingerprint") // declared: replay from the Start history
		return
	}
	r.msgs = append([]types.Message(nil), cp.Msgs...)
	r.ckptInterrupts = append([]string(nil), cp.InterruptIDs...)
}

// interruptIDs lifts one live interrupt id into the envelope's list.
func interruptIDs(ids ...string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func (r *Runtime) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

// Start binds the per-run wiring the subsequent Steps dispatch to. One
// instance drives one run, like the native stepper.
func (r *Runtime) Start(_ context.Context, run runtime.AgentRun) (runtime.State, error) {
	if run.Model == nil {
		return runtime.State{}, errors.New("adkgo runtime: start without a governed model")
	}
	if len(run.Tools) == 0 {
		run.Tools = nil
	}
	r.run = run
	r.pending = nil
	r.resumeID, r.resumeCalls, r.resumeDone = "", nil, false
	// The resume path reloads the session history before Start: the
	// working history rebuilds from it, so re-entry never replays input
	// the checkpoint already carried.
	r.msgs = append([]types.Message(nil), run.History.Messages...)
	r.msgs = append(r.msgs, run.Input...)
	// The agent and the runner live once per Runtime instance: a native
	// pause survives the re-entry Start inside the same in-memory
	// session, and a fresh process rebuilds both, losing the pause to the
	// replay fallback.
	if r.ag == nil {
		ag, err := agent.New(agent.Config{Name: "adkgo", Run: r.turnNode()})
		if err != nil {
			return runtime.State{}, fmt.Errorf("adkgo runtime: build agent: %w", err)
		}
		rn, err := runner.NewInMemory("adkgo", ag)
		if err != nil {
			return runtime.State{}, fmt.Errorf("adkgo runtime: build runner: %w", err)
		}
		r.ag, r.rn = ag, rn
	}
	return runtime.State{}, nil
}

// Step runs exactly one runner invocation whose agent body performs one
// governed model call plus its tool batch. Events are runtime-originated
// only: component events travel through the sink ctx carries, reached
// unchanged by every governed call.
func (r *Runtime) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if err := ctx.Err(); err != nil {
		return st, nil, runtime.Continue, err
	}
	r.restore(st)
	r.pending = nil
	// A live native pause with recorded interrupt ids resumes through the
	// runner: the FunctionResponse answer re-runs the paused node and the
	// approved calls execute inside the resumed activation. Anything else
	// falls back to replay.
	if r.paused && r.rn != nil && len(st.Pending) > 0 && len(r.ckptInterrupts) > 0 {
		nst, evs, status, err := r.resumeNative(ctx, st)
		if !errors.Is(err, errResumeUnavailable) {
			return nst, evs, status, err
		}
		// The native path carried no answer for the parked interrupt.
		// adk v2.7.1 drives a custom (non-LlmAgent) root agent straight
		// from runner.run, outside the workflow engine, so no resume
		// payload ever reaches the agent and the base invocation
		// context answers every ResumedInput with (nil, false). The
		// declared replay path covers the approved calls instead.
		r.paused, r.interrupt = false, ""
		r.resumeID, r.resumeCalls, r.resumeDone = "", nil, false
		st = nst
	}
	r.pending = nil
	// A resumed run executes its checkpointed pending calls under their
	// recorded ids before another model invocation: an approved call
	// never regenerates through the model. One pending call clears only
	// after its result settles.
	for len(st.Pending) > 0 {
		call := st.Pending[0]
		res, err := r.replayCall(ctx, call)
		if err != nil {
			var se *types.SuspendError
			if errors.As(err, &se) {
				return r.park(st, se), nil, runtime.Continue, se
			}
			return st, nil, runtime.Continue, err
		}
		r.msgs = append(r.msgs, res)
		st.Pending = st.Pending[1:]
	}
	return r.runTurn(ctx, st)
}

// resumeNative answers the live adk pause natively: the runner matches
// the FunctionResponse against the paused invocation and re-runs the
// node, where the resumed activation executes only the approved parked
// calls and skips the model call.
func (r *Runtime) resumeNative(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	id := r.ckptInterrupts[0]
	r.resumeID = id
	r.resumeCalls = append([]types.ToolUse(nil), st.Pending...)
	r.resumeDone = false
	msg := &genai.Content{
		Role: genai.RoleUser,
		Parts: []*genai.Part{{
			FunctionResponse: &genai.FunctionResponse{ID: id, Response: map[string]any{"response": "approved"}},
		}},
	}
	if err := r.drain(ctx, r.rn.Run(ctx, "gohan", "adkgo", msg, agent.RunConfig{})); err != nil {
		var limited *types.LimitExceededError
		// Only the MaxTurns refusal is a stop the driver owns; a MaxCost
		// or MaxWallClock hard abort stays a limit error the driver sees.
		if errors.As(err, &limited) && limited.Limit == "MaxTurns" {
			return st, []types.Event{types.Done{Reason: types.StopLimit}}, runtime.DoneStatus, nil
		}
		var se *types.SuspendError
		if errors.As(err, &se) {
			// A governed component parked again inside the resumed
			// activation; the new interrupt replaces the answered one.
			st.Pending = nil
			return r.park(st, se), nil, runtime.Continue, se
		}
		return st, nil, runtime.Continue, err
	}
	if !r.resumeDone {
		return st, nil, runtime.Continue, errors.New("adkgo runtime: resumed activation executed no calls")
	}
	r.paused, r.interrupt = false, ""
	r.resumeID, r.resumeCalls = "", nil
	st.Pending = nil
	// Same shape as the replay fallback: after the approved calls settle,
	// the turn continues with the next governed model call.
	return r.runTurn(ctx, st)
}

// runTurn performs the fresh runner invocation whose agent body performs
// one governed model call plus its tool batch.
func (r *Runtime) runTurn(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if r.rn == nil {
		return st, nil, runtime.Continue, errors.New("adkgo runtime: step before start")
	}
	r.resumeID, r.resumeCalls, r.resumeDone = "", nil, false
	if err := r.drain(ctx, r.rn.Run(ctx, "gohan", "adkgo", nil, agent.RunConfig{})); err != nil {
		var limited *types.LimitExceededError
		// Only the MaxTurns refusal is a stop the driver owns; a MaxCost
		// or MaxWallClock hard abort stays a limit error the driver sees.
		if errors.As(err, &limited) && limited.Limit == "MaxTurns" {
			return st, []types.Event{types.Done{Reason: types.StopLimit}}, runtime.DoneStatus, nil
		}
		var se *types.SuspendError
		if errors.As(err, &se) {
			// A governed component parked the run; the signal crosses
			// as-is and the parked calls land in the pending queue.
			return r.park(st, se), nil, runtime.Continue, se
		}
		return st, nil, runtime.Continue, err
	}
	st.Turn++
	if r.pending == nil {
		return st, nil, runtime.Continue, errors.New("adkgo runtime: turn produced no model result")
	}
	if len(r.pending.calls) == 0 {
		msg := r.pending.asMessage()
		r.msgs = append(r.msgs, msg)
		return st, []types.Event{
			types.AssistantMessage{Message: msg},
			types.Done{Reason: types.StopCompleted},
		}, runtime.DoneStatus, nil
	}
	return st, []types.Event{types.AssistantMessage{Message: r.pending.asMessage()}}, runtime.Continue, nil
}

// turnNode exposes the single-turn body as an adk agent behavior. The
// runner supplies the invocation context, so every governed call inside
// runs inside a live adk invocation.
func (r *Runtime) turnNode() func(agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(ic agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			// One promotion per activation: every governed call runs
			// under a derived agent.Context so bridged tools that assert
			// agent.Context accept it.
			actx := agent.NewContext(ic)
			if r.resumeID != "" {
				r.runResume(actx, ic, yield)
				return
			}
			if _, err := r.turn(actx); err != nil {
				var se *types.SuspendError
				if errors.As(err, &se) {
					// Park natively: the runner sees a long-running
					// request and holds the node for the answer.
					yield(workflow.NewRequestInputEvent(ic, session.RequestInput{
						InterruptID: r.suspendID,
						Payload:     se.Payload,
					}), nil)
					return
				}
				yield(nil, err)
			}
		}
	}
}

// runResume is the resumed activation: the approved parked calls execute
// under their recorded ids, the model call is skipped.
func (r *Runtime) runResume(ctx agent.Context, ic agent.InvocationContext, yield func(*session.Event, error) bool) {
	if _, ok := ic.ResumedInput(r.resumeID); !ok {
		yield(nil, fmt.Errorf("%w: interrupt %q", errResumeUnavailable, r.resumeID))
		return
	}
	for _, call := range r.resumeCalls {
		msg, err := r.replayCall(ctx, call)
		if err != nil {
			var se *types.SuspendError
			if errors.As(err, &se) {
				r.suspendID, r.suspendPayload, r.suspendErr = call.ID, se.Payload, se
				yield(workflow.NewRequestInputEvent(ic, session.RequestInput{
					InterruptID: call.ID,
					Payload:     se.Payload,
				}), nil)
				return
			}
			yield(nil, err)
			return
		}
		r.msgs = append(r.msgs, msg)
	}
	r.resumeDone = true
}

// drain consumes one runner invocation. ctx flows into the iterator, so a
// cancelled turn stops here; the governed error crosses unchanged.
func (r *Runtime) drain(ctx context.Context, it iter.Seq2[*session.Event, error]) error {
	for ev, err := range it {
		if err != nil {
			return err
		}
		// The runner surfaced the native pause request: the governed
		// suspension crosses to the driver as the typed signal it owns.
		if ev != nil && (ev.RequestedInput != nil || len(ev.LongRunningToolIDs) > 0) {
			if se := r.takeSuspend(); se != nil {
				return se
			}
			return &types.SuspendError{Payload: r.suspendPayload}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

// takeSuspend hands back the governed suspension signal the turn body
// held and clears it, so a later turn cannot re-surface a stale suspend.
func (r *Runtime) takeSuspend() *types.SuspendError {
	se := r.suspendErr
	r.suspendErr = nil
	return se
}

// turn performs one governed model call and, when the response carries
// calls, the tool batch in call order. ctx is the promoted agent.Context
// the turn body derived from the runner-supplied invocation context, so
// the sink, the cancel shield and the limits ledger stay live.
func (r *Runtime) turn(ctx agent.Context) (turnResult, error) {
	req := types.ModelRequest{Messages: r.msgs}
	if r.run.Assemble != nil {
		var err error
		req, err = r.run.Assemble(ctx, types.AssembleInput{History: r.msgs})
		if err != nil {
			return turnResult{}, err
		}
	}
	var res turnResult
	for chunk, err := range r.run.Model.Generate(ctx, req) {
		if err != nil {
			return res, err
		}
		// Identity-only streaming previews carry no executable args; the
		// complete call arrives later with its args.
		if chunk.Kind == types.DeltaToolArgs && chunk.ToolUse != nil && len(chunk.ToolUse.Args) == 0 {
			continue
		}
		switch {
		case chunk.ToolUse != nil:
			res.calls = append(res.calls, *chunk.ToolUse)
		case chunk.Kind == types.DeltaText:
			res.text += chunk.Delta
		}
	}
	r.pending = &res
	if len(res.calls) == 0 {
		return res, nil
	}
	r.msgs = append(r.msgs, res.asMessage())
	for _, call := range res.calls {
		t := r.tool(call.Name)
		if t == nil {
			return res, fmt.Errorf("adkgo runtime: tool call %q names no declared tool", call.Name)
		}
		tr, err := t.Call(withToolUse(ctx, call), call.Args)
		if err != nil {
			var se *types.SuspendError
			if errors.As(err, &se) {
				r.suspendID, r.suspendPayload, r.suspendErr = call.ID, se.Payload, se
			}
			return res, err
		}
		tr.ID = call.ID
		msg := types.Message{Role: types.RoleUser, Blocks: []types.Block{tr}}
		res.results = append(res.results, msg)
		r.msgs = append(r.msgs, msg)
	}
	return res, nil
}

func (r *Runtime) tool(name string) types.Tool {
	for _, t := range r.run.Tools {
		if t.Spec().Name == name {
			return t
		}
	}
	return nil
}

// governedCtx carries the backend tool call identity alongside the
// promoted adk context: it answers the ToolUse key through the recorded
// values and keeps every other read, cancellation included, on the
// genuine agent.Context a bridged foreign tool asserts.
type governedCtx struct {
	agent.Context
	vals context.Context
}

func (c governedCtx) Value(k any) any { return c.vals.Value(k) }

// withToolUse binds the call identity onto the promoted context without
// hiding the agent.Context shape behind a plain value wrapper.
func withToolUse(ctx agent.Context, call types.ToolUse) context.Context {
	return governedCtx{Context: ctx, vals: types.WithToolUse(ctx, call)}
}

// replayCall runs one checkpointed pending call through its governed tool
// under the recorded id and returns the result message the backend history
// continues from.
func (r *Runtime) replayCall(ctx context.Context, call types.ToolUse) (types.Message, error) {
	t := r.tool(call.Name)
	if t == nil {
		return types.Message{}, fmt.Errorf("adkgo runtime: pending call %q names no declared tool", call.Name)
	}
	res, err := t.Call(types.WithToolUse(ctx, call), call.Args)
	if err != nil {
		return types.Message{}, err
	}
	res.ID = call.ID
	return types.Message{Role: types.RoleUser, Blocks: []types.Block{res}}, nil
}

var _ runtime.Runtime = (*Runtime)(nil)

// park records the calls a governed component suspended on into the
// state's pending queue, so the driver's approval persistence sees the
// same head-of-queue shape a native batch park produces.
func (r *Runtime) park(st runtime.State, se *types.SuspendError) runtime.State {
	// The pause lives in the runner's in-memory session: with interrupt
	// ids in the envelope the next Step can resume it natively.
	r.paused = true
	r.interrupt = r.suspendID
	if calls, ok := se.Payload.([]types.ToolUse); ok && len(calls) > 0 && len(st.Pending) == 0 {
		st.Pending = calls
	}
	return r.withCheckpoint(st)
}

// turnResult is one model turn the governed bridge observed.
type turnResult struct {
	text    string
	calls   []types.ToolUse
	results []types.Message
}

func (res turnResult) asMessage() types.Message {
	msg := types.Message{Role: types.RoleAssistant}
	if res.text != "" {
		msg.Blocks = append(msg.Blocks, types.Text{Text: res.text})
	}
	for _, c := range res.calls {
		msg.Blocks = append(msg.Blocks, c)
	}
	return msg
}
