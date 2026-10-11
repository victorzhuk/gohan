//go:build full

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func wsStores(t *testing.T, name string, src stores.PrincipalSource) (*postgres.SessionLog, *postgres.Runs) {
	t.Helper()
	db := newDB(t, bindSchema(name))
	log := postgres.NewSessionLog(db, postgres.WithSessionPrincipals(src))
	return log, postgres.NewRuns(db)
}

func wsOwner(gen uint64) types.WorkspaceOwner {
	return types.WorkspaceOwner{RunID: "run-ws", LeaseGeneration: gen}
}

func wsStartRun(t *testing.T, runs *postgres.Runs, sessionID string) stores.Lease {
	t.Helper()
	lease, err := runs.Start(context.Background(), stores.Run{SessionID: sessionID, RunID: "run-ws"}, time.Minute)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	return lease
}

func wsAppend(t *testing.T, log *postgres.SessionLog, sessionID string) {
	t.Helper()
	if _, err := log.Append(context.Background(), sessionID, 0, types.Message{Role: types.RoleUser}); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func wsMeta(t *testing.T, log *postgres.SessionLog, sessionID string) (stores.WorkspaceMeta, bool) {
	t.Helper()
	m, ok, err := log.WorkspaceMeta(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("workspace meta: %v", err)
	}
	return m, ok
}

func TestWorkspaceRestoreRules(t *testing.T) {
	ctx := context.Background()

	t.Run("sandbox.restore-skips-equal-metadata", func(t *testing.T) {
		log, runs := wsStores(t, "wsrestore", fixedPrincipal(bindPrincipal))
		wsAppend(t, log, "s1")
		lease := wsStartRun(t, runs, "s1")
		owner := types.WorkspaceOwner{RunID: lease.RunID, LeaseGeneration: lease.Generation}

		if _, err := log.SetWorkspaceMeta(ctx, "s1", 0, stores.WorkspaceMeta{Ref: "snap-1", Owner: owner}); err != nil {
			t.Fatalf("seed meta: %v", err)
		}
		stored, ok := wsMeta(t, log, "s1")
		if !ok || stored.Ref != "snap-1" || stored.Generation != 1 {
			t.Fatalf("stored meta = %+v ok = %v, want snap-1 at generation 1", stored, ok)
		}

		// The restore caller compares the checkpoint against the stored
		// record and issues no write on equality: the record keeps its
		// generation.
		equal, ok := wsMeta(t, log, "s1")
		if !ok || equal != stored {
			t.Fatalf("reread meta = %+v ok = %v, want the equal record unchanged", equal, ok)
		}
		if _, ok := wsMeta(t, log, "s1"); !ok || equal.Generation != 1 {
			t.Fatalf("equal restore rewrote metadata: %+v", equal)
		}

		// A stored generation presented with a different ref is a token
		// mismatch and is refused loudly, leaving the record unchanged.
		_, err := log.SetWorkspaceMeta(ctx, "s1", 1, stores.WorkspaceMeta{Ref: "snap-2", Generation: 1, Owner: owner})
		if !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("different ref at stored generation err = %v, want ErrVersionConflict", err)
		}
		if got, ok := wsMeta(t, log, "s1"); !ok || got != stored {
			t.Fatalf("refused write changed the record: %+v", got)
		}
	})

	t.Run("sandbox.no-ref-session-migrates", func(t *testing.T) {
		log, runs := wsStores(t, "wsfresh", fixedPrincipal(bindPrincipal))
		wsAppend(t, log, "s1")
		_ = wsStartRun(t, runs, "s1")

		// No durable metadata and no checkpoint ref: the generation-zero
		// fresh open applies and writes nothing.
		m, ok := wsMeta(t, log, "s1")
		if ok || m != (stores.WorkspaceMeta{}) {
			t.Fatalf("absent session reported meta = %+v ok = %v", m, ok)
		}
	})

	t.Run("sandbox.legacy-ref-explicit-import", func(t *testing.T) {
		log, runs := wsStores(t, "wslegacy", fixedPrincipal(bindPrincipal))
		wsAppend(t, log, "s1")
		lease := wsStartRun(t, runs, "s1")
		owner := types.WorkspaceOwner{RunID: lease.RunID, LeaseGeneration: lease.Generation}

		// A generation-zero checkpoint ref never appears as stored
		// metadata: the store reports absent, so the restore refuses
		// until an authorized caller imports it explicitly.
		if _, ok := wsMeta(t, log, "s1"); ok {
			t.Fatal("legacy ref surfaced as stored metadata without an import")
		}
		snap, err := log.ImportWorkspaceMeta(ctx, "s1", owner, types.WorkspaceSnapshot{Ref: "legacy-1"})
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		if snap.Ref != "legacy-1" || snap.Generation != 1 {
			t.Fatalf("imported snapshot = %+v, want legacy-1 at generation 1", snap)
		}
		m, ok := wsMeta(t, log, "s1")
		if !ok || m.Ref != "legacy-1" || m.Generation != 1 || m.Owner != owner {
			t.Fatalf("stored meta = %+v ok = %v, want the imported record", m, ok)
		}
	})
}

func TestImportWorkspaceMeta(t *testing.T) {
	ctx := context.Background()

	t.Run("sandbox.import-writes-generation-one", func(t *testing.T) {
		log, runs := wsStores(t, "wsimport", fixedPrincipal(bindPrincipal))
		wsAppend(t, log, "s1")
		lease := wsStartRun(t, runs, "s1")
		owner := types.WorkspaceOwner{RunID: lease.RunID, LeaseGeneration: lease.Generation}

		snap, err := log.ImportWorkspaceMeta(ctx, "s1", owner, types.WorkspaceSnapshot{Ref: "legacy"})
		if err != nil || snap.Ref != "legacy" || snap.Generation != 1 {
			t.Fatalf("import = %+v err = %v, want legacy at generation 1", snap, err)
		}
		m, ok, err := log.WorkspaceMeta(ctx, "s1")
		if err != nil || !ok || m.Ref != "legacy" || m.Generation != 1 || m.Owner != owner {
			t.Fatalf("stored meta = %+v ok = %v err = %v, want the imported record", m, ok, err)
		}
	})

	t.Run("sandbox.import-refuses", func(t *testing.T) {
		p := bindPrincipal
		src := func(context.Context) (types.Principal, bool) { return p, true }
		log, runs := wsStores(t, "wsimportref", src)
		wsAppend(t, log, "s1")
		lease := wsStartRun(t, runs, "s1")
		owner := types.WorkspaceOwner{RunID: lease.RunID, LeaseGeneration: lease.Generation}

		stranger := bindPrincipal
		stranger.Subject = "other-subject"
		stranger.Scopes = nil
		refusals := []struct {
			name   string
			log    *postgres.SessionLog
			owner  types.WorkspaceOwner
			legacy types.WorkspaceSnapshot
		}{
			{"unauthorized caller", func() *postgres.SessionLog {
				pdb := newDB(t, bindSchema("wsimportref-other"))
				plog := postgres.NewSessionLog(pdb, postgres.WithSessionPrincipals(fixedPrincipal(stranger)))
				wsAppend(t, plog, "s1")
				if _, err := postgres.NewRuns(pdb).Start(context.Background(), stores.Run{SessionID: "s1", RunID: "run-other"}, time.Minute); err != nil {
					t.Fatalf("unauthorized caller: start run: %v", err)
				}
				return plog
			}(), owner, types.WorkspaceSnapshot{Ref: "legacy"}},
			{"stale owner", log, wsOwner(999), types.WorkspaceSnapshot{Ref: "legacy"}},
			{"nonzero input generation", log, owner, types.WorkspaceSnapshot{Ref: "legacy", Generation: 2}},
			{"empty ref", log, owner, types.WorkspaceSnapshot{}},
		}
		for _, tt := range refusals {
			if _, err := tt.log.ImportWorkspaceMeta(context.Background(), "s1", tt.owner, tt.legacy); err == nil {
				t.Fatalf("%s: import succeeded, want error", tt.name)
			}
		}

		if _, err := log.ImportWorkspaceMeta(ctx, "s1", owner, types.WorkspaceSnapshot{Ref: "legacy"}); err != nil {
			t.Fatalf("seed import: %v", err)
		}
		if _, err := log.ImportWorkspaceMeta(ctx, "s1", owner, types.WorkspaceSnapshot{Ref: "other"}); !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("existing metadata err = %v, want ErrVersionConflict", err)
		}
		m, ok, err := log.WorkspaceMeta(ctx, "s1")
		if err != nil || !ok || m.Ref != "legacy" || m.Generation != 1 {
			t.Fatalf("refusals changed the store: meta = %+v ok = %v err = %v", m, ok, err)
		}
	})
}
