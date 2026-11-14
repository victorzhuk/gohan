package adkgo

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

func TestNewDefaults(t *testing.T) {
	rt, err := New(context.Background())
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	if rt.Name() != "adkgo" {
		t.Errorf("Name = %q, want adkgo", rt.Name())
	}
	if rt.Granularity() != runtime.GranularityTurn {
		t.Errorf("Granularity = %v, want GranularityTurn", rt.Granularity())
	}
	if rt.BackendVersion() != "adkgo-v2" {
		t.Errorf("BackendVersion = %q, want adkgo-v2", rt.BackendVersion())
	}
	if rt.FlowKind() != stores.FlowAgent {
		t.Errorf("FlowKind = %v, want FlowAgent", rt.FlowKind())
	}
}

func TestStartRefusesNilModel(t *testing.T) {
	rt, err := New(context.Background())
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	_, err = rt.Start(context.Background(), runtime.AgentRun{})
	if err == nil {
		t.Fatal("Start without a model error = nil, want an error")
	}
}

func TestDeclaredFallbacks(t *testing.T) {
	want := map[string]bool{
		"journal-fingerprint":   false,
		"step-notices":          false,
		"block-origin-sequence": false,
		"cache-breakpoints":     false,
		"native-pause-resume":   false,
	}
	for _, f := range DeclaredFallbacks() {
		if _, ok := want[f.Capability]; !ok {
			t.Errorf("unexpected declared fallback %q", f.Capability)
			continue
		}
		want[f.Capability] = true
		if f.Mode != FallbackReplay {
			t.Errorf("fallback %q mode = %q, want replay", f.Capability, f.Mode)
		}
	}
	for cap, seen := range want {
		if !seen {
			t.Errorf("declared fallbacks miss %q", cap)
		}
	}
}

func TestUndeclaredGapWrapsSentinel(t *testing.T) {
	err := requireFallback("time-travel")
	if !errors.Is(err, ErrUndeclaredFallback) {
		t.Fatalf("requireFallback error = %v, want ErrUndeclaredFallback wrap", err)
	}
	for _, cap := range []string{"journal-fingerprint", "step-notices", "block-origin-sequence", "cache-breakpoints"} {
		if err := requireFallback(cap); err != nil {
			t.Errorf("requireFallback(%q) error = %v, want declared", cap, err)
		}
	}
}

func TestCheckpointRoundTrip(t *testing.T) {
	rt, err := New(context.Background())
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	hist := stores.History{Messages: []types.Message{
		{ID: "h-1", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "old"}}},
	}}
	input := []types.Message{
		{ID: "in-1", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}},
	}
	if _, err := rt.Start(context.Background(), runtime.AgentRun{
		Model:   gohantest.NewScriptedModel(conformanceProfile, gohantest.Text("done")),
		History: hist,
		Input:   input,
	}); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	st := rt.withCheckpoint(runtime.State{Turn: 2})
	if len(st.Backend) == 0 {
		t.Fatal("checkpoint backend = empty, want an envelope")
	}
	var fresh Runtime
	fresh.restore(st)
	if !reflect.DeepEqual(fresh.msgs, rt.msgs) {
		t.Fatalf("restored history = %+v, want %+v", fresh.msgs, rt.msgs)
	}
	var cp backendCheckpoint
	if err := json.Unmarshal(st.Backend, &cp); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if cp.Version != "adkgo-v2" || cp.Turn != 2 || len(cp.Msgs) != 2 {
		t.Errorf("envelope = %+v, want version adkgo-v2, turn 2, two messages", cp)
	}
	if len(cp.InterruptIDs) != 0 {
		t.Errorf("InterruptIDs = %v, want empty for a checkpoint without live interrupts", cp.InterruptIDs)
	}
}

func TestStepCancelMidTurn(t *testing.T) {
	rt, err := New(context.Background())
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	m := &blockingModel{entered: make(chan struct{})}
	if _, err := rt.Start(context.Background(), runtime.AgentRun{Model: m}); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-m.entered
		cancel()
	}()
	done := make(chan error, 1)
	go func() {
		_, _, _, err := rt.Step(ctx, runtime.State{})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Step error = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Step did not stop after cancellation")
	}
}
