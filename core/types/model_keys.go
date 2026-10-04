package types

import (
	"context"
	"time"
)

// ProviderCredential carries the resolved provider key for one call.
type ProviderCredential struct {
	ID        string
	Token     string
	ExpiresAt time.Time
}

// ProviderKeySource resolves the credential a profile uses for a run. The
// tenant argument is informational; the authoritative tenant comes from the
// principal in ctx.
type ProviderKeySource interface {
	ProviderKey(ctx context.Context, profile, tenant string) (ProviderCredential, error)
}

// ProviderKeyValidator checks a resolved credential before use; an auth
// failure must fail the call, never fall through to another key.
type ProviderKeyValidator interface {
	Validate(ctx context.Context, profile string, c ProviderCredential) error
}
