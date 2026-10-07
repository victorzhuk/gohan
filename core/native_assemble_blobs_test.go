package gohan

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func blobLedger(t *testing.T) *stores.MemorySessionLog {
	t.Helper()
	return stores.NewMemorySessionLog(stores.WithSessionPrincipals(func(context.Context) (types.Principal, bool) {
		return types.Principal{Tenant: "t1", Subject: "u1", Scopes: []string{"session:read", "session:write"}}, true
	}))
}

func openBlobSession(t *testing.T, ctx context.Context, log *stores.MemorySessionLog, id string) {
	t.Helper()
	if _, err := log.Append(ctx, id, 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}); err != nil {
		t.Fatalf("append: %v", err)
	}
}

type countingUploader struct {
	mu    sync.Mutex
	calls int
	last  types.Blob
	id    string
}

func newCountingUploader(id string) *countingUploader { return &countingUploader{id: id} }

func (u *countingUploader) Upload(_ context.Context, b types.Blob, _ string) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls++
	u.last = b
	return u.id, nil
}

func (u *countingUploader) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

func seedBlobBytes(t *testing.T, ctx context.Context, outs stores.OutputStore, run types.RunInfo, mime string) string {
	t.Helper()
	ref, err := outs.Put(ctx, run, []types.Block{types.Image{MIME: mime, Data: []byte("blob-bytes")}})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	return ref
}

func blobResolveCtx() context.Context {
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
	return types.WithRunInfo(ctx, types.RunInfo{SessionID: "s1", Principal: types.Principal{Tenant: "t1", Subject: "u1"}})
}

func TestBlobAssemblyResolution(t *testing.T) {
	ctx := blobResolveCtx()

	t.Run("ledger-hit-reuses-id", func(t *testing.T) {
		log := blobLedger(t)
		openBlobSession(t, ctx, log, "s1")
		if err := log.SetBlobProviderID(ctx, "s1", "r1", "native", "prov-1"); err != nil {
			t.Fatalf("seed: %v", err)
		}
		up := newCountingUploader("up-1")
		ba := blobAssembly{profile: "native", ledger: log, outputs: stores.NewMemoryOutputs(), upload: up}
		req := types.ModelRequest{Messages: []types.Message{{Role: types.RoleUser, Blocks: []types.Block{
			types.Image{MIME: "image/png", Blob: types.Blob{Ref: "r1", SHA256: "ab"}},
		}}}}
		got, err := ba.resolve(ctx, req)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		img := got.Messages[0].Blocks[0].(types.Image)
		if img.Blob.Provider != "native" || img.Blob.ProviderID != "prov-1" {
			t.Fatalf("blob = %+v, want the ledger id prov-1", img.Blob)
		}
		if img.Data != nil {
			t.Fatal("a ledger hit loaded bytes")
		}
		if n := up.count(); n != 0 {
			t.Fatalf("uploads = %d, want 0", n)
		}
	})

	t.Run("miss-uploads-once-and-records", func(t *testing.T) {
		log := blobLedger(t)
		openBlobSession(t, ctx, log, "s1")
		up := newCountingUploader("up-1")
		ba := blobAssembly{profile: "native", ledger: log, outputs: stores.NewMemoryOutputs(), upload: up}
		blob := types.Blob{Ref: "r1", SHA256: "ab", Bytes: 11}
		req := types.ModelRequest{Messages: []types.Message{{Role: types.RoleUser, Blocks: []types.Block{
			types.Image{MIME: "image/png", Blob: blob},
		}}}}
		if _, err := ba.resolve(ctx, req); err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if _, err := ba.resolve(ctx, req); err != nil {
			t.Fatalf("second resolve: %v", err)
		}
		if n := up.count(); n != 1 {
			t.Fatalf("uploads = %d, want one per (session, ref)", n)
		}
		provider, id, ok := log.BlobProviderID(ctx, "s1", "r1")
		if !ok || provider != "native" || id != "up-1" {
			t.Fatalf("ledger = (%q, %q, %v), want (native, up-1, true)", provider, id, ok)
		}
	})

	t.Run("adapter-without-port-inlines-bytes", func(t *testing.T) {
		log := blobLedger(t)
		openBlobSession(t, ctx, log, "s1")
		outs := stores.NewMemoryOutputs()
		ref := seedBlobBytes(t, ctx, outs, types.RunInfo{SessionID: "s1", Principal: types.Principal{Tenant: "t1", Subject: "u1"}}, "image/png")
		ba := blobAssembly{profile: "native", ledger: log, outputs: outs}
		req := types.ModelRequest{Messages: []types.Message{{Role: types.RoleUser, Blocks: []types.Block{
			types.Image{MIME: "image/png", Blob: types.Blob{Ref: ref}},
		}}}}
		got, err := ba.resolve(ctx, req)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		img := got.Messages[0].Blocks[0].(types.Image)
		if len(img.Data) == 0 {
			t.Fatal("no bytes inlined for an adapter without the port")
		}
		if _, _, ok := log.BlobProviderID(ctx, "s1", ref); ok {
			t.Fatal("a byte load wrote a ledger row")
		}
	})
}

func TestBlobForeignProviderRematerialises(t *testing.T) {
	ctx := blobResolveCtx()
	log := blobLedger(t)
	openBlobSession(t, ctx, log, "s1")
	if err := log.SetBlobProviderID(ctx, "s1", "r1", "acme", "acme-1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	outs := stores.NewMemoryOutputs()
	ref := seedBlobBytes(t, ctx, outs, types.RunInfo{SessionID: "s1", Principal: types.Principal{Tenant: "t1", Subject: "u1"}}, "image/png")
	up := newCountingUploader("up-1")
	ba := blobAssembly{profile: "native", ledger: log, outputs: outs, upload: up}
	req := types.ModelRequest{Messages: []types.Message{{Role: types.RoleUser, Blocks: []types.Block{
		types.Image{MIME: "image/png", Blob: types.Blob{Ref: ref}},
	}}}}
	got, err := ba.resolve(ctx, req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	img := got.Messages[0].Blocks[0].(types.Image)
	if img.Blob.ProviderID == "acme-1" {
		t.Fatal("a foreign id reached the request")
	}
	if img.Blob.Provider != "native" || img.Blob.ProviderID != "up-1" {
		t.Fatalf("blob = %+v, want a fresh native upload", img.Blob)
	}
	if n := up.count(); n != 1 {
		t.Fatalf("uploads = %d, want 1", n)
	}
	if provider, id, _ := log.BlobProviderID(ctx, "s1", ref); provider != "native" || id != "up-1" {
		t.Fatalf("ledger = (%q, %q), want the row re-issued by native", provider, id)
	}
}

func TestBlobMisuseInputInvalid(t *testing.T) {
	ctx := blobResolveCtx()
	log := blobLedger(t)
	openBlobSession(t, ctx, log, "s1")
	if err := log.SetBlobProviderID(ctx, "s1", "r1", "acme", "acme-1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ba := blobAssembly{profile: "native", ledger: log, outputs: stores.NewMemoryOutputs()}
	req := types.ModelRequest{Messages: []types.Message{{Role: types.RoleUser, Blocks: []types.Block{
		types.Image{MIME: "image/png", Blob: types.Blob{Ref: "r1"}},
	}}}}
	_, err := ba.resolve(ctx, req)
	if !errors.Is(err, types.ErrInputInvalid) {
		t.Fatalf("err = %v, want ErrInputInvalid", err)
	}
	if code := types.ProblemOf(err).Code; code != "gohan.input_invalid" {
		t.Fatalf("code = %q, want gohan.input_invalid", code)
	}
}
