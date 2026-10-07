package conformance

import (
	"context"
	"iter"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	stdpermission "github.com/victorzhuk/gohan/std/permission"
)

// reuseModel is the double whose Upload counts calls and whose Generate
// records the assembled request, so a shipped-path run can observe what the
// port suite cannot.
type reuseModel struct {
	mu       sync.Mutex
	uploads  int
	requests []types.ModelRequest
}

func (m *reuseModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "conformance-blob", Version: "v1"}
}

func (m *reuseModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	return func(yield func(types.ModelChunk, error) bool) {
		if !yield(types.ModelChunk{Kind: types.DeltaText, Delta: "ok"}, nil) {
			return
		}
		yield(types.ModelChunk{Finish: types.FinishStop}, nil)
	}
}

func (m *reuseModel) Upload(_ context.Context, _ types.Blob, _ string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.uploads++
	return "up-blob-1", nil
}

// TestBlobProviderIDReuseShippedPath lands the two halves the port fixture
// cannot observe: the blob is uploaded exactly once across three turns, and
// the outgoing request of later turns carries the resolved provider id with
// no bytes. It drives the shipped assembler through the public stack.
func TestBlobProviderIDReuseShippedPath(t *testing.T) {
	ctx := gohan.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
	log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	outs := stores.NewMemoryOutputs()
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	model := &reuseModel{}
	stack, err := gohan.Build(
		gohan.WithStores(stores.Stores{SessionLog: log, Runs: runs, OutputStore: outs}),
		gohan.WithModels(model),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: "chat"},
			Profile: "conformance-blob",
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := gohan.NewNativeConversation(stack, "chat",
		gohan.WithConversationRuns(runs),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		gohan.WithConversationApprovalPolicy(stdpermission.PolicySource{}),
	)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}

	send := func(msg types.Message) {
		t.Helper()
		for ev, err := range conv.Send(ctx, "s1", msg) {
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			if d, ok := ev.(types.Done); ok && d.Reason != types.StopCompleted {
				t.Fatalf("done %v, want completed", d.Reason)
			}
		}
	}

	send(types.Message{Role: types.RoleUser, Blocks: []types.Block{
		types.Image{MIME: "image/png", Data: make([]byte, stores.InlineBlobBytes+1)},
	}})

	hist, err := log.Load(ctx, "s1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var ref string
	for _, msg := range hist.Messages {
		for _, b := range msg.Blocks {
			if img, ok := b.(types.Image); ok && img.Blob.Ref != "" {
				ref = img.Blob.Ref
			}
		}
	}
	if ref == "" {
		t.Fatal("the first turn persisted no blob ref")
	}

	carrier := types.Message{Role: types.RoleUser, Blocks: []types.Block{
		types.Image{MIME: "image/png", Blob: types.Blob{Ref: ref}},
	}}
	send(carrier)
	send(carrier)

	if n := model.uploads; n != 1 {
		t.Fatalf("uploads = %d, want exactly one across three turns", n)
	}
	if len(model.requests) != 3 {
		t.Fatalf("model saw %d requests, want 3", len(model.requests))
	}
	for i, req := range model.requests[1:] {
		found := false
		for _, msg := range req.Messages {
			for _, b := range msg.Blocks {
				img, ok := b.(types.Image)
				if !ok || img.Blob.Ref != ref {
					continue
				}
				found = true
				if img.Blob.Provider != "conformance-blob" || img.Blob.ProviderID != "up-blob-1" {
					t.Fatalf("turn %d carries blob %+v, want the reused provider id", i+2, img.Blob)
				}
				if len(img.Data) != 0 {
					t.Fatalf("turn %d carried bytes alongside the id: %d bytes", i+2, len(img.Data))
				}
			}
		}
		if !found {
			t.Fatalf("turn %d carried no blob block", i+2)
		}
	}
}
