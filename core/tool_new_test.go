package gohan

import (
	"context"
	"testing"
	"time"

	"encoding/json/jsontext"

	"github.com/victorzhuk/gohan/core/types"
)

func TestNewToolExecution(t *testing.T) {
	t.Run("tools.out-passthrough", func(t *testing.T) {
		tool, err := NewTool("list_things", "returns blocks",
			func(ctx context.Context, _ struct{}) ([]types.Block, error) {
				return []types.Block{
					types.Image{MIME: "image/png", Data: []byte{1, 2}},
					types.Text{Text: "hello"},
				}, nil
			})
		if err != nil {
			t.Fatal(err)
		}
		res, err := tool.Call(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != types.Succeeded {
			t.Fatalf("outcome = %v, want Succeeded", res.Outcome)
		}
		if len(res.Content) != 2 {
			t.Fatalf("len(content) = %d, want 2 blocks unchanged", len(res.Content))
		}
		img, ok := res.Content[0].(types.Image)
		if !ok || img.MIME != "image/png" {
			t.Fatalf("content[0] = %#v, want the Image unchanged", res.Content[0])
		}
		txt, ok := res.Content[1].(types.Text)
		if !ok || txt.Text != "hello" {
			t.Fatalf("content[1] = %#v, want the Text unchanged", res.Content[1])
		}
		resTool, err := NewTool("give_result", "returns a ToolResult",
			func(ctx context.Context, _ struct{}) (types.ToolResult, error) {
				return types.ToolResult{
					Content: []types.Block{types.Text{Text: "raw"}},
					Outcome: types.Failed,
					Ref:     "out://1",
				}, nil
			})
		if err != nil {
			t.Fatal(err)
		}
		got, err := resTool.Call(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Outcome != types.Failed || got.Ref != "out://1" || len(got.Content) != 1 {
			t.Fatalf("ToolResult not passed through: %+v", got)
		}
	})

	t.Run("tools.panic-recovered", func(t *testing.T) {
		tool, err := NewTool("charge_card", "panics mid-call",
			func(ctx context.Context, _ struct{}) (string, error) {
				panic("gateway exploded")
			},
			WithEffect(types.SideEffect))
		if err != nil {
			t.Fatal(err)
		}
		set := ToolSetFunc(func(name string) (types.Tool, bool) { return tool, true })
		res, err := CallTool(context.Background(), set, "charge_card", jsontext.Value("{}"))
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != types.Unknown {
			t.Fatalf("outcome = %v, want Unknown", res.Outcome)
		}
		if res.Error == nil || res.Error.Kind != types.OutcomeUnknown {
			t.Fatalf("error = %+v, want OutcomeUnknown", res.Error)
		}
		if res.Error.Message == "" {
			t.Fatal("empty panic message")
		}
	})

	t.Run("tools.default-timeout", func(t *testing.T) {
		// The zero value takes the effect-derived default; an explicit
		// value wins.
		defaults := map[types.Effect]time.Duration{
			types.ReadOnly:   10 * time.Second,
			types.Idempotent: 30 * time.Second,
			types.SideEffect: 60 * time.Second,
		}
		for effect, want := range defaults {
			tool, err := NewTool("timeout_probe", "default timeout",
				func(ctx context.Context, _ struct{}) (string, error) { return "", nil },
				WithEffect(effect))
			if err != nil {
				t.Fatal(err)
			}
			if got := tool.Spec().Timeout; got != want {
				t.Fatalf("%v: timeout = %v, want %v", effect, got, want)
			}
		}
		explicit, err := NewTool("timeout_explicit", "explicit timeout",
			func(ctx context.Context, _ struct{}) (string, error) { return "", nil },
			WithTimeout(1500*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		if got := explicit.Spec().Timeout; got != 1500*time.Millisecond {
			t.Fatalf("explicit timeout = %v, want 1.5s", got)
		}

		// A blocking ReadOnly tool is cancelled by its timeout and the
		// failure classifies as retryable.
		slow, err := NewTool("slow_read", "blocks past its deadline",
			func(ctx context.Context, _ struct{}) (string, error) {
				<-ctx.Done()
				return "", ctx.Err()
			},
			WithTimeout(50*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		set := ToolSetFunc(func(name string) (types.Tool, bool) { return slow, true })
		start := time.Now()
		res, err := CallTool(context.Background(), set, "slow_read", jsontext.Value("{}"))
		if err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("call ran %v, timeout was not applied", elapsed)
		}
		if res.Outcome != types.Failed || res.Error == nil || res.Error.Kind != types.Retryable {
			t.Fatalf("result = %+v, want Failed(Retryable)", res)
		}
	})
}
