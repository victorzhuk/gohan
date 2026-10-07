package std

import (
	"context"
	"errors"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

func TestIdentityValuesSurviveShield(t *testing.T) {
	t.Run("identity.values-survive-shield", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		p := types.Principal{Tenant: "t-a", Subject: "u1"}
		cred := types.Credential{Token: "tok-1", ExpiresAt: time.Now().Add(time.Hour)}
		info := types.RunInfo{RunID: "r1", RootRunID: "r1", Principal: p}
		ctx = types.WithPrincipal(ctx, p)
		ctx = gohan.WithCredential(ctx, cred)
		ctx = types.WithRunInfo(ctx, info)
		ctx = types.WithIdempotencyKey(ctx, "op-1")
		cancel()

		var gotP types.Principal
		var gotPbool bool
		var gotC types.Credential
		var gotCbool bool
		var gotI types.RunInfo
		var gotIbool bool
		tool := chains.ToolFunc(func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			gotP, gotPbool = types.PrincipalFrom(ctx)
			gotC, gotCbool = gohan.CredentialFrom(ctx)
			gotI, gotIbool = types.RunInfoFrom(ctx)
			return types.ToolResult{Outcome: types.Succeeded}, nil
		})
		chain := Shield(lookupOf(bookingSpecs()))(tool)
		res, err := chain(ctx, toolCall("create_booking", "c1", `{}`))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled after the shielded effect", err)
		}
		if res.Outcome != types.Succeeded {
			t.Fatalf("outcome = %v, want the shielded tool result", res.Outcome)
		}
		if !gotPbool || gotP.Tenant != p.Tenant || gotP.Subject != p.Subject {
			t.Fatalf("shielded principal = %+v, %v; want %+v", gotP, gotPbool, p)
		}
		if !gotCbool || gotC != cred {
			t.Fatalf("shielded credential = %+v, %v; want %+v", gotC, gotCbool, cred)
		}
		if !gotIbool || gotI.RunID != info.RunID || gotI.RootRunID != info.RootRunID {
			t.Fatalf("shielded run info = %+v, %v; want %+v", gotI, gotIbool, info)
		}
	})
}
