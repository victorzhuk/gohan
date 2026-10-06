// Package openai adapts the gohan model port to an OpenAI-compatible
// chat-completions endpoint.
package openai

import (
	"context"
	"fmt"
	"iter"
	"net/http"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

const defaultEndpoint = "https://api.openai.com/v1"

// Config builds a Provider. The profile carries the capability decisions;
// the endpoint and key source carry the deployment specifics.
type Config struct {
	Profile        types.ModelProfile
	Endpoint       string
	Transport      Transport
	Keys           types.ProviderKeySource
	AffinityHeader string
	Now            func() time.Time
}

// Transport performs the provider call; *http.Client satisfies it.
type Transport interface {
	Do(*http.Request) (*http.Response, error)
}

// Provider is the OpenAI chat-completions adapter.
type Provider struct {
	profile        types.ModelProfile
	endpoint       string
	transport      Transport
	keys           types.ProviderKeySource
	affinityHeader string
	now            func() time.Time
}

// New validates the profile before the provider serves a single call: a bad
// profile fails construction, never a Generate.
func New(cfg Config) (*Provider, error) {
	p := cfg.Profile
	if !p.Caps.Streaming {
		return nil, fmt.Errorf("profile %q does not declare streaming", p.Name)
	}
	for _, kind := range modelledKinds {
		if _, ok := p.Caps.Fidelity[kind]; !ok {
			return nil, fmt.Errorf("profile %q declares no fidelity for block kind %q", p.Name, kind)
		}
	}
	if p.Caps.Cache == types.CacheExplicit && p.Caps.CacheBreakpoints <= 0 {
		return nil, fmt.Errorf("profile %q declares explicit cache with no breakpoints", p.Name)
	}
	if cfg.Keys == nil {
		return nil, fmt.Errorf("profile %q has no provider key source", p.Name)
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	transport := cfg.Transport
	if transport == nil {
		transport = roundTripperTransport{rt: http.DefaultTransport}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Provider{
		profile:        p,
		endpoint:       endpoint,
		transport:      transport,
		keys:           cfg.Keys,
		affinityHeader: cfg.AffinityHeader,
		now:            now,
	}, nil
}

var modelledKinds = []types.BlockKind{
	types.KindText,
	types.KindToolUse,
	types.KindToolResult,
}

// Profile reports the profile the provider was built with.
func (p *Provider) Profile() types.ModelProfile { return p.profile }

type roundTripperTransport struct{ rt http.RoundTripper }

func (t roundTripperTransport) Do(r *http.Request) (*http.Response, error) {
	return t.rt.RoundTrip(r)
}

// Generate streams one model call. Every per-call decision — governed Extra
// keys, schema support, the credential — resolves before the transport runs.
func (p *Provider) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(types.ModelChunk{}, err)
			return
		}
		if key, ok := governedExtraKey(req.Options.Extra); ok {
			yield(types.ModelChunk{}, &types.ModelError{
				Class:    types.ClassPermanent,
				Provider: p.profile.Name,
				Code:     "gohan.extra_governed_key",
				Err:      fmt.Errorf("option extra key %q is governed by the request contract", key),
			})
			return
		}
		if len(req.Options.ResponseSchema) > 0 && !p.profile.Caps.Constrained {
			yield(types.ModelChunk{}, &types.ModelError{
				Class:    types.ClassPermanent,
				Provider: p.profile.Name,
				Code:     "output_schema_unsupported",
				Err:      fmt.Errorf("profile %q does not declare constrained output", p.profile.Name),
			})
			return
		}
		body, err := p.assembleBody(req)
		if err != nil {
			yield(types.ModelChunk{}, &types.ModelError{
				Class:    types.ClassPermanent,
				Provider: p.profile.Name,
				Err:      err,
			})
			return
		}
		cred, err := p.resolveKey(ctx)
		if err != nil {
			yield(types.ModelChunk{}, &types.ModelError{
				Class:    types.ClassAuth,
				Provider: p.profile.Name,
				Err:      err,
			})
			return
		}
		payload, err := marshalBody(body)
		if err != nil {
			yield(types.ModelChunk{}, &types.ModelError{
				Class:    types.ClassPermanent,
				Provider: p.profile.Name,
				Err:      fmt.Errorf("encode request: %w", err),
			})
			return
		}
		hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/chat/completions", payload)
		if err != nil {
			yield(types.ModelChunk{}, &types.ModelError{
				Class:    types.ClassPermanent,
				Provider: p.profile.Name,
				Err:      fmt.Errorf("build request: %w", err),
			})
			return
		}
		hreq.Header.Set("Content-Type", "application/json")
		hreq.Header.Set("Authorization", "Bearer "+cred.Token)
		if p.affinityHeader != "" && req.Options.AffinityKey != "" {
			hreq.Header.Set(p.affinityHeader, req.Options.AffinityKey)
		}
		resp, err := p.transport.Do(hreq)
		if err != nil {
			yield(types.ModelChunk{}, &types.ModelError{
				Class:    types.ClassTransient,
				Provider: p.profile.Name,
				Err:      fmt.Errorf("send request: %w", err),
			})
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			yield(types.ModelChunk{}, p.mapResponseError(resp))
			return
		}
		p.stream(ctx, resp, req, cred.ID, yield)
	}
}

func governedExtraKey(extra map[string]any) (string, bool) {
	if len(extra) == 0 {
		return "", false
	}
	for _, key := range types.GovernedModelOptionKeys() {
		if _, ok := extra[key]; ok {
			return key, true
		}
	}
	return "", false
}

func (p *Provider) resolveKey(ctx context.Context) (types.ProviderCredential, error) {
	tenant := ""
	if principal, ok := types.PrincipalFrom(ctx); ok {
		tenant = principal.Tenant
	}
	switch p.profile.Keys {
	case types.PlatformKey:
		tenant = ""
	case types.TenantOrPlatformKey:
		if tenant == "" {
			break
		}
		cred, err := p.keys.ProviderKey(ctx, p.profile.Name, tenant)
		if err == nil {
			return p.validated(ctx, cred)
		}
		fallthrough
	default:
	}
	cred, err := p.keys.ProviderKey(ctx, p.profile.Name, tenant)
	if err != nil {
		return types.ProviderCredential{}, fmt.Errorf("resolve provider key: %w", err)
	}
	return p.validated(ctx, cred)
}

func (p *Provider) validated(ctx context.Context, cred types.ProviderCredential) (types.ProviderCredential, error) {
	if v, ok := p.keys.(types.ProviderKeyValidator); ok {
		if err := v.Validate(ctx, p.profile.Name, cred); err != nil {
			return types.ProviderCredential{}, fmt.Errorf("validate provider key: %w", err)
		}
	}
	return cred, nil
}
