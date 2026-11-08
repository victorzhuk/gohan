package anthropic

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/conformance"
)

func fixtureModel(fx conformance.Fixture) types.Model {
	caps := types.Caps{
		Tools:            true,
		ParallelTools:    true,
		Constrained:      fx.Kind != conformance.FixtureSchemaUnsupported,
		Streaming:        true,
		Temperature:      true,
		Cache:            types.CacheExplicit,
		CacheBreakpoints: 4,
		Fidelity:         map[types.BlockKind]types.Fidelity{},
	}
	if fx.Kind == conformance.FixtureCacheBreakpoints {
		caps.CacheBreakpoints = fx.MaxBreakpoints
	}
	m, err := New(Config{
		APIKey:  "fixture" + "-key",
		KeyID:   "fixture-key-1",
		Profile: types.ModelProfile{Name: "claude-fixture", Version: "served-1", Caps: caps},
		Client:  playbackClient(fx.Name),
	})
	if err != nil {
		panic(err)
	}
	return m
}

func playbackClient(name string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		pb, ok := Fixtures()[name]
		if !ok {
			pb = Fixtures()["probe"]
		}
		return &http.Response{
			StatusCode: pb.Status,
			Header:     pb.Header,
			Body:       io.NopCloser(bytes.NewReader(pb.Body)),
			Request:    r,
		}, nil
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestModelConformance(t *testing.T) {
	conformance.ModelScenarios(t, fixtureModel, conformance.DefaultFixtures())
	t.Run("legacy conformance suite", func(t *testing.T) {
		conformance.Model(t, fixtureModel, conformance.DefaultFixtures())
	})
}
