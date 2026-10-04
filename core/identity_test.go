package gohan

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestIdentityContext(t *testing.T) {
	t.Run("identity.accessor-outside-run", func(t *testing.T) {
		ctx := context.Background()

		p, ok := PrincipalFrom(ctx)
		if ok || p.Subject != "" || p.Tenant != "" || p.Scopes != nil {
			t.Fatalf("PrincipalFrom outside a run: got (%v, %v), want zero, false", p, ok)
		}
		c, ok := CredentialFrom(ctx)
		if ok || c != (types.Credential{}) {
			t.Fatalf("CredentialFrom outside a run: got (%v, %v), want zero, false", c, ok)
		}
		r, ok := RunInfoFrom(ctx)
		if ok || r.Flow != "" || r.RunID != "" || r.Turn != 0 || r.Mode != types.Primary {
			t.Fatalf("RunInfoFrom outside a run: got (%v, %v), want zero, false", r, ok)
		}
		k, ok := IdempotencyKey(ctx)
		if ok || k != "" {
			t.Fatalf("IdempotencyKey outside a run: got (%q, %v), want \"\", false", k, ok)
		}
	})
}

func TestCredentialIsolation(t *testing.T) {
	t.Run("identity.token-never-exported", func(t *testing.T) {
		credentialValue := fmt.Sprintf("%s-%s-%s", "tok", "secret", "value")
		c := types.Credential{Token: credentialValue, ExpiresAt: time.Now()}

		for name, got := range map[string]string{
			"%v":    fmt.Sprintf("%v", c),
			"%+v":   fmt.Sprintf("%+v", c),
			"%s":    c.String(),
			"json":  string(mustJSON(t, c)),
			"pjson": string(mustJSON(t, map[string]any{"cred": c})),
		} {
			if strings.Contains(got, credentialValue) {
				t.Errorf("%s representation contains the credential value: %q", name, got)
			}
		}
	})

	t.Run("identity.credential-not-on-principal", func(t *testing.T) {
		p := principalFields(types.Principal{})
		for _, f := range p {
			lf := strings.ToLower(f)
			if strings.Contains(lf, "token") || strings.Contains(lf, "cred") || strings.Contains(lf, "secret") {
				t.Errorf("Principal has credential-like field %q", f)
			}
		}
	})
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

func principalFields(v any) []string {
	typ := reflect.TypeOf(v)
	names := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		names = append(names, typ.Field(i).Name)
	}
	return names
}
