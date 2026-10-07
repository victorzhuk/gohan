package eino

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestBackendCheckpoint(t *testing.T) {
	t.Run("backend_checkpoint_bytes_survive_the_round_trip", func(t *testing.T) {
		ctx := context.Background()
		rt, err := New(ctx, &adk.ChatModelAgent{})
		if err != nil {
			t.Fatal(err)
		}
		if rt.Granularity() != runtime.GranularityTurn {
			t.Fatalf("granularity before the round trip = %v, want turn", rt.Granularity())
		}
		st, err := rt.Start(ctx, suspendAgentRun(suspendModel(), &parkTool{}))
		if err != nil {
			t.Fatal(err)
		}
		st, _, _, err = rt.Step(ctx, st)
		var se *types.SuspendError
		if !errors.As(err, &se) {
			t.Fatalf("first step err = %v, want a park", err)
		}
		if len(st.Backend) == 0 {
			t.Fatal("suspend carried no backend checkpoint bytes")
		}
		var env backendCheckpoint
		if err := json.Unmarshal(st.Backend, &env); err != nil {
			t.Fatalf("backend envelope: %v", err)
		}
		if env.Version != backendCheckpointVersion {
			t.Fatalf("envelope version = %q, want %q", env.Version, backendCheckpointVersion)
		}
		if len(env.Msgs) == 0 {
			t.Fatal("envelope carries no working history")
		}

		// The driver persists the backend bytes through the checkpoint
		// envelope; Checkpoint.Data is the carrier the bytes ride in and
		// BackendVersion names their format. No core writer fills
		// BackendVersion yet, so the persisting side sets it here.
		cps := stores.NewMemoryCheckpoints()
		token, err := cps.Put(ctx, stores.Checkpoint{
			RunID:          "r1",
			SessionID:      "s1",
			Flow:           "chat",
			Backend:        "eino",
			BackendVersion: backendCheckpointVersion,
			Reason:         se.Reason,
			Data:           st.Backend,
			ExpiresAt:      time.Now().Add(time.Minute),
		})
		if err != nil {
			t.Fatalf("put checkpoint: %v", err)
		}
		cp, err := cps.Consume(ctx, token, stores.ResumeInput{Verdict: stores.VerdictApprove})
		if err != nil {
			t.Fatalf("consume before backend resume: %v", err)
		}
		if cp.BackendVersion != backendCheckpointVersion || string(cp.Data) != string(st.Backend) {
			t.Fatalf("consumed checkpoint version %q data %q, want the persisted envelope", cp.BackendVersion, cp.Data)
		}

		resumed, err := New(ctx, &adk.ChatModelAgent{})
		if err != nil {
			t.Fatal(err)
		}
		ag := suspendAgentRun(suspendModel(), &parkTool{})
		ag.History = stores.History{Messages: env.Msgs}
		if _, err := resumed.Start(ctx, ag); err != nil {
			t.Fatal(err)
		}
		resumed.restore(runtime.State{Backend: cp.Data})
		if resumed.Granularity() != runtime.GranularityTurn {
			t.Fatalf("granularity after the round trip = %v, want turn", resumed.Granularity())
		}
		if len(resumed.msgs) != len(env.Msgs) {
			t.Fatalf("restored history = %d messages, want %d", len(resumed.msgs), len(env.Msgs))
		}
	})
}
