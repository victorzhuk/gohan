package docker

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/core/types"
)

// Enumerate lists the durable sandbox resources by label (D7): every
// committed snapshot image, canonical encoded refs for workload-role
// containers, raw container IDs for legacy containers without the
// workload role, raw anchor IDs for anchors with no existing workload
// owner and network IDs for empty owned networks. A dead pod's
// resources keep the labels, so the next pod enumerates them without
// the dead pod's process.
func (s *Sandbox) Enumerate(ctx context.Context) ([]types.WorkspaceRef, error) {
	refs := []types.WorkspaceRef{}
	containers, err := s.c.api.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", LabelSandbox+"=true"),
	})
	if err != nil {
		return nil, fmt.Errorf("docker: enumerate containers: %w", err)
	}
	liveOwners := map[string]bool{}
	for _, c := range containers.Items {
		if c.Labels[labelRole] == roleWorkload {
			liveOwners[c.Labels[labelOwner]] = true
		}
	}
	for _, c := range containers.Items {
		role := c.Labels[labelRole]
		switch role {
		case roleWorkload:
			refs = append(refs, encodeWorkloadRef(c.ID, c.Labels[labelOwner]))
		case roleAnchor:
			// An anchor serving an existing running or stopped
			// workload is never orphan-removable.
			if liveOwners[c.Labels[labelOwner]] {
				continue
			}
			refs = append(refs, types.WorkspaceRef(c.ID))
		case "":
			refs = append(refs, types.WorkspaceRef(c.ID))
		}
	}
	images, err := s.c.api.ImageList(ctx, client.ImageListOptions{
		Filters: client.Filters{}.Add("label", LabelSnapshot+"=true"),
	})
	if err != nil {
		return nil, fmt.Errorf("docker: enumerate snapshot images: %w", err)
	}
	// One ref per image: a legacy gohan-snap tag keeps its tag, an
	// untagged immutable image is emitted by its ID, never both.
	for _, img := range images.Items {
		ref := img.ID
		for _, tag := range img.RepoTags {
			if strings.HasPrefix(tag, snapshotPrefix+":") {
				ref = tag
				break
			}
		}
		refs = append(refs, types.WorkspaceRef(ref))
	}
	networks, err := s.ownedNetworks(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range networks {
		empty, err := s.networkEmpty(ctx, n.ID)
		if err != nil {
			return nil, err
		}
		if empty {
			refs = append(refs, types.WorkspaceRef(n.ID))
		}
	}
	return refs, nil
}

// networkEmpty reports whether the network has no attached containers.
// A network that disappeared concurrently counts as empty.
func (s *Sandbox) networkEmpty(ctx context.Context, id string) (bool, error) {
	insp, err := s.c.api.NetworkInspect(ctx, id, client.NetworkInspectOptions{})
	if err != nil {
		if isNotFound(err) {
			return true, nil
		}
		return false, fmt.Errorf("docker: inspect network %s: %w", id, err)
	}
	return len(insp.Network.Containers) == 0, nil
}

// ownedNetworks lists the labelled private networks the adapter created.
func (s *Sandbox) ownedNetworks(ctx context.Context) ([]network.Summary, error) {
	networks, err := s.c.api.NetworkList(ctx, client.NetworkListOptions{
		Filters: client.Filters{}.Add("label", labelRole+"="+roleNetwork),
	})
	if err != nil {
		return nil, fmt.Errorf("docker: enumerate networks: %w", err)
	}
	return networks.Items, nil
}

// encodeWorkloadRef builds the versioned identity from a container ID
// and its immutable owner name.
func encodeWorkloadRef(id, owner string) types.WorkspaceRef {
	return types.WorkspaceRef(workloadRefPrefix + id + ":" + owner)
}

// ownerNamePattern matches the frozen ownership seam newOwnerName
// mints: the "gohan-workload-" prefix over exactly 32 lowercase hex
// digits.
var ownerNamePattern = regexp.MustCompile(`^gohan-workload-[0-9a-f]{32}$`)

// parseWorkloadRef strictly decodes the versioned encoding. It never
// reinterprets another ref shape as a workload identity: the container
// ID must be a full 64-lowercase-hex Docker ID and the owner must
// match the frozen ownership seam.
func parseWorkloadRef(ref string) (id, owner string, ok bool) {
	rest, found := strings.CutPrefix(ref, workloadRefPrefix)
	if !found {
		return "", "", false
	}
	id, owner, found = strings.Cut(rest, ":")
	if !found || !isContainerID(id) || !ownerNamePattern.MatchString(owner) {
		return "", "", false
	}
	return id, owner, true
}

// isContainerID reports whether s is a full Docker container or network
// ID.
func isContainerID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// CloseByRef removes one durable resource by identity (D7): a snapshot
// image through ImageRemove, an encoded workload ref through exact group
// cleanup, a raw legacy container ID through a label-checked removal, a
// raw orphan anchor ID through a non-forced reclaim with its empty
// network, or a raw empty owned network ID. Only labelled resources are
// reachable; malformed identities, contradicting owners and raw IDs for
// workload-role containers are refused before any destructive API call.
func (s *Sandbox) CloseByRef(ctx context.Context, ref types.WorkspaceRef) error {
	r := string(ref)
	if strings.HasPrefix(r, snapshotPrefix+":") || isContainerID(r) || strings.HasPrefix(r, "sha256:") {
		handled, err := s.closeSnapshotImage(ctx, r)
		if handled || err != nil {
			return err
		}
	}
	if id, owner, ok := parseWorkloadRef(r); ok {
		return s.closeWorkloadGroup(ctx, id, owner)
	}
	if isContainerID(r) {
		return s.closeRawResource(ctx, r)
	}
	return fmt.Errorf("docker: close %s: malformed workload reference", r)
}

// closeSnapshotImage removes one labelled snapshot image: a legacy
// gohan-snap tag by that tag, an untagged immutable image by its ID.
// Both sandbox and snapshot labels are required; the legacy prefix or a
// bare ID never grants removal authority, so an unlabelled lookalike is
// refused before any destructive call. The removal is never forced, so
// Docker keeps an in-use image with its live user.
func (s *Sandbox) closeSnapshotImage(ctx context.Context, r string) (bool, error) {
	insp, err := s.c.api.ImageInspect(ctx, r)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("docker: close snapshot %s: %w", r, err)
	}
	if insp.Config.Labels[LabelSandbox] != "true" || insp.Config.Labels[LabelSnapshot] != "true" {
		return true, fmt.Errorf("docker: close %s: image lacks the sandbox and snapshot labels", r)
	}
	target := r
	if !strings.HasPrefix(r, snapshotPrefix+":") {
		target = insp.ID
	}
	if _, err := s.c.api.ImageRemove(ctx, target, client.ImageRemoveOptions{}); err != nil {
		return true, fmt.Errorf("docker: close snapshot %s: %w", r, err)
	}
	return true, nil
}

// closeWorkloadGroup removes the workload, its associated anchor and the
// private network selected by the encoded owner name, then reports
// success only when every selected resource is absent. Discovery or
// removal failures return as errors so the durable record stays
// retryable.
func (s *Sandbox) closeWorkloadGroup(ctx context.Context, workloadID, owner string) error {
	containers, err := s.c.api.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", LabelSandbox+"=true"),
	})
	if err != nil {
		return fmt.Errorf("docker: close group %s: list containers: %w", owner, err)
	}
	anchorID := ""
	workloadPresent := false
	var removals []error
	for _, c := range containers.Items {
		switch {
		case c.Labels[labelRole] == roleWorkload && c.Labels[labelOwner] == owner:
			if c.ID != workloadID {
				return fmt.Errorf("docker: close group %s: extant workload %s contradicts encoded identity %s", owner, c.ID, workloadID)
			}
			workloadPresent = true
		case c.Labels[labelRole] == roleAnchor && c.Labels[labelOwner] == owner:
			anchorID = c.ID
		case c.ID == workloadID:
			return fmt.Errorf("docker: close group %s: workload %s carries owner %q, want %s", workloadID, c.ID, c.Labels[labelOwner], owner)
		}
	}
	if workloadPresent {
		if err := removeContainer(ctx, s.c.api, workloadID, true); err != nil {
			removals = append(removals, err)
		}
	} else {
		// The encoded identity may name the workload only after the
		// labelled scan matched it or the container is already absent;
		// an unlabelled foreign container is never force-removed.
		if _, err := s.c.api.ContainerInspect(ctx, workloadID, client.ContainerInspectOptions{}); err == nil {
			return fmt.Errorf("docker: close group %s: workload %s exists without the sandbox workload role and owner; refusing removal", owner, workloadID)
		} else if !isNotFound(err) {
			removals = append(removals, fmt.Errorf("docker: close group %s: inspect workload %s: %w", owner, workloadID, err))
		}
	}
	if anchorID != "" {
		if err := removeContainer(ctx, s.c.api, anchorID, true); err != nil {
			removals = append(removals, err)
		}
	}
	networks, err := s.ownedNetworks(ctx)
	if err != nil {
		removals = append(removals, err)
	}
	for _, n := range networks {
		if n.Labels[labelOwner] != owner {
			continue
		}
		if err := removeNetwork(ctx, s.c.api, n.ID); err != nil {
			removals = append(removals, err)
		}
	}
	if err := s.verifyGroupAbsent(ctx, workloadID, anchorID, owner); err != nil {
		removals = append(removals, err)
	}
	return errors.Join(removals...)
}

// verifyGroupAbsent reports success only when the whole selected group
// is gone.
func (s *Sandbox) verifyGroupAbsent(ctx context.Context, workloadID, anchorID, owner string) error {
	var errs []error
	for _, id := range []string{workloadID, anchorID} {
		if id == "" {
			continue
		}
		_, err := s.c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
		switch {
		case err == nil:
			errs = append(errs, fmt.Errorf("docker: close group %s: container %s still present", owner, id))
		case isNotFound(err):
		default:
			errs = append(errs, fmt.Errorf("docker: close group %s: inspect %s: %w", owner, id, err))
		}
	}
	networks, err := s.ownedNetworks(ctx)
	if err != nil {
		errs = append(errs, err)
	}
	for _, n := range networks {
		if n.Labels[labelOwner] == owner {
			errs = append(errs, fmt.Errorf("docker: close group %s: network %s still present", owner, n.ID))
		}
	}
	return errors.Join(errs...)
}

// closeRawResource routes a raw 64-hex ID: legacy containers without the
// workload role, unassociated anchors with their empty private network,
// or empty owned networks. Anything else is refused without a
// destructive call.
func (s *Sandbox) closeRawResource(ctx context.Context, id string) error {
	found, err := s.c.api.ContainerList(ctx, client.ContainerListOptions{
		All: true,
		Filters: client.Filters{}.Add("id", id).
			Add("label", LabelSandbox+"=true"),
	})
	if err != nil {
		return fmt.Errorf("docker: close %s: %w", id, err)
	}
	if len(found.Items) == 0 {
		networks, nerr := s.ownedNetworks(ctx)
		if nerr != nil {
			return nerr
		}
		for _, n := range networks {
			if n.ID == id {
				empty, eerr := s.networkEmpty(ctx, id)
				if eerr != nil {
					return eerr
				}
				if !empty {
					return fmt.Errorf("docker: close network %s: network still has attached containers", id)
				}
				if err := removeNetwork(ctx, s.c.api, id); err != nil {
					return err
				}
				if _, err := s.c.api.NetworkInspect(ctx, id, client.NetworkInspectOptions{}); err == nil {
					return fmt.Errorf("docker: close network %s: network still present", id)
				}
				return nil
			}
		}
		return fmt.Errorf("docker: close %s: no labelled sandbox resource", id)
	}
	labels := found.Items[0].Labels
	switch labels[labelRole] {
	case roleWorkload:
		return fmt.Errorf("docker: close %s: raw IDs are refused for workload-role containers; use the encoded resource ref", id)
	case roleAnchor:
		owner := labels[labelOwner]
		serves, err := s.ownerHasWorkload(ctx, owner)
		if err != nil {
			return err
		}
		if serves {
			return fmt.Errorf("docker: close anchor %s: anchor serves existing workload owner %s", id, owner)
		}
		// Non-forced: a concurrent start prevents the removal.
		if err := removeContainer(ctx, s.c.api, id, false); err != nil {
			// A running anchor refuses the non-forced removal; a
			// leaked running orphan is reclaimable only after the
			// owner still serves no workload, then stop and remove.
			insp, ierr := s.c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
			if ierr != nil || !insp.Container.State.Running {
				return err
			}
			serves, werr := s.ownerHasWorkload(ctx, owner)
			if werr != nil {
				return werr
			}
			if serves {
				return fmt.Errorf("docker: close anchor %s: anchor serves existing workload owner %s", id, owner)
			}
			sctx, scancel := context.WithTimeout(context.Background(), adminTimeout)
			if _, err := s.c.api.ContainerStop(sctx, id, client.ContainerStopOptions{}); err != nil && !isNotFound(err) {
				scancel()
				return fmt.Errorf("docker: close anchor %s: stop: %w", id, err)
			}
			scancel()
			if err := removeContainer(ctx, s.c.api, id, true); err != nil {
				return err
			}
		}
		networks, err := s.ownedNetworks(ctx)
		if err != nil {
			return err
		}
		for _, n := range networks {
			if n.Labels[labelOwner] != owner {
				continue
			}
			empty, eerr := s.networkEmpty(ctx, n.ID)
			if eerr != nil {
				return eerr
			}
			if !empty {
				continue
			}
			if err := removeNetwork(ctx, s.c.api, n.ID); err != nil {
				return err
			}
		}
		if _, err := s.c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{}); err == nil {
			return fmt.Errorf("docker: close anchor %s: anchor still present", id)
		}
		return nil
	}
	if _, err := s.c.api.ContainerRemove(ctx, id, client.ContainerRemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	}); err != nil {
		return fmt.Errorf("docker: close container %s: %w", id, err)
	}
	return nil
}

// ownerHasWorkload reports whether a labelled workload container with
// this owner exists.
func (s *Sandbox) ownerHasWorkload(ctx context.Context, owner string) (bool, error) {
	containers, err := s.c.api.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", LabelSandbox+"=true"),
	})
	if err != nil {
		return false, fmt.Errorf("docker: list containers for owner %s: %w", owner, err)
	}
	for _, c := range containers.Items {
		if c.Labels[labelRole] == roleWorkload && c.Labels[labelOwner] == owner {
			return true, nil
		}
	}
	return false, nil
}

// removeContainer removes one container; an already absent container is
// idempotent success.
func removeContainer(ctx context.Context, api *client.Client, id string, force bool) error {
	if id == "" {
		return nil
	}
	if _, err := api.ContainerRemove(ctx, id, client.ContainerRemoveOptions{
		Force:         force,
		RemoveVolumes: true,
	}); err != nil && !isNotFound(err) {
		return fmt.Errorf("docker: remove container %s: %w", id, err)
	}
	return nil
}

// removeNetwork removes one network by name or ID; an already absent
// network is idempotent success.
func removeNetwork(ctx context.Context, api *client.Client, name string) error {
	if name == "" || name == "none" {
		return nil
	}
	if _, err := api.NetworkRemove(ctx, name, client.NetworkRemoveOptions{}); err != nil && !isNotFound(err) {
		return fmt.Errorf("docker: remove network %s: %w", name, err)
	}
	return nil
}

// isNotFound reports daemon not-found responses.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "no such") ||
		strings.Contains(strings.ToLower(err.Error()), "not found")
}
