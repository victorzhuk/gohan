package gohan

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	jsonv2 "encoding/json/v2"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// foreignAdmissionKey is reserved session-history metadata. A message that
// carries it is a core-written foreign admission record; a caller who
// supplies it through Send is refused like a forged approval receipt.
const foreignAdmissionKey = "gohan.foreign.admission"

// foreignProgressVersion is independent of the checkpoint envelope
// version: persisted admission records must keep decoding while the
// envelope format moves on.
const foreignProgressVersion = 1

type foreignProgressKey struct{}

// foreignAdmission is the durable per-identity record one newly admitted
// foreign tool call appends before its gate. AdmittedTotal is the run's
// cumulative admitted count at the moment of the admission, so a scan
// reproduces the exact total even when checkpoints are older than the
// history.
type foreignAdmission struct {
	Version       int    `json:"version"`
	RunID         string `json:"run_id"`
	AdmittedTotal int    `json:"admitted_total"`
	CallID        string `json:"call_id"`
}

func decodeForeignAdmission(value any) (foreignAdmission, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return foreignAdmission{}, fmt.Errorf("%w: unreadable foreign admission record", types.ErrCheckpointIncompatible)
	}
	var rec foreignAdmission
	if err := jsonv2.Unmarshal(raw, &rec); err != nil {
		return foreignAdmission{}, fmt.Errorf("%w: malformed foreign admission record: %s", types.ErrCheckpointIncompatible, err)
	}
	if rec.Version != foreignProgressVersion {
		return foreignAdmission{}, fmt.Errorf("%w: unsupported foreign admission record version %d", types.ErrCheckpointIncompatible, rec.Version)
	}
	if rec.RunID == "" || rec.CallID == "" || rec.AdmittedTotal <= 0 {
		return foreignAdmission{}, fmt.Errorf("%w: incomplete foreign admission record", types.ErrCheckpointIncompatible)
	}
	return rec, nil
}

func foreignAdmissionMessage(rec foreignAdmission) (types.Message, error) {
	rec.Version = foreignProgressVersion
	if rec.RunID == "" || rec.CallID == "" || rec.AdmittedTotal <= 0 {
		return types.Message{}, fmt.Errorf("%w: incomplete foreign admission record", types.ErrCheckpointIncompatible)
	}
	return types.Message{Role: types.RoleAssistant, Meta: map[string]any{foreignAdmissionKey: rec}}, nil
}

func withForeignProgress(ctx context.Context, p *runtime.ForeignProgress) context.Context {
	return context.WithValue(ctx, foreignProgressKey{}, p)
}

func foreignProgressFrom(ctx context.Context) (*runtime.ForeignProgress, bool) {
	p, ok := ctx.Value(foreignProgressKey{}).(*runtime.ForeignProgress)
	return p, ok && p != nil
}

// validateForeignProgress refuses persisted accounting core cannot trust:
// an unknown version, a negative total, empty or duplicated identities, or
// a total that does not equal the retained identity count.
func validateForeignProgress(p *runtime.ForeignProgress) error {
	if p == nil {
		return nil
	}
	if p.Version != foreignProgressVersion {
		return fmt.Errorf("%w: unsupported foreign progress version %d", types.ErrCheckpointIncompatible, p.Version)
	}
	if p.AdmittedTotal < 0 {
		return fmt.Errorf("%w: negative foreign admitted total", types.ErrCheckpointIncompatible)
	}
	seen := make(map[string]bool, len(p.ReplayCallIDs))
	for _, id := range p.ReplayCallIDs {
		if id == "" || seen[id] {
			return fmt.Errorf("%w: foreign replay call ids must be nonempty and distinct", types.ErrCheckpointIncompatible)
		}
		seen[id] = true
	}
	if p.AdmittedTotal != len(p.ReplayCallIDs) {
		return fmt.Errorf("%w: foreign admitted total %d does not match %d replay identities", types.ErrCheckpointIncompatible, p.AdmittedTotal, len(p.ReplayCallIDs))
	}
	return nil
}

// foreignRunState is one foreign run's admission accounting. The governed
// tool closure and the accounting decorator share one instance, so the
// cumulative total, the replay identities and the history version the
// admission appends advance stay consistent across every governed call of
// the run.
type foreignRunState struct {
	mu     sync.Mutex
	runID  string
	total  int
	order  []string
	ids    map[string]bool
	curVer int64
}

func newForeignRunState(runID string, histVer int64) *foreignRunState {
	return &foreignRunState{runID: runID, ids: map[string]bool{}, curVer: histVer}
}

func (fs *foreignRunState) admitted(id string) bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.ids[id]
}

func (fs *foreignRunState) totalCalls() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.total
}

func (fs *foreignRunState) currentVersion() int64 {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.curVer
}

func (fs *foreignRunState) setVersion(v int64) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if v > fs.curVer {
		fs.curVer = v
	}
}

// restore adopts validated checkpoint accounting.
func (fs *foreignRunState) restore(p *runtime.ForeignProgress) error {
	if err := validateForeignProgress(p); err != nil {
		return err
	}
	if p == nil {
		return nil
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if p.AdmittedTotal < fs.total {
		return fmt.Errorf("%w: foreign progress total %d is older than the restored total %d", types.ErrCheckpointIncompatible, p.AdmittedTotal, fs.total)
	}
	fs.total = p.AdmittedTotal
	for _, id := range p.ReplayCallIDs {
		if !fs.ids[id] {
			fs.ids[id] = true
			fs.order = append(fs.order, id)
		}
	}
	return nil
}

// admit merges one durable admission record: totals must grow by exactly
// one per new identity, and a record whose identity is already retained is
// an idempotent re-read of an older scan.
func (fs *foreignRunState) admit(callID string, total int) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.ids[callID] {
		return nil
	}
	if total != fs.total+1 {
		return fmt.Errorf("%w: foreign admission total %d breaks the monotonic sequence at %d", types.ErrCheckpointIncompatible, total, fs.total)
	}
	fs.total = total
	fs.ids[callID] = true
	fs.order = append(fs.order, callID)
	return nil
}

// record commits one freshly appended admission: the append already
// succeeded, so the identity and its cumulative total become visible to
// every later governed call.
func (fs *foreignRunState) record(callID string, total int) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.ids[callID] {
		return
	}
	fs.total = total
	fs.ids[callID] = true
	fs.order = append(fs.order, callID)
}

func (fs *foreignRunState) progress() *runtime.ForeignProgress {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.total == 0 {
		return nil
	}
	return &runtime.ForeignProgress{
		Version:       foreignProgressVersion,
		AdmittedTotal: fs.total,
		ReplayCallIDs: append([]string(nil), fs.order...),
	}
}

// scanForeignAdmissions rebuilds accounting from the run's admission
// records in session history, merging records newer than any checkpoint
// progress the run already restored. A malformed or non-monotonic record
// fails closed instead of fabricating counts.
func scanForeignAdmissions(fs *foreignRunState, h stores.History) error {
	for _, msg := range h.Messages {
		raw, ok := msg.Meta[foreignAdmissionKey]
		if !ok {
			continue
		}
		rec, err := decodeForeignAdmission(raw)
		if err != nil {
			return err
		}
		if rec.RunID != fs.runID {
			continue
		}
		if err := fs.admit(rec.CallID, rec.AdmittedTotal); err != nil {
			return err
		}
	}
	return nil
}

// foreignAccountingRuntime wraps the backend runtime a foreign flow
// declared and snapshots the shared run accounting onto every State the
// backend returns: normal advancement, suspension, error and terminal
// completion all carry the same cumulative total. Name and granularity
// stay the backend's, so checkpoint binding and step boundaries are
// unchanged.
type foreignAccountingRuntime struct {
	rt runtime.Runtime
	fs *foreignRunState
}

func (d foreignAccountingRuntime) Name() string                         { return d.rt.Name() }
func (d foreignAccountingRuntime) Granularity() runtime.StepGranularity { return d.rt.Granularity() }

func (d foreignAccountingRuntime) Start(ctx context.Context, r runtime.AgentRun) (runtime.State, error) {
	st, err := d.rt.Start(ctx, r)
	if err != nil {
		return st, err
	}
	return d.overlay(st), nil
}

func (d foreignAccountingRuntime) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	nst, evs, status, err := d.rt.Step(ctx, st)
	return d.overlay(nst), evs, status, err
}

// overlay stamps the owned progress and the highest history version the
// admission appends reached onto the returned state, so a checkpoint the
// drive persists never loses an admission the Step recorded.
func (d foreignAccountingRuntime) overlay(st runtime.State) runtime.State {
	if p := d.fs.progress(); p != nil {
		st.Foreign = p
	}
	if v := d.fs.currentVersion(); v > st.HistoryVersion {
		st.HistoryVersion = v
	}
	return st
}
