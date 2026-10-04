package gohan

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestIdentityArguments(t *testing.T) {
	t.Run("identity.model-cannot-set-identity", func(t *testing.T) {
		m := NewIdentityFieldMatcher("user_id", "tenant", "customer_id")

		args := map[string]string{
			"user_id":     "attacker",
			"tenant":      "t2",
			"customer_id": "c9",
			"query":       "ok",
		}
		var kept []string
		for name, v := range args {
			if m.Match(name) {
				continue
			}
			kept = append(kept, name+"="+v)
		}
		if len(kept) != 1 || kept[0] != "query=ok" {
			t.Fatalf("identity fields not excluded: kept %v", kept)
		}

		// PrincipalFrom stays authoritative: the identity the tool acts on is
		// the transport principal in ctx, never a value from args.
		ctx := WithPrincipal(context.Background(), types.Principal{Subject: "op-7", Tenant: "t1"})
		p, ok := PrincipalFrom(ctx)
		if !ok || p.Tenant != "t1" || p.Subject != "op-7" {
			t.Fatalf("PrincipalFrom(ctx): got (%v, %v), want op-7/t1, true", p, ok)
		}
	})
}
