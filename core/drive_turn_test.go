package gohan

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

// scriptTurns replays one chunk list per model call and keeps every request
// it received.
type scriptTurns struct {
	turns [][]types.ModelChunk
	reqs  []types.ModelRequest
	n     int
}

func (m *scriptTurns) model() types.ModelFunc {
	return func(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			m.reqs = append(m.reqs, req)
			if m.n >= len(m.turns) {
				yield(types.ModelChunk{}, errors.New("script exhausted"))
				return
			}
			for _, c := range m.turns[m.n] {
				if !yield(c, nil) {
					return
				}
			}
			m.n++
		}
	}
}

func toolCall(id, name string) types.ModelChunk {
	return types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: id, Name: name, Args: jsontext.Value(`{}`)}}
}

func turnTextChunk(s string) types.ModelChunk {
	return types.ModelChunk{Kind: types.DeltaText, Delta: s}
}

// echoTool is a ReadOnly tool that answers with a fixed string.
type echoTool struct {
	ran int
}

func (t *echoTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "echo", Effect: types.ReadOnly}
}

func (t *echoTool) Call(_ context.Context, _ jsontext.Value) (types.ToolResult, error) {
	t.ran++
	return types.ToolResult{Content: []types.Block{types.Text{Text: "echoed"}}, Outcome: types.Succeeded}, nil
}

func turnAssemble(req *types.ModelRequest) func(context.Context, types.AssembleInput) (types.ModelRequest, error) {
	return func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
		return types.ModelRequest{Messages: in.History}, nil
	}
}

func driveTurnCollect(seq func(yield func(types.Event, error) bool)) ([]types.Event, error) {
	var evs []types.Event
	var err error
	seq(func(e types.Event, eErr error) bool {
		if eErr != nil {
			err = eErr
			return false
		}
		evs = append(evs, e)
		return true
	})
	return evs, err
}

// sinkInto returns a Sink appending every step event to dst, skipping the
// streaming deltas the round-trip scenarios do not list.
func sinkInto(dst *[]types.Event) types.Sink {
	return sinkFunc(func(_ context.Context, e types.Event) {
		switch e.(type) {
		case types.TextDelta, types.ToolArgsDelta, types.AssistantMessage:
			return
		}
		*dst = append(*dst, e)
	})
}

type sinkFunc func(ctx context.Context, e types.Event)

func (f sinkFunc) Emit(ctx context.Context, e types.Event) { f(ctx, e) }

func turnToolset(tools ...types.Tool) ToolSetFunc {
	return ToolSetFunc(func(name string) (types.Tool, bool) {
		for _, t := range tools {
			if t.Spec().Name == name {
				return t, true
			}
		}
		return nil, false
	})
}

func TestRuntimeTurns(t *testing.T) {
	ctx := context.Background()

	t.Run("runtime.tool-round-trip", func(t *testing.T) {
		m := &scriptTurns{turns: [][]types.ModelChunk{
			{toolCall("c1", "echo"), {Finish: types.FinishToolUse}},
			{turnTextChunk("done")},
		}}
		tool := &echoTool{}
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			tools:    turnToolset(tool),
			maxTurns: 6,
		}
		var evs []types.Event
		var iterErr error
		driveTurns(types.WithSink(ctx, sinkInto(&evs)), cfg, func(e types.Event, err error) bool {
			if err != nil {
				iterErr = err
				return false
			}
			evs = append(evs, e)
			return true
		})
		if iterErr != nil {
			t.Fatalf("drive: %v", iterErr)
		}
		if len(evs) != 4 {
			t.Fatalf("got %d events, want 4: %v", len(evs), evs)
		}
		if _, ok := evs[0].(types.ToolStarted); !ok {
			t.Fatalf("first event %v, want ToolStarted", evs[0])
		}
		fin, ok := evs[1].(types.ToolFinished)
		if !ok || fin.Result.ID != "c1" || fin.Result.Outcome != types.Succeeded {
			t.Fatalf("second event %v, want ToolFinished c1", evs[1])
		}
		asst, ok := evs[2].(types.AssistantMessage)
		if !ok || asst.Turn != 2 {
			t.Fatalf("third event %v, want second-turn AssistantMessage", evs[2])
		}
		done, ok := evs[3].(types.Done)
		if !ok || done.Reason != types.StopCompleted {
			t.Fatalf("last event %v, want Done completed", evs[3])
		}
		if m.n != 2 {
			t.Fatalf("got %d model calls, want 2", m.n)
		}
		if tool.ran != 1 {
			t.Fatalf("tool ran %d times, want 1", tool.ran)
		}
	})

	t.Run("runtime.parallel-calls-ordering", func(t *testing.T) {
		m := &scriptTurns{turns: [][]types.ModelChunk{
			{toolCall("c1", "echo"), toolCall("c2", "echo"), {Finish: types.FinishToolUse}},
			{turnTextChunk("done")},
		}}
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			tools:    turnToolset(&echoTool{}),
			maxTurns: 6,
		}
		if _, err := driveTurnCollect(func(y func(types.Event, error) bool) { driveTurns(ctx, cfg, y) }); err != nil {
			t.Fatalf("drive: %v", err)
		}
		if len(m.reqs) != 2 {
			t.Fatalf("got %d model calls, want 2", len(m.reqs))
		}
		var order []string
		for _, msg := range m.reqs[1].Messages {
			for _, b := range msg.Blocks {
				switch blk := b.(type) {
				case types.ToolUse:
					order = append(order, "use:"+blk.ID)
				case types.ToolResult:
					order = append(order, "res:"+blk.ID)
				}
			}
		}
		want := "use:c1,use:c2,res:c1,res:c2"
		if got := strings.Join(order, ","); got != want {
			t.Fatalf("call order %q, want %q", got, want)
		}
	})

	t.Run("runtime.max-turns", func(t *testing.T) {
		toolTurn := []types.ModelChunk{toolCall("c", "echo"), {Finish: types.FinishToolUse}}
		m := &scriptTurns{turns: [][]types.ModelChunk{toolTurn, toolTurn, toolTurn, toolTurn}}
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			tools:    turnToolset(&echoTool{}),
			maxTurns: 3,
		}
		evs, err := driveTurnCollect(func(y func(types.Event, error) bool) { driveTurns(ctx, cfg, y) })
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
		if len(evs) != 1 {
			t.Fatalf("got %d events, want 1: %v", len(evs), evs)
		}
		done, ok := evs[0].(types.Done)
		if !ok || done.Reason != types.StopLimit {
			t.Fatalf("event %v, want Done limit", evs[0])
		}
		if m.n != 3 {
			t.Fatalf("got %d model calls, want 3", m.n)
		}
	})

	t.Run("runtime.cancellation", func(t *testing.T) {
		started := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		m := &scriptTurns{}
		cfg := turnConfig{
			model: func(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
				m.reqs = append(m.reqs, req)
				return func(yield func(types.ModelChunk, error) bool) {
					if !yield(turnTextChunk("part"), nil) {
						return
					}
					started <- struct{}{}
					<-ctx.Done()
					yield(types.ModelChunk{}, ctx.Err())
				}
			},
			assemble: turnAssemble(nil),
			maxTurns: 6,
		}
		done := make(chan error, 1)
		go func() {
			_, err := driveTurnCollect(func(y func(types.Event, error) bool) { driveTurns(ctx, cfg, y) })
			done <- err
		}()
		<-started
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("error %v, want context.Canceled", err)
		}
	})

	t.Run("side effect finishes under the cancel shield", func(t *testing.T) {
		finished := make(chan struct{}, 1)
		effect := &blockingTool{finished: finished}
		started := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		cfg := turnConfig{
			model: func(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
				return func(yield func(types.ModelChunk, error) bool) {
					if !yield(toolCall("c1", "push"), nil) {
						return
					}
					started <- struct{}{}
					<-ctx.Done()
					yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
				}
			},
			assemble: turnAssemble(nil),
			tools:    turnToolset(effect),
			maxTurns: 6,
		}
		done := make(chan error, 1)
		go func() {
			_, err := driveTurnCollect(func(y func(types.Event, error) bool) { driveTurns(ctx, cfg, y) })
			done <- err
		}()
		<-started
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("error %v, want context.Canceled", err)
		}
		select {
		case <-finished:
		default:
			t.Fatal("side effect did not finish under the shield")
		}
		if effect.ran != 1 {
			t.Fatalf("side effect ran %d times, want 1", effect.ran)
		}
	})

	t.Run("signal cancel stops at the safe point", func(t *testing.T) {
		m := &scriptTurns{turns: [][]types.ModelChunk{
			{toolCall("c1", "echo"), {Finish: types.FinishToolUse}},
			{turnTextChunk("done")},
		}}
		calls := 0
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			tools:    turnToolset(&echoTool{}),
			maxTurns: 6,
			poll: func() types.StopReason {
				calls++
				return types.StopCancelled
			},
		}
		evs, err := driveTurnCollect(func(y func(types.Event, error) bool) { driveTurns(ctx, cfg, y) })
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
		done, ok := evs[len(evs)-1].(types.Done)
		if !ok || done.Reason != types.StopCancelled {
			t.Fatalf("last event %v, want Done cancelled", evs[len(evs)-1])
		}
		if m.n != 1 {
			t.Fatalf("got %d model calls, want 1", m.n)
		}
		if calls != 1 {
			t.Fatalf("poll ran %d times, want 1", calls)
		}
	})

	t.Run("truncated args retry with a larger allowance", func(t *testing.T) {
		m := &scriptTurns{turns: [][]types.ModelChunk{
			{toolCall("c1", "echo"), {Finish: types.FinishMaxTokens}},
			{turnTextChunk("done")},
		}}
		cfg := turnConfig{
			model:     m.model(),
			assemble:  turnAssemble(nil),
			tools:     turnToolset(&echoTool{}),
			maxTurns:  6,
			maxTokens: 4096,
			onTruncated: func(tu types.ToolUse, finish types.FinishReason, attempt int) (int, *types.ToolResult, error) {
				if attempt != 1 || finish != types.FinishMaxTokens || tu.ID != "c1" {
					t.Errorf("unexpected decision input: %d %s %+v", attempt, finish, tu)
					return 0, nil, nil
				}
				res := types.ToolResult{
					ID:      tu.ID,
					Content: []types.Block{types.Text{Text: "truncated"}},
					Outcome: types.Failed,
					Error:   &types.ToolError{Kind: types.Permanent, Message: "truncated"},
				}
				return 8192, &res, nil
			},
		}
		evs, err := driveTurnCollect(func(y func(types.Event, error) bool) { driveTurns(ctx, cfg, y) })
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
		if done, ok := evs[len(evs)-1].(types.Done); !ok || done.Reason != types.StopCompleted {
			t.Fatalf("last event %v, want Done completed", evs[len(evs)-1])
		}
		if m.n != 2 {
			t.Fatalf("got %d model calls, want 2", m.n)
		}
		if got := m.reqs[1].Options.MaxTokens; got != 8192 {
			t.Fatalf("retry allowance %d, want 8192", got)
		}
		var sawTruncation bool
		for _, msg := range m.reqs[1].Messages {
			for _, b := range msg.Blocks {
				if res, ok := b.(types.ToolResult); ok && res.ID == "c1" {
					sawTruncation = true
				}
			}
		}
		if !sawTruncation {
			t.Fatal("truncation result not sent back to the model")
		}
		if m.reqs[0].Options.MaxTokens != 4096 {
			t.Fatalf("first allowance %d, want 4096", m.reqs[0].Options.MaxTokens)
		}
	})

	t.Run("repair turn", func(t *testing.T) {
		m := &scriptTurns{turns: [][]types.ModelChunk{
			{turnTextChunk("bad")},
			{turnTextChunk("good")},
		}}
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			maxTurns: 6,
			repair: func(text string, attempt int) (types.Message, bool, error) {
				if attempt == 1 && text == "bad" {
					return types.Message{ID: "repair-1", Role: types.RoleUser,
						Blocks: []types.Block{types.Text{Text: "fix the output"}}}, true, nil
				}
				return types.Message{}, false, nil
			},
		}
		evs, err := driveTurnCollect(func(y func(types.Event, error) bool) { driveTurns(ctx, cfg, y) })
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
		asst, ok := evs[len(evs)-2].(types.AssistantMessage)
		if !ok {
			t.Fatalf("event %v, want AssistantMessage", evs[len(evs)-2])
		}
		if got := asst.Message.Blocks[0].(types.Text).Text; got != "good" {
			t.Fatalf("assistant text %q, want good", got)
		}
		if done, ok := evs[len(evs)-1].(types.Done); !ok || done.Reason != types.StopCompleted {
			t.Fatalf("last event %v, want Done completed", evs[len(evs)-1])
		}
		if m.n != 2 {
			t.Fatalf("got %d model calls, want 2", m.n)
		}
		last := m.reqs[1].Messages
		if len(last) < 2 {
			t.Fatalf("repair request carries %d messages, want the reply and the repair prompt", len(last))
		}
		if reply := last[len(last)-2].Blocks[0].(types.Text).Text; reply != "bad" {
			t.Fatalf("replied text %q, want bad", reply)
		}
		if prompt := last[len(last)-1]; prompt.ID != "repair-1" {
			t.Fatalf("repair prompt %+v, want repair-1", prompt)
		}
	})
}

// askTool is a SideEffect tool the batch gate routes through an approval
// ask until the gate allows it.
type askTool struct {
	ran int
}

func (t *askTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "ask_tool", Effect: types.SideEffect}
}

func (t *askTool) Call(_ context.Context, _ jsontext.Value) (types.ToolResult, error) {
	t.ran++
	return types.ToolResult{Content: []types.Block{types.Text{Text: "did it"}}, Outcome: types.Succeeded}, nil
}

func turnBatchGate(ask func(types.ToolUse) bool) runtime.BatchGate {
	return func(_ context.Context, call types.ToolUse) runtime.BatchDecision {
		if ask(call) {
			return runtime.BatchDecision{Outcome: runtime.BatchAsk}
		}
		return runtime.BatchDecision{Outcome: runtime.BatchAllow}
	}
}

func TestRuntimeBatchTurn(t *testing.T) {
	ctx := context.Background()

	t.Run("runtime.batch-ask-after-allowed", func(t *testing.T) {
		m := &scriptTurns{turns: [][]types.ModelChunk{
			{toolCall("c1", "echo"), toolCall("c2", "ask_tool"), toolCall("c3", "echo"), {Finish: types.FinishToolUse}},
			{toolCall("c4", "ask_tool"), {Finish: types.FinishToolUse}},
			{turnTextChunk("done")},
		}}
		echo := &echoTool{}
		ask := &askTool{}
		decisions := 0
		askCount := 0
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			tools:    turnToolset(echo, ask),
			maxTurns: 6,
			gate: turnBatchGate(func(call types.ToolUse) bool {
				decisions++
				if call.Name != "ask_tool" {
					return false
				}
				askCount++
				return askCount == 1
			}),
		}
		var evs []types.Event
		_, err := driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(types.WithSink(ctx, sinkInto(&evs)), cfg, y)
		})
		var susp *types.SuspendError
		if !errors.As(err, &susp) {
			t.Fatalf("err = %v, want SuspendError", err)
		}
		if susp.Reason != types.HumanApproval {
			t.Fatalf("reason = %v, want HumanApproval", susp.Reason)
		}
		if susp.Payload != nil {
			t.Fatalf("payload = %v, want none: the lifecycle owns the approval payload", susp.Payload)
		}
		if echo.ran != 2 {
			t.Fatalf("read-only tool ran %d times, want 2", echo.ran)
		}
		// The gate settled all three calls before anything executed.
		if decisions != 3 {
			t.Fatalf("gate decisions = %d, want 3", decisions)
		}
		if ask.ran != 0 {
			t.Fatalf("asked tool executed before approval")
		}

		// Resume with the approval in place: only the asked call executes.
		evs, err = driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(types.WithSink(ctx, sinkInto(&evs)), cfg, y)
		})
		if err != nil {
			t.Fatalf("resume drive: %v", err)
		}
		if ask.ran != 1 {
			t.Fatalf("asked tool ran %d times, want 1 after approval", ask.ran)
		}
		done, ok := evs[len(evs)-1].(types.Done)
		if !ok || done.Reason != types.StopCompleted {
			t.Fatalf("last event %v, want Done completed", evs[len(evs)-1])
		}
	})
}

// blockingTool is a SideEffect tool whose external call completes and then
// observes the cancelled context, the way the injected cancel shield hands
// the error back after the effect finished.
type blockingTool struct {
	finished chan struct{}
	ran      int
}

func (t *blockingTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "push", Effect: types.SideEffect}
}

func (t *blockingTool) Call(ctx context.Context, _ jsontext.Value) (types.ToolResult, error) {
	t.ran++
	t.finished <- struct{}{}
	return types.ToolResult{}, ctx.Err()
}
