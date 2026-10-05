package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// Native advances a run one driver-supplied effect per Step. It holds no
// model, store or tool of its own: each Step dispatches the phase's
// AgentRun callback and flips to the other phase, so a suspended state
// resumes at its phase. The phase lives in State.Backend as JSON. Start
// binds the AgentRun the subsequent Steps dispatch to; one instance drives
// one run.
type Native struct {
	run AgentRun
}

// NewNative builds the native runtime.
func NewNative() *Native { return &Native{} }

// Name is the backend-declared runtime name.
func (n *Native) Name() string { return "native" }

// Granularity reports that one Step is one effect boundary.
func (n *Native) Granularity() StepGranularity { return GranularityEffect }

const (
	phaseModel = "model"
	phaseBatch = "batch"
)

type nativePhase struct {
	Phase string `json:"phase"`
}

var errMissingEffect = errors.New("gohan: native runtime phase has no effect callback")
var errUnknownPhase = errors.New("gohan: native runtime state carries an unknown phase")

// Start binds the run and puts it in the model phase.
func (n *Native) Start(ctx context.Context, r AgentRun) (State, error) {
	n.run = r
	enc, err := json.Marshal(nativePhase{Phase: phaseModel})
	if err != nil {
		return State{}, fmt.Errorf("gohan: encode native phase: %w", err)
	}
	return State{Backend: enc}, nil
}

// Step runs exactly one effect: the current phase's callback, then advances
// to the other phase. A callback missing for the phase is an error, not a
// panic.
func (n *Native) Step(ctx context.Context, st State) (State, []types.Event, Status, error) {
	var p nativePhase
	if err := json.Unmarshal(st.Backend, &p); err != nil {
		return st, nil, Continue, fmt.Errorf("gohan: decode native phase: %w", err)
	}

	var next string
	var effect EffectFunc
	switch p.Phase {
	case phaseModel:
		next, effect = phaseBatch, n.run.ModelEffect
	case phaseBatch:
		next, effect = phaseModel, n.run.BatchEffect
	default:
		return st, nil, Continue, fmt.Errorf("%w: %q", errUnknownPhase, p.Phase)
	}
	if effect == nil {
		return st, nil, Continue, fmt.Errorf("%w: %s", errMissingEffect, p.Phase)
	}
	out, events, status, err := effect(ctx, st)
	if err != nil {
		return out, events, status, err
	}
	enc, err := json.Marshal(nativePhase{Phase: next})
	if err != nil {
		return out, events, status, fmt.Errorf("gohan: encode native phase: %w", err)
	}
	out.Backend = enc
	return out, events, status, nil
}
