package gohan

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// RecordQuarantined appends a quarantined extraction to the session
// history: one ToolResult block per distinct origin in q.Origins, whose
// BlockBase.Origin is that origin and whose Content is one Text block of
// the canonical JSON of q.Value. The call path, not the recipe, sets
// provenance, so the blocks enter the session's untrusted window and a
// later argument containing the extracted substring is tainted.
func RecordQuarantined[Out any](ctx context.Context, s *Stack, sessionID string, q types.Quarantined[Out]) error {
	if s.stores.SessionLog == nil {
		return fmt.Errorf("record quarantine: session log is not wired: %w", types.ErrSessionForbidden)
	}
	hist, err := s.stores.SessionLog.Load(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("record quarantine: load: %w", err)
	}
	raw, err := json.Marshal(q.Value)
	if err != nil {
		return fmt.Errorf("record quarantine: encode value: %w", err)
	}
	blocks := make([]types.Block, 0, len(q.Origins))
	seen := make(map[types.Origin]bool, len(q.Origins))
	for _, o := range q.Origins {
		if seen[o] {
			continue
		}
		seen[o] = true
		blocks = append(blocks, types.ToolResult{
			BlockBase: types.BlockBase{Origin: o},
			Content:   []types.Block{types.Text{Text: string(raw)}},
			Outcome:   types.Succeeded,
		})
	}
	msg := types.Message{Role: types.RoleUser, Blocks: blocks}
	if _, err := s.stores.SessionLog.Append(ctx, sessionID, hist.Version, msg); err != nil {
		return fmt.Errorf("record quarantine: append: %w", err)
	}
	return nil
}
