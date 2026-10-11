package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// ProviderCompact folds history through the /responses/compact primitive.
// It implements the stores.Compactor contract adapter-side over the same
// endpoint and credential seam the Provider uses; core never imports this
// package, so the caller binds it through ContextPolicy.Compactor.
type ProviderCompact struct {
	profile   types.ModelProfile
	endpoint  string
	transport Transport
	keys      types.ProviderKeySource
}

// NewProviderCompact pins the compactor to the provider configuration.
func NewProviderCompact(cfg Config) (*ProviderCompact, error) {
	if cfg.Profile.Name == "" {
		return nil, fmt.Errorf("profile %q: compaction needs a profile name", cfg.Profile.Name)
	}
	if cfg.Keys == nil {
		return nil, fmt.Errorf("profile %q has no provider key source", cfg.Profile.Name)
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	transport := cfg.Transport
	if transport == nil {
		transport = roundTripperTransport{rt: http.DefaultTransport}
	}
	return &ProviderCompact{
		profile:   cfg.Profile,
		endpoint:  endpoint,
		transport: transport,
		keys:      cfg.Keys,
	}, nil
}

// ServedModes declares the Caps.Compaction modes the compactor serves:
// /responses/compact returns an opaque encrypted item, so it serves opaque
// mode.
func (c *ProviderCompact) ServedModes() []types.CompactionMode {
	return []types.CompactionMode{types.CompactionOpaqueCap}
}

// Compact folds the covered prefix the caller selected into one
// types.Compaction. The returned item travels as the provider sent it: the
// block holds the response bytes verbatim and the request encoder renders
// them byte-identically, never pruned or rewritten. The block records the
// endpoint identity in Model and covers exactly History.Version.
func (c *ProviderCompact) Compact(ctx context.Context, h stores.History, p types.ModelProfile) (types.Compaction, error) {
	if !servesModes(c.ServedModes(), p.Caps.Compaction) {
		return types.Compaction{}, fmt.Errorf("openai: profile %q: compaction mode %d: /responses/compact serves opaque compaction only", p.Name, p.Caps.Compaction)
	}
	item, err := c.compact(ctx, h, p)
	if err != nil {
		return types.Compaction{}, err
	}
	return types.Compaction{
		Kind:       types.CompactionOpaque,
		CoversUpTo: h.Version,
		Opaque:     types.Raw{Provider: p.Name, Value: item},
		Model:      p.Name,
	}, nil
}

func (c *ProviderCompact) compact(ctx context.Context, h stores.History, p types.ModelProfile) (json.RawMessage, error) {
	cred, err := c.resolveKey(ctx)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{
		"model":    p.Version,
		"messages": compactTranscript(h.Messages),
	})
	if err != nil {
		return nil, fmt.Errorf("encode compact request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/responses/compact", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build compact request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cred.Token)
	resp, err := c.transport.Do(req)
	if err != nil {
		if ctxErr := context.Cause(ctx); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("send compact request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("profile %q: compact primitive status %d", p.Name, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read compact item: %w", err)
	}
	item := json.RawMessage(bytes.TrimSpace(data))
	if !json.Valid(item) || len(item) == 0 {
		return nil, fmt.Errorf("profile %q: compact primitive returned no opaque item", p.Name)
	}
	return item, nil
}

func (c *ProviderCompact) resolveKey(ctx context.Context) (types.ProviderCredential, error) {
	tenant := ""
	if principal, ok := types.PrincipalFrom(ctx); ok {
		tenant = principal.Tenant
	}
	cred, err := c.keys.ProviderKey(ctx, c.profile.Name, tenant)
	if err != nil {
		return types.ProviderCredential{}, fmt.Errorf("resolve provider key: %w", err)
	}
	return cred, nil
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

func servesModes(modes []types.CompactionMode, mode types.CompactionMode) bool {
	for _, m := range modes {
		if m == mode {
			return true
		}
	}
	return false
}
