package adkgo

import (
	"testing"

	"github.com/victorzhuk/gohan/testkit/conformance"
)

func TestBackendSwap(t *testing.T) {
	t.Run("engines.adkgo-workflow-swap", func(t *testing.T) {
		conformance.BackendSwap(t, newAdkgoHarness)
	})
}
