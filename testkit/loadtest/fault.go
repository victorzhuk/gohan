// Package loadtest provides a deterministic fault-injection wrapper over
// core/stores ports and a bounded concurrent run driver for load tests.
package loadtest

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type Port string

const (
	PortJournal     Port = "journal"
	PortRuns        Port = "runs"
	PortSessionLog  Port = "session_log"
	PortEventLog    Port = "event_log"
	PortCheckpoints Port = "checkpoints"
)

// ErrUnavailable is the error every injected store outage returns.
var ErrUnavailable = errors.New("loadtest: injected store outage")

// portState tracks the operation counter and the schedules judged against it.
type portState struct {
	ops     int
	failed  int
	methods map[string]int
	failAll bool
	// next marks the op range (opsAtFailNext, opsAtFailNext+n] still to fail.
	next      int
	nextStart int
	ranges    []opRange
}

type opRange struct {
	start int
	end   int // exclusive
}

// Faults is a deterministic, concurrency-safe fault schedule keyed on
// per-port operation numbers (1-based), not wall time.
type Faults struct {
	mu    sync.Mutex
	ports map[Port]*portState
}

func NewFaults() *Faults {
	return &Faults{ports: make(map[Port]*portState)}
}

func (f *Faults) state(p Port) *portState {
	s, ok := f.ports[p]
	if !ok {
		s = &portState{}
		f.ports[p] = s
	}
	return s
}

// FailNext fails the next n operations on p, counted from the port's current
// operation count plus one.
func (f *Faults) FailNext(p Port, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.state(p)
	s.nextStart = s.ops + 1
	s.next = n
}

// FailRange fails operations [start, start+n) on p (1-based, inclusive start).
func (f *Faults) FailRange(p Port, start, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.state(p)
	s.ranges = append(s.ranges, opRange{start: start, end: start + n})
}

// FailAll fails every operation on p until Clear.
func (f *Faults) FailAll(p Port) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state(p).failAll = true
}

// Clear removes every schedule and switch for p.
func (f *Faults) Clear(p Port) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.ports, p)
}

// Operations reports how many operations p has observed.
func (f *Faults) Operations(p Port) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state(p).ops
}

// Failures reports how many operations p refused with ErrUnavailable.
func (f *Faults) Failures(p Port) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state(p).failed
}

// MethodFailures reports how many refused operations on p ran the named
// store method.
func (f *Faults) MethodFailures(p Port, method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state(p).methods[method]
}

// step counts one operation and reports whether it must fail.
func (f *Faults) step(p Port, method string) (shouldFail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.state(p)
	s.ops++
	op := s.ops
	if s.failAll {
		shouldFail = true
	} else if s.next > 0 && op >= s.nextStart && op < s.nextStart+s.next {
		shouldFail = true
	} else {
		for _, r := range s.ranges {
			if op >= r.start && op < r.end {
				shouldFail = true
				break
			}
		}
	}
	if shouldFail {
		s.failed++
		if s.methods == nil {
			s.methods = make(map[string]int)
		}
		s.methods[method]++
	}
	return shouldFail
}

func fail() error { return ErrUnavailable }

type faultJournal struct {
	stores.Journal
	f *Faults
}

func (w faultJournal) Reserve(ctx context.Context, k types.CallKey, tool string, fp stores.Fingerprint, args json.RawMessage) (stores.Entry, bool, error) {
	if w.f.step(PortJournal, "Reserve") {
		return stores.Entry{}, false, fail()
	}
	return w.Journal.Reserve(ctx, k, tool, fp, args)
}

func (w faultJournal) Complete(ctx context.Context, k types.CallKey, res types.ToolResult) error {
	if w.f.step(PortJournal, "Complete") {
		return fail()
	}
	return w.Journal.Complete(ctx, k, res)
}

func (w faultJournal) ByFingerprint(ctx context.Context, sessionID string, fp stores.Fingerprint) ([]stores.Entry, error) {
	if w.f.step(PortJournal, "ByFingerprint") {
		return nil, fail()
	}
	return w.Journal.ByFingerprint(ctx, sessionID, fp)
}

func (w faultJournal) Unknown(ctx context.Context, sessionID string) ([]stores.Entry, error) {
	if w.f.step(PortJournal, "Unknown") {
		return nil, fail()
	}
	return w.Journal.Unknown(ctx, sessionID)
}

type faultRuns struct {
	stores.Runs
	f *Faults
}

func (w faultRuns) Start(ctx context.Context, r stores.Run, ttl time.Duration) (stores.Lease, error) {
	if w.f.step(PortRuns, "Start") {
		return stores.Lease{}, fail()
	}
	return w.Runs.Start(ctx, r, ttl)
}

func (w faultRuns) Heartbeat(ctx context.Context, l stores.Lease) (stores.Lease, error) {
	if w.f.step(PortRuns, "Heartbeat") {
		return stores.Lease{}, fail()
	}
	return w.Runs.Heartbeat(ctx, l)
}

func (w faultRuns) Suspend(ctx context.Context, l stores.Lease, t types.ResumeToken) error {
	if w.f.step(PortRuns, "Suspend") {
		return fail()
	}
	return w.Runs.Suspend(ctx, l, t)
}

func (w faultRuns) Resuming(ctx context.Context, runID string, ttl time.Duration) (stores.Lease, error) {
	if w.f.step(PortRuns, "Resuming") {
		return stores.Lease{}, fail()
	}
	return w.Runs.Resuming(ctx, runID, ttl)
}

func (w faultRuns) Finish(ctx context.Context, l stores.Lease, state stores.RunState, uncertain []types.CallKey, resultRef string) error {
	if w.f.step(PortRuns, "Finish") {
		return fail()
	}
	return w.Runs.Finish(ctx, l, state, uncertain, resultRef)
}

func (w faultRuns) ByOperation(ctx context.Context, tenant, operationID string) (stores.Run, error) {
	if w.f.step(PortRuns, "ByOperation") {
		return stores.Run{}, fail()
	}
	return w.Runs.ByOperation(ctx, tenant, operationID)
}

func (w faultRuns) Stale(ctx context.Context, staleAfter time.Duration, limit int) ([]stores.Run, error) {
	if w.f.step(PortRuns, "Stale") {
		return nil, fail()
	}
	return w.Runs.Stale(ctx, staleAfter, limit)
}

func (w faultRuns) Reclaim(ctx context.Context, r stores.Run, ttl time.Duration) (stores.Lease, error) {
	if w.f.step(PortRuns, "Reclaim") {
		return stores.Lease{}, fail()
	}
	return w.Runs.Reclaim(ctx, r, ttl)
}

func (w faultRuns) Signal(ctx context.Context, runID string, s stores.Signal) error {
	if w.f.step(PortRuns, "Signal") {
		return fail()
	}
	return w.Runs.Signal(ctx, runID, s)
}

func (w faultRuns) Drain(ctx context.Context, l stores.Lease) ([]stores.Signal, error) {
	if w.f.step(PortRuns, "Drain") {
		return nil, fail()
	}
	return w.Runs.Drain(ctx, l)
}

func (w faultRuns) Notices(ctx context.Context, limit int) ([]types.RunNotice, error) {
	if w.f.step(PortRuns, "Notices") {
		return nil, fail()
	}
	return w.Runs.Notices(ctx, limit)
}

func (w faultRuns) AckNotice(ctx context.Context, id string) error {
	if w.f.step(PortRuns, "AckNotice") {
		return fail()
	}
	return w.Runs.AckNotice(ctx, id)
}

func runsPreempted(ctx context.Context, w faultRuns, limit int) ([]stores.Run, error) {
	if w.f.step(PortRuns, "Preempted") {
		return nil, fail()
	}
	return w.Runs.(stores.PreemptedLister).Preempted(ctx, limit)
}

func runsSuspensionToken(ctx context.Context, w faultRuns, runID string) (types.ResumeToken, error) {
	if w.f.step(PortRuns, "SuspensionToken") {
		return "", fail()
	}
	return w.Runs.(stores.CheckpointRuns).SuspensionToken(ctx, runID)
}

func runsResumingCheckpoint(ctx context.Context, w faultRuns, runID string, t types.ResumeToken, ttl time.Duration) (stores.Lease, error) {
	if w.f.step(PortRuns, "ResumingCheckpoint") {
		return stores.Lease{}, fail()
	}
	return w.Runs.(stores.CheckpointRuns).ResumingCheckpoint(ctx, runID, t, ttl)
}

// runsPreemptedStore adds stores.PreemptedLister for inner stores that
// implement it.
type runsPreemptedStore struct{ faultRuns }

func (w runsPreemptedStore) Preempted(ctx context.Context, limit int) ([]stores.Run, error) {
	return runsPreempted(ctx, w.faultRuns, limit)
}

// runsCheckpointStore adds stores.CheckpointRuns, the token-fenced resume
// surface asserted on the Runs port by Stack.Recover, for inner stores that
// implement it.
type runsCheckpointStore struct{ faultRuns }

func (w runsCheckpointStore) SuspensionToken(ctx context.Context, runID string) (types.ResumeToken, error) {
	return runsSuspensionToken(ctx, w.faultRuns, runID)
}

func (w runsCheckpointStore) ResumingCheckpoint(ctx context.Context, runID string, t types.ResumeToken, ttl time.Duration) (stores.Lease, error) {
	return runsResumingCheckpoint(ctx, w.faultRuns, runID, t, ttl)
}

// runsCheckpointPreemptedStore adds both optional surfaces for inner stores
// that implement them.
type runsCheckpointPreemptedStore struct{ faultRuns }

func (w runsCheckpointPreemptedStore) Preempted(ctx context.Context, limit int) ([]stores.Run, error) {
	return runsPreempted(ctx, w.faultRuns, limit)
}

func (w runsCheckpointPreemptedStore) SuspensionToken(ctx context.Context, runID string) (types.ResumeToken, error) {
	return runsSuspensionToken(ctx, w.faultRuns, runID)
}

func (w runsCheckpointPreemptedStore) ResumingCheckpoint(ctx context.Context, runID string, t types.ResumeToken, ttl time.Duration) (stores.Lease, error) {
	return runsResumingCheckpoint(ctx, w.faultRuns, runID, t, ttl)
}

type faultSessionLog struct {
	stores.SessionLog
	f *Faults
}

func (w faultSessionLog) Load(ctx context.Context, sessionID string) (stores.History, error) {
	if w.f.step(PortSessionLog, "Load") {
		return stores.History{}, fail()
	}
	return w.SessionLog.Load(ctx, sessionID)
}

func (w faultSessionLog) Append(ctx context.Context, sessionID string, expectedVersion int64, msgs ...types.Message) (int64, error) {
	if w.f.step(PortSessionLog, "Append") {
		return 0, fail()
	}
	return w.SessionLog.Append(ctx, sessionID, expectedVersion, msgs...)
}

func (w faultSessionLog) Purge(ctx context.Context, olderThan time.Time) (int, error) {
	if w.f.step(PortSessionLog, "Purge") {
		return 0, fail()
	}
	return w.SessionLog.Purge(ctx, olderThan)
}

func (w faultSessionLog) Delete(ctx context.Context, sessionID string) error {
	if w.f.step(PortSessionLog, "Delete") {
		return fail()
	}
	return w.SessionLog.Delete(ctx, sessionID)
}

type faultEventLog struct {
	stores.EventLog
	f *Faults
}

func (w faultEventLog) Append(ctx context.Context, runID string, e stores.Event) error {
	if w.f.step(PortEventLog, "Append") {
		return fail()
	}
	return w.EventLog.Append(ctx, runID, e)
}

func (w faultEventLog) Read(ctx context.Context, runID string, afterSeq int64) iter.Seq2[stores.Event, error] {
	if w.f.step(PortEventLog, "Read") {
		return func(yield func(stores.Event, error) bool) {
			yield(stores.Event{}, ErrUnavailable)
		}
	}
	return w.EventLog.Read(ctx, runID, afterSeq)
}

func (w faultEventLog) Expire(ctx context.Context, olderThan time.Time) error {
	if w.f.step(PortEventLog, "Expire") {
		return fail()
	}
	return w.EventLog.Expire(ctx, olderThan)
}

type faultCheckpoints struct {
	stores.Checkpoints
	f *Faults
}

func (w faultCheckpoints) Put(ctx context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
	if w.f.step(PortCheckpoints, "Put") {
		return "", fail()
	}
	return w.Checkpoints.Put(ctx, cp)
}

func (w faultCheckpoints) Consume(ctx context.Context, t types.ResumeToken, in stores.ResumeInput) (stores.Checkpoint, error) {
	if w.f.step(PortCheckpoints, "Consume") {
		return stores.Checkpoint{}, fail()
	}
	return w.Checkpoints.Consume(ctx, t, in)
}

func (w faultCheckpoints) PendingInput(ctx context.Context, runID string) (stores.Checkpoint, stores.ResumeInput, error) {
	if w.f.step(PortCheckpoints, "PendingInput") {
		return stores.Checkpoint{}, stores.ResumeInput{}, fail()
	}
	return w.Checkpoints.PendingInput(ctx, runID)
}

func checkpointsResumeReady(ctx context.Context, w faultCheckpoints, limit int) ([]stores.Checkpoint, error) {
	if w.f.step(PortCheckpoints, "ResumeReady") {
		return nil, fail()
	}
	return w.Checkpoints.(stores.ResumeReadyLister).ResumeReady(ctx, limit)
}

func checkpointsPeek(ctx context.Context, w faultCheckpoints, t types.ResumeToken) (stores.Checkpoint, error) {
	if w.f.step(PortCheckpoints, "Peek") {
		return stores.Checkpoint{}, fail()
	}
	return w.Checkpoints.(stores.CheckpointResumer).Peek(ctx, t)
}

func checkpointsConsumeIf(ctx context.Context, w faultCheckpoints, t types.ResumeToken, expected stores.Checkpoint, in stores.ResumeInput) (stores.Checkpoint, error) {
	if w.f.step(PortCheckpoints, "ConsumeIf") {
		return stores.Checkpoint{}, fail()
	}
	return w.Checkpoints.(stores.CheckpointResumer).ConsumeIf(ctx, t, expected, in)
}

func checkpointsUpdatePending(ctx context.Context, w faultCheckpoints, t types.ResumeToken, expected, next stores.Checkpoint) error {
	if w.f.step(PortCheckpoints, "UpdatePending") {
		return fail()
	}
	return w.Checkpoints.(stores.CheckpointResumer).UpdatePending(ctx, t, expected, next)
}

// checkpointsResumeReadyStore adds stores.ResumeReadyLister for inner stores
// that implement it.
type checkpointsResumeReadyStore struct{ faultCheckpoints }

func (w checkpointsResumeReadyStore) ResumeReady(ctx context.Context, limit int) ([]stores.Checkpoint, error) {
	return checkpointsResumeReady(ctx, w.faultCheckpoints, limit)
}

// checkpointsResumerStore adds stores.CheckpointResumer, asserted by
// Stack.Recover recovery paths on the wrapped port, for inner stores that
// implement it.
type checkpointsResumerStore struct{ faultCheckpoints }

func (w checkpointsResumerStore) Peek(ctx context.Context, t types.ResumeToken) (stores.Checkpoint, error) {
	return checkpointsPeek(ctx, w.faultCheckpoints, t)
}

func (w checkpointsResumerStore) ConsumeIf(ctx context.Context, t types.ResumeToken, expected stores.Checkpoint, in stores.ResumeInput) (stores.Checkpoint, error) {
	return checkpointsConsumeIf(ctx, w.faultCheckpoints, t, expected, in)
}

func (w checkpointsResumerStore) UpdatePending(ctx context.Context, t types.ResumeToken, expected, next stores.Checkpoint) error {
	return checkpointsUpdatePending(ctx, w.faultCheckpoints, t, expected, next)
}

// checkpointsResumeAllStore adds both optional surfaces for inner stores
// that implement them.
type checkpointsResumeAllStore struct{ faultCheckpoints }

func (w checkpointsResumeAllStore) ResumeReady(ctx context.Context, limit int) ([]stores.Checkpoint, error) {
	return checkpointsResumeReady(ctx, w.faultCheckpoints, limit)
}

func (w checkpointsResumeAllStore) Peek(ctx context.Context, t types.ResumeToken) (stores.Checkpoint, error) {
	return checkpointsPeek(ctx, w.faultCheckpoints, t)
}

func (w checkpointsResumeAllStore) ConsumeIf(ctx context.Context, t types.ResumeToken, expected stores.Checkpoint, in stores.ResumeInput) (stores.Checkpoint, error) {
	return checkpointsConsumeIf(ctx, w.faultCheckpoints, t, expected, in)
}

func (w checkpointsResumeAllStore) UpdatePending(ctx context.Context, t types.ResumeToken, expected, next stores.Checkpoint) error {
	return checkpointsUpdatePending(ctx, w.faultCheckpoints, t, expected, next)
}

// wrapRuns returns the fault-injected runs port exposing exactly the
// optional surfaces the inner store implements.
func wrapRuns(r stores.Runs, f *Faults) stores.Runs {
	base := faultRuns{Runs: r, f: f}
	_, hasPreempted := r.(stores.PreemptedLister)
	_, hasCheckpoint := r.(stores.CheckpointRuns)
	switch {
	case hasPreempted && hasCheckpoint:
		return runsCheckpointPreemptedStore{base}
	case hasCheckpoint:
		return runsCheckpointStore{base}
	case hasPreempted:
		return runsPreemptedStore{base}
	}
	return base
}

// wrapCheckpoints returns the fault-injected checkpoints port exposing
// exactly the optional surfaces the inner store implements.
func wrapCheckpoints(c stores.Checkpoints, f *Faults) stores.Checkpoints {
	base := faultCheckpoints{Checkpoints: c, f: f}
	_, hasReady := c.(stores.ResumeReadyLister)
	_, hasResumer := c.(stores.CheckpointResumer)
	switch {
	case hasReady && hasResumer:
		return checkpointsResumeAllStore{base}
	case hasResumer:
		return checkpointsResumerStore{base}
	case hasReady:
		return checkpointsResumeReadyStore{base}
	}
	return base
}

// Wrap returns st with the named ports wrapped by f. With no ports named,
// every non-nil port in st is wrapped. Ports absent from st stay absent.
// Optional surfaces on the wrapped runs and checkpoints ports match the
// inner store's capabilities exactly.
func Wrap(st stores.Stores, f *Faults, ports ...Port) stores.Stores {
	wrap := func(p Port) {
		switch p {
		case PortJournal:
			if st.Journal != nil {
				st.Journal = faultJournal{Journal: st.Journal, f: f}
			}
		case PortRuns:
			if st.Runs != nil {
				st.Runs = wrapRuns(st.Runs, f)
			}
		case PortSessionLog:
			if st.SessionLog != nil {
				st.SessionLog = faultSessionLog{SessionLog: st.SessionLog, f: f}
			}
		case PortEventLog:
			if st.EventLog != nil {
				st.EventLog = faultEventLog{EventLog: st.EventLog, f: f}
			}
		case PortCheckpoints:
			if st.Checkpoints != nil {
				st.Checkpoints = wrapCheckpoints(st.Checkpoints, f)
			}
		}
	}
	if len(ports) == 0 {
		ports = []Port{PortJournal, PortRuns, PortSessionLog, PortEventLog, PortCheckpoints}
	}
	for _, p := range ports {
		wrap(p)
	}
	return st
}
