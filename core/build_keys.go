package gohan

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// ValidateProviderKeys resolves and validates the credential of every
// configured platform-key profile through validator. Build runs it before
// the stack is returned: a missing key fails closed with ErrNoProviderKey
// and an invalid key surfaces the validator's error, so a bad platform
// account never reaches the first call. Tenant-key profiles resolve their
// credential per request from the principal and are skipped here.
func ValidateProviderKeys(ctx context.Context, profiles []types.ModelProfile, src types.ProviderKeySource, validator types.ProviderKeyValidator) error {
	for i := range profiles {
		p := &profiles[i]
		if p.Keys != types.PlatformKey {
			continue
		}
		c, err := src.ProviderKey(ctx, p.Name, "")
		if err != nil {
			return fmt.Errorf("profile %s: %w", p.Name, err)
		}
		if err := validator.Validate(ctx, p.Name, c); err != nil {
			return fmt.Errorf("profile %s: %w", p.Name, err)
		}
	}
	return nil
}
