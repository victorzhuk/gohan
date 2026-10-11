package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/core/types"
)

// Enumerate lists the durable sandbox resources by label (D7): every
// container carrying the sandbox label, snapshot or not, and every
// committed snapshot image. A dead pod's resources keep the labels, so
// the next pod enumerates them without the dead pod's process.
func (s *Sandbox) Enumerate(ctx context.Context) ([]types.WorkspaceRef, error) {
	refs := []types.WorkspaceRef{}
	containers, err := s.c.api.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", LabelSandbox+"=true"),
	})
	if err != nil {
		return nil, fmt.Errorf("docker: enumerate containers: %w", err)
	}
	for _, c := range containers.Items {
		refs = append(refs, types.WorkspaceRef(c.ID))
	}
	images, err := s.c.api.ImageList(ctx, client.ImageListOptions{
		Filters: client.Filters{}.Add("label", LabelSnapshot+"=true"),
	})
	if err != nil {
		return nil, fmt.Errorf("docker: enumerate snapshot images: %w", err)
	}
	for _, img := range images.Items {
		for _, tag := range img.RepoTags {
			if strings.HasPrefix(tag, snapshotPrefix+":") {
				refs = append(refs, types.WorkspaceRef(tag))
				break
			}
		}
	}
	return refs, nil
}

// CloseByRef removes one durable resource by identity (D7): a snapshot
// image through ImageRemove, a container through a label-checked
// ContainerRemove. Only labelled resources are reachable; a live handle
// stays usable until its owner closes it by the same explicit call.
func (s *Sandbox) CloseByRef(ctx context.Context, ref types.WorkspaceRef) error {
	r := string(ref)
	if strings.HasPrefix(r, snapshotPrefix+":") {
		if _, err := s.c.api.ImageRemove(ctx, r, client.ImageRemoveOptions{}); err != nil {
			return fmt.Errorf("docker: close snapshot %s: %w", r, err)
		}
		return nil
	}
	found, err := s.c.api.ContainerList(ctx, client.ContainerListOptions{
		All: true,
		Filters: client.Filters{}.Add("id", r).
			Add("label", LabelSandbox+"=true"),
	})
	if err != nil {
		return fmt.Errorf("docker: close %s: %w", r, err)
	}
	if len(found.Items) == 0 {
		return fmt.Errorf("docker: close %s: no labelled sandbox resource", r)
	}
	if _, err := s.c.api.ContainerRemove(ctx, r, client.ContainerRemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	}); err != nil {
		return fmt.Errorf("docker: close container %s: %w", r, err)
	}
	return nil
}
