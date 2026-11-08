package stores

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
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
	cruns   CheckpointRuns
	ttl     time.Duration
	toks    map[types.ResumeToken]*checkpointRecord
	byRun   map[string]types.ResumeToken
	schemas *SchemaRegistry
	region  string
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

// WithMemoryCheckpointRuns binds the run store recovery discovery consults
// for current-token and suspended-run evidence. An unbound store refuses
// discovery: that evidence is not decidable locally.
func WithMemoryCheckpointRuns(r CheckpointRuns) MemoryCheckpointOption {
	return func(s *MemoryCheckpoints) { s.cruns = r }
}

// WithMemoryCheckpointTTL sets the automatic checkpoint lifetime measured
// from the store clock at Put. Zero keeps no automatic expiry; explicit
// expiry on the checkpoint is never overwritten.
func WithMemoryCheckpointTTL(ttl time.Duration) MemoryCheckpointOption {
	return func(s *MemoryCheckpoints) { s.ttl = ttl }
}

func WithMemoryCheckpointRegion(region string) MemoryCheckpointOption {
	return func(s *MemoryCheckpoints) { s.region = region }
}

func (s *MemoryCheckpoints) Region() string { return s.region }

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
	if s.ttl < 0 {
		return "", fmt.Errorf("gohan: negative checkpoint ttl %s", s.ttl)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("gohan: mint resume token: %w", err)
	}
	t := types.ResumeToken("cp_" + hex.EncodeToString(raw[:]))

	cp = cloneCheckpoint(cp)
	if s.ttl > 0 && cp.ExpiresAt.IsZero() {
		cp.ExpiresAt = s.now().Add(s.ttl)
	}
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

// SeedRecord installs envelope as the stored bytes of identity, so the next
// public read consumes them as persisted bytes. Seeding bypasses expiry:
// fixtures replay historical bytes, not client writes.
func (s *MemoryCheckpoints) SeedRecord(_ context.Context, identity string, envelope RecordEnvelope) error {
	raw, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("seed %s: %w", identity, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.checkpointSlot(identity)
	if err != nil {
		return err
	}
	rec.raw = raw
	return nil
}

// StoredRecord returns the stored bytes of identity unchanged.
func (s *MemoryCheckpoints) StoredRecord(_ context.Context, identity string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.checkpointSlot(identity)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), rec.raw...), nil
}

// checkpointSlot resolves a "checkpoint/<token>" identity. Tokens must not
// carry a slash.
func (s *MemoryCheckpoints) checkpointSlot(identity string) (*checkpointRecord, error) {
	token, ok := strings.CutPrefix(identity, "checkpoint/")
	if !ok || token == "" || strings.Contains(token, "/") {
		return nil, fmt.Errorf("record identity %s: not a checkpoint identity", identity)
	}
	rec, ok := s.toks[types.ResumeToken(token)]
	if !ok {
		return nil, fmt.Errorf("record identity %s: not found", identity)
	}
	return rec, nil
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

// ResumeReady lists consumed checkpoints the run still waits on: a crash
// after conditional consumption and a failed lease left the recorded
// decision recoverable exactly once. Current-token, consumed-state, expiry
// and suspended-run filters all run before the limit, so an older
// ineligible record cannot shadow a newer eligible one. The run store is
// consulted only after the checkpoint-local filters pass, and the lock
// order stays checkpoint then run: no run path calls back into this store.
func (s *MemoryCheckpoints) ResumeReady(ctx context.Context, limit int) ([]Checkpoint, error) {
	if s.cruns == nil {
		return nil, fmt.Errorf("gohan: recovery discovery unbound: %w", types.ErrCheckpointIncompatible)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	out := make([]Checkpoint, 0)
	for t, rec := range s.toks {
		if limit > 0 && len(out) >= limit {
			break
		}
		if !rec.consumed {
			continue
		}
		if !rec.cp.ExpiresAt.IsZero() && !now.Before(rec.cp.ExpiresAt) {
			continue
		}
		runID := rec.cp.RunID
		if s.byRun[runID] != t {
			continue
		}
		if cur, err := s.cruns.SuspensionToken(ctx, runID); err != nil || cur != t {
			continue
		}
		cp, err := s.decode(ctx, rec)
		if err != nil {
			return nil, err
		}
		out = append(out, cp)
	}
	return out, nil

}
