package gohan

import (
	"context"

	"github.com/victorzhuk/gohan/core/types"
)

// The identity context seams live in the floor; these wrappers keep the
// driver call sites unchanged.
func WithPrincipal(ctx context.Context, p types.Principal) context.Context {
	return types.WithPrincipal(ctx, p)
}

func WithCredential(ctx context.Context, c types.Credential) context.Context {
	return context.WithValue(ctx, ctxCredential, c)
}

func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return types.WithIdempotencyKey(ctx, key)
}

func PrincipalFrom(ctx context.Context) (types.Principal, bool) {
	return types.PrincipalFrom(ctx)
}

func CredentialFrom(ctx context.Context) (types.Credential, bool) {
	c, ok := ctx.Value(ctxCredential).(types.Credential)
	return c, ok
}

func RunInfoFrom(ctx context.Context) (types.RunInfo, bool) {
	return types.RunInfoFrom(ctx)
}

func IdempotencyKey(ctx context.Context) (string, bool) {
	return types.IdempotencyKey(ctx)
}

type ctxKey int

const ctxCredential ctxKey = iota
