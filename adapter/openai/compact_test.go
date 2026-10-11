package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// compactKeys is the credential stub of the compactor tests.
type compactKeys struct{}

func (compactKeys) ProviderKey(context.Context, string, string) (types.ProviderCredential, error) {
	return types.ProviderCredential{Token: "k"}, nil
}

// compactTransport is the stub transport of the compactor tests: it records
// the request and answers with a fixed body.
type compactTransport struct {
	status int
	body   string

	gotPath string
	gotBody []byte
}

func (t *compactTransport) Do(r *http.Request) (*http.Response, error) {
	t.gotPath = r.URL.Path
	t.gotBody, _ = io.ReadAll(r.Body)
	return &http.Response{
		StatusCode: t.status,
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Header:     http.Header{},
	}, nil
}

func opaqueTestProfile(mode types.CompactionMode) types.ModelProfile {
	return types.ModelProfile{
		Name:          "oai-opaque",
		Version:       "oai-x",
		ContextWindow: 128,
		Caps: types.Caps{
			Streaming:  true,
			Compaction: mode,
			Fidelity: map[types.BlockKind]types.Fidelity{
				types.KindText:       types.Preserved,
				types.KindToolUse:    types.Preserved,
				types.KindToolResult: types.Preserved,
			},
		},
	}
}

const compactItem = `{"type":"item","encrypted_content":"Zm9vYmFy"}`

func TestProviderCompact(t *testing.T) {
	history := stores.History{
		Version: 4,
		Messages: []types.Message{
			{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "one"}}},
			{Role: types.RoleAssistant, Blocks: []types.Block{types.Text{Text: "two"}}},
		},
	}

	t.Run("opaque-compaction", func(t *testing.T) {
		tr := &compactTransport{status: 200, body: compactItem}
		cfg := Config{Endpoint: "https://api.test/v1", Profile: opaqueTestProfile(types.CompactionOpaqueCap), Transport: tr, Keys: compactKeys{}}
		c, err := NewProviderCompact(cfg)
		if err != nil {
			t.Fatalf("constructor: %v", err)
		}
		comp, err := c.Compact(context.Background(), history, cfg.Profile)
		if err != nil {
			t.Fatalf("compact: %v", err)
		}
		if comp.Kind != types.CompactionOpaque || comp.CoversUpTo != 4 || comp.Model != "oai-opaque" {
			t.Fatalf("compaction %+v: want opaque kind, cover 4, bound to oai-opaque", comp)
		}
		if comp.Opaque.Provider != "oai-opaque" {
			t.Fatalf("opaque provider %q: want the endpoint identity", comp.Opaque.Provider)
		}
		item, ok := comp.Opaque.Value.(json.RawMessage)
		if !ok || string(item) != compactItem {
			t.Fatalf("opaque value %v: want the item bytes verbatim", comp.Opaque.Value)
		}
		if tr.gotPath != "/v1/responses/compact" {
			t.Fatalf("compact path %q: want the compact primitive", tr.gotPath)
		}
	})

	t.Run("refuses-unserved-mode", func(t *testing.T) {
		for _, mode := range []types.CompactionMode{types.CompactionNone, types.CompactionTextCap} {
			cfg := Config{Endpoint: "https://api.test/v1", Profile: opaqueTestProfile(mode), Transport: &compactTransport{status: 200}, Keys: compactKeys{}}
			c, err := NewProviderCompact(cfg)
			if err != nil {
				t.Fatalf("constructor: %v", err)
			}
			_, err = c.Compact(context.Background(), history, cfg.Profile)
			if err == nil {
				t.Fatalf("mode %d: want a refusal", mode)
			}
			if !strings.Contains(err.Error(), "oai-opaque") {
				t.Fatalf("err %v: want the refusal to name the profile", err)
			}
		}
	})

	t.Run("opaque-item-passes-through-byte-identically", func(t *testing.T) {
		p, err := New(Config{
			Profile:   opaqueTestProfile(types.CompactionOpaqueCap),
			Endpoint:  "https://api.test/v1",
			Transport: &compactTransport{status: 200},
			Keys:      compactKeys{},
		})
		if err != nil {
			t.Fatalf("provider: %v", err)
		}
		req := types.ModelRequest{Messages: []types.Message{
			{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "tail"}}},
			{Role: types.RoleAssistant, Blocks: []types.Block{types.Compaction{
				Kind: types.CompactionOpaque, CoversUpTo: 1, Model: "oai-opaque",
				Opaque: types.Raw{Provider: "oai-opaque", Value: json.RawMessage(compactItem)},
			}}},
		}}
		body, err := p.assembleBody(req)
		if err != nil {
			t.Fatalf("assemble: %v", err)
		}
		payload, err := marshalBody(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		sent, _ := io.ReadAll(payload)
		if !bytes.Contains(sent, []byte(compactItem)) {
			t.Fatalf("request %s: want the opaque item byte-identical", sent)
		}
	})

	t.Run("foreign-opaque-item-is-not-encoded", func(t *testing.T) {
		p, err := New(Config{
			Profile:   opaqueTestProfile(types.CompactionOpaqueCap),
			Endpoint:  "https://api.test/v1",
			Transport: &compactTransport{status: 200},
			Keys:      compactKeys{},
		})
		if err != nil {
			t.Fatalf("provider: %v", err)
		}
		req := types.ModelRequest{Messages: []types.Message{
			{Role: types.RoleAssistant, Blocks: []types.Block{types.Compaction{
				Kind: types.CompactionOpaque, Model: "other",
				Opaque: types.Raw{Provider: "other", Value: json.RawMessage(compactItem)},
			}}},
		}}
		body, err := p.assembleBody(req)
		if err != nil {
			t.Fatalf("assemble: %v", err)
		}
		payload, err := marshalBody(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		sent, _ := io.ReadAll(payload)
		if bytes.Contains(sent, []byte("encrypted_content")) {
			t.Fatalf("request %s: want no foreign opaque item", sent)
		}
	})
}
