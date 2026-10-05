package gohantest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func scriptProfile() types.ModelProfile {
	return types.ModelProfile{
		Name:    "scripted",
		Version: "v1",
		Caps: types.Caps{
			Tools:       true,
			Streaming:   true,
			Temperature: true,
		},
	}
}

func drain(t *testing.T, m types.Model, req types.ModelRequest) ([]types.ModelChunk, error) {
	t.Helper()
	var chunks []types.ModelChunk
	for c, err := range m.Generate(context.Background(), req) {
		if err != nil {
			return chunks, err
		}
		chunks = append(chunks, c)
	}
	return chunks, nil
}

func TestScriptedModel(t *testing.T) {
	t.Run("turns-replay-in-order", func(t *testing.T) {
		m := NewScriptedModel(scriptProfile(),
			Text("hello"),
			ToolCall("lookup", map[string]string{"q": "x"}),
			Refuse(),
		)
		req := types.ModelRequest{}

		chunks, err := drain(t, m, req)
		if err != nil {
			t.Fatalf("turn 1: %v", err)
		}
		if len(chunks) != 2 || chunks[0].Delta != "hello" || chunks[1].Finish != types.FinishStop {
			t.Fatalf("turn 1 chunks = %+v, want text then stop", chunks)
		}

		chunks, err = drain(t, m, req)
		if err != nil {
			t.Fatalf("turn 2: %v", err)
		}
		if len(chunks) != 2 || chunks[0].ToolUse == nil || chunks[0].ToolUse.Name != "lookup" ||
			string(chunks[0].ToolUse.Args) != `{"q":"x"}` || chunks[1].Finish != types.FinishToolUse {
			t.Fatalf("turn 2 chunks = %+v, want complete tool_use", chunks)
		}

		chunks, err = drain(t, m, req)
		if err != nil {
			t.Fatalf("turn 3: %v", err)
		}
		if len(chunks) != 1 || chunks[0].Finish != types.FinishRefusal {
			t.Fatalf("turn 3 chunks = %+v, want refusal", chunks)
		}
	})

	t.Run("request-assertion-fires-on-mismatch", func(t *testing.T) {
		assertTurn := func() Turn {
			return Text("hi").WithAssert(func(req types.ModelRequest) error {
				if len(req.Messages) != 1 {
					return errWantOneMessage
				}
				return nil
			})
		}
		m := NewScriptedModel(scriptProfile(), assertTurn(), assertTurn())

		chunks, err := drain(t, m, types.ModelRequest{Messages: []types.Message{{}}})
		if err != nil {
			t.Fatalf("matching request rejected: %v", err)
		}
		if len(chunks) != 2 {
			t.Fatalf("chunks = %d, want 2", len(chunks))
		}

		_, err = drain(t, m, types.ModelRequest{})
		var me *types.ModelError
		if !errors.As(err, &me) || me.Class != types.ClassPermanent {
			t.Fatalf("err = %v, want *ModelError ClassPermanent", err)
		}
		if !errors.Is(err, errWantOneMessage) {
			t.Fatalf("err = %v, want wrapped assertion error", err)
		}
	})

	t.Run("usage-reaches-final-chunk", func(t *testing.T) {
		m := NewScriptedModel(scriptProfile(), Text("a"))
		chunks, err := drain(t, m, types.ModelRequest{})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		u := chunks[len(chunks)-1].Usage
		if u == nil || u.ModelVersion != "v1" || !u.Estimated || u.InputTokens == 0 || u.OutputTokens == 0 {
			t.Fatalf("default usage = %+v, want profile-derived estimate", u)
		}

		m = NewScriptedModel(scriptProfile(), Text("a").WithUsage(types.Usage{
			InputTokens:       12,
			CachedInputTokens: 3,
			OutputTokens:      7,
			CacheWriteTokens:  1,
			ModelVersion:      "v1-served",
		}))
		chunks, err = drain(t, m, types.ModelRequest{})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		u = chunks[len(chunks)-1].Usage
		if u == nil || u.InputTokens != 12 || u.CachedInputTokens != 3 ||
			u.OutputTokens != 7 || u.CacheWriteTokens != 1 || u.Estimated {
			t.Fatalf("pinned usage = %+v, want the WithUsage values", u)
		}
	})

	t.Run("timed-deltas-recorded-not-waited", func(t *testing.T) {
		m := NewScriptedModel(scriptProfile(),
			Text("a").WithDelay(50*time.Millisecond).WithUsage(types.Usage{InputTokens: 1, OutputTokens: 1, ModelVersion: "v1"}),
		)
		start := time.Now()
		chunks, err := drain(t, m, types.ModelRequest{})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("Generate took %v, want no wall-clock wait", elapsed)
		}
		timings := m.Timings()
		if len(timings) != 1 || len(timings[0]) != len(chunks) {
			t.Fatalf("timings = %v, want one duration per chunk", timings)
		}
		for i, d := range timings[0] {
			if d != 50*time.Millisecond {
				t.Fatalf("timing[%d] = %v, want 50ms", i, d)
			}
		}
	})

	t.Run("error-classes", func(t *testing.T) {
		classes := []types.ErrorClass{
			types.ClassRateLimited,
			types.ClassTransient,
			types.ClassContextOverflow,
			types.ClassContentPolicy,
			types.ClassDeprecated,
			types.ClassAuth,
			types.ClassPermanent,
			types.ClassVersionDrift,
		}
		turns := make([]Turn, len(classes))
		for i, class := range classes {
			turns[i] = Fail(class)
		}
		m := NewScriptedModel(scriptProfile(), turns...)
		for _, class := range classes {
			_, err := drain(t, m, types.ModelRequest{})
			var me *types.ModelError
			if !errors.As(err, &me) || me.Class != class {
				t.Fatalf("err = %v, want *ModelError Class %d", err, class)
			}
		}
	})

	t.Run("no-more-turns", func(t *testing.T) {
		m := NewScriptedModel(scriptProfile(), Text("only"))
		if _, err := drain(t, m, types.ModelRequest{}); err != nil {
			t.Fatalf("turn 1: %v", err)
		}
		_, err := drain(t, m, types.ModelRequest{})
		var me *types.ModelError
		if !errors.As(err, &me) || me.Class != types.ClassPermanent {
			t.Fatalf("err = %v, want *ModelError ClassPermanent", err)
		}
	})

	t.Run("consumer-may-stop-reading", func(t *testing.T) {
		m := NewScriptedModel(scriptProfile(), Text("a"), Text("b"))
		n := 0
		for range m.Generate(context.Background(), types.ModelRequest{}) {
			n++
			break
		}
		if n != 1 {
			t.Fatalf("chunks before break = %d, want 1", n)
		}
		chunks, err := drain(t, m, types.ModelRequest{})
		if err != nil || len(chunks) != 2 {
			t.Fatalf("second call = %d chunks, err %v; the break must not disturb the next turn", len(chunks), err)
		}
	})

	t.Run("requests-are-recorded", func(t *testing.T) {
		m := NewScriptedModel(scriptProfile(), Text("a"), Text("b"))
		req := types.ModelRequest{Options: types.ModelOptions{MaxTokens: 42}}
		if _, err := drain(t, m, req); err != nil {
			t.Fatalf("call 1: %v", err)
		}
		if _, err := drain(t, m, req); err != nil {
			t.Fatalf("call 2: %v", err)
		}
		got := m.Requests()
		if len(got) != 2 || got[0].Options.MaxTokens != 42 || got[1].Options.MaxTokens != 42 {
			t.Fatalf("Requests() = %+v, want both recorded calls", got)
		}
	})
}

var errWantOneMessage = errors.New("want exactly one message")
