package permission

import (
	"context"
	"errors"
	"testing"
	"time"

	corepermission "github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestSessionGrant(t *testing.T) {
	ctx := context.Background()
	now := func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }

	request := func(tool string, fp stores.Fingerprint) corepermission.ApprovalRequest {
		return corepermission.ApprovalRequest{
			Tool:        types.ToolSpec{Name: tool, Risk: types.RiskMedium},
			Fingerprint: fp,
			Risk:        types.RiskMedium,
			ExpiresAt:   now(),
			Eligible:    corepermission.Eligibility{Scopes: []string{"approve:" + tool}},
		}
	}

	t.Run("permission.grant-inherits-policy", func(t *testing.T) {
		store := NewMemoryGrantStore(now)
		suspended := request("deploy_prod", "deploy_prod:{\"env\":\"prod\"}")
		suspended.Eligible.Scopes = []string{"approve:deploy_prod"}
		_, _, err := ApproveGrant(ctx, store, "s1", suspended, types.Principal{Subject: "op", Scopes: []string{"session:write"}}, 2*time.Hour)
		if !errors.Is(err, corepermission.ErrApproverNotEligible) {
			t.Fatalf("ineligible high-risk grant: got %v, want ErrApproverNotEligible", err)
		}
		if _, ok, _ := store.Find(ctx, "s1", "deploy_prod", suspended.Fingerprint, "op"); ok {
			t.Fatal("grant stored despite refused approval")
		}
	})

	t.Run("permission.grant-removes-the-repeat-ask", func(t *testing.T) {
		store := NewMemoryGrantStore(now)
		suspended := request("create_booking", "create_booking:{\"room\":2}")
		g, audit, err := ApproveGrant(ctx, store, "s1", suspended, types.Principal{Subject: "op", Scopes: []string{"approve:create_booking"}}, 2*time.Hour)
		if err != nil {
			t.Fatalf("approve: %v", err)
		}
		if audit.Event != GrantedByScope {
			t.Fatalf("audit event: got %q, want %q", audit.Event, GrantedByScope)
		}
		inv := &corepermission.ToolInvocation{Spec: types.ToolSpec{Name: "create_booking"}}
		ok, err := HasGrant(ctx, store, "s1", inv, g.Fingerprint, "op")
		if err != nil || !ok {
			t.Fatalf("second call of same identity: ok=%v err=%v, want granted", ok, err)
		}
	})

	t.Run("permission.fingerprint-change-misses-the-grant", func(t *testing.T) {
		store := NewMemoryGrantStore(now)
		suspended := request("create_booking", "create_booking:{\"room\":2}")
		g, _, err := ApproveGrant(ctx, store, "s1", suspended, types.Principal{Subject: "op", Scopes: []string{"approve:create_booking"}}, 2*time.Hour)
		if err != nil {
			t.Fatalf("approve: %v", err)
		}
		changed := stores.Fingerprint(string(g.Fingerprint) + "x")
		if changed == g.Fingerprint {
			t.Fatal("changed fingerprint equals the granted one")
		}
		inv := &corepermission.ToolInvocation{Spec: types.ToolSpec{Name: "create_booking"}}
		ok, err := HasGrant(ctx, store, "s1", inv, changed, "op")
		if err != nil || ok {
			t.Fatalf("changed fingerprint: ok=%v err=%v, want miss", ok, err)
		}
	})

	t.Run("permission.grant-does-not-cross-sessions-or-principals", func(t *testing.T) {
		store := NewMemoryGrantStore(now)
		suspended := request("create_booking", "create_booking:{\"room\":2}")
		g, _, err := ApproveGrant(ctx, store, "s1", suspended, types.Principal{Subject: "op", Scopes: []string{"approve:create_booking"}}, 2*time.Hour)
		if err != nil {
			t.Fatalf("approve: %v", err)
		}
		inv := &corepermission.ToolInvocation{Spec: types.ToolSpec{Name: "create_booking"}}
		if ok, _ := HasGrant(ctx, store, "s2", inv, g.Fingerprint, "op"); ok {
			t.Fatal("grant crossed into another session")
		}
		if ok, _ := HasGrant(ctx, store, "s1", inv, g.Fingerprint, "mallory"); ok {
			t.Fatal("grant crossed to another subject")
		}
	})
}
