// Package runtime holds the stepper vocabulary every backend implements.
// It is a leaf: it imports only the floor packages.
package runtime

import (
	"context"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Status is what one Step reports about the run after its effect boundary.
type Status int

const (
	Continue Status = iota
	SuspendedStatus
	DoneStatus
)

// FlagSnapshot records the frozen flag values the run evaluated at start;
// replay must see the same values the first execution saw.
type FlagSnapshot map[string]any

// State is the resumable position of one run. It is serializable and is
// what a checkpoint carries for agent flows; HistoryVersion is the
// SessionLog version the state was taken at.
type State struct {
	Turn           int
	HistoryVersion int64
	Pending        []types.ToolUse
	ActiveTools    []string
	Flags          FlagSnapshot
	Usage          types.Usage
	Calibration    map[string]float64
	Backend        []byte
}

// Stepper advances a run one effect boundary at a time. The events Step
// returns are runtime-originated only; component events travel through the
// sink in ctx.
type Stepper interface {
	Start(ctx context.Context, r AgentRun) (State, error)
	Step(ctx context.Context, st State) (State, []types.Event, Status, error)
}

// Runtime is a named stepper a backend declares, with the granularity its
// Step boundary counts.
type Runtime interface {
	Stepper
	Name() string
	Granularity() StepGranularity
}

// StepGranularity says whether one Step is one model call or tool batch
// (Effect) or one whole turn (Turn).
type StepGranularity int

const (
	GranularityEffect StepGranularity = iota
	GranularityTurn
)

// AgentRun is the per-run wiring Drive hands a runtime. Model and Tools are
// already governed when the runtime sees them.
type AgentRun struct {
	Model    types.Model
	Tools    []types.Tool
	Assemble func(ctx context.Context, in types.AssembleInput) (types.ModelRequest, error)
	History  stores.History
	Input    []types.Message
	Save     func(ctx context.Context, cp stores.Checkpoint) (types.ResumeToken, error)
	Mode     types.RunMode
}
