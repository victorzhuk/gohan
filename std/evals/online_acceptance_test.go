package evals

import (
	"context"
	"errors"
	"iter"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/telemetry"
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
				status   types.EvalStatus
				category types.EvalErrorCategory
				wantTrue bool // category must match
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

	t.Run("release.online-provenance", func(t *testing.T) {
		dir := t.TempDir()
		outage := &outcomeDest{
			name:    "dest-eval",
			signals: []types.ExportSignalKind{types.SignalSpan, types.SignalMetric, types.SignalEval},
			fail:    true,
		}
		spoolCfg := telemetry.SpoolConfig{
			ByteQuota:       1 << 20,
			SegmentBytes:    64 << 10,
			MaxPendingAge:   time.Hour,
			Retry:           telemetry.RetryPolicy{MaxAttempts: 1, Backoff: time.Millisecond, MaxBackoff: time.Millisecond},
			Workers:         1,
			RequestDeadline: time.Second,
		}
		spool, err := telemetry.OpenSpool(dir, []types.ExportDestination{outage}, spoolCfg)
		if err != nil {
			t.Fatalf("OpenSpool: %v", err)
		}

		tel := &telRecorder{}
		met, err := telemetry.Build(tel, Metrics(), telemetry.WithRelease("rel-1"), telemetry.WithVariant("control"))
		if err != nil {
			t.Fatalf("telemetry.Build: %v", err)
		}

		judge := NewJudge(types.EvalQuality, &fixedModel{version: "judge-v1", delta: "0.8"}, "judge-v1",
			Rubric{Version: "rub-v1", Instruction: "score quality"}, WithRepeat(DefaultJudgeRepeat))
		capture := &captureSink{inner: telemetry.SpoolSink(spool)}
		eng, err := NewOnline(1, []Judge{judge}, WithSink(capture, "dest-eval"), WithMetrics(met))
		if err != nil {
			t.Fatalf("NewOnline: %v", err)
		}

		comp := evalCompletionWithDone("sp1")
		if err := eng.Evaluate(context.Background(), comp); err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		// Outage leg: the engine shuts down, the destination refuses every
		// delivery attempt, so the admitted record stays durable pending.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := eng.Shutdown(shutdownCtx); err != nil {
			t.Fatalf("Shutdown engine: %v", err)
		}
		flush, err := spool.Flush(shutdownCtx)
		if err != nil {
			t.Errorf("Flush against the outage destination: %v; parked records report undelivered work, Flush does not fail", err)
		}
		pairs := capture.pairs()
		if flush.Undelivered.Records != int64(len(pairs)) {
			t.Errorf("Flush undelivered records = %d; want the %d parked eval records reported", flush.Undelivered.Records, len(pairs))
		}
		if flush.Undelivered.ByDestination["dest-eval"] != int64(len(pairs)) {
			t.Errorf("Flush undelivered for dest-eval = %d; want %d parked for the outage destination", flush.Undelivered.ByDestination["dest-eval"], len(pairs))
		}
		if _, err := spool.Shutdown(shutdownCtx); err != nil {
			t.Fatalf("Shutdown spool: %v", err)
		}
		if len(outage.delivered()) != 0 {
			t.Errorf("%d records delivered to the outage destination; the record must stay pending", len(outage.delivered()))
		}

		// Restart leg: reopen the same spool directory with a healthy
		// destination; the record must replay with identical identity.
		good := &outcomeDest{
			name:    "dest-eval",
			signals: []types.ExportSignalKind{types.SignalSpan, types.SignalMetric, types.SignalEval},
		}
		spool2, err := telemetry.OpenSpool(dir, []types.ExportDestination{good}, spoolCfg)
		if err != nil {
			t.Fatalf("reopen OpenSpool: %v", err)
		}
		flushCtx, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel2()
		if _, err := spool2.Flush(flushCtx); err != nil {
			t.Fatalf("Flush after restart: %v", err)
		}
		if _, err := spool2.Shutdown(flushCtx); err != nil {
			t.Fatalf("Shutdown reopened spool: %v", err)
		}

		delivered := good.delivered()
		if len(delivered) == 0 {
			t.Fatalf("no record delivered after spool restart; the pending eval record must replay")
		}
		if len(pairs) == 0 {
			t.Fatalf("no eval record was ingested into the spool; one settled job must mint one record")
		}
		byID := map[types.RecordID]string{}
		for _, p := range pairs {
			byID[p.recordID] = p.evaluationID
		}
		for _, r := range delivered {
			if r.Signal != types.SignalEval || r.Eval == nil {
				t.Errorf("delivered record %s carries signal %q; want eval with an eval payload", r.ID, r.Signal)
				continue
			}
			wantEvalID, ok := byID[r.ID]
			if !ok {
				t.Errorf("delivered record ID %s was never ingested; replay must preserve ExportRecord.ID", r.ID)
				continue
			}
			if r.Eval.EvaluationID != wantEvalID {
				t.Errorf("delivered EvaluationID = %q; want the identical admission identity %q across the outage and restart", r.Eval.EvaluationID, wantEvalID)
			}

			ev := r.Eval
			if ev.ReleaseID != "rel-1" {
				t.Errorf("EvalRecord.ReleaseID = %q; want %q", ev.ReleaseID, "rel-1")
			}
			if ev.Variant != "control" {
				t.Errorf("EvalRecord.Variant = %q; want %q", ev.Variant, "control")
			}
			if ev.Flow != comp.Info.Flow {
				t.Errorf("EvalRecord.Flow = %q; want %q", ev.Flow, comp.Info.Flow)
			}
			if ev.RunID != comp.Info.RunID {
				t.Errorf("EvalRecord.RunID = %q; want %q", ev.RunID, comp.Info.RunID)
			}
			if len(ev.Results) == 0 {
				t.Error("EvalRecord carries no Results; each configured dimension settles as an independent named result")
			} else {
				seen := map[string]bool{}
				for _, res := range ev.Results {
					if seen[res.Dimension] {
						t.Errorf("two results for dimension %s in one record; per-dimension results stay independent", res.Dimension)
					}
					seen[res.Dimension] = true
					if res.Status != types.EvalScored {
						t.Errorf("%s result status = %q; want scored with an independent per-dimension score", res.Dimension, res.Status)
						continue
					}
					if res.Median != 0.8 {
						t.Errorf("%s result median = %v; want the judged median 0.8", res.Dimension, res.Median)
					}
					if res.Rubric != "rub-v1" {
						t.Errorf("%s result rubric version = %q; want %q carried per result", res.Dimension, res.Rubric, "rub-v1")
					}
					if res.Model != "judge-v1" {
						t.Errorf("%s result model version = %q; want %q carried per result", res.Dimension, res.Model, "judge-v1")
					}
					if len(res.Judgements) != DefaultJudgeRepeat {
						t.Errorf("%s result preserves %d judgements; want all %d repeated judgements", res.Dimension, len(res.Judgements), DefaultJudgeRepeat)
					}
				}
			}
			if ev.Sampling.Probability != 1 {
				t.Errorf("Sampling.Probability = %v; want the admission probability 1", ev.Sampling.Probability)
			}
			if ev.Sampling.Reason != ReasonSampled {
				t.Errorf("Sampling.Reason = %q; want %q", ev.Sampling.Reason, ReasonSampled)
			}
			if ev.Sampling.Policy != SelectionUniform {
				t.Errorf("Sampling.Policy = %q; want %q", ev.Sampling.Policy, SelectionUniform)
			}
			var haveRunOutput, haveHistory bool
			for _, e := range ev.Evidence {
				switch e.Kind {
				case types.EvidenceRunOutput:
					haveRunOutput = e.Ref == comp.Info.RunID
				case types.EvidenceHistoryVersion:
					haveHistory = e.Ref == "7"
				}
			}
			if !haveRunOutput {
				t.Errorf("Evidence lacks a run_output ref naming %q; the record carries evidence references", comp.Info.RunID)
			}
			if !haveHistory {
				t.Errorf("Evidence lacks a history_version ref naming version 7; the record carries evidence references")
			}
			if ev.StartedAt.IsZero() || ev.CompletedAt.IsZero() {
				t.Error("StartedAt or CompletedAt zero; the record carries both timestamps")
			} else if ev.CompletedAt.Before(ev.StartedAt) {
				t.Errorf("CompletedAt %v before StartedAt %v", ev.CompletedAt, ev.StartedAt)
			}
			if ev.Tokens != 30 {
				t.Errorf("EvalRecord.Tokens = %d; want the run usage 30", ev.Tokens)
			}
			if ev.Cost != 0.02 {
				t.Errorf("EvalRecord.Cost = %v; want the run cost 0.02", ev.Cost)
			}
		}

		// Metric provenance: the score emission names flow and judge (plus
		// the wired release/variant stamps) and never an identity label.
		points := tel.named(telemetry.MetricEvalScore)
		if len(points) == 0 {
			t.Errorf("no %s points recorded; every scored dimension emits the score metric", telemetry.MetricEvalScore)
		}
		for _, p := range points {
			attrs := attrMap(p.attrs)
			if attrs["flow"] != comp.Info.Flow {
				t.Errorf("score metric flow label = %q; want %q", attrs["flow"], comp.Info.Flow)
			}
			if attrs["judge"] == "" {
				t.Error("score metric carries no judge label")
			}
			if attrs[types.KeyRelease] != "rel-1" || attrs[types.KeyVariant] != "control" {
				t.Errorf("score metric stamps release=%q variant=%q; want the wired rel-1/control", attrs[types.KeyRelease], attrs[types.KeyVariant])
			}
			for _, forbidden := range []string{"run_id", "session_id", "subject"} {
				if _, ok := attrs[forbidden]; ok {
					t.Errorf("score metric carries forbidden identity label %q", forbidden)
				}
			}
		}

		// A destination that does not declare SignalEval fails a pipeline
		// requiring eval before any record is admitted.
		plain := &outcomeDest{
			name:    "dest-plain",
			signals: []types.ExportSignalKind{types.SignalSpan, types.SignalMetric},
		}
		_, err = telemetry.NewPipeline([]types.ExportDestination{plain},
			telemetry.WithExportSignals(types.SignalSpan, types.SignalMetric, types.SignalEval))
		if !errors.Is(err, types.ErrExportUnsupportedSignal) {
			t.Errorf("NewPipeline over a destination without SignalEval: err = %v; want types.ErrExportUnsupportedSignal", err)
		}
	})

	t.Run("release.online-calibration", func(t *testing.T) {
		set := CalibrationSet{
			Version: "cal-set-v1",
			Labels: []CalibratedLabel{
				{
					ID:    "l1",
					Class: "easy",
					Evidence: Evidence{History: stores.History{
						Messages: []types.Message{
							evalMessage(types.RoleUser, types.OriginUser, "Say hello."),
							evalMessage(types.RoleAssistant, types.OriginModel, "Hello!"),
						},
						Version: 3,
					}},
					Expected: map[string]float64{types.EvalQuality: 0.8},
				},
				{
					ID:    "l2",
					Class: "hard",
					Evidence: Evidence{History: stores.History{
						Messages: []types.Message{
							evalMessage(types.RoleUser, types.OriginUser, "What is 2+2?"),
							evalMessage(types.RoleAssistant, types.OriginModel, "4."),
						},
						Version: 5,
					}},
					Expected: map[string]float64{types.EvalQuality: 0.8},
				},
			},
		}
		criteria := AcceptanceCriteria{
			MinAgreement:  0.5,
			Tolerance:     DefaultCalibrationTolerance,
			MaxClassError: map[string]float64{"easy": 0.2, "hard": 0.2},
		}
		gate := CalibrationGate{Set: set, Criteria: criteria}

		judge := NewJudge(types.EvalQuality, &fixedModel{version: "judge-v1", delta: "0.8"}, "judge-v1",
			Rubric{Version: "rub-v1", Instruction: "score quality"})

		// (1) A passing review grants an approval covering the fingerprint.
		appr, err := gate.Review(context.Background(), judge)
		if err != nil {
			t.Fatalf("Review over the versioned labelled set: %v", err)
		}
		if appr.Fingerprint != judge.Fingerprint() {
			t.Errorf("approval fingerprint = %q; want the judge fingerprint %q it was reviewed under", appr.Fingerprint, judge.Fingerprint())
		}
		if appr.SetVersion != "cal-set-v1" {
			t.Errorf("approval SetVersion = %q; want the reviewed set version cal-set-v1", appr.SetVersion)
		}

		rec := &sinkRecorder{}
		eng, err := NewOnline(1, []Judge{judge}, WithSink(rec, "dest-eval"))
		if err != nil {
			t.Fatalf("NewOnline: %v", err)
		}
		if err := eng.Activate(appr); err != nil {
			t.Fatalf("Activate with the matching approval: %v", err)
		}
		if err := eng.Validate(); err != nil {
			t.Errorf("Validate with the matching approval: %v", err)
		}
		if err := eng.Start(); err != nil {
			t.Errorf("Start with the matching approval: %v", err)
		}

		// (2) A judge whose version changed loses its approval: Start and
		// Validate refuse, and the stranded approval reports invalidated.
		changed := NewJudge(types.EvalQuality, &fixedModel{version: "judge-v2", delta: "0.8"}, "judge-v2",
			Rubric{Version: "rub-v2", Instruction: "score quality v2"})
		if changed.Fingerprint() == judge.Fingerprint() {
			t.Fatal("judge fingerprint unchanged after model and rubric change; the fingerprint must cover model and rubric versions")
		}
		eng2, err := NewOnline(1, []Judge{changed}, WithSink(&sinkRecorder{}, "dest-eval"))
		if err != nil {
			t.Fatalf("NewOnline with the changed judge: %v", err)
		}
		if err := eng2.Validate(); !errors.Is(err, ErrCalibrationRequired) {
			t.Errorf("Validate without a covering approval: err = %v; want errors.Is ErrCalibrationRequired", err)
		}
		if err := eng2.Start(); !errors.Is(err, ErrCalibrationRequired) {
			t.Errorf("Start without a covering approval: err = %v; want errors.Is ErrCalibrationRequired", err)
		}
		if err := eng2.Activate(appr); !errors.Is(err, ErrCalibrationInvalidated) {
			t.Errorf("Activate of the stranded approval: err = %v; want errors.Is ErrCalibrationInvalidated", err)
		}

		// (3) Re-review with the changed judge passes, activates and starts.
		appr2, err := gate.Review(context.Background(), changed)
		if err != nil {
			t.Fatalf("re-Review with the changed judge: %v", err)
		}
		if appr2.Fingerprint != changed.Fingerprint() {
			t.Errorf("re-review approval fingerprint = %q; want %q", appr2.Fingerprint, changed.Fingerprint())
		}
		if err := eng2.Activate(appr2); err != nil {
			t.Errorf("Activate with the re-reviewed approval: %v", err)
		}
		if err := eng2.Start(); err != nil {
			t.Errorf("Start after re-calibration: %v", err)
		}

		// (4) Deterministic evaluators need no approval.
		det, err := NewOnline(1, []Judge{Faithfulness()}, WithSink(&sinkRecorder{}, "dest-eval"))
		if err != nil {
			t.Fatalf("NewOnline with deterministic judges: %v", err)
		}
		if err := det.Validate(); err != nil {
			t.Errorf("Validate without any approval for deterministic evaluators: %v", err)
		}
		if err := det.Start(); err != nil {
			t.Errorf("Start without any approval for deterministic evaluators: %v", err)
		}

		// A criteria miss wraps ErrCalibrationCriteria and names the class.
		miss := set
		miss.Labels = append([]CalibratedLabel(nil), set.Labels...)
		miss.Labels[0] = set.Labels[0]
		miss.Labels[0].Expected = map[string]float64{types.EvalQuality: 1.0}
		missGate := CalibrationGate{Set: miss, Criteria: criteria}
		if _, err := missGate.Review(context.Background(), judge); !errors.Is(err, ErrCalibrationCriteria) {
			t.Errorf("Review with a label outside tolerance: err = %v; want errors.Is ErrCalibrationCriteria naming the failing class", err)
		}
	})
}

// fixedModel is a deterministic judge model always answering the same
// score token under a configurable profile version.
type fixedModel struct {
	version string
	delta   string
}

func (m *fixedModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "scripted-judge", Version: m.version}
}

func (m *fixedModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: m.delta, Finish: types.FinishStop}, nil)
	}
}

// evalCompletionWithDone is a terminal primary completion carrying real
// usage and cost so the settled record's cost and token accounting are
// assertable.
func evalCompletionWithDone(sessionID string) gohan.RunCompletion {
	comp := evalCompletion(sessionID)
	comp.Done = types.Done{
		Reason: types.StopCompleted,
		Seq:    1,
		Usage:  types.Usage{InputTokens: 10, OutputTokens: 20},
		Cost:   0.02,
	}
	return comp
}

type recordPair struct {
	recordID     types.RecordID
	evaluationID string
}

// captureSink forwards to an inner sink and remembers every record's
// envelope ID and evaluation ID at admission time.
type captureSink struct {
	inner types.RecordSink

	mu  sync.Mutex
	ids []recordPair
}

func (s *captureSink) Ingest(ctx context.Context, dest string, recs []types.ExportRecord) error {
	s.mu.Lock()
	for _, r := range recs {
		evalID := ""
		if r.Eval != nil {
			evalID = r.Eval.EvaluationID
		}
		s.ids = append(s.ids, recordPair{recordID: r.ID, evaluationID: evalID})
	}
	s.mu.Unlock()
	return s.inner.Ingest(ctx, dest, recs)
}

func (s *captureSink) Flush(ctx context.Context) (types.UnresolvedWork, error) {
	return s.inner.Flush(ctx)
}

func (s *captureSink) Shutdown(ctx context.Context) (types.UnresolvedWork, error) {
	return s.inner.Shutdown(ctx)
}

func (s *captureSink) pairs() []recordPair {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordPair(nil), s.ids...)
}

// outcomeDest is an ExportDestination that captures delivered records and
// optionally refuses every delivery attempt, simulating an outage.
type outcomeDest struct {
	name    string
	signals []types.ExportSignalKind
	fail    bool

	mu  sync.Mutex
	got []types.ExportRecord
}

func (d *outcomeDest) Name() string { return d.name }

func (d *outcomeDest) Capabilities() types.DestinationCapabilities {
	return types.DestinationCapabilities{Signals: d.signals, Replay: types.ReplayDedupByID}
}

func (d *outcomeDest) Export(ctx context.Context, recs []types.ExportRecord) ([]types.RecordOutcome, error) {
	if d.fail {
		return nil, errors.New("destination outage")
	}
	d.mu.Lock()
	d.got = append(d.got, recs...)
	d.mu.Unlock()
	out := make([]types.RecordOutcome, len(recs))
	for i, r := range recs {
		out[i] = types.RecordOutcome{ID: r.ID, Status: types.OutcomeAccepted}
	}
	return out, nil
}

func (d *outcomeDest) delivered() []types.ExportRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]types.ExportRecord(nil), d.got...)
}

type metricPoint struct {
	name  string
	attrs []types.Attr
}

// telRecorder is a types.Telemetry that captures every metric emission.
type telRecorder struct {
	mu     sync.Mutex
	points []metricPoint
}

func (t *telRecorder) StartSpan(ctx context.Context, name string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (t *telRecorder) Count(ctx context.Context, name string, n int64, attrs ...types.Attr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.points = append(t.points, metricPoint{name: name, attrs: append([]types.Attr(nil), attrs...)})
}

func (t *telRecorder) Record(ctx context.Context, name string, v float64, attrs ...types.Attr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.points = append(t.points, metricPoint{name: name, attrs: append([]types.Attr(nil), attrs...)})
}

func (t *telRecorder) named(name string) []metricPoint {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []metricPoint
	for _, p := range t.points {
		if p.name == name {
			out = append(out, p)
		}
	}
	return out
}

func attrMap(attrs []types.Attr) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		if s, ok := a.Value.(string); ok {
			m[a.Key] = s
		}
	}
	return m
}

func fmtSession(prefix string, i int) string {
	return prefix + string(rune('0'+i))
}
