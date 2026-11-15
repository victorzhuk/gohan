package evals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
	"uuid"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Case is one recorded or scripted input a shadow run replays: the
// dataset shape the release spec pins (Input, optional Expected, Tags).
type Case struct {
	ID       string   `json:"id,omitempty"` // optional; sha256(Input)[:16] hex when empty
	Input    string   `json:"input"`
	Expected string   `json:"expected,omitempty"` // optional; scripted suites set it, cassettes rarely do
	Tags     []string `json:"tags,omitempty"`     // optional
}

// Input provenance a report records.
const (
	SourceCassette = "cassette"
	SourceScripted = "scripted"
)

// LoadCases reads a cassette of recorded inputs: a JSON array of Case
// records. A missing or malformed file is an error, never an empty run.
func LoadCases(path string) ([]Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("gohan/evals: read cassette %s: %w", path, err)
	}
	var cases []Case
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("gohan/evals: parse cassette %s: %w", path, err)
	}
	return cases, nil
}

// ShadowFlow is the runnable seam Shadow drives: one input in, one
// observed outcome out. The orchestrator owns the context, so a flow
// never decides its own run identity or mode.
type ShadowFlow func(ctx context.Context, input string) (ShadowOutcome, error)

// ShadowOutcome is what one flow reports for one input. Cost is the
// run's own accounting (types.Done.Cost for a conversation); History
// carries the run's messages so the scorer and the tool-sequence diff
// read the same evidence. Latency is not self-reported: the orchestrator
// measures it around the call.
type ShadowOutcome struct {
	Output  string
	History stores.History
	Cost    float64
}

// Side names one release under comparison: the flow to drive plus the
// release identity every row attributes its results to.
type Side struct {
	Flow      ShadowFlow
	ReleaseID string
	Variant   string // optional; empty variant is recorded as empty
}

// CallerTransport is the serving seam a shadow run must never carry. The
// interface exists so the refusal is typed, not so anything serves: no
// shadow orchestration path ever calls Serve.
type CallerTransport interface {
	Serve(ctx context.Context) error
}

var (
	// ErrCallerServing refuses a caller transport before any run starts
	// (release spec: shadow orchestration serves no caller).
	ErrCallerServing = errors.New("gohan/evals: shadow orchestration serves no caller")
	// ErrShadowConfig rejects an unusable shadow configuration.
	ErrShadowConfig = errors.New("gohan/evals: shadow configuration invalid")
)

// Row is one input's pairwise comparison: per-side cost and latency, the
// tool-sequence diff, and the score delta — never an aggregate alone.
type Row struct {
	CaseID string `json:"case_id"`
	Input  string `json:"input"`

	ChampionReleaseID   string `json:"champion_release_id"`
	ChallengerReleaseID string `json:"challenger_release_id"`
	ChampionVariant     string `json:"champion_variant,omitempty"`
	ChallengerVariant   string `json:"challenger_variant,omitempty"`

	ScoreStatus types.EvalStatus `json:"score_status"`           // scored or unscored, never skipped
	ScoreDelta  float64          `json:"score_delta"`            // challenger − champion; 0 when unscored
	ScoreReason string           `json:"score_reason,omitempty"` // why unscored

	// ToolSequenceDiff is the ordered diff of tool names: "-name" for
	// champion-only calls, "+name" for challenger-only calls.
	ToolSequenceDiff []string `json:"tool_sequence_diff"`

	ChampionCost      float64       `json:"champion_cost"`
	ChallengerCost    float64       `json:"challenger_cost"`
	ChampionLatency   time.Duration `json:"champion_latency_ns"` // measured by the orchestrator
	ChallengerLatency time.Duration `json:"challenger_latency_ns"`

	Err error `json:"-"` // set when a side's run failed; the row stays, unscored
}

// Report is the pairwise shadow result: one Row per input plus the
// release identity both sides ran under. Identity is deterministic over
// canonical JSON, so re-emitting the same report preserves it.
type Report struct {
	Source              string   `json:"source"` // SourceCassette or SourceScripted
	ChampionReleaseID   string   `json:"champion_release_id"`
	ChallengerReleaseID string   `json:"challenger_release_id"`
	ChampionVariant     string   `json:"champion_variant,omitempty"`
	ChallengerVariant   string   `json:"challenger_variant,omitempty"`
	DatasetHash         string   `json:"dataset_hash"` // sha256 over canonical JSON of Inputs
	JudgeFingerprints   []string `json:"judge_fingerprints,omitempty"`
	Rows                []Row    `json:"rows"`
}

// Identity returns the report's deterministic identity: SHA-256 over
// the canonical JSON of the report. The release decision references a
// report through this value only.
func (r Report) Identity() string {
	data, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ShadowRun is one shadow comparison: champion and challenger sides,
// the inputs to replay, and the optional scorer that turns outputs into
// a score delta.
type ShadowRun struct {
	Champion        Side
	Challenger      Side
	Inputs          []Case
	Source          string          // SourceCassette or SourceScripted; defaults to SourceScripted
	Scorer          Evaluator       // optional; nil leaves every row unscored with a reason
	CallerTransport CallerTransport // must stay nil; a non-nil value refuses the whole run
	Logger          *slog.Logger    // optional; defaults to slog.New(slog.DiscardHandler)
}

// Shadow runs champion and challenger over every input (release spec:
// evals.Shadow(champion, challenger Flow, inputs)). The champion runs
// under types.Primary; the challenger runs under types.Shadow with its
// own ReleaseID and Variant. Every input yields one pairwise row; a
// failed side keeps its row unscored with the error. A non-nil
// CallerTransport fails with ErrCallerServing before any run starts.
func Shadow(ctx context.Context, run ShadowRun) (Report, error) {
	logger := run.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if run.CallerTransport != nil {
		return Report{}, fmt.Errorf("%w: candidate transport %T", ErrCallerServing, run.CallerTransport)
	}
	if run.Champion.Flow == nil {
		return Report{}, fmt.Errorf("gohan/evals: champion flow missing: %w", ErrShadowConfig)
	}
	if run.Challenger.Flow == nil {
		return Report{}, fmt.Errorf("gohan/evals: challenger flow missing: %w", ErrShadowConfig)
	}
	if len(run.Inputs) == 0 {
		return Report{}, fmt.Errorf("gohan/evals: no inputs to replay: %w", ErrShadowConfig)
	}
	if run.Champion.ReleaseID == "" {
		return Report{}, fmt.Errorf("gohan/evals: champion release id missing: %w", ErrShadowConfig)
	}
	if run.Challenger.ReleaseID == "" {
		return Report{}, fmt.Errorf("gohan/evals: challenger release id missing: %w", ErrShadowConfig)
	}

	source := run.Source
	if source == "" {
		source = SourceScripted
	}
	datasetHash := sha256.Sum256([]byte(mustJSON(run.Inputs)))

	report := Report{
		Source:              source,
		ChampionReleaseID:   run.Champion.ReleaseID,
		ChallengerReleaseID: run.Challenger.ReleaseID,
		ChampionVariant:     run.Champion.Variant,
		ChallengerVariant:   run.Challenger.Variant,
		DatasetHash:         hex.EncodeToString(datasetHash[:]),
		Rows:                make([]Row, 0, len(run.Inputs)),
	}
	if run.Scorer != nil {
		report.JudgeFingerprints = []string{
			Judge{Dimension: run.Scorer.Dimension(), Version: run.Scorer.Version()}.Fingerprint(),
		}
	}

	for _, c := range run.Inputs {
		caseID := c.ID
		if caseID == "" {
			sum := sha256.Sum256([]byte(c.Input))
			caseID = hex.EncodeToString(sum[:])[:16]
		}

		champOutcome, champInfo, champErr := runSide(ctx, logger, types.Primary, run.Champion, caseID, c.Input)
		challOutcome, challInfo, challErr := runSide(ctx, logger, types.Shadow, run.Challenger, caseID, c.Input)

		row := Row{
			CaseID:              caseID,
			Input:               c.Input,
			ChampionReleaseID:   run.Champion.ReleaseID,
			ChallengerReleaseID: run.Challenger.ReleaseID,
			ChampionVariant:     run.Champion.Variant,
			ChallengerVariant:   run.Challenger.Variant,
			ToolSequenceDiff:    diffToolSeq(toolSequence(champOutcome.History), toolSequence(challOutcome.History)),
			ChampionCost:        champOutcome.Cost,
			ChallengerCost:      challOutcome.Cost,
			ChampionLatency:     champInfo.latency,
			ChallengerLatency:   challInfo.latency,
		}
		if champErr != nil {
			row.Err = fmt.Errorf("champion run failed: %w", champErr)
		}
		if challErr != nil {
			row.Err = errors.Join(row.Err, fmt.Errorf("challenger run failed: %w", challErr))
		}
		row.ScoreStatus, row.ScoreDelta, row.ScoreReason = scorePair(run.Scorer,
			Evidence{Info: champInfo.info, History: champOutcome.History},
			Evidence{Info: challInfo.info, History: challOutcome.History},
			champErr, challErr)
		report.Rows = append(report.Rows, row)
	}
	return report, nil
}

type sideResult struct {
	info    types.RunInfo
	latency time.Duration
}

// runSide drives one side over one input with the side's run identity
// attached and the latency measured around the call.
func runSide(ctx context.Context, logger *slog.Logger, mode types.RunMode, side Side, caseID, input string) (ShadowOutcome, sideResult, error) {
	info := types.RunInfo{
		Flow:      "evals.shadow",
		SessionID: "evals-shadow-" + caseID,
		RunID:     uuid.NewV7().String(),
		ReleaseID: side.ReleaseID,
		Variant:   side.Variant,
		Mode:      mode,
	}
	sctx := types.WithRunInfo(ctx, info)
	start := time.Now()
	outcome, err := side.Flow(sctx, input)
	latency := time.Since(start)
	if err != nil {
		logger.Error("shadow side failed", slog.String("release", side.ReleaseID), slog.String("case", caseID), slog.String("err", err.Error()))
	} else {
		logger.Info("shadow side ran", slog.String("release", side.ReleaseID), slog.String("case", caseID))
	}
	return outcome, sideResult{info: info, latency: latency}, err
}

// scorePair settles one row's score from the two sides' evidence. Both
// evaluations must return EvalScored for a delta; anything else stays
// unscored with the reason.
func scorePair(scorer Evaluator, champ, chall Evidence, champErr, challErr error) (types.EvalStatus, float64, string) {
	if champErr != nil || challErr != nil {
		return types.EvalUnscored, 0, "side run failed"
	}
	if scorer == nil {
		return types.EvalUnscored, 0, "no scorer configured"
	}
	champRes := scorer.Evaluate(champ)
	challRes := scorer.Evaluate(chall)
	if champRes.Status != types.EvalScored {
		return types.EvalUnscored, 0, "champion unscored: " + champRes.Reason
	}
	if challRes.Status != types.EvalScored {
		return types.EvalUnscored, 0, "challenger unscored: " + challRes.Reason
	}
	return types.EvalScored, challRes.Score - champRes.Score, ""
}

// toolSequence collects ToolUse block names in message order.
func toolSequence(h stores.History) []string {
	var names []string
	for _, m := range h.Messages {
		for _, b := range m.Blocks {
			if tu, ok := b.(types.ToolUse); ok {
				names = append(names, tu.Name)
			}
		}
	}
	return names
}

// diffToolSeq emits the ordered LCS diff: "-name" champion-only,
// "+name" challenger-only. diffToolSeq([search book], [search refund])
// == ["-book", "+refund"].
func diffToolSeq(champion, challenger []string) []string {
	n, m := len(champion), len(challenger)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if champion[i] == challenger[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var diff []string
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case champion[i] == challenger[j]:
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			diff = append(diff, "-"+champion[i])
			i++
		default:
			diff = append(diff, "+"+challenger[j])
			j++
		}
	}
	for ; i < n; i++ {
		diff = append(diff, "-"+champion[i])
	}
	for ; j < m; j++ {
		diff = append(diff, "+"+challenger[j])
	}
	return diff
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}
