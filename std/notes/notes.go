// Package notes provides the durable working-state notes primitive: a
// notes_write tool backed by a NotesStore and a session-slot provider that
// assembles the saved notes into every request. The notes never enter the
// history, so truncation, compaction and a full run reset cannot touch
// them; the next run in the session reads them back from the store.
package notes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// DefaultMaxNotes is the assembled-size cap for the session slot.
const DefaultMaxNotes = 2048

type config struct {
	maxNotes        int
	redactor        types.Redactor
	redactionPolicy types.RedactionPolicy
	redactionMaps   stores.RedactionMaps
	allowUntrusted  bool
}

type Option func(*config)

func WithRedaction(redactor types.Redactor, policy types.RedactionPolicy, maps stores.RedactionMaps) Option {
	return func(c *config) {
		c.redactor, c.redactionPolicy, c.redactionMaps = redactor, policy, maps
	}
}

// WithMaxNotes caps the notes injected into the session slot, in bytes.
func WithMaxNotes(n int) Option {
	return func(c *config) { c.maxNotes = n }
}

// Notes is the session notes pair: a notes_write tool and the SlotSession
// provider. Both go through the same NotesStore, so what the tool writes
// the next run reads. The value is built once and shared by every run;
// per-request state comes only from ctx and args.
type Notes struct {
	store           stores.NotesStore
	max             int
	tool            types.Tool
	redactor        types.Redactor
	redactionPolicy types.RedactionPolicy
	redactionMaps   stores.RedactionMaps
}

// New builds the notes pair over the store.
func New(store stores.NotesStore, opts ...Option) (*Notes, error) {
	cfg := config{maxNotes: DefaultMaxNotes}
	for _, o := range opts {
		o(&cfg)
	}
	write, err := gohan.NewTool("notes_write",
		"Record durable notes for the session: progress, constraints and decisions that must survive a context reset",
		func(ctx context.Context, args writeArgs) (string, error) {
			ri, _ := types.RunInfoFrom(ctx)
			key := sessionKey(ri)
			_, version, err := store.Read(ctx, key)
			if err != nil {
				return "", fmt.Errorf("notes_write: read notes: %w", err)
			}
			if args.ExpectedVersion != 0 {
				version = args.ExpectedVersion
			}
			if cfg.redactor != nil && len(cfg.redactionPolicy.Detectors) > 0 {
				info, _ := types.RunInfoFrom(ctx)
				blocks, m, err := cfg.redactor.Redact(ctx, []types.Block{types.Text{Text: args.Notes}}, cfg.redactionPolicy)
				if err != nil {
					return "", fmt.Errorf("notes_write: redact: %w", err)
				}
				if cfg.redactionMaps == nil {
					return "", fmt.Errorf("notes_write: redaction map store missing")
				}
				m.Tenant, m.Session = info.Principal.Tenant, info.SessionID
				if old, err := cfg.redactionMaps.Get(ctx, m.Tenant, m.Session); err == nil {
					for token, entity := range old.Entries {
						if m.Entries == nil {
							m.Entries = make(map[string]types.Entity)
						}
						m.Entries[token] = entity
					}
				} else if !errors.Is(err, stores.ErrRedactionMapNotFound) {
					return "", fmt.Errorf("notes_write: load redaction map: %w", err)
				}
				if err := cfg.redactionMaps.Put(ctx, m.Tenant, m.Session, m); err != nil {
					return "", fmt.Errorf("notes_write: store redaction map: %w", err)
				}
				args.Notes = blocks[0].(types.Text).Text
			}
			next, err := store.Write(ctx, key, version, args.Notes)
			if err != nil {
				return "", fmt.Errorf("notes_write: %w", err)
			}
			return fmt.Sprintf("notes saved at version %d", next), nil
		},
		gohan.WithEffect(types.ReadOnly),
	)
	if err != nil {
		return nil, fmt.Errorf("notes_write: %w", err)
	}
	return &Notes{store: store, max: cfg.maxNotes, tool: write, redactor: cfg.redactor, redactionPolicy: cfg.redactionPolicy, redactionMaps: cfg.redactionMaps}, nil
}

// sessionKey addresses the session notes record for one run. The harness
// builds it from the run's identity, so a caller cannot name another
// tenant, subject or session.
func sessionKey(ri types.RunInfo) stores.NotesKey {
	return stores.NotesKey{
		Scope:   stores.ScopeSession,
		Tenant:  ri.Principal.Tenant,
		Subject: ri.Principal.Subject,
		Session: ri.SessionID,
	}
}

type writeArgs struct {
	Notes           string `json:"notes" desc:"the notes text that replaces the stored session notes"`
	ExpectedVersion int64  `json:"expected_version,omitempty" desc:"last version seen; omit to overwrite the current notes"`
}

// Spec returns the notes_write tool spec.
func (n *Notes) Spec() types.ToolSpec { return n.tool.Spec() }

// Call writes the notes through the store.
func (n *Notes) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	return n.tool.Call(ctx, args)
}

// Slot is the session slot: the notes travel outside the history.
func (n *Notes) Slot() types.ContextSlot { return types.SlotSession }

// Provide assembles the stored notes into the request. An empty store
// contributes no blocks.
func (n *Notes) Provide(ctx context.Context, ri types.RunInfo) ([]types.Block, error) {
	notes, _, err := n.store.Read(ctx, sessionKey(ri))
	if err != nil {
		return nil, fmt.Errorf("provide notes: %w", err)
	}
	if notes == "" {
		return nil, nil
	}
	if len(notes) > n.max {
		notes = notes[:n.max]
	}
	return []types.Block{types.Text{
		BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "notes"}},
		Text:      notes,
	}}, nil
}
