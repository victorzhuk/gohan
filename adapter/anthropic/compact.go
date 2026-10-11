package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// ProviderCompact folds history through the Anthropic compact primitive.
// It implements the stores.Compactor contract adapter-side over the same
// endpoint and credential seam the Model uses; core never imports this
// package, so the caller binds it through ContextPolicy.Compactor.
type ProviderCompact struct {
	cfg Config
	hc  *http.Client
}

// NewProviderCompact validates the profile the compactor folds for.
func NewProviderCompact(cfg Config) (*ProviderCompact, error) {
	if cfg.Profile.Name == "" {
		return nil, errors.New("anthropic: profile name is required")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultEndpoint
	}
	if cfg.Client == nil {
		cfg.Client = http.DefaultClient
	}
	return &ProviderCompact{cfg: cfg, hc: cfg.Client}, nil
}

// ServedModes declares the Caps.Compaction modes the compactor serves: the
// compact primitive returns a readable summary, so it serves text mode.
func (c *ProviderCompact) ServedModes() []types.CompactionMode {
	return []types.CompactionMode{types.CompactionTextCap}
}

// Compact folds the covered prefix the caller selected into one
// types.Compaction. It folds only the history it is handed and never reads
// beyond it. The block records the endpoint identity in Model and covers
// exactly History.Version.
func (c *ProviderCompact) Compact(ctx context.Context, h stores.History, p types.ModelProfile) (types.Compaction, error) {
	if !servesMode(c.ServedModes(), p.Caps.Compaction) {
		return types.Compaction{}, fmt.Errorf("anthropic: profile %q: compaction mode %d: the compact primitive serves text compaction only", p.Name, p.Caps.Compaction)
	}
	summary, err := c.compact(ctx, h, p)
	if err != nil {
		return types.Compaction{}, err
	}
	return types.Compaction{
		Kind:       types.CompactionText,
		CoversUpTo: h.Version,
		Summary: []types.Block{types.Text{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}},
			Text:      summary,
		}},
		Model: p.Name,
	}, nil
}

// compactEndpoint derives the compact primitive path from the messages
// endpoint the adapter already pins.
func compactEndpoint(endpoint string) string {
	if strings.HasSuffix(endpoint, "/messages") {
		return endpoint + "/compact"
	}
	return strings.TrimSuffix(endpoint, "/") + "/messages/compact"
}

func (c *ProviderCompact) compact(ctx context.Context, h stores.History, p types.ModelProfile) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model":    p.Version,
		"messages": compactTranscript(h.Messages),
	})
	if err != nil {
		return "", &types.ModelError{Class: types.ClassPermanent, Provider: providerName, Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, compactEndpoint(c.cfg.Endpoint), strings.NewReader(string(body)))
	if err != nil {
		return "", &types.ModelError{Class: types.ClassPermanent, Provider: providerName, Err: err}
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("x-api-key", c.cfg.APIKey)
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctxErr := context.Cause(ctx); ctxErr != nil {
			return "", ctxErr
		}
		return "", &types.ModelError{Class: types.ClassTransient, Provider: providerName, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &types.ModelError{
			Class:    types.ClassPermanent,
			Provider: providerName,
			Err:      fmt.Errorf("compact primitive status %d", resp.StatusCode),
		}
	}
	var parsed struct {
		Summary string `json:"summary"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&parsed); err != nil {
		return "", &types.ModelError{Class: types.ClassPermanent, Provider: providerName, Err: err}
	}
	if parsed.Summary == "" {
		return "", &types.ModelError{
			Class:    types.ClassPermanent,
			Provider: providerName,
			Err:      errors.New("compact primitive returned no summary"),
		}
	}
	return parsed.Summary, nil
}

// compactTranscript renders the covered history as plain role/content turns.
func compactTranscript(msgs []types.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, map[string]any{"role": string(m.Role), "content": blocksPlainText(m.Blocks)})
	}
	return out
}

func blocksPlainText(blocks []types.Block) string {
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if t, ok := b.(types.Text); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func servesMode(modes []types.CompactionMode, mode types.CompactionMode) bool {
	for _, m := range modes {
		if m == mode {
			return true
		}
	}
	return false
}
