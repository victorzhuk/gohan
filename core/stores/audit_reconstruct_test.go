package stores

import (
	"context"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type trailAudit struct {
	mu   sync.Mutex
	recs []AuditRecord
}

func (a *trailAudit) Append(_ context.Context, r AuditRecord) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	prev := ""
	if len(a.recs) > 0 {
		prev = a.recs[len(a.recs)-1].Hash
	}
	r.PrevHash = prev
	r.Hash = auditHash(r)
	a.recs = append(a.recs, r)
	return nil
}

func (a *trailAudit) Read(_ context.Context, _ string) iter.Seq2[AuditRecord, error] {
	return func(yield func(AuditRecord, error) bool) {
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, r := range a.recs {
			if !yield(r, nil) {
				return
			}
		}
	}
}

func (a *trailAudit) Purge(_ context.Context, _ string, _ time.Time) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := len(a.recs)
	a.recs = nil
	return n, nil
}

func TestAuditReconstruct(t *testing.T) {
	base := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	tick := func(n int) time.Time { return base.Add(time.Duration(n) * time.Second) }

	session := func(t *testing.T) *MemorySessionLog {
		t.Helper()
		return NewMemorySessionLog(WithSessionPrincipals(func(context.Context) (types.Principal, bool) {
			return types.Principal{Tenant: "acme", Subject: "u1"}, true
		}))
	}

	t.Run("stores.decision-trail", func(t *testing.T) {
		ctx := context.Background()
		slog := session(t)
		if _, err := slog.Append(ctx, "s-1", 0, types.Message{}); err != nil {
			t.Fatalf("session append: %v", err)
		}
		audit := &trailAudit{}
		recs := []AuditRecord{
			{Kind: AuditRunStarted, At: tick(0), SessionID: "s-1", RunID: "r-1", ManifestHash: "m1", ModelVersion: "v1"},
			{Kind: AuditToolDecision, At: tick(1), SessionID: "s-1", RunID: "r-1", Tool: "net", Decision: "denied", ManifestHash: "m1", ModelVersion: "v1"},
			{Kind: AuditToolDecision, At: tick(2), SessionID: "s-1", RunID: "r-1", Tool: "write", Decision: "ask", ManifestHash: "m1", ModelVersion: "v1"},
			{Kind: AuditResumed, At: tick(3), SessionID: "s-1", RunID: "r-1", Approver: "op-7", Verdict: "approve", ManifestHash: "m1", ModelVersion: "v1"},
			{Kind: AuditToolOutcome, At: tick(4), SessionID: "s-1", RunID: "r-1", Tool: "write", Outcome: types.Succeeded, ResultSHA: "abc123", ResultBytes: 51200, ManifestHash: "m1", ModelVersion: "v1"},
			{Kind: AuditRunFinished, At: tick(5), SessionID: "s-1", RunID: "r-1", ManifestHash: "m1", ModelVersion: "v1"},
		}
		for _, r := range recs {
			if err := audit.Append(ctx, r); err != nil {
				t.Fatalf("audit append %s: %v", r.Kind, err)
			}
		}

		trail, err := Reconstruct(ctx, audit, slog, "s-1")
		if err != nil {
			t.Fatalf("Reconstruct: %v", err)
		}
		if trail.Owner != (types.SessionOwner{Tenant: "acme", Subject: "u1"}) {
			t.Fatalf("owner = %+v", trail.Owner)
		}
		if len(trail.Records) != len(recs) {
			t.Fatalf("records = %d, want %d", len(trail.Records), len(recs))
		}
		wantKinds := []AuditKind{AuditRunStarted, AuditToolDecision, AuditToolDecision, AuditResumed, AuditToolOutcome, AuditRunFinished}
		for i, tr := range trail.Records {
			if tr.Seq != i+1 {
				t.Fatalf("record %d seq = %d", i, tr.Seq)
			}
			if tr.Rec.Kind != wantKinds[i] {
				t.Fatalf("record %d kind = %s, want %s", i, tr.Rec.Kind, wantKinds[i])
			}
			if tr.Rec.ManifestHash == "" || tr.Rec.ModelVersion == "" {
				t.Fatalf("record %d missing manifest or model version", i)
			}
		}
		if trail.Broken != nil {
			t.Fatalf("unexpected chain break: %+v", trail.Broken)
		}
		denied := trail.Records[1].Rec
		if denied.Decision != "denied" || denied.Tool != "net" {
			t.Fatalf("denial = %+v", denied)
		}
		ask := trail.Records[2].Rec
		if ask.Decision != "ask" {
			t.Fatalf("ask = %+v", ask)
		}
		resumed := trail.Records[3].Rec
		if resumed.Approver != "op-7" || resumed.Verdict != "approve" {
			t.Fatalf("resumed = %+v", resumed)
		}
		outcome := trail.Records[4].Rec
		if outcome.ResultSHA != "abc123" || outcome.ResultBytes != 51200 {
			t.Fatalf("outcome = %+v", outcome)
		}
	})

	t.Run("stores.hash-chain", func(t *testing.T) {
		ctx := context.Background()
		slog := session(t)
		if _, err := slog.Append(ctx, "s-2", 0, types.Message{}); err != nil {
			t.Fatalf("session append: %v", err)
		}
		audit := &trailAudit{}
		for i, kind := range []AuditKind{AuditRunStarted, AuditToolDecision, AuditRunFinished} {
			if err := audit.Append(ctx, AuditRecord{Kind: kind, At: tick(i), SessionID: "s-2", RunID: "r-2"}); err != nil {
				t.Fatalf("audit append %s: %v", kind, err)
			}
		}
		audit.mu.Lock()
		audit.recs[1].Decision = "forged"
		audit.mu.Unlock()

		trail, err := Reconstruct(ctx, audit, slog, "s-2")
		if err != nil {
			t.Fatalf("Reconstruct: %v", err)
		}
		if trail.Broken == nil {
			t.Fatal("expected a chain break, got none")
		}
		if trail.Broken.Seq != 2 {
			t.Fatalf("break seq = %d, want 2", trail.Broken.Seq)
		}
		if trail.Broken.Kind != AuditToolDecision {
			t.Fatalf("break kind = %s, want %s", trail.Broken.Kind, AuditToolDecision)
		}
	})
}
