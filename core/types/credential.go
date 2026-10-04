package types

import (
	"context"
	"encoding/json"
	"time"
)

// Credential is a short-lived secret bound to a principal. It lives only in
// context, set by transport code; it is never persisted, logged or exported.
type Credential struct {
	Token     string
	ExpiresAt time.Time
}

// Token is withheld from every textual and JSON representation so capture
// enabled anywhere cannot leak it.
func (c Credential) String() string { return "gohan:credential(redacted)" }

func (c Credential) MarshalJSON() ([]byte, error) { return json.Marshal(struct{}{}) }

// CredentialSource issues a fresh credential for a principal, used on resume
// and detached runs where the caller's token is expired or unavailable.
type CredentialSource interface {
	Credentials(ctx context.Context, p Principal) (Credential, error)
}
