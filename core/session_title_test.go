package gohan

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"iter"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func titleLog() *stores.MemorySessionLog {
	return stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
}

func sessionMeta(t *testing.T, log *stores.MemorySessionLog, sid string) stores.SessionMeta {
	t.Helper()
	ctx := principalCtx(context.Background())
	owner, err := log.Owner(ctx, sid)
	if err != nil {
		t.Fatalf("owner %s: %v", sid, err)
	}
	metas, _, err := log.Sessions(ctx, owner, stores.SessionQuery{Kinds: []stores.SessionKind{stores.SessionPrimary, stores.SessionFork}})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	for _, m := range metas {
		if m.ID == sid {
			return m
		}
	}
	t.Fatalf("session %s not indexed", sid)
	return stores.SessionMeta{}
}

func titleModel(calls *int) func(string) (RecipeCall, error) {
	return func(string) (RecipeCall, error) {
		*calls++
		n := *calls
		return RecipeCall{Call: func(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			return func(yield func(types.ModelChunk, error) bool) {
				yield(types.ModelChunk{Kind: types.DeltaText, Delta: fmt.Sprintf("%q", fmt.Sprintf("Title %d", n))}, nil)
			}
		}}, nil
	}
}

func titleRun(t *testing.T, log *stores.MemorySessionLog, conv *conversation, sid, want string) {
	t.Helper()
	conv.maybeTitle(principalCtx(context.Background()), sid)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if sessionMeta(t, log, sid).Title == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("title = %q, want %q", sessionMeta(t, log, sid).Title, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSessionTitleOnce(t *testing.T) {
	t.Run("flow.title-generated-after-first-done", func(t *testing.T) {
		log := titleLog()
		conv := &conversation{log: log, titleProfile: "cheap"}
		var calls int
		conv.recipeModel = titleModel(&calls)
		ctx := principalCtx(context.Background())
		if _, err := log.Append(ctx, "s1", 0, userMsg("hello there")); err != nil {
			t.Fatalf("append: %v", err)
		}
		titleRun(t, log, conv, "s1", "Title 1")
		meta := sessionMeta(t, log, "s1")
		if !meta.TitleLocked {
			t.Fatalf("generated title left TitleLocked = false, want the write to lock it")
		}
		if pending, err := conv.titlePending(ctx, "s1"); err != nil || pending {
			t.Fatalf("titlePending after a title = (%v, %v), want false", pending, err)
		}
		conv.maybeTitle(ctx, "s1")
		time.Sleep(20 * time.Millisecond)
		if calls != 1 {
			t.Fatalf("title model called %d times, want the one run", calls)
		}
		if got := sessionMeta(t, log, "s1").Title; got != "Title 1" {
			t.Fatalf("title = %q, want the first title kept", got)
		}
	})

	t.Run("flow.title-locked-by-rename", func(t *testing.T) {
		log := titleLog()
		conv := &conversation{log: log, titleProfile: "cheap"}
		var calls int
		conv.recipeModel = titleModel(&calls)
		ctx := principalCtx(context.Background())
		if _, err := log.Append(ctx, "s1", 0, userMsg("hello there")); err != nil {
			t.Fatalf("append: %v", err)
		}
		renamed := "My Own Name"
		if err := log.UpdateSession(ctx, "s1", stores.SessionPatch{Title: &renamed}); err != nil {
			t.Fatalf("rename: %v", err)
		}
		if pending, err := conv.titlePending(ctx, "s1"); err != nil || pending {
			t.Fatalf("titlePending after a rename = (%v, %v), want false", pending, err)
		}
		conv.maybeTitle(ctx, "s1")
		time.Sleep(20 * time.Millisecond)
		if calls != 0 {
			t.Fatalf("title model called %d times after a rename, want 0", calls)
		}
		if got := sessionMeta(t, log, "s1").Title; got != renamed {
			t.Fatalf("title = %q, want the user's rename", got)
		}
	})
}

func TestSessionTitleFork(t *testing.T) {
	log := titleLog()
	conv := &conversation{log: log, titleProfile: "cheap"}
	var calls int
	conv.recipeModel = titleModel(&calls)
	ctx := principalCtx(context.Background())
	if _, err := log.Append(ctx, "parent", 0, userMsg("hello there")); err != nil {
		t.Fatalf("append: %v", err)
	}
	parentTitle := "Quarterly Refund Questions"
	if err := log.UpdateSession(ctx, "parent", stores.SessionPatch{Title: &parentTitle}); err != nil {
		t.Fatalf("title parent: %v", err)
	}
	h, err := log.Load(ctx, "parent")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	forker, ok := any(log).(stores.SessionForker)
	if !ok {
		t.Fatal("session log does not satisfy SessionForker")
	}
	child, err := forker.Fork(ctx, "parent", h.Messages[0].ID)
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	want := parentTitle + " (edited)"
	meta := sessionMeta(t, log, child)
	if meta.Kind != stores.SessionFork {
		t.Fatalf("fork kind = %v, want SessionFork", meta.Kind)
	}
	if meta.Title != want {
		t.Fatalf("fork title = %q, want %q", meta.Title, want)
	}
	if pending, err := conv.titlePending(ctx, child); err != nil || pending {
		t.Fatalf("titlePending for a fork = (%v, %v), want false: a fork is not titled again", pending, err)
	}
	conv.maybeTitle(ctx, child)
	time.Sleep(20 * time.Millisecond)
	if calls != 0 {
		t.Fatalf("title model called %d times for a fork, want 0", calls)
	}
	if got := sessionMeta(t, log, child).Title; got != want {
		t.Fatalf("fork title = %q, want %q kept", got, want)
	}
}

type titleProfileModel struct {
	profile types.ModelProfile
}

func (m *titleProfileModel) Profile() types.ModelProfile { return m.profile }

func (m *titleProfileModel) Generate(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return nil
}

func TestTitleProfileOption(t *testing.T) {
	big := &titleProfileModel{profile: types.ModelProfile{Name: "big", Pricing: types.Pricing{Input: 2}}}
	small := &titleProfileModel{profile: types.ModelProfile{Name: "small", Pricing: types.Pricing{Input: 1}}}
	stack, err := Build(WithModels(big, small))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := stack.TitleProfile(); got != "small" {
		t.Fatalf("default title profile = %q, want the cheapest profile small", got)
	}
	stack, err = Build(WithModels(big, small), WithTitleProfile("big"))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := stack.TitleProfile(); got != "big" {
		t.Fatalf("explicit title profile = %q, want big", got)
	}
}

func TestRequestHumanTool(t *testing.T) {
	tool, err := NewRequestHumanTool()
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}
	if tool.Spec().Name != RequestHumanName {
		t.Fatalf("name = %q, want %q", tool.Spec().Name, RequestHumanName)
	}
	if err := CheckToolCollisions([]RegisteredTool{
		{Spec: types.ToolSpec{Name: RequestHumanName}, Source: "user"},
	}); err == nil {
		t.Fatal("user registration under request_human returned no collision error")
	}
	res, err := CallTool(context.Background(),
		ToolSetFunc(func(string) (types.Tool, bool) { return tool, true }),
		RequestHumanName,
		jsontext.Value(`{"reason":"needs a human approval","skill":"refunds","fields":{"order":"o-1"}}`),
	)
	if res.Outcome != 0 || len(res.Content) != 0 {
		t.Fatalf("result = %+v, want the zero ToolResult", res)
	}
	he, ok := errors.AsType[*types.HandoffError](err)
	if !ok {
		t.Fatalf("error %T, want *types.HandoffError", err)
	}
	if he.Context.Reason != "needs a human approval" || he.Context.Skill != "refunds" || he.Context.Fields["order"] != "o-1" {
		t.Fatalf("context = %+v, want the model's reason carried", he.Context)
	}
}
