//go:build full

package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func writeNotices(t *testing.T, ctx context.Context, s interface {
	Start(ctx context.Context, r stores.Run, ttl time.Duration) (stores.Lease, error)
	Finish(ctx context.Context, l stores.Lease, state stores.RunState, uncertain []types.CallKey, resultRef string) error
}, n int) {
	t.Helper()
	for i := range n {
		l, err := s.Start(ctx, stores.Run{RunID: "r-notice-" + string(rune('a'+i)), SessionID: "s-notice-" + string(rune('a'+i))}, stores.LeaseTTL)
		if err != nil {
			t.Fatalf("start run %d: %v", i, err)
		}
		if err := s.Finish(ctx, l, stores.Failed, nil, ""); err != nil {
			t.Fatalf("finish run %d: %v", i, err)
		}
	}
}

func noticeIDs(notices []types.RunNotice) []string {
	out := make([]string, 0, len(notices))
	for _, n := range notices {
		out = append(out, n.ID)
	}
	return out
}

func TestNoticesRemediation(t *testing.T) {
	t.Run("stores.pg-notice-claim-exclusive", func(t *testing.T) {
		ctx := context.Background()
		clk := &advClock{now: time.Now()}
		s, _ := newRemediationRuns(t, clk)
		const notices = 12
		writeNotices(t, ctx, s, notices)

		const dispatchers = 3
		seen := make([][]string, dispatchers)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range dispatchers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				got, err := s.Notices(ctx, notices)
				if err != nil {
					t.Errorf("dispatcher %d: %v", i, err)
					return
				}
				for _, id := range noticeIDs(got) {
					for j, other := range seen {
						if j == i {
							continue
						}
						for _, dup := range other {
							if id == dup {
								t.Errorf("notice %s claimed by dispatchers %d and %d", id, i, j)
							}
						}
					}
				}
				seen[i] = noticeIDs(got)
			}(i)
		}
		close(start)
		wg.Wait()
		total := 0
		for _, ids := range seen {
			total += len(ids)
		}
		if total != notices {
			t.Fatalf("dispatchers claimed %d notices, want all %d with no duplicates", total, notices)
		}

		// After the claim lapses every notice returns to pending with the
		// same id.
		clk.Advance(2 * stores.LeaseTTL)
		got, err := s.Notices(ctx, notices)
		if err != nil {
			t.Fatalf("Notices after claim expiry: %v", err)
		}
		if len(got) != notices {
			t.Fatalf("Notices after expiry = %d notices, want %d", len(got), notices)
		}
		live := map[string]bool{}
		for _, ids := range seen {
			for _, id := range ids {
				live[id] = true
			}
		}
		for _, id := range noticeIDs(got) {
			if !live[id] {
				t.Fatalf("notice %s after expiry, want one of the originally claimed ids", id)
			}
			delete(live, id)
		}
	})
}
