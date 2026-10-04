package stores

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestCheckpointCompatibility(t *testing.T) {
	t.Run("stores.native-checkpoint-after-adapter-upgrade", func(t *testing.T) {
		cp := Checkpoint{
			SchemaVersion:  CurrentSchemaVersion,
			Flow:           "agent",
			Backend:        "eino",
			BackendVersion: "adapter-x",
		}
		plan, err := PlanResume(cp, FlowAgent, "adapter-y")
		if err != nil {
			t.Fatalf("PlanResume across adapter upgrade: %v", err)
		}
		if plan != ResumeReplay {
			t.Fatalf("PlanResume across adapter upgrade: got plan %d, want ResumeReplay", plan)
		}
	})

	t.Run("stores.graph-checkpoint-incompatible", func(t *testing.T) {
		cps := NewMemoryCheckpoints()
		cp := Checkpoint{
			SchemaVersion:  CurrentSchemaVersion,
			Flow:           "graph",
			Backend:        "eino",
			BackendVersion: "adapter-x",
		}
		tok, err := cps.Put(context.Background(), cp)
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		_, err = PlanResume(cp, FlowGraph, "adapter-y")
		if !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("PlanResume across adapter upgrade: got %v, want ErrCheckpointIncompatible", err)
		}
		if _, err := cps.Consume(context.Background(), tok, ResumeInput{}); err != nil {
			t.Fatalf("Consume after refused plan: %v, want the token still unconsumed", err)
		}
	})

	t.Run("same backend version resumes natively", func(t *testing.T) {
		cp := Checkpoint{
			SchemaVersion:  CurrentSchemaVersion,
			Backend:        "eino",
			BackendVersion: "adapter-x",
		}
		plan, err := PlanResume(cp, FlowGraph, "adapter-x")
		if err != nil {
			t.Fatalf("PlanResume at the same version: %v", err)
		}
		if plan != ResumeNative {
			t.Fatalf("PlanResume at the same version: got plan %d, want ResumeNative", plan)
		}
	})

	t.Run("checkpoint written by a newer release is incompatible", func(t *testing.T) {
		cp := Checkpoint{SchemaVersion: CurrentSchemaVersion + 1, BackendVersion: "adapter-x"}
		if _, err := PlanResume(cp, FlowAgent, "adapter-x"); !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("PlanResume with a newer schema: got %v, want ErrCheckpointIncompatible", err)
		}
	})
}
