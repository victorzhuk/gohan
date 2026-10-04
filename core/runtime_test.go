package gohan

import (
	"context"
	"iter"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

// scriptedModel replays a fixed chunk sequence; the request it received is
// kept for assertions. It yields nothing on a timer.
type scriptedModel struct {
	chunks []types.ModelChunk
	req    types.ModelRequest
}

func (m *scriptedModel) Profile() types.ModelProfile { return types.ModelProfile{Name: "scripted"} }

func (m *scriptedModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.req = req
	return func(yield func(types.ModelChunk, error) bool) {
		for _, c := range m.chunks {
			if !yield(c, nil) {
				return
			}
		}
	}
}

// scriptRuntime is a local stepper fake: the model's text streams through
// the sink as component events, the assistant message is appended once and
// the history version advances with that single append.
type scriptRuntime struct {
	model   *scriptedModel
	run     runtime.AgentRun
	histVer int64
	steps   []runtime.State
}

func (s *scriptRuntime) Name() string                         { return "scripted" }
func (s *scriptRuntime) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }

func (s *scriptRuntime) Start(_ context.Context, r runtime.AgentRun) (runtime.State, error) {
	s.run = r
	return runtime.State{HistoryVersion: s.histVer}, nil
}

func (s *scriptRuntime) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	s.steps = append(s.steps, st)
	req, err := s.run.Assemble(ctx, types.AssembleInput{History: s.run.History.Messages, Input: s.run.Input})
	if err != nil {
		return st, nil, runtime.Continue, err
	}
	var text []string
	for chunk, err := range s.model.Generate(ctx, req) {
		if err != nil {
			return st, nil, runtime.Continue, err
		}
		if chunk.Kind != types.DeltaText {
			continue
		}
		text = append(text, chunk.Delta)
		if sink, ok := types.SinkFrom(ctx); ok {
			sink.Emit(ctx, types.TextDelta{Turn: st.Turn, MessageID: "assistant-1", Delta: chunk.Delta})
		}
	}
	msg := types.Message{ID: "assistant-1", Role: types.RoleAssistant, Blocks: []types.Block{types.Text{Text: strings.Join(text, "")}}}
	if sink, ok := types.SinkFrom(ctx); ok {
		sink.Emit(ctx, types.AssistantMessage{Turn: st.Turn, Message: msg})
	}
	s.histVer++
	return runtime.State{Turn: st.Turn + 1, HistoryVersion: s.histVer, Usage: types.Usage{InputTokens: 3}},
		nil, runtime.DoneStatus, nil
}

func scriptRun(m *scriptedModel, input ...types.Message) runtime.AgentRun {
	return runtime.AgentRun{
		Model: m,
		Input: input,
		Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
			req := types.ModelRequest{Messages: slices.Clone(in.History)}
			req.Messages = append(req.Messages, in.Input...)
			return req, nil
		},
	}
}

func driveCollect(seq iter.Seq2[types.Event, error]) ([]types.Event, error) {
	var evs []types.Event
	for e, err := range seq {
		if err != nil {
			return evs, err
		}
		evs = append(evs, e)
	}
	return evs, nil
}

func TestRuntimeState(t *testing.T) {
	ctx := context.Background()

	t.Run("runtime.plain-answer", func(t *testing.T) {
		m := &scriptedModel{chunks: []types.ModelChunk{{Kind: types.DeltaText, Delta: "hi"}}}
		rt := &scriptRuntime{model: m, histVer: 2}
		evs, err := driveCollect(Drive(ctx, rt, scriptRun(m)))
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
		if len(evs) != 3 {
			t.Fatalf("got %d events, want 3: %v", len(evs), evs)
		}
		delta, ok := evs[0].(types.TextDelta)
		if !ok || delta.Delta != "hi" {
			t.Fatalf("first event %v, want TextDelta hi", evs[0])
		}
		asst, ok := evs[1].(types.AssistantMessage)
		if !ok || len(asst.Message.Blocks) != 1 || asst.Message.Blocks[0].(types.Text).Text != "hi" {
			t.Fatalf("second event %v, want AssistantMessage hi", evs[1])
		}
		done, ok := evs[2].(types.Done)
		if !ok || done.Reason != types.StopCompleted {
			t.Fatalf("last event %v, want Done completed", evs[2])
		}
	})

	t.Run("state round-trips through Start and Step", func(t *testing.T) {
		m := &scriptedModel{chunks: []types.ModelChunk{{Kind: types.DeltaText, Delta: "x"}}}
		rt := &scriptRuntime{model: m, histVer: 7}
		evs, err := driveCollect(Drive(ctx, rt, scriptRun(m)))
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
		if len(evs) == 0 {
			t.Fatal("no events")
		}
		if len(rt.steps) != 1 {
			t.Fatalf("got %d steps, want 1", len(rt.steps))
		}
		started := rt.steps[0]
		if started.Turn != 0 || started.HistoryVersion != 7 {
			t.Fatalf("Start state %+v, want turn 0 version 7", started)
		}
		last := evs[len(evs)-1].(types.Done)
		if last.Usage.InputTokens != 3 {
			t.Fatalf("Done usage %+v, want the state usage", last.Usage)
		}
	})

	t.Run("message blocks survive a step unchanged", func(t *testing.T) {
		input := types.Message{ID: "u1", Role: types.RoleUser, Blocks: []types.Block{
			types.Text{Text: "hello"},
			types.Text{Text: "world"},
		}}
		m := &scriptedModel{chunks: []types.ModelChunk{{Kind: types.DeltaText, Delta: "ok"}}}
		rt := &scriptRuntime{model: m, histVer: 1}
		if _, err := driveCollect(Drive(ctx, rt, scriptRun(m, input))); err != nil {
			t.Fatalf("drive: %v", err)
		}
		if len(m.req.Messages) != 1 {
			t.Fatalf("request carries %d messages, want 1", len(m.req.Messages))
		}
		if !reflect.DeepEqual(m.req.Messages[0].Blocks, input.Blocks) {
			t.Fatalf("blocks changed: %+v", m.req.Messages[0].Blocks)
		}
	})

	t.Run("history version advances exactly once per append", func(t *testing.T) {
		m := &scriptedModel{chunks: []types.ModelChunk{
			{Kind: types.DeltaText, Delta: "a"},
			{Kind: types.DeltaText, Delta: "b"},
			{Kind: types.DeltaText, Delta: "c"},
		}}
		rt := &scriptRuntime{model: m, histVer: 5}
		evs, err := driveCollect(Drive(ctx, rt, scriptRun(m)))
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
		if rt.histVer != 6 {
			t.Fatalf("history version %d, want 6", rt.histVer)
		}
		asst := evs[3].(types.AssistantMessage)
		if got := asst.Message.Blocks[0].(types.Text).Text; got != "abc" {
			t.Fatalf("assistant text %q, want abc", got)
		}
	})

	t.Run("drive text arrives through the sink", func(t *testing.T) {
		m := &scriptedModel{chunks: []types.ModelChunk{
			{Kind: types.DeltaText, Delta: "h"},
			{Kind: types.DeltaText, Delta: "i"},
		}}
		rt := &scriptRuntime{model: m, histVer: 0}
		evs, err := driveCollect(Drive(ctx, rt, scriptRun(m)))
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
		var got []string
		for _, e := range evs {
			if d, ok := e.(types.TextDelta); ok {
				got = append(got, d.Delta)
			}
		}
		if len(got) != 2 || got[0] != "h" || got[1] != "i" {
			t.Fatalf("deltas %v, want h,i", got)
		}
		if _, ok := evs[len(evs)-1].(types.Done); !ok {
			t.Fatalf("last event %v, want Done", evs[len(evs)-1])
		}
	})
}
