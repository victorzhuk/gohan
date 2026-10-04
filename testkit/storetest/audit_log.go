package storetest

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// AuditKind mirrors the port's kind vocabulary without naming a particular
// implementation's type.
type AuditKind string

// AuditRecord is the port-level shape of one decision-trail entry. It
// carries checksums and sizes of args and results, never their content.
type AuditRecord struct {
	Kind         AuditKind
	At           time.Time
	SessionID    string
	RunID        string
	RootRunID    string
	Flow         string
	Subject      string
	Tenant       string
	Tool         string
	Effect       types.Effect
	ArgsChecksum string
	Decision     string
	Confidence   float64
	Approver     string
	Verdict      string
	Outcome      types.Outcome
	ResultSHA    string
	ResultBytes  int
	Stage        types.GuardStage
	Model        string
	ModelVersion string
	ManifestHash string
	PrevHash     string
	Hash         string
}

// AuditStore is the AuditLog port as the conformance suite sees it.
type AuditStore interface {
	Append(ctx context.Context, r AuditRecord) error
	Read(ctx context.Context, sessionID string) iter.Seq2[AuditRecord, error]
	Purge(ctx context.Context, tenant string, olderThan time.Time) (int, error)
}

// AuditFactory returns a fresh, empty audit log.
type AuditFactory func(t *testing.T) AuditStore

// AuditLog runs the AuditLog conformance suite against the implementation
// produced by newLog. It covers the per-session hash chain, content-free
// records, ordered reads and tenant- and time-scoped Purge.
func AuditLog(t *testing.T, newLog AuditFactory) {
	t.Run("chain integrity", func(t *testing.T) {
		log := newLog(t)
		ctx := context.Background()
		for range 3 {
			for _, sid := range []string{"s1", "s2"} {
				if err := log.Append(ctx, AuditRecord{
					Kind:      AuditKind("run.started"),
					SessionID: sid,
					Tenant:    "acme",
					Tool:      "search",
				}); err != nil {
					t.Fatalf("Append: %v", err)
				}
			}
		}

		seen := map[string][]AuditRecord{}
		for _, sid := range []string{"s1", "s2"} {
			seen[sid] = allRecords(t, log.Read(ctx, sid))
		}

		for sid, recs := range seen {
			if len(recs) != 3 {
				t.Fatalf("session %s: got %d records, want 3", sid, len(recs))
			}
			if recs[0].PrevHash != "" {
				t.Fatalf("session %s: first PrevHash = %q, want empty", sid, recs[0].PrevHash)
			}
			for i, rec := range recs {
				if rec.Hash == "" {
					t.Fatalf("session %s record %d: empty Hash", sid, i)
				}
				if rec.At.IsZero() {
					t.Fatalf("session %s record %d: store clock did not set At", sid, i)
				}
				if i > 0 {
					prev := recs[i-1]
					if rec.PrevHash != prev.Hash {
						t.Fatalf("session %s record %d: PrevHash = %q, want previous Hash %q", sid, i, rec.PrevHash, prev.Hash)
					}
					if rec.At.Before(prev.At) {
						t.Fatalf("session %s record %d: At %v before previous %v", sid, i, rec.At, prev.At)
					}
				}
			}
		}
		if seen["s1"][0].Hash == seen["s2"][0].Hash {
			t.Fatal("identical records in different sessions sealed to the same Hash")
		}

		for _, sid := range []string{"s1", "s2"} {
			again := allRecords(t, log.Read(ctx, sid))
			for i, rec := range again {
				if rec.Hash != seen[sid][i].Hash || rec.PrevHash != seen[sid][i].PrevHash {
					t.Fatalf("session %s record %d: chain fields changed between reads", sid, i)
				}
			}
		}
	})

	t.Run("ordered reads", func(t *testing.T) {
		log := newLog(t)
		ctx := context.Background()
		kinds := []string{"run.started", "model.call", "run.finished"}
		for _, sid := range []string{"s1", "s2"} {
			for i, kind := range kinds {
				if err := log.Append(ctx, AuditRecord{
					Kind:      AuditKind(kind),
					SessionID: sid,
					Tenant:    "acme",
					Tool:      string(rune('a' + i)),
				}); err != nil {
					t.Fatalf("Append: %v", err)
				}
			}
		}

		for _, sid := range []string{"s1", "s2"} {
			got := allRecords(t, log.Read(ctx, sid))
			if len(got) != 3 {
				t.Fatalf("session %s: got %d records, want 3", sid, len(got))
			}
			for i := 1; i < len(got); i++ {
				if got[i].At.Before(got[i-1].At) {
					t.Fatalf("session %s record %d At %v before record %d At %v", sid, i, got[i].At, i-1, got[i-1].At)
				}
				if got[i].Kind == got[i-1].Kind {
					t.Fatalf("session %s: records out of append order", sid)
				}
			}
		}
	})

	t.Run("record carries no content", func(t *testing.T) {
		log := newLog(t)
		ctx := context.Background()
		in := AuditRecord{
			Kind:         AuditKind("tool.decision"),
			SessionID:    "s1",
			RunID:        "r1",
			RootRunID:    "r0",
			Flow:         "main",
			Subject:      "alice",
			Tenant:       "acme",
			Tool:         "search",
			Effect:       types.SideEffect,
			ArgsChecksum: "argsum-1",
			Decision:     "allow",
			Confidence:   0.75,
			Approver:     "bob",
			Verdict:      "granted",
			Outcome:      types.Succeeded,
			ResultSHA:    "resum-1",
			ResultBytes:  128,
			Stage:        types.StageInput,
			Model:        "m",
			ModelVersion: "v2",
			ManifestHash: "man-1",
		}
		if err := log.Append(ctx, in); err != nil {
			t.Fatalf("Append: %v", err)
		}
		recs := allRecords(t, log.Read(ctx, "s1"))
		if len(recs) != 1 {
			t.Fatalf("got %d records, want 1", len(recs))
		}
		want := in
		want.At = recs[0].At
		want.PrevHash = recs[0].PrevHash
		want.Hash = recs[0].Hash
		if recs[0] != want {
			t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", recs[0], want)
		}
		if recs[0].ArgsChecksum == "" || recs[0].ResultSHA == "" || recs[0].ResultBytes == 0 {
			t.Fatal("checksums and size not preserved")
		}

		other := in
		other.ArgsChecksum = "argsum-2"
		if err := log.Append(ctx, other); err != nil {
			t.Fatalf("Append: %v", err)
		}
		recs = allRecords(t, log.Read(ctx, "s1"))
		if len(recs) != 2 {
			t.Fatalf("got %d records, want 2", len(recs))
		}
		if recs[1].Hash == recs[0].Hash {
			t.Fatal("record with different ArgsChecksum sealed to the same Hash")
		}
	})

	t.Run("purge scoped by tenant and time", func(t *testing.T) {
		log := newLog(t)
		ctx := context.Background()
		for _, tenant := range []string{"acme", "globex"} {
			for range 4 {
				if err := log.Append(ctx, AuditRecord{
					Kind:      AuditKind("run.started"),
					SessionID: tenant,
					Tenant:    tenant,
				}); err != nil {
					t.Fatalf("Append: %v", err)
				}
			}
		}

		acme := allRecords(t, log.Read(ctx, "acme"))
		globex := allRecords(t, log.Read(ctx, "globex"))
		cutoff := acme[1].At

		n, err := log.Purge(ctx, "acme", cutoff)
		if err != nil {
			t.Fatalf("Purge: %v", err)
		}
		if n != 1 {
			t.Fatalf("Purge removed %d records, want 1", n)
		}

		got := allRecords(t, log.Read(ctx, "acme"))
		if len(got) != 3 {
			t.Fatalf("got %d acme records, want 3", len(got))
		}
		if got[0].PrevHash != "" {
			t.Fatalf("surviving chain starts with PrevHash %q, want empty", got[0].PrevHash)
		}
		for i := 1; i < len(got); i++ {
			if got[i].PrevHash != got[i-1].Hash {
				t.Fatalf("surviving record %d: PrevHash does not link to previous survivor", i)
			}
		}

		if len(globex) != 4 {
			t.Fatalf("got %d globex records, want 4", len(globex))
		}
		if globex[0].PrevHash != "" || globex[1].PrevHash != globex[0].Hash {
			t.Fatal("purge of another tenant disturbed the globex chain")
		}

		n, err = log.Purge(ctx, "globex", globex[3].At.Add(time.Hour))
		if err != nil {
			t.Fatalf("Purge: %v", err)
		}
		if n != 4 {
			t.Fatalf("Purge removed %d records, want 4", n)
		}
		if got := allRecords(t, log.Read(ctx, "globex")); len(got) != 0 {
			t.Fatalf("globex kept %d records after a full purge", len(got))
		}

		n, err = log.Purge(ctx, "acme", acme[0].At.Add(-time.Hour))
		if err != nil {
			t.Fatalf("Purge: %v", err)
		}
		if n != 0 {
			t.Fatalf("Purge with past cutoff removed %d records, want 0", n)
		}
	})
}

func allRecords(t *testing.T, seq iter.Seq2[AuditRecord, error]) []AuditRecord {
	t.Helper()
	var out []AuditRecord
	for rec, err := range seq {
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		out = append(out, rec)
	}
	return out
}
