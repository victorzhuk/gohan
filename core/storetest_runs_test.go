package gohan_test

import (
	"context"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/storetest"
)

// runsBinding adapts the memory Runs implementation to the conformance
// suite's port view and drives both clocks from one handle.
type runsBinding struct {
	s *stores.MemoryRuns
}

func (b runsBinding) Start(ctx context.Context, r storetest.RunRow, ttl time.Duration) (storetest.Lease, error) {
	l, err := b.s.Start(ctx, toStoreRun(r), ttl)
	return storetest.Lease{RunID: l.RunID}, err
}

func (b runsBinding) Heartbeat(ctx context.Context, l storetest.Lease) (storetest.Lease, error) {
	got, err := b.s.Heartbeat(ctx, stores.Lease{RunID: l.RunID})
	return storetest.Lease{RunID: got.RunID}, err
}

func (b runsBinding) Suspend(ctx context.Context, l storetest.Lease, token types.ResumeToken) error {
	return b.s.Suspend(ctx, stores.Lease{RunID: l.RunID}, token)
}

func (b runsBinding) Resuming(ctx context.Context, runID string, ttl time.Duration) (storetest.Lease, error) {
	l, err := b.s.Resuming(ctx, runID, ttl)
	return storetest.Lease{RunID: l.RunID}, err
}

func (b runsBinding) Finish(ctx context.Context, l storetest.Lease, state storetest.RunState, uncertain []types.CallKey, resultRef string) error {
	return b.s.Finish(ctx, stores.Lease{RunID: l.RunID}, stores.RunState(state), uncertain, resultRef)
}

func (b runsBinding) ByOperation(ctx context.Context, tenant, operationID string) (storetest.RunRow, error) {
	r, err := b.s.ByOperation(ctx, tenant, operationID)
	if err != nil {
		return storetest.RunRow{}, err
	}
	return fromStoreRun(r), nil
}

func (b runsBinding) Stale(ctx context.Context, staleAfter time.Duration, limit int) ([]storetest.RunRow, error) {
	runs, err := b.s.Stale(ctx, staleAfter, limit)
	if err != nil {
		return nil, err
	}
	out := make([]storetest.RunRow, len(runs))
	for i, r := range runs {
		out[i] = fromStoreRun(r)
	}
	return out, nil
}

func (b runsBinding) Reclaim(ctx context.Context, r storetest.RunRow, ttl time.Duration) (storetest.Lease, error) {
	l, err := b.s.Reclaim(ctx, toStoreRun(r), ttl)
	return storetest.Lease{RunID: l.RunID}, err
}

func (b runsBinding) Signal(ctx context.Context, runID string, s storetest.Signal) error {
	return b.s.Signal(ctx, runID, stores.Signal{Kind: stores.SignalKind(s.Kind), Message: s.Message})
}

func (b runsBinding) Drain(ctx context.Context, l storetest.Lease) ([]storetest.Signal, error) {
	sigs, err := b.s.Drain(ctx, stores.Lease{RunID: l.RunID})
	if err != nil {
		return nil, err
	}
	out := make([]storetest.Signal, len(sigs))
	for i, sig := range sigs {
		out[i] = storetest.Signal{Kind: storetest.SignalKind(sig.Kind), Message: sig.Message}
	}
	return out, nil
}

func (b runsBinding) Notices(ctx context.Context, limit int) ([]types.RunNotice, error) {
	return b.s.Notices(ctx, limit)
}

func (b runsBinding) AckNotice(ctx context.Context, id string) error {
	return b.s.AckNotice(ctx, id)
}

func toStoreRun(r storetest.RunRow) stores.Run {
	return stores.Run{
		SessionID:   r.SessionID,
		RunID:       r.RunID,
		OperationID: r.OperationID,
		State:       stores.RunState(r.State),
		Heartbeat:   r.Heartbeat,
		Uncertain:   r.Uncertain,
		ResultRef:   r.ResultRef,
	}
}

func fromStoreRun(r stores.Run) storetest.RunRow {
	return storetest.RunRow{
		SessionID:   r.SessionID,
		RunID:       r.RunID,
		OperationID: r.OperationID,
		State:       storetest.RunState(r.State),
		Heartbeat:   r.Heartbeat,
		Uncertain:   r.Uncertain,
		ResultRef:   r.ResultRef,
	}
}

type storeClock struct {
	now *time.Time
}

func (c storeClock) Advance(d time.Duration) {
	*c.now = c.now.Add(d)
}

func newMemoryRuns(_ *testing.T) (storetest.RunStore, storetest.RunClock) {
	now := time.Now()
	s := stores.NewMemoryRuns(stores.WithMemoryRunClock(func() time.Time { return now }))
	return runsBinding{s: s}, storeClock{now: &now}
}

func TestStoretestRunsBind(t *testing.T) {
	storetest.Runs(t, newMemoryRuns)
}
