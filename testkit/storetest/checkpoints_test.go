package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type fakeCheckpoints struct {
	mu    sync.Mutex
	toks  map[types.ResumeToken]*fakeCheckpointRecord
	byRun map[string]types.ResumeToken
}

type fakeCheckpointRecord struct {
	cp       Checkpoint
	input    ResumeInput
	consumed bool
}

func newFakeCheckpoints(_ context.Context, runID string) (CheckpointStore, error) {
	return &fakeCheckpoints{
		toks:  make(map[types.ResumeToken]*fakeCheckpointRecord),
		byRun: map[string]types.ResumeToken{runID: ""},
	}, nil
}

func (f *fakeCheckpoints) Put(_ context.Context, cp Checkpoint) (types.ResumeToken, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("mint token: %w", err)
	}
	t := types.ResumeToken("cp_" + hex.EncodeToString(raw[:]))

	cp.Data = append([]byte(nil), cp.Data...)
	p := cp.Originator
	p.Scopes = append([]string(nil), cp.Originator.Scopes...)
	cp.Originator = p

	f.mu.Lock()
	defer f.mu.Unlock()
	f.toks[t] = &fakeCheckpointRecord{cp: cp}
	f.byRun[runID] = t
	return t, nil
}

func (f *fakeCheckpoints) Consume(_ context.Context, t types.ResumeToken, in ResumeInput) (Checkpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.toks[t]
	if !ok {
		return Checkpoint{}, fmt.Errorf("token %s: %w", t, types.ErrTokenMismatch)
	}
	if rec.consumed {
		return Checkpoint{}, fmt.Errorf("token %s: %w", t, types.ErrTokenConsumed)
	}
	if !rec.cp.ExpiresAt.IsZero() && !time.Now().Before(rec.cp.ExpiresAt) {
		return Checkpoint{}, fmt.Errorf("token %s: %w", t, types.ErrTokenExpired)
	}
	rec.consumed = true
	rec.input = in
	return rec.cp, nil
}

func (f *fakeCheckpoints) PendingInput(_ context.Context, runID string) (Checkpoint, ResumeInput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.byRun[runID]
	if !ok || t == "" {
		return Checkpoint{}, ResumeInput{}, fmt.Errorf("no checkpoint for run %s", runID)
	}
	rec := f.toks[t]
	return rec.cp, rec.input, nil
}

func TestStoretestCheckpoints(t *testing.T) {
	Checkpoints(t, newFakeCheckpoints)
}
