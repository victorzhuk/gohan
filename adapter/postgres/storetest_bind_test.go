//go:build full

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/storetest"
)

var bindPrincipal = types.Principal{
	Subject: "u1",
	Tenant:  "t1",
	Scopes:  []string{"session:read", "session:write", "flow:run"},
}

func bindSchema(name string) string {
	return fmt.Sprintf("bind_%s_%d", name, time.Now().UnixNano())
}

func TestSessionLog(t *testing.T) {
	storetest.SessionLog(t, func(ctx context.Context) (storetest.SessionLogStore, error) {
		db := newDB(t, bindSchema("sessionlog"))
		p := bindPrincipal
		log := postgres.NewSessionLog(db, postgres.WithSessionPrincipals(func(ctx context.Context) (types.Principal, bool) {
			return p, true
		}))
		return sessionLogBinding{log}, nil
	})
}

type sessionLogBinding struct{ s stores.SessionLog }

func (b sessionLogBinding) Load(ctx context.Context, sessionID string) (storetest.SessionHistory, error) {
	h, err := b.s.Load(ctx, sessionID)
	if err != nil {
		return storetest.SessionHistory{}, err
	}
	return storetest.SessionHistory{Messages: h.Messages, Version: h.Version}, nil
}

func (b sessionLogBinding) Append(ctx context.Context, sessionID string, expectedVersion int64, msgs ...types.Message) (int64, error) {
	return b.s.Append(ctx, sessionID, expectedVersion, msgs...)
}

func (b sessionLogBinding) Purge(ctx context.Context, olderThan time.Time) (int, error) {
	return b.s.Purge(ctx, olderThan)
}

func (b sessionLogBinding) Delete(ctx context.Context, sessionID string) error {
	return b.s.Delete(ctx, sessionID)
}

func checkpointToStore(cp storetest.Checkpoint) stores.Checkpoint {
	return stores.Checkpoint{
		RunID:          cp.RunID,
		SessionID:      cp.SessionID,
		Flow:           cp.Flow,
		Backend:        cp.Backend,
		BackendVersion: cp.BackendVersion,
		Reason:         cp.Reason,
		Originator:     cp.Originator,
		Data:           cp.Data,
		Child:          cp.Child,
		Workspace:      stores.WorkspaceRef(cp.Workspace),
		ExpiresAt:      cp.ExpiresAt,
	}
}

func checkpointFromStore(cp stores.Checkpoint) storetest.Checkpoint {
	return storetest.Checkpoint{
		RunID:          cp.RunID,
		SessionID:      cp.SessionID,
		Flow:           cp.Flow,
		Backend:        cp.Backend,
		BackendVersion: cp.BackendVersion,
		Reason:         cp.Reason,
		Originator:     cp.Originator,
		Data:           cp.Data,
		Child:          cp.Child,
		Workspace:      string(cp.Workspace),
		ExpiresAt:      cp.ExpiresAt,
	}
}

func inputToStore(in storetest.ResumeInput) stores.ResumeInput {
	return stores.ResumeInput{Approver: in.Approver, Verdict: stores.ApprovalVerdict(in.Verdict), Args: in.Args, Data: in.Data, Reason: in.Reason}
}

func inputFromStore(in stores.ResumeInput) storetest.ResumeInput {
	return storetest.ResumeInput{Approver: in.Approver, Verdict: storetest.ApprovalVerdict(in.Verdict), Args: in.Args, Data: in.Data, Reason: in.Reason}
}

type checkpointsBinding struct {
	s     *postgres.Checkpoints
	runID string
}

func (b checkpointsBinding) Put(ctx context.Context, cp storetest.Checkpoint) (types.ResumeToken, error) {
	in := checkpointToStore(cp)
	in.RunID = b.runID
	return b.s.Put(ctx, in)
}

func (b checkpointsBinding) Consume(ctx context.Context, t types.ResumeToken, in storetest.ResumeInput) (storetest.Checkpoint, error) {
	cp, err := b.s.Consume(ctx, t, inputToStore(in))
	if err != nil {
		return storetest.Checkpoint{}, err
	}
	cp.RunID = ""
	return checkpointFromStore(cp), nil
}

func (b checkpointsBinding) PendingInput(ctx context.Context, runID string) (storetest.Checkpoint, storetest.ResumeInput, error) {
	cp, in, err := b.s.PendingInput(ctx, runID)
	if err != nil {
		return storetest.Checkpoint{}, storetest.ResumeInput{}, err
	}
	cp.RunID = ""
	out := inputFromStore(in)
	return checkpointFromStore(cp), out, nil
}

func (b checkpointsBinding) Peek(ctx context.Context, t types.ResumeToken) (storetest.Checkpoint, error) {
	cp, err := b.s.Peek(ctx, t)
	if err != nil {
		return storetest.Checkpoint{}, err
	}
	cp.RunID = ""
	return checkpointFromStore(cp), nil
}

func (b checkpointsBinding) ConsumeIf(ctx context.Context, t types.ResumeToken, expected storetest.Checkpoint, in storetest.ResumeInput) (storetest.Checkpoint, error) {
	exp := checkpointToStore(expected)
	exp.RunID = b.runID
	cp, err := b.s.ConsumeIf(ctx, t, exp, inputToStore(in))
	if err != nil {
		return storetest.Checkpoint{}, err
	}
	cp.RunID = ""
	return checkpointFromStore(cp), nil
}

func (b checkpointsBinding) UpdatePending(ctx context.Context, t types.ResumeToken, expected, next storetest.Checkpoint) error {
	exp, nxt := checkpointToStore(expected), checkpointToStore(next)
	exp.RunID, nxt.RunID = b.runID, b.runID
	return b.s.UpdatePending(ctx, t, exp, nxt)
}

func newCheckpoints(t *testing.T, runID string) checkpointsBinding {
	t.Helper()
	db := newDB(t, bindSchema("checkpoints"))
	s := postgres.NewCheckpoints(db, postgres.WithCheckpointRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
		return types.RunInfo{RunID: runID}, true
	}))
	return checkpointsBinding{s: s, runID: runID}
}

func TestCheckpoints(t *testing.T) {
	storetest.Checkpoints(t, func(ctx context.Context, runID string) (storetest.CheckpointStore, error) {
		return newCheckpoints(t, runID), nil
	})
	storetest.CheckpointsResumer(t, func(ctx context.Context, runID string) (storetest.CheckpointResumerStore, error) {
		return newCheckpoints(t, runID), nil
	})
}

type advClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *advClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *advClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func stateToStore(s storetest.RunState) stores.RunState { return stores.RunState(s) }
func stateFromStore(s stores.RunState) storetest.RunState {
	return storetest.RunState(s)
}

type runsBinding struct {
	s *postgres.Runs
}

func rowToStore(r storetest.RunRow) stores.Run {
	return stores.Run{SessionID: r.SessionID, RunID: r.RunID, OperationID: r.OperationID, State: stateToStore(r.State), Heartbeat: r.Heartbeat}
}

func runFromStore(r stores.Run) storetest.RunRow {
	return storetest.RunRow{
		SessionID: r.SessionID, RunID: r.RunID, OperationID: r.OperationID,
		State: stateFromStore(r.State), Heartbeat: r.Heartbeat,
		Uncertain: r.Uncertain, ResultRef: r.ResultRef,
	}
}

func (b runsBinding) Start(ctx context.Context, r storetest.RunRow, ttl time.Duration) (storetest.Lease, error) {
	l, err := b.s.Start(ctx, rowToStore(r), ttl)
	return storetest.Lease{RunID: l.RunID, Generation: l.Generation}, err
}

func (b runsBinding) Heartbeat(ctx context.Context, l storetest.Lease) (storetest.Lease, error) {
	got, err := b.s.Heartbeat(ctx, stores.Lease{RunID: l.RunID, Generation: l.Generation})
	return storetest.Lease{RunID: got.RunID, Generation: got.Generation}, err
}

func (b runsBinding) Suspend(ctx context.Context, l storetest.Lease, token types.ResumeToken) error {
	return b.s.Suspend(ctx, stores.Lease{RunID: l.RunID, Generation: l.Generation}, token)
}

func (b runsBinding) Resuming(ctx context.Context, runID string, ttl time.Duration) (storetest.Lease, error) {
	l, err := b.s.Resuming(ctx, runID, ttl)
	return storetest.Lease{RunID: l.RunID, Generation: l.Generation}, err
}

func (b runsBinding) Finish(ctx context.Context, l storetest.Lease, state storetest.RunState, uncertain []types.CallKey, resultRef string) error {
	return b.s.Finish(ctx, stores.Lease{RunID: l.RunID, Generation: l.Generation}, stateToStore(state), uncertain, resultRef)
}

func (b runsBinding) ByOperation(ctx context.Context, tenant, operationID string) (storetest.RunRow, error) {
	r, err := b.s.ByOperation(ctx, tenant, operationID)
	return runFromStore(r), err
}

func (b runsBinding) Stale(ctx context.Context, staleAfter time.Duration, limit int) ([]storetest.RunRow, error) {
	rs, err := b.s.Stale(ctx, staleAfter, limit)
	if err != nil {
		return nil, err
	}
	out := make([]storetest.RunRow, len(rs))
	for i, r := range rs {
		out[i] = runFromStore(r)
	}
	return out, nil
}

func (b runsBinding) Reclaim(ctx context.Context, r storetest.RunRow, ttl time.Duration) (storetest.Lease, error) {
	l, err := b.s.Reclaim(ctx, stores.Run{RunID: r.RunID, SessionID: r.SessionID}, ttl)
	return storetest.Lease{RunID: l.RunID, Generation: l.Generation}, err
}

func (b runsBinding) Signal(ctx context.Context, runID string, s storetest.Signal) error {
	return b.s.Signal(ctx, runID, stores.Signal{Kind: stores.SignalKind(s.Kind), Message: s.Message})
}

func (b runsBinding) Drain(ctx context.Context, l storetest.Lease) ([]storetest.Signal, error) {
	sigs, err := b.s.Drain(ctx, stores.Lease{RunID: l.RunID, Generation: l.Generation})
	if err != nil {
		return nil, err
	}
	out := make([]storetest.Signal, len(sigs))
	for i, sig := range sigs {
		out[i] = storetest.Signal{Kind: storetest.SignalKind(sig.Kind), Message: sig.Message}
	}
	return out, nil
}

func (b runsBinding) Notices(ctx context.Context, limit int) ([]types.RunNotice, error) {
	return b.s.Notices(ctx, limit)
}

func (b runsBinding) AckNotice(ctx context.Context, id string) error {
	return b.s.AckNotice(ctx, id)
}

func (b runsBinding) SuspendWithReason(ctx context.Context, l storetest.Lease, t types.ResumeToken, reason types.SuspendReason) error {
	return b.s.SuspendWithReason(ctx, stores.Lease{RunID: l.RunID, Generation: l.Generation}, t, reason)
}

func (b runsBinding) Preempted(ctx context.Context, limit int) ([]storetest.RunRow, error) {
	runs, err := b.s.Preempted(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]storetest.RunRow, len(runs))
	for i, r := range runs {
		out[i] = runFromStore(r)
	}
	return out, nil
}

func TestRuns(t *testing.T) {
	storetest.Runs(t, func(t *testing.T) (storetest.RunStore, storetest.RunClock) {
		t.Helper()
		clk := &advClock{now: time.Now()}
		db := newDB(t, bindSchema("runs"), postgres.WithClock(clk.Now))
		return runsBinding{s: postgres.NewRuns(db)}, clk
	})
}

// runsFencedBinding exposes the optional CheckpointRuns surface beside the
// frozen Runs port view.
type runsFencedBinding struct {
	runsBinding
	cp stores.CheckpointRuns
}

func (b runsFencedBinding) SuspensionToken(ctx context.Context, runID string) (types.ResumeToken, error) {
	return b.cp.SuspensionToken(ctx, runID)
}

func (b runsFencedBinding) ResumingCheckpoint(ctx context.Context, runID string, t types.ResumeToken, ttl time.Duration) (storetest.Lease, error) {
	l, err := b.cp.ResumingCheckpoint(ctx, runID, t, ttl)
	return storetest.Lease{RunID: l.RunID, Generation: l.Generation}, err
}

func TestRunsWithCheckpoints(t *testing.T) {
	storetest.RunsWithCheckpoints(t, func(t *testing.T) (storetest.RunCheckpointStore, storetest.RunClock) {
		t.Helper()
		clk := &advClock{now: time.Now()}
		db := newDB(t, bindSchema("runs_fence"), postgres.WithClock(clk.Now))
		runs := postgres.NewRuns(db)
		return runsFencedBinding{runsBinding{s: runs}, runs}, clk
	})
}

// recoveryBinding pairs the checkpoints and runs adapters recovery
// discovery consults on one schema. Checkpoints carry their run id on the
// checkpoint itself, so discovery keys on what the suite put.
type recoveryBinding struct {
	checkpointsBinding
	runsBinding
}

func (b recoveryBinding) Put(ctx context.Context, cp storetest.Checkpoint) (types.ResumeToken, error) {
	return b.checkpointsBinding.s.Put(ctx, checkpointToStore(cp))
}

func (b recoveryBinding) ResumeReady(ctx context.Context, limit int) ([]storetest.Checkpoint, error) {
	cps, err := b.checkpointsBinding.s.ResumeReady(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]storetest.Checkpoint, len(cps))
	for i, cp := range cps {
		out[i] = checkpointFromStore(cp)
	}
	return out, nil
}

func TestRecoveryDiscovery(t *testing.T) {
	storetest.RecoveryDiscovery(t, func(t *testing.T) (storetest.RecoveryStore, error) {
		t.Helper()
		db := newDB(t, bindSchema("recovery_suite"))
		return recoveryBinding{
			checkpointsBinding{s: postgres.NewCheckpoints(db)},
			runsBinding{s: postgres.NewRuns(db)},
		}, nil
	})
}

type journalBinding struct{ s stores.Journal }

func (b journalBinding) Reserve(ctx context.Context, k types.CallKey, tool string, fp string, args json.RawMessage) (storetest.JournalEntry, bool, error) {
	e, created, err := b.s.Reserve(ctx, k, tool, stores.Fingerprint(fp), args)
	if err != nil {
		return storetest.JournalEntry{}, created, err
	}
	return storetest.JournalEntry{
		State:       storetest.JournalEntryState(e.State),
		Fingerprint: string(e.Fingerprint),
		Key:         e.Key,
		Result:      e.Result,
	}, created, nil
}

func (b journalBinding) Complete(ctx context.Context, k types.CallKey, res types.ToolResult) error {
	return b.s.Complete(ctx, k, res)
}

func (b journalBinding) ByFingerprint(ctx context.Context, sessionID string, fp string) ([]storetest.JournalEntry, error) {
	entries, err := b.s.ByFingerprint(ctx, sessionID, stores.Fingerprint(fp))
	if err != nil {
		return nil, err
	}
	out := make([]storetest.JournalEntry, len(entries))
	for i, e := range entries {
		out[i] = storetest.JournalEntry{
			State:       storetest.JournalEntryState(e.State),
			Fingerprint: string(e.Fingerprint),
			Key:         e.Key,
			Result:      e.Result,
		}
	}
	return out, nil
}

func TestJournal(t *testing.T) {
	storetest.Journal(t, func(t *testing.T, replayTTL time.Duration) storetest.JournalStore {
		t.Helper()
		db := newDB(t, bindSchema("journal"))
		return journalBinding{s: postgres.NewJournal(db, postgres.WithJournalTTL(replayTTL))}
	})
}

type auditBinding struct{ s stores.AuditLog }

func (b auditBinding) Append(ctx context.Context, r storetest.AuditRecord) error {
	return b.s.Append(ctx, auditToStore(r))
}

func (b auditBinding) Read(ctx context.Context, sessionID string) iter.Seq2[storetest.AuditRecord, error] {
	return func(yield func(storetest.AuditRecord, error) bool) {
		for rec, err := range b.s.Read(ctx, sessionID) {
			if err != nil {
				yield(storetest.AuditRecord{}, err)
				return
			}
			if !yield(auditFromStore(rec), nil) {
				return
			}
		}
	}
}

func (b auditBinding) Purge(ctx context.Context, tenant string, olderThan time.Time) (int, error) {
	return b.s.Purge(ctx, tenant, olderThan)
}

func auditToStore(r storetest.AuditRecord) stores.AuditRecord {
	return stores.AuditRecord{
		Kind:         stores.AuditKind(r.Kind),
		At:           r.At,
		SessionID:    r.SessionID,
		RunID:        r.RunID,
		RootRunID:    r.RootRunID,
		Flow:         r.Flow,
		Subject:      r.Subject,
		Tenant:       r.Tenant,
		Tool:         r.Tool,
		Effect:       r.Effect,
		ArgsChecksum: r.ArgsChecksum,
		Decision:     r.Decision,
		Confidence:   r.Confidence,
		Approver:     r.Approver,
		Verdict:      r.Verdict,
		Outcome:      r.Outcome,
		ResultSHA:    r.ResultSHA,
		ResultBytes:  r.ResultBytes,
		Stage:        r.Stage,
		Model:        r.Model,
		ModelVersion: r.ModelVersion,
		ManifestHash: r.ManifestHash,
		PrevHash:     r.PrevHash,
		Hash:         r.Hash,
	}
}

func auditFromStore(r stores.AuditRecord) storetest.AuditRecord {
	return storetest.AuditRecord{
		Kind:         storetest.AuditKind(r.Kind),
		At:           r.At,
		SessionID:    r.SessionID,
		RunID:        r.RunID,
		RootRunID:    r.RootRunID,
		Flow:         r.Flow,
		Subject:      r.Subject,
		Tenant:       r.Tenant,
		Tool:         r.Tool,
		Effect:       r.Effect,
		ArgsChecksum: r.ArgsChecksum,
		Decision:     r.Decision,
		Confidence:   r.Confidence,
		Approver:     r.Approver,
		Verdict:      r.Verdict,
		Outcome:      r.Outcome,
		ResultSHA:    r.ResultSHA,
		ResultBytes:  r.ResultBytes,
		Stage:        r.Stage,
		Model:        r.Model,
		ModelVersion: r.ModelVersion,
		ManifestHash: r.ManifestHash,
		PrevHash:     r.PrevHash,
		Hash:         r.Hash,
	}
}

func TestAuditLog(t *testing.T) {
	storetest.AuditLog(t, func(t *testing.T) storetest.AuditStore {
		t.Helper()
		db := newDB(t, bindSchema("audit"))
		return auditBinding{s: postgres.NewAuditLog(db)}
	})
}

type eventLogBinding struct{ s stores.EventLog }

func (b eventLogBinding) Append(ctx context.Context, runID string, e storetest.Event) error {
	return b.s.Append(ctx, runID, stores.Event(e))
}

func (b eventLogBinding) Read(ctx context.Context, runID string, afterSeq int64) iter.Seq2[storetest.Event, error] {
	return func(yield func(storetest.Event, error) bool) {
		for e, err := range b.s.Read(ctx, runID, afterSeq) {
			if err != nil {
				yield(storetest.Event{}, err)
				return
			}
			if !yield(storetest.Event(e), nil) {
				return
			}
		}
	}
}

func (b eventLogBinding) Expire(ctx context.Context, olderThan time.Time) error {
	return b.s.Expire(ctx, olderThan)
}

func TestEventLog(t *testing.T) {
	storetest.EventLog(t, func(t *testing.T, now func() time.Time, capacity int) storetest.EventLogStore {
		t.Helper()
		db := newDB(t, bindSchema("events"), postgres.WithClock(now))
		return eventLogBinding{s: postgres.NewEventLog(db, postgres.WithEventLogCapacity(capacity))}
	})
}

func TestOutputs(t *testing.T) {
	ctx := context.Background()
	db := newDB(t, bindSchema("outputs"))
	s := postgres.NewOutputs(db)
	ri := types.RunInfo{Principal: types.Principal{Tenant: "t1", Subject: "u1"}}
	content := []types.Block{types.Text{Text: "hello"}}

	ref, err := s.Put(ctx, ri, content)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	again, err := s.Put(ctx, ri, content)
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if ref != again {
		t.Fatalf("equal content yielded refs %s and %s, want one", ref, again)
	}
	got, err := s.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) == "" {
		t.Fatal("Get returned empty bytes")
	}
	if _, err := s.Get(ctx, "missing"); !errors.Is(err, stores.ErrOutputNotFound) {
		t.Fatalf("Get of an unknown ref: err=%v, want ErrOutputNotFound", err)
	}
}

// TestOutputsForkCascade drives the fork claim inheritance through
// SessionLog.Delete: a fork keeps its parent's ref after the parent is
// deleted, and deleting the fork drops the last claim and the bytes.
func TestOutputsForkCascade(t *testing.T) {
	ctx := context.Background()
	db := newDB(t, bindSchema("outfork"))
	outputs := postgres.NewOutputs(db)
	sessions := postgres.NewSessionLog(db,
		postgres.WithSessionPrincipals(fixedPrincipal(bindPrincipal)),
		postgres.WithSessionDependents(outputs),
	)
	parent := "of-parent"
	fork := "of-fork"
	if _, err := sessions.Append(ctx, parent, 0, types.Message{Blocks: []types.Block{types.Text{Text: "hi"}}}); err != nil {
		t.Fatalf("append parent: %v", err)
	}
	if _, err := sessions.Append(ctx, fork, 0, types.Message{Blocks: []types.Block{types.Text{Text: "hi"}}}); err != nil {
		t.Fatalf("append fork: %v", err)
	}
	ri := types.RunInfo{SessionID: parent, Principal: bindPrincipal}
	ref, err := outputs.Put(ctx, ri, []types.Block{types.Text{Text: "fork-bytes"}})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := outputs.CopySessionDependents(ctx, parent, fork); err != nil {
		t.Fatalf("CopySessionDependents: %v", err)
	}
	if n := count(t, db, `SELECT count(*) FROM output_claim WHERE ref = $1`, ref); n != 2 {
		t.Fatalf("claims after fork copy = %d, want 2", n)
	}
	if err := sessions.Delete(ctx, parent); err != nil {
		t.Fatalf("Delete parent: %v", err)
	}
	got, err := outputs.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get after parent delete: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("Get returned empty bytes after parent delete")
	}
	if err := sessions.Delete(ctx, fork); err != nil {
		t.Fatalf("Delete fork: %v", err)
	}
	if _, err := outputs.Get(ctx, ref); !errors.Is(err, stores.ErrOutputNotFound) {
		t.Fatalf("Get after last claim deleted: err=%v, want ErrOutputNotFound", err)
	}
}

func TestNotes(t *testing.T) {
	ctx := context.Background()
	db := newDB(t, bindSchema("notes"))
	s := postgres.NewNotes(db)
	key := stores.NotesKey{Scope: stores.ScopeSubject, Tenant: "t1", Subject: "u1"}

	notes, version, err := s.Read(ctx, key)
	if err != nil || notes != "" || version != 0 {
		t.Fatalf("initial Read = (%q, %d, %v), want empty at version 0", notes, version, err)
	}
	v, err := s.Write(ctx, key, 0, "first")
	if err != nil || v != 1 {
		t.Fatalf("create Write = (%d, %v), want version 1", v, err)
	}
	if _, err := s.Write(ctx, key, 0, "second"); !errors.Is(err, types.ErrVersionConflict) {
		t.Fatalf("stale create Write err=%v, want ErrVersionConflict", err)
	}
	v, err = s.Write(ctx, key, 1, "second")
	if err != nil || v != 2 {
		t.Fatalf("cas Write = (%d, %v), want version 2", v, err)
	}
	notes, version, err = s.Read(ctx, key)
	if err != nil || notes != "second" || version != 2 {
		t.Fatalf("Read after writes = (%q, %d, %v)", notes, version, err)
	}
	sessionKey := stores.NotesKey{Scope: stores.ScopeSession, Tenant: "t1", Subject: "u1", Session: "s1"}
	if v, err := s.Write(ctx, sessionKey, 0, "session"); err != nil || v != 1 {
		t.Fatalf("session-scope Write = (%d, %v)", v, err)
	}
}

func TestRedaction(t *testing.T) {
	ctx := context.Background()
	db := newDB(t, bindSchema("redaction"))
	s := postgres.NewRedactionMaps(db, postgres.WithRedactionRegion("eu"))
	if s.Region() != "eu" {
		t.Fatalf("Region = %q, want eu", s.Region())
	}
	m := types.RedactionMap{Entries: map[string]types.Entity{"tok": {Kind: "email"}}}
	if _, ok := m.Entries["tok"]; !ok {
		t.Fatal("fixture lost its entry")
	}
	if err := s.Put(ctx, "t1", "s1", m); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put(ctx, "", "", m); err == nil {
		t.Fatal("Put without tenant: err=nil, want a refusal")
	}
	got, err := s.Get(ctx, "t1", "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Tenant != "t1" || got.Session != "s1" || len(got.Entries) != 1 {
		t.Fatalf("Get = %+v, want the stored map with identity filled in", got)
	}
	if _, err := s.Get(ctx, "t1", "missing"); !errors.Is(err, stores.ErrRedactionMapNotFound) {
		t.Fatalf("Get of an unknown session err=%v, want ErrRedactionMapNotFound", err)
	}
}
