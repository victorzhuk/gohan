package notes

import (
	"context"
	"errors"
	"iter"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

const consolidateShape = `{
	"type": "object",
	"properties": {"fact": {"type": "string"}},
	"required": ["fact"],
	"additionalProperties": false
}`

// scriptModel replays one canned completion per call.
type scriptModel struct {
	profile types.ModelProfile
	texts   []string
	calls   int
}

func (m *scriptModel) Profile() types.ModelProfile { return m.profile }

func (m *scriptModel) Generate(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	i := m.calls
	m.calls++
	if i >= len(m.texts) {
		i = len(m.texts) - 1
	}
	text := m.texts[i]
	return func(yield func(types.ModelChunk, error) bool) {
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: text}, nil)
	}
}

func consolidateStack(t *testing.T, texts ...string) *gohan.Stack {
	t.Helper()
	s, err := gohan.Build(
		gohan.WithModels(&scriptModel{profile: types.ModelProfile{Name: "cheap"}, texts: texts}),
		gohan.WithPrompts(chains.PromptSet{}),
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return s
}

func consolidateFixture(t *testing.T, texts ...string) (Consolidation, *stores.MemoryNotes, *stores.MemoryAuditLog) {
	t.Helper()
	store := stores.NewMemoryNotes(stores.WithMemoryNotesPolicy(stores.MemoryPolicy{Schema: []byte(consolidateShape)}))
	sessions := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	audit := stores.NewMemoryAuditLog()
	c := Consolidation{
		Stack:    consolidateStack(t, texts...),
		Profile:  "cheap",
		Store:    store,
		Sessions: sessions,
		Audit:    audit,
		Policy:   stores.MemoryPolicy{Schema: []byte(consolidateShape)},
	}
	return c, store, audit
}

func seedSession(t *testing.T, sessions stores.SessionLog) {
	t.Helper()
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
	if _, err := sessions.Append(ctx, "s1", 0,
		types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "booked flight BA100 leaving June 3"}}},
		types.Message{Role: types.RoleAssistant, Blocks: []types.Block{types.Text{Text: "confirmed"}}},
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func consolidateRunInfo() types.RunInfo {
	return types.RunInfo{
		Flow:      "agent",
		SessionID: "s1",
		RunID:     "r1",
		Principal: types.Principal{Tenant: "t1", Subject: "u1"},
	}
}

func consolidateCtx() context.Context {
	return types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
}

func auditKinds(t *testing.T, audit stores.AuditLog) []stores.AuditKind {
	t.Helper()
	var kinds []stores.AuditKind
	for r, err := range audit.Read(context.Background(), "s1") {
		if err != nil {
			t.Fatalf("audit read: %v", err)
		}
		kinds = append(kinds, r.Kind)
		if r.SessionID != "s1" {
			t.Errorf("audit session = %q, want s1", r.SessionID)
		}
	}
	return kinds
}

func TestConsolidation(t *testing.T) {
	t.Run("working-state.memory-consolidate-at-done", func(t *testing.T) {
		c, store, audit := consolidateFixture(t, `{"fact":"flies BA100 on June 3"}`)
		seedSession(t, c.Sessions)
		if err := c.Run(consolidateCtx(), consolidateRunInfo()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		entries, err := store.Entries(context.Background(), memoryKey(consolidateRunInfo()))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("entries = %d, want 1", len(entries))
		}
		if string(entries[0].Value) != `{"fact":"flies BA100 on June 3"}` {
			t.Errorf("value = %s", entries[0].Value)
		}
		kinds := auditKinds(t, audit)
		if len(kinds) != 1 || kinds[0] != AuditMemoryConsolidate {
			t.Fatalf("audit kinds = %v, want [%s]", kinds, AuditMemoryConsolidate)
		}
	})
	t.Run("schema-invalid extraction stores nothing", func(t *testing.T) {
		c, store, audit := consolidateFixture(t, `"free text"`)
		seedSession(t, c.Sessions)
		if err := c.Run(consolidateCtx(), consolidateRunInfo()); !errors.Is(err, types.ErrMemoryWriteInvalid) {
			t.Fatalf("err = %v, want ErrMemoryWriteInvalid", err)
		}
		entries, err := store.Entries(context.Background(), memoryKey(consolidateRunInfo()))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("entries = %d, want 0", len(entries))
		}
		if kinds := auditKinds(t, audit); len(kinds) != 0 {
			t.Fatalf("audit kinds = %v, want none", kinds)
		}
	})
	t.Run("compaction writes nothing", func(t *testing.T) {
		c, store, audit := consolidateFixture(t, `{"fact":"flies BA100 on June 3"}`)
		seedSession(t, c.Sessions)
		if err := c.Run(consolidateCtx(), consolidateRunInfo()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		before, err := store.Entries(context.Background(), memoryKey(consolidateRunInfo()))
		if err != nil {
			t.Fatal(err)
		}
		// Compaction shrinks the history to a summary marker; the run that
		// follows it carries no consolidation of its own.
		hist, err := c.Sessions.Load(consolidateCtx(), "s1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Sessions.Append(consolidateCtx(), "s1", hist.Version,
			types.Message{Role: types.RoleAssistant, Blocks: []types.Block{types.Compaction{
				CoversUpTo: 2,
				Kind:       types.CompactionText,
				Summary:    []types.Block{types.Text{Text: "so far"}},
			}}},
		); err != nil {
			t.Fatalf("compact: %v", err)
		}
		after, err := store.Entries(context.Background(), memoryKey(consolidateRunInfo()))
		if err != nil {
			t.Fatal(err)
		}
		if len(after) != len(before) {
			t.Fatalf("entries after compaction = %d, want %d", len(after), len(before))
		}
		if kinds := auditKinds(t, audit); len(kinds) != 1 {
			t.Fatalf("audit kinds after compaction = %v, want only the Done record", kinds)
		}
	})
}
