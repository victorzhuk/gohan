package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
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

	insp, err := sb.c.api.ImageInspect(context.Background(), string(ref))
	if err != nil {
		t.Fatalf("inspect %s: %v", ref, err)
	}
	if insp.ID != string(ref) {
		t.Fatalf("ref = %q, want the committed image ID %q", ref, insp.ID)
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
		if _, err := sb.c.api.ImageInspect(context.Background(), want); err != nil {
			t.Fatalf("snapshot %s not retained after close: %v", want, err)
		}
	}

	// An explicit ImageRemove is never forced: the parent snapshot ref
	// has a dependent child image (second was committed from a workspace
	// restored from ref), so Docker refuses the deletion and the sibling
	// stays intact. The container using the image is closed first, as a
	// reclaim would.
	if err := fresh.Close(context.Background()); err != nil {
		t.Fatalf("close fresh: %v", err)
	}
	if _, err := sb.c.api.ImageRemove(context.Background(), string(ref),
		client.ImageRemoveOptions{}); err == nil {
		t.Fatal("remove of a snapshot with a dependent child succeeded; want a non-forced conflict")
	}
	if _, err := sb.c.api.ImageInspect(context.Background(), string(second)); err != nil {
		t.Fatalf("sibling snapshot %s wrongly removed by the refused removal: %v", second, err)
	}
	// Removing the leaf first makes the parent removable: each snapshot
	// is removed exactly when explicitly named, never together.
	if _, err := sb.c.api.ImageRemove(context.Background(), string(second),
		client.ImageRemoveOptions{}); err != nil {
		t.Fatalf("remove %s: %v", second, err)
	}
	if _, err := sb.c.api.ImageInspect(context.Background(), string(second)); err == nil {
		t.Fatalf("removed snapshot %s still present", second)
	}
	if _, err := sb.c.api.ImageInspect(context.Background(), string(ref)); err != nil {
		t.Fatalf("parent snapshot %s removed by the child's removal: %v", ref, err)
	}
	if _, err := sb.c.api.ImageRemove(context.Background(), string(ref),
		client.ImageRemoveOptions{}); err != nil {
		t.Fatalf("remove %s: %v", ref, err)
	}
	if _, err := sb.c.api.ImageInspect(context.Background(), string(ref)); err == nil {
		t.Fatalf("removed snapshot %s still present", ref)
	}
}

// readFile reads one workspace file as a string.
func readFile(t *testing.T, w types.Workspace, path string) string {
	t.Helper()
	r, err := w.Get(context.Background(), path)
	if err != nil {
		t.Fatalf("get %s: %v", path, err)
	}
	body, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// labelledImageIDs lists every image carrying the snapshot label,
// including untagged ones.
func labelledImageIDs(t *testing.T, sb *Sandbox) map[string]bool {
	t.Helper()
	images, err := sb.c.api.ImageList(context.Background(), client.ImageListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", LabelSnapshot+"=true"),
	})
	if err != nil {
		t.Fatalf("image list: %v", err)
	}
	ids := map[string]bool{}
	for _, img := range images.Items {
		ids[img.ID] = true
	}
	return ids
}

func TestSandboxSnapshotIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("gohan: snapshot isolation run needs a live daemon; skipped in short mode")
	}
	ctx := context.Background()
	sb := newTestSandbox(t)

	t.Run("sandbox.same-workspace-snapshots-immutable", func(t *testing.T) {
		w, err := sb.Open(ctx, "", types.SandboxPolicy{})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if err := w.Put(ctx, "/tmp/data.txt", strings.NewReader("alpha-bytes\n")); err != nil {
			t.Fatalf("put A: %v", err)
		}
		refA, err := w.Snapshot(ctx)
		if err != nil {
			t.Fatalf("snapshot A: %v", err)
		}
		if err := w.Put(ctx, "/tmp/data.txt", strings.NewReader("beta-bytes\n")); err != nil {
			t.Fatalf("put B: %v", err)
		}
		refB, err := w.Snapshot(ctx)
		if err != nil {
			t.Fatalf("snapshot B: %v", err)
		}

		inspA, err := sb.c.api.ImageInspect(ctx, string(refA))
		if err != nil {
			t.Fatalf("inspect A: %v", err)
		}
		inspB, err := sb.c.api.ImageInspect(ctx, string(refB))
		if err != nil {
			t.Fatalf("inspect B: %v", err)
		}
		if string(refA) != inspA.ID {
			t.Fatalf("ref A = %q, want the committed image ID %q", refA, inspA.ID)
		}
		if string(refB) != inspB.ID {
			t.Fatalf("ref B = %q, want the committed image ID %q", refB, inspB.ID)
		}
		if refA == refB {
			t.Fatalf("refs A and B are equal (%s), want two immutable image identities", refA)
		}

		restA, err := sb.Open(ctx, refA, types.SandboxPolicy{})
		if err != nil {
			t.Fatalf("restore A: %v", err)
		}
		restB, err := sb.Open(ctx, refB, types.SandboxPolicy{})
		if err != nil {
			t.Fatalf("restore B: %v", err)
		}
		if got := readFile(t, restA, "/tmp/data.txt"); got != "alpha-bytes\n" {
			t.Fatalf("restored A = %q, want %q", got, "alpha-bytes\n")
		}
		if got := readFile(t, restB, "/tmp/data.txt"); got != "beta-bytes\n" {
			t.Fatalf("restored B = %q, want %q", got, "beta-bytes\n")
		}

		// Closing the source and both restores keeps the snapshots:
		// retention is explicit.
		for _, cw := range []types.Workspace{w, restA, restB} {
			if err := cw.Close(ctx); err != nil {
				t.Fatalf("close: %v", err)
			}
		}
		if _, err := sb.c.api.ImageInspect(ctx, string(refA)); err != nil {
			t.Fatalf("snapshot A removed by close: %v", err)
		}
		if _, err := sb.c.api.ImageInspect(ctx, string(refB)); err != nil {
			t.Fatalf("snapshot B removed by close: %v", err)
		}

		// Removing one snapshot preserves the independent sibling.
		if err := sb.CloseByRef(ctx, refA); err != nil {
			t.Fatalf("close snapshot A: %v", err)
		}
		if _, err := sb.c.api.ImageInspect(ctx, string(refA)); err == nil {
			t.Fatal("closed snapshot A still present")
		}
		if _, err := sb.c.api.ImageInspect(ctx, string(refB)); err != nil {
			t.Fatalf("sibling snapshot B wrongly removed: %v", err)
		}
		t.Cleanup(func() { _ = sb.CloseByRef(ctx, refB) })
	})

	t.Run("sandbox.legacy-snapshot-ref-readable", func(t *testing.T) {
		w, err := sb.Open(ctx, "", types.SandboxPolicy{})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = w.Close(ctx) })
		if err := w.Put(ctx, "/tmp/data.txt", strings.NewReader("legacy-bytes\n")); err != nil {
			t.Fatalf("put legacy: %v", err)
		}
		legacyTag := fmt.Sprintf("%s:legacy%d", snapshotPrefix, time.Now().UnixNano())
		if _, err := sb.c.api.ContainerCommit(ctx, w.(*workspace).containerID, client.ContainerCommitOptions{
			Reference: legacyTag,
			NoPause:   true,
			Config: &container.Config{
				Labels: map[string]string{
					LabelSandbox:  "true",
					LabelSnapshot: "true",
				},
			},
		}); err != nil {
			t.Fatalf("commit legacy tag: %v", err)
		}

		if err := w.Put(ctx, "/tmp/data.txt", strings.NewReader("immutable-bytes\n")); err != nil {
			t.Fatalf("put immutable: %v", err)
		}
		imgRef, err := w.Snapshot(ctx)
		if err != nil {
			t.Fatalf("snapshot immutable: %v", err)
		}
		inspImg, err := sb.c.api.ImageInspect(ctx, string(imgRef))
		if err != nil {
			t.Fatalf("inspect immutable: %v", err)
		}

		refs, err := sb.Enumerate(ctx)
		if err != nil {
			t.Fatalf("enumerate: %v", err)
		}
		count := func(want types.WorkspaceRef) int {
			n := 0
			for _, r := range refs {
				if r == want {
					n++
				}
			}
			return n
		}
		if count(types.WorkspaceRef(legacyTag)) != 1 {
			t.Fatalf("enumerate = %v, want the legacy ref %s exactly once", refs, legacyTag)
		}
		if count(imgRef) != 1 {
			t.Fatalf("enumerate = %v, want the immutable ref %s exactly once", refs, imgRef)
		}
		if len(inspImg.RepoTags) != 0 {
			t.Fatalf("immutable image carries tags %v, want none", inspImg.RepoTags)
		}

		// No migration or tag rewrite: the legacy tag keeps its target.
		inspLegacy, err := sb.c.api.ImageInspect(ctx, legacyTag)
		if err != nil {
			t.Fatalf("inspect legacy: %v", err)
		}
		if !slices.Contains(inspLegacy.RepoTags, legacyTag) {
			t.Fatalf("legacy tags = %v, want %s retained", inspLegacy.RepoTags, legacyTag)
		}

		restLegacy, err := sb.Open(ctx, types.WorkspaceRef(legacyTag), types.SandboxPolicy{})
		if err != nil {
			t.Fatalf("restore legacy: %v", err)
		}
		restImm, err := sb.Open(ctx, imgRef, types.SandboxPolicy{})
		if err != nil {
			t.Fatalf("restore immutable: %v", err)
		}
		if got := readFile(t, restLegacy, "/tmp/data.txt"); got != "legacy-bytes\n" {
			t.Fatalf("legacy restored = %q, want %q", got, "legacy-bytes\n")
		}
		if got := readFile(t, restImm, "/tmp/data.txt"); got != "immutable-bytes\n" {
			t.Fatalf("immutable restored = %q, want %q", got, "immutable-bytes\n")
		}
		if err := restLegacy.Close(ctx); err != nil {
			t.Fatalf("close legacy restore: %v", err)
		}
		if err := restImm.Close(ctx); err != nil {
			t.Fatalf("close immutable restore: %v", err)
		}

		// An unlabelled lookalike under the legacy prefix is refused
		// before any destructive call.
		lookTag := fmt.Sprintf("%s:lookalike%d", snapshotPrefix, time.Now().UnixNano())
		if _, err := sb.c.api.ContainerCommit(ctx, w.(*workspace).containerID, client.ContainerCommitOptions{
			Reference: lookTag,
			NoPause:   true,
		}); err != nil {
			t.Fatalf("commit lookalike: %v", err)
		}
		t.Cleanup(func() { _, _ = sb.c.api.ImageRemove(ctx, lookTag, client.ImageRemoveOptions{}) })
		if err := sb.CloseByRef(ctx, types.WorkspaceRef(lookTag)); err == nil {
			t.Fatal("close by unlabelled prefix lookalike: want error")
		}
		if _, err := sb.c.api.ImageInspect(ctx, lookTag); err != nil {
			t.Fatalf("lookalike %s removed by a prefix match: %v", lookTag, err)
		}

		// Removing one selected reference preserves the sibling snapshot
		// and the live workspace.
		if err := sb.CloseByRef(ctx, types.WorkspaceRef(legacyTag)); err != nil {
			t.Fatalf("close legacy ref: %v", err)
		}
		if _, err := sb.c.api.ImageInspect(ctx, legacyTag); err == nil {
			t.Fatal("closed legacy ref still present")
		}
		if _, err := sb.c.api.ImageInspect(ctx, string(imgRef)); err != nil {
			t.Fatalf("sibling immutable snapshot wrongly removed: %v", err)
		}
		if _, err := w.Exec(ctx, types.ExecRequest{Argv: []string{"true"}}); err != nil {
			t.Fatalf("live exec after legacy close: %v", err)
		}
		restImm2, err := sb.Open(ctx, imgRef, types.SandboxPolicy{})
		if err != nil {
			t.Fatalf("restore immutable after legacy close: %v", err)
		}
		if got := readFile(t, restImm2, "/tmp/data.txt"); got != "immutable-bytes\n" {
			t.Fatalf("immutable after legacy close = %q, want %q", got, "immutable-bytes\n")
		}
		if err := restImm2.Close(ctx); err != nil {
			t.Fatalf("close restore: %v", err)
		}
		t.Cleanup(func() { _ = sb.CloseByRef(ctx, imgRef) })
	})
}
