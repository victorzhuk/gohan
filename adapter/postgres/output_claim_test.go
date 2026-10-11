//go:build full

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func fixedPrincipal(p types.Principal) stores.PrincipalSource {
	return func(context.Context) (types.Principal, bool) { return p, true }
}

func TestOutputClaimsLedger(t *testing.T) {
	ctx := context.Background()
	content := []types.Block{types.Text{Text: "shared-bytes"}}

	t.Run("stores.pg-output-shared-ref-survives-erase", func(t *testing.T) {
		db := newDB(t, bindSchema("outshared"))
		s := postgres.NewOutputs(db)
		riA := types.RunInfo{SessionID: "oc-sa", Principal: types.Principal{Tenant: "t1", Subject: "u1"}}
		riB := types.RunInfo{SessionID: "oc-sb", Principal: types.Principal{Tenant: "t1", Subject: "u2"}}
		refA, err := s.Put(ctx, riA, content)
		if err != nil {
			t.Fatalf("Put A: %v", err)
		}
		refB, err := s.Put(ctx, riB, content)
		if err != nil {
			t.Fatalf("Put B: %v", err)
		}
		if refA != refB {
			t.Fatalf("equal bytes yielded refs %s and %s, want one ref", refA, refB)
		}
		removed, err := s.EraseSubject(ctx, "t1", "u1")
		if err != nil {
			t.Fatalf("EraseSubject u1: %v", err)
		}
		if removed != 0 {
			t.Fatalf("EraseSubject u1 removed %d refs, want 0: u2 still claims the ref", removed)
		}
		got, err := s.Get(ctx, refA)
		if err != nil {
			t.Fatalf("Get after u1 erase: %v", err)
		}
		if len(got) == 0 {
			t.Fatal("Get returned empty bytes after u1 erase")
		}
		if n := count(t, db, `SELECT count(*) FROM output_claim WHERE ref = $1`, refA); n != 1 {
			t.Fatalf("claims on ref = %d, want 1 (u2's session claim)", n)
		}
	})

	t.Run("stores.pg-output-last-claim-deletes-ref", func(t *testing.T) {
		db := newDB(t, bindSchema("outlast"))
		principal := types.Principal{Tenant: "t1", Subject: "u1"}
		outputs := postgres.NewOutputs(db)
		sessions := postgres.NewSessionLog(db,
			postgres.WithSessionPrincipals(fixedPrincipal(principal)),
			postgres.WithSessionDependents(outputs))
		if _, err := sessions.Append(ctx, "oc-del", 0,
			types.Message{Blocks: []types.Block{types.Text{Text: "hi"}}}); err != nil {
			t.Fatalf("Append session: %v", err)
		}
		ri := types.RunInfo{SessionID: "oc-del", Principal: principal}
		ref, err := outputs.Put(ctx, ri, content)
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := sessions.Delete(ctx, "oc-del"); err != nil {
			t.Fatalf("Delete session: %v", err)
		}
		if _, err := outputs.Get(ctx, ref); !errors.Is(err, stores.ErrOutputNotFound) {
			t.Fatalf("Get after last claim dropped: err=%v, want ErrOutputNotFound", err)
		}
		if n := count(t, db, `SELECT count(*) FROM output_claim WHERE ref = $1`, ref); n != 0 {
			t.Fatalf("claims left on ref = %d, want 0", n)
		}
	})

	t.Run("stores.pg-output-legacy-claims-preserved", func(t *testing.T) {
		schema := bindSchema("outlegacy")
		pool, err := pgxpoolFor(t, schema)
		if err != nil {
			t.Fatalf("pool: %v", err)
		}
		if _, err := postgres.Migrate(ctx, pool, postgres.MigrateOptions{Schema: schema, TargetVersion: 4}); err != nil {
			t.Fatalf("migrate to 4: %v", err)
		}
		seed := []string{
			`INSERT INTO ` + schema + `.session_record (session_id, owner, kind, archived, version, history, payload, updated_at)
			 VALUES ('lg-s1', '{"Tenant":"t1","Subject":"u1"}', 0, false, 1, '[]', 'p', now())`,
			`INSERT INTO ` + schema + `.session_record (session_id, owner, kind, archived, version, history, payload, updated_at)
			 VALUES ('lg-s2', '{"Tenant":"t1","Subject":"u2"}', 0, false, 1, '[]', 'p', now())`,
			`INSERT INTO ` + schema + `.output_record (ref, tenant, subject, content, payload)
			 VALUES ('lg-ref-shared', 't1', 'u1', 'c', 'shared-payload')`,
			`INSERT INTO ` + schema + `.output_record (ref, tenant, subject, content, payload)
			 VALUES ('lg-ref-orphan', 't1', 'u9', 'c', 'orphan-payload')`,
			`INSERT INTO ` + schema + `.run_record (run_id, session_id, tenant, state, result_ref, payload, started_at, heartbeat_at)
			 VALUES ('lg-r1', 'lg-s1', 't1', 0, 'lg-ref-shared', 'p', now(), now())`,
			`INSERT INTO ` + schema + `.run_record (run_id, session_id, tenant, state, result_ref, payload, started_at, heartbeat_at)
			 VALUES ('lg-r2', 'lg-s2', 't1', 0, 'lg-ref-shared', 'p', now(), now())`,
		}
		for _, stmt := range seed {
			if _, err := pool.Exec(ctx, stmt); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
		if applied, err := postgres.Migrate(ctx, pool, postgres.MigrateOptions{Schema: schema}); err != nil {
			t.Fatalf("migrate to head: %v", err)
		} else if len(applied) == 0 {
			t.Fatal("upgrade applied no migration, want 00005")
		}
		db, err := postgres.New(ctx, dsn(t), postgres.WithSchema(schema))
		if err != nil {
			t.Fatalf("open upgraded schema: %v", err)
		}
		t.Cleanup(db.Close)
		s := postgres.NewOutputs(db)

		if n := count(t, db, `SELECT count(*) FROM output_claim WHERE ref = 'lg-ref-shared'`); n != 2 {
			t.Fatalf("session claims on reconstructed ref = %d, want 2", n)
		}
		if n := count(t, db, `SELECT count(*) FROM output_legacy_claim WHERE ref = 'lg-ref-orphan'`); n != 1 {
			t.Fatalf("legacy owner claims on unattributed ref = %d, want 1", n)
		}
		removed, err := s.EraseSubject(ctx, "t1", "u1")
		if err != nil {
			t.Fatalf("EraseSubject u1: %v", err)
		}
		if removed != 0 {
			t.Fatalf("EraseSubject u1 removed %d refs, want 0: u2 still claims the shared ref", removed)
		}
		if _, err := s.Get(ctx, "lg-ref-shared"); err != nil {
			t.Fatalf("Get shared ref after u1 erase: %v", err)
		}
		removed, err = s.EraseSubject(ctx, "t1", "u9")
		if err != nil {
			t.Fatalf("EraseSubject u9: %v", err)
		}
		if removed != 1 {
			t.Fatalf("EraseSubject u9 removed %d refs, want 1 (the orphan)", removed)
		}
		if _, err := s.Get(ctx, "lg-ref-orphan"); !errors.Is(err, stores.ErrOutputNotFound) {
			t.Fatalf("Get orphan after its only owner erased: err=%v, want ErrOutputNotFound", err)
		}
	})

	t.Run("stores.pg-output-put-erase-race", func(t *testing.T) {
		db := newDB(t, bindSchema("outrace"))
		s := postgres.NewOutputs(db)
		riA := types.RunInfo{SessionID: "oc-ra", Principal: types.Principal{Tenant: "t1", Subject: "u1"}}
		riB := types.RunInfo{SessionID: "oc-rb", Principal: types.Principal{Tenant: "t1", Subject: "u2"}}
		ref, err := s.Put(ctx, riA, content)
		if err != nil {
			t.Fatalf("Put A: %v", err)
		}
		for i := range 25 {
			var wg sync.WaitGroup
			errs := make([]error, 2)
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, errs[0] = s.Put(ctx, riB, content)
			}()
			go func() {
				defer wg.Done()
				_, errs[1] = s.EraseSubject(ctx, "t1", "u1")
			}()
			wg.Wait()
			for _, err := range errs {
				if err != nil {
					t.Fatalf("round %d: %v", i, err)
				}
			}
		}
		if _, err := s.Get(ctx, ref); err != nil {
			t.Fatalf("Get after put/erase race: %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM output_claim WHERE ref = $1`, ref); n != 1 {
			t.Fatalf("claims on ref after race = %d, want 1", n)
		}
	})
}
