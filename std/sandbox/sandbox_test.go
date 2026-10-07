package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type fakeWorkspace struct {
	entries  []types.WorkspaceEntry
	listable bool
	execReqs []types.ExecRequest
	puts     []string
	reads    []string
	closes   int
	closeErr error
}

func (w *fakeWorkspace) Exec(ctx context.Context, req types.ExecRequest) (types.ExecResult, error) {
	w.execReqs = append(w.execReqs, req)
	return types.ExecResult{Stdout: []byte("hello\n")}, nil
}

func (w *fakeWorkspace) Put(ctx context.Context, path string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	w.puts = append(w.puts, string(data))
	return nil
}

func (w *fakeWorkspace) Get(ctx context.Context, path string) (io.ReadCloser, error) {
	w.reads = append(w.reads, path)
	return io.NopCloser(strings.NewReader("file body")), nil
}

func (w *fakeWorkspace) SetEnv(ctx context.Context, env map[string]string) error { return nil }

func (w *fakeWorkspace) Snapshot(ctx context.Context) (types.WorkspaceRef, error) {
	return types.WorkspaceRef("snap"), nil
}

func (w *fakeWorkspace) Close(ctx context.Context) error {
	w.closes++
	return w.closeErr
}

type fakeLister struct{ *fakeWorkspace }

func (w fakeLister) List(ctx context.Context, path string) ([]types.WorkspaceEntry, error) {
	return w.entries, nil
}

type fakeSandbox struct {
	ws     types.Workspace
	policy types.SandboxPolicy
	opens  int
	openWS types.WorkspaceRef
}

func (s *fakeSandbox) Open(ctx context.Context, ref types.WorkspaceRef, p types.SandboxPolicy) (types.Workspace, error) {
	s.opens++
	s.policy = p
	s.openWS = ref
	return s.ws, nil
}

func newFake(listable bool) (*fakeSandbox, *fakeWorkspace) {
	w := &fakeWorkspace{listable: listable}
	if listable {
		return &fakeSandbox{ws: fakeLister{w}}, w
	}
	return &fakeSandbox{ws: w}, w
}

func args(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}

func TestNewToolNames(t *testing.T) {
	if _, err := New(nil, types.SandboxPolicy{Lifecycle: types.PerCall}); !errors.Is(err, ErrNilSandbox) {
		t.Fatalf("nil sandbox err = %v, want ErrNilSandbox", err)
	}
	// The zero Lifecycle equals PerCall, so only an out-of-range value can trip ErrZeroLifecycle.
	if _, err := New(&fakeSandbox{}, types.SandboxPolicy{Lifecycle: types.Lifecycle(9)}); !errors.Is(err, ErrZeroLifecycle) {
		t.Fatalf("unknown lifecycle err = %v, want ErrZeroLifecycle", err)
	}
	sb, _ := newFake(false)
	ts, err := New(sb, types.SandboxPolicy{Lifecycle: types.PerCall})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tools := ts.Tools()
	if len(tools) != 4 {
		t.Fatalf("len(tools) = %d, want 4", len(tools))
	}
	want := []string{"run_code", "write_file", "read_file", "list_files"}
	for i, name := range want {
		if got := tools[i].Spec().Name; got != name {
			t.Errorf("tool %d = %q, want %q", i, got, name)
		}
	}
	checker, ok := ts.(interface{ Check([]string) error })
	if !ok {
		t.Fatal("ToolSet does not expose Check")
	}
	if err := checker.Check(want); err != nil {
		t.Errorf("Check(pinned): %v", err)
	}
	var drift types.ErrToolSetDrift
	if err := checker.Check([]string{"run_code"}); !errors.As(err, &drift) {
		t.Errorf("Check(renamed) err = %v, want set drift", err)
	}
}

func TestDeriveEffect(t *testing.T) {
	tests := []struct {
		name string
		p    types.SandboxPolicy
		want types.Effect
	}{
		{"per call no egress", types.SandboxPolicy{Lifecycle: types.PerCall}, types.ReadOnly},
		{"per session no egress", types.SandboxPolicy{Lifecycle: types.PerSession}, types.Idempotent},
		{"per call egress", types.SandboxPolicy{Lifecycle: types.PerCall, Egress: types.EgressPolicy{Allow: []string{"api.example.com"}}}, types.SideEffect},
		{"per session egress", types.SandboxPolicy{Lifecycle: types.PerSession, Egress: types.EgressPolicy{Allow: []string{"api.example.com"}}}, types.SideEffect},
	}
	for _, tt := range tests {
		if got := deriveEffect(tt.p); got != tt.want {
			t.Errorf("%s: deriveEffect = %v, want %v", tt.name, got, tt.want)
		}
	}
	sb, _ := newFake(false)
	ts, err := New(sb, types.SandboxPolicy{Lifecycle: types.PerSession})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tool := range ts.Tools() {
		if got := tool.Spec().Effect; got != types.Idempotent {
			t.Errorf("%s effect = %v, want idempotent", tool.Spec().Name, got)
		}
	}
}

func TestWithEffectTightenOnly(t *testing.T) {
	sb, _ := newFake(false)
	if _, err := New(sb, types.SandboxPolicy{Lifecycle: types.PerSession}, WithEffect(types.ReadOnly)); !errors.Is(err, ErrEffectLoosened) {
		t.Fatalf("loosening err = %v, want ErrEffectLoosened", err)
	} else if want := "derived idempotent, requested read_only"; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want it to name %q", err, want)
	}
	ts, err := New(sb, types.SandboxPolicy{Lifecycle: types.PerCall}, WithEffect(types.SideEffect))
	if err != nil {
		t.Fatalf("tighten New: %v", err)
	}
	for _, tool := range ts.Tools() {
		if got := tool.Spec().Effect; got != types.SideEffect {
			t.Errorf("%s effect = %v, want side effect", tool.Spec().Name, got)
		}
	}
}

func TestEgressPolicyPassedThrough(t *testing.T) {
	sb, _ := newFake(false)
	policy := types.SandboxPolicy{
		Lifecycle: types.PerCall,
		Egress:    types.EgressPolicy{Allow: []string{"api.example.com"}, Schemes: []string{"https"}, Timeout: 5 * time.Second},
	}
	ts, err := New(sb, policy)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tool := range ts.Tools() {
		eg := tool.Spec().Egress
		if eg == nil {
			t.Errorf("%s egress = nil, want the policy", tool.Spec().Name)
			continue
		}
		if len(eg.Allow) != 1 || eg.Allow[0] != "api.example.com" || len(eg.Schemes) != 1 || eg.Schemes[0] != "https" {
			t.Errorf("%s egress = %+v, want the policy hosts and schemes", tool.Spec().Name, eg)
		}
	}
}

func TestListFilesOptional(t *testing.T) {
	sb, w := newFake(false)
	ts, err := New(sb, types.SandboxPolicy{Lifecycle: types.PerCall})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tool := ts.Tools()[3]
	res, err := tool.Call(context.Background(), args(t, listFilesArgs{Path: "/"}))
	if err == nil {
		t.Fatal("list_files on a non-lister workspace succeeded")
	}
	if !errors.Is(err, types.ErrWorkspaceListingRequired) {
		t.Fatalf("err = %v, want ErrWorkspaceListingRequired", err)
	}
	if len(res.Content) != 0 {
		t.Errorf("content = %d blocks, want none", len(res.Content))
	}
	if w.closes != 1 {
		t.Errorf("closes = %d, want 1", w.closes)
	}
}

func TestListFiles(t *testing.T) {
	sb, w := newFake(true)
	w.entries = []types.WorkspaceEntry{
		{Path: "data.csv", Size: 12, MIME: "text/csv"},
		{Path: "out.bin", Size: 4096, MIME: "application/octet-stream"},
	}
	ts, err := New(sb, types.SandboxPolicy{Lifecycle: types.PerCall})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := ts.Tools()[3].Call(context.Background(), args(t, listFilesArgs{Path: "/"}))
	if err != nil {
		t.Fatalf("list_files: %v", err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("content = %d blocks, want 1", len(res.Content))
	}
	text, ok := res.Content[0].(types.Text)
	if !ok {
		t.Fatalf("content[0] = %T, want types.Text", res.Content[0])
	}
	for _, want := range []string{"data.csv", "12", "text/csv", "out.bin"} {
		if !strings.Contains(text.Text, want) {
			t.Errorf("listing missing %q in %q", want, text.Text)
		}
	}
	if w.closes != 1 {
		t.Errorf("closes = %d, want 1", w.closes)
	}
}
