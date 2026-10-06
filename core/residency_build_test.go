package gohan

import (
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type regionlessSessionLog struct {
	stores.SessionLog
}

func residencyProfile(name, region string) fakeModel {
	p := profile(name, true)
	p.Region = region
	return fakeModel{profile: p}
}

func TestResidencyEnforcedAtBuild(t *testing.T) {
	t.Run("redaction.residency-enforced-at-build", func(t *testing.T) {
		t.Run("profile in another region", func(t *testing.T) {
			_, err := Build(
				WithTenant(types.Tenant{ID: "t1", Residency: "eu"}),
				WithModels(residencyProfile("primary", "us")),
			)
			if err == nil {
				t.Fatal("Build = nil error, want residency refusal")
			}
			for _, want := range []string{`t1`, `primary`, `us`, `eu`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q misses %q", err, want)
				}
			}
		})
		t.Run("fallback candidate in another region", func(t *testing.T) {
			_, err := Build(
				WithTenant(types.Tenant{ID: "t1", Residency: "eu"}),
				WithModels(residencyProfile("primary", "eu"), residencyProfile("backup", "us")),
			)
			if err == nil || !strings.Contains(err.Error(), `backup`) {
				t.Fatalf("Build error = %v, want refusal naming fallback profile", err)
			}
		})
		t.Run("store region mismatch", func(t *testing.T) {
			_, err := Build(
				WithTenant(types.Tenant{ID: "t1", Residency: "eu"}),
				WithStores(stores.Stores{
					SessionLog: stores.NewMemorySessionLog(
						stores.WithMemorySessionRegion("us"),
						stores.WithSessionPrincipals(types.PrincipalFrom),
					),
				}),
				WithModels(residencyProfile("primary", "eu")),
			)
			if err == nil {
				t.Fatal("Build = nil error, want residency refusal")
			}
			for _, want := range []string{`t1`, `session_log`, `us`, `eu`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q misses %q", err, want)
				}
			}
		})
		t.Run("store without region", func(t *testing.T) {
			_, err := Build(
				WithTenant(types.Tenant{ID: "t1", Residency: "eu"}),
				WithStores(stores.Stores{
					SessionLog: regionlessSessionLog{},
				}),
				WithModels(residencyProfile("primary", "eu")),
			)
			if err == nil || !strings.Contains(err.Error(), `session_log`) {
				t.Fatalf("Build error = %v, want refusal naming regionless store", err)
			}
		})
		t.Run("all regions match", func(t *testing.T) {
			s, err := Build(
				WithTenant(types.Tenant{ID: "t1", Residency: "eu"}),
				WithStores(stores.Stores{
					SessionLog: stores.NewMemorySessionLog(
						stores.WithMemorySessionRegion("eu"),
						stores.WithSessionPrincipals(types.PrincipalFrom),
					),
				}),
				WithModels(residencyProfile("primary", "eu"), residencyProfile("backup", "eu")),
			)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if got := s.Tenant(); got.ID != "t1" || got.Residency != "eu" {
				t.Errorf("Tenant() = %+v, want t1/eu", got)
			}
		})
		t.Run("no tenant checks nothing", func(t *testing.T) {
			if _, err := Build(WithModels(residencyProfile("primary", "us"))); err != nil {
				t.Fatalf("Build: %v", err)
			}
			if _, err := Build(
				WithTenant(types.Tenant{ID: "t1"}),
				WithModels(residencyProfile("primary", "us")),
			); err != nil {
				t.Fatalf("Build with unbound tenant: %v", err)
			}
		})
	})
}
