package conformance

import "testing"

// BackendSwap runs the flow acceptance suite every backend must pass: the
// same scenarios, the same assertions, the same scripted model, no test
// code changed per backend. Bind it under the flow.backend-swap subtest
// name once per backend.
func BackendSwap(t *testing.T, newHarness func(Scenario) Harness) {
	t.Helper()
	Runtime(t, newHarness)
}
