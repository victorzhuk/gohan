package gohan

import (
	"context"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/storetest"
)

// journalAdapter binds the memory Journal to the conformance suite's
// port-level interface without letting the suite import core/stores.
type journalAdapter struct {
	j *stores.MemoryJournal
}

func (a journalAdapter) Reserve(ctx context.Context, k types.CallKey, fp string) (storetest.JournalEntry, bool, error) {
	e, created, err := a.j.Reserve(ctx, k, stores.Fingerprint(fp))
	if err != nil {
		return storetest.JournalEntry{}, false, err
	}
	return storetest.JournalEntry{
		State:       stateToStoretest(e.State),
		Fingerprint: string(e.Fingerprint),
		Key:         e.Key,
		Result:      e.Result,
	}, created, nil
}

func (a journalAdapter) Complete(ctx context.Context, k types.CallKey, res types.ToolResult) error {
	return a.j.Complete(ctx, k, res)
}

func (a journalAdapter) ByFingerprint(ctx context.Context, sessionID string, fp string) ([]storetest.JournalEntry, error) {
	entries, err := a.j.ByFingerprint(ctx, sessionID, stores.Fingerprint(fp))
	if err != nil {
		return nil, err
	}
	out := make([]storetest.JournalEntry, len(entries))
	for i, e := range entries {
		out[i] = storetest.JournalEntry{
			State:       stateToStoretest(e.State),
			Fingerprint: string(e.Fingerprint),
			Key:         e.Key,
			Result:      e.Result,
		}
	}
	return out, nil
}

func stateToStoretest(s stores.EntryState) storetest.JournalEntryState {
	if s == stores.Completed {
		return storetest.JournalCompleted
	}
	return storetest.JournalReserved
}

func newConformanceJournal(t *testing.T, ttl time.Duration) storetest.JournalStore {
	t.Helper()
	return journalAdapter{j: stores.NewMemoryJournal(stores.WithJournalTTL(ttl))}
}

func TestStoretestJournalBind(t *testing.T) {
	storetest.Journal(t, newConformanceJournal)
}
