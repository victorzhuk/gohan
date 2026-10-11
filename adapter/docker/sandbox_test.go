package docker

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestOpen(t *testing.T) {
	ctx := context.Background()
	sb := newTestSandbox(t)

	w, err := sb.Open(ctx, "", types.SandboxPolicy{})
	if err != nil {
		t.Fatalf("open fresh: %v", err)
	}
	res, err := w.Exec(ctx, types.ExecRequest{Argv: []string{"echo", "fresh"}})
	if err != nil {
		t.Fatalf("exec in fresh workspace: %v", err)
	}
	if res.ExitCode != 0 || string(res.Stdout) != "fresh\n" {
		t.Fatalf("fresh exec = %+v", res)
	}

	const data = "snapshot payload\n"
	if err := w.Put(ctx, "/tmp/keeper.txt", strings.NewReader(data)); err != nil {
		t.Fatalf("put: %v", err)
	}
	ref, err := w.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if ref == "" {
		t.Fatal("snapshot returned empty ref")
	}
	if err := w.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	restored, err := sb.Open(ctx, ref, types.SandboxPolicy{})
	if err != nil {
		t.Fatalf("open from ref %s: %v", ref, err)
	}
	defer restored.Close(ctx)
	got, err := restored.Get(ctx, "/tmp/keeper.txt")
	if err != nil {
		t.Fatalf("get from restored: %v", err)
	}
	body, err := io.ReadAll(got)
	_ = got.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(body, []byte(data)) {
		t.Fatalf("restored file = %q, want %q", body, data)
	}

	if _, err := sb.Open(ctx, "gohan-snap:missing0ref0image", types.SandboxPolicy{}); err == nil {
		t.Fatal("open with missing snapshot ref: want error")
	} else if !strings.Contains(err.Error(), "gohan-snap:missing0ref0image") {
		t.Fatalf("missing-snapshot error does not name the ref: %v", err)
	}
}
