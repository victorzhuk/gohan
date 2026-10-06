package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type erasePrincipalKey struct{}

func erasePrincipal(ctx context.Context) (types.Principal, bool) {
	p, ok := ctx.Value(erasePrincipalKey{}).(types.Principal)
	return p, ok
}

func eraseCtx(tenant, subject string) context.Context {
	return context.WithValue(context.Background(), erasePrincipalKey{},
		types.Principal{Tenant: tenant, Subject: subject,
			Scopes: []string{"session:read", "session:write", "session:hold"}})
}

type eraseStores struct {
	stack    *Stack
	sessions *stores.MemorySessionLog
	runs     *stores.MemoryRuns
	events   *stores.MemoryEventLog
	cps      *stores.MemoryCheckpoints
	maps     *stores.MemoryRedactionMaps
	outputs  *stores.MemoryOutputs
	notes    *stores.MemoryNotes
	audit    *stores.MemoryAuditLog
}

func newEraseStores() *eraseStores {
	f := &eraseStores{
		outputs: stores.NewMemoryOutputs(),
		notes:   &stores.MemoryNotes{},
		audit:   stores.NewMemoryAuditLog(),
	}
	f.sessions = stores.NewMemorySessionLog(
		stores.WithSessionPrincipals(erasePrincipal),
		stores.WithSessionAudit(f.audit),
		stores.WithSessionDependents(f.outputs, f.notes),
	)
	f.runs = stores.NewMemoryRuns()
	f.events = stores.NewMemoryEventLog()
	f.cps = stores.NewMemoryCheckpoints()
	f.maps = stores.NewMemoryRedactionMaps()
	f.stack = &Stack{
		logger: slog.New(slog.DiscardHandler),
		stores: stores.Stores{
			SessionLog:    f.sessions,
			Runs:          f.runs,
			Checkpoints:   f.cps,
			OutputStore:   f.outputs,
			EventLog:      f.events,
			Notes:         f.notes,
			RedactionMaps: f.maps,
		},
	}
	return f
}

func (f *eraseStores) seedSession(t *testing.T, ctx context.Context, sessionID string, msgs ...types.Message) {
	t.Helper()
	if _, err := f.sessions.Append(ctx, sessionID, 0, msgs...); err != nil {
		t.Fatalf("seed session %s: %v", sessionID, err)
	}
}

func (f *eraseStores) seedRun(t *testing.T, ctx context.Context, sessionID, runID string) {
	t.Helper()
	if _, err := f.runs.Start(ctx, stores.Run{SessionID: sessionID, RunID: runID, RootRunID: runID}, time.Hour); err != nil {
		t.Fatalf("seed run %s: %v", runID, err)
	}
}

func (f *eraseStores) seedEvent(t *testing.T, ctx context.Context, runID string) {
	t.Helper()
	if err := f.events.Append(ctx, runID, stores.Event{Payload: types.TextDelta{Turn: 1, Delta: "hi"}}); err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

func (f *eraseStores) seedCheckpoint(t *testing.T, ctx context.Context, sessionID, runID string) types.ResumeToken {
	t.Helper()
	tok, err := f.cps.Put(ctx, stores.Checkpoint{RunID: runID, SessionID: sessionID,
		Data: []byte(`{"turn":1,"Ref":"blob-checkpoint"}`)})
	if err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	return tok
}

func (f *eraseStores) seedMap(t *testing.T, ctx context.Context, sessionID string) {
	t.Helper()
	if err := f.maps.Put(ctx, "t1", sessionID, types.RedactionMap{
		Entries: map[string]types.Entity{"tok": {Kind: types.EntityPerson, Value: "Anna Petrova"}},
	}); err != nil {
		t.Fatalf("seed map: %v", err)
	}
}

func TestEraseSubject(t *testing.T) {
	t.Run("redaction.erasure", func(t *testing.T) {
		f := newEraseStores()
		ctx := eraseCtx("t1", "u1")
		f.seedSession(t, ctx, "s1", types.Message{Role: types.RoleUser})
		f.seedSession(t, ctx, "s2", types.Message{Role: types.RoleUser})
		f.seedRun(t, ctx, "s1", "r1")
		f.seedEvent(t, ctx, "r1")
		f.seedCheckpoint(t, ctx, "s1", "r1")
		f.seedMap(t, ctx, "s1")
		f.seedMap(t, ctx, "s2")

		report, err := f.stack.EraseSubject(ctx, "t1", "u1")
		if err != nil {
			t.Fatalf("erase: %v", err)
		}
		if report.Sessions != 2 || report.Entries != 2 {
			t.Fatalf("report = %+v", report)
		}
		if _, err := f.sessions.Load(eraseCtx("t1", "u1"), "s1"); !errors.Is(err, stores.ErrSessionNotFound) {
			t.Fatalf("session s1 survived: %v", err)
		}
		if _, err := f.runs.ByID(context.Background(), "r1"); !errors.Is(err, stores.ErrRunNotFound) {
			t.Fatalf("run survived: %v", err)
		}
		events := 0
		for _, err := range f.events.Read(context.Background(), "r1", 0) {
			if err != nil {
				t.Fatalf("read events: %v", err)
			}
			events++
		}
		if events != 0 {
			t.Fatalf("events survived: %d", events)
		}
		if _, _, err := f.cps.PendingInput(context.Background(), "r1"); err == nil {
			t.Fatal("checkpoint survived")
		}
		if _, err := f.maps.Get(context.Background(), "t1", "s1"); !errors.Is(err, stores.ErrRedactionMapNotFound) {
			t.Fatalf("map survived: %v", err)
		}
		var kinds []stores.AuditKind
		for r, err := range f.audit.Read(context.Background(), "s1") {
			if err != nil {
				t.Fatalf("read audit: %v", err)
			}
			kinds = append(kinds, r.Kind)
		}
		if !slices.Contains(kinds, stores.AuditErasure) {
			t.Fatalf("audit kinds = %v, want erasure record", kinds)
		}
	})

	t.Run("redaction.erase-removes-blobs", func(t *testing.T) {
		f := newEraseStores()
		own := eraseCtx("t1", "u1")
		other := eraseCtx("t1", "u2")
		f.seedSession(t, own, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{
			types.Image{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser}}, Blob: types.Blob{Ref: "blob-exclusive"}},
		}})
		f.seedSession(t, other, "s2")
		shared := []types.Block{types.Text{Text: "shared payload"}}
		exclusive := []types.Block{types.Text{Text: "exclusive payload"}}
		refShared, err := f.outputs.Put(own, types.RunInfo{SessionID: "s1", Principal: types.Principal{Tenant: "t1", Subject: "u1"}}, shared)
		if err != nil {
			t.Fatalf("put shared: %v", err)
		}
		if _, err := f.outputs.Put(other, types.RunInfo{SessionID: "s2", Principal: types.Principal{Tenant: "t1", Subject: "u2"}}, shared); err != nil {
			t.Fatalf("put shared copy: %v", err)
		}
		refExclusive, err := f.outputs.Put(own, types.RunInfo{SessionID: "s1", Principal: types.Principal{Tenant: "t1", Subject: "u1"}}, exclusive)
		if err != nil {
			t.Fatalf("put exclusive: %v", err)
		}

		report, err := f.stack.EraseSubject(own, "t1", "u1")
		if err != nil {
			t.Fatalf("erase: %v", err)
		}
		bctx := context.Background()
		if _, err := f.outputs.Get(bctx, refExclusive); !errors.Is(err, stores.ErrOutputNotFound) {
			t.Fatalf("exclusive ref survived: %v", err)
		}
		if _, err := f.outputs.Get(bctx, refShared); err != nil {
			t.Fatalf("shared ref removed: %v", err)
		}
		if !slices.Contains(report.BlobRefs, "blob-exclusive") {
			t.Fatalf("report.BlobRefs = %v, want history blob ref", report.BlobRefs)
		}
	})

	t.Run("redaction.erase-removes-memory", func(t *testing.T) {
		f := newEraseStores()
		ctx := eraseCtx("t1", "u1")
		f.seedSession(t, ctx, "s1")
		subjectKey := stores.NotesKey{Scope: stores.ScopeSubject, Tenant: "t1", Subject: "u1"}
		sessionKey := stores.NotesKey{Scope: stores.ScopeSession, Tenant: "t1", Subject: "u1", Session: "s1"}
		value, _ := json.Marshal("remembers the booking")
		if err := f.notes.Put(ctx, subjectKey, stores.MemoryEntry{ID: "mem-subject", Value: value}); err != nil {
			t.Fatalf("seed subject memory: %v", err)
		}
		if err := f.notes.Put(ctx, sessionKey, stores.MemoryEntry{ID: "mem-session", Value: value}); err != nil {
			t.Fatalf("seed session memory: %v", err)
		}

		report, err := f.stack.EraseSubject(ctx, "t1", "u1")
		if err != nil {
			t.Fatalf("erase: %v", err)
		}
		if got, err := f.notes.Entries(context.Background(), sessionKey); err != nil || len(got) != 0 {
			t.Fatalf("session memory survived: %v %v", got, err)
		}
		if got, err := f.notes.Entries(context.Background(), subjectKey); err != nil || len(got) != 0 {
			t.Fatalf("subject memory survived: %v %v", got, err)
		}
		if !slices.Equal(report.Memory, []string{"mem-subject"}) {
			t.Fatalf("report.Memory = %v", report.Memory)
		}
	})
}
