package stores

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// ownerGuardFake validates owner proofs against a set of live owners, so
// the fencing and refusal behavior is exercised without the runs store.
type ownerGuardFake struct {
	mu     sync.Mutex
	active map[types.WorkspaceOwner]bool
	writes int
}

func newOwnerGuardFake(live ...types.WorkspaceOwner) *ownerGuardFake {
	g := &ownerGuardFake{active: make(map[types.WorkspaceOwner]bool)}
	for _, o := range live {
		g.active[o] = true
	}
	return g
}

func (g *ownerGuardFake) WithWorkspaceOwner(_ context.Context, _ string, owner types.WorkspaceOwner, write func() error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.active[owner] {
		return fmt.Errorf("owner %s gen %d: %w", owner.RunID, owner.LeaseGeneration, types.ErrRunNotActive)
	}
	g.writes++
	return write()
}

// cleanupMetaFake carries the SessionWorkspaceCleanup surface over the same
// in-memory bookkeeping the memory store keeps, with an injectable live
// lease set, so claim exclusion is observable without the runs store.
type cleanupLeases struct{ active map[string]bool }

func (f *cleanupLeases) SessionLeaseActive(_ context.Context, sessionID string) bool {
	return f.active[sessionID]
}

func ownerA(gen uint64) types.WorkspaceOwner {
	return types.WorkspaceOwner{RunID: "run-1", LeaseGeneration: gen}
}

func TestSessionWorkspaceMeta(t *testing.T) {
	var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	store, _, _ := sessionTestStore(&now)
	ctx := withTestPrincipal(context.Background(), ownerTenantA)

	if _, err := store.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser}); err != nil {
		t.Fatalf("append: %v", err)
	}

	if _, ok, err := store.WorkspaceMeta(ctx, "s1"); err != nil || ok {
		t.Fatalf("absent meta ok = %v, err = %v, want false, nil", ok, err)
	}

	guard := newOwnerGuardFake(ownerA(1))
	store.ownerGuard = guard

	gen, err := store.SetWorkspaceMeta(ctx, "s1", 0, WorkspaceMeta{Ref: "ref-1", Owner: ownerA(1)})
	if err != nil || gen != 1 {
		t.Fatalf("first write gen = %d, err = %v, want 1, nil", gen, err)
	}
	m, ok, err := store.WorkspaceMeta(ctx, "s1")
	if err != nil || !ok || m.Ref != "ref-1" || m.Generation != 1 {
		t.Fatalf("meta = %+v ok = %v err = %v", m, ok, err)
	}

	// A stale token is refused without a write.
	if _, err := store.SetWorkspaceMeta(ctx, "s1", 0, WorkspaceMeta{Ref: "ref-2", Owner: ownerA(1)}); !errors.Is(err, types.ErrVersionConflict) {
		t.Fatalf("stale token err = %v, want ErrVersionConflict", err)
	}

	// The refusal precedence is observable: a stale token beats a stale owner.
	if _, err := store.SetWorkspaceMeta(ctx, "s1", 0, WorkspaceMeta{Ref: "ref-2", Owner: ownerA(7)}); !errors.Is(err, types.ErrVersionConflict) {
		t.Fatalf("stale token with stale owner err = %v, want ErrVersionConflict", err)
	}

	gen, err = store.SetWorkspaceMeta(ctx, "s1", 1, WorkspaceMeta{Ref: "ref-2", Owner: ownerA(1)})
	if err != nil || gen != 2 {
		t.Fatalf("second write gen = %d, err = %v, want 2, nil", gen, err)
	}
}

func TestWorkspaceOwnerExclusion(t *testing.T) {
	t.Run("sandbox.cleanup-claim-excludes-live-use", func(t *testing.T) {
		var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		rec := WorkspaceCleanupRecord{SessionID: "s1", Owner: ownerA(1), ResourceRef: "res-1"}
		store.cleanups["s1"] = []WorkspaceCleanupRecord{rec}

		// Two concurrent claims for the same record leave exactly one winner.
		const racers = 8
		wins := make(chan bool, racers)
		var wg sync.WaitGroup
		for range racers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				claimed, err := store.ClaimWorkspaceCleanup(ctx, rec)
				if err != nil {
					wins <- false
					return
				}
				wins <- claimed
			}()
		}
		wg.Wait()
		close(wins)
		winners := 0
		for claimed := range wins {
			if claimed {
				winners++
			}
		}
		if winners != 1 {
			t.Fatalf("claim winners = %d, want 1", winners)
		}

		// A live session lease refuses the claim and changes nothing.
		if err := store.AbortWorkspaceCleanup(ctx, rec); err != nil {
			t.Fatalf("abort claim: %v", err)
		}
		store.leases = &cleanupLeases{active: map[string]bool{"s1": true}}
		if claimed, err := store.ClaimWorkspaceCleanup(ctx, rec); err != nil || claimed {
			t.Fatalf("live-lease claim = %v, err = %v, want false, nil", claimed, err)
		}
		if len(store.cleanupLocks["s1"]) != 0 {
			t.Fatalf("live-lease refusal left claims: %v", store.cleanupLocks)
		}

		// A claimed session refuses a concurrent acquisition.
		store.leases = &cleanupLeases{active: map[string]bool{}}
		if claimed, err := store.ClaimWorkspaceCleanup(ctx, rec); err != nil || !claimed {
			t.Fatalf("inactive claim = %v, err = %v, want true, nil", claimed, err)
		}
		guard := newOwnerGuardFake(ownerA(2))
		store.ownerGuard = guard
		if err := store.ValidateWorkspaceAcquire(ctx, "s1", ownerA(2)); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("claimed-session acquire err = %v, want ErrRunNotActive", err)
		}
	})
}

func TestWorkspaceOwnerCASAtomic(t *testing.T) {
	t.Run("sandbox.owner-cas-atomic", func(t *testing.T) {
		var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		guard := newOwnerGuardFake(ownerA(3))
		store.ownerGuard = guard

		gen, err := store.SetWorkspaceMeta(ctx, "s1", 0, WorkspaceMeta{Ref: "ref-1", Owner: ownerA(3)})
		if err != nil || gen != 1 {
			t.Fatalf("seed write gen = %d, err = %v, want 1, nil", gen, err)
		}

		// A stale lease generation is refused with ErrRunNotActive and no write.
		if _, err := store.SetWorkspaceMeta(ctx, "s1", 1, WorkspaceMeta{Ref: "ref-2", Owner: ownerA(2)}); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("stale owner err = %v, want ErrRunNotActive", err)
		}
		if guard.writes != 1 {
			t.Fatalf("guard writes = %d, want 1: stale owner reached the write", guard.writes)
		}
		if m, _, _ := store.WorkspaceMeta(ctx, "s1"); m.Ref != "ref-1" {
			t.Fatalf("stale owner mutated the store: ref = %s", m.Ref)
		}

		// A matching generation under a different token than the stored one
		// is refused with ErrVersionConflict and no write.
		store.ownerGuard = newOwnerGuardFake(ownerA(3))
		if _, err := store.SetWorkspaceMeta(ctx, "s1", 0, WorkspaceMeta{Ref: "ref-1", Generation: 0, Owner: ownerA(3)}); !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("generation mismatch err = %v, want ErrVersionConflict", err)
		}
		if m, _, _ := store.WorkspaceMeta(ctx, "s1"); m.Generation != 1 {
			t.Fatalf("failed cas mutated the store: generation = %d", m.Generation)
		}
	})
}

func TestWorkspaceGenerationFenced(t *testing.T) {
	t.Run("sandbox.ref-generation-fenced", func(t *testing.T) {
		var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		store.ownerGuard = newOwnerGuardFake(ownerA(1), ownerA(2))

		var wg sync.WaitGroup
		results := make(chan error, 2)
		publish := func(ref types.WorkspaceRef, owner types.WorkspaceOwner) {
			defer wg.Done()
			_, err := store.SetWorkspaceMeta(ctx, "s1", 0, WorkspaceMeta{Ref: ref, Owner: owner})
			results <- err
		}
		wg.Add(2)
		go publish("ref-1", ownerA(1))
		go publish("ref-2", ownerA(2))
		wg.Wait()
		close(results)

		failures := 0
		for err := range results {
			if err != nil {
				if !errors.Is(err, types.ErrVersionConflict) {
					t.Fatalf("loser err = %v, want ErrVersionConflict", err)
				}
				failures++
			}
		}
		if failures != 1 {
			t.Fatalf("racing publications failures = %d, want 1", failures)
		}
		m, ok, err := store.WorkspaceMeta(ctx, "s1")
		if err != nil || !ok || m.Generation != 1 {
			t.Fatalf("winner meta = %+v ok = %v err = %v", m, ok, err)
		}
	})
}

func TestRestoreKeepsNewerGeneration(t *testing.T) {
	t.Run("sandbox.restore-keeps-newer-generation", func(t *testing.T) {
		var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		store.ownerGuard = newOwnerGuardFake(ownerA(1))

		if _, err := store.SetWorkspaceMeta(ctx, "s1", 0, WorkspaceMeta{Ref: "newer", Owner: ownerA(1)}); err != nil {
			t.Fatalf("seed write: %v", err)
		}
		if _, err := store.SetWorkspaceMeta(ctx, "s1", 1, WorkspaceMeta{Ref: "newer-2", Owner: ownerA(1)}); err != nil {
			t.Fatalf("second write: %v", err)
		}

		// An older checkpoint generation (1) is not applied over the stored
		// generation 2: the compare-and-set refuses it.
		if _, err := store.SetWorkspaceMeta(ctx, "s1", 1, WorkspaceMeta{Ref: "older", Owner: ownerA(1)}); !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("older checkpoint err = %v, want ErrVersionConflict", err)
		}
		m, ok, err := store.WorkspaceMeta(ctx, "s1")
		if err != nil || !ok || m.Ref != "newer-2" || m.Generation != 2 {
			t.Fatalf("meta = %+v ok = %v err = %v, want newer-2 at generation 2", m, ok, err)
		}
	})
}

func TestWorkspaceOwnerGuardRequired(t *testing.T) {
	t.Run("sandbox.owner-guard-required", func(t *testing.T) {
		var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		if store.ownerGuard != nil {
			t.Fatalf("store without the option wired a guard: %T", store.ownerGuard)
		}

		if _, err := store.SetWorkspaceMeta(ctx, "s1", 0, WorkspaceMeta{Ref: "ref-1", Owner: ownerA(1)}); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("SetWorkspaceMeta without guard err = %v, want ErrRunNotActive", err)
		}
		if _, err := store.ImportWorkspaceMeta(ctx, "s1", ownerA(1), types.WorkspaceSnapshot{Ref: "ref-1"}); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("ImportWorkspaceMeta without guard err = %v, want ErrRunNotActive", err)
		}
		if err := store.RecordWorkspaceCleanup(ctx, "s1", ownerA(1), "res-1"); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("RecordWorkspaceCleanup without guard err = %v, want ErrRunNotActive", err)
		}
		if _, ok, err := store.WorkspaceMeta(ctx, "s1"); err != nil || ok {
			t.Fatalf("refused writes stored metadata: ok = %v err = %v", ok, err)
		}
		if len(store.cleanups["s1"]) != 0 {
			t.Fatalf("refused write stored cleanup records: %v", store.cleanups)
		}
	})
}

func TestImportWorkspaceMeta(t *testing.T) {
	t.Run("sandbox.import-writes-generation-one", func(t *testing.T) {
		var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		store.ownerGuard = newOwnerGuardFake(ownerA(4))

		got, err := store.ImportWorkspaceMeta(ctx, "s1", ownerA(4), types.WorkspaceSnapshot{Ref: "legacy"})
		if err != nil || got.Ref != "legacy" || got.Generation != 1 {
			t.Fatalf("import = %+v err = %v, want legacy at generation 1", got, err)
		}
		m, ok, err := store.WorkspaceMeta(ctx, "s1")
		if err != nil || !ok || m.Ref != "legacy" || m.Generation != 1 || m.Owner != ownerA(4) {
			t.Fatalf("stored meta = %+v ok = %v err = %v", m, ok, err)
		}
	})

	t.Run("sandbox.import-refuses", func(t *testing.T) {
		var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		store.ownerGuard = newOwnerGuardFake(ownerA(4))

		refusals := []struct {
			name   string
			owner  types.WorkspaceOwner
			legacy types.WorkspaceSnapshot
		}{
			{"unauthorized caller", ownerA(9), types.WorkspaceSnapshot{Ref: "legacy"}},
			{"nonzero input generation", ownerA(4), types.WorkspaceSnapshot{Ref: "legacy", Generation: 2}},
			{"empty ref", ownerA(4), types.WorkspaceSnapshot{}},
		}
		for _, tt := range refusals {
			if _, err := store.ImportWorkspaceMeta(ctx, "s1", tt.owner, tt.legacy); err == nil {
				t.Fatalf("%s: import succeeded, want error", tt.name)
			}
		}

		if _, err := store.ImportWorkspaceMeta(ctx, "s1", ownerA(4), types.WorkspaceSnapshot{Ref: "legacy"}); err != nil {
			t.Fatalf("seed import: %v", err)
		}
		// Existing metadata refuses a second import.
		if _, err := store.ImportWorkspaceMeta(ctx, "s1", ownerA(4), types.WorkspaceSnapshot{Ref: "other"}); !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("existing metadata err = %v, want ErrVersionConflict", err)
		}
		m, ok, err := store.WorkspaceMeta(ctx, "s1")
		if err != nil || !ok || m.Ref != "legacy" || m.Generation != 1 {
			t.Fatalf("refusals changed the store: meta = %+v ok = %v err = %v", m, ok, err)
		}
	})
}
