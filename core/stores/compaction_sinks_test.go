package stores

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// TestMemoryCompactionSinkAppend drives the optional compaction audit and
// event sinks through their public methods: identity-keyed once semantics
// for context.compaction-publication-once.
func TestMemoryCompactionSinkAppend(t *testing.T) {
	const scenario = "context.compaction-publication-once"
	ctx := context.Background()

	auditRecord := func() AuditRecord {
		return AuditRecord{
			Kind:      AuditCompacted,
			SessionID: "s-1",
			RunID:     "r-1",
			Tenant:    "t-1",
			Subject:   "publisher",
			Compaction: &CompactionAudit{
				PublicationID: CompactionPublicationID("s-1", "r-1", 3),
				FromVersion:   3,
				ToVersion:     4,
				TokensBefore:  900,
				TokensAfter:   300,
				Policy:        "rolling",
				SummarySHA:    "abc123",
			},
		}
	}

	t.Run(scenario+"/audit-new-seals-one-record", func(t *testing.T) {
		audit := NewMemoryAuditLog()
		inserted, err := audit.AppendCompactionAudit(ctx, auditRecord())
		if err != nil || !inserted {
			t.Fatalf("AppendCompactionAudit = (%v, %v), want (true, nil)", inserted, err)
		}
		var got []AuditRecord
		for rec, err := range audit.Read(ctx, "s-1") {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			got = append(got, rec)
		}
		if len(got) != 1 {
			t.Fatalf("len(records) = %d, want 1", len(got))
		}
		if got[0].Kind != AuditCompacted || got[0].Compaction == nil {
			t.Fatalf("record = %+v, want an AuditCompacted record with a payload", got[0])
		}
		if got[0].Compaction.FromVersion != 3 || got[0].Compaction.ToVersion != 4 || got[0].Compaction.SummarySHA != "abc123" {
			t.Fatalf("payload = %+v, want the accepted compaction fields", got[0].Compaction)
		}
		if got[0].PrevHash != "" || got[0].Hash == "" {
			t.Fatalf("chain = {%q, %q}, want genesis and a seal", got[0].PrevHash, got[0].Hash)
		}
	})

	t.Run(scenario+"/audit-identical-duplicate-noop", func(t *testing.T) {
		audit := NewMemoryAuditLog()
		if inserted, err := audit.AppendCompactionAudit(ctx, auditRecord()); err != nil || !inserted {
			t.Fatalf("first append = (%v, %v), want (true, nil)", inserted, err)
		}
		dup := auditRecord()
		dup.At = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
		dup.PrevHash = "forged"
		dup.Hash = "forged"
		inserted, err := audit.AppendCompactionAudit(ctx, dup)
		if err != nil || inserted {
			t.Fatalf("duplicate = (%v, %v), want (false, nil)", inserted, err)
		}
		var count int
		var stored AuditRecord
		for rec, err := range audit.Read(ctx, "s-1") {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			count++
			stored = rec
		}
		if count != 1 {
			t.Fatalf("record count = %d, want 1", count)
		}
		if stored.Hash == "forged" || stored.At.Equal(dup.At) {
			t.Fatal("duplicate append replaced the sealed record")
		}
	})

	t.Run(scenario+"/audit-conflict-refused-without-mutation", func(t *testing.T) {
		audit := NewMemoryAuditLog()
		if inserted, err := audit.AppendCompactionAudit(ctx, auditRecord()); err != nil || !inserted {
			t.Fatalf("first append = (%v, %v), want (true, nil)", inserted, err)
		}
		var first AuditRecord
		for rec, err := range audit.Read(ctx, "s-1") {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			first = rec
		}
		conflict := auditRecord()
		conflict.Compaction.SummarySHA = "different"
		inserted, err := audit.AppendCompactionAudit(ctx, conflict)
		if inserted || !errors.Is(err, errCompactionConflict) {
			t.Fatalf("conflict = (%v, %v), want (false, errCompactionConflict)", inserted, err)
		}
		if errors.Is(err, types.ErrVersionConflict) {
			t.Fatal("conflict wrapped types.ErrVersionConflict")
		}
		var after AuditRecord
		for rec, err := range audit.Read(ctx, "s-1") {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			after = rec
		}
		if after.Hash != first.Hash {
			t.Fatal("conflicting append mutated the sealed record")
		}
	})

	t.Run(scenario+"/audit-concurrent-duplicates-insert-once", func(t *testing.T) {
		audit := NewMemoryAuditLog()
		var wg sync.WaitGroup
		results := make([]bool, 8)
		errs := make([]error, 8)
		for i := range 8 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i], errs[i] = audit.AppendCompactionAudit(ctx, auditRecord())
			}(i)
		}
		wg.Wait()
		inserted := 0
		for i := range 8 {
			if errs[i] != nil {
				t.Fatalf("writer %d = %v, want nil", i, errs[i])
			}
			if results[i] {
				inserted++
			}
		}
		if inserted != 1 {
			t.Fatalf("inserted = %d, want 1", inserted)
		}
		var count int
		for _, err := range audit.Read(ctx, "s-1") {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			count++
		}
		if count != 1 {
			t.Fatalf("record count = %d, want 1", count)
		}
	})

	t.Run(scenario+"/audit-invalid-payload-refused-before-lookup", func(t *testing.T) {
		audit := NewMemoryAuditLog()
		bad := auditRecord()
		bad.Compaction = nil
		if inserted, err := audit.AppendCompactionAudit(ctx, bad); inserted || !errors.Is(err, errCompactionPayload) {
			t.Fatalf("nil payload = (%v, %v), want (false, errCompactionPayload)", inserted, err)
		}
		bad = auditRecord()
		bad.Compaction.PublicationID = ""
		if inserted, err := audit.AppendCompactionAudit(ctx, bad); inserted || !errors.Is(err, errCompactionPayload) {
			t.Fatalf("empty publication = (%v, %v), want (false, errCompactionPayload)", inserted, err)
		}
		var count int
		for _, err := range audit.Read(ctx, "s-1") {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			count++
		}
		if count != 0 {
			t.Fatalf("record count = %d, want 0", count)
		}
	})

	compactionEvent := func(pubID string) Event {
		return Event{
			Payload: types.Compacted{
				FromVersion:   3,
				ToVersion:     4,
				TokensBefore:  900,
				TokensAfter:   300,
				Policy:        "rolling",
				PublicationID: pubID,
			},
			Meta: types.EventMeta{SessionID: "s-1"},
		}
	}

	t.Run(scenario+"/event-new-commits-once", func(t *testing.T) {
		events := NewMemoryEventLog()
		pubID := CompactionPublicationID("s-1", "r-1", 3)
		inserted, err := events.AppendCompactionEvent(ctx, pubID, "r-1", compactionEvent(pubID))
		if err != nil || !inserted {
			t.Fatalf("AppendCompactionEvent = (%v, %v), want (true, nil)", inserted, err)
		}
		var got []Event
		for e, err := range events.Read(ctx, "r-1", 0) {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			got = append(got, e)
		}
		if len(got) != 1 || got[0].Meta.Seq != 1 {
			t.Fatalf("events = %+v, want one at Seq 1", got)
		}
		if got[0].Meta.RunID != "r-1" {
			t.Fatalf("Meta.RunID = %q, want the run partition", got[0].Meta.RunID)
		}
		if c, ok := got[0].Payload.(types.Compacted); !ok || c.PublicationID != pubID {
			t.Fatalf("payload = %+v, want the accepted Compacted publication", got[0].Payload)
		}
	})

	t.Run(scenario+"/event-identical-duplicate-noop", func(t *testing.T) {
		events := NewMemoryEventLog()
		pubID := CompactionPublicationID("s-1", "r-1", 3)
		if inserted, err := events.AppendCompactionEvent(ctx, pubID, "r-1", compactionEvent(pubID)); err != nil || !inserted {
			t.Fatalf("first append = (%v, %v), want (true, nil)", inserted, err)
		}
		dup := compactionEvent(pubID)
		dup.Meta.Time = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
		dup.Meta.Seq = 99
		if inserted, err := events.AppendCompactionEvent(ctx, pubID, "r-1", dup); err != nil || inserted {
			t.Fatalf("duplicate = (%v, %v), want (false, nil)", inserted, err)
		}
		if err := events.Append(ctx, "r-1", Event{Payload: types.Done{}}); err != nil {
			t.Fatalf("ordinary Append: %v", err)
		}
		var seqs []int64
		for e, err := range events.Read(ctx, "r-1", 0) {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			seqs = append(seqs, e.Meta.Seq)
		}
		if len(seqs) != 2 || seqs[0] != 1 || seqs[1] != 2 {
			t.Fatalf("seqs = %v, want [1 2]: the duplicate consumed no sequence", seqs)
		}
	})

	t.Run(scenario+"/event-conflict-refused-without-mutation", func(t *testing.T) {
		events := NewMemoryEventLog()
		pubID := CompactionPublicationID("s-1", "r-1", 3)
		if inserted, err := events.AppendCompactionEvent(ctx, pubID, "r-1", compactionEvent(pubID)); err != nil || !inserted {
			t.Fatalf("first append = (%v, %v), want (true, nil)", inserted, err)
		}
		conflict := compactionEvent(pubID)
		changed := conflict.Payload.(types.Compacted)
		changed.ToVersion = 9
		conflict.Payload = changed
		inserted, err := events.AppendCompactionEvent(ctx, pubID, "r-1", conflict)
		if inserted || !errors.Is(err, errCompactionConflict) {
			t.Fatalf("conflict = (%v, %v), want (false, errCompactionConflict)", inserted, err)
		}
		var count int
		for _, err := range events.Read(ctx, "r-1", 0) {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			count++
		}
		if count != 1 {
			t.Fatalf("event count = %d, want 1", count)
		}
	})

	t.Run(scenario+"/event-duplicate-survives-ring-overwrite", func(t *testing.T) {
		events := NewMemoryEventLog(WithMemoryEventLogCapacity(1))
		pubID := CompactionPublicationID("s-1", "r-1", 3)
		if inserted, err := events.AppendCompactionEvent(ctx, pubID, "r-1", compactionEvent(pubID)); err != nil || !inserted {
			t.Fatalf("first append = (%v, %v), want (true, nil)", inserted, err)
		}
		if err := events.Append(ctx, "r-1", Event{Payload: types.Done{}}); err != nil {
			t.Fatalf("ordinary Append: %v", err)
		}
		inserted, err := events.AppendCompactionEvent(ctx, pubID, "r-1", compactionEvent(pubID))
		if err != nil || inserted {
			t.Fatalf("duplicate after overwrite = (%v, %v), want (false, nil)", inserted, err)
		}
		var kinds []string
		for e, err := range events.Read(ctx, "r-1", 1) {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if e.Meta.Seq != 2 {
				t.Fatalf("seq = %d, want 2: the duplicate must not consume a sequence", e.Meta.Seq)
			}
			kinds = append(kinds, eventKindForTest(e))
		}
		if len(kinds) != 1 || kinds[0] != "done" {
			t.Fatalf("kinds = %v, want [done]: the duplicate must not restore the evicted event", kinds)
		}
	})

	t.Run(scenario+"/event-duplicate-survives-expire", func(t *testing.T) {
		now := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
		events := NewMemoryEventLog(WithMemoryEventLogClock(func() time.Time { now = now.Add(time.Minute); return now }))
		pubID := CompactionPublicationID("s-1", "r-1", 3)
		if inserted, err := events.AppendCompactionEvent(ctx, pubID, "r-1", compactionEvent(pubID)); err != nil || !inserted {
			t.Fatalf("first append = (%v, %v), want (true, nil)", inserted, err)
		}
		if err := events.Expire(ctx, now.Add(24*time.Hour)); err != nil {
			t.Fatalf("Expire: %v", err)
		}
		inserted, err := events.AppendCompactionEvent(ctx, pubID, "r-1", compactionEvent(pubID))
		if err != nil || inserted {
			t.Fatalf("duplicate after expire = (%v, %v), want (false, nil)", inserted, err)
		}
		if err := events.Append(ctx, "r-1", Event{Payload: types.Done{}}); err != nil {
			t.Fatalf("ordinary Append: %v", err)
		}
		for e, err := range events.Read(ctx, "r-1", 1) {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if e.Meta.Seq != 2 {
				t.Fatalf("seq = %d, want 2: the duplicate consumed no sequence", e.Meta.Seq)
			}
		}
	})

	t.Run(scenario+"/event-concurrent-duplicates-insert-once", func(t *testing.T) {
		events := NewMemoryEventLog()
		pubID := CompactionPublicationID("s-1", "r-1", 3)
		var wg sync.WaitGroup
		results := make([]bool, 8)
		errs := make([]error, 8)
		for i := range 8 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i], errs[i] = events.AppendCompactionEvent(ctx, pubID, "r-1", compactionEvent(pubID))
			}(i)
		}
		wg.Wait()
		inserted := 0
		for i := range 8 {
			if errs[i] != nil {
				t.Fatalf("writer %d = %v, want nil", i, errs[i])
			}
			if results[i] {
				inserted++
			}
		}
		if inserted != 1 {
			t.Fatalf("inserted = %d, want 1", inserted)
		}
		var count int
		for _, err := range events.Read(ctx, "r-1", 0) {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			count++
		}
		if count != 1 {
			t.Fatalf("event count = %d, want 1", count)
		}
	})

	t.Run(scenario+"/event-invalid-payload-refused", func(t *testing.T) {
		events := NewMemoryEventLog()
		if inserted, err := events.AppendCompactionEvent(ctx, "", "r-1", compactionEvent("x")); inserted || !errors.Is(err, errCompactionPayload) {
			t.Fatalf("empty publication = (%v, %v), want (false, errCompactionPayload)", inserted, err)
		}
		bad := compactionEvent("declared")
		bad.Payload = types.Done{}
		if inserted, err := events.AppendCompactionEvent(ctx, "declared", "r-1", bad); inserted || !errors.Is(err, errCompactionPayload) {
			t.Fatalf("wrong payload = (%v, %v), want (false, errCompactionPayload)", inserted, err)
		}
		bad = compactionEvent("declared")
		if inserted, err := events.AppendCompactionEvent(ctx, "other", "r-1", bad); inserted || !errors.Is(err, errCompactionPayload) {
			t.Fatalf("mismatched publication = (%v, %v), want (false, errCompactionPayload)", inserted, err)
		}
		var count int
		for _, err := range events.Read(ctx, "r-1", 0) {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			count++
		}
		if count != 0 {
			t.Fatalf("event count = %d, want 0", count)
		}
	})
}

func eventKindForTest(e Event) string {
	kind, err := eventKind(e.Payload)
	if err != nil {
		return fmt.Sprintf("unknown(%T)", e.Payload)
	}
	return kind
}
