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
)

type releaseFlags struct{}

func (releaseFlags) Bool(_ context.Context, _ string, def bool, _ types.FlagContext) (bool, types.Freshness, error) {
	return def, types.Freshness{}, nil
}

func (releaseFlags) String(_ context.Context, key, def string, _ types.FlagContext) (string, types.Freshness, error) {
	if key == "assist_v2" {
		return "b", types.Freshness{}, nil
	}
	return def, types.Freshness{}, nil
}

func (releaseFlags) Int(_ context.Context, _ string, def int64, _ types.FlagContext) (int64, types.Freshness, error) {
	return def, types.Freshness{}, nil
}

type releaseRecord struct {
	name  string
	value float64
	attrs []types.Attr
}

type releaseSpan struct {
	name  string
	attrs []types.Attr
}

type releaseTelemetry struct {
	mu      sync.Mutex
	records []releaseRecord
	spans   []releaseSpan
}

func (t *releaseTelemetry) StartSpan(ctx context.Context, name string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	t.mu.Lock()
	t.spans = append(t.spans, releaseSpan{name: name, attrs: attrs})
	t.mu.Unlock()
	return ctx, func(...types.Attr) {}
}

func (t *releaseTelemetry) Count(ctx context.Context, name string, v int64, attrs ...types.Attr) {
	t.Record(ctx, name, float64(v), attrs...)
}

func (t *releaseTelemetry) Record(_ context.Context, name string, v float64, attrs ...types.Attr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.records = append(t.records, releaseRecord{name: name, value: v, attrs: attrs})
}

func (t *releaseTelemetry) span(name string) (releaseSpan, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, s := range t.spans {
		if s.name == name {
			return s, true
		}
	}
	return releaseSpan{}, false
}

func (t *releaseTelemetry) record(name string) (releaseRecord, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out releaseRecord
	for _, r := range t.records {
		if r.name == name {
			out = r
		}
	}
	return out, out.name != ""
}

type releaseModel struct {
	profile types.ModelProfile
}

func (m *releaseModel) Profile() types.ModelProfile { return m.profile }

func (m *releaseModel) Generate(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: "ok", Usage: &types.Usage{InputTokens: 100}}, nil)
	}
}

type captureRuns struct {
	*stores.MemoryRuns
	mu   sync.Mutex
	last stores.Run
}

func (c *captureRuns) Start(ctx context.Context, r stores.Run, ttl time.Duration) (stores.Lease, error) {
	c.mu.Lock()
	c.last = r
	c.mu.Unlock()
	return c.MemoryRuns.Start(ctx, r, ttl)
}

func TestReleaseAttributes(t *testing.T) {
	t.Run("release.run-attributes", func(t *testing.T) {
		tel := &releaseTelemetry{}
		model := &releaseModel{profile: types.ModelProfile{Name: "primary", Pricing: types.Pricing{Input: 0.04}}}
		runs := &captureRuns{MemoryRuns: stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))}
		sessions := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		cps := stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom))
		audit := stores.NewMemoryAuditLog()
		events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
		native := gohan.NativeSpec{
			Request:  gohan.FlowRequest{Name: "chat"},
			Profile:  "primary",
			Assemble: std.StablePrefix{}.Assemble,
		}
		stack, err := gohan.Build(
			gohan.AllowAnonymous(),
			gohan.WithTelemetry(tel),
			gohan.WithFlags(releaseFlags{}),
			gohan.WithFrozenFlags(map[string]any{"assist_v2": "a"}),
			gohan.WithReleaseIdentity("rel", "", "assist_v2"),
			gohan.WithStores(stores.Stores{
				SessionLog:  sessions,
				Runs:        runs,
				Checkpoints: cps,
				Audit:       audit,
				EventLog:    events,
			}),
			gohan.WithModels(model),
			gohan.WithNativeAgent(native),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		conv, err := gohan.NewNativeConversation(stack, "chat",
			gohan.WithConversationRuns(runs),
			gohan.WithConversationEventLog(events),
		)
		if err != nil {
			t.Fatalf("new conversation: %v", err)
		}
		ctx := types.WithTelemetry(types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"}), tel)
		for _, sendErr := range conv.Send(ctx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}) {
			if sendErr != nil {
				t.Fatalf("send: %v", sendErr)
			}
		}

		runID := func() string {
			runs.mu.Lock()
			defer runs.mu.Unlock()
			return runs.last.RunID
		}()
		if runID == "" {
			t.Fatal("no run started")
		}

		span, ok := tel.span(gohan.SpanRun)
		if !ok {
			t.Fatalf("span %q missing among spans", gohan.SpanRun)
		}
		spanAttr := func(key, want string) {
			t.Helper()
			for _, a := range span.attrs {
				if a.Key == key {
					if a.Value != want {
						t.Fatalf("span attr %s = %q, want %q", key, a.Value, want)
					}
					return
				}
			}
			t.Fatalf("span attr %s missing on %v", key, span.attrs)
		}
		spanAttr(types.KeyRelease, "rel")
		spanAttr(types.KeyVariant, "b")
		spanAttr(types.KeyModeAttr, "primary")

		var started *stores.AuditRecord
		for rec, rerr := range audit.Read(ctx, "s1") {
			if rerr != nil {
				t.Fatalf("audit read: %v", rerr)
			}
			if rec.Kind == stores.AuditRunStarted {
				started = &rec
			}
		}
		if started == nil {
			t.Fatal("audit run.started record missing")
		}
		if started.ReleaseID != "rel" || started.Variant != "b" {
			t.Fatalf("audit release/variant = %q/%q, want rel/b", started.ReleaseID, started.Variant)
		}

		var done *stores.Event
		for ev, rerr := range events.Read(ctx, runID, 0) {
			if rerr != nil {
				t.Fatalf("event read: %v", rerr)
			}
			if _, isDone := ev.Payload.(types.Done); isDone {
				done = &ev
			}
		}
		if done == nil {
			t.Fatalf("Done event missing for run %s", runID)
		}
		if done.Meta.ReleaseID != "rel" || done.Meta.Variant != "b" {
			t.Fatalf("Done meta release/variant = %q/%q, want rel/b", done.Meta.ReleaseID, done.Meta.Variant)
		}

		cost, ok := tel.record("gohan.cost.per_run")
		if !ok {
			t.Fatalf("gohan.cost.per_run missing among %d records", len(tel.records))
		}
		hasAttr := func(key, want string) {
			t.Helper()
			for _, a := range cost.attrs {
				if a.Key == key {
					if a.Value != want {
						t.Fatalf("cost attr %s = %q, want %q", key, a.Value, want)
					}
					return
				}
			}
			t.Fatalf("cost attr %s missing on %v", key, cost.attrs)
		}
		hasAttr(types.KeyRelease, "rel")
		hasAttr(types.KeyVariant, "b")
		hasAttr(types.KeyFlow, "chat")

		shadowTel := &releaseTelemetry{}
		shadowModel := &releaseModel{profile: types.ModelProfile{Name: "primary", Pricing: types.Pricing{Input: 0.04}}}
		shadowStack, err := gohan.Build(
			gohan.AllowAnonymous(),
			gohan.WithTelemetry(shadowTel),
			gohan.WithShadowMode(),
			gohan.WithFlags(releaseFlags{}),
			gohan.WithFrozenFlags(map[string]any{"assist_v2": "a"}),
			gohan.WithReleaseIdentity("rel", "", "assist_v2"),
			gohan.WithStores(stores.Stores{
				SessionLog:  sessions,
				Runs:        runs,
				Checkpoints: cps,
				Audit:       audit,
				EventLog:    events,
			}),
			gohan.WithModels(shadowModel),
			gohan.WithNativeAgent(native),
		)
		if err != nil {
			t.Fatalf("shadow build: %v", err)
		}
		shadowConv, err := gohan.NewNativeConversation(shadowStack, "chat",
			gohan.WithConversationRuns(runs),
			gohan.WithConversationEventLog(events),
		)
		if err != nil {
			t.Fatalf("shadow new conversation: %v", err)
		}
		shadowCtx := types.WithTelemetry(types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"}), shadowTel)
		for _, sendErr := range shadowConv.Send(shadowCtx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}) {
			if sendErr != nil {
				t.Fatalf("shadow send: %v", sendErr)
			}
		}
		shadowRuns, ok := shadowTel.record("gohan.shadow.runs")
		if !ok {
			t.Fatalf("gohan.shadow.runs missing among %d records", len(shadowTel.records))
		}
		shadowAttr := func(key, want string) {
			t.Helper()
			for _, a := range shadowRuns.attrs {
				if a.Key == key {
					if a.Value != want {
						t.Fatalf("shadow runs attr %s = %q, want %q", key, a.Value, want)
					}
					return
				}
			}
			t.Fatalf("shadow runs attr %s missing on %v", key, shadowRuns.attrs)
		}
		shadowAttr(types.KeyRelease, "rel")
		shadowAttr(types.KeyVariant, "b")
	})
}
