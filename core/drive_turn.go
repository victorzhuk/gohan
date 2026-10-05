package gohan

import (
	"context"
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
}

// turnEnv carries the per-run state the two effects share across their
// boundaries: the loop position, the working history and the pending batch
// the model effect hands to the batch effect. The runtime State only
// carries what resume needs; this scope lives for one driveTurns call and
// travels in the context.
type turnEnv struct {
	c         turnConfig
	sink      types.Sink
	msgs      []types.Message
	turn      int
	maxTokens int
	truncated int
	repaired  int
	used      int
	calls     []types.ToolUse
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
			for _, ev := range events {
				yield(ev, nil)
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
// the batch effect. A final answer returns the assistant message and Done
// as the events a terminal status carries.
func modelEffect(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	env := turnEnvFrom(ctx)
	c := env.c
	if notes := steerNotesFrom(ctx); notes != nil {
		// The steers the lifecycle drained at its safe point enter the
		// working history here, so the next request carries them.
		env.msgs = append(env.msgs, notes.take()...)
	}
	req, err := c.assemble(ctx, types.AssembleInput{History: env.msgs})
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
	fmt.Println("DBG modelEffect results", n, "msgs", len(env.msgs))
	st.Turn = env.turn

	var sb strings.Builder
	var reasoning strings.Builder
	var calls []types.ToolUse
	finish := types.FinishStop
	mctx, endChat := startSpan(ctx, telemetryFrom(ctx), SpanChat)
	for chunk, err := range c.model(mctx, req) {
		if err != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			endChat(types.String(types.KeySuspendReason, "error"))
			env.flushDeltas(ctx)
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
	if c.resultPreview != nil {
		for _, d := range c.resultPreview(asst) {
			env.emitDelta(ctx, types.ResultDelta{Turn: env.turn, MessageID: asst.ID, Delta: d})
		}
	}
	env.flushDeltas(ctx)
	ev := types.AssistantMessage{Turn: env.turn, Message: asst}
	if env.sink != nil {
		env.sink.Emit(ctx, ev)
	}
	return st, []types.Event{ev, types.Done{Reason: types.StopCompleted}}, runtime.DoneStatus, nil
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

// batchEffect settles one turn's batch under the batch protocol: the gate
// decides every call before the first one executes, allowed calls run in
// call order through the scheduler, denials render as Failed(Permanent)
// results at their index, and the first ask suspends the batch as a
// SuspendError the caller persists — never an ordinary failed tool result.
// It appends the assistant message with its calls and the settled results
// in call order, and adds the batch's reservation spend to the per-run
// scope. The batch runs even when the context is already cancelled, so an
// in-flight side effect finishes under the injected shield; after it the
// run stops.
func batchEffect(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	env := turnEnvFrom(ctx)
	c := env.c
	turn := env.turn
	calls := env.calls
	if len(calls) == 0 && len(st.Pending) > 0 && pendingReplayFrom(ctx) {
		// A resumed or recovered drive re-enters the batch phase from the
		// checkpointed state alone: the per-run scope that parks a model
		// turn's calls is gone, so the pending calls replay from the state.
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
	*msgs = append(*msgs, asst)

	if h, ok := historyAppenderFrom(ctx); ok {
		v, aerr := AppendBeforeBatch(ctx, h, st.HistoryVersion, asst)
		if aerr != nil {
			return st, nil, runtime.Continue, aerr
		}
		st.HistoryVersion = v
	}

	refund := func() {}
	if c.reserve != nil {
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
			if res, ok := validateCompletion(call); !ok {
				return res, nil
			}
			if env.sink != nil {
				env.sink.Emit(ctx, types.ToolStarted{Turn: turn, Call: call})
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
		},
	}.Run(ctx)
	if err != nil {
		refund()
		return st, nil, runtime.Continue, err
	}

	unsettled := map[string]bool{}
	if report.Suspend != nil {
		unsettled[report.Suspend.Call.ID] = true
		for _, p := range report.Suspend.Pending {
			unsettled[p.ID] = true
		}
	}
	results := types.Message{ID: resultID(turn), Role: types.RoleUser}
	for _, br := range report.Results {
		if unsettled[br.Call.ID] {
			continue
		}
		res := br.Result
		res.ID = br.Call.ID
		results.Blocks = append(results.Blocks, res)
		if env.sink != nil {
			env.sink.Emit(ctx, types.ToolFinished{Turn: turn, Result: res})
		}
	}
	*msgs = append(*msgs, results)
	env.used += report.Spent
	if report.Suspend != nil {
		st.Pending = report.Suspend.Pending
		return st, nil, runtime.SuspendedStatus, &types.SuspendError{Reason: types.AwaitingBatch, Payload: *report.Suspend}
	}
	if h, ok := historyAppenderFrom(ctx); ok {
		v, aerr := h.Append(ctx, st.HistoryVersion, results)
		if aerr != nil {
			return st, nil, runtime.Continue, aerr
		}
		st.HistoryVersion = v
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
