package evals

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/telemetry"
)

// OnlineLimits bounds asynchronous evaluation work. The zero value means
// the defaults; every limit is explicit and enforced.
type OnlineLimits struct {
	QueueCapacity int           // default 1024
	Workers       int           // default 2
	JobDeadline   time.Duration // default 30s, bounds a job including retries
	MaxRetries    int           // default 1, per judge call
	TokenBudget   int64         // per window, default 200_000
	CostBudget    float64       // per window, default 10.0
}

const (
	DefaultQueueCapacity = 1024
	DefaultWorkers       = 2
	DefaultJobDeadline   = 30 * time.Second
	DefaultMaxRetries    = 1
	DefaultTokenBudget   = 200_000
	DefaultCostBudget    = 10.0
	DefaultWindow        = time.Hour
)

// ClosurePolicy declares how unfinished jobs settle when Shutdown drains.
type ClosurePolicy int

const (
	CloseUnscored ClosurePolicy = iota // settle unscored, category timeout
	CloseSkipped                       // settle skipped, category drained
)

// Admission sampling reasons, carried on EvalSampling.Reason and Rates.
const (
	ReasonSampled     = "sampled"
	ReasonNotSelected = "not_selected"
	ReasonQueueFull   = "queue_full"
	ReasonBudget      = "budget_exhausted"
	ReasonDisabled    = "disabled"
	ReasonDrained     = "drained"
)

// SelectionUniform is this slice's selection policy value.
const SelectionUniform = "uniform"

var (
	ErrSamplingRate           = errors.New("gohan/evals: sampling rate must be in [0,1]")
	ErrJudgeConfig            = errors.New("gohan/evals: judge configuration invalid")
	ErrDuplicateDimension     = errors.New("gohan/evals: two judges score the same dimension")
	ErrCalibrationRequired    = errors.New("gohan/evals: judge activation requires a calibration approval covering its version")
	ErrCalibrationInvalidated = errors.New("gohan/evals: calibration approval invalidated by judge model or rubric change")
	ErrCalibrationCriteria    = errors.New("gohan/evals: judge does not meet the configured calibration criteria")
	ErrEngineClosed           = errors.New("gohan/evals: online evaluation engine is shut down")
)

// Online is the Done hook that samples finished primary runs and
// evaluates them off the request path (release spec, evals module:
// evals.Online{Rate, Window, Judges}). *Online implements
// gohan.RunEvaluator; install with gohan.WithRunEvaluator, using the
// spec literal as &evals.Online{Rate: 0.1, Judges: js} or build with
// NewOnline. Non-primary completions are not eligible and return nil.
type Online struct {
	Rate    float64 // sampling probability in [0,1]; 0 keeps admission disabled
	Window  time.Duration
	Judges  []Judge
	Limits  OnlineLimits
	Closure ClosurePolicy

	// set through options only
	sink     types.RecordSink
	dest     string
	redactor types.Redactor
	policy   types.RedactionPolicy
	metrics  *telemetry.Metrics
	logger   *slog.Logger
	// engine state (unexported)
}

type OnlineOption func(*Online) error

func WithSink(s types.RecordSink, dest string) OnlineOption {
	return func(o *Online) error { return nil }
}

func WithRedactor(r types.Redactor, p types.RedactionPolicy) OnlineOption {
	return func(o *Online) error { return nil }
}

func WithMetrics(m *telemetry.Metrics) OnlineOption {
	return func(o *Online) error { return nil }
}

func WithLogger(l *slog.Logger) OnlineOption {
	return func(o *Online) error { return nil }
}

// NewOnline validates the configuration and returns an engine ready to
// install: rate bounds, one judge per dimension, judge shape, activated
// model judges. The literal form defers the same checks to Start.
func NewOnline(rate float64, judges []Judge, opts ...OnlineOption) (*Online, error) {
	o := &Online{Rate: rate, Judges: judges}
	for _, opt := range opts {
		if err := opt(o); err != nil {
			return nil, err
		}
	}
	return o, nil
}

// Start validates and starts the worker pool; Evaluate calls it lazily
// once. Idempotent.
func (o *Online) Start() error { return nil }

// Shutdown closes admission, settles owned jobs per ClosurePolicy within
// ctx and stops the workers. Idempotent.
func (o *Online) Shutdown(ctx context.Context) error { return nil }

// Evaluate implements gohan.RunEvaluator. It samples, mints the
// evaluation ID, records the sampling decision and enqueues; it never
// waits on judge work. A full queue or exhausted budget produces a
// skipped outcome, never an error to the Done path.
func (o *Online) Evaluate(ctx context.Context, c gohan.RunCompletion) error { return nil }

// Rates reports admission accounting for the current window and the
// selection policy in force. Eligible counts offered terminal primary
// completions; Evaluated counts jobs with at least one scored result;
// Skipped counts offers not admitted; Failed counts admitted jobs all of
// whose results are error-unscored; Unknown counts admitted jobs not yet
// settled.
type Rates struct {
	Window    time.Duration
	Selection string
	Eligible  int64
	Evaluated int64
	Skipped   int64
	Failed    int64
	Unknown   int64
}

func (o *Online) Rates() Rates { return Rates{} }
