package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"iter"
	"sync"
	"testing"
	"time"
)

type fakeAudit struct {
	mu       sync.Mutex
	byTenant map[string][]AuditRecord
	now      func() time.Time
}

func newFakeAudit(_ *testing.T) AuditStore {
	return &fakeAudit{byTenant: map[string][]AuditRecord{}, now: time.Now}
}

func (f *fakeAudit) Append(_ context.Context, r AuditRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	rec := r
	rec.At = f.now()
	for i := len(f.byTenant[rec.Tenant]) - 1; i >= 0; i-- {
		if f.byTenant[rec.Tenant][i].SessionID == rec.SessionID {
			rec.PrevHash = f.byTenant[rec.Tenant][i].Hash
			break
		}
	}
	rec.Hash = fakeAuditHash(rec)
	f.byTenant[rec.Tenant] = append(f.byTenant[rec.Tenant], rec)
	return nil
}

func (f *fakeAudit) Read(_ context.Context, sessionID string) iter.Seq2[AuditRecord, error] {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []AuditRecord
	for _, chain := range f.byTenant {
		for _, rec := range chain {
			if rec.SessionID == sessionID {
				out = append(out, rec)
			}
		}
	}
	return func(yield func(AuditRecord, error) bool) {
		for _, rec := range out {
			if !yield(rec, nil) {
				return
			}
		}
	}
}

func (f *fakeAudit) Purge(_ context.Context, tenant string, olderThan time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	chain := f.byTenant[tenant]
	kept := chain[:0]
	removed := 0
	for _, rec := range chain {
		if rec.At.Before(olderThan) {
			removed++
			continue
		}
		kept = append(kept, rec)
	}
	if removed == 0 {
		return 0, nil
	}
	var prev *AuditRecord
	for i := range kept {
		if prev == nil || prev.SessionID != kept[i].SessionID {
			kept[i].PrevHash = ""
		} else {
			kept[i].PrevHash = prev.Hash
		}
		kept[i].Hash = fakeAuditHash(kept[i])
		prev = &kept[i]
	}
	f.byTenant[tenant] = kept
	return removed, nil
}

func fakeAuditHash(r AuditRecord) string {
	h := sha256.New()
	// Writing to a hash.Hash never fails; discard the count and error explicitly.
	_, _ = fmt.Fprintf(h, "%s|%s|%s|%s|%s", r.SessionID, r.Tenant, r.Kind, r.ArgsChecksum, r.PrevHash)
	return hex.EncodeToString(h.Sum(nil))
}

func TestStoretestAuditLog(t *testing.T) {
	AuditLog(t, newFakeAudit)
}
