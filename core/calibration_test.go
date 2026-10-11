package gohan

import (
	"context"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"

	"encoding/json/jsontext"

	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// fixedEstimator answers one fixed estimate for every request, so the
// calibration denominator is exact.
type fixedEstimator struct{ n int }

func (e fixedEstimator) Estimate(types.ModelRequest, types.Caps) int { return e.n }

func almostEqual(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

// usageModel serves scripted turns and reports one fixed usage on every
// call, so the calibration a response earns is observable.
type usageModel struct {
	script  scriptTurns
	usage   *types.Usage
	profile types.ModelProfile
}

func (m *usageModel) Profile() types.ModelProfile { return m.profile }

func (m *usageModel) Count(_ context.Context, req types.ModelRequest) (int, error) {
	return countTextRunes(req), nil
}

func (m *usageModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return m.script.model()(ctx, req)
}

func (m *usageModel) model() types.ModelFunc { return m.Generate }

func countTextRunes(req types.ModelRequest) int {
	n := 0
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			if t, ok := b.(types.Text); ok {
				n += len([]rune(t.Text))
			}
		}
	}
	return n
}

// padTool is a side-effect call whose result text is sized by its pad, so
// a resumed turn's request crosses the calibrated ceiling.
type padTool struct{}

// padRune and padRunes size the tool result. Callers assert against the
// same literals, so a pad change cannot silently orphan an assertion.
const (
	padRune  = "r"
	padRunes = 600
)

func (padTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "confirm", Risk: types.RiskHigh, Effect: types.SideEffect}
}

func (padTool) Call(_ context.Context, _ jsontext.Value) (types.ToolResult, error) {
	return types.ToolResult{Content: []types.Block{types.Text{Text: strings.Repeat(padRune, padRunes)}}, Outcome: types.Succeeded}, nil
}

// calibrationTelemetry adds recorded samples to the counting sink.
type calibrationTelemetry struct {
	guardCountTelemetry
	mu      sync.Mutex
	records map[string][]float64
}

func (t *calibrationTelemetry) Record(_ context.Context, name string, v float64, _ ...types.Attr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.records == nil {
		t.records = map[string][]float64{}
	}
	t.records[name] = append(t.records[name], v)
}

func (t *calibrationTelemetry) record(name string) []float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.records[name]
}

func calibrationEnv(t *testing.T, ctx context.Context, cfg turnConfig, msgs []types.Message) context.Context {
	t.Helper()
	sink, _ := types.SinkFrom(types.WithSink(ctx, sinkInto(new([]types.Event))))
	return withTurnEnv(ctx, &turnEnv{c: cfg, sink: sink, msgs: msgs})
}

func TestCalibrationWiring(t *testing.T) {
	m := &usageModel{
		usage:   &types.Usage{InputTokens: 12000},
		profile: types.ModelProfile{Name: "calib-wire", ContextWindow: 20000, Caps: types.Caps{Tools: true}},
		script:  scriptTurns{turns: [][]types.ModelChunk{{{Usage: &types.Usage{InputTokens: 12000}}, turnTextChunk("ok")}}},
	}
	stack, err := Build(
		WithModels(m),
		WithStores(publicPathStores()),
		WithEstimator(fixedEstimator{n: 10000}),
		WithNativeAgent(NativeSpec{
			Request:  FlowRequest{Name: "chat"},
			Profile:  "calib-wire",
			Assemble: publicPathAssemble,
		}),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	rcfg, ok := stack.resolvedNative("chat")
	if !ok {
		t.Fatal("flow chat unresolved")
	}
	if rcfg.compactionCount != nil {
		t.Fatal("a compactor-free flow resolved a compaction count")
	}
	if rcfg.estimator == nil {
		t.Fatal("a flow with a declared estimator resolved none")
	}
	cfg := stack.nativeTurnConfig(rcfg)
	if cfg.estimator == nil {
		t.Fatal("the turn config carries no estimator")
	}

	tel := &calibrationTelemetry{}
	ctx := calibrationEnv(t, types.WithTelemetry(stageContext(context.Background()), tel), cfg, nil)
	st, _, _, err := modelEffect(ctx, runtime.State{})
	if err != nil {
		t.Fatalf("modelEffect: %v", err)
	}
	if got := st.Calibration["calib-wire"]; got != 1.2 {
		t.Fatalf("calibration %v: want 1.2, the bound estimator consulted on the sent request", got)
	}
	if recs := tel.record("gohan.context.estimate_error"); len(recs) != 1 || !almostEqual(recs[0], 0.2) {
		t.Fatalf("estimate_error samples %v: want one 0.2", recs)
	}
}

func TestCalibrationFromUsage(t *testing.T) {
	profile := types.ModelProfile{Name: "calib", ContextWindow: 20000}

	run := func(t *testing.T, usage *types.Usage, est types.TokenEstimator) (runtime.State, *calibrationTelemetry) {
		t.Helper()
		m := &usageModel{
			usage:   usage,
			profile: profile,
			script:  scriptTurns{turns: [][]types.ModelChunk{{{Usage: usage}, turnTextChunk("ok")}}},
		}
		cfg := turnConfig{
			model:     m.model(),
			assemble:  turnAssemble(nil),
			profile:   profile,
			estimator: est,
			maxTurns:  6,
		}
		tel := &calibrationTelemetry{}
		ctx := calibrationEnv(t, types.WithTelemetry(stageContext(context.Background()), tel), cfg, []types.Message{textMessage("seed")})
		st, _, _, err := modelEffect(ctx, runtime.State{})
		if err != nil {
			t.Fatalf("modelEffect: %v", err)
		}
		return st, tel
	}

	t.Run("reported-usage-calibrates", func(t *testing.T) {
		st, tel := run(t, &types.Usage{InputTokens: 12000}, fixedEstimator{n: 10000})
		if got := st.Calibration["calib"]; got != 1.2 {
			t.Fatalf("calibration %v: want 1.2 from 12000 reported over 10000 estimated", got)
		}
		if recs := tel.record("gohan.context.estimate_error"); len(recs) != 1 || !almostEqual(recs[0], 0.2) {
			t.Fatalf("estimate_error samples %v: want one 0.2", recs)
		}
	})

	t.Run("estimated-usage-calibrates-nothing", func(t *testing.T) {
		st, tel := run(t, &types.Usage{InputTokens: 12000, Estimated: true}, fixedEstimator{n: 10000})
		if len(st.Calibration) != 0 {
			t.Fatalf("calibration %v: want none, the usage is adapter-synthesized", st.Calibration)
		}
		if recs := tel.record("gohan.context.estimate_error"); len(recs) != 0 {
			t.Fatalf("estimate_error samples %v: want none", recs)
		}
	})

	t.Run("no-estimator-calibrates-nothing", func(t *testing.T) {
		st, tel := run(t, &types.Usage{InputTokens: 12000}, nil)
		if len(st.Calibration) != 0 {
			t.Fatalf("calibration %v: want none without a declared estimator", st.Calibration)
		}
		if recs := tel.record("gohan.context.estimate_error"); len(recs) != 0 {
			t.Fatalf("estimate_error samples %v: want none", recs)
		}
	})
}

func TestCompactionRatioApplied(t *testing.T) {
	ctx := stageContext(context.Background())
	msgs := stageTexts(90)
	count := func(_ context.Context, req types.ModelRequest) (int, error) {
		return blockCount(req), nil
	}
	build := func(t *testing.T) (turnConfig, *stores.MemorySessionLog, *stores.MemoryAuditLog) {
		t.Helper()
		cfg, sessions, audit, _ := stageConfig(t, ctx, msgs, stageCompactor{}, &stageGuard{action: guards.Pass}, shrinkProjection{drop: 4}, count)
		cfg.profile.ContextWindow = 100
		return cfg, sessions, audit
	}

	t.Run("ceiling-scales-by-the-reciprocal", func(t *testing.T) {
		profile := types.ModelProfile{Name: "stage-test", ContextWindow: 100}
		plain := types.ContextBudget(profile, types.ModelRequest{}, nil, 0)
		scaled := types.ContextBudget(profile, types.ModelRequest{}, nil, budgetRatio("stage-test", map[string]float64{"stage-test": 1.2}))
		if plain.Limit != 100 {
			t.Fatalf("uncalibrated limit %d: want the full window 100", plain.Limit)
		}
		ratio := 1.2
		want := int(float64(100) / ratio)
		if scaled.Limit != want {
			t.Fatalf("calibrated limit %d: want %d, the window tightened by 1.2", scaled.Limit, want)
		}
		if budgetRatio("stage-test", nil) != 0 {
			t.Fatal("no calibration must keep the zero ratio")
		}
	})

	t.Run("decision-flips-once-calibrated", func(t *testing.T) {
		cfg, sessions, audit := build(t)
		env := &turnEnv{c: cfg, msgs: msgs}
		req := types.ModelRequest{Messages: env.msgs}
		var st runtime.State
		req2, _, err := runCompactionStage(ctx, &st, env, req)
		if err != nil {
			t.Fatalf("uncalibrated stage: %v", err)
		}
		if len(req2.Messages) != 90 {
			t.Fatalf("uncalibrated stage returned %d messages: want the request unchanged, 90 fits the window", len(req2.Messages))
		}
		if h, err := sessions.Load(ctx, "s1"); err == nil && h.Version != 0 {
			t.Fatalf("uncalibrated stage appended to version %d: want no compaction", h.Version)
		}
		if records := stageAuditRecords(t, audit); len(records) != 0 {
			t.Fatalf("uncalibrated stage audited %d records: want none", len(records))
		}

		st = runtime.State{Calibration: map[string]float64{"stage-test": 1.2}}
		if _, _, err := runCompactionStage(ctx, &st, env, req); err != nil {
			t.Fatalf("calibrated stage: %v", err)
		}
		h, err := sessions.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if h.Version != 91 {
			t.Fatalf("calibrated stage history version %d: want 91, one compaction append over the 90-message request that only the tightened ceiling refuses", h.Version)
		}
	})
}

func TestCalibrationCheckpointRoundTrip(t *testing.T) {
	cp, env := envelopeCheckpoint()
	env.State.Calibration = map[string]float64{"p": 1.2}
	data, err := encodeCheckpoint(env)
	if err != nil {
		t.Fatal(err)
	}
	cp.Data = data
	_, state, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a")
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Calibration["p"]; got != 1.2 {
		t.Fatalf("restored calibration %v: want 1.2", got)
	}
	clone := copyResumeState(state)
	clone.Calibration["p"] = 9
	if state.Calibration["p"] != 1.2 {
		t.Fatalf("resume copy aliases the checkpoint calibration: %v", state.Calibration)
	}
}

func overflowStageConfig(t *testing.T, ctx context.Context, model types.ModelFunc) (turnConfig, *stores.MemorySessionLog, *stores.MemoryAuditLog) {
	t.Helper()
	msgs := stageTexts(90)
	count := func(_ context.Context, req types.ModelRequest) (int, error) {
		return blockCount(req), nil
	}
	cfg, sessions, audit, _ := stageConfig(t, ctx, msgs, stageCompactor{}, &stageGuard{action: guards.Pass}, shrinkProjection{drop: 4}, count)
	cfg.profile.ContextWindow = 100
	cfg.model = model
	return cfg, sessions, audit
}

func TestOverflowRetryOnce(t *testing.T) {
	ctx := stageContext(context.Background())
	m := &scriptedOverflowModel{
		turns: [][]types.ModelChunk{{{Kind: types.DeltaText, Delta: "ignored"}}, {{Kind: types.DeltaText, Delta: "ok"}}},
		errs: map[int]error{
			0: &types.ModelError{Class: types.ClassContextOverflow, Provider: "overflow-test", Err: errors.New("prompt too long")},
		},
	}
	cfg, sessions, audit := overflowStageConfig(t, ctx, m.model())
	tel := &calibrationTelemetry{}
	var driveErr error
	driveTurns(types.WithTelemetry(types.WithSink(ctx, sinkInto(new([]types.Event))), tel), cfg, func(e types.Event, err error) bool {
		if err != nil {
			driveErr = err
			return false
		}
		return true
	})
	if driveErr != nil {
		t.Fatalf("drive: %v", driveErr)
	}
	if m.n != 2 {
		t.Fatalf("model calls %d: want 2, the overflowed attempt and the single retry", m.n)
	}
	if n := tel.count("gohan.context.overflow_retry"); n != 1 {
		t.Fatalf("gohan.context.overflow_retry %d: want 1", n)
	}
	if !tel.attr("gohan.context.overflow_retry", "provider-profile", "stage-test") && !tel.attr("gohan.context.overflow_retry", types.KeyModelProfile, "stage-test") {
		t.Fatal("the retry counter carried no profile attribute")
	}
	if records := stageAuditRecords(t, audit); len(records) != 1 {
		t.Fatalf("audit records %d: want 1, the compaction the raised ratio fired", len(records))
	}
	h, err := sessions.Load(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if h.Version != 91 {
		t.Fatalf("history version %d: want 91, one compaction append over the 90-message request", h.Version)
	}
}

func TestOverflowSecondFailsPermanent(t *testing.T) {
	ctx := stageContext(context.Background())
	overflow := &types.ModelError{Class: types.ClassContextOverflow, Provider: "overflow-test", Err: errors.New("prompt too long")}
	m := &scriptedOverflowModel{errs: map[int]error{0: overflow, 1: overflow}}
	cfg, _, _ := overflowStageConfig(t, ctx, m.model())
	tel := &calibrationTelemetry{}
	var driveErr error
	driveTurns(types.WithTelemetry(types.WithSink(ctx, sinkInto(new([]types.Event))), tel), cfg, func(_ types.Event, err error) bool {
		if err != nil {
			driveErr = err
			return false
		}
		return true
	})
	if driveErr == nil {
		t.Fatal("a second overflow must fail the drive")
	}
	var me *types.ModelError
	if !errors.As(driveErr, &me) || me.Class != types.ClassPermanent {
		t.Fatalf("err %v: want a ClassPermanent ModelError after exactly two attempts", driveErr)
	}
	if m.n != 2 {
		t.Fatalf("model calls %d: want exactly 2, no third attempt", m.n)
	}
	if n := tel.count("gohan.context.overflow_retry"); n != 1 {
		t.Fatalf("gohan.context.overflow_retry %d: want 1", n)
	}
}

// scriptedOverflowModel fails the scripted call indexes with their error
// and serves the remaining calls from the turn script.
type scriptedOverflowModel struct {
	turns [][]types.ModelChunk
	errs  map[int]error
	reqs  []types.ModelRequest
	n     int
}

func (m *scriptedOverflowModel) model() types.ModelFunc {
	return func(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			m.reqs = append(m.reqs, req)
			if e, ok := m.errs[m.n]; ok {
				m.n++
				yield(types.ModelChunk{}, e)
				return
			}
			if m.n >= len(m.turns) {
				yield(types.ModelChunk{}, errors.New("script exhausted"))
				return
			}
			for _, c := range m.turns[m.n] {
				if !yield(c, nil) {
					return
				}
			}
			m.n++
		}
	}
}
