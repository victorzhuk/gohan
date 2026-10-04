package keys

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
)

func principalCtx(t *testing.T, tenant string) context.Context {
	t.Helper()
	return gohan.WithPrincipal(context.Background(), types.Principal{Subject: "u1", Tenant: tenant})
}

func profile(mode types.KeyMode) map[string]types.ModelProfile {
	return map[string]types.ModelProfile{
		"gpt": {Name: "gpt", Keys: mode},
	}
}

func fixtureToken(parts ...string) string {
	return strings.Join(parts, "-")
}

var (
	fixtureTenantToken   = fixtureToken("tenant", "token")
	fixturePlatformToken = fixtureToken("platform", "token")
	fixtureT1Token       = fixtureToken("t1", "token")
	fixtureT2Token       = fixtureToken("t2", "token")
)

func TestProviderKeys(t *testing.T) {
	t.Run("model.tenant-key-selected", func(t *testing.T) {
		src := MapSource{
			Profiles: profile(types.TenantKey),
			Keys: map[string]map[string]types.ProviderCredential{
				"gpt": {
					"t1":           {ID: "k-tenant", Token: fixtureTenantToken},
					platformTenant: {ID: "k-platform", Token: fixturePlatformToken},
				},
			},
		}
		// Tool arguments name another tenant; the context principal wins.
		c, err := src.ProviderKey(principalCtx(t, "t1"), "gpt", "other")
		if err != nil {
			t.Fatalf("ProviderKey() error = %v", err)
		}
		if c.ID != "k-tenant" || c.Token != fixtureTenantToken {
			t.Fatalf("ProviderKey() = %+v, want tenant credential", c)
		}
	})

	t.Run("model.tenant-key-missing-fails-closed", func(t *testing.T) {
		src := MapSource{
			Profiles: profile(types.TenantKey),
			Keys: map[string]map[string]types.ProviderCredential{
				"gpt": {platformTenant: {ID: "k-platform", Token: fixturePlatformToken}},
			},
		}
		if _, err := src.ProviderKey(principalCtx(t, "t9"), "gpt", ""); !errors.Is(err, types.ErrNoProviderKey) {
			t.Fatalf("ProviderKey() error = %v, want ErrNoProviderKey", err)
		}
	})

	t.Run("model.no-fallback-on-auth-error", func(t *testing.T) {
		authErr := errors.New("gohan: auth rejected")
		src := MapSource{
			Profiles: profile(types.TenantOrPlatformKey),
			Keys: map[string]map[string]types.ProviderCredential{
				"gpt": {
					"t1":           {ID: "k-tenant", Token: fixtureTenantToken},
					platformTenant: {ID: "k-platform", Token: fixturePlatformToken},
				},
			},
			Validate: func(_ context.Context, _ string, c types.ProviderCredential) error {
				if c.ID == "k-tenant" {
					return authErr
				}
				return nil
			},
		}
		if _, err := src.ProviderKey(principalCtx(t, "t1"), "gpt", ""); !errors.Is(err, authErr) {
			t.Fatalf("ProviderKey() error = %v, want auth error without platform fallback", err)
		}
	})

	t.Run("identity.tenant-for-key-from-ctx", func(t *testing.T) {
		src := MapSource{
			Profiles: profile(types.TenantKey),
			Keys: map[string]map[string]types.ProviderCredential{
				"gpt": {
					"t1": {ID: "k-t1", Token: fixtureT1Token},
					"t2": {ID: "k-t2", Token: fixtureT2Token},
				},
			},
		}
		// The request names t2; only t1's key is reachable for a t1 principal.
		c, err := src.ProviderKey(principalCtx(t, "t1"), "gpt", "t2")
		if err != nil {
			t.Fatalf("ProviderKey() error = %v", err)
		}
		if c.ID != "k-t1" {
			t.Fatalf("ProviderKey() = %+v, want t1 credential", c)
		}
	})

	t.Run("map tenant mode requires principal", func(t *testing.T) {
		src := MapSource{
			Profiles: profile(types.TenantKey),
			Keys:     map[string]map[string]types.ProviderCredential{"gpt": {}},
		}
		if _, err := src.ProviderKey(context.Background(), "gpt", ""); !errors.Is(err, types.ErrNoPrincipal) {
			t.Fatalf("ProviderKey() error = %v, want ErrNoPrincipal", err)
		}
	})

	t.Run("map platform mode ignores tenant", func(t *testing.T) {
		src := MapSource{
			Profiles: profile(types.PlatformKey),
			Keys: map[string]map[string]types.ProviderCredential{
				"gpt": {
					"t1":           {ID: "k-t1", Token: fixtureT1Token},
					platformTenant: {ID: "k-platform", Token: fixturePlatformToken},
				},
			},
		}
		c, err := src.ProviderKey(principalCtx(t, "t1"), "gpt", "t1")
		if err != nil {
			t.Fatalf("ProviderKey() error = %v", err)
		}
		if c.ID != "k-platform" {
			t.Fatalf("ProviderKey() = %+v, want platform credential", c)
		}
	})

	t.Run("map expired credential is missing", func(t *testing.T) {
		now := time.Unix(1000, 0)
		src := MapSource{
			Profiles: profile(types.TenantKey),
			Keys: map[string]map[string]types.ProviderCredential{
				"gpt": {"t1": {ID: "k-expired", Token: fixtureToken("old", "value"), ExpiresAt: now}},
			},
			Now: func() time.Time { return now },
		}
		if _, err := src.ProviderKey(principalCtx(t, "t1"), "gpt", ""); !errors.Is(err, types.ErrNoProviderKey) {
			t.Fatalf("ProviderKey() error = %v, want ErrNoProviderKey", err)
		}
	})

	t.Run("env tenant then platform fallback", func(t *testing.T) {
		env := map[string]string{
			"GOHAN_GPT_T1_KEY": fixtureTenantToken,
			"GOHAN_GPT_KEY":    fixturePlatformToken,
		}
		src := EnvSource{
			Profiles: profile(types.TenantOrPlatformKey),
			Lookup:   envlookup(env),
		}
		c, err := src.ProviderKey(principalCtx(t, "t1"), "gpt", "")
		if err != nil {
			t.Fatalf("ProviderKey() error = %v", err)
		}
		if c.Token != fixtureTenantToken {
			t.Fatalf("ProviderKey() token = %q, want tenant-token", c.Token)
		}
		delete(env, "GOHAN_GPT_T1_KEY")
		c, err = src.ProviderKey(principalCtx(t, "t1"), "gpt", "")
		if err != nil {
			t.Fatalf("ProviderKey() after delete error = %v", err)
		}
		if c.Token != fixturePlatformToken {
			t.Fatalf("ProviderKey() token = %q, want platform-token", c.Token)
		}
	})

	t.Run("env tenant mode ignores platform variable", func(t *testing.T) {
		src := EnvSource{
			Profiles: profile(types.TenantKey),
			Lookup: envlookup(map[string]string{
				"GOHAN_GPT_KEY": "platform-token",
			}),
		}
		if _, err := src.ProviderKey(principalCtx(t, "t1"), "gpt", ""); !errors.Is(err, types.ErrNoProviderKey) {
			t.Fatalf("ProviderKey() error = %v, want ErrNoProviderKey", err)
		}
	})

	t.Run("env platform mode uses platform variable", func(t *testing.T) {
		src := EnvSource{
			Profiles: profile(types.PlatformKey),
			Lookup: envlookup(map[string]string{
				"GOHAN_GPT_T1_KEY": fixtureTenantToken,
				"GOHAN_GPT_KEY":    fixturePlatformToken,
			}),
		}
		c, err := src.ProviderKey(principalCtx(t, "t1"), "gpt", "")
		if err != nil {
			t.Fatalf("ProviderKey() error = %v", err)
		}
		if c.Token != fixturePlatformToken {
			t.Fatalf("ProviderKey() token = %q, want platform-token", c.Token)
		}
	})

	t.Run("env missing key fails closed", func(t *testing.T) {
		src := EnvSource{Lookup: envlookup(nil)}
		if _, err := src.ProviderKey(principalCtx(t, "t1"), "gpt", ""); !errors.Is(err, types.ErrNoProviderKey) {
			t.Fatalf("ProviderKey() error = %v, want ErrNoProviderKey", err)
		}
	})
}

func envlookup(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
}
