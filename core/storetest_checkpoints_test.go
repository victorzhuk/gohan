package gohan

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/storetest"
)

// adapter/postgres binds the same suite in M3 through this mirror interface.
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
	}
}

func toSuiteCheckpoint(cp stores.Checkpoint) storetest.Checkpoint {
	return storetest.Checkpoint{
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
