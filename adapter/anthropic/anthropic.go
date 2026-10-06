// Package anthropic implements the gohan model port over the Anthropic
// Messages API. The user wires it in as a types.Model; core never imports
// this package, so no Anthropic type reaches the floor. The provider
// credential travels only in the request header: it never appears in a
// span attribute, a log record or an error string.
package anthropic

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

const providerName = "anthropic"

const defaultMaxTokens = 4096

const anthropicVersion = "2023-06-01"

const defaultEndpoint = "https://api.anthropic.com/v1/messages"

// Config pins one Anthropic endpoint and its credential.
type Config struct {
	Endpoint string
	APIKey   string
	// KeyID names the credential in usage records and telemetry. It must
	// never carry the credential itself.
	KeyID   string
	Profile types.ModelProfile
	Client  *http.Client
}

// Model is a types.Model over the Anthropic Messages API. One value serves
// every run; per-request state lives only in the call.
type Model struct {
	cfg Config
	hc  *http.Client
}

// New validates the profile and returns the adapter.
func New(cfg Config) (*Model, error) {
	if cfg.Profile.Name == "" {
		return nil, errors.New("anthropic: profile name is required")
	}
	if cfg.Profile.Version == "" {
		return nil, errors.New("anthropic: profile version is required")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultEndpoint
	}
	if cfg.Client == nil {
		cfg.Client = http.DefaultClient
	}
	return &Model{cfg: cfg, hc: cfg.Client}, nil
}

// Profile returns the validated profile.
func (m *Model) Profile() types.ModelProfile { return m.cfg.Profile }

// Generate translates the request, calls the endpoint and streams the
// response as model chunks. Every failure is a *types.ModelError.
func (m *Model) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(types.ModelChunk{}, err)
			return
		}
		body, state, err := m.assemble(ctx, req)
		if err != nil {
			yield(types.ModelChunk{}, err)
			return
		}
		resp, err := m.send(ctx, body)
		if err != nil {
			yield(types.ModelChunk{}, err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			yield(types.ModelChunk{}, mapError(resp.StatusCode, resp.Header, resp.Body))
			return
		}
		if drift := servedVersionDrift(resp.Header, m.cfg.Profile); drift != "" {
			yield(types.ModelChunk{}, &types.ModelError{
				Class:    types.ClassVersionDrift,
				Provider: providerName,
				Err:      errors.New(drift),
			})
			return
		}
		m.decode(ctx, resp.Body, state, yield)
	}
}

func (m *Model) send(ctx context.Context, body []byte) (*http.Response, error) {
	endpoint := m.cfg.Endpoint
	if endpoint == defaultEndpoint && m.cfg.Profile.Endpoint != "" {
		endpoint = m.cfg.Profile.Endpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, &types.ModelError{Class: types.ClassPermanent, Provider: providerName, Err: err}
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("x-api-key", m.cfg.APIKey)
	resp, err := m.hc.Do(req)
	if err != nil {
		class := types.ClassTransient
		if ctxErr := context.Cause(ctx); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, &types.ModelError{Class: class, Provider: providerName, Err: err}
	}
	return resp, nil
}

func servedVersionDrift(h http.Header, profile types.ModelProfile) string {
	if !profile.Caps.StrictVersion {
		return ""
	}
	served := h.Get("x-served-model")
	if served == "" || served == profile.Version {
		return ""
	}
	return "served model version " + served + " differs from validated version " + profile.Version
}

func parseRetryAfter(h http.Header) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return 0
}
