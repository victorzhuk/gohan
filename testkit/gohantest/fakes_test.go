package gohantest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestFakesAndFaults(t *testing.T) {
	t.Run("tool double defaults", func(t *testing.T) {
		tool := NewFakeTool("lookup")
		spec := tool.Spec()
		if spec.Name != "lookup" || spec.Effect != types.ReadOnly {
			t.Fatalf("default spec = %+v, want name lookup with ReadOnly effect", spec)
		}
		res, err := tool.Call(context.Background(), json.RawMessage(`{"q":"x"}`))
		if err != nil || res.Outcome != types.Succeeded {
			t.Fatalf("default call = (%+v, %v), want succeeded with no error", res, err)
		}
		calls := tool.Calls()
		if len(calls) != 1 || string(calls[0]) != `{"q":"x"}` {
			t.Fatalf("Calls() = %v, want one recorded arg", calls)
		}
	})

	t.Run("tool double error", func(t *testing.T) {
		boom := errors.New("boom")
		tool := NewFakeTool("lookup", WithToolError(boom))
		res, err := tool.Call(context.Background(), nil)
		if !errors.Is(err, boom) {
			t.Fatalf("Call err = %v, want boom", err)
		}
		if res.Outcome != types.Failed || res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("result = %+v, want failed with permanent tool error", res)
		}
	})

	t.Run("clock double defaults", func(t *testing.T) {
		clock := NewFakeClock()
		if got, want := clock.Now(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
			t.Fatalf("default start = %v, want %v", got, want)
		}
		started := NewFakeClock(WithClockStart(time.Unix(100, 0)))
		fired := started.After(time.Second)
		if started.Now().Equal(time.Unix(101, 0)) {
			t.Fatal("After must not move the clock")
		}
		started.Advance(time.Second)
		select {
		case at := <-fired:
			if !at.Equal(time.Unix(101, 0)) {
				t.Fatalf("After fired at %v, want 101s", at)
			}
		default:
			t.Fatal("After did not fire after Advance crossed the deadline")
		}
		if at := <-started.After(0); !at.Equal(started.Now()) {
			t.Fatalf("After(0) = %v, want immediate now", at)
		}
	})

	t.Run("store double defaults", func(t *testing.T) {
		store := NewFakeStore(WithSeed(map[string][]byte{"a": []byte("1")}))
		if v, err := store.Get("a"); err != nil || string(v) != "1" {
			t.Fatalf("Get seeded = (%q, %v), want 1", v, err)
		}
		if err := store.Put("b", []byte("2")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := store.Delete("a"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := store.Get("a"); !errors.Is(err, ErrKeyMissing) {
			t.Fatalf("Get deleted = %v, want ErrKeyMissing", err)
		}
		if store.Calls() != 4 {
			t.Fatalf("Calls() = %d, want 4", store.Calls())
		}
	})

	t.Run("model fault carries class", func(t *testing.T) {
		model := Flaky(NewScriptedModel(scriptProfile(), Text("a"), Text("b")), FaultPlan{
			Every:       2,
			Class:       types.ClassRateLimited,
			AfterChunks: 1,
		})
		if _, err := collect(t, model); err != nil {
			t.Fatalf("first call must pass through, got %v", err)
		}
		chunks, err := collect(t, model)
		if err == nil || len(chunks) != 1 {
			t.Fatalf("faulted call = %d chunks, err %v, want 1 chunk then error", len(chunks), err)
		}
		me, ok := errors.AsType[*types.ModelError](err)
		if !ok || me.Class != types.ClassRateLimited {
			t.Fatalf("err = %v, want ClassRateLimited model error", err)
		}
		if !errors.Is(err, ErrFault) {
			t.Fatalf("err = %v, want ErrFault in chain", err)
		}
	})

	t.Run("tool fault reaches caller", func(t *testing.T) {
		boom := errors.New("boom")
		tool := FaultyTool(NewFakeTool("lookup"), boom)
		res, err := tool.Call(context.Background(), nil)
		if !errors.Is(err, ErrFault) || !errors.Is(err, boom) {
			t.Fatalf("Call err = %v, want ErrFault wrapping boom", err)
		}
		if res.Outcome != types.Failed {
			t.Fatalf("outcome = %v, want Failed", res.Outcome)
		}
	})

	t.Run("store fault reaches caller", func(t *testing.T) {
		boom := errors.New("disk full")
		store := FaultyStore(NewFakeStore(), boom)
		if err := store.Put("a", nil); !errors.Is(err, ErrFault) || !errors.Is(err, boom) {
			t.Fatalf("Put err = %v, want ErrFault wrapping boom", err)
		}
		store.ClearFault()
		if err := store.Put("a", []byte("1")); err != nil {
			t.Fatalf("Put after ClearFault: %v", err)
		}
	})

	t.Run("cancelled context seam", func(t *testing.T) {
		ctx, cancel := CancelledContext()
		defer cancel()
		if err := ctx.Err(); !errors.Is(err, context.Canceled) {
			t.Fatalf("ctx.Err() = %v, want context.Canceled", err)
		}
	})

	t.Run("leak check passes a clean function", func(t *testing.T) {
		LeakCheck(t, func() {
			done := make(chan struct{})
			go func() { done <- struct{}{} }()
			<-done
		})
	})

	t.Run("leak check fails a leaking function", func(t *testing.T) {
		var rep leakStub
		release := make(chan struct{})
		LeakCheck(&rep, func() {
			go func() { <-release }()
		})
		if !rep.failed || len(rep.messages) != 1 {
			t.Fatalf("LeakCheck on a leaking function: failed=%v messages=%v, want one failure", rep.failed, rep.messages)
		}
		LeakCheck(t, func() {
			close(release)
		})
	})
}

// collect drains one model call, returning the chunks and the first error.
func collect(t *testing.T, m types.Model) ([]types.ModelChunk, error) {
	t.Helper()
	var (
		chunks []types.ModelChunk
		err    error
	)
	for chunk, callErr := range m.Generate(context.Background(), types.ModelRequest{}) {
		if callErr != nil && err == nil {
			err = callErr
			break
		}
		chunks = append(chunks, chunk)
	}
	return chunks, err
}
