package docker

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/core/types"
)

// snapshotPrefix recognises legacy gohan-snap tags. New snapshots are
// published as untagged immutable image IDs.
const snapshotPrefix = "gohan-snap"

// Snapshot commits the container to a labelled image with NoPause and
// returns the committed image ID: the publication is immutable content,
// never a mutable tag (D5). A snapshot image is retained until an
// explicit remove; nothing removes it on a timer.
func (w *workspace) Snapshot(ctx context.Context) (types.WorkspaceRef, error) {
	res, err := w.api.ContainerCommit(ctx, w.containerID, client.ContainerCommitOptions{
		NoPause: true,
		Config: &container.Config{
			Labels: map[string]string{
				LabelSandbox:  "true",
				LabelSnapshot: "true",
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("docker: snapshot container %s: %w", w.containerID, err)
	}
	if res.ID == "" {
		return "", fmt.Errorf("docker: snapshot container %s: empty commit ID", w.containerID)
	}
	return types.WorkspaceRef(res.ID), nil
}
