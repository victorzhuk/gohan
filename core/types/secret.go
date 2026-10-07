package types

import "context"

// SecretResolver resolves one named secret value for a principal. It is
// distinct from CredentialSource, which takes no name and returns the one
// harness credential for a run; the two are never satisfied by one object.
type SecretResolver interface {
	Resolve(ctx context.Context, p Principal, name string) (string, error)
}

type secretResolverContextKey struct{}

// WithSecrets carries the build's resolver to the tool-call boundary, so a
// tool resolves named secret values without holding a Stack reference.
func WithSecrets(ctx context.Context, r SecretResolver) context.Context {
	return context.WithValue(ctx, secretResolverContextKey{}, r)
}

func SecretsFrom(ctx context.Context) (SecretResolver, bool) {
	r, ok := ctx.Value(secretResolverContextKey{}).(SecretResolver)
	return r, ok
}
