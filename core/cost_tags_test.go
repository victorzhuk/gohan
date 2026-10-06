package gohan_test

import (
	"context"
	"iter"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
	"github.com/victorzhuk/gohan/std/limit"
)

const metricCostPerRun = "gohan.cost.per_run"

type costRecord struct {
	name  string
	value float64
	attrs []types.Attr
}

type costSink struct {
	mu      sync.Mutex
	records []costRecord
}

func (s *costSink) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (s *costSink) Count(context.Context, string, int64, ...types.Attr) {}

func (s *costSink) Record(_ context.Context, name string, v float64, attrs ...types.Attr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, costRecord{name: name, value: v, attrs: attrs})
}

type costTagModel struct {
	profile types.ModelProfile
	mu      sync.Mutex
	reqs    []types.ModelRequest
}

func (m *costTagModel) Profile() types.ModelProfile { return m.profile }

func (m *costTagModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.mu.Lock()
	m.reqs = append(m.reqs, req)
	m.mu.Unlock()
	return func(yield func(types.ModelChunk, error) bool) {
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: "ok", Usage: &types.Usage{InputTokens: 100}}, nil)
	}
}

func TestCostTagsForwarded(t *testing.T) {
	t.Run("lifecycle.cost-tags-forwarded", func(t *testing.T) {
		tags := types.CostTags{Feature: "assist", Environment: "prod"}
		pricing := types.Pricing{Input: 0.04}
		tel := &costSink{}
		model := &costTagModel{profile: types.ModelProfile{Name: "primary", Pricing: pricing}}
		native := gohan.NativeSpec{
			Request:  gohan.FlowRequest{Name: "chat"},
			Profile:  "primary",
			Assemble: std.StablePrefix{}.Assemble,
		}
		stack, err := gohan.Build(
			gohan.AllowAnonymous(),
			gohan.WithTelemetry(tel),
			gohan.WithModelMiddleware(limit.Limits(types.RunLimits{MaxCost: 100, MaxWallClock: time.Minute, SoftRatio: 0.8}, pricing)),
			gohan.WithStores(stores.Stores{
				SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
				Runs:        stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom)),
				Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
			}),
			gohan.WithModels(model),
			gohan.WithNativeAgent(native),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		conv, err := gohan.NewNativeConversation(stack, "chat",
			gohan.WithConversationRuns(stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))),
			gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		)
		if err != nil {
			t.Fatalf("new conversation: %v", err)
		}
		ctx := types.WithRunInfo(
			types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"}),
			types.RunInfo{CostTags: tags},
		)
		for _, sendErr := range conv.Send(ctx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}) {
			if sendErr != nil {
				t.Fatalf("send: %v", sendErr)
			}
		}

		model.mu.Lock()
		reqs := append([]types.ModelRequest(nil), model.reqs...)
		model.mu.Unlock()
		if len(reqs) == 0 {
			t.Fatal("model never called")
		}
		if reqs[0].Metadata.CostTags != tags {
			t.Fatalf("metadata cost tags = %+v, want %+v", reqs[0].Metadata.CostTags, tags)
		}

		tel.mu.Lock()
		records := append([]costRecord(nil), tel.records...)
		tel.mu.Unlock()
		var cost costRecord
		for _, r := range records {
			if r.name == metricCostPerRun {
				cost = r
				break
			}
		}
		if cost.name != metricCostPerRun {
			t.Fatalf("gohan.cost.per_run missing among %d records", len(records))
		}
		if want := 100 * pricing.Input; cost.value < want-1e-9 || cost.value > want+1e-9 {
			t.Errorf("cost value = %v, want %v", cost.value, want)
		}
		if len(cost.attrs) != 1 || cost.attrs[0].Key != types.KeyFlow || cost.attrs[0].Value != "chat" {
			t.Errorf("cost attrs = %+v, want only flow=chat", cost.attrs)
		}
	})
}
