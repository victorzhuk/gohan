package stores

import (
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// FlowKind names the flow a suspended checkpoint belongs to. The agent
// flow replays from the journal across an adapter upgrade. The graph flow
// cannot, because its compiled shape is bound to the adapter version.
type FlowKind string

const (
	FlowAgent FlowKind = "agent"
	FlowGraph FlowKind = "graph"
)

// ResumePlan is how the harness resumes a suspended run.
type ResumePlan int

const (
	// ResumeNative resumes in place: the backend version matches the one
	// the run suspended under.
	ResumeNative ResumePlan = iota
	// ResumeReplay re-drives the run from the journal: the adapter was
	// upgraded under the run. The harness counts the metric
	// gohan.resume.fallback_replay on this plan.
	ResumeReplay
)

// PlanResume decides how a checkpoint resumes under backendVersion. It
// reads nothing mutable and consumes no token. A caller applies the plan to
// Checkpoints.Consume only after PlanResume returns no error.
func PlanResume(cp Checkpoint, kind FlowKind, backendVersion string) (ResumePlan, error) {
	v := cp.SchemaVersion
	if v == 0 {
		v = 1
	}
	if v > CurrentSchemaVersion {
		return ResumeNative, fmt.Errorf("gohan: checkpoint schema version %d is newer than %d: %w", v, CurrentSchemaVersion, types.ErrCheckpointIncompatible)
	}
	if cp.BackendVersion == backendVersion {
		return ResumeNative, nil
	}
	if kind == FlowAgent {
		return ResumeReplay, nil
	}
	return ResumeNative, fmt.Errorf("gohan: checkpoint flow %s backend %s resumed under %s: %w", kind, cp.BackendVersion, backendVersion, types.ErrCheckpointIncompatible)
}
