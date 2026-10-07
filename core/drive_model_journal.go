package gohan

import (
	"context"
	"fmt"
	"strings"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// modelStepKey pins a model call's journal entry to the run's session and
// the call's turn.
func modelStepKey(ctx context.Context, turn int) (types.CallKey, bool) {
	info, ok := types.RunInfoFrom(ctx)
	if !ok || info.SessionID == "" {
		return types.CallKey{}, false
	}
	return types.CallKey{SessionID: info.SessionID, CallID: fmt.Sprintf("model/%d", turn)}, true
}

func modelFingerprint(turn int) stores.Fingerprint {
	return stores.Fingerprint(fmt.Sprintf("model:%d", turn))
}

// completeModelStep records the streamed response once the winner's stream
// ends. Attempts and the winner's endpoint come from the hedge sink on the
// call's context: 1 when the timer never fired, 2 when a second attempt
// started. The hedged pair journals as one step; the loser has no entry.
func completeModelStep(ctx context.Context, j stores.Journal, k types.CallKey, sink *types.HedgeSink, reply, reasoning string, calls []types.ToolUse) error {
	res := types.ToolResult{ID: k.CallID, Outcome: types.Succeeded}
	if reply != "" {
		res.Content = append(res.Content, types.Text{Text: reply})
	}
	if reasoning != "" {
		res.Content = append(res.Content, types.Reasoning{Text: reasoning})
	}
	for _, call := range calls {
		res.Content = append(res.Content, call)
	}
	mc := stores.ModelCompletion{Attempts: 1}
	if sink != nil {
		mc.Attempts, mc.Endpoint = sink.Outcome()
	}
	if cmpl, ok := j.(stores.ModelCompleter); ok {
		return cmpl.CompleteModel(ctx, k, res, mc)
	}
	return j.Complete(ctx, k, res)
}

// replayModelStep restores the recorded model response instead of
// re-issuing the call. Tool calls parked in the entry re-enter the batch;
// a final answer re-enters history and the terminal event.
func replayModelStep(ctx context.Context, st runtime.State, env *turnEnv, entry stores.Entry) (runtime.State, []types.Event, runtime.Status, error) {
	var (
		reply     strings.Builder
		reasoning strings.Builder
		calls     []types.ToolUse
	)
	for _, b := range entry.Result.Content {
		switch t := b.(type) {
		case types.Text:
			reply.WriteString(t.Text)
		case types.Reasoning:
			reasoning.WriteString(t.Text)
		case types.ToolUse:
			calls = append(calls, t)
		}
	}
	if len(calls) > 0 {
		env.flushDeltas(ctx)
		env.reasoning = reasoning.String()
		env.calls = calls
		return st, nil, runtime.Continue, nil
	}
	asst := types.Message{ID: assistantID(env.turn), Role: types.RoleAssistant, Blocks: []types.Block{types.Text{Text: reply.String()}}}
	retainReasoning(&asst, reasoning.String())
	env.msgs = append(env.msgs, asst)
	if h, ok := historyAppenderFrom(ctx); ok {
		v, err := h.Append(ctx, st.HistoryVersion, asst)
		if err != nil {
			return st, nil, runtime.Continue, err
		}
		st.HistoryVersion = v
	}
	env.flushDeltas(ctx)
	return st, []types.Event{types.AssistantMessage{Turn: env.turn, Message: asst}}, runtime.DoneStatus, nil
}
