package docker

import (
	"context"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// dial runs one HTTP fetch probe inside the workspace: busybox nc exits
// zero even on a failed DNS lookup, so a real HTTP round trip is the
// success signal and any failure exits non-zero.
func dial(t *testing.T, w types.Workspace, host string, port string) types.ExecResult {
	t.Helper()
	res, err := w.Exec(context.Background(), types.ExecRequest{
		Argv:    []string{"sh", "-c", "wget -q -T 4 -O /dev/null http://" + host + ":" + port + "/"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial %s:%s: %v", host, port, err)
	}
	t.Logf("dial %s:%s -> %+v", host, port, res)
	return res
}

// reached reports whether the probe actually talked to the endpoint:
// the probe succeeds on a full HTTP exchange or on the server's HTTP
// error, and fails on DNS, connect or drop.
func reached(res types.ExecResult) bool {
	return res.ExitCode == 0 || strings.Contains(string(res.Stderr), "HTTP/1.")
}

// dialRaw runs one literal-address connect probe against a listener.
func dialRaw(t *testing.T, w types.Workspace, ip string, port string) types.ExecResult {
	t.Helper()
	res, err := w.Exec(context.Background(), types.ExecRequest{
		Argv:    []string{"sh", "-c", "echo | timeout 5 nc -w 2 " + ip + " " + port},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("raw dial %s:%s: %v", ip, port, err)
	}
	return res
}

func TestEgressDenyAll(t *testing.T) {
	sb := newTestSandbox(t)
	w, err := sb.Open(context.Background(), "", types.SandboxPolicy{Lifecycle: types.PerCall})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close(context.Background()) })

	sb.rec.Reset()
	res := dial(t, w, "example.com", "80")
	if res.ExitCode == 0 {
		t.Fatalf("dial under deny-all egress exit = %d, want refusal", res.ExitCode)
	}
	if sb.rec.Denied() == 0 {
		t.Fatalf("denied observations = %d, want the refusal observed", sb.rec.Denied())
	}
}

// TestEgressAllowlist runs the normative allowlist case against the
// controlled fixture peers: api.example.com resolves to the allowed
// fixture listener, other.example.com to an equally healthy peer outside
// the allowlist. No public resolver or internet listener takes part. It
// then attempts an iptables flush through the public Workspace.Exec:
// the attempt must fail and leave the allowed and denied outcomes
// intact — this behavioral assertion stays red until the trusted-setup
// chunk removes the workload's firewall authority.
func TestEgressAllowlist(t *testing.T) {
	f := newConformanceFactory(t)
	f.NetworkFixture(t)
	sb := f.Sandbox(t)
	p := types.SandboxPolicy{Lifecycle: types.PerCall}
	p.Egress.Allow = []string{"api.example.com:443"}
	w, err := sb.Open(context.Background(), types.WorkspaceRef(f.WorkloadImage()), p)
	if err != nil {
		t.Fatalf("open allowlisted: %v", err)
	}
	t.Cleanup(func() { _ = w.Close(context.Background()) })

	res := dial(t, w, "api.example.com", "443")
	if !reached(res) {
		t.Fatalf("allowlisted fixture peer not reached: exit %d stderr %s", res.ExitCode, res.Stderr)
	}

	f.rec.Reset()
	res = dial(t, w, "other.example.com", "443")
	if res.ExitCode == 0 {
		t.Fatal("non-allowlisted fixture peer dial succeeded, want denial")
	}
	if f.rec.Denied() == 0 {
		t.Fatal("denied observations = 0, want the refusal observed")
	}
	for _, k := range f.rec.Kinds() {
		if k != "egress_denied" {
			t.Fatalf("observed kind = %q, want egress_denied only", k)
		}
	}

	flush, err := w.Exec(context.Background(), types.ExecRequest{
		Argv:    []string{"sh", "-c", "iptables -F"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("exec iptables flush: %v", err)
	}
	if flush.ExitCode == 0 {
		t.Fatal("workload iptables flush exited 0, want rejection of the firewall mutation")
	}

	res = dial(t, w, "api.example.com", "443")
	if !reached(res) {
		t.Fatalf("allowlisted fixture peer unreachable after flush attempt: exit %d stderr %s", res.ExitCode, res.Stderr)
	}
	f.rec.Reset()
	res = dial(t, w, "other.example.com", "443")
	if res.ExitCode == 0 {
		t.Fatal("non-allowlisted fixture peer dial succeeded after flush attempt, want denial")
	}
	if f.rec.Denied() == 0 {
		t.Fatal("denied observations after flush attempt = 0, want the refusal observed")
	}
	for _, k := range f.rec.Kinds() {
		if k != "egress_denied" {
			t.Fatalf("observed kind after flush attempt = %q, want egress_denied only", k)
		}
	}
}

func TestEgressPrivateRanges(t *testing.T) {
	ctx := context.Background()
	sb := newTestSandbox(t)

	t.Run("deny_private", func(t *testing.T) {
		p := types.SandboxPolicy{Lifecycle: types.PerCall}
		p.Egress.Allow = []string{"example.com:80"}
		p.Egress.PrivateRanges = types.DenyPrivate
		w, err := sb.Open(ctx, "", p)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = w.Close(ctx) })

		ip := privatePeer(t, w.(*workspace))
		sb.rec.Reset()
		res := dialRaw(t, w, ip, "8080")
		if res.ExitCode == 0 {
			t.Fatal("private dial under DenyPrivate succeeded, want denial")
		}
		if sb.rec.Denied() == 0 {
			t.Fatal("denied observations = 0, want the refusal observed")
		}
	})

	t.Run("allow_private", func(t *testing.T) {
		p := types.SandboxPolicy{Lifecycle: types.PerCall}
		p.Egress.Allow = []string{"example.com:80"}
		p.Egress.PrivateRanges = types.AllowPrivate
		w, err := sb.Open(ctx, "", p)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = w.Close(ctx) })

		ip := privatePeer(t, w.(*workspace))
		sb.rec.Reset()
		res := dialRaw(t, w, ip, "8080")
		if res.ExitCode != 0 {
			t.Fatalf("private dial under AllowPrivate exit = %d stderr %s", res.ExitCode, res.Stderr)
		}
		if sb.rec.Denied() != 0 {
			t.Fatalf("denied observations = %d, want 0 under AllowPrivate", sb.rec.Denied())
		}
	})
}

// privatePeer runs a listener container on the workspace's own bridge
// network and returns its address: a private destination a real peer
// listens on, so the positive control dials something reachable.
func privatePeer(t *testing.T, ws *workspace) string {
	t.Helper()
	created, err := ws.api.ContainerCreate(context.Background(),
		client.ContainerCreateOptions{
			Config: &container.Config{
				Image: "alpine:3.23",
				Cmd:   []string{"nc", "-l", "-p", "8080"},
			},
			NetworkingConfig: &network.NetworkingConfig{
				EndpointsConfig: map[string]*network.EndpointSettings{ws.netID: {}},
			},
		})
	if err != nil {
		t.Fatalf("create peer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = ws.api.ContainerRemove(context.Background(), created.ID,
			client.ContainerRemoveOptions{Force: true})
	})
	if _, err := ws.api.ContainerStart(context.Background(), created.ID,
		client.ContainerStartOptions{}); err != nil {
		t.Fatalf("start peer: %v", err)
	}
	insp, err := ws.api.ContainerInspect(context.Background(), created.ID,
		client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("inspect peer: %v", err)
	}
	var ip netip.Addr
	deadline := time.Now().Add(10 * time.Second)
	for {
		if insp.Container.NetworkSettings != nil &&
			insp.Container.NetworkSettings.Networks != nil {
			if ep := insp.Container.NetworkSettings.Networks[ws.netID]; ep != nil && ep.IPAddress.IsValid() {
				ip = ep.IPAddress
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("peer has no address on %s", ws.netID)
		}
		time.Sleep(200 * time.Millisecond)
		insp, err = ws.api.ContainerInspect(context.Background(), created.ID,
			client.ContainerInspectOptions{})
		if err != nil {
			t.Fatalf("inspect peer: %v", err)
		}
	}
	return ip.String()
}

// TestEgressDenialObserved asserts the refusal reaches the shared
// observation seam as the governed audit kind and no metric name: the
// Recorder carries only counts and kinds, and the counter-name
// reconciliation stays the conformance change's open item O1.
func TestEgressDenialObserved(t *testing.T) {
	sb := newTestSandbox(t)
	p := types.SandboxPolicy{Lifecycle: types.PerCall}
	p.Egress.Allow = []string{"example.com:80"}
	w, err := sb.Open(context.Background(), "", p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close(context.Background()) })

	sb.rec.Reset()
	if sb.rec.Denied() != 0 || len(sb.rec.Kinds()) != 0 {
		t.Fatalf("after reset: denied = %d kinds = %v, want empty", sb.rec.Denied(), sb.rec.Kinds())
	}
	res := dial(t, w, "other.example.com", "443")
	if res.ExitCode == 0 {
		t.Fatal("non-allowlisted dial succeeded, want denial")
	}
	if sb.rec.Denied() != 1 {
		t.Fatalf("denied observations = %d, want 1", sb.rec.Denied())
	}
	kinds := sb.rec.Kinds()
	if len(kinds) != 1 || kinds[0] != "egress_denied" {
		t.Fatalf("kinds = %v, want [egress_denied] appended by the governed seam", kinds)
	}

	// A classified resolution refusal with no DROP evidence records
	// exactly one observation: no packet counter moved, and the stderr
	// fallback must not stack on a second source.
	before := sb.rec.Denied()
	res = dial(t, w, "missing.gohan-test.invalid", "443")
	if res.ExitCode == 0 {
		t.Fatal("NXDOMAIN dial succeeded, want refusal")
	}
	if sb.rec.Denied() != before+1 {
		t.Fatalf("denied observations after NXDOMAIN = %d, want %d", sb.rec.Denied(), before+1)
	}

	// An unrelated command failure establishes no policy denial.
	before = sb.rec.Denied()
	res, err = w.Exec(context.Background(), types.ExecRequest{
		Argv:    []string{"sh", "-c", "false"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("exec false: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatal("false exited zero")
	}
	if sb.rec.Denied() != before {
		t.Fatalf("denied observations after unrelated failure = %d, want %d", sb.rec.Denied(), before)
	}

	// A sleep timeout is a process kill, not an egress refusal.
	before = sb.rec.Denied()
	res, err = w.Exec(context.Background(), types.ExecRequest{
		Argv:    []string{"sleep", "5"},
		Timeout: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("exec sleep: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatal("timed-out sleep exited zero, want kill")
	}
	if sb.rec.Denied() != before {
		t.Fatalf("denied observations after sleep timeout = %d, want %d", sb.rec.Denied(), before)
	}
}

// TestEgressDenyAddressRules pins the shared private-range and hop
// decisions the chain is built from, without a daemon.
func mustIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("parse %s", s)
	}
	return ip
}
func TestEgressDenyAddressRules(t *testing.T) {
	cases := []struct {
		ip   string
		pr   types.PrivateRanges
		deny bool
	}{
		{"192.168.1.10", types.DenyPrivate, true},
		{"192.168.1.10", types.AllowPrivate, false},
		{"10.0.0.7", types.DenyPrivate, true},
		{"169.254.1.1", types.AllowPrivate, true},
		{"127.0.0.1", types.DenyPrivate, true},
		{"8.8.8.8", types.DenyPrivate, false},
		{"127.0.0.1", types.AllowPrivate, false},
	}
	for _, c := range cases {
		if got := deniedAddress(mustIP(t, c.ip), c.pr); got != c.deny {
			t.Errorf("deniedAddress(%s, %d) = %v, want %v", c.ip, c.pr, got, c.deny)
		}
	}
}
