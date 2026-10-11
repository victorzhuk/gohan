package docker

import (
	"context"
	"slices"
	"testing"

	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/core/types"
)

var _ types.SandboxResources = (*Sandbox)(nil)

func imageListByRef(ref string) client.ImageListOptions {
	return client.ImageListOptions{Filters: client.Filters{}.Add("reference", ref)}
}

func TestEnumerateCloseByRef(t *testing.T) {
	ctx := context.Background()
	sb := newTestSandbox(t)

	// The live handle's container is enumerated but never closed by the
	// reclaim path while the handle works.
	live, err := sb.Open(ctx, "", types.SandboxPolicy{})
	if err != nil {
		t.Fatalf("open live: %v", err)
	}
	t.Cleanup(func() { _ = live.Close(ctx) })

	// A dead pod's container: labelled, its handle dropped without a
	// close, exactly what a vanished pod leaves behind.
	dead, err := sb.Open(ctx, "", types.SandboxPolicy{})
	if err != nil {
		t.Fatalf("open dead: %v", err)
	}
	deadID := dead.(*workspace).containerID
	t.Cleanup(func() { _ = sb.CloseByRef(ctx, types.WorkspaceRef(deadID)) })

	refs, err := sb.Enumerate(ctx)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	refSet := map[types.WorkspaceRef]bool{}
	for _, r := range refs {
		refSet[r] = true
	}
	if !refSet[types.WorkspaceRef(deadID)] {
		t.Fatalf("enumerate = %v, want the labelled orphan %s", refs, deadID)
	}

	if err := sb.CloseByRef(ctx, types.WorkspaceRef(deadID)); err != nil {
		t.Fatalf("close by ref %s: %v", deadID, err)
	}
	refs, err = sb.Enumerate(ctx)
	if err != nil {
		t.Fatalf("enumerate after close: %v", err)
	}
	if slices.Contains(refs, types.WorkspaceRef(deadID)) {
		t.Fatalf("closed ref %s still enumerated", deadID)
	}

	// The live handle is untouched and still usable.
	res, err := live.Exec(ctx, types.ExecRequest{Argv: []string{"echo", "alive"}})
	if err != nil || res.ExitCode != 0 || string(res.Stdout) != "alive\n" {
		t.Fatalf("live exec after reclaim: %+v err %v", res, err)
	}

	// A snapshot is closed by its ref too.
	ref, err := live.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	t.Cleanup(func() { _, _ = sb.c.api.ImageRemove(ctx, string(ref), client.ImageRemoveOptions{}) })
	refs, err = sb.Enumerate(ctx)
	if err != nil {
		t.Fatalf("enumerate with snapshot: %v", err)
	}
	if !slices.Contains(refs, ref) {
		t.Fatalf("enumerate = %v, want snapshot %s", refs, ref)
	}
	if err := sb.CloseByRef(ctx, ref); err != nil {
		t.Fatalf("close snapshot by ref: %v", err)
	}
	refs, err = sb.Enumerate(ctx)
	if err != nil {
		t.Fatalf("enumerate after snapshot close: %v", err)
	}
	if slices.Contains(refs, ref) {
		t.Fatalf("closed snapshot %s still enumerated", ref)
	}

	if _, err := live.Exec(ctx, types.ExecRequest{Argv: []string{"true"}}); err != nil {
		t.Fatalf("live exec after snapshot close: %v", err)
	}

	// A ref outside the labels is refused, not guessed.
	if err := sb.CloseByRef(ctx, "alpine:3.23"); err == nil {
		t.Fatal("close by unlabelled ref: want error")
	}
}
