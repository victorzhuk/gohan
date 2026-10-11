package anthropic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// compactRT is the stub transport of the compactor tests: it records the
// request and answers with a fixed body.
type compactRT struct {
	status int
	body   string

	gotPath string
	gotBody []byte
}

func (rt *compactRT) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.gotPath = r.URL.Path
	rt.gotBody, _ = io.ReadAll(r.Body)
	return &http.Response{
		StatusCode: rt.status,
		Body:       io.NopCloser(strings.NewReader(rt.body)),
		Header:     http.Header{},
	}, nil
}

func TestProviderCompact(t *testing.T) {
	profile := func(mode types.CompactionMode) types.ModelProfile {
		return types.ModelProfile{
			Name:    "claude-text",
			Version: "claude-x",
			Caps:    types.Caps{Compaction: mode},
		}
	}
	history := stores.History{
		Version: 3,
		Messages: []types.Message{
			{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "one"}}},
			{Role: types.RoleAssistant, Blocks: []types.Block{types.Text{Text: "two"}}},
		},
	}

	t.Run("text-compaction", func(t *testing.T) {
		rt := &compactRT{status: 200, body: `{"summary":"folded"}`}
		cfg := Config{Endpoint: "https://api.test/v1/messages", APIKey: "k", Profile: profile(types.CompactionTextCap), Client: &http.Client{Transport: rt}}
		c, err := NewProviderCompact(cfg)
		if err != nil {
			t.Fatalf("constructor: %v", err)
		}
		comp, err := c.Compact(context.Background(), history, cfg.Profile)
		if err != nil {
			t.Fatalf("compact: %v", err)
		}
		if comp.Kind != types.CompactionText || comp.CoversUpTo != 3 || comp.Model != "claude-text" {
			t.Fatalf("compaction %+v: want text kind, cover 3, bound to claude-text", comp)
		}
		if len(comp.Summary) != 1 {
			t.Fatalf("summary blocks %d: want 1", len(comp.Summary))
		}
		text, ok := comp.Summary[0].(types.Text)
		if !ok || text.Text != "folded" {
			t.Fatalf("summary block %+v: want the folded text", comp.Summary[0])
		}
		if text.Origin.Kind != types.OriginModel {
			t.Fatalf("summary origin %+v: want OriginModel", text.Origin)
		}
		if rt.gotPath != "/v1/messages/compact" {
			t.Fatalf("compact path %q: want the compact primitive", rt.gotPath)
		}
		if !strings.Contains(string(rt.gotBody), `"messages"`) {
			t.Fatalf("compact request %s: want the covered transcript", rt.gotBody)
		}
	})

	t.Run("refuses-unserved-mode", func(t *testing.T) {
		for _, mode := range []types.CompactionMode{types.CompactionNone, types.CompactionOpaqueCap} {
			cfg := Config{Endpoint: "https://api.test/v1/messages", Profile: profile(mode), Client: &http.Client{Transport: &compactRT{status: 200}}}
			c, err := NewProviderCompact(cfg)
			if err != nil {
				t.Fatalf("constructor: %v", err)
			}
			_, err = c.Compact(context.Background(), history, cfg.Profile)
			if err == nil {
				t.Fatalf("mode %d: want a refusal", mode)
			}
			if !strings.Contains(err.Error(), "claude-text") {
				t.Fatalf("err %v: want the refusal to name the profile", err)
			}
		}
	})

	t.Run("primitive-error-is-an-error", func(t *testing.T) {
		cfg := Config{Endpoint: "https://api.test/v1/messages", Profile: profile(types.CompactionTextCap), Client: &http.Client{Transport: &compactRT{status: 500}}}
		c, err := NewProviderCompact(cfg)
		if err != nil {
			t.Fatalf("constructor: %v", err)
		}
		if _, err = c.Compact(context.Background(), history, cfg.Profile); err == nil {
			t.Fatal("a failing primitive must fail the compact")
		}
		var merr *types.ModelError
		if !errors.As(err, &merr) {
			t.Fatalf("err %v: want a *types.ModelError", err)
		}
	})
}
