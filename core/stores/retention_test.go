package stores

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"
)

var retentionNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

type fakeSessionPurger struct {
	store   string
	records map[string]int
	purged  []string
}

func (f *fakeSessionPurger) purge(_ context.Context, _, sessionID string) (int, error) {
	n := f.records[sessionID]
	delete(f.records, sessionID)
	f.purged = append(f.purged, sessionID)
	return n, nil
}

type fakeWorkingStore struct {
	notes map[string][]time.Time
}

func (f *fakeWorkingStore) purge(_ context.Context, _ string, olderThan time.Time) (int, error) {
	var kept []time.Time
	purged := 0
	for _, at := range f.notes["t"] {
		if at.Before(olderThan) {
			purged++
			continue
		}
		kept = append(kept, at)
	}
	f.notes["t"] = kept
	return purged, nil
}

type fakeRetentionTrail struct {
	appended []AuditRecord
	purged   int
}

func (f *fakeRetentionTrail) Append(_ context.Context, r AuditRecord) error {
	f.appended = append(f.appended, r)
	return nil
}

func (f *fakeRetentionTrail) Read(_ context.Context, _ string) iter.Seq2[AuditRecord, error] {
	return func(yield func(AuditRecord, error) bool) {
		for _, r := range f.appended {
			if !yield(r, nil) {
				return
			}
		}
	}
}

func (f *fakeRetentionTrail) Purge(_ context.Context, _ string, olderThan time.Time) (int, error) {
	var kept []AuditRecord
	for _, r := range f.appended {
		if r.At.Before(olderThan) {
			f.purged++
			continue
		}
		kept = append(kept, r)
	}
	f.appended = kept
	return f.purged, nil
}

type fakeRetentionSessions struct {
	sessions []SessionMeta
}

func (f *fakeRetentionSessions) RetentionSessions(_ context.Context, _ string) ([]SessionMeta, error) {
	return f.sessions, nil
}

func retentionDeps(now time.Time, sessions *fakeRetentionSessions, trail *fakeRetentionTrail, working *fakeWorkingStore, sessionPurgers ...*fakeSessionPurger) RetentionDeps {
	deps := RetentionDeps{
		Now:      func() time.Time { return now },
		Tenants:  func(context.Context) ([]string, error) { return []string{"t"}, nil },
		Sessions: sessions,
		Trail:    trail,
	}
	for _, p := range sessionPurgers {
		deps.Session = append(deps.Session, NamedPurge{Store: p.store, Purge: p.purge})
	}
	if working != nil {
		deps.Working = []TierPurge{{Store: "notes", Purge: working.purge}}
	}
	return deps
}

func retentionSession(id string, idle time.Duration) SessionMeta {
	return SessionMeta{ID: id, LastActivity: retentionNow.Add(-idle)}
}

func TestRetention(t *testing.T) {
	ctx := context.Background()

	t.Run("stores.retention-purge-by-tier", func(t *testing.T) {
		policy := RetentionPolicy{
			Conversation: 30 * 24 * time.Hour,
			Working:      60 * 24 * time.Hour,
			Audit:        365 * 24 * time.Hour,
		}
		session := retentionSession("s1", 40*24*time.Hour)
		var purgers []*fakeSessionPurger
		for _, store := range []string{"session", "events", "outputs", "checkpoints", "journal", "mailbox", "feedback", "redaction"} {
			purgers = append(purgers, &fakeSessionPurger{store: store, records: map[string]int{"s1": 1}})
		}
		working := &fakeWorkingStore{notes: map[string][]time.Time{
			"t": {retentionNow.Add(-40 * 24 * time.Hour)},
		}}
		trail := &fakeRetentionTrail{}
		if err := trail.Append(ctx, AuditRecord{Kind: AuditToolOutcome, At: retentionNow.Add(-40 * 24 * time.Hour), Tenant: "t"}); err != nil {
			t.Fatalf("seed audit: %v", err)
		}
		sessions := &fakeRetentionSessions{sessions: []SessionMeta{session}}

		report, err := Sweep(ctx, retentionDeps(retentionNow, sessions, trail, working, purgers...), policy)
		if err != nil {
			t.Fatalf("sweep: %v", err)
		}
		for _, p := range purgers {
			if len(p.records) != 0 || len(p.purged) != 1 {
				t.Errorf("store %s: records %d, purged %d", p.store, len(p.records), len(p.purged))
			}
			if report.Purged[p.store] != 1 {
				t.Errorf("report purged %s = %d, want 1", p.store, report.Purged[p.store])
			}
		}
		if len(working.notes["t"]) != 1 {
			t.Errorf("notes purged under working tier: %v", working.notes["t"])
		}
		if len(trail.appended) != 2 {
			t.Fatalf("audit trail changed: %d records", len(trail.appended))
		}
		if report.Held != 0 {
			t.Errorf("held = %d, want 0", report.Held)
		}
		purge := trail.appended[1]
		if purge.Kind != AuditPurge || purge.Tenant != "t" {
			t.Errorf("purge record kind %q tenant %q", purge.Kind, purge.Tenant)
		}
		if purge.SessionID != "" || purge.ResultSHA != "" {
			t.Errorf("purge record carries content: %+v", purge)
		}
		var detail struct {
			Policy RetentionPolicy `json:"policy"`
			Purged map[string]int  `json:"purged"`
		}
		if err := json.Unmarshal([]byte(purge.Decision), &detail); err != nil {
			t.Fatalf("purge detail: %v", err)
		}
		if detail.Policy != policy {
			t.Errorf("policy in record %+v, want %+v", detail.Policy, policy)
		}
		if detail.Purged["session"] != 1 || detail.Purged["redaction"] != 1 {
			t.Errorf("counts in record %v", detail.Purged)
		}
		if _, ok := detail.Purged["notes"]; ok {
			t.Errorf("notes counted in purge record")
		}
	})

	t.Run("stores.retention-zero-deletes-at-finish", func(t *testing.T) {
		zero := RetentionPolicy{Conversation: 0, Working: 60 * 24 * time.Hour, Audit: 365 * 24 * time.Hour}
		if err := zero.Validate(); err != nil {
			t.Fatalf("zero policy: %v", err)
		}
		var purgers []*fakeSessionPurger
		for _, store := range []string{"session", "events", "outputs", "checkpoints"} {
			purgers = append(purgers, &fakeSessionPurger{store: store, records: map[string]int{"s1": 1}})
		}
		trail := &fakeRetentionTrail{}
		if err := trail.Append(ctx, AuditRecord{Kind: AuditRunFinished, At: retentionNow, Tenant: "t"}); err != nil {
			t.Fatalf("seed audit: %v", err)
		}
		deps := retentionDeps(retentionNow, &fakeRetentionSessions{}, trail, nil, purgers...)

		if err := PurgeEphemeral(ctx, deps, zero, "t", "s1"); err != nil {
			t.Fatalf("ephemeral purge: %v", err)
		}
		for _, p := range purgers {
			if len(p.records) != 0 {
				t.Errorf("store %s kept %d records at finish", p.store, len(p.records))
			}
		}
		if len(trail.appended) != 1 || trail.purged != 0 {
			t.Errorf("audit trail changed: %d records, %d purged", len(trail.appended), trail.purged)
		}

		full := RetentionPolicy{Conversation: 30 * 24 * time.Hour, Audit: 365 * 24 * time.Hour}
		for _, p := range purgers {
			p.records["s1"] = 1
		}
		if err := PurgeEphemeral(ctx, deps, full, "t", "s1"); err != nil {
			t.Fatalf("full-retention purge: %v", err)
		}
		for _, p := range purgers {
			if len(p.purged) != 1 {
				t.Errorf("store %s purged under full retention", p.store)
			}
		}
	})

	t.Run("stores.purge-audited", func(t *testing.T) {
		policy := RetentionPolicy{
			Conversation: 30 * 24 * time.Hour,
			Working:      60 * 24 * time.Hour,
			Audit:        365 * 24 * time.Hour,
		}
		var metas []SessionMeta
		for i := range 12 {
			metas = append(metas, retentionSession(strings.Repeat("s", i+1), 40*24*time.Hour))
		}
		purger := &fakeSessionPurger{store: "session", records: map[string]int{}}
		for _, m := range metas {
			purger.records[m.ID] = 1
		}
		trail := &fakeRetentionTrail{}
		deps := retentionDeps(retentionNow, &fakeRetentionSessions{sessions: metas}, trail, nil, purger)

		report, err := Sweep(ctx, deps, policy)
		if err != nil {
			t.Fatalf("sweep: %v", err)
		}
		if report.Purged["session"] != 12 {
			t.Errorf("purged session = %d, want 12", report.Purged["session"])
		}
		if len(trail.appended) != 1 {
			t.Fatalf("appended %d audit records, want 1", len(trail.appended))
		}
		rec := trail.appended[0]
		if rec.Kind != AuditPurge || rec.Tenant != "t" {
			t.Errorf("record kind %q tenant %q", rec.Kind, rec.Tenant)
		}
		if rec.SessionID != "" || rec.ResultSHA != "" || rec.ResultBytes != 0 {
			t.Errorf("record carries content: %+v", rec)
		}
		var detail struct {
			Policy RetentionPolicy `json:"policy"`
			Purged map[string]int  `json:"purged"`
		}
		if err := json.Unmarshal([]byte(rec.Decision), &detail); err != nil {
			t.Fatalf("purge detail: %v", err)
		}
		if detail.Policy != policy {
			t.Errorf("policy in record %+v, want %+v", detail.Policy, policy)
		}
		if detail.Purged["session"] != 12 {
			t.Errorf("counts in record %v", detail.Purged)
		}
	})

	t.Run("working-state.notes-follow-working-retention", func(t *testing.T) {
		def := RetentionPolicy{
			Conversation: 90 * 24 * time.Hour,
			Working:      180 * 24 * time.Hour,
			Audit:        2 * 365 * 24 * time.Hour,
		}
		working := &fakeWorkingStore{notes: map[string][]time.Time{
			"t": {
				retentionNow.Add(-200 * 24 * time.Hour),
				retentionNow.Add(-30 * 24 * time.Hour),
			},
		}}
		trail := &fakeRetentionTrail{}
		if err := trail.Append(ctx, AuditRecord{Kind: AuditRunFinished, At: retentionNow.Add(-100 * 24 * time.Hour), Tenant: "t"}); err != nil {
			t.Fatalf("seed audit: %v", err)
		}
		deps := retentionDeps(retentionNow, &fakeRetentionSessions{}, trail, working)

		report, err := Sweep(ctx, deps, def)
		if err != nil {
			t.Fatalf("sweep: %v", err)
		}
		if report.Purged["notes"] != 1 {
			t.Errorf("purged notes = %d, want 1", report.Purged["notes"])
		}
		notes := working.notes["t"]
		if len(notes) != 1 || !notes[0].Equal(retentionNow.Add(-30*24*time.Hour)) {
			t.Errorf("remaining notes %v", notes)
		}
		if len(trail.appended) != 2 {
			t.Errorf("audit records %d, want seed plus purge record", len(trail.appended))
		}
		if trail.purged != 0 {
			t.Errorf("audit purged %d records, want 0", trail.purged)
		}
		if !errors.Is(RetentionPolicy{Conversation: 365 * 24 * time.Hour, Audit: 30 * 24 * time.Hour}.Validate(), ErrRetentionAuditShort) {
			t.Errorf("audit shorter than conversation must fail validation")
		}
	})
}
