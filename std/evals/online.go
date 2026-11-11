package evals

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"uuid"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
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

const (
	stateIdle = iota
	stateRunning
	stateClosed
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

	mu          sync.Mutex
	state       int
	approvals   map[string]CalibrationApproval
	pending     chan *evalJob
	live        int
	cancel      context.CancelFunc
	runCtx      context.Context
	wg          sync.WaitGroup
	limits      OnlineLimits
	window      time.Duration
	windowStart time.Time
	tokensUsed  int64
	costUsed    float64
	rates       Rates
}

type OnlineOption func(*Online) error

func WithSink(s types.RecordSink, dest string) OnlineOption {
	return func(o *Online) error {
		if s == nil {
			return errors.New("gohan/evals: WithSink needs a non-nil sink")
		}
		o.sink, o.dest = s, dest
		return nil
	}
}

func WithRedactor(r types.Redactor, p types.RedactionPolicy) OnlineOption {
	return func(o *Online) error {
		o.redactor, o.policy = r, p
		return nil
	}
}

func WithMetrics(m *telemetry.Metrics) OnlineOption {
	return func(o *Online) error {
		o.metrics = m
		return nil
	}
}

func WithLogger(l *slog.Logger) OnlineOption {
	return func(o *Online) error {
		if l == nil {
			return errors.New("gohan/evals: WithLogger needs a non-nil logger")
		}
		o.logger = l
		return nil
	}
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
	if o.Rate < 0 || o.Rate > 1 {
		return nil, ErrSamplingRate
	}
	if err := validateJudges(o.Judges); err != nil {
		return nil, err
	}
	return o, nil
}

func validateJudges(js []Judge) error {
	seen := make(map[string]bool, len(js))
	for _, j := range js {
		if j.Dimension == "" {
			return fmt.Errorf("%w: a judge names no dimension", ErrJudgeConfig)
		}
		if (j.Evaluator == nil) == (j.Model == nil) {
			return fmt.Errorf("%w: judge %s needs exactly one of evaluator or model", ErrJudgeConfig, j.Dimension)
		}
		if seen[j.Dimension] {
			return fmt.Errorf("%w: %s", ErrDuplicateDimension, j.Dimension)
		}
		seen[j.Dimension] = true
	}
	return nil
}

// Start validates and starts the worker pool; Evaluate calls it lazily
// once. Idempotent.
func (o *Online) Start() error {
	return o.start(true)
}

// start runs the Start sequence; enforce gates the calibration activation
// check, which the lazy admission path defers to an explicit Start.
func (o *Online) start(enforce bool) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch o.state {
	case stateRunning:
		return nil
	case stateClosed:
		return ErrEngineClosed
	}
	if o.Rate < 0 || o.Rate > 1 {
		return ErrSamplingRate
	}
	if err := validateJudges(o.Judges); err != nil {
		return err
	}
	if enforce {
		if err := o.validateCalibrationLocked(); err != nil {
			return err
		}
	}
	lim := o.Limits
	if lim.QueueCapacity <= 0 {
		lim.QueueCapacity = DefaultQueueCapacity
	}
	if lim.Workers <= 0 {
		lim.Workers = DefaultWorkers
	}
	if lim.JobDeadline <= 0 {
		lim.JobDeadline = DefaultJobDeadline
	}
	if lim.MaxRetries < 0 {
		lim.MaxRetries = DefaultMaxRetries
	}
	if lim.TokenBudget <= 0 {
		lim.TokenBudget = DefaultTokenBudget
	}
	if lim.CostBudget <= 0 {
		lim.CostBudget = DefaultCostBudget
	}
	o.limits = lim
	if o.Window <= 0 {
		o.Window = DefaultWindow
	}
	o.window = o.Window
	if o.logger == nil {
		o.logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	o.cancel = cancel
	o.runCtx = ctx
	o.pending = make(chan *evalJob, lim.QueueCapacity)
	o.windowStart = time.Now()
	o.state = stateRunning
	return nil
}

// Shutdown closes admission, settles owned jobs per ClosurePolicy within
// ctx and stops the workers. Idempotent. Workers settle in-flight and
// queued jobs to their deadlines; if ctx expires first, remaining work
// is cancelled and settles unscored.
func (o *Online) Shutdown(ctx context.Context) error {
	o.mu.Lock()
	switch o.state {
	case stateClosed:
		o.mu.Unlock()
		return nil
	case stateIdle:
		o.state = stateClosed
		o.mu.Unlock()
		return nil
	}
	o.state = stateClosed
	o.mu.Unlock()

	done := make(chan struct{})
	go func() {
		o.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		o.mu.Lock()
		cancel := o.cancel
		o.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		<-done
	}
	return nil
}

// evalJob is one admitted evaluation.
type evalJob struct {
	evalID    string
	recordID  types.RecordID
	version   int64
	startedAt time.Time
	info      types.RunInfo
	done      types.Done
	history   func(context.Context) (stores.History, error)
}

// Evaluate implements gohan.RunEvaluator. It samples, mints the
// evaluation ID, records the sampling decision and enqueues; it never
// waits on judge work. A full queue or exhausted budget produces a
// skipped outcome, never an error to the Done path.
func (o *Online) Evaluate(ctx context.Context, c gohan.RunCompletion) error {
	o.mu.Lock()
	closed := o.state == stateClosed
	o.mu.Unlock()
	if closed {
		o.mu.Lock()
		o.rates.Skipped++
		o.mu.Unlock()
		return nil
	}
	if err := o.start(false); err != nil {
		return err
	}
	o.mu.Lock()
	if o.state == stateClosed {
		o.rates.Skipped++
		o.mu.Unlock()
		return nil
	}
	if c.Info.Mode != types.Primary {
		o.mu.Unlock()
		return nil
	}
	o.rates.Eligible++
	if o.Rate <= 0 {
		o.rates.Skipped++
		o.mu.Unlock()
		return nil
	}
	o.rollWindowLocked()
	if o.tokensUsed >= o.limits.TokenBudget || o.costUsed >= o.limits.CostBudget {
		o.rates.Skipped++
		o.mu.Unlock()
		return nil
	}
	if o.Rate < 1 && rand.Float64() >= o.Rate {
		o.rates.Skipped++
		o.mu.Unlock()
		return nil
	}
	job := &evalJob{
		evalID:    uuid.NewV7().String(),
		recordID:  types.RecordID(uuid.NewV7().String()),
		version:   c.Version,
		startedAt: time.Now(),
		info:      c.Info,
		done:      c.Done,
		history:   c.History,
	}
	if o.live < o.limits.Workers {
		o.live++
		o.wg.Add(1)
		ctx := o.runCtx
		o.mu.Unlock()
		go o.worker(ctx, job)
		o.mu.Lock()
		o.rates.Unknown++
		o.mu.Unlock()
		return nil
	}
	select {
	case o.pending <- job:
		o.rates.Unknown++
	default:
		o.rates.Skipped++
	}
	o.mu.Unlock()
	return nil
}

// rollWindowLocked resets consumption and accounting when the current
// window elapsed.
func (o *Online) rollWindowLocked() {
	if o.window > 0 && time.Since(o.windowStart) >= o.window {
		o.windowStart = time.Now()
		o.tokensUsed = 0
		o.costUsed = 0
		o.rates = Rates{}
	}
}

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

func (o *Online) Rates() Rates {
	o.mu.Lock()
	defer o.mu.Unlock()
	r := o.rates
	r.Window = o.window
	r.Selection = SelectionUniform
	return r
}

// worker settles its dispatched job and then drains the pending queue.
// It exits as soon as the queue is empty: admission spawns a fresh
// worker on the next offer while live workers stay under Limits.Workers.
func (o *Online) worker(ctx context.Context, first *evalJob) {
	defer o.wg.Done()
	defer func() {
		o.mu.Lock()
		o.live--
		o.mu.Unlock()
	}()
	o.run(ctx, first)
	for {
		select {
		case job := <-o.pending:
			o.run(ctx, job)
		default:
			return
		}
	}
}

type evidenceErr struct {
	cat    types.EvalErrorCategory
	reason string
}

// run settles one admitted job: evidence load, per-judge scoring and the
// single settle-to-record handoff. A deadline bounds the whole job
// including retries.
func (o *Online) run(parent context.Context, job *evalJob) {
	jobCtx, cancel := context.WithTimeout(parent, o.jobDeadline())
	defer cancel()

	var results []types.EvalDimensionResult
	if hist, eerr := o.evidence(jobCtx, job); eerr != nil {
		results = o.unscoredAll(job, eerr.cat, eerr.reason)
	} else {
		ev := Evidence{Info: job.info, Done: job.done, History: hist}
		for _, j := range o.Judges {
			results = append(results, o.scoreJudge(jobCtx, j, ev))
		}
	}
	if jobCtx.Err() != nil && o.Closure == CloseSkipped {
		o.settleUnfinished(job)
		return
	}
	o.settle(job, results)
}

func (o *Online) jobDeadline() time.Duration {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.limits.JobDeadline > 0 {
		return o.limits.JobDeadline
	}
	return DefaultJobDeadline
}

// evidence loads and redacts the job's history snapshot.
func (o *Online) evidence(ctx context.Context, job *evalJob) (stores.History, *evidenceErr) {
	if job.history == nil {
		return stores.History{}, &evidenceErr{types.EvalErrMissingEvidence, "no history loader bound to the completion"}
	}
	h, err := job.history(ctx)
	if err != nil {
		return stores.History{}, &evidenceErr{types.EvalErrMissingEvidence, "history load failed: " + err.Error()}
	}
	if o.redactor != nil && len(h.Messages) > 0 {
		for i, m := range h.Messages {
			redacted, _, rerr := o.redactor.Redact(ctx, m.Blocks, o.policy)
			if rerr != nil {
				return stores.History{}, &evidenceErr{types.EvalErrRedaction, "evidence redaction failed: " + rerr.Error()}
			}
			m.Blocks = redacted
			h.Messages[i] = m
		}
	}
	return h, nil
}

func (o *Online) unscoredAll(job *evalJob, cat types.EvalErrorCategory, reason string) []types.EvalDimensionResult {
	results := make([]types.EvalDimensionResult, 0, len(o.Judges))
	for _, j := range o.Judges {
		results = append(results, unscored(j.Dimension, cat, reason))
	}
	return results
}

// scoreJudge resolves one judge's dimension result: deterministic
// evaluators run in process; model judges repeat, retry and take the
// median within the job deadline.
func (o *Online) scoreJudge(ctx context.Context, j Judge, ev Evidence) types.EvalDimensionResult {
	if j.Evaluator != nil {
		r := j.Evaluator.Evaluate(ev)
		r.Dimension = j.Dimension
		if r.Status == types.EvalScored && r.Scale == "" {
			r.Scale = types.ScaleUnit
		}
		return r
	}
	repeats := j.Repeat
	if repeats <= 0 {
		repeats = DefaultJudgeRepeat
	}
	model := j.Version
	if j.Model != nil {
		if p := j.Model.Profile(); model == "" {
			model = p.Version
		}
	}
	attempts := 1 + o.maxRetries()
	var scores []float64
	var judgements []types.EvalJudgement
	for len(scores) < repeats {
		if err := ctx.Err(); err != nil {
			return unscored(j.Dimension, types.EvalErrTimeout, "job deadline exceeded before judging finished")
		}
		text, err := callJudge(ctx, j.Model, ev, j.Rubric)
		if err != nil {
			if ctx.Err() != nil {
				return unscored(j.Dimension, types.EvalErrTimeout, "job deadline exceeded before judging finished")
			}
			if attempts--; attempts <= 0 {
				return unscored(j.Dimension, types.EvalErrJudgeFailed, "judge call failed after retries: "+err.Error())
			}
			continue
		}
		s := parseScore(text)
		scores = append(scores, s)
		judgements = append(judgements, types.EvalJudgement{Judge: j.Dimension, Version: j.Version, Score: s})
	}
	median, spread := medianSpread(scores)
	return types.EvalDimensionResult{
		Dimension:  j.Dimension,
		Status:     types.EvalScored,
		Score:      median,
		Scale:      types.ScaleUnit,
		Median:     median,
		Unstable:   spread > UnstableSpread,
		Judgements: judgements,
		Rubric:     j.Rubric.Version,
		Model:      model,
	}
}

func (o *Online) maxRetries() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.limits.MaxRetries > 0 {
		return o.limits.MaxRetries
	}
	return 0
}

// callJudge runs one judge model call over the redacted evidence.
func callJudge(ctx context.Context, m types.Model, ev Evidence, r Rubric) (string, error) {
	instruction := "Score the run on dimension scale 0..1. Rubric " + r.Version + ": " + r.Instruction
	req := types.ModelRequest{
		System:   []types.Block{types.Text{Text: instruction}},
		Messages: ev.History.Messages,
	}
	var sb strings.Builder
	for chunk, err := range m.Generate(ctx, req) {
		if err != nil {
			return sb.String(), err
		}
		sb.WriteString(chunk.Delta)
	}
	return sb.String(), nil
}

// parseScore reads the first 0..1 score token in the judge output; a
// bare or empty output scores 0.
func parseScore(text string) float64 {
	for _, f := range strings.Fields(text) {
		f = strings.Trim(f, ",.;:()[]")
		if v, err := strconv.ParseFloat(f, 64); err == nil && v >= 0 && v <= 1 {
			return v
		}
	}
	return 0
}

func medianSpread(scores []float64) (median, spread float64) {
	sorted := slices.Clone(scores)
	slices.Sort(sorted)
	n := len(sorted)
	if n%2 == 1 {
		median = sorted[n/2]
	} else {
		median = (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return median, sorted[n-1] - sorted[0]
}

// settle records the accounting for one finished job and hands its
// export record to the sink once.
func (o *Online) settle(job *evalJob, results []types.EvalDimensionResult) {
	completed := time.Now()
	scored := false
	for _, r := range results {
		if r.Status == types.EvalScored {
			scored = true
		}
	}
	rec := o.buildRecord(job, results, completed)
	o.mu.Lock()
	o.rates.Unknown--
	if scored {
		o.rates.Evaluated++
	} else {
		o.rates.Failed++
	}
	o.tokensUsed += rec.Eval.Tokens
	o.costUsed += rec.Eval.Cost
	o.mu.Unlock()
	o.emitMetrics(results, job.info.Flow)
	if o.sink != nil {
		if err := o.sink.Ingest(context.Background(), o.dest, []types.ExportRecord{rec}); err != nil {
			o.logger.Warn("gohan/evals: eval record ingest failed", "evaluation_id", job.evalID, "err", err)
		}
	}
}

// settleUnfinished accounts an owned job Shutdown settles as skipped
// under CloseSkipped.
func (o *Online) settleUnfinished(job *evalJob) {
	o.mu.Lock()
	o.rates.Unknown--
	o.rates.Skipped++
	o.mu.Unlock()
}

func (o *Online) buildRecord(job *evalJob, results []types.EvalDimensionResult, completed time.Time) types.ExportRecord {
	tokens := int64(job.done.Usage.InputTokens + job.done.Usage.CachedInputTokens + job.done.Usage.OutputTokens)
	ev := &types.EvalRecord{
		EvaluationID: job.evalID,
		RunID:        job.info.RunID,
		SessionID:    job.info.SessionID,
		Flow:         job.info.Flow,
		ReleaseID:    job.info.ReleaseID,
		Variant:      job.info.Variant,
		Results:      results,
		Sampling: types.EvalSampling{
			Probability: o.Rate,
			Reason:      ReasonSampled,
			Policy:      SelectionUniform,
		},
		Evidence:    o.evidenceRefs(job),
		StartedAt:   job.startedAt,
		CompletedAt: completed,
		Cost:        job.done.Cost,
		Tokens:      tokens,
	}
	return types.ExportRecord{
		SchemaVersion: types.CurrentExportSchemaVersion,
		ID:            job.recordID,
		Resource: types.ResourceIdentity{
			Name:  "gohan/evals",
			Attrs: []types.Attr{types.String("flow", job.info.Flow)},
		},
		Signal: types.SignalEval,
		Eval:   ev,
	}
}

func (o *Online) evidenceRefs(job *evalJob) []types.EvidenceRef {
	refs := []types.EvidenceRef{{Kind: types.EvidenceRunOutput, Ref: job.info.RunID}}
	if job.history != nil {
		refs = append(refs, types.EvidenceRef{Kind: types.EvidenceHistoryVersion, Ref: strconv.FormatInt(job.version, 10)})
	}
	return refs
}

func (o *Online) emitMetrics(results []types.EvalDimensionResult, flow string) {
	if o.metrics == nil {
		return
	}
	ctx := context.Background()
	for _, r := range results {
		if r.Status != types.EvalScored {
			continue
		}
		o.metrics.Record(ctx, telemetry.MetricEvalScore, r.Score, types.String("flow", flow), types.String("judge", r.Dimension))
		if r.Unstable {
			o.metrics.Count(ctx, telemetry.MetricEvalUnstable, 1, types.String("flow", flow), types.String("judge", r.Dimension))
		}
	}
}

func unscored(dimension string, cat types.EvalErrorCategory, reason string) types.EvalDimensionResult {
	return types.EvalDimensionResult{
		Dimension:     dimension,
		Status:        types.EvalUnscored,
		Reason:        reason,
		ErrorCategory: cat,
	}
}
