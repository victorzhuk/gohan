package gohan

import (
	"context"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type blobUploadingModel struct {
	mu       sync.Mutex
	uploads  int
	requests []types.ModelRequest
}

func (m *blobUploadingModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "native"}
}

func (m *blobUploadingModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	return func(yield func(types.ModelChunk, error) bool) {
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: "ok"}, nil)
		yield(types.ModelChunk{Finish: types.FinishStop}, nil)
	}
}

func (m *blobUploadingModel) Upload(_ context.Context, b types.Blob, _ string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.uploads++
	return "prov-1", nil
}

func TestBlobProviderID(t *testing.T) {
	t.Run("messages.blob-provider-id-reused", func(t *testing.T) {
		ctx := nrrCtx()
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		outs := stores.NewMemoryOutputs()
		runs := stores.NewMemoryRuns(
			stores.WithMemoryRunClock(time.Now),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		)
		model := &blobUploadingModel{}
		stack, err := Build(
			WithStores(stores.Stores{SessionLog: log, Runs: runs, OutputStore: outs}),
			WithModels(model),
			WithNativeAgent(NativeSpec{
				Request: FlowRequest{Name: "chat"},
				Profile: "native",
				Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
					return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
				},
			}),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		conv, err := NewNativeConversation(stack, "chat",
			WithConversationRuns(runs),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
			WithConversationApprovalPolicy(nrrAllowPolicy{}),
		)
		if err != nil {
			t.Fatalf("new native conversation: %v", err)
		}

		drain := func(msg types.Message) {
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

		drain(types.Message{Role: types.RoleUser, Blocks: []types.Block{
			types.Image{MIME: "image/png", Data: make([]byte, stores.InlineBlobBytes+1)},
		}})

		var ref string
		for _, msg := range func() []types.Message {
			hist, err := log.Load(ctx, "s1")
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			return hist.Messages
		}() {
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
		drain(carrier)
		drain(carrier)

		if n := model.uploads; n != 1 {
			t.Fatalf("uploads = %d, want exactly one across three turns", n)
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
					if img.Blob.Provider != "native" || img.Blob.ProviderID != "prov-1" {
						t.Fatalf("turn %d carries blob %+v, want the reused provider id", i+2, img.Blob)
					}
				}
			}
			if !found {
				t.Fatalf("turn %d carried no blob block", i+2)
			}
		}
	})
}
