package gohan

import (
	"context"

	"github.com/victorzhuk/gohan/core/types"
)

type ctxKey int

const (
	ctxPrincipal ctxKey = iota
	ctxCredential
	ctxRunInfo
	ctxIdempotencyKey
)

// WithPrincipal attaches the transport-verified principal. Only transport
// and harness code call it.
func WithPrincipal(ctx context.Context, p types.Principal) context.Context {
	return context.WithValue(ctx, ctxPrincipal, p)
}

// WithCredential attaches a credential for the run. Only transport and
// harness code call it; the value never leaves context except through
// CredentialSource.
func WithCredential(ctx context.Context, c types.Credential) context.Context {
	return context.WithValue(ctx, ctxCredential, c)
}

// WithIdempotencyKey attaches the caller-supplied idempotency key. Only
// transport and harness code call it.
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, ctxIdempotencyKey, key)
}

// PrincipalFrom reports the principal in ctx, or ok == false outside a run.
func PrincipalFrom(ctx context.Context) (types.Principal, bool) {
	p, ok := ctx.Value(ctxPrincipal).(types.Principal)
	return p, ok
}

// CredentialFrom reports the credential in ctx, or ok == false when none was
// attached.
func CredentialFrom(ctx context.Context) (types.Credential, bool) {
	c, ok := ctx.Value(ctxCredential).(types.Credential)
	return c, ok
}

// RunInfoFrom reports the run info in ctx, or ok == false outside a run.
func RunInfoFrom(ctx context.Context) (types.RunInfo, bool) {
	r, ok := ctx.Value(ctxRunInfo).(types.RunInfo)
	return r, ok
}

// IdempotencyKey reports the idempotency key in ctx, or ok == false when the
// caller supplied none.
func IdempotencyKey(ctx context.Context) (string, bool) {
	k, ok := ctx.Value(ctxIdempotencyKey).(string)
	return k, ok
}
