package gohan

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// restoreWorkspace validates a checkpoint's workspace ref against the
// session's durable metadata before the run re-drives, under the restore
// rules of the workspace lifecycle design. It performs no write: equal
// metadata issues no store call, an older checkpoint leaves the stored
// snapshot, and the coordinator's Acquire applies the fresh open only when
// the session holds no durable metadata and the checkpoint carries no ref.
//
// A checkpoint newer than the stored metadata means the durable
// publication that should have preceded the save never landed, and the
// restore fails. A generation-zero checkpoint ref is a legacy pointer a
// pre-change run wrote; importing it is an explicit ImportWorkspaceMeta
// call, never an implicit one inside Resume or Recover.
func (s *Stack) restoreWorkspace(ctx context.Context, sessionID string, cp *stores.Checkpoint) error {
	if s.sandboxHandles == nil || s.stores.SessionLog == nil {
		return nil
	}
	meta, ok := s.stores.SessionLog.(stores.SessionWorkspaceMeta)
	if !ok {
		return nil
	}
	stored, has, err := meta.WorkspaceMeta(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("restore workspace: %w", err)
	}
	var cpRef types.WorkspaceRef
	var cpGen int64
	if cp != nil {
		cpRef, cpGen = cp.Workspace, cp.WorkspaceGeneration
	}
	switch {
	case cpRef == "":
		// An empty checkpoint ref never discards a stored snapshot,
		// whatever its generation: the stored record is the newer one.
		return nil
	case !has:
		if cpGen == 0 {
			return fmt.Errorf("restore workspace: legacy ref %q requires an explicit ImportWorkspaceMeta call", cpRef)
		}
		return fmt.Errorf("restore workspace: checkpoint generation %d has no stored metadata: %w", cpGen, types.ErrVersionConflict)
	case stored.Generation > cpGen:
		return nil
	case stored.Generation < cpGen:
		return fmt.Errorf("restore workspace: checkpoint generation %d newer than stored %d: %w", cpGen, stored.Generation, types.ErrVersionConflict)
	case stored.Ref != cpRef:
		return fmt.Errorf("restore workspace: stored ref %q differs from checkpoint ref %q: %w", stored.Ref, cpRef, types.ErrVersionConflict)
	}
	return nil
}
