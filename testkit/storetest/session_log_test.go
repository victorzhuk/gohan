package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// fakeSessionLog is a deliberately simple implementation of the port used
// to exercise the suite itself.
type fakeSessionLog struct {
	mu       sync.Mutex
	sessions map[string]*fakeHistory
}

type fakeHistory struct {
	msgs         []types.Message
	version      int64
	lastActivity time.Time
}

func newFakeSessionLog(context.Context) (SessionLogStore, error) {
	return &fakeSessionLog{sessions: map[string]*fakeHistory{}}, nil
}

func (f *fakeSessionLog) Load(_ context.Context, id string) (SessionHistory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.sessions[id]
	if !ok {
		return SessionHistory{}, fmt.Errorf("session %s: not found", id)
	}
	return SessionHistory{Messages: append([]types.Message(nil), h.msgs...), Version: h.version}, nil
}

func (f *fakeSessionLog) Append(_ context.Context, id string, expected int64, msgs ...types.Message) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.sessions[id]
	if !ok {
		if expected != 0 {
			return 0, types.ErrVersionConflict
		}
		h = &fakeHistory{}
		f.sessions[id] = h
	}
	if expected != h.version {
		return 0, types.ErrVersionConflict
	}
	for i := range msgs {
		msgs[i].ID = fmt.Sprintf("m%d", len(h.msgs)+i+1)
	}
	h.lastActivity = time.Now()
	h.msgs = append(h.msgs, msgs...)
	h.version += int64(len(msgs))
	return h.version, nil
}

func (f *fakeSessionLog) Purge(_ context.Context, olderThan time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int
	for id, h := range f.sessions {
		if h.lastActivity.Before(olderThan) {
			delete(f.sessions, id)
			n++
		}
	}
	return n, nil
}

func (f *fakeSessionLog) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sessions[id]; !ok {
		return errors.New("not found")
	}
	delete(f.sessions, id)
	return nil
}

func TestStoretestSessionLog(t *testing.T) {
	SessionLog(t, newFakeSessionLog)
}
