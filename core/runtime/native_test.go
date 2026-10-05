package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestNativeMissingModelEffect(t *testing.T) {
	n := NewNative()
	_, err := n.Start(context.Background(), AgentRun{BatchEffect: noopEffect})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, _, _, err = n.Step(context.Background(), State{Backend: phaseJSON(t, phaseModel)})
	if err == nil {
		t.Fatal("Step without ModelEffect: want error, got nil")
	}
	if !errors.Is(err, errMissingEffect) {
		t.Fatalf("Step error = %v, want errMissingEffect", err)
	}
}

func TestNativeMissingBatchEffect(t *testing.T) {
	n := NewNative()
	if _, err := n.Start(context.Background(), AgentRun{ModelEffect: noopEffect}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, _, _, err := n.Step(context.Background(), State{Backend: phaseJSON(t, phaseBatch)}); !errors.Is(err, errMissingEffect) {
		t.Fatalf("Step without BatchEffect: err = %v, want errMissingEffect", err)
	}
}

func TestNativeModelStepCallsOnlyModelEffect(t *testing.T) {
	called := ""
	n := NewNative()
	st, err := n.Start(context.Background(), AgentRun{
		ModelEffect: func(ctx context.Context, st State) (State, []types.Event, Status, error) {
			called = "model"
			return st, nil, Continue, nil
		},
		BatchEffect: func(ctx context.Context, st State) (State, []types.Event, Status, error) {
			called = "batch"
			return st, nil, Continue, nil
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, _, _, err = n.Step(context.Background(), st)
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if called != "model" {
		t.Fatalf("effect called = %q, want model", called)
	}
}

func TestNativeBatchStepCallsOnlyBatchEffect(t *testing.T) {
	called := ""
	n := NewNative()
	if _, err := n.Start(context.Background(), AgentRun{
		ModelEffect: func(ctx context.Context, st State) (State, []types.Event, Status, error) {
			called = "model"
			return st, nil, Continue, nil
		},
		BatchEffect: func(ctx context.Context, st State) (State, []types.Event, Status, error) {
			called = "batch"
			return st, nil, Continue, nil
		},
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, _, _, err := n.Step(context.Background(), State{Backend: phaseJSON(t, phaseBatch)}); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if called != "batch" {
		t.Fatalf("effect called = %q, want batch", called)
	}
}

func TestNativePhaseRoundTripsThroughBackend(t *testing.T) {
	called := ""
	r := AgentRun{
		ModelEffect: func(ctx context.Context, st State) (State, []types.Event, Status, error) {
			called = "model"
			return st, nil, Continue, nil
		},
		BatchEffect: func(ctx context.Context, st State) (State, []types.Event, Status, error) {
			called = "batch"
			return st, nil, Continue, nil
		},
	}
	n := NewNative()
	st, err := n.Start(context.Background(), r)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	st, _, _, err = n.Step(context.Background(), st)
	if err != nil {
		t.Fatalf("first Step: %v", err)
	}

	// Re-encode like a checkpoint round trip, then resume in the batch phase.
	enc, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	var resumed State
	if err := json.Unmarshal(enc, &resumed); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}

	n2 := NewNative()
	if _, err := n2.Start(context.Background(), r); err != nil {
		t.Fatalf("resume Start: %v", err)
	}
	if _, _, _, err := n2.Step(context.Background(), resumed); err != nil {
		t.Fatalf("resumed Step: %v", err)
	}
	if called != "batch" {
		t.Fatalf("resumed effect = %q, want batch", called)
	}
}

func TestNativeGranularity(t *testing.T) {
	if got := (NewNative()).Granularity(); got != GranularityEffect {
		t.Fatalf("Granularity() = %v, want GranularityEffect", got)
	}
}

func noopEffect(ctx context.Context, st State) (State, []types.Event, Status, error) {
	return st, nil, Continue, nil
}

func phaseJSON(t *testing.T, phase string) []byte {
	t.Helper()
	enc, err := json.Marshal(nativePhase{Phase: phase})
	if err != nil {
		t.Fatalf("marshal phase: %v", err)
	}
	return enc
}
