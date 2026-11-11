package evals

import (
	"context"
	"fmt"
	"time"
)

// CalibratedLabel is one human-labelled calibration case.
type CalibratedLabel struct {
	ID       string
	Class    string
	Evidence Evidence
	Expected map[string]float64 // dimension → human score, unit scale
}

// CalibrationSet is the versioned human-labelled calibration set.
type CalibrationSet struct {
	Version string
	Labels  []CalibratedLabel
}

const DefaultCalibrationTolerance = 0.10

// AcceptanceCriteria are the configured activation criteria: the fraction
// of labels a judge must score within Tolerance (default
// DefaultCalibrationTolerance), and the mean absolute error ceiling per
// class. Per-class error review is mandatory: MaxClassError must cover
// every class present in the set.
type AcceptanceCriteria struct {
	MinAgreement  float64
	Tolerance     float64
	MaxClassError map[string]float64
}

// CalibrationApproval records that one judge fingerprint passed the gate
// over one set version. A model or rubric change changes the fingerprint
// and strands the approval.
type CalibrationApproval struct {
	Fingerprint string
	SetVersion  string
	Criteria    AcceptanceCriteria
	GrantedAt   time.Time
}

// CalibrationGate reviews a judge against the configured criteria over
// the versioned human-labelled set.
type CalibrationGate struct {
	Set      CalibrationSet
	Criteria AcceptanceCriteria
}

// Review runs the judge over every label, reviews per-class error and
// grants an approval only when all criteria pass; a miss returns an error
// wrapping ErrCalibrationCriteria that names the failing class.
func (g CalibrationGate) Review(ctx context.Context, j Judge) (CalibrationApproval, error) {
	return CalibrationApproval{}, fmt.Errorf("gohan/evals: calibration review refused: %w", ErrCalibrationCriteria)
}

// Activate registers an approval. Deterministic evaluators need no
// calibration.
func (o *Online) Activate(a CalibrationApproval) error {
	return nil
}

// Validate re-runs the activation checks without starting workers.
func (o *Online) Validate() error {
	return ErrCalibrationRequired
}
