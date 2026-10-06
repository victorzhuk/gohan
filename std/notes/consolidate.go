package notes

import (
	"context"
	"encoding/json"
	"fmt"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/flow"
)

// AuditMemoryConsolidate marks the Done consolidation record on the audit
// trail.
const AuditMemoryConsolidate stores.AuditKind = "memory_consolidate"

// Consolidation carries the wiring the Done hook needs: the stack and
// profile the one governed Extract runs under, the stores it reads and
// writes, and the policy the flow declared.
type Consolidation struct {
	Stack    *gohan.Stack
	Profile  string
	Store    stores.NotesStore
	Sessions stores.SessionLog
	Audit    stores.AuditLog
	Policy   stores.MemoryPolicy
}

// Run is the privileged session-to-subject write: one Extract over the
// session's log at Done, its value stored through the memory store so the
// policy schema and eviction rules judge it, and one memory_consolidate
// audit record naming the source session. Compaction never reaches this
// path, so it writes nothing. A failure returns to the caller for
// recording; it never fails the run.
func (c Consolidation) Run(ctx context.Context, ri types.RunInfo) error {
	if c.Stack == nil || c.Store == nil || c.Sessions == nil {
		return fmt.Errorf("memory consolidation: incomplete wiring")
	}
	mem, ok := c.Store.(stores.MemoryStore)
	if !ok {
		return fmt.Errorf("memory consolidation: %w", types.ErrMemoryStoreRequired)
	}
	hist, err := c.Sessions.Load(ctx, ri.SessionID)
	if err != nil {
		return fmt.Errorf("memory consolidation: %w", err)
	}
	blocks := documentBlocks(hist.Messages)
	if len(blocks) == 0 {
		return nil
	}
	var opts []flow.Option
	if c.Policy.Schema != nil {
		opts = append(opts, flow.WithSchema(c.Policy.Schema))
	}
	value, err := flow.Extract[json.RawMessage](c.Stack, c.Profile, opts...).Invoke(ctx, blocks)
	if err != nil {
		return fmt.Errorf("memory consolidation: %w", err)
	}
	key := memoryKey(ri)
	if err := mem.Put(ctx, key, stores.MemoryEntry{ID: "consolidated-" + ri.RunID, Value: value, RunID: ri.RunID}); err != nil {
		return fmt.Errorf("memory consolidation: %w", err)
	}
	if c.Audit != nil {
		rec := stores.AuditRecord{
			Kind:      AuditMemoryConsolidate,
			SessionID: ri.SessionID,
			RunID:     ri.RunID,
			Tenant:    key.Tenant,
			Subject:   key.Subject,
		}
		if err := c.Audit.Append(ctx, rec); err != nil {
			return fmt.Errorf("memory consolidation: audit %s: %w", AuditMemoryConsolidate, err)
		}
	}
	return nil
}

// documentBlocks flattens the session log into the document the recipe
// summarises; compaction markers carry no content of their own.
func documentBlocks(msgs []types.Message) []types.Block {
	var out []types.Block
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if _, isCompaction := b.(types.Compaction); !isCompaction {
				out = append(out, b)
			}
		}
	}
	return out
}
