//go:build full

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestErase(t *testing.T) {
	ctx := context.Background()
	db := newDB(t, "erase_subject")
	owner := types.Principal{Tenant: "t1", Subject: "u1",
		Scopes: []string{"session:read", "session:write", "session:hold"}}
	other := types.Principal{Tenant: "t1", Subject: "u2",
		Scopes: []string{"session:read", "session:write"}}
	who := owner
	log := postgres.NewSessionLog(db, postgres.WithSessionPrincipals(func(ctx context.Context) (types.Principal, bool) {
		return who, true
	}))
	append := func(t *testing.T, session string) {
		t.Helper()
		if _, err := log.Append(ctx, session, 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}); err != nil {
			t.Fatalf("append %s: %v", session, err)
		}
	}
	append(t, "s1")
	append(t, "s2")
	who = other
	append(t, "s3")
	who = owner

	hold := "case-42"
	if err := log.UpdateSession(ctx, "s2", stores.SessionPatch{Hold: &hold}); err != nil {
		t.Fatalf("set hold: %v", err)
	}

	runs := postgres.NewRuns(db)
	if _, err := runs.Start(ctx, stores.Run{RunID: "r1", SessionID: "s1", State: stores.Running}, time.Minute); err != nil {
		t.Fatalf("start run: %v", err)
	}
	events := postgres.NewEventLog(db)
	if err := events.Append(ctx, "r1", stores.Event{Payload: types.TextDelta{Delta: "hi"}}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	checkpoints := postgres.NewCheckpoints(db)
	if _, err := checkpoints.Put(ctx, stores.Checkpoint{SessionID: "s1", RunID: "r1", Flow: "f"}); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	journal := postgres.NewJournal(db)
	if _, _, err := journal.Reserve(ctx, types.CallKey{SessionID: "s1", CallID: "c1"}, "book", "fp1", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	outputs := postgres.NewOutputs(db)
	ref1, err := outputs.Put(ctx, types.RunInfo{Principal: owner}, []types.Block{types.Text{Text: "one"}})
	if err != nil {
		t.Fatalf("put output: %v", err)
	}
	if _, err := outputs.Put(ctx, types.RunInfo{Principal: other}, []types.Block{types.Text{Text: "two"}}); err != nil {
		t.Fatalf("put other output: %v", err)
	}

	notes := postgres.NewNotes(db)
	if _, err := notes.Write(ctx, stores.NotesKey{Scope: stores.ScopeSubject, Tenant: "t1", Subject: "u1"}, 0, "remember"); err != nil {
		t.Fatalf("write subject notes: %v", err)
	}
	if _, err := notes.Write(ctx, stores.NotesKey{Scope: stores.ScopeSession, Tenant: "t1", Subject: "u1", Session: "s1"}, 0, "session"); err != nil {
		t.Fatalf("write session notes: %v", err)
	}

	redaction := postgres.NewRedactionMaps(db, postgres.WithRedactionRegion("eu"))
	if err := redaction.Put(ctx, "t1", "s1", types.RedactionMap{Entries: map[string]types.Entity{"tok": {Kind: "email"}}}); err != nil {
		t.Fatalf("put map: %v", err)
	}

	audit := postgres.NewAuditLog(db)
	if err := audit.Append(ctx, stores.AuditRecord{Kind: stores.AuditRunStarted, SessionID: "s1", Tenant: "t1"}); err != nil {
		t.Fatalf("append audit: %v", err)
	}

	st := &postgres.Stores{
		Sessions: log, Runs: runs, Events: events, Checkpoints: checkpoints,
		Journal: journal, Outputs: outputs, Notes: notes, Redaction: redaction,
		Audit: audit,
	}
	report, err := st.EraseSubject(ctx, "t1", "u1")
	if err != nil {
		t.Fatalf("erase subject: %v", err)
	}
	if report.Sessions != 1 || len(report.Held) != 1 || report.Held[0] != "s2" {
		t.Fatalf("report sessions = %d held = %v, want 1 and [s2]", report.Sessions, report.Held)
	}
	if report.Outputs != 1 {
		t.Fatalf("report outputs = %d, want 1", report.Outputs)
	}
	if report.Entries == 0 {
		t.Fatal("report entries = 0, want the journal and map entries counted")
	}

	if _, err := log.Load(ctx, "s1"); !errors.Is(err, stores.ErrSessionNotFound) {
		t.Fatalf("load erased session: %v, want ErrSessionNotFound", err)
	}
	if _, err := log.Load(ctx, "s2"); err != nil {
		t.Fatalf("held session was erased: %v", err)
	}
	if _, err := log.Load(ctx, "s3"); err != nil {
		t.Fatalf("another subject's session was erased: %v", err)
	}
	if got := count(t, db, `SELECT count(*) FROM erase_subject.run_record`); got != 0 {
		t.Fatalf("run rows = %d, want 0", got)
	}
	if got := count(t, db, `SELECT count(*) FROM erase_subject.event_record`); got != 0 {
		t.Fatalf("event rows = %d, want 0", got)
	}
	if got := count(t, db, `SELECT count(*) FROM erase_subject.checkpoint_record`); got != 0 {
		t.Fatalf("checkpoint rows = %d, want 0", got)
	}
	if got := count(t, db, `SELECT count(*) FROM erase_subject.journal_record`); got != 0 {
		t.Fatalf("journal rows = %d, want 0", got)
	}
	if got := count(t, db, `SELECT count(*) FROM erase_subject.notes_record`); got != 0 {
		t.Fatalf("notes rows = %d, want 0", got)
	}
	if got := count(t, db, `SELECT count(*) FROM erase_subject.redaction_map`); got != 0 {
		t.Fatalf("redaction rows = %d, want 0", got)
	}
	if _, err := outputs.Get(ctx, ref1); !errors.Is(err, stores.ErrOutputNotFound) {
		t.Fatalf("Get after erase = %v, want ErrOutputNotFound", err)
	}
	if got := count(t, db, `SELECT count(*) FROM erase_subject.audit_record`); got != 3 {
		t.Fatalf("audit rows = %d, want the original record and one erasure record per session", got)
	}
}

func TestEraseRefusesLiveLease(t *testing.T) {
	ctx := context.Background()
	db := newDB(t, "erase_lease")
	p := types.Principal{Tenant: "t1", Subject: "u1", Scopes: []string{"session:read", "session:write"}}
	log := postgres.NewSessionLog(db, postgres.WithSessionPrincipals(func(ctx context.Context) (types.Principal, bool) {
		return p, true
	}))
	if _, err := log.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	runs := postgres.NewRuns(db)
	log = postgres.NewSessionLog(db, postgres.WithSessionPrincipals(func(ctx context.Context) (types.Principal, bool) {
		return p, true
	}), postgres.WithSessionLeases(runs))
	if _, err := runs.Start(ctx, stores.Run{RunID: "r1", SessionID: "s1", State: stores.Running}, time.Minute); err != nil {
		t.Fatalf("start run: %v", err)
	}
	st := &postgres.Stores{Sessions: log, Runs: runs}
	if _, err := st.EraseSubject(ctx, "t1", "u1"); !errors.Is(err, types.ErrRunActive) {
		t.Fatalf("erase with live lease = %v, want ErrRunActive", err)
	}
	if _, err := log.Load(ctx, "s1"); err != nil {
		t.Fatalf("refused erasure removed the session: %v", err)
	}
}

func TestDeleteCascade(t *testing.T) {
	ctx := context.Background()
	db := newDB(t, "delete_cascade")
	p := types.Principal{Tenant: "t1", Subject: "u1",
		Scopes: []string{"session:read", "session:write", "session:hold"}}
	log := postgres.NewSessionLog(db, postgres.WithSessionPrincipals(func(ctx context.Context) (types.Principal, bool) {
		return p, true
	}))
	if _, err := log.Append(ctx, "p", 0,
		types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "one"}}},
		types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "two"}}}); err != nil {
		t.Fatalf("append parent: %v", err)
	}
	if _, err := log.Append(ctx, "child", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "child"}}}); err != nil {
		t.Fatalf("append child: %v", err)
	}
	if _, err := db.Pool().Exec(ctx,
		`UPDATE delete_cascade.session_record SET forked_from = '{"SessionID":"p"}'::jsonb, kind = $1 WHERE session_id = 'child'`,
		int(stores.SessionChild)); err != nil {
		t.Fatalf("link child: %v", err)
	}
	if _, err := log.Append(ctx, "grandchild", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "grandchild"}}}); err != nil {
		t.Fatalf("append grandchild: %v", err)
	}
	if _, err := db.Pool().Exec(ctx,
		`UPDATE delete_cascade.session_record SET forked_from = '{"SessionID":"child"}'::jsonb, kind = $1 WHERE session_id = 'grandchild'`,
		int(stores.SessionShadow)); err != nil {
		t.Fatalf("link grandchild: %v", err)
	}

	checkpoints := postgres.NewCheckpoints(db)
	notes := postgres.NewNotes(db)
	dependentLog := postgres.NewSessionLog(db,
		postgres.WithSessionPrincipals(func(ctx context.Context) (types.Principal, bool) {
			return p, true
		}),
		postgres.WithSessionDependents(
			dependentFunc(func(ctx context.Context, sessionID string) error {
				return checkpoints.DeleteSessionDependents(ctx, sessionID)
			}),
			dependentFunc(func(ctx context.Context, sessionID string) error {
				return notes.DeleteSessionDependents(ctx, sessionID)
			}),
		))
	if _, err := checkpoints.Put(ctx, stores.Checkpoint{SessionID: "p", RunID: "rp", Flow: "f"}); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}
	if _, err := notes.Write(ctx, stores.NotesKey{Scope: stores.ScopeSession, Tenant: "t1", Subject: "u1", Session: "p"}, 0, "n"); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	if err := dependentLog.Delete(ctx, "p"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := dependentLog.Load(ctx, "p"); !errors.Is(err, stores.ErrSessionNotFound) {
		t.Fatalf("load deleted parent: %v, want ErrSessionNotFound", err)
	}
	if _, err := dependentLog.Load(ctx, "child"); !errors.Is(err, stores.ErrSessionNotFound) {
		t.Fatalf("child survived the cascade: %v", err)
	}
	if got := count(t, db, `SELECT count(*) FROM delete_cascade.checkpoint_record WHERE session_id = 'p'`); got != 0 {
		t.Fatalf("checkpoint rows = %d, want 0", got)
	}
	if got := count(t, db, `SELECT count(*) FROM delete_cascade.notes_record WHERE session = 'p'`); got != 0 {
		t.Fatalf("notes rows = %d, want 0", got)
	}
	if _, err := dependentLog.Load(ctx, "forked"); !errors.Is(err, stores.ErrSessionNotFound) {
		t.Fatalf("grandchild survived the cascade: %v", err)
	}
}

func TestDeleteRefusesHoldAndLease(t *testing.T) {
	ctx := context.Background()
	db := newDB(t, "delete_refusals")
	p := types.Principal{Tenant: "t1", Subject: "u1",
		Scopes: []string{"session:read", "session:write", "session:hold"}}
	log := postgres.NewSessionLog(db, postgres.WithSessionPrincipals(func(ctx context.Context) (types.Principal, bool) {
		return p, true
	}))
	if _, err := log.Append(ctx, "p", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "one"}}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	hold := "case-42"
	if err := log.UpdateSession(ctx, "p", stores.SessionPatch{Hold: &hold}); err != nil {
		t.Fatalf("set hold: %v", err)
	}
	if err := log.Delete(ctx, "p"); !errors.Is(err, types.ErrSessionHeld) {
		t.Fatalf("delete held = %v, want ErrSessionHeld", err)
	}
	none := ""
	if err := log.UpdateSession(ctx, "p", stores.SessionPatch{Hold: &none}); err != nil {
		t.Fatalf("clear hold: %v", err)
	}
	runs := postgres.NewRuns(db)
	log = postgres.NewSessionLog(db, postgres.WithSessionPrincipals(func(ctx context.Context) (types.Principal, bool) {
		return p, true
	}), postgres.WithSessionLeases(runs))
	if _, err := runs.Start(ctx, stores.Run{RunID: "r1", SessionID: "p", State: stores.Running}, time.Minute); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := log.Delete(ctx, "p"); !errors.Is(err, types.ErrRunActive) {
		t.Fatalf("delete leased = %v, want ErrRunActive", err)
	}
	if _, err := log.Load(ctx, "p"); err != nil {
		t.Fatalf("refused delete removed the session: %v", err)
	}
}

type dependentFunc func(ctx context.Context, sessionID string) error

func (f dependentFunc) DeleteSessionDependents(ctx context.Context, sessionID string) error {
	return f(ctx, sessionID)
}

func (dependentFunc) CopySessionDependents(context.Context, string, string) error { return nil }
