package conformance

import "testing"

func TestBackendSwap(t *testing.T) {
	t.Run("flow.backend-swap", func(t *testing.T) {
		BackendSwap(t, newNativeRuntimeHarness)
	})
}
