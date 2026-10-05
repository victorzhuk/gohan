package conformance

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

var confProfile = types.ModelProfile{
	Name:    "conformance",
	Version: "v1",
}

// TestModelConformance runs the suite against ScriptedModel over the
// fixtures ScriptedModel can express (errors and usage) and against a
// fixture double over the full set, including truncation and Raw round trip.
func TestModelConformance(t *testing.T) {
	t.Run("scripted model", func(t *testing.T) {
		Model(t, scriptedModelFor, scriptedFixtures(DefaultFixtures()))
	})
	t.Run("fixture double", func(t *testing.T) {
		Model(t, fixtureModelFor, DefaultFixtures())
	})
}

// TestModelConformanceRejectsSwallowedErrors proves the suite fails a
// deliberately non-conforming model: one that turns a provider failure into
// a clean stop must not pass the error fixtures.
func TestModelConformanceRejectsSwallowedErrors(t *testing.T) {
	capture := &captureTB{}
	runModel(capture, swallowModelFor, scriptedFixtures(DefaultFixtures()))
	if !capture.failed {
		t.Fatal("suite passed a model that swallows ModelError")
	}
}

// TestDefaultFixtures pins the fixture set: every contract area the spec
// names, unique names, and the scenario table's class mapping.
func TestDefaultFixtures(t *testing.T) {
	fs := DefaultFixtures()
	if len(fs) == 0 {
		t.Fatal("DefaultFixtures is empty")
	}
	seen := make(map[string]bool, len(fs))
	classes := make(map[types.ErrorClass]bool, len(fs))
	for _, fx := range fs {
		if seen[fx.Name] {
			t.Errorf("fixture %q appears twice", fx.Name)
		}
		seen[fx.Name] = true
		if fx.Name == "" {
			t.Error("fixture with empty name")
		}
		switch fx.Kind {
		case FixtureError:
			if fx.Class == 0 {
				t.Errorf("fixture %q has no class", fx.Name)
			}
			if fx.Status == 0 {
				t.Errorf("fixture %q has no status", fx.Name)
			}
			classes[fx.Class] = true
		case FixtureUsage:
			if fx.Text == "" {
				t.Errorf("usage fixture %q streams no text", fx.Name)
			}
		case FixtureTruncation:
			if fx.ToolName == "" {
				t.Errorf("truncation fixture %q names no tool", fx.Name)
			}
		case FixtureRawRoundTrip:
			if fx.Raw.Provider == "" || fx.Raw.Value == nil {
				t.Errorf("raw fixture %q has no provider or value", fx.Name)
			}
		default:
			t.Errorf("fixture %q has unknown kind %d", fx.Name, fx.Kind)
		}
	}
	for _, class := range []types.ErrorClass{
		types.ClassRateLimited, types.ClassTransient, types.ClassAuth,
		types.ClassPermanent, types.ClassContextOverflow, types.ClassDeprecated,
	} {
		if !classes[class] {
			t.Errorf("fixture set misses class %d", class)
		}
	}
}

// TestFixturesAreData pins the fixture rule: a fixture set is a plain value,
// so running it twice drives the same assertions with no shared state.
func TestFixturesAreData(t *testing.T) {
	fs := DefaultFixtures()
	kinds := fs.Kinds()
	for _, kind := range []FixtureKind{FixtureError, FixtureUsage, FixtureTruncation, FixtureRawRoundTrip} {
		if !kinds[kind] {
			t.Errorf("fixture set misses kind %d", kind)
		}
	}
}

// scriptedFixtures keeps the fixtures a scripted replay can express:
// ScriptedModel has no Raw chunk kind and always completes its turns.
func scriptedFixtures(fs Fixtures) Fixtures {
	var out Fixtures
	for _, fx := range fs {
		if fx.Kind == FixtureError || fx.Kind == FixtureUsage {
			out = append(out, fx)
		}
	}
	return out
}

// scriptedModelFor maps a fixture onto the ScriptedModel turns that express
// its model-level outcome. ScriptedModel itself neither honours context
// cancellation nor carries RetryAfter, so the wrapper supplies both at the
// port boundary.
func scriptedModelFor(fx Fixture) types.Model {
	var turns []gohantest.Turn
	switch fx.Kind {
	case FixtureError:
		turns = []gohantest.Turn{gohantest.Fail(fx.Class)}
	case FixtureUsage:
		turn := gohantest.Text(fx.Text)
		if fx.Usage != nil {
			turn = turn.WithUsage(*fx.Usage)
		}
		turns = []gohantest.Turn{turn}
	}
	return scriptedFixture{inner: gohantest.NewScriptedModel(confProfile, turns...), fx: fx}
}

type scriptedFixture struct {
	inner types.Model
	fx    Fixture
}

func (m scriptedFixture) Profile() types.ModelProfile { return m.inner.Profile() }

func (m scriptedFixture) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(types.ModelChunk{}, err)
			return
		}
		for chunk, err := range m.inner.Generate(ctx, req) {
			if me, ok := errors.AsType[*types.ModelError](err); ok && m.fx.RetryAfter > 0 {
				me.RetryAfter = m.fx.RetryAfter
			}
			if !yield(chunk, err) {
				return
			}
		}
	}
}

// fixtureModel replays a fixture directly, covering the outcomes
// ScriptedModel cannot express: a max_tokens finish on an open tool call
// and a Raw block echoing out of the request.
type fixtureModel struct{ fx Fixture }

func fixtureModelFor(fx Fixture) types.Model { return fixtureModel{fx: fx} }

func (m fixtureModel) Profile() types.ModelProfile { return confProfile }

func (m fixtureModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(types.ModelChunk{}, err)
			return
		}
		var hasRaw bool
		for _, msg := range req.Messages {
			for _, b := range msg.Blocks {
				if _, ok := b.(types.Raw); ok {
					hasRaw = true
				}
			}
		}
		switch {
		case hasRaw:
			if !yield(types.ModelChunk{Kind: types.DeltaText, Delta: "raw carried"}, nil) {
				return
			}
			yield(types.ModelChunk{Finish: types.FinishStop, Usage: &types.Usage{
				InputTokens: 10, OutputTokens: 2, ModelVersion: confProfile.Version, Estimated: true,
			}}, nil)
		case m.fx.Kind == FixtureError:
			yield(types.ModelChunk{}, &types.ModelError{
				Class:      m.fx.Class,
				Provider:   confProfile.Name,
				Status:     m.fx.Status,
				Code:       m.fx.Code,
				RetryAfter: m.fx.RetryAfter,
			})
		case m.fx.Kind == FixtureTruncation:
			if !yield(types.ModelChunk{ToolUse: &types.ToolUse{
				ID: "tu_1", Name: m.fx.ToolName, Args: []byte(`{"q":"x"}`),
			}}, nil) {
				return
			}
			yield(types.ModelChunk{Finish: types.FinishMaxTokens, Usage: &types.Usage{
				InputTokens: 10, OutputTokens: 4, ModelVersion: confProfile.Version, Estimated: true,
			}}, nil)
		default:
			if !yield(types.ModelChunk{Kind: types.DeltaText, Delta: m.fx.Text}, nil) {
				return
			}
			u := &types.Usage{
				InputTokens:  10,
				OutputTokens: 2,
				ModelVersion: confProfile.Version,
				Estimated:    true,
			}
			if m.fx.Usage != nil {
				u = m.fx.Usage
			}
			yield(types.ModelChunk{Finish: types.FinishStop, Usage: u}, nil)
		}
	}
}

// swallowModel wraps a fixture model and drops every error class, the
// non-conformance the negative test must catch.
type swallowModel struct{ inner types.Model }

func swallowModelFor(fx Fixture) types.Model { return swallowModel{inner: fixtureModelFor(fx)} }

func (m swallowModel) Profile() types.ModelProfile { return m.inner.Profile() }

func (m swallowModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		for chunk, err := range m.inner.Generate(ctx, req) {
			if err != nil {
				yield(types.ModelChunk{Finish: types.FinishStop}, nil)
				return
			}
			if !yield(chunk, nil) {
				return
			}
		}
	}
}

// captureTB records suite failures instead of failing the test.
type captureTB struct {
	failed bool
}

func (c *captureTB) Helper() {}
func (c *captureTB) Fatalf(f string, a ...any) {
	c.failed = true
}
func (c *captureTB) Errorf(f string, a ...any) {
	c.failed = true
}
func (c *captureTB) Failed() bool { return c.failed }
func (c *captureTB) Run(name string, fn func(tb conformanceTB)) bool {
	fn(c)
	return !c.failed
}
