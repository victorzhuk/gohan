package sandboxtest

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

var (
	errEmptyArgv      = errString("gohan: empty argv")
	errUnknownCommand = errString("gohan: unknown command")
)

type errString string

func (e errString) Error() string { return string(e) }

const (
	dialMarker  = "__dial__"
	sleepMarker = "__sleep__"
	echoMarker  = "__echo__"
)

type testRecorder struct {
	mu     sync.Mutex
	denied int
	kinds  []stores.AuditKind
}

func newTestRecorder() *testRecorder { return &testRecorder{} }

func (r *testRecorder) record(k stores.AuditKind) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.denied++
	r.kinds = append(r.kinds, k)
}

func (r *testRecorder) Denied() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.denied
}

func (r *testRecorder) Kinds() []stores.AuditKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]stores.AuditKind(nil), r.kinds...)
}

func (r *testRecorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.denied = 0
	r.kinds = nil
}

type fakeProbe struct{}

func (fakeProbe) Dial(host string, port int) []string {
	return []string{dialMarker, host, strconv.Itoa(port)}
}

func (fakeProbe) Sleep(d time.Duration) []string {
	return []string{sleepMarker, d.String()}
}

func (fakeProbe) Echo(s string) []string {
	return []string{echoMarker, s}
}

type fakeSandbox struct {
	rec   *testRecorder
	opens int
	mu    sync.Mutex
}

func (s *fakeSandbox) Open(_ context.Context, _ types.WorkspaceRef, p types.SandboxPolicy) (types.Workspace, error) {
	s.mu.Lock()
	s.opens++
	s.mu.Unlock()
	return &fakeWorkspace{policy: p, rec: s.rec, files: map[string][]byte{}}, nil
}

type fakeWorkspace struct {
	policy types.SandboxPolicy
	rec    *testRecorder

	mu     sync.Mutex
	execs  int
	closes int
	files  map[string][]byte
}

func (w *fakeWorkspace) Exec(ctx context.Context, req types.ExecRequest) (types.ExecResult, error) {
	w.mu.Lock()
	w.execs++
	w.mu.Unlock()
	if len(req.Argv) == 0 {
		return types.ExecResult{}, errEmptyArgv
	}
	switch req.Argv[0] {
	case dialMarker:
		return w.dial(req.Argv[1]), nil
	case sleepMarker:
		d, err := time.ParseDuration(req.Argv[1])
		if err != nil {
			return types.ExecResult{}, err
		}
		return w.sleep(ctx, d)
	case echoMarker:
		return types.ExecResult{ExitCode: 0, Stdout: []byte(req.Argv[1] + "\n")}, nil
	case "sh":
		if len(req.Argv) > 2 && req.Argv[1] == "-c" {
			return w.shell(ctx, req.Argv[2])
		}
	}
	return types.ExecResult{}, errUnknownCommand
}

func (w *fakeWorkspace) shell(ctx context.Context, code string) (types.ExecResult, error) {
	fields := strings.Fields(code)
	if len(fields) == 2 && fields[0] == sleepMarker {
		d, err := time.ParseDuration(fields[1])
		if err != nil {
			return types.ExecResult{}, err
		}
		return w.sleep(ctx, d)
	}
	if len(fields) >= 2 && fields[0] == echoMarker {
		return types.ExecResult{ExitCode: 0, Stdout: []byte(strings.Join(fields[1:], " ") + "\n")}, nil
	}
	return types.ExecResult{}, errUnknownCommand
}

func (w *fakeWorkspace) dial(host string) types.ExecResult {
	if w.denied(host) {
		w.rec.record(stores.AuditEgressDenied)
		return types.ExecResult{ExitCode: 1, Stderr: []byte("gohan: egress to " + host + " refused by policy")}
	}
	return types.ExecResult{ExitCode: 0, Stdout: []byte("connected\n")}
}

func (w *fakeWorkspace) denied(host string) bool {
	if ip := net.ParseIP(host); ip != nil && (ip.IsPrivate() || ip.IsLoopback()) {
		return w.policy.Egress.PrivateRanges == types.DenyPrivate
	}
	for _, allowed := range w.policy.Egress.Allow {
		if allowed == host {
			return false
		}
	}
	return true
}

func (w *fakeWorkspace) sleep(ctx context.Context, d time.Duration) (types.ExecResult, error) {
	if dl, ok := ctx.Deadline(); ok && dl.Before(time.Now().Add(d)) {
		<-ctx.Done()
		return types.ExecResult{}, ctx.Err()
	}
	if w.policy.MaxExecTime > 0 && w.policy.MaxExecTime < d {
		return types.ExecResult{ExitCode: 137, Stderr: []byte("gohan: killed: MaxExecTime exceeded")}, nil
	}
	time.Sleep(d)
	return types.ExecResult{ExitCode: 0}, nil
}

func (w *fakeWorkspace) Put(_ context.Context, path string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.files[path] = data
	return nil
}

func (w *fakeWorkspace) Get(_ context.Context, path string) (io.ReadCloser, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	data, ok := w.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (w *fakeWorkspace) SetEnv(_ context.Context, _ map[string]string) error { return nil }

func (w *fakeWorkspace) Snapshot(_ context.Context) (types.WorkspaceRef, error) {
	return types.WorkspaceRef(""), nil
}

func (w *fakeWorkspace) Close(_ context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closes++
	return nil
}

type fakeFactory struct {
	sb  *fakeSandbox
	rec *testRecorder
}

func newFakeFactory() *fakeFactory {
	rec := newTestRecorder()
	return &fakeFactory{sb: &fakeSandbox{rec: rec}, rec: rec}
}

func (f *fakeFactory) Sandbox(_ *testing.T) types.Sandbox { return f.sb }

func (f *fakeFactory) Probe() Probe { return fakeProbe{} }

func (f *fakeFactory) Recorder() Recorder { return f.rec }

// NetworkFixture is the fake's companion implementation: the fake
// enforces its policy from the dialed address, so a nominal RFC1918
// endpoint suffices and no listener is provisioned. Fake success stays
// suite-mechanics evidence, never provider evidence.
func (f *fakeFactory) NetworkFixture(_ *testing.T) NetworkFixture {
	return NetworkFixture{PrivateHost: "192.168.1.10", PrivatePort: 8080}
}

func TestFactorySeam(t *testing.T) {
	f := newFakeFactory()
	if f.Sandbox(t) == nil {
		t.Fatal("Sandbox() = nil")
	}
	probe := f.Probe()
	if probe == nil {
		t.Fatal("Probe() = nil")
	}
	for name, argv := range map[string][]string{
		"dial":  probe.Dial("api.example.com", 443),
		"sleep": probe.Sleep(time.Second),
		"echo":  probe.Echo("hi"),
	} {
		if len(argv) == 0 {
			t.Fatalf("%s probe returned no argv", name)
		}
	}
	rec := f.Recorder()
	if rec == nil {
		t.Fatal("Recorder() = nil")
	}
	f.rec.record(stores.AuditEgressDenied)
	if rec.Denied() != 1 {
		t.Fatalf("Denied() = %d, want 1", rec.Denied())
	}
	rec.Reset()
	if rec.Denied() != 0 || len(rec.Kinds()) != 0 {
		t.Fatalf("after Reset: denied = %d kinds = %v, want empty", rec.Denied(), rec.Kinds())
	}
}

func TestFakeAdapter(t *testing.T) {
	f := newFakeFactory()
	p := types.SandboxPolicy{Lifecycle: types.PerCall}
	w, err := f.sb.Open(context.Background(), types.WorkspaceRef(""), p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if f.sb.opens != 1 {
		t.Fatalf("opens = %d, want 1", f.sb.opens)
	}
	if err := w.SetEnv(context.Background(), map[string]string{"K": "v"}); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	if err := w.Put(context.Background(), "a.txt", strings.NewReader("body")); err != nil {
		t.Fatalf("put: %v", err)
	}
	r, err := w.Get(context.Background(), "a.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	data, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "body" {
		t.Fatalf("get = %q, want body", data)
	}

	w2, err := f.sb.Open(context.Background(), types.WorkspaceRef(""), types.SandboxPolicy{Lifecycle: types.PerCall})
	if err != nil {
		t.Fatalf("open deny-all: %v", err)
	}
	res, err := w2.Exec(context.Background(), types.ExecRequest{Argv: fakeProbe{}.Dial("api.example.com", 443)})
	if err != nil {
		t.Fatalf("dial exec: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatal("deny-all dial succeeded, want refusal")
	}
	if f.rec.Denied() != 1 {
		t.Fatalf("denied = %d, want 1", f.rec.Denied())
	}

	p.MaxExecTime = 50 * time.Millisecond
	w3, err := f.sb.Open(context.Background(), types.WorkspaceRef(""), p)
	if err != nil {
		t.Fatalf("open timeout: %v", err)
	}
	res, err = w3.Exec(context.Background(), types.ExecRequest{Argv: fakeProbe{}.Sleep(time.Second)})
	if err != nil {
		t.Fatalf("sleep exec: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatal("sleep past MaxExecTime exited zero, want kill")
	}
	if err := w3.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if w3.(*fakeWorkspace).closes != 1 {
		t.Fatalf("closes = %d, want 1", w3.(*fakeWorkspace).closes)
	}
}

func TestSuiteEntrypoint(t *testing.T) {
	Sandbox(t, newFakeFactory())
}
