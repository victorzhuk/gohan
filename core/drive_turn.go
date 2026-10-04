package gohan

import (
	"context"
	"slices"
	"strconv"
	"strings"

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
}

// driveTurns runs one model call per turn, executes the turn's tool calls
// in call order, appends the results in call order and counts MaxTurns over
// model calls. A steer-drain turn counts too, because it is one more model
// call after a drain. Deltas and tool events stream through the sink; the
// iterator carries the final assistant message, Done and errors.
func driveTurns(ctx context.Context, c turnConfig, yield func(types.Event, error) bool) {
	sink, _ := types.SinkFrom(ctx)
	msgs := slices.Clone(c.history)
	if len(c.input) > 0 {
		msgs = append(msgs, c.input...)
	}
	turn := 0
	maxTokens := c.maxTokens
	truncated, repaired := 0, 0
	for {
		if turn >= c.maxTurns {
			yield(types.Done{Reason: types.StopLimit}, nil)
			return
		}
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		req, err := c.assemble(ctx, types.AssembleInput{History: msgs})
		if err != nil {
			yield(nil, err)
			return
		}
		if maxTokens > 0 {
			req.Options.MaxTokens = maxTokens
		}
		turn++

		var sb strings.Builder
		var calls []types.ToolUse
		finish := types.FinishStop
		for chunk, err := range c.model(ctx, req) {
			if err != nil {
				if ctx.Err() != nil {
					err = ctx.Err()
				}
				yield(nil, err)
				return
			}
			switch chunk.Kind {
			case types.DeltaText:
				sb.WriteString(chunk.Delta)
				if sink != nil {
					sink.Emit(ctx, types.TextDelta{Turn: turn, Delta: chunk.Delta})
				}
			case types.DeltaToolArgs:
				if chunk.ToolUse != nil && sink != nil {
					sink.Emit(ctx, types.ToolArgsDelta{Turn: turn, CallID: chunk.ToolUse.ID, Name: chunk.ToolUse.Name, Delta: chunk.Delta})
				}
			}
			if chunk.ToolUse != nil {
				calls = append(calls, *chunk.ToolUse)
			}
			if chunk.Finish != "" && chunk.Finish != types.FinishStop {
				finish = chunk.Finish
			}
		}

		// A truncated finish earns a retry turn before anything executes:
		// the pending calls are never run, the truncation result goes back
		// to the model and the retry carries the larger allowance.
		if finish == types.FinishMaxTokens && c.onTruncated != nil {
			tu := types.ToolUse{}
			if len(calls) > 0 {
				tu = calls[len(calls)-1]
			}
			truncated++
			retry, result, err := c.onTruncated(tu, finish, truncated)
			if err != nil {
				yield(nil, err)
				return
			}
			if retry > 0 {
				maxTokens = retry
				msgs = append(msgs, truncatedTurn(tu, *result, turn))
				continue
			}
		}

		if len(calls) > 0 {
			stop, callErr := runCalls(ctx, c, sink, turn, calls, &msgs)
			if callErr != nil {
				yield(nil, callErr)
				return
			}
			if stop != "" {
				yield(types.Done{Reason: stop}, nil)
				return
			}
			continue
		}

		reply := sb.String()
		if c.repair != nil {
			repaired++
			prompt, ok, err := c.repair(reply, repaired)
			if err != nil {
				yield(nil, err)
				return
			}
			if ok {
				msgs = append(msgs,
					types.Message{ID: assistantID(turn), Role: types.RoleAssistant, Blocks: []types.Block{types.Text{Text: reply}}},
					prompt)
				continue
			}
		}
		asst := types.Message{ID: assistantID(turn), Role: types.RoleAssistant, Blocks: []types.Block{types.Text{Text: reply}}}
		msgs = append(msgs, asst)
		ev := types.AssistantMessage{Turn: turn, Message: asst}
		if sink != nil {
			sink.Emit(ctx, ev)
		}
		yield(ev, nil)
		yield(types.Done{Reason: types.StopCompleted}, nil)
		return
	}
}

// runCalls executes the batch in call order and appends the assistant
// message with its calls and one result per call. The batch runs even when
// the context is already cancelled, so an in-flight side effect finishes
// under the injected shield; after it the run stops. It returns a stop
// reason when the safe point ends the run, the cancellation error when a
// call observed it, or "" and nil when the loop continues.
func runCalls(ctx context.Context, c turnConfig, sink types.Sink, turn int, calls []types.ToolUse, msgs *[]types.Message) (types.StopReason, error) {
	asst := types.Message{ID: assistantID(turn), Role: types.RoleAssistant}
	for _, cu := range calls {
		asst.Blocks = append(asst.Blocks, cu)
	}
	*msgs = append(*msgs, asst)

	results := types.Message{ID: resultID(turn), Role: types.RoleUser}
	for _, cu := range calls {
		if sink != nil {
			sink.Emit(ctx, types.ToolStarted{Turn: turn, Call: cu})
		}
		res, err := CallTool(ctx, c.tools, cu.Name, cu.Args)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			res = types.ToolResult{
				ID:      cu.ID,
				Outcome: types.Failed,
				Error:   &types.ToolError{Kind: types.Permanent, Message: "not_executed: " + err.Error()},
			}
		}
		res.ID = cu.ID
		results.Blocks = append(results.Blocks, res)
		if sink != nil {
			sink.Emit(ctx, types.ToolFinished{Turn: turn, Result: res})
		}
	}
	*msgs = append(*msgs, results)
	if c.poll == nil {
		return "", nil
	}
	if stop := c.poll(); stop != "" && stop != types.StopCompleted {
		return stop, nil
	}
	return "", nil
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
