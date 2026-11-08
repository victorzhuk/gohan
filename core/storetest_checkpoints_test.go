package gohan_test

import (
	"context"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/storetest"
)

// checkpointBinding adapts the memory Checkpoints implementation to the
// conformance suite's port view; adapter/postgres binds the same suites
// through the mirror interfaces.
type checkpointBinding struct {
	s *stores.MemoryCheckpoints
}

func (b *checkpointBinding) Put(ctx context.Context, cp storetest.Checkpoint) (types.ResumeToken, error) {
	return b.s.Put(ctx, toStoresCheckpoint(cp))
}

func (b *checkpointBinding) Consume(ctx context.Context, t types.ResumeToken, in storetest.ResumeInput) (storetest.Checkpoint, error) {
	cp, err := b.s.Consume(ctx, t, toStoresResumeInput(in))
	if err != nil {
		return storetest.Checkpoint{}, err
	}
	return toSuiteCheckpoint(cp), nil
}

func (b *checkpointBinding) PendingInput(ctx context.Context, runID string) (storetest.Checkpoint, storetest.ResumeInput, error) {
	cp, in, err := b.s.PendingInput(ctx, runID)
	if err != nil {
		return storetest.Checkpoint{}, storetest.ResumeInput{}, err
	}
	return toSuiteCheckpoint(cp), toSuiteResumeInput(in), nil
}

func toStoresCheckpoint(cp storetest.Checkpoint) stores.Checkpoint {
	return stores.Checkpoint{
		RunID:          cp.RunID,
		SessionID:      cp.SessionID,
		Flow:           cp.Flow,
		Backend:        cp.Backend,
		BackendVersion: cp.BackendVersion,
		Reason:         cp.Reason,
		Originator:     cp.Originator,
		Data:           cp.Data,
		Child:          cp.Child,
		Workspace:      stores.WorkspaceRef(cp.Workspace),
		ExpiresAt:      cp.ExpiresAt,
		Token:          cp.Token,
	}
}

func toSuiteCheckpoint(cp stores.Checkpoint) storetest.Checkpoint {
	return storetest.Checkpoint{
		RunID:          cp.RunID,
		SessionID:      cp.SessionID,
		Flow:           cp.Flow,
		Backend:        cp.Backend,
		BackendVersion: cp.BackendVersion,
		Reason:         cp.Reason,
		Originator:     cp.Originator,
		Data:           cp.Data,
		Child:          cp.Child,
		Workspace:      string(cp.Workspace),
		ExpiresAt:      cp.ExpiresAt,
		Token:          cp.Token,
	}
}

func toStoresResumeInput(in storetest.ResumeInput) stores.ResumeInput {
	return stores.ResumeInput{
		Approver: in.Approver,
		Verdict:  stores.ApprovalVerdict(in.Verdict),
		Args:     in.Args,
		Data:     in.Data,
		Reason:   in.Reason,
	}
}

func toSuiteResumeInput(in stores.ResumeInput) storetest.ResumeInput {
	return storetest.ResumeInput{
		Approver: in.Approver,
		Verdict:  storetest.ApprovalVerdict(in.Verdict),
		Args:     in.Args,
		Data:     in.Data,
		Reason:   in.Reason,
	}
}

func TestStoretestCheckpointsBind(t *testing.T) {
	storetest.Checkpoints(t, func(ctx context.Context, runID string) (storetest.CheckpointStore, error) {
		s := stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointRunInfo(
			func(context.Context) (types.RunInfo, bool) {
				return types.RunInfo{RunID: runID}, true
			},
		))
		return &checkpointBinding{s: s}, nil
	})
}

func TestStoretestCheckpointsLifetimeBind(t *testing.T) {
	storetest.CheckpointsLifetime(t, func(t *testing.T, runID string, ttl time.Duration) (storetest.CheckpointStore, error) {
		now := time.Now()
		s := stores.NewMemoryCheckpoints(
			stores.WithMemoryCheckpointClock(func() time.Time { return now }),
			stores.WithMemoryCheckpointRunInfo(func(context.Context) (types.RunInfo, bool) {
				return types.RunInfo{RunID: runID}, true
			}),
			stores.WithMemoryCheckpointTTL(ttl),
		)
		return &checkpointBinding{s: s}, nil
	})
}

// recoveryBinding pairs the memory checkpoints and runs stores the way
// recovery discovery consults them: ResumeReady reads the run surface for
// current-token and suspended-state evidence.
type recoveryBinding struct {
	*checkpointBinding
	runsBinding
}

func (b *recoveryBinding) ResumeReady(ctx context.Context, limit int) ([]storetest.Checkpoint, error) {
	cps, err := b.checkpointBinding.s.ResumeReady(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]storetest.Checkpoint, len(cps))
	for i, cp := range cps {
		out[i] = toSuiteCheckpoint(cp)
	}
	return out, nil
}

func TestStoretestRecoveryDiscoveryBind(t *testing.T) {
	storetest.RecoveryDiscovery(t, func(t *testing.T) (storetest.RecoveryStore, error) {
		now := time.Now()
		clock := func() time.Time { return now }
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(clock))
		s := stores.NewMemoryCheckpoints(
			stores.WithMemoryCheckpointClock(clock),
			stores.WithMemoryCheckpointRuns(runs),
		)
		return &recoveryBinding{
			checkpointBinding: &checkpointBinding{s: s},
			runsBinding:       runsBinding{s: runs},
		}, nil
	})
}
