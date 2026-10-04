package gohan

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestResumeInputConstructors(t *testing.T) {
	if got := Approve(); got.Verdict != stores.VerdictApprove {
		t.Fatalf("Approve: got verdict %d, want VerdictApprove", got.Verdict)
	}
	if got := Reject("no"); got.Verdict != stores.VerdictReject || got.Reason != "no" {
		t.Fatalf("Reject: got verdict %d reason %q, want VerdictReject and \"no\"", got.Verdict, got.Reason)
	}
	if got := EditArgs(json.RawMessage(`{"x":1}`)); got.Verdict != stores.VerdictEdit || string(got.Args) != `{"x":1}` {
		t.Fatalf("EditArgs: got verdict %d args %s, want VerdictEdit and the edited args", got.Verdict, got.Args)
	}
	if got := Deliver(json.RawMessage(`"done"`)); string(got.Data) != `"done"` {
		t.Fatalf("Deliver: got data %s, want the delivered payload", got.Data)
	}
	if got := Continue(); got.Verdict != stores.VerdictApprove || len(got.Data) != 0 {
		t.Fatalf("Continue: got %+v, want the zero decision", got)
	}
}

func TestApprovalInContext(t *testing.T) {
	ctx := context.Background()
	if _, ok := types.ApprovalFrom(ctx); ok {
		t.Fatal("ApprovalFrom on a plain context: got ok, want false")
	}
	ap := types.Approval{Approver: types.Principal{Tenant: "t", Subject: "op"}}
	got, ok := types.ApprovalFrom(types.WithApproval(ctx, ap))
	if !ok || got.Approver.Subject != "op" {
		t.Fatalf("ApprovalFrom after WithApproval: got %+v ok=%v, want the approval", got, ok)
	}
}
