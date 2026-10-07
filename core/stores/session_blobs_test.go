package stores

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func sessionForLedger(t *testing.T, ctx context.Context, log *MemorySessionLog, id string) {
	t.Helper()
	if _, err := log.Append(ctx, id, 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func TestSessionBlobLedger(t *testing.T) {
	var log SessionLog = NewMemorySessionLog()
	if _, ok := log.(SessionBlobs); !ok {
		t.Fatal("MemorySessionLog does not implement SessionBlobs")
	}
}

type bareSessionLog struct{ SessionLog }

func TestSessionBlobLedgerMemory(t *testing.T) {
	ctx := context.Background()
	log := NewMemorySessionLog(WithSessionPrincipals(func(context.Context) (types.Principal, bool) {
		return types.Principal{Tenant: "t1", Subject: "u1", Scopes: []string{scopeSessionRead, scopeSessionWrite}}, true
	}))
	sessionForLedger(t, ctx, log, "s1")

	if _, _, ok := log.BlobProviderID(ctx, "s1", "r1"); ok {
		t.Fatal("empty ledger answered a ref")
	}
	if err := log.SetBlobProviderID(ctx, "s1", "r1", "acme", "prov-1"); err != nil {
		t.Fatalf("set: %v", err)
	}
	provider, id, ok := log.BlobProviderID(ctx, "s1", "r1")
	if !ok || provider != "acme" || id != "prov-1" {
		t.Fatalf("read = (%q, %q, %v), want (acme, prov-1, true)", provider, id, ok)
	}

	if _, err := log.Load(ctx, "s1"); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, _, ok := log.BlobProviderID(ctx, "s1", "r1"); !ok {
		t.Fatal("row lost after Load")
	}

	hist, err := log.Load(ctx, "s1")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	forkID, err := log.Fork(ctx, "s1", hist.Messages[0].ID)
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if _, _, ok := log.BlobProviderID(ctx, forkID, "r1"); !ok {
		t.Fatal("fork lost the ledger row")
	}

	if err := log.Delete(ctx, "s1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, ok := log.BlobProviderID(ctx, "s1", "r1"); ok {
		t.Fatal("row survived the session cascade")
	}

	var embedded SessionLog = bareSessionLog{log}
	if _, ok := embedded.(SessionBlobs); ok {
		t.Error("a log without the implementation reported the surface")
	}
}
