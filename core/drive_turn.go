package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

// turnConfig wires one per-turn Drive loop. Turn counting and cancellation
// live here; limits over tool calls stay in the chains and the batch
// protocol stays in the runtime, so the loop never counts a tool call.
// Every std-provided decision arrives as a function value, because the
// driver may not import std.
type turnConfig struct {
	model     types.ModelFunc
	assemble  func(context.Context, types.AssembleInput) (types.ModelRequest, error)
	history   []types.Message
	input     []types.Message
	tools     ToolSet
	maxTurns  int
	maxTokens int
	gate      runtime.BatchGate
	limits    types.RunLimits
	// scheduler carries the resolved batch strategy. A zero value runs
	// every call sequentially.
	scheduler runtime.SchedulerConfig
	// exec is the governed call path for one batch call: the tool chain
	// around CallTool with the call's original identity. nil uses the
	// direct CallTool path.
	exec func(context.Context, types.ToolUse) (types.ToolResult, error)
	// reserve reserves the batch's whole call set against the run ledger
	// before any tool executes; the returned context carries the
	// reservation, so the batch's executions consume it instead of
	// charging again, and the returned func refunds the reservation when
	// the batch is refused. The ledger stays outside core.
	reserve func(ctx context.Context, n int) (context.Context, func(), error)

	// onTruncated decides the retry turn a truncated tool call earns. It
	// returns the larger output allowance for the retry and the truncation
	// result handed back to the model; an error ends the run when the
	// allowance is spent. A nil result with a nil error means the finish was
	// not a truncation.
	onTruncated func(tu types.ToolUse, finish types.FinishReason, attempt int) (maxTokens int, result *types.ToolResult, err error)
	// repair decides a repair turn for a structured reply. A prompt with ok
	// true is appended and one more model call runs; the allowance is the
	// caller's attempt accounting, not MaxTurns.
	repair func(text string, attempt int) (prompt types.Message, ok bool, err error)
	// poll runs at the safe point after the batch results are appended and
	// before Finish. A stop reason other than completed ends the loop there,
	// which is how Runs.SignalCancel lands at the next safe point.
	poll func() types.StopReason
	// coalesce merges consecutive preview deltas into one record before the
	// sink forwards them to the EventLog in a detached run; nil streams
	// every fragment as it arrives.
	coalesce *DeltaCoalescer
	// resultPreview renders the run's typed result as the ResultDelta
	// preview fragments the client sees before Done. The preview is never
	// journaled, gated or passed to a tool; nil emits none.
	resultPreview func(msg types.Message) []string
	// reasoningVisible gates whether provider reasoning streams to the
	// consumer as ReasoningDelta; opaque reasoning is retained on the
	// assistant message at completion either way.
	reasoningVisible bool
	// profile, specs and instruction carry the resolved configuration the
	// assembler reads: the selected model profile, the registered tool
	// specs and the instruction blocks. The native wiring binds all three;
	// a run without a resolution leaves them unset.
	profile     types.ModelProfile
	specs       []types.ToolSpec
	instruction []types.Block
}

// turnEnv carries the per-run state the two effects share across their
// boundaries: the loop position, the working history and the pending batch
// the model effect hands to the batch effect. The runtime State only
// carries what resume needs; this scope lives for one driveTurns call and
// travels in the context.
type turnEnv struct {
	c          turnConfig
	sink       types.Sink
	msgs       []types.Message
	turn       int
	turnSeeded bool
	maxTokens  int
	truncated  int
	repaired   int
	used       int
	calls      []types.ToolUse
	// reasoning is the provider reasoning the model effect streamed for the
	// current turn; the batch effect retains it on the assistant message.
	reasoning string
}

type turnEnvKey struct{}

func withTurnEnv(ctx context.Context, env *turnEnv) context.Context {
	return context.WithValue(ctx, turnEnvKey{}, env)
}

func turnEnvFrom(ctx context.Context) *turnEnv {
	env, _ := ctx.Value(turnEnvKey{}).(*turnEnv)
	return env
}

// emitDelta streams one preview delta; with a coalescer the fragment
// feeds the open record and the closed records come back out.
func (env *turnEnv) emitDelta(ctx context.Context, ev types.Event) {
	if env.sink == nil {
		return
	}
	if env.c.coalesce == nil {
		env.sink.Emit(ctx, ev)
		return
	}
	for _, e := range env.c.coalesce.Add(ev) {
		env.sink.Emit(ctx, e)
	}
}

// flushDeltas closes the open coalesced record before a non-delta event
// or a terminal tuple, so the log never holds a partial call's fragments
// behind the record that completes the call.
func (env *turnEnv) flushDeltas(ctx context.Context) {
	if env.sink == nil || env.c.coalesce == nil {
		return
	}
	for _, e := range env.c.coalesce.Flush() {
		env.sink.Emit(ctx, e)
	}
}

// driveTurns runs one model call per turn, executes the turn's tool calls
// in call order, appends the results in call order and counts MaxTurns over
// model calls. A steer-drain turn counts too, because it is one more model
// call after a drain. Deltas and tool events stream through the sink; the
// iterator carries the final assistant message, Done and errors.
func driveTurns(ctx context.Context, c turnConfig, yield func(types.Event, error) bool) {
	sink, _ := types.SinkFrom(ctx)
	env := &turnEnv{
		c:         c,
		sink:      sink,
		msgs:      slices.Clone(c.history),
		maxTokens: c.maxTokens,
	}
	if len(c.input) > 0 {
		env.msgs = append(env.msgs, c.input...)
	}
	ctx = withTurnEnv(ctx, env)
	var st runtime.State
	for {
		if env.turn >= c.maxTurns {
			env.flushDeltas(ctx)
			yield(types.Done{Reason: types.StopLimit}, nil)
			return
		}
		if err := ctx.Err(); err != nil {
			env.flushDeltas(ctx)
			yield(nil, err)
			return
		}
		var events []types.Event
		var status runtime.Status
		var err error
		st, events, status, err = modelEffect(ctx, st)
		if err != nil {
			yield(nil, err)
			return
		}
		if status == runtime.DoneStatus {
			terminated := false
			for _, ev := range events {
				if _, ok := ev.(types.Done); ok {
					terminated = true
				}
				yield(ev, nil)
			}
			if !terminated {
				// The loop owns the terminal transition: the model effect
				// returns the assistant message only.
				yield(types.Done{Reason: types.StopCompleted}, nil)
			}
			return
		}
		if len(env.calls) > 0 {
			_, events, status, err = batchEffect(ctx, st)
			if err != nil {
				yield(nil, err)
				return
			}
			if status == runtime.DoneStatus {
				for _, ev := range events {
					yield(ev, nil)
				}
				return
			}
		}
	}
}

// modelEffect performs exactly one governed model invocation: assembly,
// request preparation, streaming through the sink, parsing, truncated-call
// repair and truncation handling. It advances the turn counter once and,
// when the reply carries tool calls, parks them on the per-run scope for
// the batch effect. A final answer returns the assistant message as the
// event a terminal status carries; the driver owns the terminal Done.
// appendFailedPartial records an ordinary mid-stream provider failure: the
// assistant content streamed so far appends to history carrying
// FinishError, so a resumed session continues from the partial turn. A
// controlled stop (cancellation, preemption) never reaches this; a failure
// before any content streamed appends nothing.
func appendFailedPartial(ctx context.Context, st runtime.State, env *turnEnv, reply, reasoning string, calls []types.ToolUse) error {
	if reply == "" && reasoning == "" && len(calls) == 0 {
		return nil
	}
	asst := types.Message{ID: assistantID(env.turn), Role: types.RoleAssistant, Meta: map[string]any{metaFinish: types.FinishError}}
	if reply != "" {
		asst.Blocks = append(asst.Blocks, types.Text{Text: reply})
	}
	for _, call := range calls {
		asst.Blocks = append(asst.Blocks, call)
	}
	retainReasoning(&asst, reasoning)
	env.msgs = append(env.msgs, asst)
	if h, ok := historyAppenderFrom(ctx); ok {
		v, err := h.Append(ctx, st.HistoryVersion, asst)
		if err != nil {
			return err
		}
		st.HistoryVersion = v
	}
	return nil
}

func modelEffect(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	env := turnEnvFrom(ctx)
	c := env.c
	if !env.turnSeeded {
		env.turn = st.Turn
		env.turnSeeded = true
	}
	if c.maxTurns > 0 && env.turn >= c.maxTurns {
		env.flushDeltas(ctx)
		return st, []types.Event{types.Done{Reason: types.StopLimit}}, runtime.DoneStatus, nil
	}
	if notes := steerNotesFrom(ctx); notes != nil {
		// The steers the lifecycle drained at its safe point enter the
		// working history here, so the next request carries them.
		env.msgs = append(env.msgs, notes.take()...)
	}
	req, err := c.assemble(ctx, env.assemblyInput(ctx, env.msgs))
	if err != nil {
		return st, nil, runtime.Continue, err
	}
	if env.maxTokens > 0 {
		req.Options.MaxTokens = env.maxTokens
	}
	env.turn++
	n := 0
	for _, msg := range env.msgs {
		for _, b := range msg.Blocks {
			if _, ok := b.(types.ToolResult); ok {
				n++
			}
		}
	}
	st.Turn = env.turn

	var sb strings.Builder
	var reasoning strings.Builder
	var calls []types.ToolUse
	finish := types.FinishStop
	mctx, endChat := startSpan(ctx, telemetryFrom(ctx), SpanChat)
	for chunk, err := range c.model(mctx, req) {
		if err != nil {
			endChat(types.String(types.KeySuspendReason, "error"))
			env.flushDeltas(ctx)
			if ctx.Err() != nil {
				// The run context was cancelled or preempted: this is a
				// controlled stop, not an ordinary provider failure, so
				// the partial turn appends nothing.
				return st, nil, runtime.Continue, ctx.Err()
			}
			if aerr := appendFailedPartial(ctx, st, env, sb.String(), reasoning.String(), calls); aerr != nil {
				err = aerr
			}
			return st, nil, runtime.Continue, err
		}
		switch chunk.Kind {
		case types.DeltaText:
			sb.WriteString(chunk.Delta)
			env.emitDelta(ctx, types.TextDelta{Turn: env.turn, Delta: chunk.Delta})
		case types.DeltaReasoning:
			reasoning.WriteString(chunk.Delta)
			if c.reasoningVisible {
				env.emitDelta(ctx, types.ReasoningDelta{Turn: env.turn, Delta: chunk.Delta})
			}
		case types.DeltaToolArgs:
			if chunk.ToolUse != nil {
				// A fragment preview carries the call identity and the
				// fragment text; the complete block carries Args and
				// never streams a fragment of its own.
				if chunk.Delta != "" {
					env.emitDelta(ctx, types.ToolArgsDelta{Turn: env.turn, CallID: chunk.ToolUse.ID, Name: chunk.ToolUse.Name, Delta: chunk.Delta})
				}
				// Only the complete block enters the batch: a fragment
				// is a preview, never an argument set to execute.
				if len(chunk.ToolUse.Args) > 0 {
					calls = append(calls, *chunk.ToolUse)
				}
			}
		}
		if chunk.ToolUse != nil && chunk.Kind != types.DeltaToolArgs && len(chunk.ToolUse.Args) > 0 {
			calls = append(calls, *chunk.ToolUse)
		}
		if chunk.Finish != "" && chunk.Finish != types.FinishStop {
			finish = chunk.Finish
		}
	}
	endChat()

	// A truncated finish earns a retry turn before anything executes:
	// the pending calls are never run, the truncation result goes back
	// to the model and the retry carries the larger allowance.
	if finish == types.FinishMaxTokens && c.onTruncated != nil {
		tu := types.ToolUse{}
		if len(calls) > 0 {
			tu = calls[len(calls)-1]
		}
		env.truncated++
		retry, result, err := c.onTruncated(tu, finish, env.truncated)
		if err != nil {
			return st, nil, runtime.Continue, err
		}
		if retry > 0 {
			env.maxTokens = retry
			env.msgs = append(env.msgs, truncatedTurn(tu, *result, env.turn))
			env.flushDeltas(ctx)
			return st, nil, runtime.Continue, nil
		}
	}

	if len(calls) > 0 {
		env.flushDeltas(ctx)
		env.reasoning = reasoning.String()
		env.calls = calls
		return st, nil, runtime.Continue, nil
	}

	reply := sb.String()
	if c.repair != nil {
		env.repaired++
		prompt, ok, err := c.repair(reply, env.repaired)
		if err != nil {
			env.flushDeltas(ctx)
			return st, nil, runtime.Continue, err
		}
		if ok {
			asst := types.Message{ID: assistantID(env.turn), Role: types.RoleAssistant, Blocks: []types.Block{types.Text{Text: reply}}}
			retainReasoning(&asst, reasoning.String())
			env.msgs = append(env.msgs,
				asst,
				prompt)
			env.flushDeltas(ctx)
			return st, nil, runtime.Continue, nil
		}
	}
	asst := types.Message{ID: assistantID(env.turn), Role: types.RoleAssistant, Blocks: []types.Block{types.Text{Text: reply}}}
	retainReasoning(&asst, reasoning.String())
	env.msgs = append(env.msgs, asst)
	if h, ok := historyAppenderFrom(ctx); ok {
		v, aerr := h.Append(ctx, st.HistoryVersion, asst)
		if aerr != nil {
			env.flushDeltas(ctx)
			return st, nil, runtime.Continue, aerr
		}
		st.HistoryVersion = v
	}
	if c.resultPreview != nil {
		for _, d := range c.resultPreview(asst) {
			env.emitDelta(ctx, types.ResultDelta{Turn: env.turn, MessageID: asst.ID, Delta: d})
		}
	}
	env.flushDeltas(ctx)
	// The iterator carries the terminal assistant message; emitting it
	// through the sink too would deliver it twice wherever the driver
	// merges the sink into the same stream. The terminal transition
	// belongs to the driver: the model effect returns the assistant
	// message and its status only, so the run ends with one Done.
	ev := types.AssistantMessage{Turn: env.turn, Message: asst}
	return st, []types.Event{ev}, runtime.DoneStatus, nil
}

// nativeProgressVersion is the driver record format the batch effect
// writes; a decode refusing unknown versions lets newer records fail
// closed instead of misreading older accounting.
const nativeProgressVersion = 1

// nativeProgress is the durable driver record. Calls holds the original
// batch in original order; Results is index-aligned with Calls, so an
// empty Results[i].ID means the call at that index is still unresolved.
// ToolCalls is the admitted total the reservations restore from.
type nativeProgress struct {
	Version   int                      `json:"version"`
	ToolCalls int                      `json:"tool_calls"`
	Batch     *nativeBatchContinuation `json:"batch,omitempty"`
}

type nativeBatchContinuation struct {
	Calls   []types.ToolUse    `json:"calls"`
	Results []types.ToolResult `json:"results"`
	Ready   bool               `json:"ready,omitempty"`
}

type nativeBackend struct {
	Phase  string          `json:"phase"`
	Driver *nativeProgress `json:"driver,omitempty"`
}

// decodeNativeBackend parses the durable native record. An empty backend
// yields a zero record, because a state from before any batch ran carries
// no driver evidence; malformed bytes refuse rather than invent progress.
func decodeNativeBackend(st runtime.State) (nativeBackend, error) {
	if len(st.Backend) == 0 {
		return nativeBackend{}, nil
	}
	var b nativeBackend
	if err := json.Unmarshal(st.Backend, &b); err != nil {
		return nativeBackend{}, fmt.Errorf("%w: malformed native backend record: %s", types.ErrCheckpointIncompatible, err)
	}
	return b, nil
}

func encodeNativeBackend(b nativeBackend) (json.RawMessage, error) {
	enc, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("%w: encode native backend record: %s", types.ErrCheckpointIncompatible, err)
	}
	return enc, nil
}

// validateNativeContinuation refuses a driver record a resume cannot
// honour: unknown versions, a batch whose settled slots contradict their
// calls, or a pending ask without its own unresolved slot. A populated
// result ID must name its own call, so a result can never be attached to
// a different call across a checkpoint.
func validateNativeContinuation(p *nativeProgress, pending []types.ToolUse) error {
	if p == nil {
		return nil
	}
	if p.Version != nativeProgressVersion {
		return fmt.Errorf("%w: unsupported native driver record version %d", types.ErrCheckpointIncompatible, p.Version)
	}
	if p.ToolCalls < 0 {
		return fmt.Errorf("%w: negative native driver tool call total", types.ErrCheckpointIncompatible)
	}
	if p.Batch == nil {
		if len(pending) > 0 {
			return fmt.Errorf("%w: pending calls without a batch continuation", types.ErrCheckpointIncompatible)
		}
		return nil
	}
	if p.ToolCalls < len(p.Batch.Calls) {
		return fmt.Errorf("%w: native driver tool call total below the recorded batch", types.ErrCheckpointIncompatible)
	}
	if len(p.Batch.Calls) != len(p.Batch.Results) {
		return fmt.Errorf("%w: native batch calls and results lengths differ", types.ErrCheckpointIncompatible)
	}
	seen := map[string]bool{}
	for i, cu := range p.Batch.Calls {
		if cu.ID == "" {
			return fmt.Errorf("%w: native batch call %d missing id", types.ErrCheckpointIncompatible, i)
		}
		if seen[cu.ID] {
			return fmt.Errorf("%w: duplicate native batch call id %q", types.ErrCheckpointIncompatible, cu.ID)
		}
		seen[cu.ID] = true
		if id := p.Batch.Results[i].ID; id != "" && id != cu.ID {
			return fmt.Errorf("%w: native batch result %d names call %q, want %q", types.ErrCheckpointIncompatible, i, id, cu.ID)
		}
	}
	slot := map[string]int{}
	for i, cu := range p.Batch.Calls {
		slot[cu.ID] = i
	}
	for _, pu := range pending {
		i, ok := slot[pu.ID]
		if !ok || p.Batch.Results[i].ID != "" {
			return fmt.Errorf("%w: pending call %q has no unresolved batch slot", types.ErrCheckpointIncompatible, pu.ID)
		}
	}
	return nil
}

type replayKey struct{}

// withPendingReplay marks a drive that re-enters the batch phase with a
// decision already applied to its state: its pending calls replay from the
// state instead of the per-run scope a model turn parks.
func withPendingReplay(ctx context.Context) context.Context {
	return context.WithValue(ctx, replayKey{}, true)
}

func pendingReplayFrom(ctx context.Context) bool {
	v, _ := ctx.Value(replayKey{}).(bool)
	return v
}

type appenderKey struct{}

func withHistoryAppender(ctx context.Context, h HistoryAppender) context.Context {
	return context.WithValue(ctx, appenderKey{}, h)
}

func historyAppenderFrom(ctx context.Context) (HistoryAppender, bool) {
	h, ok := ctx.Value(appenderKey{}).(HistoryAppender)
	return h, ok
}

// admittedToolKey carries the tool-call total a run already admitted from
// its durable driver record to the reservation seam, so a re-entered drive
// re-admits that many slots before any new batch.
type admittedToolKey struct{}

func withAdmittedToolTotal(ctx context.Context, total int) context.Context {
	return context.WithValue(ctx, admittedToolKey{}, total)
}

func admittedToolTotalFrom(ctx context.Context) (int, bool) {
	v, ok := ctx.Value(admittedToolKey{}).(int)
	return v, ok
}

// batchEffect settles one turn's batch under the batch protocol: the gate
// decides every call before the first one executes, allowed calls run in
// call order through the scheduler, denials render as Failed(Permanent)
// results at their index, and the first ask suspends the batch as a
// SuspendError the caller persists — never an ordinary failed tool result.
// It appends the assistant message with its calls and the settled results
// in call order. A turn with any committed call appends before execution;
// a read-only turn joins the assistant message and its results into one
// append at settlement. It adds the batch's reservation spend to the
// per-run scope. The batch runs even when the context is already
// cancelled, so an
// in-flight side effect finishes under the injected shield; after it the
// run stops.
func batchEffect(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	env := turnEnvFrom(ctx)
	c := env.c
	turn := env.turn
	calls := env.calls
	if len(calls) == 0 && len(st.Pending) > 0 && pendingReplayFrom(ctx) {
		// A resumed or recovered drive re-enters the batch phase from the
		// checkpointed state alone. With a driver record only the active
		// ask replays; without one the pending calls replay as one gated
		// batch, because nothing names the queue head as decided.
		b, derr := decodeNativeBackend(st)
		if derr != nil {
			return st, nil, runtime.Continue, derr
		}
		if b.Driver != nil && b.Driver.Batch != nil {
			return replayAskEffect(ctx, st)
		}
		calls = st.Pending
		env.calls = calls
	}
	if len(calls) == 0 {
		// The batch already settled before the suspension (delivered or
		// rejected pending calls): nothing to gate or execute.
		return st, nil, runtime.Continue, nil
	}
	msgs := &env.msgs
	used := env.used
	limits := c.limits
	if limits.MaxToolCalls <= 0 {
		limits.MaxToolCalls = math.MaxInt
	}
	gate := c.gate
	if gate == nil {
		gate = func(context.Context, types.ToolUse) runtime.BatchDecision {
			return runtime.BatchDecision{Outcome: runtime.BatchAllow}
		}
	}
	asst := types.Message{ID: assistantID(turn), Role: types.RoleAssistant}
	retainReasoning(&asst, env.reasoning)
	for _, cu := range calls {
		asst.Blocks = append(asst.Blocks, cu)
	}
	effects := make([]types.Effect, len(calls))
	for i, call := range calls {
		e := types.SideEffect
		if c.scheduler.EffectOf != nil {
			switch got := c.scheduler.EffectOf(call); got {
			case types.ReadOnly, types.Idempotent, types.SideEffect:
				e = got
			}
		}
		effects[i] = e
	}
	shape := ShapeFor(effects...)
	*msgs = append(*msgs, asst)

	if shape == ShapeBeforeBatch {
		if h, ok := historyAppenderFrom(ctx); ok {
			v, aerr := AppendBeforeBatch(ctx, h, st.HistoryVersion, asst)
			if aerr != nil {
				return st, nil, runtime.Continue, aerr
			}
			st.HistoryVersion = v
		}
	}

	refund := func() {}
	if c.reserve != nil {
		historical := 0
		if b, derr := decodeNativeBackend(st); derr == nil && b.Driver != nil {
			historical = b.Driver.ToolCalls
		}
		ctx = withAdmittedToolTotal(ctx, historical)
		rctx, r, err := c.reserve(ctx, len(calls))
		if err != nil {
			return st, nil, runtime.Continue, err
		}
		ctx, refund = rctx, r
	}
	exec := c.exec
	if exec == nil {
		exec = func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			return CallTool(ctx, c.tools, call.Name, call.Args)
		}
	}

	report, err := runtime.Batch{
		Calls:     calls,
		Limits:    limits,
		Used:      used,
		Gate:      gate,
		Scheduler: c.scheduler,
		Exec: func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			return governedAskExec(ctx, env, exec, call)
		},
	}.Run(ctx)
	if err != nil {
		refund()
		if errors.Is(err, types.ErrBatchOverrun) {
			return st, nil, runtime.Continue, &types.LimitExceededError{
				Limit: "MaxToolCalls",
				Value: float64(used + len(calls)),
			}
		}
		return st, nil, runtime.Continue, err
	}

	unsettled := map[string]bool{}
	if report.Suspend != nil {
		unsettled[report.Suspend.Call.ID] = true
		for _, p := range report.Suspend.Pending {
			unsettled[p.ID] = true
		}
	}
	index := make(map[string]int, len(calls))
	for i, cu := range calls {
		index[cu.ID] = i
	}
	// The continuation is index-aligned with the gated batch: an
	// unsettled ask keeps its empty-ID slot so the record can name it as
	// the one active ask on resume.
	aligned := make([]types.ToolResult, len(calls))
	settled := make([]types.ToolResult, 0, len(calls))
	for _, br := range report.Results {
		if unsettled[br.Call.ID] {
			continue
		}
		i, ok := index[br.Call.ID]
		if !ok {
			continue
		}
		res := br.Result
		res.ID = br.Call.ID
		aligned[i] = res
		settled = append(settled, res)
	}
	results := types.Message{ID: resultID(turn), Role: types.RoleUser}
	for _, res := range settled {
		results.Blocks = append(results.Blocks, res)
	}
	*msgs = append(*msgs, results)
	// Results persist before any acknowledgement or suspension: a result
	// a resume cannot find in the session log was never settled. A
	// step-end turn joins the assistant message and the results into one
	// append.
	if h, ok := historyAppenderFrom(ctx); ok {
		var v int64
		var aerr error
		if shape == ShapeBeforeBatch {
			v, aerr = h.Append(ctx, st.HistoryVersion, results)
		} else {
			v, aerr = AppendStepEnd(ctx, h, st.HistoryVersion, asst, results)
		}
		if aerr != nil {
			return st, nil, runtime.Continue, aerr
		}
		st.HistoryVersion = v
	}
	if env.sink != nil {
		for _, res := range settled {
			env.sink.Emit(ctx, types.ToolFinished{Turn: turn, Result: res})
		}
	}
	// The batch phase marker is a string literal: the runtime's phase
	// constants are unexported there. ToolCalls uses the gated batch size
	// until the reservation hook reports the admitted ledger total.
	enc, err := encodeNativeBackend(nativeBackend{
		Phase: "batch",
		Driver: &nativeProgress{
			Version:   nativeProgressVersion,
			ToolCalls: len(calls),
			Batch:     &nativeBatchContinuation{Calls: calls, Results: aligned},
		},
	})
	if err != nil {
		return st, nil, runtime.Continue, err
	}
	st.Backend = enc
	env.used += report.Spent
	if report.Suspend != nil {
		st.Pending = report.Suspend.Pending
		return st, nil, runtime.SuspendedStatus, &types.SuspendError{Reason: types.HumanApproval}
	}
	env.calls = nil
	if c.poll == nil {
		return st, nil, runtime.Continue, nil
	}
	if stop := c.poll(); stop != "" && stop != types.StopCompleted {
		return st, []types.Event{types.Done{Reason: stop}}, runtime.DoneStatus, nil
	}
	return st, nil, runtime.Continue, nil
}

// replayAskEffect settles one active ask when a drive re-enters the batch
// phase from the checkpointed state. Only Pending[0] is active: the queue
// tail stays pending until it becomes the head, and no model call or new
// reservation runs between asks. An undecided ask suspends the head again;
// a decided ask re-consults the gate — a live Deny or taint denial still
// wins — executes the one allowed call through the governed path under a
// prepaid one-call batch, or settles the denial. The result persists
// before any acknowledgement, and a remaining queue suspends the next ask
// immediately.
func replayAskEffect(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	env := turnEnvFrom(ctx)
	c := env.c
	b, err := decodeNativeBackend(st)
	if err != nil {
		return st, nil, runtime.Continue, err
	}
	if b.Driver == nil || b.Driver.Batch == nil {
		return st, nil, runtime.Continue, fmt.Errorf("%w: pending calls without a batch continuation", types.ErrCheckpointIncompatible)
	}
	head := st.Pending[0]
	slot := -1
	for i, cu := range b.Driver.Batch.Calls {
		if cu.ID == head.ID {
			slot = i
			break
		}
	}
	if slot < 0 {
		return st, nil, runtime.Continue, fmt.Errorf("%w: pending call %q has no batch slot", types.ErrCheckpointIncompatible, head.ID)
	}
	if !b.Driver.Batch.Ready {
		// The client never decided, or decided without reaching quorum:
		// the same head asks again under a fresh token.
		return st, nil, runtime.SuspendedStatus, &types.SuspendError{Reason: types.HumanApproval}
	}
	gate := c.gate
	if gate == nil {
		gate = func(context.Context, types.ToolUse) runtime.BatchDecision {
			return runtime.BatchDecision{Outcome: runtime.BatchAllow}
		}
	}
	var res types.ToolResult
	switch d := gate(ctx, head); d.Outcome {
	case runtime.BatchAsk:
		b.Driver.Batch.Ready = false
		enc, cerr := encodeNativeBackend(b)
		if cerr != nil {
			return st, nil, runtime.Continue, cerr
		}
		st.Backend = enc
		return st, nil, runtime.SuspendedStatus, &types.SuspendError{Reason: types.HumanApproval}
	case runtime.BatchDeny, runtime.BatchTaintDenied:
		res = types.ToolResult{
			ID:      head.ID,
			Outcome: types.Failed,
			Error:   &types.ToolError{Kind: types.Permanent, Message: runtime.NotExecutedPrefix + d.Reason},
		}
	default:
		res, err = runApprovedAsk(ctx, env, head)
		if err != nil {
			return st, nil, runtime.Continue, err
		}
	}
	if err := persistAskResult(ctx, &st, res); err != nil {
		return st, nil, runtime.Continue, err
	}
	if err := settleNativePending(&st, res); err != nil {
		return st, nil, runtime.Continue, err
	}
	if env.sink != nil {
		env.sink.Emit(ctx, types.ToolFinished{Turn: env.turn, Result: res})
	}
	if len(st.Pending) > 0 {
		return st, nil, runtime.SuspendedStatus, &types.SuspendError{Reason: types.HumanApproval}
	}
	env.calls = nil
	if c.poll == nil {
		return st, nil, runtime.Continue, nil
	}
	if stop := c.poll(); stop != "" && stop != types.StopCompleted {
		return st, []types.Event{types.Done{Reason: stop}}, runtime.DoneStatus, nil
	}
	return st, nil, runtime.Continue, nil
}

// runApprovedAsk executes the one decided call through the governed call
// path under a prepaid one-call batch: the slot was authorized when the
// batch was first admitted, so the local bound caps the single execution
// and the report's spend never re-charges the run.
func runApprovedAsk(ctx context.Context, env *turnEnv, call types.ToolUse) (types.ToolResult, error) {
	exec := env.c.exec
	if exec == nil {
		exec = func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			return CallTool(ctx, env.c.tools, call.Name, call.Args)
		}
	}
	report, err := runtime.Batch{
		Calls:  []types.ToolUse{call},
		Limits: types.RunLimits{MaxToolCalls: 1},
		Gate: func(context.Context, types.ToolUse) runtime.BatchDecision {
			return runtime.BatchDecision{Outcome: runtime.BatchAllow}
		},
		Exec: func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			return governedAskExec(ctx, env, exec, call)
		},
	}.Run(ctx)
	if err != nil {
		return types.ToolResult{}, err
	}
	if len(report.Results) == 0 {
		return types.ToolResult{}, fmt.Errorf("%w: approved ask %q produced no result", types.ErrCheckpointIncompatible, call.ID)
	}
	res := report.Results[0].Result
	res.ID = call.ID
	return res, nil
}

// governedAskExec runs one call through the governed path with its
// original identity, streaming the start and outcome and rendering an
// ordinary failure as a permanent not-executed result.
func governedAskExec(ctx context.Context, env *turnEnv, exec func(context.Context, types.ToolUse) (types.ToolResult, error), call types.ToolUse) (types.ToolResult, error) {
	if res, ok := validateCompletion(call); !ok {
		return res, nil
	}
	if env.sink != nil {
		env.sink.Emit(ctx, types.ToolStarted{Turn: env.turn, Call: call})
	}
	tctx, endTool := startSpan(ctx, telemetryFrom(ctx), SpanTool,
		types.String(types.KeyToolName, call.Name),
	)
	res, err := exec(tctx, call)
	endTool(types.String(types.KeyToolOutcome, outcomeName(res)))
	if err != nil {
		if ctx.Err() != nil {
			return types.ToolResult{}, ctx.Err()
		}
		if isControlError(err) {
			return types.ToolResult{}, err
		}
		res = types.ToolResult{
			ID:    call.ID,
			Error: &types.ToolError{Kind: types.Permanent, Message: runtime.NotExecutedPrefix + err.Error()},
		}
	}
	res.ID = call.ID
	return res, nil
}

// persistAskResult appends the settled active ask's result in one append
// before any acknowledgement, so the record's populated slot and the
// session log agree on what is durable.
func persistAskResult(ctx context.Context, st *runtime.State, res types.ToolResult) error {
	msg := types.Message{ID: resultID(st.Turn) + "-" + res.ID, Role: types.RoleUser, Blocks: []types.Block{res}}
	if h, ok := historyAppenderFrom(ctx); ok {
		v, err := h.Append(ctx, st.HistoryVersion, msg)
		if err != nil {
			return err
		}
		st.HistoryVersion = v
	}
	if env := turnEnvFrom(ctx); env != nil {
		env.msgs = append(env.msgs, msg)
	}
	return nil
}

// settleNativePending fills the active ask's index-aligned slot with its
// result, removes only the head from the pending queue and clears Ready,
// then writes the updated driver record back into the state. It appends
// no history: the caller persists the result first.
func settleNativePending(st *runtime.State, res types.ToolResult) error {
	b, err := decodeNativeBackend(*st)
	if err != nil {
		return err
	}
	if b.Driver == nil || b.Driver.Batch == nil || len(st.Pending) == 0 {
		return fmt.Errorf("%w: settling without an active ask", types.ErrCheckpointIncompatible)
	}
	head := st.Pending[0]
	settled := false
	for i, cu := range b.Driver.Batch.Calls {
		if cu.ID != head.ID {
			continue
		}
		if b.Driver.Batch.Results[i].ID != "" {
			return fmt.Errorf("%w: batch slot for %q is already settled", types.ErrCheckpointIncompatible, head.ID)
		}
		b.Driver.Batch.Results[i] = res
		settled = true
		break
	}
	if !settled {
		return fmt.Errorf("%w: pending call %q has no batch slot", types.ErrCheckpointIncompatible, head.ID)
	}
	st.Pending = st.Pending[1:]
	b.Driver.Batch.Ready = false
	enc, err := encodeNativeBackend(b)
	if err != nil {
		return err
	}
	st.Backend = enc
	return nil
}

// markAskReady records in the restored state that the active ask was
// decided: the replayed batch phase may attempt the head. A backend
// without a driver record has nothing to mark.
func markAskReady(st *runtime.State) error {
	b, err := decodeNativeBackend(*st)
	if err != nil {
		return err
	}
	if b.Driver == nil || b.Driver.Batch == nil {
		return nil
	}
	b.Driver.Batch.Ready = true
	enc, err := encodeNativeBackend(b)
	if err != nil {
		return err
	}
	st.Backend = enc
	return nil
}

// isControlError reports the errors that steer the run — cancellation,
// suspension, abort and limit termination — which must never render as an
// ordinary tool failure.
func isControlError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	var suspend *types.SuspendError
	var abort *types.AbortError
	var limit *types.LimitExceededError
	return errors.As(err, &suspend) || errors.As(err, &abort) || errors.As(err, &limit)
}

func truncatedTurn(tu types.ToolUse, res types.ToolResult, turn int) types.Message {
	return types.Message{
		ID:   resultID(turn),
		Role: types.RoleUser,
		Blocks: []types.Block{
			tu,
			res,
		},
	}
}

func assistantID(turn int) string { return "assistant-" + strconv.Itoa(turn) }
func resultID(turn int) string    { return "tool-results-" + strconv.Itoa(turn) }
