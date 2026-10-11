package stores

import (
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// TestCheckpointWorkspaceGeneration pins the additive field: a checkpoint
// written before the field existed decodes to zero, older than every
// stored generation, and a written value is held.
func TestCheckpointWorkspaceGeneration(t *testing.T) {
	var legacy Checkpoint
	if legacy.WorkspaceGeneration != 0 {
		t.Fatalf("zero checkpoint generation = %d, want 0", legacy.WorkspaceGeneration)
	}

	c := Checkpoint{
		Workspace:           types.WorkspaceRef("ref-1"),
		WorkspaceGeneration: 3,
		ExpiresAt:           time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}
	if c.WorkspaceGeneration != 3 || c.Workspace != "ref-1" {
		t.Fatalf("checkpoint = %+v", c)
	}
}
