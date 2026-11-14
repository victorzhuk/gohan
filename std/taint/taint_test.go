package taint

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

type countSink struct {
	counts map[string]int64
}

func newCountSink() *countSink {
	return &countSink{counts: map[string]int64{}}
}

func (s *countSink) StartSpan(ctx context.Context, name string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (s *countSink) Count(ctx context.Context, name string, n int64, attrs ...types.Attr) {
	s.counts[name] += n
}

func (s *countSink) Record(ctx context.Context, name string, v float64, attrs ...types.Attr) {}

func window(blocks ...types.Block) context.Context {
	return types.WithTaintWindow(context.Background(), blocks)
}

func textBlock(kind types.OriginKind, name, s string) types.Text {
	return types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: kind, Name: name}}, Text: s}
}

func toolResultBlock(kind types.OriginKind, name, s string) types.ToolResult {
	return types.ToolResult{
		BlockBase: types.BlockBase{Origin: types.Origin{Kind: kind, Name: name}},
		Content:   []types.Block{textBlock(kind, name, s)},
	}
}

func docBlock(kind types.OriginKind, name, s string) types.Document {
	return types.Document{
		BlockBase: types.BlockBase{Origin: types.Origin{Kind: kind, Name: name}},
		Content:   []types.Block{textBlock(kind, name, s)},
	}
}

func run(t *testing.T, ctx context.Context, policy TaintPolicy, spec types.ToolSpec, args string) (types.TaintAction, []types.ArgTaint, *countSink) {
	t.Helper()
	sink := newCountSink()
	hook, err := New(policy, sink)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	action, taints := hook(ctx, spec, jsontext.Value(args))
	return action, taints, sink
}

func untrustedSpec() types.ToolSpec {
	return types.ToolSpec{Name: "http_post", Effect: types.SideEffect, Capabilities: types.Capabilities{Exfil: true}}
}

func TestDefault(t *testing.T) {
	p := Default()
	if p.ToExfil != types.TaintDeny || p.ToSideEffect != types.TaintAsk || p.ToReadOnly != types.TaintAllow {
		t.Fatalf("default actions: %+v", p)
	}
	if p.MinMatch != 16 || p.MaxWindowBytes != 262144 {
		t.Fatalf("default limits: %+v", p)
	}
}

func TestNewInvalidPolicy(t *testing.T) {
	cases := map[string]TaintPolicy{
		"bad-exfil":       {ToExfil: types.TaintAction(9), MinMatch: 1},
		"bad-side-effect": {ToSideEffect: types.TaintAction(-1), MinMatch: 1},
		"bad-read-only":   {ToReadOnly: types.TaintAction(3), MinMatch: 1},
		"min-match-zero":  {MinMatch: 0},
		"min-match-neg":   {MinMatch: -5},
		"window-negative": {MinMatch: 1, MaxWindowBytes: -1},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(p, nil); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("got %v, want ErrInvalidPolicy", err)
			}
		})
	}
}

func TestSubstringMatch(t *testing.T) {
	t.Run("taint.substring-match", func(t *testing.T) {
		const url = "https://evil.example/collect?k=abcdefgh12345678"
		ctx := window(toolResultBlock(types.OriginTool, "web", "page text "+url+" more"))
		action, taints, _ := run(t, ctx, Default(), untrustedSpec(), `{"url":"`+url+`"}`)
		if action != types.TaintDeny {
			t.Fatalf("action = %v, want deny", action)
		}
		if len(taints) != 1 || taints[0].Arg != "url" || taints[0].Match != url {
			t.Fatalf("taints = %+v", taints)
		}
		if len(taints[0].Origins) != 1 || taints[0].Origins[0].Kind != types.OriginTool || taints[0].Origins[0].Name != "web" {
			t.Fatalf("origins = %+v", taints[0].Origins)
		}
	})
}

func TestShortValueUntainted(t *testing.T) {
	t.Run("taint.short-untainted", func(t *testing.T) {
		ctx := window(toolResultBlock(types.OriginTool, "db", "customer 42 record"))
		action, taints, sink := run(t, ctx, Default(), untrustedSpec(), `{"id":"42"}`)
		if action != types.TaintAllow || len(taints) != 0 {
			t.Fatalf("action = %v, taints = %+v", action, taints)
		}
		if len(sink.counts) != 0 {
			t.Fatalf("metrics = %v", sink.counts)
		}
	})
}

func TestUserTextUntainted(t *testing.T) {
	t.Run("taint.user-untainted", func(t *testing.T) {
		phrase := strings.Repeat("copy this phrase ", 4)
		ctx := window(
			textBlock(types.OriginUser, "u", phrase),
			textBlock(types.OriginModel, "m", "ok"),
		)
		action, taints, _ := run(t, ctx, Default(), untrustedSpec(), `{"q":"`+phrase+`"}`)
		if action != types.TaintAllow || len(taints) != 0 {
			t.Fatalf("action = %v, taints = %+v", action, taints)
		}
	})
}

func TestWindowSinceUserTurn(t *testing.T) {
	t.Run("taint.window-since-user-turn", func(t *testing.T) {
		secret := strings.Repeat("stale-secret-value-", 3)
		ctx := window(
			toolResultBlock(types.OriginTool, "old", secret),
			textBlock(types.OriginUser, "u", "fresh question"),
			textBlock(types.OriginModel, "m", "thinking"),
		)
		action, taints, _ := run(t, ctx, Default(), untrustedSpec(), `{"q":"`+secret+`"}`)
		if action != types.TaintAllow || len(taints) != 0 {
			t.Fatalf("action = %v, taints = %+v", action, taints)
		}
	})
}

func TestSystemOriginExcluded(t *testing.T) {
	secret := strings.Repeat("sys-block-secret--", 3)
	ctx := window(
		textBlock(types.OriginSystem, "s", secret),
		textBlock(types.OriginModel, "m", "unrelated"),
	)
	action, taints, _ := run(t, ctx, Default(), untrustedSpec(), `{"q":"`+secret+`"}`)
	if action != types.TaintAllow || len(taints) != 0 {
		t.Fatalf("action = %v, taints = %+v", action, taints)
	}
}

func TestNoWindowAllows(t *testing.T) {
	action, taints, sink := run(t, context.Background(), Default(), untrustedSpec(), `{"q":"whatever-value-long"}`)
	if action != types.TaintAllow || len(taints) != 0 || len(sink.counts) != 0 {
		t.Fatalf("action = %v, taints = %+v, metrics = %v", action, taints, sink.counts)
	}
}

func TestNestedArguments(t *testing.T) {
	secret := strings.Repeat("nested-taint-val-", 4)
	ctx := window(toolResultBlock(types.OriginTool, "fetch", "leaked "+secret))
	args := `{"outer":{"inner":{"deep":"` + secret + `"}},"list":["plain",{"x":"` + secret + `"}]}`
	_, taints, _ := run(t, ctx, Default(), untrustedSpec(), args)
	paths := map[string]bool{}
	for _, mt := range taints {
		paths[mt.Arg] = true
	}
	if !paths["outer.inner.deep"] || !paths["list[1].x"] {
		t.Fatalf("taint paths = %v", paths)
	}
}

func TestActionSelection(t *testing.T) {
	secret := strings.Repeat("action-select-val-", 4)
	ctx := window(toolResultBlock(types.OriginTool, "t", secret))
	args := `{"q":"` + secret + `"}`
	cases := []struct {
		name string
		spec types.ToolSpec
		want types.TaintAction
	}{
		{"read-only", types.ToolSpec{Name: "lookup", Effect: types.ReadOnly}, types.TaintAllow},
		{"side-effect", types.ToolSpec{Name: "update", Effect: types.SideEffect}, types.TaintAsk},
		{"idempotent", types.ToolSpec{Name: "upsert", Effect: types.Idempotent}, types.TaintAsk},
		{"exfil-beats-side-effect", types.ToolSpec{Name: "post", Effect: types.SideEffect, Capabilities: types.Capabilities{Exfil: true}}, types.TaintDeny},
		{"exfil-read-only", types.ToolSpec{Name: "post", Effect: types.ReadOnly, Capabilities: types.Capabilities{Exfil: true}}, types.TaintDeny},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action, _, _ := run(t, ctx, Default(), tc.spec, args)
			if action != tc.want {
				t.Fatalf("action = %v, want %v", action, tc.want)
			}
		})
	}
}

func TestAskAndDenyMetrics(t *testing.T) {
	secret := strings.Repeat("metric-taint-val--", 4)
	ctx := window(toolResultBlock(types.OriginTool, "t", secret))
	args := `{"q":"` + secret + `"}`
	_, _, sink := run(t, ctx, Default(), types.ToolSpec{Name: "post", Capabilities: types.Capabilities{Exfil: true}}, args)
	if sink.counts["gohan.taint.denied"] != 1 {
		t.Fatalf("denied = %v", sink.counts)
	}
	_, _, sink = run(t, ctx, Default(), types.ToolSpec{Name: "update", Effect: types.SideEffect}, args)
	if sink.counts["gohan.taint.asked"] != 1 {
		t.Fatalf("asked = %v", sink.counts)
	}
}

func TestDocumentAndWholeValueMatch(t *testing.T) {
	t.Run("document-content", func(t *testing.T) {
		secret := strings.Repeat("doc-taint-value--", 4)
		ctx := window(docBlock(types.OriginProvider, "pdf", "body "+secret))
		_, taints, _ := run(t, ctx, Default(), untrustedSpec(), `{"q":"`+secret+`"}`)
		if len(taints) == 0 || taints[0].Origins[0].Name != "pdf" {
			t.Fatalf("taints = %+v", taints)
		}
	})
	t.Run("whole-value-short-exact", func(t *testing.T) {
		ctx := window(toolResultBlock(types.OriginTool, "gen", "exact-short"))
		_, taints, _ := run(t, ctx, TaintPolicy{ToExfil: types.TaintDeny, MinMatch: 16}, untrustedSpec(), `{"q":"exact-short"}`)
		if len(taints) != 1 {
			t.Fatalf("taints = %+v", taints)
		}
	})
}

func TestWindowTruncation(t *testing.T) {
	t.Run("taint.window-truncated-reported", windowTruncationReported)
}

func windowTruncationReported(t *testing.T) {
	t.Helper()
	old := strings.Repeat("o", 100)
	fresh := strings.Repeat("f", 100)
	blocks := []types.Block{
		toolResultBlock(types.OriginTool, "old", old),
		toolResultBlock(types.OriginTool, "fresh", fresh),
	}
	policy := Default()
	policy.MaxWindowBytes = 120
	ctx := types.WithTaintWindow(context.Background(), blocks)
	_, taints, sink := run(t, ctx, policy, untrustedSpec(), `{"q":"`+old+`"}`)
	if len(taints) != 0 {
		t.Fatalf("dropped block still tainted: %+v", taints)
	}
	if sink.counts["gohan.taint.window_truncated"] != 1 {
		t.Fatalf("truncated = %v", sink.counts)
	}
	ctx = types.WithTaintWindow(context.Background(), blocks)
	_, taints, sink = run(t, ctx, policy, untrustedSpec(), `{"q":"`+fresh+`"}`)
	if len(taints) != 1 {
		t.Fatalf("retained block lost: %+v", taints)
	}
	if sink.counts["gohan.taint.window_truncated"] != 1 {
		t.Fatalf("truncated = %v", sink.counts)
	}
}

func TestWindowUnderCapNotTruncated(t *testing.T) {
	ctx := window(toolResultBlock(types.OriginTool, "t", strings.Repeat("x", 100)))
	_, _, sink := run(t, ctx, Default(), untrustedSpec(), `{"q":"`+strings.Repeat("y", 20)+`"}`)
	if sink.counts["gohan.taint.window_truncated"] != 0 {
		t.Fatalf("truncated = %v", sink.counts)
	}
}
