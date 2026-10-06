package openai_test

import (
	"context"
	"embed"
	"encoding/json"
	"io"
	"iter"
	"net/http"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/adapter/openai"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/conformance"
)

//go:embed testdata/*.json
var recordingsFS embed.FS

type playback struct {
	Status int
	Header http.Header
	Body   string
}

var recordings = loadRecordings()

func loadRecordings() map[string]playback {
	entries, err := recordingsFS.ReadDir("testdata")
	if err != nil {
		panic(err)
	}
	out := make(map[string]playback, len(entries))
	for _, entry := range entries {
		data, err := recordingsFS.ReadFile("testdata/" + entry.Name())
		if err != nil {
			panic(err)
		}
		var rec struct {
			Name   string              `json:"name"`
			Status int                 `json:"status"`
			Header map[string][]string `json:"header"`
			Body   string              `json:"body"`
		}
		if err := json.Unmarshal(data, &rec); err != nil {
			panic(err)
		}
		out[rec.Name] = playback{Status: rec.Status, Header: http.Header(rec.Header), Body: rec.Body}
	}
	return out
}

const (
	fixtureProfile = "acme"
	fixtureVersion = "served-1"
	fixtureKeyID   = "fixture-key-1"
	fixtureToken   = "sentinel-fixture-" + "token"
)

func fixtureCaps(fx conformance.Fixture) types.Caps {
	caps := types.Caps{
		Tools:            true,
		ParallelTools:    true,
		Constrained:      fx.Kind != conformance.FixtureSchemaUnsupported,
		Streaming:        true,
		Temperature:      true,
		Cache:            types.CacheExplicit,
		CacheBreakpoints: 4,
		Fidelity: map[types.BlockKind]types.Fidelity{
			types.KindText:       types.Preserved,
			types.KindToolUse:    types.Preserved,
			types.KindToolResult: types.Preserved,
		},
	}
	if fx.Kind == conformance.FixtureCacheBreakpoints {
		caps.CacheBreakpoints = fx.MaxBreakpoints
	}
	return caps
}

// newModel builds one provider per fixture. The fixture name travels in the
// outgoing body under the conformance fixture key, so the playback transport
// picks the recorded response from what assembly actually produced.
func newModel(fx conformance.Fixture) types.Model {
	p, err := openai.New(openai.Config{
		Profile: types.ModelProfile{
			Name:    fixtureProfile,
			Version: fixtureVersion,
			Keys:    types.PlatformKey,
			Caps:    fixtureCaps(fx),
		},
		Keys:      simpleKeys(fixtureKeyID, fixtureToken),
		Transport: &captureTransport{},
	})
	if err != nil {
		panic(err)
	}
	return fixtureModel{p: p, fixture: fx.Name}
}

type fixtureModel struct {
	p       *openai.Provider
	fixture string
}

func (f fixtureModel) Profile() types.ModelProfile { return f.p.Profile() }

func (f fixtureModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		if req.Options.Extra == nil {
			req.Options.Extra = map[string]any{}
		}
		req.Options.Extra[conformance.FixtureKey] = f.fixture
		for chunk, err := range f.p.Generate(ctx, req) {
			if !yield(chunk, err) {
				return
			}
		}
	}
}

type capturedRequest struct {
	Fixture  string
	Header   http.Header
	Body     []byte
	Messages []map[string]any
}

type captureTransport struct {
	requests []capturedRequest
}

func (t *captureTransport) Do(r *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Fixture  string           `json:"gohan.conformance.fixture"`
		Messages []map[string]any `json:"messages"`
	}
	_ = json.Unmarshal(body, &parsed)
	t.requests = append(t.requests, capturedRequest{
		Fixture:  parsed.Fixture,
		Header:   r.Header.Clone(),
		Body:     body,
		Messages: parsed.Messages,
	})
	rec, ok := recordings[parsed.Fixture]
	if !ok {
		rec = recordings["usage estimated"]
	}
	return &http.Response{
		StatusCode: rec.Status,
		Header:     rec.Header,
		Body:       io.NopCloser(strings.NewReader(rec.Body)),
		Request:    r,
	}, nil
}

func TestModelConformance(t *testing.T) {
	conformance.ModelScenarios(t, newModel, conformance.DefaultFixtures())
}
