package stores

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	RunID   string
	Expires time.Time
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
	mu     sync.Mutex
	runs   map[string]*runRecord
	byOp   map[string]string
	onSess map[string]string
	now    func() time.Time
	info   RunInfoSource

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

func NewMemoryRuns(opts ...MemoryRunOption) *MemoryRuns {
	s := &MemoryRuns{
		runs:   map[string]*runRecord{},
		byOp:   map[string]string{},
		onSess: map[string]string{},
		now:    time.Now,
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

func (rec *runRecord) expiredAt(now time.Time) bool {
	return !rec.lease.Expires.IsZero() && !now.Before(rec.lease.Expires)
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
	if runID, ok := s.onSess[r.SessionID]; ok {
		if rec := s.runs[runID]; rec != nil && rec.active(now) {
			return Lease{}, fmt.Errorf("%w: run %s", types.ErrRunActive, runID)
		}
	}
	opKey := tenant + "\x00" + r.OperationID
	if r.OperationID != "" {
		if runID, ok := s.byOp[opKey]; ok {
			return Lease{}, OperationExistsError{RunID: runID}
		}
	}
	now = s.now()
	r.State = Running
	r.StartedAt = now
	r.Heartbeat = now
	rec := &runRecord{
		run:   cloneRun(r),
		ttl:   ttl,
		lease: Lease{RunID: r.RunID, Expires: now.Add(ttl)},
		live:  true,
	}
	s.runs[r.RunID] = rec
	s.onSess[r.SessionID] = r.RunID
	if r.OperationID != "" {
		s.byOp[opKey] = r.RunID
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
	return cloneRun(s.runs[runID].run), nil
}

// ByID returns the run with the given id in any state, including finished
// and failed ones. Cross-pod inspection reads through it; like
// PreemptedLister it is an optional surface on Runs, discovered by type
// assertion.
func (s *MemoryRuns) ByID(_ context.Context, runID string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.runs[runID]
	if !ok {
		return Run{}, fmt.Errorf("%w: run %s", ErrRunNotFound, runID)
	}
	return cloneRun(rec.run), nil
}

// Finish closes the run with the given terminal state. An expired or
// foreign lease fails with types.ErrRunNotActive; the run is left open.
func (s *MemoryRuns) Finish(ctx context.Context, l Lease, state RunState, uncertain []types.CallKey, resultRef string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.heldLocked(l)
	if err != nil {
		return err
	}
	for _, sig := range rec.signals {
		if sig.Kind == SignalSteer {
			return fmt.Errorf("%w: run %s", types.ErrSignalsPending, rec.run.RunID)
		}
	}
	rec.run.State = state
	rec.run.Uncertain = append([]types.CallKey(nil), uncertain...)
	rec.run.ResultRef = resultRef
	rec.lease = Lease{}
	rec.live = false
	s.appendNoticeLocked(ctx, rec, state)
	return nil
}

// Suspend releases the lease and records the resume token; a suspended run
// is not recoverable until Resuming takes a fresh lease.
func (s *MemoryRuns) Suspend(ctx context.Context, l Lease, t types.ResumeToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.heldLocked(l)
	if err != nil {
		return err
	}
	rec.run.State = Suspended
	rec.token = t
	rec.lease = Lease{}
	rec.live = false
	s.appendNoticeLocked(ctx, rec, Suspended)
	return nil
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
	now := s.now()
	rec.run.State = Resuming
	rec.run.Heartbeat = now
	rec.ttl = ttl
	rec.lease = Lease{RunID: runID, Expires: now.Add(ttl)}
	rec.live = true
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
		stale = append(stale, cloneRun(rec.run))
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

// heldLocked resolves the lease to its run and verifies it is still live.
func (s *MemoryRuns) heldLocked(l Lease) (*runRecord, error) {
	rec, ok := s.runs[l.RunID]
	if !ok {
		return nil, fmt.Errorf("%w: run %s", ErrRunNotFound, l.RunID)
	}
	if !rec.live || rec.expiredAt(s.now()) {
		return nil, fmt.Errorf("%w: run %s", types.ErrRunNotActive, l.RunID)
	}
	return rec, nil
}
