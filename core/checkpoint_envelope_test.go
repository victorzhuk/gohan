package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type stubRuntime struct{}

func (stubRuntime) Name() string                    { return "stub" }
func (stubRuntime) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }
func (stubRuntime) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}
func (stubRuntime) Step(context.Context, runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	return runtime.State{}, nil, runtime.DoneStatus, nil
}

func envelopeCheckpoint() (stores.Checkpoint, checkpointEnvelope) {
	cp := stores.Checkpoint{
		RunID:     "run-1",
		SessionID: "s-1",
		Flow:      "flow-a",
		Backend:   "stub",
		Reason:    types.AwaitingTool,
	}
	env := checkpointEnvelope{
		Generation: 1,
		Run:        types.RunInfo{Flow: "flow-a", SessionID: "s-1", RunID: "run-1"},
		State: runtime.State{
			Turn:           2,
			HistoryVersion: 7,
			Pending:        []types.ToolUse{{ID: "c1", Name: "book", Args: json.RawMessage(`{"a":1}`)}},
		},
	}
	return cp, env
}

func TestCheckpointEnvelopeRoundTrip(t *testing.T) {
	cp, env := envelopeCheckpoint()
	data, err := encodeCheckpoint(env)
	if err != nil {
		t.Fatal(err)
	}
	cp.Data = data
	got, state, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Run.RunID != "run-1" || got.Run.SessionID != "s-1" || got.Run.Flow != "flow-a" {
		t.Fatalf("run identity = %+v", got.Run)
	}
	if got.Generation != 1 {
		t.Fatalf("Generation = %d, want 1", got.Generation)
	}
	if len(got.State.Pending) != 1 || got.State.Pending[0].Name != "book" {
		t.Fatalf("state pending = %+v", got.State.Pending)
	}
	if state.HistoryVersion != 7 || state.Turn != 2 {
		t.Fatalf("state = %+v", state)
	}
}

func TestCheckpointEnvelopeDecode(t *testing.T) {
	t.Run("legacy-raw-state-accepted", func(t *testing.T) {
		cp, _ := envelopeCheckpoint()
		cp.Data = []byte(`{"Turn":1,"HistoryVersion":3,"Backend":"bmV4dA=="}`)
		_, state, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a")
		if err != nil {
			t.Fatal(err)
		}
		if state.Turn != 1 || state.HistoryVersion != 3 {
			t.Fatalf("state = %+v", state)
		}
	})

	t.Run("legacy-human-approval-refused", func(t *testing.T) {
		cp, _ := envelopeCheckpoint()
		cp.Reason = types.HumanApproval
		cp.Data = []byte(`{"Turn":1}`)
		_, _, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a")
		if !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("err = %v, want ErrCheckpointIncompatible", err)
		}
	})

	t.Run("legacy-without-run-identity-refused", func(t *testing.T) {
		cp, _ := envelopeCheckpoint()
		cp.RunID = ""
		cp.Data = []byte(`{"Turn":1}`)
		_, _, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a")
		if !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("err = %v, want ErrCheckpointIncompatible", err)
		}
	})

	t.Run("corrupt-envelope-refused-without-raw-fallback", func(t *testing.T) {
		cp, _ := envelopeCheckpoint()
		cp.Data = []byte(`{"version":1,"run":{broken`)
		_, _, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a")
		if !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("err = %v, want ErrCheckpointIncompatible", err)
		}
	})

	t.Run("unsupported-envelope-version-refused", func(t *testing.T) {
		cp, env := envelopeCheckpoint()
		cp.Data = []byte(`{"version":2,"Turn":0}`)
		_ = env
		_, _, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a")
		if !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("err = %v, want ErrCheckpointIncompatible", err)
		}
	})

	t.Run("duplicate-key-refused", func(t *testing.T) {
		cp, _ := envelopeCheckpoint()
		cp.Data = []byte(`{"Turn":1,"Turn":2}`)
		_, _, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a")
		if !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("err = %v, want ErrCheckpointIncompatible", err)
		}
	})

	t.Run("trailing-value-refused", func(t *testing.T) {
		cp, _ := envelopeCheckpoint()
		cp.Data = []byte(`{"Turn":1} {"x":1}`)
		_, _, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a")
		if !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("err = %v, want ErrCheckpointIncompatible", err)
		}
	})

	t.Run("empty-and-non-object-refused", func(t *testing.T) {
		cp, _ := envelopeCheckpoint()
		for _, data := range []string{``, `   `, `null`, `[1,2]`, `"x"`, `{` /* malformed */ } {
			cp.Data = []byte(data)
			if _, _, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a"); !errors.Is(err, types.ErrCheckpointIncompatible) {
				t.Fatalf("data %q: err = %v, want ErrCheckpointIncompatible", data, err)
			}
		}
	})

	t.Run("unrecognized-keys-refused", func(t *testing.T) {
		cp, _ := envelopeCheckpoint()
		cp.Data = []byte(`{"wat":true}`)
		if _, _, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a"); !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("err = %v, want ErrCheckpointIncompatible", err)
		}
	})

	t.Run("session-mismatch-refused", func(t *testing.T) {
		cp, env := envelopeCheckpoint()
		data, err := encodeCheckpoint(env)
		if err != nil {
			t.Fatal(err)
		}
		cp.Data = data
		cp.SessionID = "s-other"
		if _, _, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a"); !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("err = %v, want ErrCheckpointIncompatible", err)
		}
	})

	t.Run("run-id-mismatch-refused", func(t *testing.T) {
		cp, env := envelopeCheckpoint()
		data, err := encodeCheckpoint(env)
		if err != nil {
			t.Fatal(err)
		}
		cp.Data = data
		cp.RunID = "run-2"
		if _, _, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a"); !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("err = %v, want ErrCheckpointIncompatible", err)
		}
	})

	t.Run("flow-mismatch-refused", func(t *testing.T) {
		cp, env := envelopeCheckpoint()
		data, err := encodeCheckpoint(env)
		if err != nil {
			t.Fatal(err)
		}
		cp.Data = data
		if _, _, err := decodeCheckpoint(cp, stubRuntime{}, "flow-b"); !errors.Is(err, types.ErrTokenMismatch) {
			t.Fatalf("err = %v, want ErrTokenMismatch", err)
		}
	})

	t.Run("backend-mismatch-refused", func(t *testing.T) {
		cp, env := envelopeCheckpoint()
		data, err := encodeCheckpoint(env)
		if err != nil {
			t.Fatal(err)
		}
		cp.Data = data
		cp.Backend = "other"
		if _, _, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a"); !errors.Is(err, types.ErrTokenMismatch) {
			t.Fatalf("err = %v, want ErrTokenMismatch", err)
		}
	})

	t.Run("negative-turn-refused", func(t *testing.T) {
		cp, _ := envelopeCheckpoint()
		cp.Data = []byte(`{"Turn":-1}`)
		if _, _, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a"); !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("err = %v, want ErrCheckpointIncompatible", err)
		}
	})

	t.Run("approval-without-pending-call-refused", func(t *testing.T) {
		cp, env := envelopeCheckpoint()
		env.Approvals = []checkpointApproval{
			{Call: types.ToolUse{ID: "cX", Name: "book", Args: json.RawMessage(`{}`)}, Risk: types.RiskHigh},
		}
		data, err := encodeCheckpoint(env)
		if err != nil {
			t.Fatal(err)
		}
		cp.Data = data
		if _, _, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a"); !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("err = %v, want ErrCheckpointIncompatible", err)
		}
	})

	t.Run("duplicate-pending-ids-refused", func(t *testing.T) {
		cp, _ := envelopeCheckpoint()
		cp.Data = []byte(`{"Turn":0,"Pending":[{"ID":"c1","Name":"a","Args":{}},{"ID":"c1","Name":"b","Args":{}}]}`)
		if _, _, err := decodeCheckpoint(cp, stubRuntime{}, "flow-a"); !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("err = %v, want ErrCheckpointIncompatible", err)
		}
	})
}
