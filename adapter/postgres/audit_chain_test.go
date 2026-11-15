package postgres_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/stores"
)

func TestAuditChain(t *testing.T) {
	t.Run("concurrent appends keep one chain", func(t *testing.T) {
		db := newDB(t, "auditchain")
		audit := postgres.NewAuditLog(db)
		for round := range 3 {
			ctx := context.Background()
			sessionID := fmt.Sprintf("ac-s%d", round)
			const n = 20
			kinds := []stores.AuditKind{
				stores.AuditRunStarted, stores.AuditToolDecision, stores.AuditToolOutcome,
				stores.AuditRunFinished, stores.AuditErasure,
			}
			errs := make([]error, n)
			var wg sync.WaitGroup
			for i := range n {
				wg.Add(1)
				go func() {
					defer wg.Done()
					rec := stores.AuditRecord{
						Kind:      kinds[i%len(kinds)],
						SessionID: sessionID,
						Tenant:    fmt.Sprintf("t%d", i%3),
						Subject:   fmt.Sprintf("u%d", i%4),
						Reason:    fmt.Sprintf("r%d", i),
					}
					errs[i] = audit.Append(ctx, rec)
				}()
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatalf("round %d: append %d: %v", round, i, err)
				}
			}
			pool, err := pgxpoolFor(t, "auditchain")
			if err != nil {
				t.Fatalf("round %d: pool: %v", round, err)
			}
			rows, err := pool.Query(ctx,
				fmt.Sprintf(`SELECT seq, prev_hash, hash FROM %s.audit_record WHERE session_id = $1 ORDER BY seq`, "auditchain"),
				sessionID)
			if err != nil {
				t.Fatalf("round %d: query: %v", round, err)
			}
			type chainRow struct {
				seq      int64
				prevHash string
				hash     string
			}
			var chain []chainRow
			for rows.Next() {
				var row chainRow
				if err := rows.Scan(&row.seq, &row.prevHash, &row.hash); err != nil {
					rows.Close()
					t.Fatalf("round %d: scan: %v", round, err)
				}
				chain = append(chain, row)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				t.Fatalf("round %d: rows: %v", round, err)
			}
			rows.Close()
			if len(chain) != n {
				t.Fatalf("round %d: %d rows, want %d", round, len(chain), n)
			}
			seen := map[int64]bool{}
			for i, row := range chain {
				want := int64(i + 1)
				if seen[row.seq] {
					t.Fatalf("round %d: duplicate seq %d", round, row.seq)
				}
				seen[row.seq] = true
				if row.seq != want {
					t.Fatalf("round %d: seq %d, want %d", round, row.seq, want)
				}
				if i == 0 {
					if row.prevHash != "" {
						t.Fatalf("round %d: first record prev_hash %q, want empty", round, row.prevHash)
					}
					continue
				}
				prev := chain[i-1]
				if row.prevHash != prev.hash {
					t.Fatalf("round %d: seq %d prev_hash %q != previous hash %q for seq %d", round, row.seq, row.prevHash, prev.hash, prev.seq)
				}
			}
		}
	})
}
