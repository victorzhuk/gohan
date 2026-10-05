package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type fakeJournal struct {
	mu     sync.Mutex
	byKey  map[types.CallKey]*fakeJournalRecord
	ttl    time.Duration
	now    func() time.Time
	nextID int
}

type fakeJournalRecord struct {
	entry    JournalEntry
	key      types.CallKey
	expireAt time.Time
}

func newFakeJournal(_ *testing.T, ttl time.Duration) JournalStore {
	return &fakeJournal{
		ttl: ttl,
		now: time.Now,
	}
}

func (f *fakeJournal) Reserve(_ context.Context, k types.CallKey, fp string) (JournalEntry, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := f.now()
	if rec, ok := f.byKey[k]; ok && now.Before(rec.expireAt) {
		if rec.entry.Fingerprint != fp {
			return JournalEntry{}, false, errors.New("gohan: journal call key reused with a different fingerprint")
		}
		return rec.entry, false, nil
	}
	f.nextID++
	rec := &fakeJournalRecord{
		entry: JournalEntry{
			State:       JournalReserved,
			Fingerprint: fp,
			Key:         k.CallID,
		},
		key:      k,
		expireAt: now.Add(f.ttl),
	}
	if f.byKey == nil {
		f.byKey = make(map[types.CallKey]*fakeJournalRecord)
	}
	f.byKey[k] = rec
	return rec.entry, true, nil
}

func (f *fakeJournal) Complete(_ context.Context, k types.CallKey, res types.ToolResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	rec, ok := f.byKey[k]
	if !ok || !f.now().Before(rec.expireAt) {
		return errors.New("gohan: journal completion found no live reservation")
	}
	rec.entry.State = JournalCompleted
	rec.entry.Result = res
	rec.entry.Result.Content = append([]types.Block(nil), res.Content...)
	rec.entry.Result.ID = res.ID
	return nil
}

func (f *fakeJournal) ByFingerprint(_ context.Context, sessionID string, fp string) ([]JournalEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := f.now()
	var out []JournalEntry
	for _, rec := range f.byKey {
		if rec.key.SessionID != sessionID || rec.entry.Fingerprint != fp {
			continue
		}
		if !now.Before(rec.expireAt) {
			continue
		}
		out = append(out, rec.entry)
	}
	return out, nil
}

func TestStoretestJournal(t *testing.T) {
	Journal(t, newFakeJournal)
}
