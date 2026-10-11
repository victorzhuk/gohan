package stores

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"testing"
	"time"
)

// legacyAuditHash recomputes the historical seal bytes: SHA-256 over the
// previous hash and the record's fields in the fixed order, with no
// compaction segment. It exists to pin byte identity of nil-payload hashes
// for context.compaction-audit-hash.
func legacyAuditHash(r AuditRecord) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%d\n%s\n%s\n%s\n%s\n%s\n%d\n%s\n%d\n%d\n%s\n%s\n%s\n%s\n",
		r.Kind, r.At.UTC().Format(time.RFC3339Nano), r.SessionID, r.RunID, r.RootRunID,
		r.Flow, r.Subject, r.Tenant, r.Tool, int(r.Effect), r.ArgsChecksum, r.Decision,
		r.Approver, strconv.FormatFloat(r.Confidence, 'b', -1, 64), r.Verdict, int(r.Outcome), r.ResultSHA,
		r.ResultBytes, int(r.Stage), r.Model, r.ModelVersion, r.ManifestHash, r.PrevHash)
	return hex.EncodeToString(h.Sum(nil))
}

// TestAuditCompactionHashMemory pins the audit seal for
// context.compaction-audit-hash: legacy bytes stay identical for a nil
// payload, and a populated payload changes the seal through the shared
// CompactionAuditHashBytes segment at append, reseal, and verification.
func TestAuditCompactionHashMemory(t *testing.T) {
	const scenario = "context.compaction-audit-hash"
	ctx := context.Background()

	t.Run(scenario+"/nil-payload-legacy-bytes-identical", func(t *testing.T) {
		base := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
		now := base
		audit := NewMemoryAuditLog(WithMemoryAuditClock(func() time.Time { now = now.Add(time.Second); return now }))
		if err := audit.Append(ctx, AuditRecord{Kind: AuditRunStarted, SessionID: "s-legacy", Tenant: "t", ManifestHash: "m1", ModelVersion: "v1"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		var stored AuditRecord
		for rec, err := range audit.Read(ctx, "s-legacy") {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			stored = rec
		}
		if stored.Compaction != nil {
			t.Fatalf("payload = %+v, want nil", stored.Compaction)
		}
		if want := legacyAuditHash(stored); stored.Hash != want {
			t.Fatalf("Hash = %q, want legacy bytes %q", stored.Hash, want)
		}
	})

	t.Run(scenario+"/nil-payload-reseal-stays-legacy", func(t *testing.T) {
		now := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
		audit := NewMemoryAuditLog(WithMemoryAuditClock(func() time.Time { return now }))
		if err := audit.Append(ctx, AuditRecord{Kind: AuditRunStarted, SessionID: "s-r", Tenant: "t", Reason: "old"}); err != nil {
			t.Fatalf("Append(old): %v", err)
		}
		now = now.Add(time.Hour)
		if err := audit.Append(ctx, AuditRecord{Kind: AuditRunFinished, SessionID: "s-r", Tenant: "t", Reason: "new"}); err != nil {
			t.Fatalf("Append(new): %v", err)
		}
		if _, err := audit.Purge(ctx, "t", now); err != nil {
			t.Fatalf("Purge: %v", err)
		}
		for rec, err := range audit.Read(ctx, "s-r") {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if rec.PrevHash != "" {
				t.Fatalf("PrevHash = %q, want the reseal to reset it", rec.PrevHash)
			}
			if want := legacyAuditHash(rec); rec.Hash != want {
				t.Fatalf("reseal Hash = %q, want legacy bytes %q", rec.Hash, want)
			}
		}
	})

	t.Run(scenario, func(t *testing.T) {
		now := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
		audit := NewMemoryAuditLog(WithMemoryAuditClock(func() time.Time { return now }))
		payload := &CompactionAudit{
			PublicationID: CompactionPublicationID("s-c", "r-c", 3),
			FromVersion:   3,
			ToVersion:     4,
			TokensBefore:  900,
			TokensAfter:   300,
			Policy:        "rolling",
			SummarySHA:    "abc123",
		}
		rec := AuditRecord{Kind: AuditCompacted, SessionID: "s-c", RunID: "r-c", Tenant: "t", Compaction: payload}
		inserted, err := audit.AppendCompactionAudit(ctx, rec)
		if err != nil || !inserted {
			t.Fatalf("AppendCompactionAudit = (%v, %v), want (true, nil)", inserted, err)
		}
		// Caller mutations after accept must not change stored facts or the chain.
		payload.TokensAfter = 999
		payload.SummarySHA = "tampered"

		read := func() AuditRecord {
			var got AuditRecord
			for r, err := range audit.Read(ctx, "s-c") {
				if err != nil {
					t.Fatalf("Read: %v", err)
				}
				got = r
			}
			return got
		}
		stored := read()
		if stored.Compaction == nil || stored.Compaction.TokensAfter != 300 || stored.Compaction.SummarySHA != "abc123" {
			t.Fatalf("stored payload = %+v, want the accepted fields after caller mutation", stored.Compaction)
		}
		// The seal covers the payload through the shared segment: the same
		// record with a nil payload seals to different legacy bytes.
		if got := auditHash(stored); stored.Hash != got {
			t.Fatalf("Hash = %q, want recomputed %q", stored.Hash, got)
		}
		legacyOnly := stored
		legacyOnly.Compaction = nil
		if stored.Hash == legacyAuditHash(legacyOnly) {
			t.Fatal("populated payload did not change the seal bytes")
		}
		// Mutating the read pointer must not change stored facts either.
		stored.Compaction.TokensAfter = 777
		again := read()
		if again.Compaction == nil || again.Compaction.TokensAfter != 300 {
			t.Fatalf("stored payload = %+v, want 300 after read-pointer mutation", again.Compaction)
		}
		if again.Hash != stored.Hash {
			t.Fatalf("Hash changed = %q, want %q", again.Hash, stored.Hash)
		}
	})

	t.Run(scenario+"/tamper-detected-by-recomputation", func(t *testing.T) {
		now := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
		audit := NewMemoryAuditLog(WithMemoryAuditClock(func() time.Time { return now }))
		payload := &CompactionAudit{
			PublicationID: CompactionPublicationID("s-t", "r-t", 3),
			FromVersion:   3,
			ToVersion:     4,
			Policy:        "rolling",
			SummarySHA:    "abc123",
		}
		if inserted, err := audit.AppendCompactionAudit(ctx, AuditRecord{Kind: AuditCompacted, SessionID: "s-t", Tenant: "t", Compaction: payload}); err != nil || !inserted {
			t.Fatalf("AppendCompactionAudit = (%v, %v), want (true, nil)", inserted, err)
		}
		var stored AuditRecord
		for rec, err := range audit.Read(ctx, "s-t") {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			stored = rec
		}
		// A historical fixture replaces the stored bytes with a tampered
		// payload; recomputation through the same shared helper must expose it.
		tampered := stored
		tampered.Compaction = CloneCompactionAudit(stored.Compaction)
		tampered.Compaction.SummarySHA = "tampered"
		tampered.Hash = stored.Hash
		if auditHash(tampered) == stored.Hash {
			t.Fatal("tampered payload resealed to the same hash")
		}
		restored := stored
		if auditHash(restored) != stored.Hash {
			t.Fatal("restored payload failed to reseal to the original hash")
		}
		// The stored record still verifies against the shared helper.
		if auditHash(stored) != stored.Hash {
			t.Fatalf("stored Hash = %q, want recomputed %q", stored.Hash, auditHash(stored))
		}
	})
}
