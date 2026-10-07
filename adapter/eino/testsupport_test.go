package eino

import (
	"context"
	"encoding/json"
	"iter"
	"slices"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/conformance"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

const (
	assistantID      = "assistant-1"
	harnessMaxTurns  = 3
	maxTurnsMetricNm = "gohan.max_turns.reached"
)

func callUse(name string) types.ToolUse {
	return types.ToolUse{ID: "call_" + name, Name: name}
}

// recordTel captures the counters the governed seams emit through the
// telemetry port.
type recordTel struct {
	mu     sync.Mutex
	counts map[string]int64
}

func newRecordTel() *recordTel {
	return &recordTel{counts: map[string]int64{}}
}

func (t *recordTel) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (t *recordTel) Count(_ context.Context, name string, n int64, _ ...types.Attr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counts[name] += n
}

func (*recordTel) Record(context.Context, string, float64, ...types.Attr) {}

func (t *recordTel) counted(name string) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.counts[name]
}

// governedModel plays the production decorator's role for the harness: it
// records the guard step, mirrors Drive's MaxTurns budget over model calls
// and forwards text deltas to the run-scoped sink the ctx carries.
type governedModel struct {
	inner types.Model
	h     *einoHarness
}

func (m *governedModel) Profile() types.ModelProfile { return m.inner.Profile() }

func (m *governedModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		m.h.record(conformance.StepGuard)
		if m.h.maxTurns > 0 && m.h.turns() >= m.h.maxTurns {
			if tel := types.TelemetryFrom(ctx); tel != nil {
				tel.Count(ctx, maxTurnsMetricNm, 1)
			}
			yield(types.ModelChunk{}, &types.LimitExceededError{Limit: "MaxTurns", Value: float64(m.h.maxTurns)})
			return
		}
		if err := ctx.Err(); err != nil {
			yield(types.ModelChunk{}, err)
			return
		}
		sink, _ := types.SinkFrom(ctx)
		for chunk, err := range m.inner.Generate(ctx, req) {
			if err != nil {
				yield(types.ModelChunk{}, err)
				return
			}
			if sink != nil && chunk.Kind == types.DeltaText && chunk.Delta != "" {
				sink.Emit(ctx, types.TextDelta{Turn: m.h.turns(), MessageID: assistantID, Delta: chunk.Delta})
			}
			if !yield(chunk, nil) {
				return
			}
		}
		m.h.addTurn()
	}
}

// governedTool records the gate step and the call, then runs the tool
// double; it refuses a cancelled ctx so nothing runs past cancellation.
type governedTool struct {
	inner types.Tool
	h     *einoHarness
}

func (t governedTool) Spec() types.ToolSpec { return t.inner.Spec() }

func (t governedTool) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return types.ToolResult{}, err
	}
	t.h.recordGate(t.inner.Spec().Name)
	return t.inner.Call(ctx, args)
}

// einoHarness drives the shipped adapter Runtime over the same scripted
// model and tool doubles the other runtime harnesses use, so the runtime
// suite certifies the product's turn seam instead of a hand-written
// stepper. The guard/gate recording and the MaxTurns budget mirror the
// native harness; only the stepper under test changed.
type einoHarness struct {
	scenario conformance.Scenario
	model    types.Model
	tools    map[string]types.Tool
	tel      *recordTel
	maxTurns int
	evs      []types.Event
	stepEvs  [][]types.Event

	mu   sync.Mutex
	obs  conformance.Observations
	turn int
}

func newEinoHarness(sc conformance.Scenario) conformance.Harness {
	h := &einoHarness{
		scenario: sc,
		tools:    map[string]types.Tool{},
		tel:      newRecordTel(),
	}
	switch sc {
	case conformance.ScenarioParallelCalls:
		h.model = gohantest.NewScriptedModel(conformanceProfile,
			gohantest.ToolUses(callUse("left"), callUse("right")),
			gohantest.Text("done"),
		)
		h.tools["left"] = gohantest.NewFakeTool("left")
		h.tools["right"] = gohantest.NewFakeTool("right")
	case conformance.ScenarioMaxTurns:
		turns := make([]gohantest.Turn, 0, harnessMaxTurns+2)
		for i := range harnessMaxTurns + 2 {
			turns = append(turns, gohantest.ToolCall("echo", map[string]string{"in": string(rune('1' + i))}))
		}
		h.model = gohantest.NewScriptedModel(conformanceProfile, turns...)
		h.tools["echo"] = gohantest.NewFakeTool("echo")
		h.maxTurns = harnessMaxTurns
	case conformance.ScenarioToolRoundTrip:
		h.model = gohantest.NewScriptedModel(conformanceProfile,
			gohantest.ToolCall("echo", map[string]string{"in": "1"}),
			gohantest.Text("done"),
		)
		h.tools["echo"] = gohantest.NewFakeTool("echo")
	default:
		h.model = gohantest.NewScriptedModel(conformanceProfile, gohantest.Text("done"))
	}
	return h
}

var conformanceProfile = types.ModelProfile{Name: "conformance", Version: "v1"}

func (h *einoHarness) assemble(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
	return types.ModelRequest{Messages: slices.Clone(in.History)}, nil
}

func (h *einoHarness) Run(ctx context.Context, input []types.Message) ([]types.Event, error) {
	ctx = types.WithTelemetry(ctx, h.tel)
	h.record(conformance.StepGuard)
	rt, err := New(ctx, &adk.ChatModelAgent{})
	if err != nil {
		return nil, err
	}
	ag := runtime.AgentRun{
		Model:    &governedModel{inner: h.model, h: h},
		Assemble: h.assemble,
	}
	for _, t := range h.tools {
		ag.Tools = append(ag.Tools, governedTool{inner: t, h: h})
	}
	if _, err := rt.Start(ctx, ag); err != nil {
		return nil, err
	}
	var st runtime.State
	for {
		var evs []types.Event
		var status runtime.Status
		st, evs, status, err = rt.Step(ctx, st)
		h.stepEvs = append(h.stepEvs, evs)
		h.appendEvs(evs)
		if err != nil {
			return h.evs, err
		}
		if status == runtime.DoneStatus {
			return h.evs, nil
		}
	}
}

// ModelCalls reports how many model calls the run made, for the max-turns
// seam.
func (h *einoHarness) ModelCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.turn
}

func (h *einoHarness) LimitReached() int64 {
	return h.tel.counted(maxTurnsMetricNm)
}

// StepEvents reports the event slice each Step returned, in Step order.
func (h *einoHarness) StepEvents() [][]types.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.stepEvs)
}

func (h *einoHarness) Observed() conformance.Observations {
	h.mu.Lock()
	defer h.mu.Unlock()
	return conformance.Observations{
		Steps:     slices.Clone(h.obs.Steps),
		ToolCalls: slices.Clone(h.obs.ToolCalls),
	}
}

func (h *einoHarness) turns() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.turn
}

func (h *einoHarness) addTurn() {
	h.mu.Lock()
	h.turn++
	h.mu.Unlock()
}

func (h *einoHarness) record(step string) {
	h.mu.Lock()
	h.obs.Steps = append(h.obs.Steps, step)
	h.mu.Unlock()
}

func (h *einoHarness) recordGate(name string) {
	h.mu.Lock()
	h.obs.Steps = append(h.obs.Steps, conformance.StepGate)
	h.obs.ToolCalls = append(h.obs.ToolCalls, name)
	h.mu.Unlock()
}

func (h *einoHarness) appendEvs(evs []types.Event) {
	h.mu.Lock()
	h.evs = append(h.evs, evs...)
	h.mu.Unlock()
}

func harnessInput() []types.Message {
	return []types.Message{
		{ID: "in-1", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}},
	}
}

// blockingModel parks its only chunk on a channel until ctx cancels, so a
// test can cancel mid-turn.
type blockingModel struct {
	entered chan struct{}
}

func (m *blockingModel) Profile() types.ModelProfile { return conformanceProfile }

func (m *blockingModel) Generate(ctx context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		close(m.entered)
		<-ctx.Done()
		yield(types.ModelChunk{}, ctx.Err())
	}
}
