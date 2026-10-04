package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type captureSink struct {
	mu     sync.Mutex
	events []types.Event
}

func (s *captureSink) Emit(_ context.Context, e types.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *captureSink) stateChanged() []types.StateChanged {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.StateChanged
	for _, e := range s.events {
		if sc, ok := e.(types.StateChanged); ok {
			out = append(out, sc)
		}
	}
	return out
}

// stubStateMeta is a hand-rolled SessionStateMeta with the same
// expected-version rule as the memory store.
type stubStateMeta struct {
	mu      sync.Mutex
	value   json.RawMessage
	version int64
}

func (m *stubStateMeta) SharedStateMeta(context.Context, string) (json.RawMessage, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.value, m.version, nil
}

func (m *stubStateMeta) SetSharedStateMeta(_ context.Context, _ string, expected int64, value json.RawMessage) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.version != expected {
		return 0, types.ErrVersionConflict
	}
	m.value = value
	m.version = expected + 1
	return m.version, nil
}

type stateSnapshot struct {
	Step  int
	Label string
}

func newStateSession(t *testing.T) (*stores.MemorySessionLog, context.Context, string) {
	t.Helper()
	store := stores.NewMemorySessionLog(
		stores.WithSessionPrincipals(func(ctx context.Context) (types.Principal, bool) {
			return types.Principal{Tenant: "t-a", Subject: "u1"}, true
		}),
	)
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t-a", Subject: "u1"})
	if _, err := store.Append(ctx, "sess", 0, types.Message{Role: types.RoleUser}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return store, ctx, "sess"
}

func TestSharedStateRestore(t *testing.T) {
	t.Run("working-state.shared-state-restored", func(t *testing.T) {
		store, ctx, sess := newStateSession(t)
		sctx, err := WithSessionState(ctx, store, sess)
		if err != nil {
			t.Fatalf("enter scope: %v", err)
		}
		sink := &captureSink{}
		sctx = types.WithSink(sctx, sink)

		if v, err := SetSharedState(sctx, stateSnapshot{Step: 1, Label: "draft"}); err != nil || v != 1 {
			t.Fatalf("set: version %d, err %v", v, err)
		}
		if v, err := SetSharedState(sctx, stateSnapshot{Step: 2, Label: "draft"}); err != nil || v != 2 {
			t.Fatalf("set: version %d, err %v", v, err)
		}

		// A resume on another pod rebuilds the scope from session metadata.
		resumed, err := WithSessionState(ctx, store, sess)
		if err != nil {
			t.Fatalf("resume scope: %v", err)
		}
		got, version, ok := SharedState[stateSnapshot](resumed)
		if !ok {
			t.Fatal("SharedState reports ok == false inside a run")
		}
		if version != 2 || got.Step != 2 || got.Label != "draft" {
			t.Fatalf("restored %+v at version %d", got, version)
		}

		changed := sink.stateChanged()
		if len(changed) != 2 {
			t.Fatalf("got %d StateChanged events, want 2", len(changed))
		}
		if changed[0].Version != 1 || changed[1].Version != 2 {
			t.Fatalf("versions %d and %d in emitted events", changed[0].Version, changed[1].Version)
		}
	})
}

func TestSharedStatePatchCarried(t *testing.T) {
	meta := &stubStateMeta{}
	sctx, err := WithSessionState(types.WithSink(context.Background(), &captureSink{}), meta, "sess")
	if err != nil {
		t.Fatalf("enter scope: %v", err)
	}
	patch := []types.PatchOp{{Op: "replace", Path: "/step", Value: 3}}
	sctx = WithStatePatch(sctx, patch)
	if _, err := SetSharedState(sctx, stateSnapshot{Step: 3}); err != nil {
		t.Fatalf("set: %v", err)
	}
	// The sink of the patched context is the one the event lands on; the
	// scope's sink was bound before WithStatePatch, so bind both together.
	sink := &captureSink{}
	sctx, _ = WithSessionState(context.Background(), meta, "sess")
	sctx = WithStatePatch(types.WithSink(sctx, sink), patch)
	if _, err := SetSharedState(sctx, stateSnapshot{Step: 4}); err != nil {
		t.Fatalf("second set: %v", err)
	}
	changed := sink.stateChanged()
	if len(changed) != 1 || len(changed[0].Patch) != 1 || changed[0].Patch[0] != patch[0] {
		t.Fatalf("emitted %+v, want one event with the bound patch", changed)
	}
}

func TestSharedStateOutsideRun(t *testing.T) {
	if _, _, ok := SharedState[stateSnapshot](context.Background()); ok {
		t.Fatal("SharedState reports ok outside a run")
	}
	if _, err := SetSharedState(context.Background(), stateSnapshot{}); !errors.Is(err, errStateOutsideRun) {
		t.Fatalf("SetSharedState outside a run: %v", err)
	}
}

func TestSharedStateVersionConflict(t *testing.T) {
	store, ctx, sess := newStateSession(t)
	stale, err := WithSessionState(ctx, store, sess)
	if err != nil {
		t.Fatalf("enter scope: %v", err)
	}
	fresh, err := WithSessionState(ctx, store, sess)
	if err != nil {
		t.Fatalf("enter scope: %v", err)
	}
	if _, err := SetSharedState(fresh, stateSnapshot{Step: 1}); err != nil {
		t.Fatalf("first set: %v", err)
	}
	if _, err := SetSharedState(stale, stateSnapshot{Step: 2}); !errors.Is(err, types.ErrVersionConflict) {
		t.Fatalf("stale set: %v", err)
	}
	got, version, _ := SharedState[stateSnapshot](fresh)
	if version != 1 || got.Step != 1 {
		t.Fatalf("after conflict: %+v at version %d", got, version)
	}
}

func TestSharedStateForkResetsVersion(t *testing.T) {
	store, ctx, sess := newStateSession(t)
	sctx, err := WithSessionState(ctx, store, sess)
	if err != nil {
		t.Fatalf("enter scope: %v", err)
	}
	for i := 1; i <= 5; i++ {
		if _, err := SetSharedState(sctx, stateSnapshot{Step: i}); err != nil {
			t.Fatalf("set %d: %v", i, err)
		}
	}
	msgs, err := store.Load(ctx, sess)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	forkID, err := store.Fork(ctx, sess, msgs.Messages[len(msgs.Messages)-1].ID)
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	fctx, err := WithSessionState(ctx, store, forkID)
	if err != nil {
		t.Fatalf("fork scope: %v", err)
	}
	got, version, ok := SharedState[stateSnapshot](fctx)
	if !ok || version != 1 || got.Step != 5 {
		t.Fatalf("fork state %+v at version %d, ok %v", got, version, ok)
	}
}
