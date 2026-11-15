//go:build full

package postgres_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

const compactionBindTenant = "t1"

func compactionTestPrincipal() types.Principal {
	return types.Principal{
		Tenant:  compactionBindTenant,
		Subject: "u1",
		Scopes:  []string{"session:read", "session:write"},
	}
}

func compactionPublicationMessage(t *testing.T, sessionID, runID string, fromVersion int64, summary []types.Block) (types.Message, stores.CompactionPublication) {
	t.Helper()
	checksum, err := stores.CompactionSummarySHA(summary)
	if err != nil {
		t.Fatal(err)
	}
	payload := stores.CompactionPublication{
		SessionID:   sessionID,
		RunID:       runID,
		RootRunID:   "root-1",
		Flow:        "flow-1",
		Tenant:      compactionBindTenant,
		Subject:     "u1",
		FromVersion: fromVersion,
		ToVersion:   fromVersion + 1,
		Policy:      "policy-1",
		SummarySHA:  checksum,
	}
	payload.PublicationID = stores.CompactionPublicationID(sessionID, runID, fromVersion)
	message := types.Message{
		ID:   payload.PublicationID,
		Role: types.RoleSystem,
		Blocks: []types.Block{
			types.Compaction{
				Kind:       types.CompactionText,
				CoversUpTo: fromVersion,
				Summary:    summary,
			},
		},
		Meta: map[string]any{stores.MetadataPayloadKey: payload},
	}
	return message, payload
}

func seedSession(t *testing.T, ctx context.Context, s *postgres.SessionLog, sessionID string) int64 {
	t.Helper()
	version, err := s.Append(ctx, sessionID, 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "seed"}}})
	if err != nil {
		t.Fatalf("seed append: %v", err)
	}
	return version
}

func newCompactionSessionLog(t *testing.T, db *postgres.DB) *postgres.SessionLog {
	t.Helper()
	return postgres.NewSessionLog(db, postgres.WithSessionPrincipals(fixedPrincipal(compactionTestPrincipal())))
}

func TestSessionAppendCompaction(t *testing.T) {
	ctx := context.Background()

	t.Run("context.compaction-publication-identity", func(t *testing.T) {
		db := newDB(t, bindSchema("cmpid"))
		s := newCompactionSessionLog(t, db)
		version := seedSession(t, ctx, s, "s1")
		message, payload := compactionPublicationMessage(t, "s1", "run-1", version, []types.Block{types.Text{Text: "summary"}})
		if want := stores.CompactionPublicationID("s1", "run-1", version); payload.PublicationID != want {
			t.Fatalf("publication id %s: want %s", payload.PublicationID, want)
		}
		got, err := s.AppendCompaction(ctx, "s1", version, message)
		if err != nil {
			t.Fatal(err)
		}
		if got != version+1 {
			t.Fatalf("append version %d: want %d", got, version+1)
		}
		h, err := s.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if h.Version != version+1 {
			t.Fatalf("history version %d: want %d", h.Version, version+1)
		}
		if last := h.Messages[len(h.Messages)-1]; last.ID != payload.PublicationID {
			t.Fatalf("stored message id %s: want %s", last.ID, payload.PublicationID)
		}
	})

	t.Run("context.compaction-append-duplicate-noop", func(t *testing.T) {
		db := newDB(t, bindSchema("cmpdup"))
		s := newCompactionSessionLog(t, db)
		version := seedSession(t, ctx, s, "s1")
		message, _ := compactionPublicationMessage(t, "s1", "run-1", version, nil)
		committed, err := s.AppendCompaction(ctx, "s1", version, message)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Append(ctx, "s1", committed, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "later"}}}); err != nil {
			t.Fatal(err)
		}
		h, err := s.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		// The stale expectedVersion must not matter: the identity is
		// already durable under its original version.
		retry, err := s.AppendCompaction(ctx, "s1", h.Version, message)
		if err != nil {
			t.Fatal(err)
		}
		if retry != committed {
			t.Fatalf("duplicate version %d: want original %d", retry, committed)
		}
		after, err := s.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if after.Version != h.Version || len(after.Messages) != len(h.Messages) {
			t.Fatalf("duplicate mutated history: version %d len %d, want %d len %d", after.Version, len(after.Messages), h.Version, len(h.Messages))
		}
	})

	t.Run("context.compaction-append-conflict-refused", func(t *testing.T) {
		db := newDB(t, bindSchema("cmpconf"))
		s := newCompactionSessionLog(t, db)
		version := seedSession(t, ctx, s, "s1")
		message, payload := compactionPublicationMessage(t, "s1", "run-1", version, nil)

		wrongID := message
		wrongID.ID = "m-deadbeef"
		if _, err := s.AppendCompaction(ctx, "s1", version, wrongID); err == nil {
			t.Fatal("wrong message id accepted")
		}
		h, err := s.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if h.Version != version {
			t.Fatalf("wrong id mutated version to %d", h.Version)
		}

		committed, err := s.AppendCompaction(ctx, "s1", version, message)
		if err != nil {
			t.Fatal(err)
		}
		conflicting := message
		conflicting.Meta = map[string]any{stores.MetadataPayloadKey: payload}
		altered := payload
		altered.Policy = "policy-2"
		conflicting.Meta[stores.MetadataPayloadKey] = altered
		if _, err := s.AppendCompaction(ctx, "s1", committed, conflicting); err == nil || !strings.Contains(err.Error(), "conflicting compaction publication") {
			t.Fatalf("conflicting payload error %v: want conflicting compaction publication", err)
		}
		after, err := s.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if after.Version != committed {
			t.Fatalf("conflict mutated version to %d: want %d", after.Version, committed)
		}
	})

	t.Run("context.compaction-append-stale-refused", func(t *testing.T) {
		db := newDB(t, bindSchema("cmpstale"))
		s := newCompactionSessionLog(t, db)
		version := seedSession(t, ctx, s, "s1")
		message, _ := compactionPublicationMessage(t, "s1", "run-1", version, nil)
		_, err := s.AppendCompaction(ctx, "s1", version-1, message)
		if !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("stale absent identity error %v: want types.ErrVersionConflict", err)
		}
		h, err := s.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if h.Version != version {
			t.Fatalf("stale refusal mutated version to %d: want %d", h.Version, version)
		}
	})
}

func TestCompactionSinkAppend(t *testing.T) {
	ctx := context.Background()

	auditRecord := func(sessionID, publicationID, policy string) stores.AuditRecord {
		return stores.AuditRecord{
			Kind:      stores.AuditCompacted,
			SessionID: sessionID,
			RunID:     "run-1",
			RootRunID: "root-1",
			Flow:      "flow-1",
			Tenant:    compactionBindTenant,
			Subject:   "u1",
			Compaction: &stores.CompactionAudit{
				PublicationID: publicationID,
				FromVersion:   3,
				ToVersion:     4,
				TokensBefore:  900,
				TokensAfter:   40,
				Policy:        policy,
				SummarySHA:    "abc",
			},
		}
	}
	compactionEvent := func(sessionID, publicationID, policy string) stores.Event {
		return stores.Event{
			Payload: types.Compacted{
				FromVersion:   3,
				ToVersion:     4,
				Policy:        policy,
				PublicationID: publicationID,
			},
			Meta: types.EventMeta{SessionID: sessionID, RootRunID: "root-1", Flow: "flow-1"},
		}
	}

	t.Run("context.compaction-publication-once", func(t *testing.T) {
		db := newDB(t, bindSchema("cmponce"))
		audit := postgres.NewAuditLog(db)
		events := postgres.NewEventLog(db, postgres.WithEventLogCapacity(1))
		schema := db.Schema()
		sessionID := "s1"
		publicationID := stores.CompactionPublicationID(sessionID, "run-1", 3)

		inserted, err := audit.AppendCompactionAudit(ctx, auditRecord(sessionID, publicationID, "policy-1"))
		if err != nil || !inserted {
			t.Fatalf("audit insert: %v inserted=%v", err, inserted)
		}
		duplicate, err := audit.AppendCompactionAudit(ctx, auditRecord(sessionID, publicationID, "policy-1"))
		if err != nil || duplicate {
			t.Fatalf("audit duplicate: %v inserted=%v", err, duplicate)
		}
		if _, err := audit.AppendCompactionAudit(ctx, auditRecord(sessionID, publicationID, "policy-2")); err == nil || !strings.Contains(err.Error(), "conflicting compaction publication") {
			t.Fatalf("audit conflict error %v: want conflicting compaction publication", err)
		}
		if got := count(t, db, `SELECT count(*) FROM `+schema+`.audit_record WHERE publication_id = $1`, publicationID); got != 1 {
			t.Fatalf("audit rows = %d: want 1", got)
		}

		inserted, err = events.AppendCompactionEvent(ctx, publicationID, "run-1", compactionEvent(sessionID, publicationID, "policy-1"))
		if err != nil || !inserted {
			t.Fatalf("event insert: %v inserted=%v", err, inserted)
		}
		tombstones := func() int {
			t.Helper()
			return count(t, db, `SELECT count(*) FROM `+schema+`.event_compaction_publication WHERE run_id = $1 AND publication_id = $2`, "run-1", publicationID)
		}
		eventRows := func() int {
			t.Helper()
			return count(t, db, `SELECT count(*) FROM `+schema+`.event_record WHERE run_id = $1`, "run-1")
		}
		if got := tombstones(); got != 1 {
			t.Fatalf("tombstone rows = %d: want 1", got)
		}
		duplicate, err = events.AppendCompactionEvent(ctx, publicationID, "run-1", compactionEvent(sessionID, publicationID, "policy-1"))
		if err != nil || duplicate {
			t.Fatalf("event duplicate: %v inserted=%v", err, duplicate)
		}
		if _, err := events.AppendCompactionEvent(ctx, publicationID, "run-1", compactionEvent(sessionID, publicationID, "policy-2")); err == nil || !strings.Contains(err.Error(), "conflicting compaction publication") {
			t.Fatalf("event conflict error %v: want conflicting compaction publication", err)
		}
		if got := tombstones(); got != 1 {
			t.Fatalf("tombstone rows after conflict = %d: want 1", got)
		}

		// The identity survives window eviction: an identical retry after
		// the event was evicted stays a no-op and consumes no sequence.
		if err := events.Append(ctx, "run-1", compactionEvent(sessionID, "other-pub", "policy-1")); err != nil {
			t.Fatal(err)
		}
		if got := eventRows(); got != 1 {
			t.Fatalf("event rows after eviction = %d: want 1", got)
		}
		duplicate, err = events.AppendCompactionEvent(ctx, publicationID, "run-1", compactionEvent(sessionID, publicationID, "policy-1"))
		if err != nil || duplicate {
			t.Fatalf("event duplicate after eviction: %v inserted=%v", err, duplicate)
		}
		seqBefore := count(t, db, `SELECT next FROM `+schema+`.event_seq WHERE run_id = 'run-1'`)
		duplicate, err = events.AppendCompactionEvent(ctx, publicationID, "run-1", compactionEvent(sessionID, publicationID, "policy-1"))
		if err != nil || duplicate {
			t.Fatalf("event duplicate after eviction: %v inserted=%v", err, duplicate)
		}
		if got := count(t, db, `SELECT next FROM `+schema+`.event_seq WHERE run_id = 'run-1'`); got != seqBefore {
			t.Fatalf("duplicate advanced event_seq to %d: want %d", got, seqBefore)
		}

		// Expiry drops the compaction payload too, but the tombstone keeps
		// the duplicate a no-op.
		if err := events.Expire(ctx, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if got := eventRows(); got != 0 {
			t.Fatalf("event rows after expire = %d: want 0", got)
		}
		if got := tombstones(); got != 1 {
			t.Fatalf("tombstone rows after expire = %d: want 1", got)
		}
		duplicate, err = events.AppendCompactionEvent(ctx, publicationID, "run-1", compactionEvent(sessionID, publicationID, "policy-1"))
		if err != nil || duplicate {
			t.Fatalf("event duplicate after expire: %v inserted=%v", err, duplicate)
		}

		if _, err := audit.AppendCompactionAudit(ctx, stores.AuditRecord{Kind: stores.AuditCompacted, SessionID: sessionID}); err == nil {
			t.Fatal("audit record without payload accepted")
		}
		if _, err := events.AppendCompactionEvent(ctx, "", "run-1", compactionEvent(sessionID, publicationID, "policy-1")); err == nil {
			t.Fatal("event without publication id accepted")
		}

		// Concurrent reconciliation of an already accepted identity inserts
		// nothing and stays a no-op.
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ok, err := audit.AppendCompactionAudit(ctx, auditRecord(sessionID, publicationID, "policy-1"))
				if err != nil {
					t.Errorf("concurrent audit: %v", err)
					return
				}
				if ok {
					t.Errorf("concurrent audit inserted a second record")
				}
			}()
		}
		wg.Wait()
		if got := count(t, db, `SELECT count(*) FROM `+schema+`.audit_record WHERE publication_id = $1`, publicationID); got != 1 {
			t.Fatalf("concurrent audit rows = %d: want 1", got)
		}

		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ok, err := events.AppendCompactionEvent(ctx, publicationID, "run-1", compactionEvent(sessionID, publicationID, "policy-1"))
				if err != nil {
					t.Errorf("concurrent event: %v", err)
					return
				}
				if ok {
					t.Errorf("concurrent event inserted a second record")
				}
			}()
		}
		wg.Wait()
		if got := tombstones(); got != 1 {
			t.Fatalf("concurrent tombstone rows = %d: want 1", got)
		}

		// Four fresh writers race on one identity: exactly one inserts, the
		// losers report duplicates, and one tombstone plus one event row
		// remain.
		run2ID := stores.CompactionPublicationID(sessionID, "run-2", 3)
		inserts := make([]bool, 4)
		for i := range 4 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ok, err := events.AppendCompactionEvent(ctx, run2ID, "run-2", compactionEvent(sessionID, run2ID, "policy-1"))
				if err != nil {
					t.Errorf("fresh concurrent event: %v", err)
					return
				}
				inserts[i] = ok
			}(i)
		}
		wg.Wait()
		insertedCount := 0
		for _, ok := range inserts {
			if ok {
				insertedCount++
			}
		}
		if insertedCount != 1 {
			t.Fatalf("fresh concurrent inserts = %d: want 1", insertedCount)
		}
		if got := count(t, db, `SELECT count(*) FROM `+schema+`.event_compaction_publication WHERE run_id = 'run-2'`); got != 1 {
			t.Fatalf("fresh concurrent tombstones = %d: want 1", got)
		}
		if got := count(t, db, `SELECT count(*) FROM `+schema+`.event_record WHERE run_id = 'run-2'`); got != 1 {
			t.Fatalf("fresh concurrent event rows = %d: want 1", got)
		}
	})

	// A standalone append needs no session_record and no run_record; the
	// empty-session key survives every ordinary cleanup and goes only
	// through an explicit run erasure.
	t.Run("context.compaction-publication-once/standalone-run-erasure", func(t *testing.T) {
		db := newDB(t, bindSchema("cmpstandalone"))
		events := postgres.NewEventLog(db, postgres.WithEventLogCapacity(1))
		schema := db.Schema()
		publicationID := stores.CompactionPublicationID("s1", "run-standalone", 3)

		inserted, err := events.AppendCompactionEvent(ctx, publicationID, "run-standalone", compactionEvent("", publicationID, "policy-1"))
		if err != nil || !inserted {
			t.Fatalf("standalone insert: %v inserted=%v", err, inserted)
		}
		if got := count(t, db, `SELECT count(*) FROM `+schema+`.session_record WHERE session_id = 's1'`); got != 0 {
			t.Fatalf("session rows = %d: want 0", got)
		}
		duplicate, err := events.AppendCompactionEvent(ctx, publicationID, "run-standalone", compactionEvent("", publicationID, "policy-1"))
		if err != nil || duplicate {
			t.Fatalf("standalone duplicate: %v inserted=%v", err, duplicate)
		}
		removed, err := events.EraseRuns(ctx, []string{"run-standalone"})
		if err != nil {
			t.Fatal(err)
		}
		if removed != 1 {
			t.Fatalf("erase removed = %d: want 1", removed)
		}
		if got := count(t, db, `SELECT count(*) FROM `+schema+`.event_compaction_publication WHERE run_id = 'run-standalone'`); got != 0 {
			t.Fatalf("tombstone rows after erasure = %d: want 0", got)
		}
		if got := count(t, db, `SELECT count(*) FROM `+schema+`.event_record WHERE run_id = 'run-standalone'`); got != 0 {
			t.Fatalf("event rows after erasure = %d: want 0", got)
		}
		if got := count(t, db, `SELECT count(*) FROM `+schema+`.event_seq WHERE run_id = 'run-standalone'`); got != 0 {
			t.Fatalf("sequence rows after erasure = %d: want 0", got)
		}
	})

	// A session delete clears its publication tombstones inside the session
	// transaction, without any run_record discovery. A legal hold blocks
	// the delete and preserves them.
	t.Run("context.compaction-publication-once/session-cleanup", func(t *testing.T) {
		db := newDB(t, bindSchema("cmpcleanup"))
		sessions := newCompactionSessionLog(t, db)
		events := postgres.NewEventLog(db, postgres.WithEventLogCapacity(1))
		schema := db.Schema()

		seedSession(t, ctx, sessions, "s2")
		publicationID := stores.CompactionPublicationID("s2", "run-s2", 1)
		if inserted, err := events.AppendCompactionEvent(ctx, publicationID, "run-s2", compactionEvent("s2", publicationID, "policy-1")); err != nil || !inserted {
			t.Fatalf("session publication insert: %v inserted=%v", err, inserted)
		}
		if got := count(t, db, `SELECT count(*) FROM `+schema+`.event_compaction_publication WHERE session_id = 's2'`); got != 1 {
			t.Fatalf("tombstone rows before delete = %d: want 1", got)
		}
		if err := sessions.Delete(ctx, "s2"); err != nil {
			t.Fatal(err)
		}
		if got := count(t, db, `SELECT count(*) FROM `+schema+`.event_compaction_publication WHERE session_id = 's2'`); got != 0 {
			t.Fatalf("tombstone rows after delete = %d: want 0", got)
		}

		seedSession(t, ctx, sessions, "s1")
		if err := sessions.UpdateSession(ctx, "s1", stores.SessionPatch{Hold: &publicationID}); err != nil {
			t.Fatal(err)
		}
		heldID := stores.CompactionPublicationID("s1", "run-held", 1)
		if inserted, err := events.AppendCompactionEvent(ctx, heldID, "run-held", compactionEvent("s1", heldID, "policy-1")); err != nil || !inserted {
			t.Fatalf("held publication insert: %v inserted=%v", err, inserted)
		}
		if err := sessions.Delete(ctx, "s1"); !errors.Is(err, types.ErrSessionHeld) {
			t.Fatalf("delete held session = %v, want ErrSessionHeld", err)
		}
		if got := count(t, db, `SELECT count(*) FROM `+schema+`.event_compaction_publication WHERE session_id = 's1'`); got != 1 {
			t.Fatalf("held tombstone rows = %d: want 1", got)
		}
	})

	// A failed write never leaves a committed tombstone behind: a canceled
	// context rolls the tombstone and the event back together.
	t.Run("context.compaction-publication-once/rollback", func(t *testing.T) {
		db := newDB(t, bindSchema("cmprollback"), postgres.WithClock(func() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) }))
		events := postgres.NewEventLog(db, postgres.WithEventLogCapacity(1))
		schema := db.Schema()

		ctx2, cancel := context.WithCancel(ctx)
		publicationID := stores.CompactionPublicationID("s1", "run-rollback", 3)
		cancel()
		if _, err := events.AppendCompactionEvent(ctx2, publicationID, "run-rollback", compactionEvent("s1", publicationID, "policy-1")); err == nil {
			t.Fatal("canceled append accepted")
		}
		if got := count(t, db, `SELECT count(*) FROM `+schema+`.event_compaction_publication WHERE run_id = 'run-rollback'`); got != 0 {
			t.Fatalf("tombstone rows after canceled append = %d: want 0", got)
		}
		if got := count(t, db, `SELECT count(*) FROM `+schema+`.event_seq WHERE run_id = 'run-rollback'`); got != 0 {
			t.Fatalf("sequence rows after canceled append = %d: want 0", got)
		}
	})
}

func TestAuditCompactionHash(t *testing.T) {
	ctx := context.Background()

	t.Run("context.compaction-audit-hash", func(t *testing.T) {
		stamp := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
		// Each store gets its own advancing clock so the fixtures stamp
		// identical instants per append while the purge cutoff can
		// single out the tampered record inside the same chain.
		newTicker := func() func() time.Time {
			now := stamp
			return func() time.Time { t := now; now = now.Add(time.Second); return t }
		}
		db := newDB(t, bindSchema("cmphash"), postgres.WithClock(newTicker()))
		if _, err := postgres.Maintain(ctx, db.Pool(), postgres.MaintainOptions{Schema: db.Schema(), WindowDays: 2, Now: func() time.Time { return stamp }}); err != nil {
			t.Fatal(err)
		}
		pg := postgres.NewAuditLog(db)
		schema := db.Schema()
		mem := stores.NewMemoryAuditLog(stores.WithMemoryAuditClock(newTicker()))

		legacy := stores.AuditRecord{
			Kind:      stores.AuditModelCall,
			SessionID: "s1",
			Tenant:    compactionBindTenant,
			Subject:   "u1",
		}
		if err := mem.Append(ctx, legacy); err != nil {
			t.Fatal(err)
		}
		if err := pg.Append(ctx, legacy); err != nil {
			t.Fatal(err)
		}
		memLegacy, err := mem.StoredRecord(ctx, "audit/"+compactionBindTenant+"/0")
		if err != nil {
			t.Fatal(err)
		}
		var pgLegacyRaw []byte
		if err := db.Pool().QueryRow(ctx, `SELECT payload FROM `+schema+`.audit_record WHERE session_id = 's1' AND seq = 1`).Scan(&pgLegacyRaw); err != nil {
			t.Fatal(err)
		}
		if string(memLegacy) != string(pgLegacyRaw) {
			t.Fatal("legacy nil-payload envelope bytes differ between memory and postgres")
		}

		compacted := stores.AuditRecord{
			Kind:      stores.AuditCompacted,
			SessionID: "s1",
			RunID:     "run-1",
			RootRunID: "root-1",
			Flow:      "flow-1",
			Tenant:    compactionBindTenant,
			Subject:   "u1",
			Compaction: &stores.CompactionAudit{
				PublicationID: stores.CompactionPublicationID("s1", "run-1", 3),
				FromVersion:   3,
				ToVersion:     4,
				TokensBefore:  900,
				TokensAfter:   40,
				Policy:        "policy-1",
				SummarySHA:    "abc",
			},
		}
		inserted, err := pg.AppendCompactionAudit(ctx, compacted)
		if err != nil || !inserted {
			t.Fatalf("postgres compaction seal: %v inserted=%v", err, inserted)
		}
		if err := mem.Append(ctx, compacted); err != nil {
			t.Fatal(err)
		}
		var memHash, pgHash string
		for rec, err := range mem.Read(ctx, "s1") {
			if err != nil {
				t.Fatal(err)
			}
			if rec.Kind == stores.AuditCompacted {
				memHash = rec.Hash
			}
		}
		for rec, err := range pg.Read(ctx, "s1") {
			if err != nil {
				t.Fatal(err)
			}
			if rec.Kind == stores.AuditCompacted {
				pgHash = rec.Hash
			}
		}
		if memHash == "" || memHash != pgHash {
			t.Fatalf("compaction hash mismatch: memory %q postgres %q", memHash, pgHash)
		}

		// The same payload hashes identically after a caller mutation
		// attempt: the stored copy is independent.
		compacted.Compaction.Policy = "mutated"
		for rec, err := range pg.Read(ctx, "s1") {
			if err != nil {
				t.Fatal(err)
			}
			if rec.Kind == stores.AuditCompacted && rec.Hash != pgHash {
				t.Fatalf("stored payload moved with the caller's copy: %q want %q", rec.Hash, pgHash)
			}
		}

		// Tamper with the sealed legacy payload, then drop the tampered
		// record with a cutoff between the two append instants and let
		// the purge reseal the surviving compaction record: the
		// resealed chain still verifies through the shared hash helper.
		if _, err := db.Pool().Exec(ctx, `UPDATE `+schema+`.audit_record SET payload = payload || ' ' WHERE session_id = 's1' AND seq = 1`); err != nil {
			t.Fatal(err)
		}
		removed, err := pg.Purge(ctx, compactionBindTenant, stamp.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if removed == 0 {
			t.Fatal("purge removed nothing: fixture did not tamper")
		}
		var prev string
		for rec, err := range pg.Read(ctx, "s1") {
			if err != nil {
				t.Fatal(err)
			}
			if rec.PrevHash != prev {
				t.Fatalf("resealed chain break: PrevHash %q want %q", rec.PrevHash, prev)
			}
			prev = rec.Hash
		}
		if prev == "" {
			t.Fatal("resealed chain holds no records")
		}
	})
}
