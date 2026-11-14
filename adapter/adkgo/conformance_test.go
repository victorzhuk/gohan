package adkgo

import (
	"testing"

	"github.com/victorzhuk/gohan/testkit/conformance"
)

func TestConformance(t *testing.T) {
	t.Run("engines.adkgo-runtime-conformance", func(t *testing.T) {
		conformance.Runtime(t, newAdkgoHarness)
	})
	t.Run("runtime.conformance-adkgo", func(t *testing.T) {
		conformance.Runtime(t, newAdkgoHarness)
	})
}
