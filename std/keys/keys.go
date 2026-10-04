// Package keys provides ProviderKeySource implementations backed by the
// environment or an in-memory map. The tenant for key selection always comes
// from the principal in the context; a tenant named in tool arguments or flow
// input never selects another tenant's key, and a key that fails
// authentication fails the call instead of falling through to another key.
package keys

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
)

// platformTenant is the map key and env placeholder for the platform
// account's own credentials.
const platformTenant = ""

// EnvSource resolves credentials from environment variables. The variable
// for a tenant key is GOHAN_<PROFILE>_<TENANT>_KEY; the platform key is
// GOHAN_<PROFILE>_KEY. Profile and tenant names are upper-cased with every
// character outside [A-Z0-9] replaced by an underscore.
type EnvSource struct {
	// Lookup reads one environment variable. nil uses os.LookupEnv.
	Lookup func(string) (string, bool)

	// Profiles maps profile names to their profiles so KeyMode drives the
	// selection; a profile absent from the map selects TenantOrPlatformKey.
	Profiles map[string]types.ModelProfile
}

// ProviderKey selects the credential for the principal's tenant according to
// the profile's KeyMode. It returns types.ErrNoProviderKey when the selected
// mode has no key.
func (s EnvSource) ProviderKey(ctx context.Context, profile, _ string) (types.ProviderCredential, error) {
	lookup := s.Lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	for _, tenant := range s.tenants(ctx, profile) {
		name, ok := envName(profile, tenant)
		if !ok {
			continue
		}
		token, ok := lookup(name)
		if !ok || len(token) == 0 {
			continue
		}
		return types.ProviderCredential{Token: token}, nil
	}
	return types.ProviderCredential{}, fmt.Errorf("%w: profile %q", types.ErrNoProviderKey, profile)
}

func (s EnvSource) tenants(ctx context.Context, profile string) []string {
	mode := types.TenantOrPlatformKey
	if p, ok := s.Profiles[profile]; ok {
		mode = p.Keys
	}
	tenant := platformTenant
	if p, ok := gohan.PrincipalFrom(ctx); ok {
		tenant = p.Tenant
	}
	switch mode {
	case types.PlatformKey:
		return []string{platformTenant}
	case types.TenantKey:
		return []string{tenant}
	default:
		if tenant != platformTenant {
			return []string{tenant, platformTenant}
		}
		return []string{platformTenant}
	}
}

func envName(profile, tenant string) (string, bool) {
	p := envSegment(profile)
	if p == "" {
		return "", false
	}
	if tenant == platformTenant {
		return "GOHAN_" + p + "_KEY", true
	}
	t := envSegment(tenant)
	if t == "" {
		return "", false
	}
	return "GOHAN_" + p + "_" + t + "_KEY", true
}

func envSegment(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		switch {
		case r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// MapSource resolves credentials from an in-memory table. Keys maps a
// profile name to a tenant-keyed table; the platform account uses the empty
// tenant. Only the tenant in the context's principal is reachable: a
// tenant named elsewhere in the request selects nothing.
type MapSource struct {
	Profiles map[string]types.ModelProfile
	Keys     map[string]map[string]types.ProviderCredential

	// Validate, when set, checks each candidate credential. A validation
	// failure is returned as-is: the selection never falls through to
	// another tenant or the platform account on an auth error.
	Validate func(ctx context.Context, profile string, c types.ProviderCredential) error

	// Now reports the time used to judge ExpiresAt; time.Now when nil.
	Now func() time.Time
}

// ProviderKey selects the credential for the principal's tenant according to
// the profile's KeyMode. It returns types.ErrNoProviderKey when the selected
// mode has no live key and types.ErrNoPrincipal when the mode needs a tenant
// but the context carries no principal.
func (s MapSource) ProviderKey(ctx context.Context, profile, _ string) (types.ProviderCredential, error) {
	tenants, err := s.tenants(ctx, profile)
	if err != nil {
		return types.ProviderCredential{}, err
	}
	table := s.Keys[profile]
	now := s.Now
	if now == nil {
		now = time.Now
	}
	for _, tenant := range tenants {
		c, ok := table[tenant]
		if !ok || c.Token == "" || !c.ExpiresAt.IsZero() && !c.ExpiresAt.After(now()) {
			continue
		}
		if s.Validate != nil {
			if err := s.Validate(ctx, profile, c); err != nil {
				return types.ProviderCredential{}, err
			}
		}
		return c, nil
	}
	return types.ProviderCredential{}, fmt.Errorf("%w: profile %q", types.ErrNoProviderKey, profile)
}

func (s MapSource) tenants(ctx context.Context, profile string) ([]string, error) {
	mode := types.TenantOrPlatformKey
	if p, ok := s.Profiles[profile]; ok {
		mode = p.Keys
	}
	principal, ok := gohan.PrincipalFrom(ctx)
	tenant := platformTenant
	if ok {
		tenant = principal.Tenant
	}
	switch mode {
	case types.PlatformKey:
		return []string{platformTenant}, nil
	case types.TenantKey:
		if !ok {
			return nil, types.ErrNoPrincipal
		}
		return []string{tenant}, nil
	default:
		if !ok {
			return []string{platformTenant}, nil
		}
		return []string{tenant, platformTenant}, nil
	}
}
