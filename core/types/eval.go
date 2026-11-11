package types

import "time"

// EvalStatus is one dimension result's settled outcome. Unscored and
// skipped results never pass a required gate.
type EvalStatus string

const (
	EvalScored   EvalStatus = "scored"
	EvalUnscored EvalStatus = "unscored"
	EvalSkipped  EvalStatus = "skipped"
)

// Standard result dimensions. Results stay separate and named; a record
// carries exactly one result per configured dimension.
const (
	EvalQuality       = "quality"
	EvalSafety        = "safety"
	EvalExecution     = "execution"
	EvalFaithfulness  = "faithfulness"
	EvalContextRecall = "context-recall"
)

// ScaleUnit is the score scale deterministic evaluators and judge medians
// report on: 0..1.
const ScaleUnit = "unit"

// EvalErrorCategory classifies why a result is unscored or an offer skipped.
type EvalErrorCategory string

const (
	EvalErrTimeout         EvalErrorCategory = "timeout"
	EvalErrMissingEvidence EvalErrorCategory = "missing_evidence"
	EvalErrAbsentReference EvalErrorCategory = "absent_reference"
	EvalErrJudgeFailed     EvalErrorCategory = "judge_failed"
	EvalErrRedaction       EvalErrorCategory = "redaction_failed"
	EvalErrQueueFull       EvalErrorCategory = "queue_full"
	EvalErrBudgetExhausted EvalErrorCategory = "budget_exhausted"
	EvalErrNotSelected     EvalErrorCategory = "not_selected"
	EvalErrDisabled        EvalErrorCategory = "disabled"
	EvalErrDrained         EvalErrorCategory = "drained"
)

// EvalJudgement is one repeated judge observation, preserved in the record.
type EvalJudgement struct {
	Judge   string  `json:"judge"`
	Version string  `json:"version,omitempty"`
	Score   float64 `json:"score"`
}

// EvalDimensionResult is one named dimension's independent result.
type EvalDimensionResult struct {
	Dimension     string            `json:"dimension"`
	Status        EvalStatus        `json:"status"`
	Score         float64           `json:"score,omitempty"`
	Scale         string            `json:"scale,omitempty"`
	Median        float64           `json:"median,omitempty"`
	Unstable      bool              `json:"unstable,omitempty"`
	Judgements    []EvalJudgement   `json:"judgements,omitempty"`
	Evaluator     string            `json:"evaluator,omitempty"`
	Rubric        string            `json:"rubric,omitempty"`
	Model         string            `json:"model,omitempty"`
	Reason        string            `json:"reason,omitempty"`
	ErrorCategory EvalErrorCategory `json:"error_category,omitempty"`
	Cost          float64           `json:"cost,omitempty"`
	Tokens        int64             `json:"tokens,omitempty"`
	DurationMs    int64             `json:"duration_ms,omitempty"`
}

// PassRequired reports whether this scored result meets min on its scale.
// An unscored or skipped result never passes, whatever min is.
func (r EvalDimensionResult) PassRequired(min float64) bool {
	return r.Status == EvalScored && r.Score >= min
}

// EvalSampling records the admission decision with its probability.
type EvalSampling struct {
	Probability float64 `json:"probability"`
	Reason      string  `json:"reason"`
	Policy      string  `json:"policy"`
}

// EvidenceRef names one evidence input by kind and reference. It carries
// references only, never message content.
type EvidenceRef struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// Evidence reference kinds.
const (
	EvidenceHistoryVersion = "history_version"
	EvidenceRunOutput      = "run_output"
)

// EvalRecord is one evaluation result crossing the export boundary as the
// payload of an ExportRecord with Signal == SignalEval.
type EvalRecord struct {
	EvaluationID string                `json:"evaluation_id"`
	RunID        string                `json:"run_id"`
	SessionID    string                `json:"session_id,omitempty"`
	Flow         string                `json:"flow,omitempty"`
	ReleaseID    string                `json:"release_id,omitempty"`
	Variant      string                `json:"variant,omitempty"`
	Results      []EvalDimensionResult `json:"results"`
	Sampling     EvalSampling          `json:"sampling"`
	Evidence     []EvidenceRef         `json:"evidence,omitempty"`
	StartedAt    time.Time             `json:"started_at"`
	CompletedAt  time.Time             `json:"completed_at"`
	Cost         float64               `json:"cost,omitempty"`
	Tokens       int64                 `json:"tokens,omitempty"`
}
