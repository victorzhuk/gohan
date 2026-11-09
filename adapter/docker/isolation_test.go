package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/go-connections/sockets"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/core/types"
)

// versionPrefixRE strips the negotiated API version segment from daemon
// request paths, so fault matching is independent of negotiation.
var versionPrefixRE = regexp.MustCompile(`^/v[0-9.]+`)

var startPathRE = regexp.MustCompile(`^/containers/([^/]+)/start$`)

// TestSandboxIsolation seals the sandbox security contracts of
// specs/sandbox/spec.md against the public Sandbox.Open and
// Workspace.Exec/Put/Get/Snapshot surface. The leaves run against the
// real daemon and stay red until the trusted-setup production chunk
// removes the workload's firewall authority, adds setup-failure cleanup
// and orders policy before workload start.
func TestSandboxIsolation(t *testing.T) {
	t.Run("sandbox.firewall-authority-isolated", testFirewallAuthorityIsolated)
	t.Run("sandbox.policy-setup-failure-cleanup", func(t *testing.T) {
		testPolicySetupFailureCleanup(t)
		t.Run("admin_timeout", testPolicySetupAdminTimeout)
		t.Run("open_cancelled", testPolicySetupOpenCancelled)
	})
	t.Run("sandbox.restored-startup-policy-first", testRestoredStartupPolicyFirst)
}

// mustExec runs one public Workspace.Exec call and fails the test on a
// start error.
func mustExec(t *testing.T, w types.Workspace, timeout time.Duration, argv ...string) types.ExecResult {
	t.Helper()
	res, err := w.Exec(context.Background(), types.ExecRequest{Argv: argv, Timeout: timeout})
	if err != nil {
		t.Fatalf("exec %v: %v", argv, err)
	}
	return res
}

// assertFixtureConnectivity proves both fixture peers keep their policy
// outcome: the allowlisted listener answers a full HTTP exchange and the
// equally healthy non-allowlisted listener stays unreachable with the
// refusal recorded as an egress denial.
func assertFixtureConnectivity(t *testing.T, f *Factory, w types.Workspace) {
	t.Helper()
	res := dial(t, w, "api.example.com", "443")
	if !reached(res) {
		t.Fatalf("allowlisted fixture peer unreachable: exit %d stderr %s", res.ExitCode, res.Stderr)
	}
	f.rec.Reset()
	res = dial(t, w, "other.example.com", "443")
	if res.ExitCode == 0 {
		t.Fatal("non-allowlisted fixture peer dial succeeded, want denial")
	}
	if f.rec.Denied() == 0 {
		t.Fatal("denied observations = 0, want the refusal observed")
	}
}

// testFirewallAuthorityIsolated runs the firewall-mutation and
// capability-regain attacks through public Workspace.Exec against a
// workload opened from the iptables-provisioned fixture image. Trusted
// controls prove the utilities run before any outcome counts as denial.
func testFirewallAuthorityIsolated(t *testing.T) {
	f := newConformanceFactory(t)
	f.NetworkFixture(t)
	ctx := context.Background()
	sb := f.Sandbox(t)
	p := types.SandboxPolicy{Lifecycle: types.PerCall}
	p.Egress.Allow = []string{"api.example.com:443"}
	w, err := sb.Open(ctx, types.WorkspaceRef(f.WorkloadImage()), p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close(ctx) })

	// Trusted positive control: each attack utility must execute
	// successfully in its authorized read-only form before any refusal
	// below counts as denial; a missing binary proves nothing.
	ctrl := mustExec(t, w, 10*time.Second, "sh", "-c", "iptables --version >/dev/null && capsh --print >/dev/null")
	if ctrl.ExitCode != 0 {
		t.Fatalf("fixture attack utilities unusable in workload image (iptables/capsh): exit %d stderr %s", ctrl.ExitCode, ctrl.Stderr)
	}

	// The kernel must deny the workload every capability, including the
	// NET_ADMIN the enforcement chain needs, and must bar privilege
	// regaining through no-new-privileges.
	status := mustExec(t, w, 10*time.Second, "sh", "-c", "grep -E '^(CapEff|CapBnd|NoNewPrivs):' /proc/self/status")
	caps, nnp, err := parseProcStatus(string(status.Stdout))
	if err != nil {
		t.Fatalf("read workload /proc/self/status: %v (stdout %q)", err, status.Stdout)
	}
	if caps["CapEff"] != 0 || caps["CapBnd"] != 0 || nnp != "1" {
		t.Fatalf("workload privilege drop missing: CapEff=%d CapBnd=%d NoNewPrivs=%s, want empty effective and bounding sets and NoNewPrivs=1", caps["CapEff"], caps["CapBnd"], nnp)
	}

	assertFixtureConnectivity(t, f, w)

	flush := mustExec(t, w, 10*time.Second, "sh", "-c", "iptables -F")
	if flush.ExitCode == 0 {
		t.Fatal("workload iptables -F exited 0, want the kernel to refuse the firewall mutation")
	}
	regain := mustExec(t, w, 10*time.Second, "sh", "-c", "capsh --caps=cap_net_admin+ep -- -c 'iptables -L -n' >/dev/null 2>&1")
	if regain.ExitCode == 0 {
		t.Fatal("capability regain through capsh exited 0, want refusal")
	}

	assertFixtureConnectivity(t, f, w)
}

// parseProcStatus reads CapEff/CapBnd hex words and the NoNewPrivs flag
// from a /proc/self/status dump.
func parseProcStatus(s string) (map[string]uint64, string, error) {
	caps := map[string]uint64{}
	var nnp string
	for _, line := range strings.Split(s, "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(fields) != 2 {
			continue
		}
		v := strings.TrimSpace(fields[1])
		switch fields[0] {
		case "CapEff", "CapBnd":
			u, err := strconv.ParseUint(v, 16, 64)
			if err != nil {
				return nil, "", fmt.Errorf("parse %s %q: %w", fields[0], v, err)
			}
			caps[fields[0]] = u
		case "NoNewPrivs":
			nnp = v
		}
	}
	if _, ok := caps["CapEff"]; !ok {
		return nil, "", fmt.Errorf("CapEff line missing")
	}
	if _, ok := caps["CapBnd"]; !ok {
		return nil, "", fmt.Errorf("CapBnd line missing")
	}
	if nnp == "" {
		return nil, "", fmt.Errorf("NoNewPrivs line missing")
	}
	return caps, nnp, nil
}

// sandboxTransport decorates the pinned Docker client's HTTP transport
// with a request log, exact created-resource capture and optional
// one-shot faults matched on method and resource identity. All unrelated
// traffic forwards unchanged; the product action stays public Open.
type sandboxTransport struct {
	base http.RoundTripper

	mu         sync.Mutex
	rules      []*faultRule
	log        []string
	containers map[string]string // created container id -> image
	networks   []string
}

type faultRule struct {
	name  string
	armed bool
	hits  int
	// delay slows the matched request instead of failing it, for
	// deadline and cancellation coverage.
	delay time.Duration
	match func(tr *sandboxTransport, method, path string, body []byte) bool
}

func newSandboxTransport() *sandboxTransport {
	return &sandboxTransport{
		base:       http.DefaultTransport,
		containers: map[string]string{},
	}
}

// arm registers one one-shot fault. It returns the rule name the hits
// assertion reads.
func (t *sandboxTransport) arm(name string, match func(tr *sandboxTransport, method, path string, body []byte) bool) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rules = append(t.rules, &faultRule{name: name, armed: true, match: match})
	return name
}

// hits counts how often the named fault fired; an armed fault must fire
// exactly once.
func (t *sandboxTransport) hits(name string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, r := range t.rules {
		if r.name == name {
			n += r.hits
		}
	}
	return n
}

// armDelay registers a one-shot slow forwarding rule: the matched
// request is delayed by d and then forwarded unchanged.
func (t *sandboxTransport) armDelay(name string, d time.Duration, match func(tr *sandboxTransport, method, path string, body []byte) bool) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rules = append(t.rules, &faultRule{name: name, armed: true, delay: d, match: match})
	return name
}

// reset clears the per-attempt request log and created-resource capture
// so a following open starts from an empty inventory window.
func (t *sandboxTransport) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.log = nil
	t.containers = map[string]string{}
	t.networks = nil
}

func (t *sandboxTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.GetBody != nil {
		gb, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(gb)
		if err != nil {
			return nil, err
		}
		_ = gb.Close()
		body = b
	} else if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		// An emptied body keeps NoBody so the transport does not switch
		// to chunked encoding, which the daemon rejects on start.
		if len(b) == 0 {
			req.Body = http.NoBody
		} else {
			req.Body = io.NopCloser(bytes.NewReader(b))
		}
		body = b
	}
	method, path := req.Method, versionPrefixRE.ReplaceAllString(req.URL.Path, "")
	t.mu.Lock()
	for _, r := range t.rules {
		if r.armed && r.match(t, method, path, body) {
			r.armed = false
			r.hits++
			if r.delay > 0 {
				t.mu.Unlock()
				time.Sleep(r.delay)
				return t.base.RoundTrip(req)
			}
			t.mu.Unlock()
			return nil, fmt.Errorf("injected fault %s", r.name)
		}
	}
	t.log = append(t.log, method+" "+path)
	t.mu.Unlock()

	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	if method == "POST" && (path == "/containers/create" || path == "/networks/create") {
		b, rerr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if rerr == nil {
			resp.Body = io.NopCloser(bytes.NewReader(b))
			t.absorb(path, body, b)
		}
	}
	return resp, err
}

// absorb records the exact IDs the daemon assigned to this attempt's
// creates, keyed by the requested image, so the inventory diff uses
// exact created IDs only.
func (t *sandboxTransport) absorb(path string, reqBody, resBody []byte) {
	var res struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(resBody, &res); err != nil || res.ID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if path == "/networks/create" {
		t.networks = append(t.networks, res.ID)
		return
	}
	var req struct {
		Image string `json:"Image"`
	}
	if err := json.Unmarshal(reqBody, &req); err != nil {
		req.Image = ""
	}
	t.containers[res.ID] = req.Image
}

// createdContainers returns a copy of the created container id -> image
// map.
func (t *sandboxTransport) createdContainers() map[string]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]string, len(t.containers))
	for k, v := range t.containers {
		out[k] = v
	}
	return out
}

// createdNetworks returns the networks the daemon created for this
// attempt.
func (t *sandboxTransport) createdNetworks() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.networks...)
}

// lastExecIndex returns the log index of the last exec API call, or -1.
func (t *sandboxTransport) lastExecIndex() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	idx := -1
	for i, e := range t.log {
		if strings.Contains(e, "/exec") {
			idx = i
		}
	}
	return idx
}

// startIndexOf returns the log index of the container's start request,
// or -1.
func (t *sandboxTransport) startIndexOf(id string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	want := "POST /containers/" + id + "/start"
	for i, e := range t.log {
		if e == want {
			return i
		}
	}
	return -1
}

// containersCreatedFrom reports the container IDs created from image,
// within the log window recorded after the last reset.
func (t *sandboxTransport) containersCreatedFrom(image string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var ids []string
	for id, img := range t.containers {
		if img == image {
			ids = append(ids, id)
		}
	}
	return ids
}

// decoratedSandbox returns the real Sandbox whose pinned client forwards
// every daemon request through tr. It shares the factory's recorder and
// fixture networks, so Open remains the only product action.
func decoratedSandbox(t *testing.T, f *Factory, tr *sandboxTransport) *Sandbox {
	t.Helper()
	host := os.Getenv(client.EnvOverrideHost)
	if host == "" {
		host = client.DefaultDockerHost
	}
	hostURL, err := client.ParseHostURL(host)
	if err != nil {
		t.Fatalf("parse daemon host %s: %v", host, err)
	}
	base := &http.Transport{}
	if err := sockets.ConfigureTransport(base, hostURL.Scheme, hostURL.Host); err != nil {
		t.Fatalf("configure daemon transport: %v", err)
	}
	tr.base = base
	api, err := client.NewClientWithOpts(
		client.WithHost(host),
		client.WithHTTPClient(&http.Client{Transport: tr}),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		t.Fatalf("build decorated docker client: %v", err)
	}
	t.Cleanup(func() { _ = api.Close() })
	if _, err := api.Ping(context.Background(), client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		t.Fatalf("decorated client handshake: %v", err)
	}
	sb := f.Sandbox(t).(*Sandbox)
	sb.c = &Client{api: api, baseImage: f.c.BaseImage()}
	return sb
}

// assertPreserved checks the resources existing before the attempt and
// the committed snapshot ref survive cleanup unchanged.
func assertPreserved(t *testing.T, f *Factory, snapRef types.WorkspaceRef, netID, ctrID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := f.c.api.ImageInspect(ctx, string(snapRef)); err != nil {
		t.Errorf("committed snapshot ref removed: %v", err)
	}
	if _, err := f.c.api.NetworkInspect(ctx, netID, client.NetworkInspectOptions{}); err != nil {
		t.Errorf("unrelated network removed: %v", err)
	}
	insp, err := f.c.api.ContainerInspect(ctx, ctrID, client.ContainerInspectOptions{})
	if err != nil {
		t.Errorf("unrelated container removed: %v", err)
		return
	}
	if !insp.Container.State.Running {
		t.Error("unrelated container no longer running")
	}
}

// assertInventoryClean asserts none of the exact resources the attempt
// created survives its cleanup.
func assertInventoryClean(t *testing.T, f *Factory, tr *sandboxTransport) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for id := range tr.createdContainers() {
		if _, err := f.c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{}); err == nil {
			t.Errorf("leaked workload/anchor container %s", id)
		}
	}
	for _, id := range tr.createdNetworks() {
		if _, err := f.c.api.NetworkInspect(ctx, id, client.NetworkInspectOptions{}); err == nil {
			t.Errorf("leaked private network %s", id)
		}
	}
}

// testPolicySetupFailureCleanup arms exactly one daemon failure at one
// setup boundary per case and requires Open to return no workspace
// while the exact-ID inventory of the attempt stays empty and unrelated
// resources and committed snapshots remain.
func testPolicySetupFailureCleanup(t *testing.T) {
	f := newConformanceFactory(t)
	ctx := context.Background()

	// A committed snapshot ref distinguishes the workload create (from
	// the ref) from a trusted anchor create (from the base image).
	src, err := f.Sandbox(t).Open(ctx, "", types.SandboxPolicy{Lifecycle: types.PerCall})
	if err != nil {
		t.Fatalf("open snapshot source: %v", err)
	}
	snapRef, err := src.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := src.Close(ctx); err != nil {
		t.Fatalf("close snapshot source: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = f.c.api.ImageRemove(cctx, string(snapRef), client.ImageRemoveOptions{})
	})

	presNet, err := f.c.api.NetworkCreate(ctx, fmt.Sprintf("gohan-fx-preserve-%d", time.Now().UnixNano()),
		client.NetworkCreateOptions{Labels: map[string]string{"gohan-fx-preserve": "true"}})
	if err != nil {
		t.Fatalf("create preservation network: %v", err)
	}
	presCtr, err := f.c.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  f.c.BaseImage(),
			Cmd:    []string{"sleep", "infinity"},
			Labels: map[string]string{"gohan-fx-preserve": "true"},
		},
	})
	if err != nil {
		t.Fatalf("create preservation container: %v", err)
	}
	if _, err := f.c.api.ContainerStart(ctx, presCtr.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("start preservation container: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = f.c.api.ContainerRemove(cctx, presCtr.ID, client.ContainerRemoveOptions{Force: true})
		_, _ = f.c.api.NetworkRemove(cctx, presNet.ID, client.NetworkRemoveOptions{})
	})

	tr := newSandboxTransport()
	sb := decoratedSandbox(t, f, tr)
	base := f.c.BaseImage()

	// matchCreateFrom matches a container create for image; matchStart
	// matches the start of a container created from image.
	matchCreateFrom := func(image string) func(*sandboxTransport, string, string, []byte) bool {
		return func(_ *sandboxTransport, method, path string, body []byte) bool {
			if method != "POST" || path != "/containers/create" {
				return false
			}
			var req struct {
				Image string `json:"Image"`
			}
			_ = json.Unmarshal(body, &req)
			return req.Image == image
		}
	}
	matchStartOf := func(image string) func(*sandboxTransport, string, string, []byte) bool {
		// Runs under the transport lock held by RoundTrip, so the
		// created-container map is read without re-locking.
		return func(tr2 *sandboxTransport, method, path string, _ []byte) bool {
			if method != "POST" {
				return false
			}
			m := startPathRE.FindStringSubmatch(path)
			return m != nil && tr2.containers[m[1]] == image
		}
	}

	p := types.SandboxPolicy{Lifecycle: types.PerCall}
	p.Egress.Allow = []string{"api.example.com:443"}

	cases := []struct {
		name  string
		point string
		match func(*sandboxTransport, string, string, []byte) bool
	}{
		{"create_network", "create network", func(_ *sandboxTransport, method, path string, _ []byte) bool {
			return method == "POST" && path == "/networks/create"
		}},
		{"create_anchor", "create anchor", matchCreateFrom(base)},
		{"start_anchor", "start anchor", matchStartOf(base)},
		{"policy_install", "policy install", func(_ *sandboxTransport, method, path string, _ []byte) bool {
			return method == "POST" && strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/exec")
		}},
		{"create_workload", "create workload", matchCreateFrom(string(snapRef))},
		{"start_workload", "start workload", matchStartOf(string(snapRef))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr.arm(tc.point, tc.match)
			w, err := sb.Open(ctx, snapRef, p)
			t.Cleanup(func() {
				if w != nil {
					_ = w.Close(context.Background())
				}
			})
			if hits := tr.hits(tc.point); hits != 1 {
				t.Fatalf("armed fault %q consumed %d times, want exactly once; open returned workspace=%v err=%v", tc.point, hits, w, err)
			}
			if err == nil {
				t.Fatalf("open with armed %q fault returned a usable workspace", tc.point)
			}
			if w != nil {
				t.Fatalf("open with armed %q fault returned workspace %s, want nil", tc.point, w.(*workspace).containerID)
			}
			assertInventoryClean(t, f, tr)
			assertPreserved(t, f, snapRef, presNet.ID, presCtr.ID)
		})
	}
}

// testPolicySetupAdminTimeout proves the ten-second administration bound
// is independent of Open's own context: a slow administration call makes
// the trusted setup fail closed and unwind every created resource.
func testPolicySetupAdminTimeout(t *testing.T) {
	f := newConformanceFactory(t)
	tr := newSandboxTransport()
	sb := decoratedSandbox(t, f, tr)
	tr.armDelay("slow_admin", 12*time.Second, func(_ *sandboxTransport, method, path string, _ []byte) bool {
		return method == "POST" && strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/exec")
	})
	p := types.SandboxPolicy{Lifecycle: types.PerCall}
	p.Egress.Allow = []string{"example.com:80"}
	w, err := sb.Open(context.Background(), "", p)
	if hits := tr.hits("slow_admin"); hits != 1 {
		t.Fatalf("slow administration call consumed %d times, want exactly once", hits)
	}
	if err == nil || w != nil {
		t.Fatalf("open past the administration bound returned workspace=%v err=%v, want nil workspace and an error", w, err)
	}
	assertInventoryClean(t, f, tr)
}

// testPolicySetupOpenCancelled proves a cancelled Open context fails the
// trusted setup and that the unwind still removes every created resource
// under its own fresh bounded contexts.
func testPolicySetupOpenCancelled(t *testing.T) {
	f := newConformanceFactory(t)
	tr := newSandboxTransport()
	sb := decoratedSandbox(t, f, tr)
	tr.armDelay("slow_admin", 15*time.Second, func(_ *sandboxTransport, method, path string, _ []byte) bool {
		return method == "POST" && strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/exec")
	})
	p := types.SandboxPolicy{Lifecycle: types.PerCall}
	p.Egress.Allow = []string{"example.com:80"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	w, err := sb.Open(ctx, "", p)
	if hits := tr.hits("slow_admin"); hits != 1 {
		t.Fatalf("slow administration call consumed %d times, want exactly once", hits)
	}
	if err == nil || w != nil {
		t.Fatalf("cancelled open returned workspace=%v err=%v, want nil workspace and an error", w, err)
	}
	assertInventoryClean(t, f, tr)
}

// testRestoredStartupPolicyFirst builds malicious restored content
// through public Put/Exec and Snapshot, replaces the idle startup
// executable and adds an inherited healthcheck through Docker commit
// configuration alone, then opens the snapshot through public
// Sandbox.Open: trusted policy must complete before the first workload
// start, inherited entrypoint and healthcheck markers must never
// execute, and fresh and restored UID/file semantics must stay equal.
func testRestoredStartupPolicyFirst(t *testing.T) {
	f := newConformanceFactory(t)
	f.NetworkFixture(t)
	ctx := context.Background()
	p := types.SandboxPolicy{Lifecycle: types.PerCall}
	p.Egress.Allow = []string{"api.example.com:443"}

	// Malicious content goes in through the public API only.
	src, err := f.Sandbox(t).Open(ctx, "", p)
	if err != nil {
		t.Fatalf("open source workspace: %v", err)
	}
	t.Cleanup(func() { _ = src.Close(ctx) })
	if err := src.Put(ctx, "/data/note", strings.NewReader("gohan-note")); err != nil {
		t.Fatalf("put note: %v", err)
	}
	idle := "#!/bin/sh\n" +
		"if wget -q -T 2 -O /dev/null http://other.example.com:443/; then : > /markers/contacted; fi\n" +
		": > /markers/entrypoint\n" +
		"exec \"$@\"\n"
	if err := src.Put(ctx, "/bad/idle", strings.NewReader(idle)); err != nil {
		t.Fatalf("put idle executable: %v", err)
	}
	if res := mustExec(t, src, 10*time.Second, "chmod", "755", "/bad/idle"); res.ExitCode != 0 {
		t.Fatalf("chmod idle executable: exit %d stderr %s", res.ExitCode, res.Stderr)
	}
	uidFresh := mustExec(t, src, 10*time.Second, "sh", "-c", "stat -c %u /data/note")
	if uidFresh.ExitCode != 0 {
		t.Fatalf("stat fresh note: exit %d stderr %s", uidFresh.ExitCode, uidFresh.Stderr)
	}
	if err := src.Put(ctx, "/markers/.keep", strings.NewReader("")); err != nil {
		t.Fatalf("put markers dir: %v", err)
	}

	// Positive execution controls: each marker command really creates
	// its marker file before absence can mean anything. The control
	// runs in the source workspace; the markers are removed before the
	// snapshot so the restored image starts without them.
	if res := mustExec(t, src, 10*time.Second, "/bad/idle", "true"); res.ExitCode != 0 {
		t.Fatalf("entrypoint control failed: exit %d stderr %s", res.ExitCode, res.Stderr)
	}
	if res := mustExec(t, src, 10*time.Second, "sh", "-c", ": > /markers/healthcheck"); res.ExitCode != 0 {
		t.Fatalf("healthcheck control failed: exit %d stderr %s", res.ExitCode, res.Stderr)
	}
	for _, marker := range []string{"/markers/entrypoint", "/markers/healthcheck"} {
		if res := mustExec(t, src, 10*time.Second, "test", "-f", marker); res.ExitCode != 0 {
			t.Fatalf("marker control %s did not appear", marker)
		}
	}
	if res := mustExec(t, src, 10*time.Second, "sh", "-c", "rm -f /markers/entrypoint /markers/healthcheck /markers/contacted"); res.ExitCode != 0 {
		t.Fatalf("clean markers: exit %d stderr %s", res.ExitCode, res.Stderr)
	}

	// Snapshot through the public API, then add only what the public
	// API cannot express: an inherited entrypoint and a one-second
	// healthcheck, via Docker commit configuration.
	snapRef, err := src.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	tag := fmt.Sprintf("gohan-fx-mal-%d", time.Now().UnixNano())
	if _, err := f.c.api.ContainerCommit(ctx, src.(*workspace).containerID, client.ContainerCommitOptions{
		Reference: tag,
		NoPause:   true,
		Config: &container.Config{
			Entrypoint: []string{"/bad/idle"},
			Healthcheck: &container.HealthConfig{
				Test:     []string{"CMD-SHELL", ": > /markers/healthcheck"},
				Interval: time.Second,
			},
		},
	}); err != nil {
		t.Fatalf("commit malicious startup config: %v", err)
	}
	malRef := types.WorkspaceRef(tag)
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = f.c.api.ImageRemove(cctx, string(snapRef), client.ImageRemoveOptions{})
		_, _ = f.c.api.ImageRemove(cctx, string(malRef), client.ImageRemoveOptions{})
	})

	tr := newSandboxTransport()
	rsb := decoratedSandbox(t, f, tr)
	base := f.c.BaseImage()

	w, err := rsb.Open(ctx, malRef, p)
	if err != nil {
		t.Fatalf("open restored workspace: %v", err)
	}
	t.Cleanup(func() { _ = w.Close(ctx) })
	workloadID := w.(*workspace).containerID

	// The trusted anchor is created from the base image, never from the
	// snapshot, and is a separate filesystem from the workload.
	var anchorID string
	for id, img := range tr.createdContainers() {
		if img == base {
			anchorID = id
		}
	}
	if anchorID == "" {
		t.Fatalf("no trusted anchor container created from base image %s during restored open; created containers: %v", base, tr.createdContainers())
	}
	if anchorID == workloadID {
		t.Fatal("anchor and workload share one container; the anchor must have a separate filesystem")
	}

	// Trusted policy completion must precede the first workload start.
	startIdx := tr.startIndexOf(workloadID)
	execIdx := tr.lastExecIndex()
	if startIdx < 0 {
		t.Fatalf("no start request for restored workload %s", workloadID)
	}
	if execIdx >= 0 && execIdx > startIdx {
		t.Fatalf("policy activity at log %d followed the workload start at %d; trusted setup must complete before the first workload start", execIdx, startIdx)
	}

	// Three seconds at a one-second interval would run three inherited
	// healthchecks; none of the inherited markers may exist, which also
	// proves the malicious entrypoint never contacted the denied peer.
	time.Sleep(3 * time.Second)
	for _, marker := range []string{"/markers/entrypoint", "/markers/healthcheck", "/markers/contacted"} {
		res := mustExec(t, w, 10*time.Second, "test", "-f", marker)
		if res.ExitCode == 0 {
			t.Fatalf("inherited startup artifact executed: %s exists", marker)
		}
	}

	// Positive startup control: the restored workload process runs and
	// answers the public exec surface.
	insp, err := f.c.api.ContainerInspect(ctx, workloadID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("inspect restored workload: %v", err)
	}
	if !insp.Container.State.Running {
		t.Fatal("restored workload container not running")
	}
	if res := mustExec(t, w, 10*time.Second, "sh", "-c", ": > /markers/legitimate; echo ok"); res.ExitCode != 0 {
		t.Fatalf("restored workload startup probe failed: exit %d stderr %s", res.ExitCode, res.Stderr)
	}
	if res := mustExec(t, w, 10*time.Second, "test", "-f", "/markers/legitimate"); res.ExitCode != 0 {
		t.Fatal("legitimate workload marker missing after startup probe")
	}

	// Fresh and restored UID/file semantics stay unchanged.
	uidRestored := mustExec(t, w, 10*time.Second, "sh", "-c", "stat -c %u /data/note")
	if uidRestored.ExitCode != 0 {
		t.Fatalf("stat restored note: exit %d stderr %s", uidRestored.ExitCode, uidRestored.Stderr)
	}
	if strings.TrimSpace(string(uidRestored.Stdout)) != strings.TrimSpace(string(uidFresh.Stdout)) {
		t.Fatalf("restored note uid %q differs from fresh %q", strings.TrimSpace(string(uidRestored.Stdout)), strings.TrimSpace(string(uidFresh.Stdout)))
	}
	rc, err := w.Get(ctx, "/data/note")
	if err != nil {
		t.Fatalf("get restored note: %v", err)
	}
	note, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("read restored note: %v", err)
	}
	if string(note) != "gohan-note" {
		t.Fatalf("restored note content %q, want gohan-note", note)
	}

	// Deny-all creates no anchor and keeps NetworkMode none.
	tr.reset()
	dw, err := rsb.Open(ctx, malRef, types.SandboxPolicy{Lifecycle: types.PerCall})
	if err != nil {
		t.Fatalf("open deny-all restored workspace: %v", err)
	}
	t.Cleanup(func() { _ = dw.Close(ctx) })
	for id := range tr.createdContainers() {
		if tr.containers[id] == base {
			t.Fatalf("deny-all open created an anchor container %s from base image", id)
		}
	}
	tr.mu.Lock()
	netCreates := 0
	for _, e := range tr.log {
		if e == "POST /networks/create" {
			netCreates++
		}
	}
	tr.mu.Unlock()
	if netCreates != 0 {
		t.Fatalf("deny-all open created %d egress networks, want none", netCreates)
	}
	dinsp, err := f.c.api.ContainerInspect(ctx, dw.(*workspace).containerID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("inspect deny-all workload: %v", err)
	}
	if dinsp.Container.HostConfig == nil || string(dinsp.Container.HostConfig.NetworkMode) != "none" {
		t.Fatalf("deny-all workload network mode %v, want none", dinsp.Container.HostConfig.NetworkMode)
	}
}
