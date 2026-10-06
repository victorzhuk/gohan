package gohan

import "github.com/victorzhuk/gohan/core/types"

// WithVerifier binds the port that reconciles Unknown journal entries at
// the verification triggers. A stack built without one leaves every
// unknown key in the uncertainty set, which reaches the caller as
// *types.UncertainOutcomeError.
func WithVerifier(v types.Verifier) Option {
	return func(c *config) error {
		c.verifier = v
		return nil
	}
}
