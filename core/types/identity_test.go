package types

import (
	"context"
	"testing"
	"time"
)

func TestWithoutRunIdentity(t *testing.T) {
	p := Principal{Tenant: "t-a", Subject: "u1"}
	info := RunInfo{RunID: "root", RootRunID: "root", Principal: p}
	ctx := context.Background()
	ctx = WithPrincipal(ctx, p)
	ctx = WithIdempotencyKey(ctx, "op-42")
	ctx = WithApproval(ctx, Approval{Approver: p, At: time.Now()})
	ctx = WithRunInfo(ctx, info)

	got := WithoutRunIdentity(ctx)
	if _, ok := IdempotencyKey(got); ok {
		t.Fatal("idempotency key survived the strip")
	}
	if _, ok := ApprovalFrom(got); ok {
		t.Fatal("approval survived the strip")
	}
	gp, ok := PrincipalFrom(got)
	if !ok || gp.Tenant != p.Tenant || gp.Subject != p.Subject {
		t.Fatalf("principal = %+v, %v; want %+v, true", gp, ok, p)
	}
	gi, ok := RunInfoFrom(got)
	if !ok || gi.RunID != info.RunID || gi.RootRunID != info.RootRunID {
		t.Fatalf("run info = %+v, %v; want %+v, true", gi, ok, info)
	}
	if _, ok := IdempotencyKey(ctx); !ok {
		t.Fatal("strip mutated the caller's context")
	}
}
