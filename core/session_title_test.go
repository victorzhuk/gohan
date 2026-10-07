package gohan

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"iter"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

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
