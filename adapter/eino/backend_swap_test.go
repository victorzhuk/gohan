package eino

import (
	"testing"

	"github.com/victorzhuk/gohan/testkit/conformance"
)

func TestBackendSwap(t *testing.T) {
	t.Run("flow.backend-swap", func(t *testing.T) {
		conformance.BackendSwap(t, newEinoHarness)
	})
}
