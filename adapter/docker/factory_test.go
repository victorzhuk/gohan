package docker

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/sandboxtest"
)

// Factory binds the Docker adapter to the shared conformance suite. It
// owns the provider-specific resources: the sandbox, the probe
// vocabulary, the observation recorder and, through the
// NetworkFixtureProvider companion, the controlled network fixtures the
// suite's network cases run against. The suite constructs none of them.
type Factory struct {
	c   *Client
	rec *Recorder

	mu       sync.Mutex
	fx       *fixtureResources
	fxNetIDs []string
}

// newConformanceFactory connects to the daemon and handshakes, skipping
// the test when no daemon is reachable.
func newConformanceFactory(t *testing.T) *Factory {
	t.Helper()
	c, err := NewClient("")
	if err != nil {
		t.Skipf("docker client unavailable: %v", err)
	}
	if err := c.Handshake(context.Background()); err != nil {
		t.Skipf("docker daemon unavailable: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &Factory{c: c, rec: NewRecorder()}
}

// Sandbox returns a fresh adapter bound to no live workspace. The suite
// opens workspaces through types.Sandbox.Open.
func (f *Factory) Sandbox(t *testing.T) types.Sandbox {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	return &Sandbox{c: f.c, rec: f.rec, fxNetIDs: f.fxNetIDs}
}

// Probe declares the argv shapes the adapter runs inside a workspace.
func (f *Factory) Probe() sandboxtest.Probe { return probe{} }

// Recorder receives the observations the suite asserts.
func (f *Factory) Recorder() sandboxtest.Recorder { return f.rec }

// probe is the provider-neutral command vocabulary over the base image's
// shell and busybox tools. Dial performs the requested TCP connection
// inside the actual workspace: nc exits zero on a connected dial and
// non-zero on a refused, dropped or unresolvable one.
type probe struct{}

func (probe) Dial(host string, port int) []string {
	// wget performs the requested TCP connection and a full HTTP
	// exchange inside the actual workspace: it exits zero only on a
	// connected, answered dial, and any resolution failure, drop or
	// refusal exits non-zero and the wrapper tags the stderr so the
	// observation layer can classify the refusal.
	return []string{"sh", "-c",
		"wget -q -T 4 -O /dev/null http://" + host + ":" + strconv.Itoa(port) + "/ || { echo dial failed; exit 1; }"}
}

func (probe) Sleep(d time.Duration) []string {
	return []string{"sleep", strconv.Itoa(int((d + time.Second - 1) / time.Second))}
}

func (probe) Echo(s string) []string {
	return []string{"echo", s}
}
