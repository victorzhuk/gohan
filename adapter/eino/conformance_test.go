package eino

import (
	"testing"

	"github.com/victorzhuk/gohan/testkit/conformance"
)

func TestConformance(t *testing.T) {
	t.Run("runtime.conformance-eino", func(t *testing.T) {
		conformance.Runtime(t, newEinoHarness)
	})
}
