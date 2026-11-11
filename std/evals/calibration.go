package evals

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/victorzhuk/gohan/core/types"
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
	if g.Set.Version == "" {
		return CalibrationApproval{}, fmt.Errorf("gohan/evals: calibration gate: set names no version: %w", ErrCalibrationCriteria)
	}
	if len(g.Set.Labels) == 0 {
		return CalibrationApproval{}, fmt.Errorf("gohan/evals: calibration set %s: no labels: %w", g.Set.Version, ErrCalibrationCriteria)
	}
	tol := g.Criteria.Tolerance
	if tol <= 0 {
		tol = DefaultCalibrationTolerance
	}
	classes := make(map[string]bool)
	for _, l := range g.Set.Labels {
		classes[l.Class] = true
		if _, ok := l.Expected[j.Dimension]; !ok {
			return CalibrationApproval{}, fmt.Errorf("gohan/evals: label %s: no expected score for dimension %s: %w", l.ID, j.Dimension, ErrCalibrationCriteria)
		}
	}
	for class := range classes {
		if _, ok := g.Criteria.MaxClassError[class]; !ok {
			return CalibrationApproval{}, fmt.Errorf("gohan/evals: class %s: criteria MaxClassError missing: %w", class, ErrCalibrationCriteria)
		}
	}
	agreed := 0
	classErrSum := make(map[string]float64, len(classes))
	classN := make(map[string]int, len(classes))
	for _, l := range g.Set.Labels {
		s, err := reviewScore(ctx, j, l.Evidence)
		if err != nil {
			return CalibrationApproval{}, fmt.Errorf("gohan/evals: label %s: judge failed: %w", l.ID, ErrCalibrationCriteria)
		}
		diff := math.Abs(s - l.Expected[j.Dimension])
		if diff <= tol {
			agreed++
		}
		classErrSum[l.Class] += diff
		classN[l.Class]++
	}
	if float64(agreed)/float64(len(g.Set.Labels)) < g.Criteria.MinAgreement {
		return CalibrationApproval{}, fmt.Errorf("gohan/evals: agreement %d/%d below MinAgreement %.2f: %w", agreed, len(g.Set.Labels), g.Criteria.MinAgreement, ErrCalibrationCriteria)
	}
	for class := range classes {
		mae := classErrSum[class] / float64(classN[class])
		if mae+1e-9 > g.Criteria.MaxClassError[class] {
			return CalibrationApproval{}, fmt.Errorf("gohan/evals: class %s: mean error %.2f above ceiling %.2f: %w", class, mae, g.Criteria.MaxClassError[class], ErrCalibrationCriteria)
		}
	}
	return CalibrationApproval{
		Fingerprint: j.Fingerprint(),
		SetVersion:  g.Set.Version,
		Criteria:    g.Criteria,
		GrantedAt:   time.Now(),
	}, nil
}

// reviewScore scores one label with the judge under review: deterministic
// evaluators run in process; model judges repeat and take the median.
func reviewScore(ctx context.Context, j Judge, ev Evidence) (float64, error) {
	if j.Evaluator != nil {
		r := j.Evaluator.Evaluate(ev)
		if r.Status != types.EvalScored {
			return 0, fmt.Errorf("evaluator %s returned %s: %s", j.Dimension, r.Status, r.Reason)
		}
		return r.Score, nil
	}
	repeats := j.Repeat
	if repeats <= 0 {
		repeats = DefaultJudgeRepeat
	}
	scores := make([]float64, 0, repeats)
	for len(scores) < repeats {
		text, err := callJudge(ctx, nil, nil, j.Model, ev, j.Rubric)
		if err != nil {
			return 0, err
		}
		scores = append(scores, parseScore(text))
	}
	median, _ := medianSpread(scores)
	return median, nil
}

// Activate registers an approval. Deterministic evaluators need no
// calibration.
func (o *Online) Activate(a CalibrationApproval) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.state == stateClosed {
		return ErrEngineClosed
	}
	if err := o.checkApproval(a); err != nil {
		return err
	}
	if o.approvals == nil {
		o.approvals = make(map[string]CalibrationApproval, 1)
	}
	o.approvals[a.Fingerprint] = a
	return nil
}

// checkApproval verifies one approval against the configured judges: an
// approval whose fingerprint covers no model-backed judge is stranded.
func (o *Online) checkApproval(a CalibrationApproval) error {
	if a.SetVersion == "" {
		return fmt.Errorf("gohan/evals: approval names no calibration set version: %w", ErrCalibrationRequired)
	}
	for _, j := range o.Judges {
		if j.Evaluator == nil && j.Fingerprint() == a.Fingerprint {
			return nil
		}
	}
	fp := a.Fingerprint
	if len(fp) > 8 {
		fp = fp[:8]
	}
	return fmt.Errorf("gohan/evals: approval %s covers no configured judge: %w", fp, ErrCalibrationInvalidated)
}

// validateRedactorLocked refuses engines whose model-backed judges run
// without a bound redactor: redaction is a data-safety gate.
func (o *Online) validateRedactorLocked() error {
	for _, j := range o.Judges {
		if j.Model != nil && o.redactor == nil {
			return ErrRedactorRequired
		}
	}
	return nil
}

// validateCalibrationLocked enforces the activation rule: every
// model-backed judge needs a registered approval with a matching
// fingerprint, and every registered approval must cover a judge.
func (o *Online) validateCalibrationLocked() error {
	for _, j := range o.Judges {
		if j.Evaluator != nil {
			continue
		}
		a, ok := o.approvals[j.Fingerprint()]
		if !ok {
			return fmt.Errorf("gohan/evals: judge %s: %w", j.Dimension, ErrCalibrationRequired)
		}
		if a.SetVersion == "" {
			return fmt.Errorf("gohan/evals: judge %s: approval names no calibration set version: %w", j.Dimension, ErrCalibrationRequired)
		}
	}
	for _, a := range o.approvals {
		if err := o.checkApproval(a); err != nil {
			return err
		}
	}
	return nil
}

// Validate re-runs the activation checks without starting workers.
func (o *Online) Validate() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.state == stateClosed {
		return ErrEngineClosed
	}
	if o.Rate < 0 || o.Rate > 1 {
		return ErrSamplingRate
	}
	if err := validateJudges(o.Judges); err != nil {
		return err
	}
	return o.validateCalibrationLocked()
}
