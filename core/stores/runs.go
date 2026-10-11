package stores

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// ErrRunNotFound reports a run id or operation id no run in the store
// carries.
var ErrRunNotFound = errors.New("gohan: run not found")

// OperationExistsError reports a duplicate OperationID and carries the
// existing run's id so the caller can read its recorded result through
// ByOperation. It satisfies errors.Is against types.ErrOperationExists.
type OperationExistsError struct {
	RunID string
}

func (e OperationExistsError) Error() string {
	return "gohan: operation id already recorded"
}

func (e OperationExistsError) Is(target error) bool {
	return target == types.ErrOperationExists
}

// RunState is the lifecycle state of a run.
type RunState int

const (
	Running RunState = iota
	Suspended
	Resuming
	Finished
	Failed
)

// Run is one stored run row. Seq is the session-log version the run's last
// turn came from; Pending carries tool calls awaiting results across a
// turn boundary.
type Run struct {
	SessionID   string
	RunID       string
	RootRunID   string
	ParentRunID string
	Depth       int
	Flow        string
	Backend     string
	OperationID string
	Mode        types.RunMode
	State       RunState
	Uncertain   []types.CallKey
	Turn        int
	Seq         int64
	Pending     []types.ToolUse
	Cost        float64
	ResultRef   string
	StartedAt   time.Time
	Heartbeat   time.Time
}

const (
	LeaseTTL       = 30 * time.Second
	HeartbeatEvery = 10 * time.Second
	ReaperEvery    = 60 * time.Second
)

// Lease is the store-minted proof that a pod drives a run. Expires is
// store time and informational: callers pass durations, never instants.
type Lease struct {
	RunID      string
	Generation uint64
	Expires    time.Time
}

// Runs persists run rows and their leases. Heartbeat, Reclaim, the signal
// mailbox (Signal, Drain) and the notice outbox (Notices, AckNotice) are
// sibling concerns layered on the same store.
type Runs interface {
	Start(ctx context.Context, r Run, ttl time.Duration) (Lease, error)
	Heartbeat(ctx context.Context, l Lease) (Lease, error)
	Suspend(ctx context.Context, l Lease, t types.ResumeToken) error
	Resuming(ctx context.Context, runID string, ttl time.Duration) (Lease, error)
	Finish(ctx context.Context, l Lease, state RunState, uncertain []types.CallKey, resultRef string) error
	ByOperation(ctx context.Context, tenant, operationID string) (Run, error)
	Stale(ctx context.Context, staleAfter time.Duration, limit int) ([]Run, error)
	Reclaim(ctx context.Context, r Run, ttl time.Duration) (Lease, error)
	Signal(ctx context.Context, runID string, s Signal) error
	Drain(ctx context.Context, l Lease) ([]Signal, error)
	Notices(ctx context.Context, limit int) ([]types.RunNotice, error)
	AckNotice(ctx context.Context, id string) error
}

// SignalKind distinguishes a cancellation from a steering message.
type SignalKind int

const (
	SignalCancel SignalKind = iota
	SignalSteer
)

// Signal is one mailbox entry for a run. At is store time.
type Signal struct {
	Kind    SignalKind
	Message types.Message
	At      time.Time
}

// MaxPendingSignals bounds a run's mailbox; a full mailbox rejects with
// types.ErrMailboxFull.
const MaxPendingSignals = 10

// PreemptedLister is an optional interface on Runs. When the store
// implements it, Recover resumes preempted runs first; otherwise they wait
// for a client decision or expire with their checkpoint.
type PreemptedLister interface {
	Preempted(ctx context.Context, limit int) ([]Run, error)
}

type runRecord struct {
	run     Run
	raw     []byte
	lease   Lease
	ttl     time.Duration
	token   types.ResumeToken
	live    bool
	signals []Signal
}

// MemoryRuns is the in-memory Runs reference implementation. The store
// clock is authoritative for StartedAt, Heartbeat and lease expiry: a
// caller with a skewed clock cannot shorten or extend anyone's lease.
type MemoryRuns struct {
	mu         sync.Mutex
	runs       map[string]*runRecord
	byOp       map[string]string
	onSess     map[string]string
	now        func() time.Time
	info       RunInfoSource
	generation uint64
	schemas    *SchemaRegistry
	region     string

	notices   []*noticeEntry
	noticeSeq uint64
}

type MemoryRunOption func(*MemoryRuns)

// WithMemoryRunClock replaces the store clock. Leases, staleness and the
// heartbeat compare against it, so tests drive time through it.
func WithMemoryRunClock(now func() time.Time) MemoryRunOption {
	return func(s *MemoryRuns) { s.now = now }
}

// WithMemoryRunInfo injects the run info extractor used to scope an
// operation id to its tenant.
func WithMemoryRunInfo(src RunInfoSource) MemoryRunOption {
	return func(s *MemoryRuns) { s.info = src }
}

// WithMemoryRunSchemas injects the schema registry run reads decode
// through.
func WithMemoryRunSchemas(reg *SchemaRegistry) MemoryRunOption {
	return func(s *MemoryRuns) { s.schemas = reg }
}

func WithMemoryRunRegion(region string) MemoryRunOption {
	return func(s *MemoryRuns) { s.region = region }
}

func (s *MemoryRuns) Region() string { return s.region }

func NewMemoryRuns(opts ...MemoryRunOption) *MemoryRuns {
	s := &MemoryRuns{
		runs:    map[string]*runRecord{},
		byOp:    map[string]string{},
		onSess:  map[string]string{},
		now:     time.Now,
		schemas: NewSchemaRegistry(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *MemoryRuns) tenant(ctx context.Context) string {
	if s.info == nil {
		return ""
	}
	ri, _ := s.info(ctx)
	return ri.Principal.Tenant
}

func cloneRun(r Run) Run {
	r.Uncertain = append([]types.CallKey(nil), r.Uncertain...)
	r.Pending = append([]types.ToolUse(nil), r.Pending...)
	return r
}

// encodeRunLocked persists rec.run's envelope bytes after a mutation. The
// envelope is the record as stored; the typed row stays the working copy.
func (s *MemoryRuns) encodeRunLocked(rec *runRecord) error {
	raw, err := encodeEnvelope("Run", rec.run)
	if err != nil {
		return err
	}
	rec.raw = raw
	return nil
}

// decodeRun reads the record's envelope through the registry. The stored
// bytes are never rewritten.
func (s *MemoryRuns) decodeRun(ctx context.Context, rec *runRecord) (Run, error) {
	var env RecordEnvelope
	if err := json.Unmarshal(rec.raw, &env); err != nil {
		return Run{}, fmt.Errorf("decode Run envelope: %w", err)
	}
	run, err := DecodeRecord[Run](ctx, s.schemas, env)
	if err != nil {
		return Run{}, err
	}
	return cloneRun(run), nil
}

// SeedRecord installs envelope as the stored bytes of identity, so the next
// public read consumes them as persisted bytes. Seeding bypasses lease
// checks: fixtures replay historical bytes, not client writes.
func (s *MemoryRuns) SeedRecord(_ context.Context, identity string, envelope RecordEnvelope) error {
	raw, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("seed %s: %w", identity, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.runSlot(identity)
	if err != nil {
		return err
	}
	rec.raw = raw
	return nil
}

// StoredRecord returns the stored bytes of identity unchanged.
func (s *MemoryRuns) StoredRecord(_ context.Context, identity string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.runSlot(identity)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), rec.raw...), nil
}

// runSlot resolves a "run/<id>" identity. Run ids must not carry a slash.
func (s *MemoryRuns) runSlot(identity string) (*runRecord, error) {
	runID, ok := strings.CutPrefix(identity, "run/")
	if !ok || runID == "" || strings.Contains(runID, "/") {
		return nil, fmt.Errorf("record identity %s: not a run identity", identity)
	}
	rec, ok := s.runs[runID]
	if !ok {
		return nil, fmt.Errorf("record identity %s: not found", identity)
	}
	return rec, nil
}

func (rec *runRecord) expiredAt(now time.Time) bool {
	return !rec.lease.Expires.IsZero() && !now.Before(rec.lease.Expires)
}

func (s *MemoryRuns) nextGenerationLocked() (uint64, error) {
	if s.generation == ^uint64(0) {
		return 0, types.ErrRunNotActive
	}
	s.generation++
	return s.generation, nil
}

// Start records a new run and mints its lease. It fails with
// types.ErrRunActive while the session holds an unexpired lease and with
// OperationExistsError when the operation id is already recorded for the
// tenant.
func (s *MemoryRuns) Start(ctx context.Context, r Run, ttl time.Duration) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tenant := s.tenant(ctx)
	now := s.now()
	// A duplicate operation id wins over the active-session refusal: the
	// caller reattaches to the recorded run instead of being refused.
	opKey := tenant + "\x00" + r.OperationID
	if r.OperationID != "" {
		if runID, ok := s.byOp[opKey]; ok {
			return Lease{}, OperationExistsError{RunID: runID}
		}
	}
	if runID, ok := s.onSess[r.SessionID]; ok {
		if rec := s.runs[runID]; rec != nil && rec.active(now) {
			return Lease{}, fmt.Errorf("%w: run %s", types.ErrRunActive, runID)
		}
	}
	generation, err := s.nextGenerationLocked()
	if err != nil {
		return Lease{}, err
	}
	now = s.now()
	r.State = Running
	r.StartedAt = now
	r.Heartbeat = now
	rec := &runRecord{
		run:   cloneRun(r),
		ttl:   ttl,
		lease: Lease{RunID: r.RunID, Generation: generation, Expires: now.Add(ttl)},
		live:  true,
	}

	s.runs[r.RunID] = rec
	s.onSess[r.SessionID] = r.RunID
	if r.OperationID != "" {
		s.byOp[opKey] = r.RunID
	}
	if err := s.encodeRunLocked(rec); err != nil {
		return Lease{}, err
	}
	return rec.lease, nil
}

// active reports whether the run is either leased or in a state a later
// operation still drives.
func (rec *runRecord) active(now time.Time) bool {
	switch rec.run.State {
	case Running, Resuming:
		return !rec.expiredAt(now)
	case Suspended:
		return false
	default:
		return false
	}
}

// ByOperation returns the run recorded for the tenant and operation id.
// The mapping survives Finish so a caller can read a recorded ResultRef.
func (s *MemoryRuns) ByOperation(ctx context.Context, tenant, operationID string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runID, ok := s.byOp[tenant+"\x00"+operationID]
	if !ok {
		return Run{}, fmt.Errorf("%w: operation %s", ErrRunNotFound, operationID)
	}
	return s.decodeRun(ctx, s.runs[runID])
}

// RunActive reports whether the run row is active: state Running or
// Resuming with a heartbeat the lease TTL still covers at now. The
// store-clock check lives in each store; callers without one judge
// liveness through this rule.
func RunActive(r Run, now time.Time) bool {
	switch r.State {
	case Running, Resuming:
		return r.Heartbeat.Add(LeaseTTL).After(now)
	default:
		return false
	}
}

// RunForSession returns the run the session currently drives: state
// Running or Resuming with a live lease on the store clock. A suspended,
// terminal or lease-expired run reports ErrRunNotFound, so a caller
// cannot name a run no pod is driving.
func (s *MemoryRuns) RunForSession(ctx context.Context, sessionID string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runID, ok := s.onSess[sessionID]
	if !ok {
		return Run{}, fmt.Errorf("%w: session %s", ErrRunNotFound, sessionID)
	}
	rec := s.runs[runID]
	now := s.now()
	if rec == nil || (rec.run.State != Running && rec.run.State != Resuming) || rec.expiredAt(now) {
		return Run{}, fmt.Errorf("%w: session %s", ErrRunNotFound, sessionID)
	}
	return s.decodeRun(ctx, rec)
}

// ByID returns the run with the given id in any state, including finished
// and failed ones. Cross-pod inspection reads through it; like
// PreemptedLister it is an optional surface on Runs, discovered by type
// assertion.
func (s *MemoryRuns) ByID(ctx context.Context, runID string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.runs[runID]
	if !ok {
		return Run{}, fmt.Errorf("%w: run %s", ErrRunNotFound, runID)
	}
	return s.decodeRun(ctx, rec)
}

// Resuming takes a fresh lease on a suspended run and marks it Resuming, so
// a crash after this point leaves a stale Resuming run Stale lists.
func (s *MemoryRuns) Resuming(ctx context.Context, runID string, ttl time.Duration) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.runs[runID]
	if !ok {
		return Lease{}, fmt.Errorf("%w: run %s", ErrRunNotFound, runID)
	}
	if rec.run.State != Suspended {
		return Lease{}, fmt.Errorf("%w: run %s is %v", types.ErrRunNotActive, runID, rec.run.State)
	}
	generation, err := s.nextGenerationLocked()
	if err != nil {
		return Lease{}, err
	}
	now := s.now()
	rec.run.State = Resuming
	rec.run.Heartbeat = now
	rec.ttl = ttl
	rec.lease = Lease{RunID: runID, Generation: generation, Expires: now.Add(ttl)}
	rec.live = true
	if err := s.encodeRunLocked(rec); err != nil {
		return Lease{}, err
	}
	return rec.lease, nil

}

// Stale lists up to limit runs whose heartbeat is older than staleAfter by
// the store's clock, in heartbeat order, covering Running and Resuming so
// both are reclaimable.
func (s *MemoryRuns) Stale(ctx context.Context, staleAfter time.Duration, limit int) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.now().Add(-staleAfter)
	var stale []Run
	for _, rec := range s.runs {
		if rec.run.State != Running && rec.run.State != Resuming {
			continue
		}
		if rec.run.Heartbeat.After(cutoff) {
			continue
		}
		run, err := s.decodeRun(ctx, rec)
		if err != nil {
			return nil, err
		}
		stale = append(stale, run)
	}
	sort.Slice(stale, func(i, j int) bool { return stale[i].Heartbeat.Before(stale[j].Heartbeat) })
	if len(stale) > limit {
		stale = stale[:limit]
	}
	return stale, nil
}

// SessionLeaseActive answers whether the session currently holds a live run
// lease. The session store consults it before a fork or a delete.
func (s *MemoryRuns) SessionLeaseActive(ctx context.Context, sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	runID, ok := s.onSess[sessionID]
	if !ok {
		return false
	}
	rec := s.runs[runID]
	return rec.active(s.now())
}

func (s *MemoryRuns) heldLocked(l Lease) (*runRecord, error) {
	rec, ok := s.runs[l.RunID]
	if !ok {
		return nil, fmt.Errorf("%w: run %s", ErrRunNotFound, l.RunID)
	}
	if rec.lease.RunID != l.RunID || rec.lease.Generation != l.Generation ||
		l.Generation == 0 || !rec.live || rec.expiredAt(s.now()) {
		return nil, fmt.Errorf("%w: run %s", types.ErrRunNotActive, l.RunID)
	}
	return rec, nil
}
