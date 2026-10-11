package sandbox

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

// persistWorkspace keeps its files in a map the snapshot backend stores by
// ref, so a closed workspace's snapshot still restores on a later Open.
type persistWorkspace struct {
	mu       sync.Mutex
	files    map[string]string
	snapshot types.WorkspaceRef
	exec     types.ExecResult
	execErr  error
	putErr   error
	listable bool
	closes   int
}

func (w *persistWorkspace) Exec(_ context.Context, _ types.ExecRequest) (types.ExecResult, error) {
	return w.exec, w.execErr
}

func (w *persistWorkspace) Put(_ context.Context, path string, r io.Reader) error {
	if w.putErr != nil {
		return w.putErr
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.files[path] = string(data)
	return nil
}

func (w *persistWorkspace) Get(_ context.Context, path string) (io.ReadCloser, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	body, ok := w.files[path]
	if !ok {
		return nil, errors.New("file not found: " + path)
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func (w *persistWorkspace) SetEnv(_ context.Context, env map[string]string) error { return nil }

func (w *persistWorkspace) Snapshot(_ context.Context) (types.WorkspaceRef, error) {
	return w.snapshot, nil
}

func (w *persistWorkspace) Close(_ context.Context) error {
	w.closes++
	return nil
}

func (w *persistWorkspace) List(_ context.Context, _ string) ([]types.WorkspaceEntry, error) {
	if !w.listable {
		return nil, types.ErrWorkspaceListingRequired
	}
	return nil, nil
}

// persistSandbox restores a workspace's files from the snapshot ref Open
// receives, or starts empty when the ref is empty. The backend outlives
// every close.
type persistSandbox struct {
	mu      sync.Mutex
	backend map[types.WorkspaceRef]map[string]string
	seq     int
	opens   int
	refs    []types.WorkspaceRef
	custom  func(*persistWorkspace)
}

func newPersistSandbox() *persistSandbox {
	return &persistSandbox{backend: make(map[types.WorkspaceRef]map[string]string)}
}

func (s *persistSandbox) Open(_ context.Context, ref types.WorkspaceRef, _ types.SandboxPolicy) (types.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opens++
	s.seq++
	s.refs = append(s.refs, ref)
	snap := types.WorkspaceRef(string(rune('a'+s.seq)) + "-snap")
	var files map[string]string
	if ref != "" {
		files = s.backend[ref]
	}
	if files == nil {
		files = make(map[string]string)
	}
	w := &persistWorkspace{files: files, snapshot: snap}
	if s.custom != nil {
		s.custom(w)
	}
	s.backend[snap] = files
	return w, nil
}

func driveCtx(h types.SandboxHandles, owner types.WorkspaceOwner) (context.Context, *types.WorkspaceUse) {
	use := &types.WorkspaceUse{}
	ctx := types.WithRunInfo(context.Background(), types.RunInfo{SessionID: "sess-1", RunID: owner.RunID})
	ctx = types.WithSandboxHandles(ctx, h)
	ctx = types.WithWorkspaceOwner(ctx, owner)
	return types.WithWorkspaceUse(ctx, use), use
}

func sandboxTool(t *testing.T, ts ToolSet, name string) types.Tool {
	t.Helper()
	for _, tool := range ts.Tools() {
		if tool.Spec().Name == name {
			return tool
		}
	}
	t.Fatalf("tool %s missing", name)
	return nil
}

func runTool(t *testing.T, ts ToolSet, name string, ctx context.Context, argsv any) types.ToolResult {
	t.Helper()
	res, err := sandboxTool(t, ts, name).Call(ctx, args(t, argsv))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func newPerSessionSet(t *testing.T, sb types.Sandbox, meta *metaFake) (ToolSet, *Handles) {
	t.Helper()
	h, err := NewHandles(sb, types.SandboxPolicy{Lifecycle: types.PerSession}, meta)
	if err != nil {
		t.Fatalf("NewHandles: %v", err)
	}
	ts, err := New(sb, types.SandboxPolicy{Lifecycle: types.PerSession}, WithCoordinator(h))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ts, h
}

func TestPerSessionLifecycle(t *testing.T) {
	t.Run("call keeps the acquired handle open", func(t *testing.T) {
		psb := newPersistSandbox()
		ts, h := newPerSessionSet(t, psb, newMetaFake(ownerA(1)))
		ctx, use := driveCtx(h, ownerA(1))
		runTool(t, ts, "write_file", ctx, writeFileArgs{Path: "a.txt", Content: "hi"})
		if !use.Acquired() {
			t.Error("acquisition was not recorded")
		}
		psb.mu.Lock()
		opens := psb.opens
		psb.mu.Unlock()
		if opens != 1 {
			t.Errorf("opens = %d, want 1", opens)
		}
	})
	t.Run("same owner reuses the handle without a second open", func(t *testing.T) {
		psb := newPersistSandbox()
		ts, h := newPerSessionSet(t, psb, newMetaFake(ownerA(1)))
		ctx, _ := driveCtx(h, ownerA(1))
		runTool(t, ts, "write_file", ctx, writeFileArgs{Path: "a.txt", Content: "hi"})
		runTool(t, ts, "read_file", ctx, readFileArgs{Path: "a.txt"})
		psb.mu.Lock()
		opens := psb.opens
		psb.mu.Unlock()
		if opens != 1 {
			t.Errorf("opens = %d, want 1", opens)
		}
	})
	t.Run("no coordinator in ctx refuses", func(t *testing.T) {
		psb := newPersistSandbox()
		ts, err := New(psb, types.SandboxPolicy{Lifecycle: types.PerSession})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = ts.Tools()[0].Call(context.Background(), args(t, runCodeArgs{Code: "echo"}))
		if !errors.Is(err, types.ErrPerSessionUnsupported) {
			t.Fatalf("err = %v, want ErrPerSessionUnsupported", err)
		}
	})
	t.Run("read-only calls publish nothing", func(t *testing.T) {
		psb := newPersistSandbox()
		psb.custom = func(w *persistWorkspace) { w.listable = true }
		meta := newMetaFake(ownerA(1))
		ts, h := newPerSessionSet(t, psb, meta)
		ctx, _ := driveCtx(h, ownerA(1))
		runTool(t, ts, "write_file", ctx, writeFileArgs{Path: "a.txt", Content: "hi"})
		meta.mu.Lock()
		stored := len(meta.meta)
		meta.mu.Unlock()
		if stored != 1 {
			t.Fatalf("metadata count = %d, want 1 after write", stored)
		}
		runTool(t, ts, "list_files", ctx, listFilesArgs{Path: "."})
		meta.mu.Lock()
		stored = len(meta.meta)
		meta.mu.Unlock()
		if stored != 1 {
			t.Errorf("read-only call published, metadata count = %d, want 1", stored)
		}
	})
}

func TestPerSessionMutationPublish(t *testing.T) {
	t.Run("sandbox.mutation-published-before-complete", func(t *testing.T) {
		psb := newPersistSandbox()
		meta := newMetaFake(ownerA(1))
		meta.setErr = errors.New("storage exploded: adapter backend refused the write")
		ts, h := newPerSessionSet(t, psb, meta)
		ctx, _ := driveCtx(h, ownerA(1))
		tool := sandboxTool(t, ts, "write_file")
		_, err := tool.Call(ctx, args(t, writeFileArgs{Path: "a.txt", Content: "hi"}))
		var unknown types.UnknownOutcomeError
		if !errors.As(err, &unknown) {
			t.Fatalf("err = %v, want UnknownOutcomeError", err)
		}
	})
}

func TestPerSessionPartialWrite(t *testing.T) {
	t.Run("sandbox.partial-mutation-publishes", func(t *testing.T) {
		psb := newPersistSandbox()
		psb.custom = func(w *persistWorkspace) { w.putErr = errors.New("partial write: disk quota exceeded") }
		meta := newMetaFake(ownerA(1))
		ts, h := newPerSessionSet(t, psb, meta)
		ctx, _ := driveCtx(h, ownerA(1))
		tool := sandboxTool(t, ts, "write_file")
		if _, err := tool.Call(ctx, args(t, writeFileArgs{Path: "a.txt", Content: "hi"})); err == nil {
			t.Fatal("write_file succeeded, want a body error")
		}
		meta.mu.Lock()
		_, published := meta.meta["sess-1"]
		meta.mu.Unlock()
		if !published {
			t.Error("body error skipped the publication")
		}
	})
}

func TestPerSessionNonzeroExit(t *testing.T) {
	t.Run("sandbox.nonzero-exit-publishes", func(t *testing.T) {
		psb := newPersistSandbox()
		psb.custom = func(w *persistWorkspace) { w.exec = types.ExecResult{ExitCode: 3} }
		meta := newMetaFake(ownerA(1))
		ts, h := newPerSessionSet(t, psb, meta)
		ctx, _ := driveCtx(h, ownerA(1))
		tool := sandboxTool(t, ts, "run_code")
		if _, err := tool.Call(ctx, args(t, runCodeArgs{Code: "exit 3"})); err == nil {
			t.Fatal("run_code succeeded, want a nonzero exit error")
		}
		meta.mu.Lock()
		_, published := meta.meta["sess-1"]
		meta.mu.Unlock()
		if !published {
			t.Error("nonzero exit skipped the publication")
		}
	})
}

type testResolver struct{}

func (testResolver) Resolve(_ context.Context, _ types.Principal, name string) (string, error) {
	if name == "TOKEN" {
		return "s3cret-value", nil
	}
	return "", errors.New("unknown secret " + name)
}

func TestPerSessionPublishErrorSanitized(t *testing.T) {
	t.Run("sandbox.publication-error-sanitized", func(t *testing.T) {
		psb := newPersistSandbox()
		meta := newMetaFake(ownerA(1))
		meta.setErr = errors.New("adapter refused: leaked s3cret-value in the message")
		policy := types.SandboxPolicy{
			Lifecycle: types.PerSession,
			Secrets:   []types.SecretRef{{Name: "TOKEN", Env: "TOKEN"}},
		}
		h, err := NewHandles(psb, policy, meta)
		if err != nil {
			t.Fatalf("NewHandles: %v", err)
		}
		resolver := testResolver{}
		ts, err := New(psb, policy, WithCoordinator(h))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		ctx, _ := driveCtx(h, ownerA(1))
		ctx = types.WithSecrets(ctx, resolver)
		tool := sandboxTool(t, ts, "write_file")
		raw, rawErr := tool.Call(ctx, args(t, writeFileArgs{Path: "a.txt", Content: "hi"}))
		callUnknown, callErr := types.UnknownOutcomeError{}, rawErr
		_ = raw
		if !errors.As(callErr, &callUnknown) {
			t.Fatalf("err = %v, want UnknownOutcomeError", callErr)
		}
		if strings.Contains(callUnknown.Message, "s3cret-value") {
			t.Fatalf("recorded message carries the raw secret: %s", callUnknown.Message)
		}
	})
}
