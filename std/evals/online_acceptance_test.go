package evals

import (
	"context"
	"iter"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// sinkRecorder is a types.RecordSink that keeps every ingested record.
type sinkRecorder struct {
	mu      sync.Mutex
	records []types.ExportRecord
}

func (s *sinkRecorder) Ingest(ctx context.Context, dest string, recs []types.ExportRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, recs...)
	return nil
}

func (s *sinkRecorder) Flush(ctx context.Context) (types.UnresolvedWork, error) {
	return types.UnresolvedWork{}, nil
}

func (s *sinkRecorder) Shutdown(ctx context.Context) (types.UnresolvedWork, error) {
	return types.UnresolvedWork{}, nil
}

func (s *sinkRecorder) snapshot() []types.ExportRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]types.ExportRecord(nil), s.records...)
}

// gateModel is a judge model whose Generate calls block until the gate
// closes or the call context is done.
type gateModel struct {
	gate chan struct{}

	mu      sync.Mutex
	live    int
	maxLive int
}

func (m *gateModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "gate-judge", Version: "judge-v1"}
}

func (m *gateModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		m.mu.Lock()
		m.live++
		if m.live > m.maxLive {
			m.maxLive = m.live
		}
		m.mu.Unlock()
		defer func() {
			m.mu.Lock()
			m.live--
			m.mu.Unlock()
		}()

		select {
		case <-ctx.Done():
			yield(types.ModelChunk{}, ctx.Err())
			return
		case <-m.gate:
		}
		yield(types.ModelChunk{}, nil)
	}
}

func (m *gateModel) maxConcurrent() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxLive
}

func evalMessage(role types.Role, origin types.OriginKind, text string) types.Message {
	return types.Message{
		Role: role,
		Blocks: []types.Block{types.Text{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: origin}},
			Text:      text,
		}},
	}
}

func evalCompletion(sessionID string) gohan.RunCompletion {
	return gohan.RunCompletion{
		Info: types.RunInfo{
			Flow:      "assist",
			SessionID: sessionID,
			RunID:     "run-" + sessionID,
			ReleaseID: "rel-1",
			Variant:   "control",
			Mode:      types.Primary,
		},
		Version: 7,
		History: func(ctx context.Context) (stores.History, error) {
			return stores.History{
				Messages: []types.Message{
					evalMessage(types.RoleUser, types.OriginUser, "What is the deploy status?"),
					evalMessage(types.RoleAssistant, types.OriginModel, "Done."),
				},
				Version: 7,
			}, nil
		},
	}
}

func TestReleaseOnline(t *testing.T) {
	t.Run("release.online-bounded", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rec := &sinkRecorder{}
			gate := make(chan struct{})
			defer close(gate)
			blocked := NewJudge(types.EvalQuality, &gateModel{gate: gate}, "judge-v1",
				Rubric{Version: "rub-v1", Instruction: "score quality"}, WithRepeat(DefaultJudgeRepeat))
			eng, err := NewOnline(1, []Judge{blocked}, WithSink(rec, "dest-eval"))
			if err != nil {
				t.Fatalf("NewOnline: %v", err)
			}
			eng.Limits = OnlineLimits{QueueCapacity: 4, Workers: 1}

			// (1) The terminal path is not delayed: Evaluate admits while the
			// judge model is still blocked, at the cost of an admission-only
			// call (the no-evaluator delay bound).
			baseline, err := NewOnline(1, []Judge{Faithfulness()}, WithSink(&sinkRecorder{}, "dest-eval"))
			if err != nil {
				t.Fatalf("NewOnline baseline: %v", err)
			}
			baseline.Limits = OnlineLimits{QueueCapacity: 4, Workers: 1}

			const admissionBound = 5 * time.Second
			start := time.Now()
			if err := eng.Evaluate(context.Background(), evalCompletion("sb1")); err != nil {
				t.Fatalf("Evaluate with blocked judges: %v", err)
			}
			blockedElapsed := time.Since(start)

			baseStart := time.Now()
			if err := baseline.Evaluate(context.Background(), evalCompletion("sb-base")); err != nil {
				t.Fatalf("Evaluate on the admission-only baseline: %v", err)
			}
			baseElapsed := time.Since(baseStart)

			select {
			case <-gate:
				t.Error("judge gate released during admission; Evaluate waited on judge work")
			default:
			}
			if blockedElapsed > admissionBound || blockedElapsed > baseElapsed+admissionBound {
				t.Errorf("Evaluate with blocked judges returned in %v (baseline %v); admission must not wait on judge work", blockedElapsed, baseElapsed)
			}

			// (2) Completions beyond queue capacity are skipped, not blocked:
			// one job picked up by the single worker, four queued, two skipped.
			for i := 2; i <= 7; i++ {
				if err := eng.Evaluate(context.Background(), evalCompletion(fmtSession("sb", i))); err != nil {
					t.Fatalf("Evaluate sb%d: %v", i, err)
				}
			}
			rates := eng.Rates()
			if rates.Eligible != 7 {
				t.Errorf("Rates().Eligible = %d; want 7 offered terminal primary completions", rates.Eligible)
			}
			if rates.Skipped != 2 {
				t.Errorf("Rates().Skipped = %d; want 2 completions beyond queue capacity skipped", rates.Skipped)
			}

			if err := eng.Shutdown(context.Background()); err != nil {
				t.Errorf("Shutdown: %v", err)
			}
			if minted := len(rec.snapshot()); minted > 5 {
				t.Errorf("%d eval records minted; only the 5 admitted offers may mint records", minted)
			}
			synctest.Wait()

			// (3) Worker bound: six admitted jobs through one worker never
			// exceed the concurrency cap and all settle with a record.
			rec3 := &sinkRecorder{}
			gate3 := make(chan struct{})
			model3 := &gateModel{gate: gate3}
			eng3, err := NewOnline(1, []Judge{NewJudge(types.EvalQuality, model3, "judge-v1",
				Rubric{Version: "rub-v1", Instruction: "score quality"})}, WithSink(rec3, "dest-eval"))
			if err != nil {
				t.Fatalf("NewOnline eng3: %v", err)
			}
			eng3.Limits = OnlineLimits{QueueCapacity: DefaultQueueCapacity, Workers: 1}
			for i := 1; i <= 6; i++ {
				if err := eng3.Evaluate(context.Background(), evalCompletion(fmtSession("sw", i))); err != nil {
					t.Fatalf("Evaluate sw%d: %v", i, err)
				}
			}
			close(gate3)
			if err := eng3.Shutdown(context.Background()); err != nil {
				t.Fatalf("Shutdown eng3: %v", err)
			}
			synctest.Wait()

			if live := model3.maxConcurrent(); live > 1 {
				t.Errorf("%d concurrent judge calls; Workers=1 must bound the pool", live)
			}
			rates3 := eng3.Rates()
			if rates3.Evaluated != 6 {
				t.Errorf("Rates().Evaluated = %d; want 6 settled jobs with scored results", rates3.Evaluated)
			}
			if got := len(rec3.snapshot()); got < 6 {
				t.Errorf("%d eval records minted; want one per settled job (6)", got)
			}
		})
	})

	t.Run("release.online-unscored", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rec := &sinkRecorder{}
			gate := make(chan struct{})
			defer close(gate)
			blocked := NewJudge(types.EvalQuality, &gateModel{gate: gate}, "judge-v1",
				Rubric{Version: "rub-v1", Instruction: "score quality"}, WithRepeat(DefaultJudgeRepeat))
			eng, err := NewOnline(1, []Judge{Faithfulness(), ContextRecall(), blocked}, WithSink(rec, "dest-eval"))
			if err != nil {
				t.Fatalf("NewOnline: %v", err)
			}

			comp := evalCompletion("su1")
			if err := eng.Evaluate(context.Background(), comp); err != nil {
				t.Fatalf("Evaluate: %v", err)
			}

			shutdownCtx, cancel := context.WithTimeout(context.Background(), DefaultJobDeadline)
			defer cancel()
			if err := eng.Shutdown(shutdownCtx); err != nil {
				t.Errorf("Shutdown: %v", err)
			}
			synctest.Wait()

			records := rec.snapshot()
			if len(records) == 0 {
				t.Fatalf("no eval record settled; want faithfulness unscored/missing_evidence, context-recall unscored/absent_reference and quality unscored/timeout within the JobDeadline")
			}

			type want struct {
				status    types.EvalStatus
				category  types.EvalErrorCategory
				wantTrue  bool // category must match
				wantFalse bool // category must be empty
			}
			expected := map[string]want{
				types.EvalFaithfulness:  {status: types.EvalUnscored, category: types.EvalErrMissingEvidence, wantTrue: true},
				types.EvalContextRecall: {status: types.EvalUnscored, category: types.EvalErrAbsentReference, wantTrue: true},
				types.EvalQuality:       {status: types.EvalUnscored, category: types.EvalErrTimeout, wantTrue: true},
			}
			seen := map[string]int{}

			for _, r := range records {
				if r.Signal != types.SignalEval || r.Eval == nil {
					t.Errorf("record %s carries signal %q; want eval with an eval payload", r.ID, r.Signal)
					continue
				}
				ev := r.Eval
				if ev.EvaluationID == "" {
					t.Error("EvalRecord.EvaluationID empty; the evaluation ID is minted once at admission")
				}
				if ev.RunID != comp.Info.RunID {
					t.Errorf("EvalRecord.RunID = %q; want %q", ev.RunID, comp.Info.RunID)
				}
				if settle := ev.CompletedAt.Sub(ev.StartedAt); settle > DefaultJobDeadline+5*time.Second {
					t.Errorf("job settled in %v; JobDeadline %v must bound settling", settle, DefaultJobDeadline)
				}
				for _, res := range ev.Results {
					seen[res.Dimension]++
					if seen[res.Dimension] > 1 {
						t.Errorf("two results for dimension %s in one record", res.Dimension)
						continue
					}
					w, ok := expected[res.Dimension]
					if !ok {
						t.Errorf("unexpected dimension %s in record", res.Dimension)
						continue
					}
					if res.Status != w.status {
						t.Errorf("%s result status = %q verbatim-in; want %q", res.Dimension, res.Status, w.status)
					}
					if w.wantTrue && res.ErrorCategory != w.category {
						t.Errorf("%s result error_category = %q; want %q with a non-empty reason", res.Dimension, res.ErrorCategory, w.category)
					}
					if w.wantTrue && res.Reason == "" {
						t.Errorf("%s unscored result has empty Reason; want the unscored cause recorded", res.Dimension)
					}
					if res.Status == types.EvalUnscored && res.PassRequired(0) {
						t.Errorf("unscored %s result passed PassRequired(0); unscored results never pass a required gate", res.Dimension)
					}
					if res.Status == types.EvalScored && res.ErrorCategory != "" {
						t.Errorf("scored %s result carries error_category %q; scored results have no error category", res.Dimension, res.ErrorCategory)
					}
				}
			}
			for dim, n := range seen {
				if n != 1 {
					t.Errorf("dimension %s settled %d times; want exactly one result", dim, n)
				}
			}
			for dim := range expected {
				if seen[dim] == 0 {
					t.Errorf("no result for dimension %s; want unscored with reason", dim)
				}
			}
		})
	})
}

func fmtSession(prefix string, i int) string {
	return prefix + string(rune('0'+i))
}
