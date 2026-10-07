package gohan

import (
	"context"
	"iter"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	stdtelemetry "github.com/victorzhuk/gohan/std/telemetry"
)

type handoffRT struct {
	err error
}

func (r *handoffRT) Name() string                         { return "handoff.test" }
func (r *handoffRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *handoffRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *handoffRT) Step(context.Context, runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	return runtime.State{}, nil, runtime.DoneStatus, r.err
}

type titleRT struct{}

func (r *titleRT) Name() string                         { return "title.test" }
func (r *titleRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *titleRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *titleRT) Step(context.Context, runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	return runtime.State{}, nil, runtime.DoneStatus, nil
}

func handoffSetup(t *testing.T, rt runtime.Runtime) *takeoverFixture {
	t.Helper()
	log := &takeoverLog{MemorySessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}
	f := takeoverSetup(t, &takeoverRT{}, log)
	f.conv.rt = rt
	return f
}

type fbObs struct {
	v     float64
	attrs []types.Attr
}

func ownerOf(t *testing.T, log *stores.MemorySessionLog, sessionID string) types.SessionOwner {
	t.Helper()
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t-a", Subject: "u1", Scopes: []string{types.ScopeSessionRead, types.ScopeSessionWrite}})
	owner, err := log.Owner(ctx, sessionID)
	if err != nil {
		t.Fatalf("owner: %v", err)
	}
	return owner
}

func titleOf(t *testing.T, log *stores.MemorySessionLog, sessionID string) string {
	t.Helper()
	metas, _, err := log.Sessions(principalCtx(context.Background()), ownerOf(t, log, sessionID), stores.SessionQuery{Kinds: []stores.SessionKind{stores.SessionPrimary}})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	for _, m := range metas {
		if m.ID == sessionID {
			return m.Title
		}
	}
	t.Fatalf("session %s not indexed", sessionID)
	return ""
}

type fbSink struct {
	records []fbObs
}

func (s *fbSink) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (s *fbSink) Count(context.Context, string, int64, ...types.Attr) {}

func (s *fbSink) Record(_ context.Context, _ string, v float64, attrs ...types.Attr) {
	s.records = append(s.records, fbObs{v: v, attrs: attrs})
}

func seedFeedbackOwner(t *testing.T, log *stores.MemorySessionLog, sessionID, subject string) {
	t.Helper()
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t-a", Subject: subject, Scopes: []string{types.ScopeSessionWrite, types.ScopeSessionRead}})
	if _, err := log.Append(ctx, sessionID, 0, userMsg("seed")); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestSessionMetadataHandoff(t *testing.T) {
	t.Run("flow.request-human-ends-run", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := handoffSetup(t, &handoffRT{err: &types.HandoffError{Context: types.HandoffContext{
				Reason: "needs a human",
				Skill:  "billing",
			}}})
			ctx := takeoverCtx(principalCtx(context.Background()), f)
			seedSession(t, f, "sess-t")

			var last Event
			for ev, err := range f.conv.Send(ctx, "sess-t", userMsg("help")) {
				if err != nil {
					t.Fatalf("send: %v", err)
				}
				last = ev
			}
			d, ok := last.(types.Done)
			if !ok {
				t.Fatalf("last event %T, want types.Done", last)
			}
			if d.Reason != types.StopHandedOff {
				t.Fatalf("reason = %q, want handed_off", d.Reason)
			}
			control, err := f.log.Control(ctx, "sess-t")
			if err != nil || control != stores.ControlHandoffRequested {
				t.Fatalf("control = %v (%v), want ControlHandoffRequested", control, err)
			}
			metas, _, err := f.log.Sessions(ctx, ownerOf(t, f.log.MemorySessionLog, "sess-t"), stores.SessionQuery{Kinds: []stores.SessionKind{stores.SessionPrimary}})
			if err != nil {
				t.Fatalf("sessions: %v", err)
			}
			var handoff types.HandoffContext
			for _, m := range metas {
				if m.ID == "sess-t" {
					handoff = m.Handoff
				}
			}
			if handoff.Reason != "needs a human" || handoff.Skill != "billing" {
				t.Fatalf("handoff = %+v, want persisted context", handoff)
			}
			kinds := auditKinds(t, f)
			if len(kinds) == 0 || kinds[len(kinds)-1] != "handoff_requested" {
				t.Fatalf("audit kinds %v, want handoff_requested", kinds)
			}
		})
	})

	t.Run("flow.title-generated-after-first-done", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := handoffSetup(t, &titleRT{})
			f.conv.titleProfile = "cheap"
			f.conv.recipeModel = func(string) (RecipeCall, error) {
				return RecipeCall{Call: func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
					return func(yield func(types.ModelChunk, error) bool) {
						yield(types.ModelChunk{Kind: types.DeltaText, Delta: `"Quarterly Refund Questions"`}, nil)
					}
				}}, nil
			}
			ctx := principalCtx(context.Background())
			seedSession(t, f, "sess-t")

			for ev, err := range f.conv.Send(ctx, "sess-t", userMsg("refund please")) {
				if err != nil {
					t.Fatalf("send: %v", err)
				}
				_ = ev
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if title := titleOf(t, f.log.MemorySessionLog, "sess-t"); title == "Quarterly Refund Questions" {
					return
				} else if time.Now().After(deadline) {
					t.Fatalf("title = %q, want generated title", title)
				}
				time.Sleep(time.Millisecond)
			}
		})
	})

	t.Run("flow.title-locked-by-rename", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := handoffSetup(t, &titleRT{})
			f.conv.titleProfile = "cheap"
			calls := 0
			f.conv.recipeModel = func(string) (RecipeCall, error) {
				calls++
				return RecipeCall{}, errConversationRuns
			}
			ctx := principalCtx(context.Background())
			seedSession(t, f, "sess-t")
			renamed := "My Own Name"
			if err := f.log.UpdateSession(ctx, "sess-t", stores.SessionPatch{Title: &renamed}); err != nil {
				t.Fatalf("rename: %v", err)
			}
			for ev, err := range f.conv.Send(ctx, "sess-t", userMsg("hello")) {
				if err != nil {
					t.Fatalf("send: %v", err)
				}
				_ = ev
			}
			time.Sleep(10 * time.Millisecond)
			if calls != 0 {
				t.Fatalf("title model called %d times after rename, want 0", calls)
			}
			if title := titleOf(t, f.log.MemorySessionLog, "sess-t"); title != renamed {
				t.Fatalf("title = %q, want the user's rename", title)
			}
		})
	})

	t.Run("flow.handback-fences-operator-turns", func(t *testing.T) {
		f := handoffSetup(t, &titleRT{})
		ctx := principalCtx(context.Background())
		seedSession(t, f, "sess-t")
		op := types.Principal{Tenant: "t-a", Subject: "op-1", Scopes: []string{types.ScopeSessionControl}}
		operatorMsg := Message{Role: types.RoleAssistant, Blocks: []types.Block{types.Text{Text: "operator says A"}}}
		if _, err := f.conv.OperatorSend(ctx, "sess-t", op, operatorMsg); err != nil {
			t.Fatalf("operator send: %v", err)
		}
		if err := f.conv.HandBack(ctx, "sess-t", op); err != nil {
			t.Fatalf("handback: %v", err)
		}
		control, err := f.log.Control(ctx, "sess-t")
		if err != nil || control != stores.ControlAgent {
			t.Fatalf("control = %v (%v), want ControlAgent", control, err)
		}
		metas, _, err := f.log.Sessions(ctx, ownerOf(t, f.log.MemorySessionLog, "sess-t"), stores.SessionQuery{Kinds: []stores.SessionKind{stores.SessionPrimary}})
		if err != nil {
			t.Fatalf("sessions: %v", err)
		}
		var operatorOrigin int
		for _, m := range metas {
			if m.ID != "sess-t" {
				continue
			}
			_ = m
		}
		h, err := f.conv.load(ctx, "sess-t")
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		for _, msg := range h.Messages {
			for _, b := range msg.Blocks {
				if b.BlockOrigin().Kind == types.OriginOperator {
					operatorOrigin++
				}
			}
		}
		if operatorOrigin == 0 {
			t.Fatalf("no operator-origin turn in history")
		}
		if len(h.Messages) != 2 {
			t.Fatalf("history rewritten: %d messages, want append-only 2", len(h.Messages))
		}
	})

	t.Run("telemetry.feedback-metric-labels", func(t *testing.T) {
		sink := &fbSink{}
		m, err := stdtelemetry.Build(sink, stdtelemetry.DefaultMetrics, stdtelemetry.WithRelease("r7"), stdtelemetry.WithVariant("b"))
		if err != nil {
			t.Fatalf("build metrics: %v", err)
		}
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		fb := stores.NewMemoryFeedback(
			stores.WithMemoryFeedbackPrincipals(types.PrincipalFrom),
			stores.WithFeedbackSessionOwner(func(_ context.Context, sid string) (types.SessionOwner, error) {
				return ownerOf(t, log, sid), nil
			}))
		stack, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		stack.telemetry = m
		stack.stores.Feedback = fb
		seedFeedbackOwner(t, log, "sess-t", "u-1")
		err = stack.Feedback(types.WithPrincipal(context.Background(), types.Principal{Tenant: "t-a", Subject: "u-1"}), "sess-t", stores.Feedback{
			Target: types.FeedbackTarget{SessionID: "sess-t", MessageID: "m3"},
			Name:   "csat",
			Value:  4,
			Source: types.Explicit,
		})
		if err != nil {
			t.Fatalf("feedback: %v", err)
		}
		rows, ferr := fb.Feedback(types.WithPrincipal(context.Background(), types.Principal{Tenant: "t-a", Subject: "u1", Scopes: []string{types.ScopeSessionRead}}), "sess-t")
		if ferr != nil || len(rows) != 1 || rows[0].Name != "csat" {
			t.Fatalf("feedback rows = %v (%v)", rows, ferr)
		}
		obs := sink.records[len(sink.records)-1]
		if obs.v != 4 {
			t.Fatalf("metric value = %v, want 4", obs.v)
		}
		got := map[string]string{}
		for _, a := range obs.attrs {
			got[a.Key] = a.Value.(string)
		}
		want := map[string]string{"name": "csat", types.KeyRelease: "r7", types.KeyVariant: "b"}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("label %s = %q, want %q", k, got[k], v)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("labels %v, want exactly %v", got, want)
		}
	})
}
