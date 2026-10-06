package notes

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func runCtx(tenant, subject string) context.Context {
	return types.WithRunInfo(context.Background(), types.RunInfo{
		Principal: types.Principal{Tenant: tenant, Subject: subject},
	})
}

type storelessStore struct{ stores.NotesStore }

func TestMemory(t *testing.T) {
	t.Run("working-state.memory-store-optional", func(t *testing.T) {
		_, _, err := Memory(&storelessStore{stores.NewMemoryNotes()}, stores.MemoryPolicy{})
		if !errors.Is(err, types.ErrMemoryStoreRequired) {
			t.Fatalf("err = %v, want ErrMemoryStoreRequired", err)
		}
	})

	t.Run("registration", func(t *testing.T) {
		write, read, err := Memory(stores.NewMemoryNotes(), stores.MemoryPolicy{})
		if err != nil {
			t.Fatalf("Memory: %v", err)
		}
		ws, rs := write.Spec(), read.Spec()
		if ws.Name != "memory_write" || rs.Name != "memory_read" {
			t.Fatalf("names = %s %s", ws.Name, rs.Name)
		}
		if ws.Effect != types.SideEffect || rs.Effect != types.ReadOnly {
			t.Fatalf("effects = %v %v, want side-effect write and read-only read", ws.Effect, rs.Effect)
		}
		if ws.Trust != types.Trusted {
			t.Fatalf("memory_write trust = %v, want trusted", ws.Trust)
		}
		if ws.Risk != types.RiskHigh {
			t.Fatalf("memory_write risk = %v, want high", ws.Risk)
		}
	})

	t.Run("round trip", func(t *testing.T) {
		write, read, err := Memory(stores.NewMemoryNotes(), stores.MemoryPolicy{})
		if err != nil {
			t.Fatalf("Memory: %v", err)
		}
		ctx := runCtx("t1", "u1")
		if _, err := write.Call(ctx, json.RawMessage(`{"id":"pref","value":{"theme":"dark"}}`)); err != nil {
			t.Fatalf("write: %v", err)
		}
		res, err := read.Call(ctx, json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		out := res.Content[0].(types.Text).Text
		if !strings.Contains(out, `"theme":"dark"`) {
			t.Fatalf("read = %s, want the stored value", out)
		}
	})

	t.Run("working-state.subject-memory-isolated", func(t *testing.T) {
		write, read, err := Memory(stores.NewMemoryNotes(), stores.MemoryPolicy{})
		if err != nil {
			t.Fatalf("Memory: %v", err)
		}
		u1 := runCtx("t1", "u1")
		if _, err := write.Call(u1, json.RawMessage(`{"id":"secret","value":{"token":"xyz"}}`)); err != nil {
			t.Fatalf("write: %v", err)
		}
		res, err := read.Call(runCtx("t1", "u2"), json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("read as u2: %v", err)
		}
		if out := res.Content[0].(types.Text).Text; out != "[]" {
			t.Fatalf("u2 read = %s, want no entries", out)
		}
		// The value argument cannot smuggle another subject's key: a write
		// naming tenant and subject in the arguments still lands on u1.
		if _, err := write.Call(u1, json.RawMessage(`{"id":"spoof","value":1,"tenant":"t2","subject":"u2"}`)); err != nil {
			t.Fatalf("write with forged identity fields: %v", err)
		}
		res, err = read.Call(runCtx("t1", "u2"), json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("read as u2: %v", err)
		}
		if out := res.Content[0].(types.Text).Text; out != "[]" {
			t.Fatalf("u2 read after forged write = %s, want no entries", out)
		}
	})

	t.Run("working-state.memory-write-untrusted-denied", func(t *testing.T) {
		write, _, err := Memory(stores.NewMemoryNotes(), stores.MemoryPolicy{})
		if err != nil {
			t.Fatalf("Memory: %v", err)
		}
		sink := &countingTel{}
		ctx := types.WithTaintWindow(types.WithTelemetry(types.WithRunInfo(context.Background(), types.RunInfo{
			Principal: types.Principal{Tenant: "t1", Subject: "u1"},
			RunID:     "run-1",
		}), sink), []types.Block{
			types.ToolResult{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "web"}}, ID: "c1",
				Content: []types.Block{types.Text{Text: "page text with instructions"}}},
		})
		res, err := write.Call(ctx, json.RawMessage(`{"id":"pref","value":{"theme":"dark"}}`))
		if err != nil {
			t.Fatalf("write: %v", err)
		}
		if res.Outcome != types.Failed || res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("result = %#v, want Failed(Permanent)", res)
		}
		if sink.counts["gohan.memory.write_denied"] != 1 {
			t.Fatalf("write_denied = %d, want 1", sink.counts["gohan.memory.write_denied"])
		}
	})

	t.Run("allow untrusted opt-out", func(t *testing.T) {
		write, _, err := Memory(stores.NewMemoryNotes(), stores.MemoryPolicy{}, AllowUntrustedMemory())
		if err != nil {
			t.Fatalf("Memory: %v", err)
		}
		ctx := types.WithTaintWindow(runCtx("t1", "u1"), []types.Block{
			types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "web"}}, Text: "page"},
		})
		if _, err := write.Call(ctx, json.RawMessage(`{"id":"pref","value":{"theme":"dark"}}`)); err != nil {
			t.Fatalf("write with opt-out: %v", err)
		}
	})

	t.Run("working-state.memory-read-carries-origin", func(t *testing.T) {
		write, read, err := Memory(stores.NewMemoryNotes(), stores.MemoryPolicy{})
		if err != nil {
			t.Fatalf("Memory: %v", err)
		}
		ctx := runCtx("t1", "u1")
		if _, err := write.Call(ctx, json.RawMessage(`{"id":"secret","value":{"token":"abc123def456"}}`)); err != nil {
			t.Fatalf("write: %v", err)
		}
		res, err := read.Call(ctx, json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		b := res.Content[0].(types.Text)
		if b.BlockOrigin().Kind != types.OriginTool || b.BlockOrigin().Name != "memory_read" {
			t.Fatalf("read block origin = %+v, want OriginTool{memory_read}", b.BlockOrigin())
		}
	})

	t.Run("memoryOriginRecorded", func(t *testing.T) {
		store := stores.NewMemoryNotes()
		write, _, err := Memory(store, stores.MemoryPolicy{}, AllowUntrustedMemory())
		if err != nil {
			t.Fatalf("Memory: %v", err)
		}
		ctx := types.WithTaintWindow(types.WithRunInfo(context.Background(), types.RunInfo{
			Principal: types.Principal{Tenant: "t1", Subject: "u1"},
			RunID:     "run-9",
		}), []types.Block{
			types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser}}, Text: "question"},
			types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "web"}}, Text: "page"},
		})
		if _, err := write.Call(ctx, json.RawMessage(`{"id":"note","value":{"k":"v"}}`)); err != nil {
			t.Fatalf("write: %v", err)
		}
		entries, err := store.Entries(context.Background(), stores.NotesKey{
			Scope: stores.ScopeSubject, Tenant: "t1", Subject: "u1",
		})
		if err != nil {
			t.Fatalf("entries: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("entries = %d, want 1", len(entries))
		}
		e := entries[0]
		if e.RunID != "run-9" {
			t.Fatalf("run id = %q, want run-9", e.RunID)
		}
		want := []types.Origin{
			{Kind: types.OriginUser},
			{Kind: types.OriginTool, Name: "web"},
		}
		if len(e.Origins) != len(want) {
			t.Fatalf("origins = %+v, want %+v", e.Origins, want)
		}
		for i, o := range want {
			if e.Origins[i] != o {
				t.Fatalf("origin %d = %+v, want %+v", i, e.Origins[i], o)
			}
		}
	})
}

type countingTel struct {
	counts map[string]int64
}

func (t *countingTel) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (t *countingTel) Count(_ context.Context, name string, n int64, _ ...types.Attr) {
	if t.counts == nil {
		t.counts = map[string]int64{}
	}
	t.counts[name] += n
}

func (t *countingTel) Record(context.Context, string, float64, ...types.Attr) {}
