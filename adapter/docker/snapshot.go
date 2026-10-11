package docker

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/core/types"
)

// snapshotPrefix tags committed snapshot images.
const snapshotPrefix = "gohan-snap"

// Snapshot commits the container to a labelled image with NoPause and
// returns the deterministic image reference (D5). The ref is opaque to the
// caller; the adapter owns the tag scheme. A snapshot image is retained
// until an explicit remove; nothing removes it on a timer.
func (w *workspace) Snapshot(ctx context.Context) (types.WorkspaceRef, error) {
	ref := snapshotRef(w.containerID)
	if _, err := w.api.ContainerCommit(ctx, w.containerID, client.ContainerCommitOptions{
		Reference: ref,
		NoPause:   true,
		Config: &container.Config{
			Labels: map[string]string{
				LabelSandbox:  "true",
				LabelSnapshot: "true",
			},
		},
	}); err != nil {
		return "", fmt.Errorf("docker: snapshot container %s: %w", w.containerID, err)
	}
	return types.WorkspaceRef(ref), nil
}

func snapshotRef(containerID string) string {
	id := containerID
	if len(id) > 12 {
		id = id[:12]
	}
	return fmt.Sprintf("%s:%s", snapshotPrefix, id)
}
