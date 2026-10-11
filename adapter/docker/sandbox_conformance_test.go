package docker

import (
	"testing"

	"github.com/victorzhuk/gohan/testkit/sandboxtest"
)

// TestSandboxConformance is the single binding file between the Docker
// adapter and the shared conformance suite. It owns no scenario id: the
// suite names the four owned ids once, and this binding imports the
// suite and supplies the Factory. The skip guard keeps the short test
// lane free of the live-daemon requirement.
func TestSandboxConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("gohan: conformance run needs a live daemon; skipped in short mode")
	}
	f := newConformanceFactory(t)
	sandboxtest.Sandbox(t, f)
}
