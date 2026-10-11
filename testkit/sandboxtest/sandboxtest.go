// Package sandboxtest holds the sandbox adapter conformance suite. Each
// suite runs unchanged against an injected types.Sandbox implementation,
// so every adapter proves the same isolation bar and keeps no private
// copy of an owned scenario.
//
// Binding contract: an adapter adds one _test.go file that imports
// testkit/sandboxtest, supplies a Factory, and calls
// sandboxtest.Sandbox(t, factory). adapter/docker/sandbox_conformance_test.go
// (owned by m2-docker-sandbox) and
// adapter/e2b/sandbox_conformance_test.go (owned by m2-e2b-sandbox) are
// those binding files. No adapter defines the owned scenario ids
// locally; the suite owns them once.
//
// The package is never imported by production code, core or std.
package sandboxtest

import (
	"context"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Factory is the seam an adapter fills in. The suite hard-codes no
// provider, image, socket, region or credential: everything
// provider-specific arrives through this one value.
type Factory interface {
	// Sandbox returns a fresh adapter bound to no live workspace. The
	// suite opens workspaces through types.Sandbox.Open.
	Sandbox(t *testing.T) types.Sandbox
	// Probe declares the argv shapes the adapter can run inside a
	// workspace. An adapter that runs no interpreter fails here, at
	// construction, rather than at assertion time.
	Probe() Probe
	// Recorder receives the observations the suite asserts and never a
	// provider secret.
	Recorder() Recorder
}

// NetworkFixture is the provider-owned endpoint set a network case runs
// against. PrivateHost names a live RFC1918 endpoint the provider
// controls; PrivatePort is its listening port. The shared private-range
// controls dial exactly this endpoint under DenyPrivate and AllowPrivate.
type NetworkFixture struct {
	PrivateHost string
	PrivatePort int
}

// NetworkFixtureProvider is the optional Factory companion an adapter
// implements when it can provision controlled network fixtures
// (docs/design/compatibility.md: interface growth by companion, never by
// extending Factory). A suite run over network scenarios requires it:
// the normative hostnames must resolve to provider-controlled listeners
// without public DNS, and the private-range controls need a live
// RFC1918 endpoint. An adapter without the companion fails construction
// of those cases; the suite never falls back to public DNS and never
// silently skips.
type NetworkFixtureProvider interface {
	// NetworkFixture returns the fixture bound to t's resources. The
	// provider registers bounded cleanup through t.Cleanup.
	NetworkFixture(t *testing.T) NetworkFixture
}

// networkFixture obtains the fixture a network case runs against. An
// adapter without the companion fails here, at construction, rather
// than at dial time.
func networkFixture(t *testing.T, f Factory) NetworkFixture {
	t.Helper()
	p, ok := f.(NetworkFixtureProvider)
	if !ok {
		t.Fatal("gohan: sandbox network fixture is required")
	}
	return p.NetworkFixture(t)
}

// Probe is the provider-neutral command vocabulary. Each method returns
// the argv for one command inside a workspace.
type Probe interface {
	// Dial opens one TCP connection and exits non-zero on refusal.
	Dial(host string, port int) []string
	// Sleep runs past MaxExecTime without finishing.
	Sleep(d time.Duration) []string
	// Echo exits zero and prints s.
	Echo(s string) []string
}

// Recorder is the observation sink: denial counts and the audit kinds
// the harness reported. It carries no URL, path, query, user-info or
// body.
type Recorder interface {
	Denied() int
	Kinds() []stores.AuditKind
	Reset()
}

// Sandbox runs the conformance suite against one adapter. The suite owns
// the workspace lifecycle: it opens through types.Sandbox.Open and
// closes through Workspace.Close, so the adapter supplies no lifecycle.
func Sandbox(t *testing.T, f Factory) {
	t.Helper()
	t.Run("sandbox.egress-denied", func(t *testing.T) { egressDenied(t, f) })
	t.Run("sandbox.egress-allowlisted", func(t *testing.T) { egressAllowlisted(t, f) })
	t.Run("sandbox.exec-timeout", func(t *testing.T) { execTimeout(t, f) })
	t.Run("sandbox.egress-shares-policy-type", func(t *testing.T) { egressSharesPolicyType(t, f) })
}

// open opens one per-call workspace and registers its close.
func open(t *testing.T, sb types.Sandbox, p types.SandboxPolicy) types.Workspace {
	t.Helper()
	w, err := sb.Open(context.Background(), types.WorkspaceRef(""), p)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	t.Cleanup(func() {
		if err := w.Close(context.Background()); err != nil {
			t.Errorf("close workspace: %v", err)
		}
	})
	return w
}

// exec runs one command and returns the result even on a non-zero exit:
// inside a workspace a non-zero exit is a result, not a harness error.
func exec(t *testing.T, w types.Workspace, argv []string) types.ExecResult {
	t.Helper()
	res, err := w.Exec(context.Background(), types.ExecRequest{Argv: argv})
	if err != nil {
		t.Fatalf("exec %v: %v", argv, err)
	}
	return res
}

// denyObserved asserts the refusal reached the observation seam. The
// suite never asserts a metric name: the denial-counter reconciliation
// is open item O1 of the change design.
func denyObserved(t *testing.T, r Recorder, before int) {
	t.Helper()
	if got := r.Denied(); got <= before {
		t.Fatalf("denied observations = %d, want more than %d", got, before)
	}
	var seen bool
	for _, k := range r.Kinds() {
		if k == stores.AuditEgressDenied {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("audit kinds %v lack %s", r.Kinds(), stores.AuditEgressDenied)
	}
}
