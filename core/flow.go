package gohan

import (
	"context"
	"encoding/json"
	"iter"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Flow is the non-streaming face of an agent: one invocation in, one typed
// result out. Streams are the Conversation's business (row 23.2).
type Flow[In, Out any] interface {
	Invoke(ctx context.Context, in In) (Out, error)
	Resume(ctx context.Context, t types.ResumeToken, r stores.ResumeInput) (Out, error)
}

// FlowFunc wraps a plain Go function as a Flow. The invocation runs through
// the runtime stepper, so the same Drive loop that governs an agent run
// governs a plain function: one Step, then Done.
func FlowFunc[In, Out any](name string, fn func(context.Context, In) (Out, error)) Flow[In, Out] {
	return &flowFunc[In, Out]{name: name, fn: fn}
}

type flowFunc[In, Out any] struct {
	name string
	fn   func(context.Context, In) (Out, error)
}

// Resume always fails: a plain function has no suspension point to return
// to (flow.not-suspendable).
func (f *flowFunc[In, Out]) Resume(_ context.Context, _ types.ResumeToken, _ stores.ResumeInput) (Out, error) {
	var zero Out
	return zero, types.ErrNotSuspendable
}

func (f *flowFunc[In, Out]) Invoke(ctx context.Context, in In) (Out, error) {
	step := &flowStep[In, Out]{fn: f.fn, in: in}
	var out Out
	for _, err := range driveFlow(ctx, step) {
		if err != nil {
			return out, err
		}
	}
	return step.out, nil
}

// driveFlow runs the flow through Drive and projects the typed result onto
// the event stream: Out ≠ string lands as one Text block of canonical JSON
// in the final assistant message, and Done.Result carries the same bytes.
func driveFlow[In, Out any](ctx context.Context, step *flowStep[In, Out]) iter.Seq2[types.Event, error] {
	rt := flowRuntime[In, Out]{step: step}
	return func(yield func(types.Event, error) bool) {
		for ev, err := range Drive(ctx, rt, runtime.AgentRun{}) {
			if err != nil {
				yield(nil, err)
				return
			}
			d, ok := ev.(types.Done)
			if !ok || step.raw == nil {
				if !yield(ev, nil) {
					return
				}
				continue
			}
			msg := types.Message{
				Role: types.RoleAssistant,
				Blocks: []types.Block{types.Text{
					BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}},
					Text:      string(step.raw),
				}},
			}
			if !yield(types.AssistantMessage{Message: msg}, nil) {
				return
			}
			d.Result = step.raw
			if !yield(d, nil) {
				return
			}
		}
	}
}

// flowStep is the single-effect stepper for a FlowFunc: one call to fn is
// the run's whole effect boundary.
type flowStep[In, Out any] struct {
	fn  func(context.Context, In) (Out, error)
	in  In
	ran bool
	out Out
	raw json.RawMessage
}

func (s *flowStep[In, Out]) Start(_ context.Context, _ runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (s *flowStep[In, Out]) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if s.ran {
		return st, nil, runtime.DoneStatus, nil
	}
	s.ran = true
	out, err := s.fn(ctx, s.in)
	if err != nil {
		return st, nil, runtime.DoneStatus, err
	}
	s.out = out
	if _, isStr := any(out).(string); !isStr {
		raw, merr := json.Marshal(out)
		if merr != nil {
			return st, nil, runtime.DoneStatus, merr
		}
		s.raw = raw
	}
	return st, nil, runtime.DoneStatus, nil
}

// flowRuntime adapts one flow invocation to the Runtime vocabulary Drive
// consumes. One Step is one whole invocation, so the granularity is Turn.
type flowRuntime[In, Out any] struct {
	step *flowStep[In, Out]
}

func (r flowRuntime[In, Out]) Name() string { return "flow.func" }

func (flowRuntime[In, Out]) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r flowRuntime[In, Out]) Start(ctx context.Context, ag runtime.AgentRun) (runtime.State, error) {
	return r.step.Start(ctx, ag)
}

func (r flowRuntime[In, Out]) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	return r.step.Step(ctx, st)
}
