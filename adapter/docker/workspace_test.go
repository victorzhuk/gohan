package docker

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/core/types"
)

func TestExec(t *testing.T) {
	ctx := context.Background()
	w := newTestWorkspace(t)

	res, err := w.Exec(ctx, types.ExecRequest{Argv: []string{"echo", "hello"}})
	if err != nil {
		t.Fatalf("echo: %v", err)
	}
	if res.ExitCode != 0 || string(res.Stdout) != "hello\n" || len(res.Stderr) != 0 {
		t.Fatalf("echo result = %+v", res)
	}
	if res.Seconds <= 0 {
		t.Fatalf("echo Seconds = %v, want positive", res.Seconds)
	}

	res, err = w.Exec(ctx, types.ExecRequest{Argv: []string{"sh", "-c", "echo boom >&2; exit 3"}})
	if err != nil {
		t.Fatalf("non-zero exit: %v", err)
	}
	if res.ExitCode != 3 || string(res.Stderr) != "boom\n" {
		t.Fatalf("non-zero exit result = %+v", res)
	}

	res, err = w.Exec(ctx, types.ExecRequest{Argv: []string{"cat"}, Stdin: []byte("piped")})
	if err != nil {
		t.Fatalf("stdin exec: %v", err)
	}
	if res.ExitCode != 0 || string(res.Stdout) != "piped" {
		t.Fatalf("stdin result = %+v", res)
	}

	res, err = w.Exec(ctx, types.ExecRequest{Argv: []string{"sh", "-c", "pwd"}, Cwd: "/etc"})
	if err != nil {
		t.Fatalf("cwd exec: %v", err)
	}
	if res.ExitCode != 0 || string(res.Stdout) != "/etc\n" {
		t.Fatalf("cwd result = %+v", res)
	}

	if _, err := w.Exec(ctx, types.ExecRequest{Argv: []string{"/no/such/binary"}}); err == nil {
		t.Fatal("start failure: want error")
	}
}

func TestSetEnv(t *testing.T) {
	ctx := context.Background()
	w := newTestWorkspace(t)

	if err := w.SetEnv(ctx, map[string]string{"GOHAN_TEST_VAR": "injected"}); err != nil {
		t.Fatalf("set env: %v", err)
	}
	res, err := w.Exec(ctx, types.ExecRequest{Argv: []string{"sh", "-c", "echo $GOHAN_TEST_VAR"}})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if string(res.Stdout) != "injected\n" {
		t.Fatalf("injected env = %q, want %q", res.Stdout, "injected\n")
	}
}

func TestPutGet(t *testing.T) {
	ctx := context.Background()
	w := newTestWorkspace(t)

	const data = "put/get roundtrip\n"
	if err := w.Put(ctx, "/tmp/nested/dir/file.txt", bytes.NewBufferString(data)); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := w.Get(ctx, "/tmp/nested/dir/file.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, err := io.ReadAll(got)
	_ = got.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != data {
		t.Fatalf("got %q, want %q", body, data)
	}

	res, err := w.Exec(ctx, types.ExecRequest{Argv: []string{"cat", "/tmp/nested/dir/file.txt"}})
	if err != nil {
		t.Fatalf("cat: %v", err)
	}
	if string(res.Stdout) != data {
		t.Fatalf("cat out = %q, want %q", res.Stdout, data)
	}

	if _, err := w.Get(ctx, "/tmp/does-not-exist"); err == nil {
		t.Fatal("get missing file: want error")
	}
}

func TestClose(t *testing.T) {
	ctx := context.Background()
	sb := newTestSandbox(t)
	w, err := sb.Open(ctx, "", types.SandboxPolicy{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ws := w.(*workspace)
	if err := w.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := ws.api.ContainerInspect(ctx, ws.containerID, client.ContainerInspectOptions{}); err == nil {
		t.Fatalf("container %s still present after Close", ws.containerID)
	}
	if _, err := w.Exec(ctx, types.ExecRequest{Argv: []string{"true"}}); err == nil {
		t.Fatal("exec after Close: want error")
	}
}

func newTestWorkspace(t *testing.T) *workspace {
	t.Helper()
	sb := newTestSandbox(t)
	w, err := sb.Open(context.Background(), "", types.SandboxPolicy{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close(context.Background()) })
	return w.(*workspace)
}
