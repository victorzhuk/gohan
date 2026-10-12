// Package cache owns the harness cache seam: the content-addressed key
// builder and the exact-match store. This package is the only owner of the
// key builder and the store; the cache capability declares no store in
// core, so a host that never enables a cache pays nothing. Redis arrives
// later as a further store behind the same seam: a backend replaces the
// store, never the key builder, and the key contract stays fixed.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	jsonv2 "encoding/json/v2"

	"github.com/victorzhuk/gohan/core/types"
)

// Scope is the principal scope class a CacheStep declares for its entries:
// shared across the tenant's principals, or isolated per user.
type Scope string

const (
	// ScopeShared admits every principal of the tenant to the entry.
	ScopeShared Scope = "shared"
	// ScopeUser pins an entry to the principal that produced it.
	ScopeUser Scope = "per-user"
)

// Manifest is the identity of everything the assembled request depends on
// besides the request itself. Each field is the contributing component's
// identity string (a hash or a version); a component with no identity
// contributes its empty string, which the key renders as an explicit
// absent marker rather than a zero shared with another component.
type Manifest struct {
	Prompts      string
	Chains       string
	Toolset      string
	ModelVersion string
}

// Input is the normalized request a key is built from. The tenant and the
// principal come from the context; the scope class, the manifest, the
// resolved prompt version and the request come from the step.
type Input struct {
	Scope         Scope
	Manifest      Manifest
	PromptVersion string
	Request       types.ModelRequest
}

const absent = "\x00absent"

// Key builds the contract key: tenant, principal scope class, manifest
// hash (prompts, chains, toolset, model Version), resolved prompt version
// and the normalized request. The rendering is length-framed per component
// and the result is a SHA-256 hex string, so equal inputs hash equal keys
// in every process. Run attribution (RequestMetadata) is excluded from the
// normalized request: cost tags attribute a run, they are not content.
func Key(ctx context.Context, req Input) (string, error) {
	h := sha256.New()
	seg := func(label, val string) {
		if val == "" {
			val = absent
		}
		_, _ = fmt.Fprintf(h, "%s\x1f%d\x1f%s\x1e", label, len(val), val)
	}

	p, ok := types.PrincipalFrom(ctx)
	if !ok {
		seg("tenant", absent+"principal")
	} else {
		seg("tenant", p.Tenant)
	}
	seg("scope", string(req.Scope))
	if req.Scope == ScopeUser {
		seg("subject", p.Subject)
	}
	seg("manifest.prompts", req.Manifest.Prompts)
	seg("manifest.chains", req.Manifest.Chains)
	seg("manifest.toolset", req.Manifest.Toolset)
	seg("model.version", req.Manifest.ModelVersion)
	seg("prompt.version", req.PromptVersion)

	norm, err := normalize(req.Request)
	if err != nil {
		return "", err
	}
	seg("request", norm)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func normalize(r types.ModelRequest) (string, error) {
	r.Metadata = types.RequestMetadata{}
	b, err := jsonv2.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("cache: normalize request: %w", err)
	}
	return string(b), nil
}
