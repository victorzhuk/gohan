package stores

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type memoryTelemetry struct {
	metrics []memoryMetric
}

type memoryMetric struct {
	name  string
	count int64
	attrs []types.Attr
}

func (t *memoryTelemetry) Count(_ context.Context, name string, n int64, attrs ...types.Attr) {
	t.metrics = append(t.metrics, memoryMetric{name: name, count: n, attrs: attrs})
}
func (*memoryTelemetry) Record(context.Context, string, float64, ...types.Attr) {}
func (t *memoryTelemetry) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

const memoryShape = `{"type":"object","additionalProperties":false,"required":["sku"],"properties":{"sku":{"type":"string","enum":["A","B"]},"n":{"type":"integer","min":1,"max":9}}}`

func memoryKey() NotesKey {
	return NotesKey{Scope: ScopeSubject, Tenant: "t1", Subject: "u1"}
}

func memoryEntry(value string) MemoryEntry {
	return MemoryEntry{ID: "e-" + value, Value: json.RawMessage(value)}
}

func TestMemoryPut(t *testing.T) {
	ctx := context.Background()

	t.Run("working-state.memory-write-schema", func(t *testing.T) {
		s := NewMemoryNotes(WithMemoryNotesPolicy(MemoryPolicy{Schema: json.RawMessage(memoryShape)}))
		if err := s.Put(ctx, memoryKey(), memoryEntry(`"free text"`)); !errors.Is(err, types.ErrMemoryWriteInvalid) {
			t.Fatalf("free text err = %v, want ErrMemoryWriteInvalid", err)
		}
		entries, err := s.Entries(ctx, memoryKey())
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("stored %d entries after refusal, want 0", len(entries))
		}
		if err := s.Put(ctx, memoryKey(), memoryEntry(`{"sku":"A","n":3}`)); err != nil {
			t.Fatalf("valid value err = %v", err)
		}
	})

	t.Run("memoryWriteSchemaNested", func(t *testing.T) {
		s := NewMemoryNotes(WithMemoryNotesPolicy(MemoryPolicy{Schema: json.RawMessage(memoryShape)}))
		if err := s.Put(ctx, memoryKey(), memoryEntry(`{"sku":"Z"}`)); !errors.Is(err, types.ErrMemoryWriteInvalid) {
			t.Fatalf("enum violation err = %v, want ErrMemoryWriteInvalid", err)
		}
		if err := s.Put(ctx, memoryKey(), memoryEntry(`{"sku":"A","n":0}`)); !errors.Is(err, types.ErrMemoryWriteInvalid) {
			t.Fatalf("bound violation err = %v, want ErrMemoryWriteInvalid", err)
		}
		if err := s.Put(ctx, memoryKey(), memoryEntry(`{"sku":"A","extra":1}`)); !errors.Is(err, types.ErrMemoryWriteInvalid) {
			t.Fatalf("undeclared property err = %v, want ErrMemoryWriteInvalid", err)
		}
	})

	t.Run("working-state.memory-ttl-eviction.stamp-and-ttl", func(t *testing.T) {
		now := time.Unix(1_800_000_000, 0)
		clock := now
		s := NewMemoryNotes(
			WithMemoryNotesClock(func() time.Time { return clock }),
			WithMemoryNotesPolicy(MemoryPolicy{TTL: time.Hour}),
		)
		if err := s.Put(ctx, memoryKey(), memoryEntry(`"first"`)); err != nil {
			t.Fatal(err)
		}
		entries, err := s.Entries(ctx, memoryKey())
		if err != nil {
			t.Fatal(err)
		}
		if !entries[0].StoredAt.Equal(now) {
			t.Fatalf("StoredAt = %v, want store clock %v", entries[0].StoredAt, now)
		}
		clock = now.Add(2 * time.Hour)
		if err := s.Put(ctx, memoryKey(), memoryEntry(`"second"`)); err != nil {
			t.Fatal(err)
		}
		entries, _ = s.Entries(ctx, memoryKey())
		if len(entries) != 1 || entries[0].ID != "e-\"second\"" {
			t.Fatalf("entries after ttl = %#v, want only the second", entries)
		}
	})

	t.Run("working-state.memory-ttl-eviction.max-entries", func(t *testing.T) {
		now := time.Unix(1_800_000_000, 0)
		clock := now
		s := NewMemoryNotes(
			WithMemoryNotesClock(func() time.Time {
				clock = clock.Add(time.Second)
				return clock
			}),
			WithMemoryNotesPolicy(MemoryPolicy{MaxEntries: 2}),
		)
		for _, v := range []string{`"a"`, `"b"`, `"c"`} {
			if err := s.Put(ctx, memoryKey(), memoryEntry(v)); err != nil {
				t.Fatal(err)
			}
		}
		entries, err := s.Entries(ctx, memoryKey())
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Fatalf("kept %d entries, want 2", len(entries))
		}
		if entries[0].ID != "e-\"b\"" || entries[1].ID != "e-\"c\"" {
			t.Fatalf("oldest not evicted: %#v", entries)
		}
		if entries[0].Version != 2 || entries[1].Version != 3 {
			t.Fatalf("survivor versions = %d,%d, want 2,3", entries[0].Version, entries[1].Version)
		}
	})

	t.Run("working-state.memory-ttl-eviction.metric", func(t *testing.T) {
		now := time.Unix(1_800_000_000, 0)
		clock := now
		tel := &memoryTelemetry{}
		s := NewMemoryNotes(
			WithMemoryNotesClock(func() time.Time {
				clock = clock.Add(time.Second)
				return clock
			}),
			WithMemoryNotesPolicy(MemoryPolicy{MaxEntries: 1}),
		)
		tctx := types.WithTelemetry(ctx, tel)
		for _, v := range []string{`"a"`, `"b"`} {
			if err := s.Put(tctx, memoryKey(), memoryEntry(v)); err != nil {
				t.Fatal(err)
			}
		}
		if len(tel.metrics) != 1 {
			t.Fatalf("emitted %d metrics, want 1: %#v", len(tel.metrics), tel.metrics)
		}
		m := tel.metrics[0]
		if m.name != "gohan.memory.evicted" || m.count != 1 {
			t.Fatalf("metric = %#v, want gohan.memory.evicted at 1", m)
		}
		if len(m.attrs) != 1 || m.attrs[0] != types.String("tenant", "t1") {
			t.Fatalf("attrs = %#v, want tenant t1", m.attrs)
		}
	})

	t.Run("working-state.memory-ttl-eviction", func(t *testing.T) {
		base := time.Unix(1_800_000_000, 0)
		clock := base
		tel := &memoryTelemetry{}
		s := NewMemoryNotes(
			WithMemoryNotesClock(func() time.Time { return clock }),
			WithMemoryNotesPolicy(MemoryPolicy{TTL: time.Hour, MaxEntries: 3}),
		)
		tctx := types.WithTelemetry(ctx, tel)
		put := func(v string) {
			if err := s.Put(tctx, memoryKey(), memoryEntry(v)); err != nil {
				t.Fatal(err)
			}
		}
		put(`"a"`)
		clock = base.Add(time.Second)
		put(`"b"`)
		clock = base.Add(2 * time.Hour)
		put(`"c"`)
		clock = clock.Add(time.Second)
		put(`"d"`)
		clock = clock.Add(time.Second)
		put(`"e"`)
		entries, err := s.Entries(ctx, memoryKey())
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 3 {
			t.Fatalf("kept %d entries, want 3: %#v", len(entries), entries)
		}
		for i, want := range []struct {
			id      string
			version int64
		}{
			{"e-\"c\"", 1}, {"e-\"d\"", 2}, {"e-\"e\"", 3},
		} {
			if entries[i].ID != want.id || entries[i].Version != want.version {
				t.Fatalf("entries[%d] = {%v v%d}, want %s v%d",
					i, entries[i].ID, entries[i].Version, want.id, want.version)
			}
		}
		if len(tel.metrics) != 2 {
			t.Fatalf("emitted %d metrics, want 2: %#v", len(tel.metrics), tel.metrics)
		}
		for _, m := range tel.metrics {
			if m.name != "gohan.memory.evicted" || m.count != 1 {
				t.Fatalf("metric = %#v, want gohan.memory.evicted at 1", m)
			}
			if len(m.attrs) != 1 || m.attrs[0] != types.String("tenant", "t1") {
				t.Fatalf("attrs = %#v, want tenant t1", m.attrs)
			}
		}
	})

	t.Run("working-state.memory-ttl-eviction.zero-stamp-expires", func(t *testing.T) {
		now := time.Unix(1_800_000_000, 0)
		s := NewMemoryNotes(WithMemoryNotesClock(func() time.Time { return now }))
		if s.memory == nil {
			s.memory = make(map[NotesKey][]memoryRecord)
		}
		s.memory[memoryKey()] = []memoryRecord{{entry: memoryEntry(`"old"`), version: 1}}
		if err := s.Put(ctx, memoryKey(), memoryEntry(`"new"`)); err != nil {
			t.Fatal(err)
		}
		entries, _ := s.Entries(ctx, memoryKey())
		if len(entries) != 1 || entries[0].ID != "e-\"new\"" {
			t.Fatalf("entries = %#v, want the zero-stamp record evicted", entries)
		}
	})

	t.Run("working-state.memory-write-schema.unset", func(t *testing.T) {
		s := NewMemoryNotes()
		if err := s.Put(ctx, memoryKey(), memoryEntry(`"free text"`)); err != nil {
			t.Fatalf("no policy err = %v, want nil", err)
		}
	})
}
