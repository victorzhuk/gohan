package stores

import (
	"context"
	"encoding/json"
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

func TestSessionBlobLedgerRebuild(t *testing.T) {
	ctx := context.Background()
	principals := WithSessionPrincipals(func(context.Context) (types.Principal, bool) {
		return types.Principal{Tenant: "t1", Subject: "u1", Scopes: []string{scopeSessionRead, scopeSessionWrite}}, true
	})

	src := NewMemorySessionLog(principals)
	sessionForLedger(t, ctx, src, "s1")
	if err := src.SetBlobProviderID(ctx, "s1", "r1", "acme", "prov-1"); err != nil {
		t.Fatalf("set r1: %v", err)
	}
	if err := src.SetBlobProviderID(ctx, "s1", "r2", "acme", "prov-2"); err != nil {
		t.Fatalf("set r2: %v", err)
	}

	stored, err := src.StoredRecord(ctx, "session/s1/blobs")
	if err != nil {
		t.Fatalf("stored record: %v", err)
	}
	var env RecordEnvelope
	if err := json.Unmarshal(stored, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}

	dst := NewMemorySessionLog(principals)
	sessionForLedger(t, ctx, dst, "s1")
	if err := dst.SeedRecord(ctx, "session/s1/blobs", env); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	for ref, want := range map[string]string{"r1": "prov-1", "r2": "prov-2"} {
		provider, id, ok := dst.BlobProviderID(ctx, "s1", ref)
		if !ok || provider != "acme" || id != want {
			t.Fatalf("rebuilt read %s = (%q, %q, %v), want (acme, %q, true)", ref, provider, id, ok, want)
		}
	}

	if err := dst.SetBlobProviderID(ctx, "s1", "r3", "acme", "prov-3"); err != nil {
		t.Fatalf("set on rebuilt store: %v", err)
	}
	if provider, id, ok := dst.BlobProviderID(ctx, "s1", "r3"); !ok || provider != "acme" || id != "prov-3" {
		t.Fatalf("rebuilt row not usable: got (%q, %q, %v)", provider, id, ok)
	}
}
