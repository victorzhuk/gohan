package eino

import (
	"testing"

	"github.com/victorzhuk/gohan/testkit/conformance"
)

func TestConformance(t *testing.T) {
	conformance.Runtime(t, newEinoHarness)
}
