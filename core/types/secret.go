package types

import "context"

// SecretResolver resolves one named secret value for a principal. It is
// distinct from CredentialSource, which takes no name and returns the one
// harness credential for a run; the two are never satisfied by one object.
type SecretResolver interface {
	Resolve(ctx context.Context, p Principal, name string) (string, error)
}
