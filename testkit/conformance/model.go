package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// conformanceTB is the slice of testing.T the suites need, so a capture
// double can run a suite against a non-conforming implementation and
// observe the failure without failing the outer test.
type conformanceTB interface {
	Helper()
	Fatalf(format string, args ...any)
	Errorf(format string, args ...any)
	Failed() bool
	Run(name string, fn func(tb conformanceTB)) bool
}

type testingTB struct{ t *testing.T }

func (b testingTB) Helper()                   { b.t.Helper() }
func (b testingTB) Fatalf(f string, a ...any) { b.t.Fatalf(f, a...) }
func (b testingTB) Errorf(f string, a ...any) { b.t.Errorf(f, a...) }
func (b testingTB) Failed() bool              { return b.t.Failed() }
func (b testingTB) Run(n string, fn func(conformanceTB)) bool {
	return b.t.Run(n, func(t *testing.T) { fn(testingTB{t}) })
}

// leakTB adapts a conformanceTB to gohantest.LeakCheck's reporter.
type leakTB struct{ tb conformanceTB }

func (l leakTB) Helper()                   { l.tb.Helper() }
func (l leakTB) Errorf(f string, a ...any) { l.tb.Errorf(f, a...) }
func (l leakTB) Failed() bool              { return l.tb.Failed() }

// Model runs the legacy fixture-named Model suite.
func Model(t *testing.T, newModel func(Fixture) types.Model, fixtures Fixtures) {
	t.Helper()
	runModel(testingTB{t}, newModel, fixtures)
}

// ModelScenarios runs shared adapter behavior under registered scenario IDs.
func ModelScenarios(t *testing.T, newModel func(Fixture) types.Model, fixtures Fixtures) {
	t.Helper()
	tb := testingTB{t}
	scenarios := []struct {
		id    string
		kinds []FixtureKind
	}{
		{"model.status-to-class", []FixtureKind{FixtureError}},
		{"model.retry-after-parsed", []FixtureKind{FixtureError}},
		{"model.cache-explicit-breakpoints", []FixtureKind{FixtureCacheBreakpoints}},
		{"model.schema-never-ignored", []FixtureKind{FixtureSchemaUnsupported}},
		{"model.usage-estimated", []FixtureKind{FixtureUsage}},
		{"model.raw-round-trip", []FixtureKind{FixtureRawRoundTrip}},
		{"model.isolation-per-key", []FixtureKind{FixtureKeyIsolation}},
		{"model.key-never-in-telemetry", []FixtureKind{FixtureKeyRedaction}},
	}
	for _, scenario := range scenarios {
		scenario := scenario
		tb.Run(scenario.id, func(tb conformanceTB) {
			for _, fx := range fixtures {
				for _, kind := range scenario.kinds {
					if fx.Kind == kind {
						runFixture(tb, newModel(fx), fx)
					}
				}
			}
		})
	}
}

func runModel(tb conformanceTB, newModel func(Fixture) types.Model, fixtures Fixtures) {
	tb.Helper()
	for _, fx := range fixtures {
		fx := fx
		tb.Run(fx.Name, func(tb conformanceTB) {
			runFixture(tb, newModel(fx), fx)
		})
	}
	tb.Run("early break releases the iterator", func(tb conformanceTB) {
		gohantest.LeakCheck(leakTB{tb}, func() {
			m := newModel(probe())
			n := 0
			for range m.Generate(context.Background(), probeRequest()) {
				n++
				break
			}
			if n != 1 {
				tb.Errorf("early break consumed %d chunks, want 1", n)
			}
		})
	})
	tb.Run("cancel mid stream returns promptly", func(tb conformanceTB) {
		gohantest.LeakCheck(leakTB{tb}, func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := newModel(probe())
			done := make(chan struct{})
			go func() {
				defer close(done)
				first := true
				for _, err := range m.Generate(ctx, probeRequest()) {
					if first {
						cancel()
						first = false
					}
					if err != nil {
						return
					}
				}
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				tb.Fatalf("iterator did not return within 5s of cancellation")
			}
		})
	})
	tb.Run("cancelled context fails fast", func(tb conformanceTB) {
		gohantest.LeakCheck(leakTB{tb}, func() {
			ctx, cancel := gohantest.CancelledContext()
			defer cancel()
			m := newModel(probe())
			for chunk, err := range m.Generate(ctx, probeRequest()) {
				if err == nil {
					tb.Errorf("cancelled context streamed a %q chunk", chunk.Delta)
				}
			}
		})
	})
}

func runFixture(tb conformanceTB, m types.Model, fx Fixture) {
	tb.Helper()
	switch fx.Kind {
	case FixtureError:
		assertErrorFixture(tb, m, fx)
	case FixtureUsage:
		assertUsageFixture(tb, m, fx)
	case FixtureTruncation:
		assertTruncationFixture(tb, m, fx)
	case FixtureRawRoundTrip:
		assertRawFixture(tb, m, fx)
	case FixtureCacheBreakpoints:
		assertCacheFixture(tb, m, fx)
	case FixtureSchemaUnsupported:
		assertSchemaFixture(tb, m, fx)
	default:
		return
	}
}

// assertCacheFixture sends a request carrying the fixture's explicit cache
// break markers and requires success with cache-write usage on the final
// chunk: an adapter maps explicit breakpoints instead of dropping them.
// The exact outgoing marker payload is inside the adapter harness.
func assertCacheFixture(tb conformanceTB, m types.Model, fx Fixture) {
	tb.Helper()
	req := probeRequest()
	for range fx.CacheBreaks {
		req.Messages = append(req.Messages, types.Message{
			Role:   types.RoleUser,
			Blocks: []types.Block{types.CacheBreak{}},
		})
	}
	chunks, err := collect(m, req)
	if err != nil {
		tb.Errorf("stream with cache breaks failed: %v", err)
		return
	}
	if len(chunks) == 0 {
		tb.Errorf("stream with cache breaks yielded no chunks")
		return
	}
	u := chunks[len(chunks)-1].Usage
	if u == nil {
		tb.Errorf("no usage on final chunk")
		return
	}
	if u.CacheWriteTokens <= 0 {
		tb.Errorf("CacheWriteTokens %d, want cache writes for explicit breakpoints", u.CacheWriteTokens)
	}
}

// assertSchemaFixture sends a request carrying a response schema and requires
// a permanent ModelError with no chunk before it: an adapter without a
// constrained-output capability must refuse rather than stream unconstrained.
func assertSchemaFixture(tb conformanceTB, m types.Model, fx Fixture) {
	tb.Helper()
	req := probeRequest()
	req.Options.ResponseSchema = json.RawMessage(`{"type":"object"}`)
	var modelErr *types.ModelError
	for chunk, err := range m.Generate(context.Background(), req) {
		if err == nil {
			tb.Errorf("streamed unconstrained %q chunk under a response schema", chunk.Delta)
			continue
		}
		if !errors.As(err, &modelErr) {
			tb.Errorf("error is %T, want *ModelError", err)
			continue
		}
		if modelErr.Class != types.ClassPermanent {
			tb.Errorf("class %d, want permanent for an unsupported schema", modelErr.Class)
		}
	}
}

// assertErrorFixture requires the stream to fail with exactly one *ModelError
// of the fixture's class, carrying RetryAfter when the fixture recorded one,
// and never to yield a chunk after the error.
func assertErrorFixture(tb conformanceTB, m types.Model, fx Fixture) {
	tb.Helper()
	err := drain(m, probeRequest())
	if err == nil {
		tb.Errorf("%s (status %d, code %q) was swallowed, want *ModelError class %d", fx.Name, fx.Status, fx.Code, fx.Class)
		return
	}
	me, ok := errors.AsType[*types.ModelError](err)
	if !ok {
		tb.Errorf("error is %T, want *ModelError", err)
		return
	}
	if me.Class != fx.Class {
		tb.Errorf("class %d, want %d", me.Class, fx.Class)
	}
	if fx.RetryAfter > 0 && me.RetryAfter != fx.RetryAfter {
		tb.Errorf("RetryAfter %s, want %s", me.RetryAfter, fx.RetryAfter)
	}
}

// assertUsageFixture requires usage on the final chunk only; with a recorded
// usage the fields must match exactly, without one the estimate rule holds:
// Estimated true and non-zero output tokens.
func assertUsageFixture(tb conformanceTB, m types.Model, fx Fixture) {
	tb.Helper()
	chunks, err := collect(m, probeRequest())
	if err != nil {
		tb.Errorf("stream failed: %v", err)
		return
	}
	if len(chunks) == 0 {
		tb.Errorf("no chunks streamed")
		return
	}
	if fx.Usage == nil {
		u := chunks[len(chunks)-1].Usage
		if u == nil {
			tb.Errorf("no usage on final chunk")
			return
		}
		if !u.Estimated {
			tb.Errorf("Estimated false for a response without usage")
		}
		if u.OutputTokens <= 0 {
			tb.Errorf("OutputTokens %d, want a non-zero estimate", u.OutputTokens)
		}
		return
	}
	last := len(chunks) - 1
	for i, c := range chunks {
		if i < last && c.Usage != nil {
			tb.Errorf("chunk %d carries usage before the final chunk", i)
		}
	}
	u := chunks[last].Usage
	if u == nil {
		tb.Errorf("no usage on final chunk")
		return
	}
	want := *fx.Usage
	if u.InputTokens != want.InputTokens {
		tb.Errorf("InputTokens %d, want %d", u.InputTokens, want.InputTokens)
	}
	if u.CachedInputTokens != want.CachedInputTokens {
		tb.Errorf("CachedInputTokens %d, want %d", u.CachedInputTokens, want.CachedInputTokens)
	}
	if u.OutputTokens != want.OutputTokens {
		tb.Errorf("OutputTokens %d, want %d", u.OutputTokens, want.OutputTokens)
	}
	if u.CacheWriteTokens != want.CacheWriteTokens {
		tb.Errorf("CacheWriteTokens %d, want %d", u.CacheWriteTokens, want.CacheWriteTokens)
	}
	if u.ModelVersion != want.ModelVersion {
		tb.Errorf("ModelVersion %q, want %q", u.ModelVersion, want.ModelVersion)
	}
}

// assertTruncationFixture requires a complete ToolUse block followed by a
// max_tokens finish: truncation ends the stream, it never drops the tool
// call or its arguments.
func assertTruncationFixture(tb conformanceTB, m types.Model, fx Fixture) {
	tb.Helper()
	chunks, err := collect(m, probeRequest())
	if err != nil {
		tb.Errorf("stream failed: %v", err)
		return
	}
	var tool *types.ToolUse
	finish := types.FinishStop
	for _, c := range chunks {
		if c.ToolUse != nil {
			tool = c.ToolUse
		}
		if c.Finish != "" {
			finish = c.Finish
		}
	}
	if tool == nil {
		tb.Errorf("no ToolUse streamed before truncation")
		return
	}
	if fx.ToolName != "" && tool.Name != fx.ToolName {
		tb.Errorf("tool name %q, want %q", tool.Name, fx.ToolName)
	}
	if len(tool.Args) > 0 {
		var probe any
		if err := json.Unmarshal([]byte(tool.Args), &probe); err != nil {
			tb.Errorf("tool args are not valid JSON: %v", err)
		}
	}
	if finish != types.FinishMaxTokens {
		tb.Errorf("finish %q, want max_tokens", finish)
	}
}

// assertRawFixture sends a request carrying the fixture's Raw block and
// requires the call to succeed: an adapter must accept an unmodelled block
// instead of failing the request. The unchanged re-send to the same provider
// is inside the adapter, so the port-level check is acceptance plus a
// complete stream.
func assertRawFixture(tb conformanceTB, m types.Model, fx Fixture) {
	tb.Helper()
	if _, err := collect(m, probeRequest()); err != nil {
		tb.Errorf("plain call failed: %v", err)
	}
	req := probeRequest()
	req.Messages = append(req.Messages, types.Message{
		Role:   types.RoleAssistant,
		Blocks: []types.Block{fx.Raw},
	})
	chunks, err := collect(m, req)
	if err != nil {
		tb.Errorf("call with a Raw block failed: %v", err)
		return
	}
	if len(chunks) == 0 {
		tb.Errorf("call with a Raw block streamed no chunks")
	}
}

func drain(m types.Model, req types.ModelRequest) error {
	for _, err := range m.Generate(context.Background(), req) {
		if err != nil {
			return err
		}
	}
	return nil
}

func collect(m types.Model, req types.ModelRequest) ([]types.ModelChunk, error) {
	var chunks []types.ModelChunk
	for chunk, err := range m.Generate(context.Background(), req) {
		if err != nil {
			return chunks, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

// probe is a synthetic usage fixture the behaviour subtests stream against.
func probe() Fixture {
	return Fixture{Name: "probe", Kind: FixtureUsage, Text: "hello"}
}

func probeRequest() types.ModelRequest {
	return types.ModelRequest{
		Messages: []types.Message{
			{ID: "probe", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "conformance probe"}}},
		},
	}
}
