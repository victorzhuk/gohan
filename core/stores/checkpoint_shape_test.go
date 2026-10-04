package stores

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestCheckpointStoredShape(t *testing.T) {
	t.Parallel()

	t.Run("stores.no-secrets-stored", func(t *testing.T) {
		t.Parallel()
		s := NewMemoryCheckpoints(WithMemoryCheckpointRunInfo(func(context.Context) (types.RunInfo, bool) {
			return types.RunInfo{RunID: "run-1"}, true
		}))
		storedCredential := strings.Join([]string{"secret-credential", "token-4f8a2c"}, "-")

		p := types.Principal{Subject: "user-1", Tenant: "acme", Scopes: []string{"session:write"}}
		ctx := t.Context()
		id, err := s.Put(ctx, Checkpoint{
			SessionID: "s-1",
			Flow:      "booking",
			Reason:    types.HumanApproval,
			Originator: types.Principal{
				Subject: "user-1",
				Tenant:  "acme",
				Scopes:  []string{"session:write"},
			},
			Data:      []byte("state"),
			ExpiresAt: time.Now().Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		if id == "" {
			t.Fatal("Put returned an empty token")
		}

		// Scan everything the store holds — tokens, runs and checkpoint
		// records, including pointer targets — for the credential.
		s.mu.Lock()
		var held strings.Builder
		fmt.Fprintf(&held, "%+v", s.toks)
		for _, rec := range s.toks {
			fmt.Fprintf(&held, " %+v %+v", rec, rec.cp)
		}
		fmt.Fprintf(&held, " %+v", s.byRun)
		s.mu.Unlock()
		if strings.Contains(held.String(), storedCredential) {
			t.Fatal("store persisted the caller's credential token")
		}

		rec := s.toks[id]
		got := rec.cp.Originator
		if got.Subject != "user-1" || got.Tenant != "acme" || len(got.Scopes) != 1 || got.Scopes[0] != "session:write" {
			t.Fatalf("Originator lost identity fields: %+v", got)
		}
		if len(got.Scopes) > 0 && &got.Scopes[0] == &p.Scopes[0] {
			t.Fatal("Originator.Scopes aliases the caller's slice")
		}
	})
}
