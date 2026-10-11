package docker

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/core/types"
)

const snapshotPayload = "snapshot retention payload\n"

func snapshotWorkspace(t *testing.T) (*Sandbox, types.Workspace, types.WorkspaceRef) {
	t.Helper()
	sb := newTestSandbox(t)
	w, err := sb.Open(context.Background(), "", types.SandboxPolicy{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close(context.Background()) })
	if err := w.Put(context.Background(), "/tmp/keeper.txt", strings.NewReader(snapshotPayload)); err != nil {
		t.Fatalf("put: %v", err)
	}
	ref, err := w.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return sb, w, ref
}

func readKeeper(t *testing.T, w types.Workspace) string {
	t.Helper()
	r, err := w.Get(context.Background(), "/tmp/keeper.txt")
	if err != nil {
		t.Fatalf("get keeper: %v", err)
	}
	body, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatalf("read keeper: %v", err)
	}
	return string(body)
}

func TestSnapshotCommit(t *testing.T) {
	sb, _, ref := snapshotWorkspace(t)

	if !strings.HasPrefix(string(ref), snapshotPrefix+":") {
		t.Fatalf("ref = %q, want the deterministic %s prefix", ref, snapshotPrefix)
	}
	insp, err := sb.c.api.ImageInspect(context.Background(), string(ref))
	if err != nil {
		t.Fatalf("inspect %s: %v", ref, err)
	}
	if insp.Config.Labels[LabelSnapshot] != "true" || insp.Config.Labels[LabelSandbox] != "true" {
		t.Fatalf("snapshot labels = %v, want the sandbox and snapshot labels", insp.Config.Labels)
	}
}

func TestSnapshotSurvivesClose(t *testing.T) {
	sb, w, ref := snapshotWorkspace(t)

	if err := w.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	restored, err := sb.Open(context.Background(), ref, types.SandboxPolicy{})
	if err != nil {
		t.Fatalf("open from ref after close: %v", err)
	}
	t.Cleanup(func() { _ = restored.Close(context.Background()) })
	if got := readKeeper(t, restored); got != snapshotPayload {
		t.Fatalf("restored file = %q, want %q", got, snapshotPayload)
	}

	restartDaemonIfRequested(t, sb)
	if os.Getenv("GOHAN_DOCKER_TEST_RESTART") == "" {
		return
	}
	after, err := sb.Open(context.Background(), ref, types.SandboxPolicy{})
	if err != nil {
		t.Fatalf("open from ref after daemon restart: %v", err)
	}
	t.Cleanup(func() { _ = after.Close(context.Background()) })
	if got := readKeeper(t, after); got != snapshotPayload {
		t.Fatalf("file after daemon restart = %q, want %q", got, snapshotPayload)
	}
}

// restartDaemonIfRequested records the daemon-restart observation of D5
// when GOHAN_DOCKER_TEST_RESTART is set: the committed image lives in the
// daemon's image store, so it survives the restart and still restores.
// The restart touches the shared daemon, so it never runs by default.
func restartDaemonIfRequested(t *testing.T, sb *Sandbox) {
	t.Helper()
	if os.Getenv("GOHAN_DOCKER_TEST_RESTART") == "" {
		return
	}
	_ = sb.c.Close()
	cmd := exec.Command("timeout", "60", "systemctl", "restart", "docker")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("restart docker daemon: %v: %s", err, out)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if err := sb.c.Handshake(context.Background()); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not come back after restart")
		}
		time.Sleep(time.Second)
	}
}

func TestSnapshotMissing(t *testing.T) {
	sb := newTestSandbox(t)

	const missing = "gohan-snap:no0such0image"
	w, err := sb.Open(context.Background(), missing, types.SandboxPolicy{})
	if err == nil {
		_ = w.Close(context.Background())
		t.Fatal("open on a missing snapshot ref: want error")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing-snapshot error does not name the ref: %v", err)
	}
}

func TestSnapshotRetention(t *testing.T) {
	sb, w, ref := snapshotWorkspace(t)

	fresh, err := sb.Open(context.Background(), ref, types.SandboxPolicy{})
	if err != nil {
		t.Fatalf("open from ref: %v", err)
	}
	t.Cleanup(func() { _ = fresh.Close(context.Background()) })
	second, err := fresh.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if second == ref {
		t.Fatalf("second snapshot ref = %q, want a distinct commit", second)
	}

	// Closing the container removes nothing: retention is explicit.
	if err := w.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	for _, want := range []string{string(ref), string(second)} {
		images, err := sb.c.api.ImageList(context.Background(),
			imageListByRef(want))
		if err != nil || len(images.Items) == 0 {
			t.Fatalf("snapshot %s not retained after close: err %v", want, err)
		}
	}

	// An explicit ImageRemove removes exactly the named snapshot. The
	// container using the image is closed first, as a reclaim would.
	if err := fresh.Close(context.Background()); err != nil {
		t.Fatalf("close fresh: %v", err)
	}
	if _, err := sb.c.api.ImageRemove(context.Background(), string(ref),
		client.ImageRemoveOptions{}); err != nil {
		t.Fatalf("remove %s: %v", ref, err)
	}
	if images, err := sb.c.api.ImageList(context.Background(),
		imageListByRef(string(ref))); err != nil || len(images.Items) != 0 {
		t.Fatalf("removed snapshot still listed: err %v", err)
	}
	if images, err := sb.c.api.ImageList(context.Background(),
		imageListByRef(string(second))); err != nil || len(images.Items) == 0 {
		t.Fatalf("sibling snapshot wrongly removed: err %v", err)
	}
	if _, err := sb.c.api.ImageRemove(context.Background(), string(second),
		client.ImageRemoveOptions{}); err != nil {
		t.Fatalf("cleanup remove %s: %v", second, err)
	}
}
