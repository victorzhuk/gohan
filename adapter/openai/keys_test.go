package openai_test

import (
	"context"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/adapter/openai"
	"github.com/victorzhuk/gohan/core/types"
)

type keyCall struct {
	profile, tenant string
}

type keySource struct {
	id       string
	token    string
	byTenant map[string]types.ProviderCredential
	calls    []keyCall
}

func simpleKeys(id, token string) *keySource {
	return &keySource{id: id, token: token}
}

// tenantToken builds the per-tenant sentinel at runtime so no credential-shaped
// literal appears in the test source.
func tenantToken(part string) string {
	return "sentinel-tenant-" + part + "-token"
}

func (k *keySource) ProviderKey(ctx context.Context, profile, tenant string) (types.ProviderCredential, error) {
	k.calls = append(k.calls, keyCall{profile: profile, tenant: tenant})
	if cred, ok := k.byTenant[tenant]; ok {
		return cred, nil
	}
	return types.ProviderCredential{ID: k.id, Token: k.token}, nil
}

func (k *keySource) tenants() []string {
	out := make([]string, 0, len(k.calls))
	for _, c := range k.calls {
		out = append(out, c.tenant)
	}
	return out
}

func TestProviderKeyIsolation(t *testing.T) {
	t.Run("model.isolation-per-key", func(t *testing.T) {
		keys := &keySource{
			id:    "platform-key-id",
			token: "platform" + "-token",
			byTenant: map[string]types.ProviderCredential{
				"tenant-a": {ID: "key-a", Token: tenantToken("a")},
				"tenant-b": {ID: "key-b", Token: tenantToken("b")},
			},
		}
		tp := &captureTransport{}
		p, err := openai.New(openai.Config{
			Profile: types.ModelProfile{
				Name:    fixtureProfile,
				Version: fixtureVersion,
				Keys:    types.TenantKey,
				Caps:    proofCaps(nil),
			},
			Keys:      keys,
			Transport: tp,
		})
		if err != nil {
			t.Fatalf("build provider: %v", err)
		}
		for _, tc := range []struct {
			tenant string
			keyID  string
			token  string
		}{
			{tenant: "tenant-a", keyID: "key-a", token: tenantToken("a")},
			{tenant: "tenant-b", keyID: "key-b", token: tenantToken("b")},
		} {
			ctx := types.WithPrincipal(context.Background(), types.Principal{Subject: "u1", Tenant: tc.tenant})
			req := proofRequest("usage reported")
			u := lastUsage(t, collectIn(t, ctx, p, req))
			if u.KeyID != tc.keyID {
				t.Errorf("tenant %s resolved key id %q, want %q", tc.tenant, u.KeyID, tc.keyID)
			}
			auth := tp.requests[len(tp.requests)-1].Header.Get("Authorization")
			if auth != "Bearer "+tc.token {
				t.Errorf("tenant %s sent authorization %q, want its own credential", tc.tenant, auth)
			}
		}
		tenants := keys.tenants()
		if len(tenants) != 2 || tenants[0] == tenants[1] {
			t.Errorf("key source saw calls %v, want one per distinct tenant", tenants)
		}
	})
}

func TestKeyNeverInTelemetry(t *testing.T) {
	t.Run("model.key-never-in-telemetry", func(t *testing.T) {
		sentinel := "sentinel-secret-" + "token-never-logged"
		h := &captureHandler{}
		withSlog(t, h)
		keys := simpleKeys(fixtureKeyID, sentinel)
		tp := &captureTransport{}
		p := proofModel(t, proofCaps(nil), keys, tp)

		u := lastUsage(t, collect(t, p, proofRequest("usage reported")))
		if u.KeyID != fixtureKeyID {
			t.Errorf("usage key id %q, want %q", u.KeyID, fixtureKeyID)
		}
		err := drainErr(p, proofRequest("rate limited with retry after"))
		if err == nil {
			t.Fatal("rate limited response was swallowed")
		}
		if strings.Contains(err.Error(), sentinel) {
			t.Errorf("error string carries the credential: %v", err)
		}
		for _, value := range h.attrValues() {
			if strings.Contains(value, sentinel) {
				t.Errorf("telemetry attribute carries the credential: %q", value)
			}
		}
		for _, rec := range h.records {
			if strings.Contains(rec.Message, sentinel) {
				t.Errorf("log message carries the credential: %q", rec.Message)
			}
		}
		for i, req := range tp.requests {
			if strings.Contains(string(req.Body), sentinel) {
				t.Errorf("request %d body carries the credential", i)
			}
		}
		last := tp.requests[len(tp.requests)-1]
		if got := last.Header.Get("Authorization"); got != "Bearer "+sentinel {
			t.Errorf("authorization header %q, want the credential on the wire", got)
		}
	})
}
