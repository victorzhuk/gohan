package evals

import (
	"context"
	"errors"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// scoredEval is a deterministic in-process judge that always scores 0.8.
type scoredEval struct{}

func (scoredEval) Dimension() string { return types.EvalQuality }
func (scoredEval) Version() string   { return "scored-eval-v1" }
func (scoredEval) Evaluate(ev Evidence) types.EvalDimensionResult {
	return types.EvalDimensionResult{Status: types.EvalScored, Score: 0.8}
}

// settleSink counts settled jobs: every Ingest is one settled job.
type settleSink struct {
	sinkRecorder
	settled chan struct{}
}

func (s *settleSink) Ingest(ctx context.Context, dest string, recs []types.ExportRecord) error {
	if err := s.sinkRecorder.Ingest(ctx, dest, recs); err != nil {
		return err
	}
	s.settled <- struct{}{}
	return nil
}

// gateJudgeModel is a judge model that signals when a call enters and then
// blocks until its gate closes or the call context is done.
type gateJudgeModel struct {
	gate    chan struct{}
	entered chan struct{}

	once sync.Once
}

func (m *gateJudgeModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "gate-judge", Version: "judge-v1"}
}

func (m *gateJudgeModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		m.once.Do(func() { close(m.entered) })
		select {
		case <-ctx.Done():
			yield(types.ModelChunk{}, ctx.Err())
			return
		case <-m.gate:
		}
		yield(types.ModelChunk{}, nil)
	}
}

// failJudgeModel is a judge model whose every call fails; it counts calls.
type failJudgeModel struct {
	mu    sync.Mutex
	calls int
}

func (m *failJudgeModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "fail-judge", Version: "fail-v1"}
}

func (m *failJudgeModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *failJudgeModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		m.mu.Lock()
		m.calls++
		m.mu.Unlock()
		yield(types.ModelChunk{}, errors.New("judge model unavailable"))
	}
}

// evalBudget is one completion whose settled job costs perJobTokens tokens.
var evalBudget = struct {
	perJobTokens int64
	perJobCost   float64
}{perJobTokens: 30, perJobCost: 0.02}

func liveWorkers(o *Online) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.live
}

func TestBudget(t *testing.T) {
	t.Run("budget exactly-once under concurrent admission", func(t *testing.T) {
		const k = 3
		const extra = 5
		const n = k + extra
		rec := &settleSink{settled: make(chan struct{}, n)}
		eng, err := NewOnline(1, []Judge{{Dimension: types.EvalQuality, Evaluator: scoredEval{}}}, WithSink(rec, "dest-eval"))
		if err != nil {
			t.Fatalf("NewOnline: %v", err)
		}
		eng.Limits = OnlineLimits{
			Workers:       k,
			QueueCapacity: n,
			TokenBudget:   evalBudget.perJobTokens * k,
			CostBudget:    1e9,
		}

		// Wave 1: k concurrent offers while the window spend is zero — all
		// k are admitted and settle, consuming exactly the whole budget.
		var mu sync.Mutex
		var firstErr error
		var wg sync.WaitGroup
		admit := func(from, count int) {
			for i := from; i < from+count; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					if err := eng.Evaluate(context.Background(), evalCompletionWithDone("s"+string(rune('a'+i)))); err != nil {
						mu.Lock()
						if firstErr == nil {
							firstErr = err
						}
						mu.Unlock()
					}
				}(i)
			}
		}
		admit(0, k)
		for range k {
			<-rec.settled
		}
		wg.Wait()
		if firstErr != nil {
			t.Fatalf("Evaluate returned error on the admission path: %v", firstErr)
		}
		if got := len(rec.snapshot()); got != k {
			t.Fatalf("settled records = %d, want exactly %d admitted", got, k)
		}
		if r := eng.Rates(); r.Eligible != k || r.Evaluated != k || r.Skipped != 0 || r.Unknown != 0 {
			t.Fatalf("rates after wave 1 = %+v, want %d eligible, %d evaluated, 0 skipped, 0 unknown", r, k, k)
		}

		// Wave 2: N-k concurrent offers against a fully consumed budget —
		// every one is refused as budget_exhausted (the only skip path
		// armed: queue open, Rate=1, engine running).
		admit(k, extra)
		wg.Wait()
		if r := eng.Rates(); r.Skipped != extra || r.Eligible != n {
			t.Fatalf("rates after wave 2 = %+v, want %d skipped of %d eligible", r, extra, n)
		}
		if got := len(rec.snapshot()); got != k {
			t.Fatalf("records after refusal = %d, want %d", got, k)
		}
		eng.mu.Lock()
		spent := eng.tokensUsed
		eng.mu.Unlock()
		if spent != evalBudget.perJobTokens*k {
			t.Fatalf("window spend = %d tokens, want exactly %d", spent, evalBudget.perJobTokens*k)
		}
		if spent > eng.Limits.TokenBudget {
			t.Fatalf("window spend %d exceeds budget %d", spent, eng.Limits.TokenBudget)
		}
	})

	t.Run("queue capacity boundary", func(t *testing.T) {
		const c = 3
		gate := make(chan struct{})
		m := &gateJudgeModel{gate: gate, entered: make(chan struct{})}
		rec := &settleSink{settled: make(chan struct{}, c+2)}
		eng, err := NewOnline(1, []Judge{NewJudge(types.EvalQuality, m, "judge-v1", Rubric{Version: "rub-v1", Instruction: "score"})}, WithSink(rec, "dest-eval"))
		if err != nil {
			t.Fatalf("NewOnline: %v", err)
		}
		eng.Limits = OnlineLimits{Workers: 1, QueueCapacity: c, JobDeadline: time.Hour, CostBudget: 1e9}

		// Occupy the single worker with a blocked judge call, then put the
		// queue boundary under concurrent load: C+1 offers race for the C
		// pending slots; the last one must be skipped, none may block.
		if err := eng.Evaluate(context.Background(), evalCompletion("q0")); err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		select {
		case <-m.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("worker never picked up the first job")
		}

		var mu sync.Mutex
		var firstErr error
		var wg sync.WaitGroup
		for i := 1; i <= c+1; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if err := eng.Evaluate(context.Background(), evalCompletion("q"+string(rune('0'+i)))); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
				}
			}(i)
		}
		wg.Wait()
		if firstErr != nil {
			t.Fatalf("Evaluate blocked or errored on the queue boundary: %v", firstErr)
		}
		if r := eng.Rates(); r.Skipped != 1 {
			t.Fatalf("skipped = %d, want exactly the last offer of %d+1 skipped (queue_full)", r.Skipped, c)
		}
		if r := eng.Rates(); r.Unknown != c+1 {
			t.Fatalf("unknown = %d, want %d admitted offers (1 running + %d queued)", r.Unknown, c+1, c)
		}

		close(gate)
		if err := eng.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if got := len(rec.snapshot()); got != c+1 {
			t.Fatalf("settled records = %d, want %d", got, c+1)
		}
	})

	t.Run("retry bound", func(t *testing.T) {
		const maxRetries = 2
		m := &failJudgeModel{}
		rec := &settleSink{settled: make(chan struct{}, 1)}
		eng, err := NewOnline(1, []Judge{NewJudge(types.EvalQuality, m, "fail-v1", Rubric{Version: "rub-v1", Instruction: "score"})}, WithSink(rec, "dest-eval"))
		if err != nil {
			t.Fatalf("NewOnline: %v", err)
		}
		eng.Limits = OnlineLimits{Workers: 1, MaxRetries: maxRetries, JobDeadline: time.Hour}

		if err := eng.Evaluate(context.Background(), evalCompletion("r0")); err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		<-rec.settled
		if err := eng.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if got := m.callCount(); got != 1+maxRetries {
			t.Fatalf("judge calls = %d, want exactly %d (1 initial + %d retries)", got, 1+maxRetries, maxRetries)
		}
		recs := rec.snapshot()
		if len(recs) != 1 {
			t.Fatalf("settled records = %d, want 1", len(recs))
		}
		results := recs[0].Eval.Results
		if len(results) != 1 {
			t.Fatalf("results = %d, want 1", len(results))
		}
		r := results[0]
		if r.Status != types.EvalUnscored || r.ErrorCategory != types.EvalErrJudgeFailed {
			t.Fatalf("result = %+v, want unscored/judge_failed after exhausting retries", r)
		}
		if r.Reason == "" {
			t.Fatal("unscored result carries no reason")
		}
		if eng.Rates().Failed != 1 {
			t.Fatalf("rates = %+v, want Failed=1 for an all-unscored admitted job", eng.Rates())
		}
	})

	t.Run("closure settles per policy", func(t *testing.T) {
		t.Run("close unscored", func(t *testing.T) {
			gate := make(chan struct{})
			m := &gateJudgeModel{gate: gate, entered: make(chan struct{})}
			rec := &settleSink{settled: make(chan struct{}, 1)}
			eng, err := NewOnline(1, []Judge{NewJudge(types.EvalQuality, m, "judge-v1", Rubric{Version: "rub-v1", Instruction: "score"})}, WithSink(rec, "dest-eval"))
			if err != nil {
				t.Fatalf("NewOnline: %v", err)
			}
			eng.Closure = CloseUnscored
			eng.Limits = OnlineLimits{Workers: 1, JobDeadline: time.Hour}

			if err := eng.Evaluate(context.Background(), evalCompletion("c0")); err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			<-m.entered
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := eng.Shutdown(ctx); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			if live := liveWorkers(eng); live != 0 {
				t.Fatalf("live workers after Shutdown = %d, want 0", live)
			}
			recs := rec.snapshot()
			if len(recs) != 1 {
				t.Fatalf("settled records = %d, want the owned job settled as unscored/timeout", len(recs))
			}
			results := recs[0].Eval.Results
			if len(results) != 1 || results[0].Status != types.EvalUnscored || results[0].ErrorCategory != types.EvalErrTimeout {
				t.Fatalf("results = %+v, want unscored/timeout under CloseUnscored", results)
			}
			// The engine stays shut down: post-closure offers are drained.
			before := eng.Rates().Skipped
			if err := eng.Evaluate(context.Background(), evalCompletion("c1")); err != nil {
				t.Fatalf("post-shutdown Evaluate: %v", err)
			}
			if after := eng.Rates().Skipped; after != before+1 {
				t.Fatalf("skipped after post-shutdown offer = %d, want %d+1 (drained)", after, before)
			}
		})

		t.Run("close skipped", func(t *testing.T) {
			gate := make(chan struct{})
			m := &gateJudgeModel{gate: gate, entered: make(chan struct{})}
			rec := &settleSink{settled: make(chan struct{}, 1)}
			eng, err := NewOnline(1, []Judge{NewJudge(types.EvalQuality, m, "judge-v1", Rubric{Version: "rub-v1", Instruction: "score"})}, WithSink(rec, "dest-eval"))
			if err != nil {
				t.Fatalf("NewOnline: %v", err)
			}
			eng.Closure = CloseSkipped
			eng.Limits = OnlineLimits{Workers: 1, JobDeadline: time.Hour}

			if err := eng.Evaluate(context.Background(), evalCompletion("c2")); err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			<-m.entered
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := eng.Shutdown(ctx); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			if live := liveWorkers(eng); live != 0 {
				t.Fatalf("live workers after Shutdown = %d, want 0", live)
			}
			if recs := rec.snapshot(); len(recs) != 0 {
				t.Fatalf("settled records = %d, want 0 under CloseSkipped (job skipped, not scored)", len(recs))
			}
			if r := eng.Rates(); r.Skipped != 1 || r.Unknown != 0 || r.Evaluated != 0 {
				t.Fatalf("rates = %+v, want the owned job skipped/drained and no unknown work", r)
			}
		})
	})
}
