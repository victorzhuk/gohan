package stores

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// RunInfoSource reports the run info in ctx. Consumer-owned: the harness
// injects the seam's extractor; the store never reads identity context
// itself.
type RunInfoSource func(ctx context.Context) (types.RunInfo, bool)

type checkpointRecord struct {
	cp       Checkpoint
	raw      []byte
	input    ResumeInput
	consumed bool
}

// MemoryCheckpoints is the in-memory Checkpoints reference implementation.
// The store clock is authoritative for ExpiresAt: a caller with a skewed
// clock cannot consume an expired token or extend a live one.
type MemoryCheckpoints struct {
	mu      sync.Mutex
	now     func() time.Time
	runs    RunInfoSource
	toks    map[types.ResumeToken]*checkpointRecord
	byRun   map[string]types.ResumeToken
	schemas *SchemaRegistry
}

type MemoryCheckpointOption func(*MemoryCheckpoints)

// WithMemoryCheckpointRunInfo injects the run info extractor used to index
// a checkpoint by run id for PendingInput.
func WithMemoryCheckpointRunInfo(src RunInfoSource) MemoryCheckpointOption {
	return func(s *MemoryCheckpoints) { s.runs = src }
}

// WithMemoryCheckpointClock replaces the store clock. Expiry is judged
// against it, so tests drive time through it.
func WithMemoryCheckpointClock(now func() time.Time) MemoryCheckpointOption {
	return func(s *MemoryCheckpoints) { s.now = now }
}

func WithMemoryCheckpointSchemas(reg *SchemaRegistry) MemoryCheckpointOption {
	return func(s *MemoryCheckpoints) { s.schemas = reg }
}

func NewMemoryCheckpoints(opts ...MemoryCheckpointOption) *MemoryCheckpoints {
	s := &MemoryCheckpoints{
		now:     time.Now,
		toks:    make(map[types.ResumeToken]*checkpointRecord),
		byRun:   make(map[string]types.ResumeToken),
		schemas: NewSchemaRegistry(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func cloneCheckpoint(cp Checkpoint) Checkpoint {
	cp.Data = append([]byte(nil), cp.Data...)
	cp.Originator.Scopes = append([]string(nil), cp.Originator.Scopes...)
	return cp
}

func cloneResumeInput(in ResumeInput) ResumeInput {
	in.Args = append([]byte(nil), in.Args...)
	in.Data = append([]byte(nil), in.Data...)
	if in.Approver != nil {
		p := *in.Approver
		p.Scopes = append([]string(nil), p.Scopes...)
		in.Approver = &p
	}
	return in
}

func checkpointEqual(a, b Checkpoint) bool {
	return reflect.DeepEqual(a, b)
}

func (s *MemoryCheckpoints) decode(ctx context.Context, rec *checkpointRecord) (Checkpoint, error) {
	var env RecordEnvelope
	if err := json.Unmarshal(rec.raw, &env); err != nil {
		return Checkpoint{}, fmt.Errorf("decode Checkpoint envelope: %w", err)
	}
	cp, err := DecodeRecord[Checkpoint](ctx, s.schemas, env)
	if err != nil {
		return Checkpoint{}, err
	}
	return cloneCheckpoint(cp), nil
}

// Put mints a fresh single-use token for cp. The run id comes from the
// RunInfo in ctx so PendingInput can find the checkpoint after a crash; a
// token put outside a run is reachable by Consume only.
func (s *MemoryCheckpoints) Put(ctx context.Context, cp Checkpoint) (types.ResumeToken, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("gohan: mint resume token: %w", err)
	}
	t := types.ResumeToken("cp_" + hex.EncodeToString(raw[:]))

	cp = cloneCheckpoint(cp)
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = SchemaVersion(s.schemas.current)
	}
	payload, err := json.Marshal(cp)
	if err != nil {
		return "", fmt.Errorf("encode Checkpoint: %w", err)
	}
	encoded, err := json.Marshal(RecordEnvelope{Shape: "Checkpoint", SchemaVersion: int(cp.SchemaVersion), Payload: payload})
	if err != nil {
		return "", fmt.Errorf("encode Checkpoint envelope: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.toks[t] = &checkpointRecord{cp: cp, raw: encoded}
	runID := cp.RunID
	if runID == "" && s.runs != nil {
		if ri, ok := s.runs(ctx); ok {
			runID = ri.RunID
		}
	}
	if runID != "" {
		s.byRun[runID] = t
	}
	return t, nil

}

// Consume atomically marks the token used and stores the resume input: the
// read, the expiry check and the consumed flip happen under one lock, so of
// any set of concurrent callers exactly one wins.
func (s *MemoryCheckpoints) Consume(ctx context.Context, t types.ResumeToken, in ResumeInput) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.toks[t]
	if !ok {
		return Checkpoint{}, fmt.Errorf("gohan: resume token %s: %w", t, types.ErrTokenMismatch)
	}
	if rec.consumed {
		return Checkpoint{}, fmt.Errorf("gohan: resume token %s: %w", t, types.ErrTokenConsumed)
	}
	if !rec.cp.ExpiresAt.IsZero() && !s.now().Before(rec.cp.ExpiresAt) {
		return Checkpoint{}, fmt.Errorf("gohan: resume token %s: %w", t, types.ErrTokenExpired)
	}
	cp, err := s.decode(ctx, rec)
	if err != nil {
		return Checkpoint{}, err
	}
	rec.consumed = true
	rec.input = cloneResumeInput(in)
	return cp, nil

}

// PendingInput returns the checkpoint a suspended run waits on and the
// input Consume recorded. Before consumption the input is the zero value.
func (s *MemoryCheckpoints) PendingInput(ctx context.Context, runID string) (Checkpoint, ResumeInput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.byRun[runID]
	if !ok {
		return Checkpoint{}, ResumeInput{}, fmt.Errorf("gohan: no checkpoint for run %s", runID)
	}
	rec := s.toks[token]
	cp, err := s.decode(ctx, rec)
	return cp, cloneResumeInput(rec.input), err
}

func (s *MemoryCheckpoints) Peek(ctx context.Context, t types.ResumeToken) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.toks[t]
	if !ok {
		return Checkpoint{}, fmt.Errorf("gohan: resume token %s: %w", t, types.ErrTokenMismatch)
	}
	if rec.consumed {
		return Checkpoint{}, fmt.Errorf("gohan: resume token %s: %w", t, types.ErrTokenConsumed)
	}
	if !rec.cp.ExpiresAt.IsZero() && !s.now().Before(rec.cp.ExpiresAt) {
		return Checkpoint{}, fmt.Errorf("gohan: resume token %s: %w", t, types.ErrTokenExpired)
	}
	return s.decode(ctx, rec)
}

func (s *MemoryCheckpoints) ConsumeIf(ctx context.Context, t types.ResumeToken, expected Checkpoint, in ResumeInput) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.toks[t]
	if !ok {
		return Checkpoint{}, fmt.Errorf("gohan: resume token %s: %w", t, types.ErrTokenMismatch)
	}
	if rec.consumed {
		return Checkpoint{}, fmt.Errorf("gohan: resume token %s: %w", t, types.ErrTokenConsumed)
	}
	if !rec.cp.ExpiresAt.IsZero() && !s.now().Before(rec.cp.ExpiresAt) {
		return Checkpoint{}, fmt.Errorf("gohan: resume token %s: %w", t, types.ErrTokenExpired)
	}
	cp, err := s.decode(ctx, rec)
	if err != nil {
		return Checkpoint{}, err
	}
	if !checkpointEqual(expected, cp) {
		return Checkpoint{}, types.ErrVersionConflict
	}
	rec.consumed = true
	rec.input = cloneResumeInput(in)
	return cp, nil
}

func (s *MemoryCheckpoints) UpdatePending(ctx context.Context, t types.ResumeToken, expected, next Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.toks[t]
	if !ok {
		return fmt.Errorf("gohan: resume token %s: %w", t, types.ErrTokenMismatch)
	}
	if rec.consumed {
		return fmt.Errorf("gohan: resume token %s: %w", t, types.ErrTokenConsumed)
	}
	if !rec.cp.ExpiresAt.IsZero() && !s.now().Before(rec.cp.ExpiresAt) {
		return fmt.Errorf("gohan: resume token %s: %w", t, types.ErrTokenExpired)
	}
	candidate, err := s.decode(ctx, rec)
	if err != nil {
		return err
	}
	if !checkpointEqual(expected, candidate) {
		return types.ErrVersionConflict
	}
	candidate.Data = append(candidate.Data[:0], next.Data...)
	if !checkpointEqual(candidate, next) {
		return types.ErrVersionConflict
	}
	payload, err := json.Marshal(candidate)
	if err != nil {
		return fmt.Errorf("encode Checkpoint: %w", err)
	}
	raw, err := json.Marshal(RecordEnvelope{Shape: "Checkpoint", SchemaVersion: int(candidate.SchemaVersion), Payload: payload})
	if err != nil {
		return fmt.Errorf("encode Checkpoint envelope: %w", err)
	}
	rec.cp = candidate
	rec.raw = raw
	return nil
}

func (s *MemoryCheckpoints) ResumeReady(ctx context.Context, limit int) ([]Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Checkpoint, 0)
	for _, rec := range s.toks {
		if rec.consumed {
			cp, err := s.decode(ctx, rec)
			if err != nil {
				return nil, err
			}
			out = append(out, cp)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil

}
