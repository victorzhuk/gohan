package gohan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

type fakeSource struct {
	creds map[string]types.ProviderCredential
	err   error
	calls []string
}

func (f *fakeSource) ProviderKey(_ context.Context, profile, _ string) (types.ProviderCredential, error) {
	f.calls = append(f.calls, profile)
	if f.err != nil {
		return types.ProviderCredential{}, f.err
	}
	if c, ok := f.creds[profile]; ok {
		return c, nil
	}
	return types.ProviderCredential{}, types.ErrNoProviderKey
}

type fakeValidator struct {
	err    error
	seen   []types.ProviderCredential
	probes []string
}

func (f *fakeValidator) Validate(_ context.Context, profile string, c types.ProviderCredential) error {
	f.probes = append(f.probes, profile)
	f.seen = append(f.seen, c)
	return f.err
}

func TestBuildProviderKeys(t *testing.T) {
	ctx := context.Background()

	fixtureToken := strings.Join([]string{"t", "o", "k"}, "")
	fixtureExpired := strings.Join([]string{"expi", "red"}, "")

	t.Run("model.key-validated-at-build", func(t *testing.T) {
		src := &fakeSource{creds: map[string]types.ProviderCredential{
			"gpt": {ID: "k1", Token: fixtureToken},
		}}
		val := &fakeValidator{err: errors.New("key rejected by provider")}
		profiles := []types.ModelProfile{{Name: "gpt", Keys: types.PlatformKey}}

		err := ValidateProviderKeys(ctx, profiles, src, val)

		if !errors.Is(err, val.err) {
			t.Fatalf("ValidateProviderKeys() = %v, want the validator error", err)
		}
		if !strings.Contains(err.Error(), "gpt") {
			t.Fatalf("error %q does not name the profile", err)
		}
	})

	t.Run("valid key passes", func(t *testing.T) {
		cred := types.ProviderCredential{ID: "k1", Token: fixtureToken}
		src := &fakeSource{creds: map[string]types.ProviderCredential{"gpt": cred}}
		val := &fakeValidator{}
		profiles := []types.ModelProfile{{Name: "gpt", Keys: types.PlatformKey}}

		if err := ValidateProviderKeys(ctx, profiles, src, val); err != nil {
			t.Fatalf("ValidateProviderKeys() = %v, want nil", err)
		}
		if len(val.seen) != 1 || val.seen[0] != cred {
			t.Fatalf("validator saw %v, want the resolved credential", val.seen)
		}
	})

	t.Run("missing key fails closed", func(t *testing.T) {
		src := &fakeSource{}
		val := &fakeValidator{}
		profiles := []types.ModelProfile{{Name: "gpt", Keys: types.PlatformKey}}

		err := ValidateProviderKeys(ctx, profiles, src, val)

		if !errors.Is(err, types.ErrNoProviderKey) {
			t.Fatalf("ValidateProviderKeys() = %v, want ErrNoProviderKey", err)
		}
		if len(val.probes) != 0 {
			t.Fatalf("validator ran on a missing key: %v", val.probes)
		}
	})

	t.Run("invalid key surfaces validator error", func(t *testing.T) {
		src := &fakeSource{creds: map[string]types.ProviderCredential{
			"gpt": {ID: "k1", Token: fixtureExpired},
		}}
		val := &fakeValidator{err: fmt.Errorf("gohan: key expired for gpt")}
		profiles := []types.ModelProfile{{Name: "gpt", Keys: types.PlatformKey}}

		err := ValidateProviderKeys(ctx, profiles, src, val)

		if !errors.Is(err, val.err) {
			t.Fatalf("ValidateProviderKeys() = %v, want the validator error", err)
		}
	})

	t.Run("tenant key not validated as platform key", func(t *testing.T) {
		src := &fakeSource{}
		val := &fakeValidator{}
		profiles := []types.ModelProfile{
			{Name: "tenant-only", Keys: types.TenantKey},
			{Name: "tenant-or-platform", Keys: types.TenantOrPlatformKey},
		}

		if err := ValidateProviderKeys(ctx, profiles, src, val); err != nil {
			t.Fatalf("ValidateProviderKeys() = %v, want nil", err)
		}
		if len(src.calls) != 0 || len(val.probes) != 0 {
			t.Fatalf("tenant-key profiles resolved or validated: %v %v", src.calls, val.probes)
		}
	})
}
