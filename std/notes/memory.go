package notes

import (
	"context"
	"encoding/json"
	"fmt"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/guard"
)

// AllowUntrustedMemory opts a flow's memory writes out of the untrusted-turn
// denial the context guard applies.
func AllowUntrustedMemory() Option {
	return func(c *config) { c.allowUntrusted = true }
}

// memoryKey addresses one subject's memory. The run identity is the only
// source: a caller cannot name another tenant or subject.
func memoryKey(ri types.RunInfo) stores.NotesKey {
	return stores.NotesKey{
		Scope:   stores.ScopeSubject,
		Tenant:  ri.Principal.Tenant,
		Subject: ri.Principal.Subject,
	}
}

type memoryWriteArgs struct {
	ID    string          `json:"id" desc:"name for the memory entry"`
	Value json.RawMessage `json:"value" desc:"the JSON value to store"`
}

// windowOrigins returns the distinct block origins of the taint window,
// the writing turn's context.
func windowOrigins(ctx context.Context) []types.Origin {
	blocks, ok := types.TaintWindowFrom(ctx)
	if !ok {
		return nil
	}
	var out []types.Origin
	seen := map[types.Origin]bool{}
	for _, b := range blocks {
		o := b.BlockOrigin()
		if o == (types.Origin{}) || seen[o] {
			continue
		}
		seen[o] = true
		out = append(out, o)
	}
	return out
}

func untrustedWindow(blocks []types.Block) bool {
	for _, b := range blocks {
		switch b.BlockOrigin().Kind {
		case types.OriginTool, types.OriginProvider, types.OriginModel, types.OriginOperator:
			return true
		}
	}
	return false
}

func deniedWrite(msg string) types.ToolResult {
	return types.ToolResult{
		Content: []types.Block{types.Text{Text: msg}},
		Outcome: types.Failed,
		Error:   &types.ToolError{Kind: types.Permanent, Message: msg},
	}
}

func countWriteDenied(ctx context.Context, tenant string) {
	if tel := types.TelemetryFrom(ctx); tel != nil {
		tel.Count(ctx, "gohan.memory.write_denied", 1, types.String("tenant", tenant))
	}
}

// Memory registers the subject-memory tool pair over the store. Both tools
// key their entries from the run identity alone, so a run for one subject
// can neither read nor name another subject's memory.
func Memory(store stores.NotesStore, policy stores.MemoryPolicy, opts ...Option) (types.Tool, types.Tool, error) {
	mem, ok := store.(stores.MemoryStore)
	if !ok {
		return nil, nil, fmt.Errorf("notes.Memory: %w", types.ErrMemoryStoreRequired)
	}
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	g := guard.NewContextGuard()
	write, err := gohan.NewTool("memory_write",
		"Store one durable memory entry for the current subject",
		func(ctx context.Context, args memoryWriteArgs) (types.ToolResult, error) {
			ri, ok := types.RunInfoFrom(ctx)
			if !ok {
				return types.ToolResult{}, fmt.Errorf("memory_write: no run identity")
			}
			window, _ := types.TaintWindowFrom(ctx)
			d, err := g.Decide(ctx, guards.GuardInput{
				Stage:  types.StageContext,
				Origin: types.Origin{Kind: types.OriginTool, Name: "memory_write"},
				Blocks: []types.Block{types.Text{Text: string(args.Value)}},
				Run:    ri,
			})
			if err != nil {
				return types.ToolResult{}, fmt.Errorf("memory_write: guard: %w", err)
			}
			if d.Value.Action == guards.Block || (!cfg.allowUntrusted && untrustedWindow(window)) {
				countWriteDenied(ctx, ri.Principal.Tenant)
				return deniedWrite("memory_write denied: untrusted context"), nil
			}
			entry := stores.MemoryEntry{
				ID:      args.ID,
				Value:   args.Value,
				Origins: windowOrigins(ctx),
				RunID:   ri.RunID,
			}
			if err := mem.Put(ctx, memoryKey(ri), entry); err != nil {
				return types.ToolResult{}, fmt.Errorf("memory_write: %w", err)
			}
			return types.ToolResult{
				Content: []types.Block{types.Text{Text: "memory saved"}},
				Outcome: types.Succeeded,
			}, nil
		},
		gohan.WithEffect(types.SideEffect),
		gohan.WithRisk(types.RiskHigh),
		gohan.WithRawArgs(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("memory_write: %w", err)
	}
	read, err := gohan.NewTool("memory_read",
		"List the current subject's stored memory entries",
		func(ctx context.Context, _ struct{}) (types.ToolResult, error) {
			ri, ok := types.RunInfoFrom(ctx)
			if !ok {
				return types.ToolResult{}, fmt.Errorf("memory_read: no run identity")
			}
			entries, err := mem.Entries(ctx, memoryKey(ri))
			if err != nil {
				return types.ToolResult{}, fmt.Errorf("memory_read: %w", err)
			}
			if len(entries) == 0 {
				return types.ToolResult{
					Content: []types.Block{types.Text{Text: "[]"}},
					Outcome: types.Succeeded,
				}, nil
			}
			content := make([]types.Block, 0, len(entries))
			for _, e := range entries {
				b, err := json.Marshal(struct {
					ID    string          `json:"id"`
					Value json.RawMessage `json:"value"`
				}{ID: e.ID, Value: e.Value})
				if err != nil {
					return types.ToolResult{}, fmt.Errorf("memory_read: %w", err)
				}
				content = append(content, types.Text{
					BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "memory_read"}},
					Text:      string(b),
				})
			}
			return types.ToolResult{Content: content, Outcome: types.Succeeded}, nil
		},
		gohan.WithEffect(types.ReadOnly),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("memory_read: %w", err)
	}
	return write, read, nil
}
