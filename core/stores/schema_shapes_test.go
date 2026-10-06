package stores

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

// mapSuffix mutates a top-level JSON string field for the upcaster test
// chain.
func mapSuffix(field string) func([]byte, string) ([]byte, error) {
	return func(raw []byte, suffix string) ([]byte, error) {
		var record map[string]any
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, err
		}
		value, ok := record[field].(string)
		if !ok {
			return nil, fmt.Errorf("field %q is %T, want a string", field, record[field])
		}
		record[field] = value + suffix
		return json.Marshal(record)
	}
}

// schemaUpcasters registers pure upcasters for shape from version 1 through
// 3; each step applies step to the record.
func schemaUpcasters(t *testing.T, reg *SchemaRegistry, shape string, step func([]byte, string) ([]byte, error)) {
	t.Helper()
	for version, suffix := range map[SchemaVersion]string{1: "-v2", 2: "-v3"} {
		version, suffix := version, suffix
		if err := reg.Register(shape, version, func(raw []byte) ([]byte, error) {
			return step(raw, suffix)
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// schemaUpcastersIncomplete registers only the first link, so the chain stops
// short of the registry's current version.
func schemaUpcastersIncomplete(t *testing.T, reg *SchemaRegistry, shape string, step func([]byte, string) ([]byte, error)) {
	t.Helper()
	if err := reg.Register(shape, 1, func(raw []byte) ([]byte, error) {
		return step(raw, "-v2")
	}); err != nil {
		t.Fatal(err)
	}
}

func noopUpcaster(raw []byte, _ string) ([]byte, error) { return raw, nil }

func schemaPrincipal(ctx context.Context) (types.Principal, bool) {
	return types.Principal{Tenant: "t", Subject: "u"}, true
}

// seedSessionState writes the session's shared state and rewrites the
// stored envelope bytes as a version 1 envelope carrying raw. It returns
// the stored bytes.
func seedSessionState(t *testing.T, s *MemorySessionLog, sessionID, raw string) []byte {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.sessions[sessionID]
	rec.state.raw, _ = json.Marshal(RecordEnvelope{Shape: "State", SchemaVersion: 1, Payload: json.RawMessage(raw)})
	return append([]byte(nil), rec.state.raw...)
}

// mapSuffixEvent mutates a string field inside the event record's payload
// for the upcaster test chain.
func mapSuffixEvent(field string) func([]byte, string) ([]byte, error) {
	return func(raw []byte, suffix string) ([]byte, error) {
		var rec eventRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, err
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Payload, &payload); err != nil {
			return nil, err
		}
		value, ok := payload[field].(string)
		if !ok {
			return nil, fmt.Errorf("field %q is %T, want a string", field, payload[field])
		}
		payload[field] = value + suffix
		updated, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		rec.Payload = updated
		return json.Marshal(rec)
	}
}

func TestStateUpcastRead(t *testing.T) {
	reg := NewSchemaRegistryFor(3)
	schemaUpcasters(t, reg, "State", mapSuffix("goal"))
	s := NewMemorySessionLog(WithMemorySessionSchemas(reg), WithSessionPrincipals(schemaPrincipal))
	ctx := types.WithTelemetry(context.Background(), &schemaTelemetry{})

	if _, err := s.Append(ctx, "s1", 0, types.Message{Role: "user"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSharedStateMeta(ctx, "s1", 0, json.RawMessage(`{"goal":"book"}`)); err != nil {
		t.Fatal(err)
	}
	before := seedSessionState(t, s, "s1", `{"goal":"book"}`)

	value, version, err := s.SharedStateMeta(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("version = %d, want 1", version)
	}
	var got map[string]any
	if err := json.Unmarshal(value, &got); err != nil {
		t.Fatal(err)
	}
	if got["goal"] != "book-v2-v3" {
		t.Fatalf("goal = %v, want book-v2-v3", got["goal"])
	}
	s.mu.Lock()
	after := append([]byte(nil), s.sessions["s1"].state.raw...)
	s.mu.Unlock()
	if string(before) != string(after) {
		t.Fatalf("stored bytes changed: before %s after %s", before, after)
	}
}

func TestAuditRecordUpcastRead(t *testing.T) {
	reg := NewSchemaRegistryFor(3)
	schemaUpcasters(t, reg, "AuditRecord", mapSuffix("Reason"))
	s := NewMemoryAuditLog(WithMemoryAuditSchemas(reg))
	ctx := types.WithTelemetry(context.Background(), &schemaTelemetry{})

	if err := s.Append(ctx, AuditRecord{Kind: AuditRunStarted, Tenant: "t", SessionID: "s1", Reason: "go"}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	row := &s.byTenant["t"][0]
	row.raw, _ = json.Marshal(RecordEnvelope{Shape: "AuditRecord", SchemaVersion: 1, Payload: mustJSON(t, row.rec)})
	before := append([]byte(nil), row.raw...)
	s.mu.Unlock()

	for got, err := range s.Read(ctx, "s1") {
		if err != nil {
			t.Fatal(err)
		}
		if got.Reason != "go-v2-v3" {
			t.Fatalf("Reason = %q, want go-v2-v3", got.Reason)
		}
	}
	s.mu.Lock()
	after := append([]byte(nil), s.byTenant["t"][0].raw...)
	s.mu.Unlock()
	if string(before) != string(after) {
		t.Fatalf("stored bytes changed: before %s after %s", before, after)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestEntryUpcastRead(t *testing.T) {
	reg := NewSchemaRegistryFor(3)
	schemaUpcasters(t, reg, "Entry", mapSuffix("Tool"))
	s := NewMemoryJournal(WithMemoryJournalSchemas(reg))
	ctx := types.WithTelemetry(context.Background(), &schemaTelemetry{})

	key := types.CallKey{SessionID: "s1", CallID: "c1"}
	if _, _, err := s.Reserve(ctx, key, "search", Fingerprint("f"), json.RawMessage(`{"q":"x"}`)); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	rec := s.byKey[key]
	rec.raw, _ = json.Marshal(RecordEnvelope{Shape: "Entry", SchemaVersion: 1, Payload: mustJSON(t, rec.entry)})
	before := append([]byte(nil), rec.raw...)
	s.mu.Unlock()

	got, err := s.ByFingerprint(ctx, "s1", Fingerprint("f"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Tool != "search-v2-v3" {
		t.Fatalf("entries = %+v, want one entry with tool search-v2-v3", got)
	}
	s.mu.Lock()
	after := append([]byte(nil), s.byKey[key].raw...)
	s.mu.Unlock()
	if string(before) != string(after) {
		t.Fatalf("stored bytes changed: before %s after %s", before, after)
	}
}

func TestRunUpcastRead(t *testing.T) {
	reg := NewSchemaRegistryFor(3)
	schemaUpcasters(t, reg, "Run", mapSuffix("Flow"))
	s := NewMemoryRuns(WithMemoryRunSchemas(reg))
	ctx := types.WithTelemetry(context.Background(), &schemaTelemetry{})

	if _, err := s.Start(ctx, Run{SessionID: "s1", RunID: "r1", Flow: "book"}, LeaseTTL); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	rec := s.runs["r1"]
	rec.raw, _ = json.Marshal(RecordEnvelope{Shape: "Run", SchemaVersion: 1, Payload: mustJSON(t, rec.run)})
	before := append([]byte(nil), rec.raw...)
	s.mu.Unlock()

	got, err := s.ByID(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Flow != "book-v2-v3" {
		t.Fatalf("Flow = %q, want book-v2-v3", got.Flow)
	}
	s.mu.Lock()
	after := append([]byte(nil), s.runs["r1"].raw...)
	s.mu.Unlock()
	if string(before) != string(after) {
		t.Fatalf("stored bytes changed: before %s after %s", before, after)
	}
}

func TestEventUpcastRead(t *testing.T) {
	reg := NewSchemaRegistryFor(3)
	schemaUpcasters(t, reg, "Event", mapSuffixEvent("MessageID"))
	s := NewMemoryEventLog(WithMemoryEventLogSchemas(reg))
	ctx := types.WithTelemetry(context.Background(), &schemaTelemetry{})

	e := Event{Payload: types.SteerApplied{MessageID: "m1"}}
	e.Meta.RunID = "r1"
	e.Meta.Seq = 1
	if err := s.Append(ctx, "r1", e); err != nil {
		t.Fatal(err)
	}
	rec := eventRecord{Kind: "steer_applied", Payload: mustJSON(t, e.Payload), Meta: e.Meta}
	env := RecordEnvelope{Shape: "Event", SchemaVersion: 1, Payload: mustJSON(t, rec)}
	if err := s.SeedRecord(ctx, "event/r1/1", env); err != nil {
		t.Fatal(err)
	}
	before, err := s.StoredRecord(ctx, "event/r1/1")
	if err != nil {
		t.Fatal(err)
	}

	var got Event
	for ev, err := range s.Read(ctx, "r1", 0) {
		if err != nil {
			t.Fatal(err)
		}
		got = ev
	}
	payload, ok := got.Payload.(types.SteerApplied)
	if !ok || payload.MessageID != "m1-v2-v3" {
		t.Fatalf("payload = %#v, want steer_applied with m1-v2-v3", got.Payload)
	}
	if got.Meta.RunID != "r1" || got.Meta.Seq != 1 {
		t.Fatalf("meta = %+v, want run r1 seq 1", got.Meta)
	}
	after, err := s.StoredRecord(ctx, "event/r1/1")
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("stored bytes changed: before %s after %s", before, after)
	}
}

func TestRedactionMapUpcastRead(t *testing.T) {
	reg := NewSchemaRegistryFor(3)
	schemaUpcasters(t, reg, "RedactionMap", mapSuffix("Tenant"))
	s := NewMemoryRedactionMaps(WithMemoryRedactionMapsSchemas(reg))
	ctx := types.WithTelemetry(context.Background(), &schemaTelemetry{})

	m := types.RedactionMap{Tenant: "t", Session: "s1", Entries: map[string]types.Entity{"a@b.c": {Kind: types.EntityEmail, Value: "a@b.c"}}}
	if err := s.Put(ctx, "t", "s1", m); err != nil {
		t.Fatal(err)
	}
	if err := s.SeedRecord(ctx, "redaction/t/s1", RecordEnvelope{Shape: "RedactionMap", SchemaVersion: 1, Payload: mustJSON(t, m)}); err != nil {
		t.Fatal(err)
	}
	before, err := s.StoredRecord(ctx, "redaction/t/s1")
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "t", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Tenant != "t-v2-v3" {
		t.Fatalf("Tenant = %q, want t-v2-v3", got.Tenant)
	}
	if got.Entries["a@b.c"].Kind != types.EntityEmail {
		t.Fatalf("entries = %+v, want email entity preserved", got.Entries)
	}
	after, err := s.StoredRecord(ctx, "redaction/t/s1")
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("stored bytes changed: before %s after %s", before, after)
	}
}

func TestShapesIncompleteChainReadsFail(t *testing.T) {
	ctx := types.WithTelemetry(context.Background(), &schemaTelemetry{})

	t.Run("State", func(t *testing.T) {
		reg := NewSchemaRegistryFor(3)
		schemaUpcastersIncomplete(t, reg, "State", noopUpcaster)
		s := NewMemorySessionLog(WithMemorySessionSchemas(reg), WithSessionPrincipals(schemaPrincipal))
		if _, err := s.Append(ctx, "s1", 0, types.Message{Role: "user"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetSharedStateMeta(ctx, "s1", 0, json.RawMessage(`{"goal":"book"}`)); err != nil {
			t.Fatal(err)
		}
		seedSessionState(t, s, "s1", `{"goal":"book"}`)
		value, _, err := s.SharedStateMeta(ctx, "s1")
		if !errors.Is(err, types.ErrSchemaTooOld) || value != nil {
			t.Fatalf("SharedStateMeta = %s, %v; want nil and ErrSchemaTooOld", value, err)
		}
	})

	t.Run("AuditRecord", func(t *testing.T) {
		reg := NewSchemaRegistryFor(3)
		schemaUpcastersIncomplete(t, reg, "AuditRecord", noopUpcaster)
		s := NewMemoryAuditLog(WithMemoryAuditSchemas(reg))
		if err := s.Append(ctx, AuditRecord{Kind: AuditRunStarted, Tenant: "t", SessionID: "s1"}); err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		row := &s.byTenant["t"][0]
		row.raw, _ = json.Marshal(RecordEnvelope{Shape: "AuditRecord", SchemaVersion: 1, Payload: mustJSON(t, row.rec)})
		before := append([]byte(nil), row.raw...)
		s.mu.Unlock()
		var got AuditRecord
		for rec, err := range s.Read(ctx, "s1") {
			if !errors.Is(err, types.ErrSchemaTooOld) {
				t.Fatalf("Read error = %v, want ErrSchemaTooOld", err)
			}
			got = rec
		}
		if got != (AuditRecord{}) {
			t.Fatalf("stale record returned: %+v", got)
		}
		s.mu.Lock()
		after := append([]byte(nil), s.byTenant["t"][0].raw...)
		s.mu.Unlock()
		if string(before) != string(after) {
			t.Fatalf("stored bytes changed")
		}
	})

	t.Run("Entry", func(t *testing.T) {
		reg := NewSchemaRegistryFor(3)
		schemaUpcastersIncomplete(t, reg, "Entry", noopUpcaster)
		s := NewMemoryJournal(WithMemoryJournalSchemas(reg))
		key := types.CallKey{SessionID: "s1", CallID: "c1"}
		if _, _, err := s.Reserve(ctx, key, "search", Fingerprint("f"), json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		s.byKey[key].raw, _ = json.Marshal(RecordEnvelope{Shape: "Entry", SchemaVersion: 1, Payload: mustJSON(t, s.byKey[key].entry)})
		s.mu.Unlock()
		got, err := s.ByFingerprint(ctx, "s1", Fingerprint("f"))
		if !errors.Is(err, types.ErrSchemaTooOld) || got != nil {
			t.Fatalf("ByFingerprint = %+v, %v; want nil and ErrSchemaTooOld", got, err)
		}
	})

	t.Run("Run", func(t *testing.T) {
		reg := NewSchemaRegistryFor(3)
		schemaUpcastersIncomplete(t, reg, "Run", noopUpcaster)
		s := NewMemoryRuns(WithMemoryRunSchemas(reg))
		if _, err := s.Start(ctx, Run{SessionID: "s1", RunID: "r1", Flow: "book"}, LeaseTTL); err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		s.runs["r1"].raw, _ = json.Marshal(RecordEnvelope{Shape: "Run", SchemaVersion: 1, Payload: mustJSON(t, s.runs["r1"].run)})
		s.mu.Unlock()
		got, err := s.ByID(ctx, "r1")
		if !errors.Is(err, types.ErrSchemaTooOld) || got.RunID != "" || got.Flow != "" {
			t.Fatalf("ByID = %+v, %v; want zero run and ErrSchemaTooOld", got, err)
		}
	})

	t.Run("Event", func(t *testing.T) {
		reg := NewSchemaRegistryFor(3)
		schemaUpcastersIncomplete(t, reg, "Event", noopUpcaster)
		s := NewMemoryEventLog(WithMemoryEventLogSchemas(reg))
		e := Event{Payload: types.SteerApplied{MessageID: "m1"}}
		e.Meta.RunID = "r1"
		if err := s.Append(ctx, "r1", e); err != nil {
			t.Fatal(err)
		}
		rec := eventRecord{Kind: "steer_applied", Payload: mustJSON(t, e.Payload), Meta: e.Meta}
		if err := s.SeedRecord(ctx, "event/r1/1", RecordEnvelope{Shape: "Event", SchemaVersion: 1, Payload: mustJSON(t, rec)}); err != nil {
			t.Fatal(err)
		}
		before, err := s.StoredRecord(ctx, "event/r1/1")
		if err != nil {
			t.Fatal(err)
		}
		var got Event
		for ev, err := range s.Read(ctx, "r1", 0) {
			if !errors.Is(err, types.ErrSchemaTooOld) {
				t.Fatalf("Read error = %v, want ErrSchemaTooOld", err)
			}
			got = ev
		}
		if got.Payload != nil {
			t.Fatalf("stale event returned: %+v", got)
		}
		after, err := s.StoredRecord(ctx, "event/r1/1")
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatalf("stored bytes changed")
		}
	})

	t.Run("RedactionMap", func(t *testing.T) {
		reg := NewSchemaRegistryFor(3)
		schemaUpcastersIncomplete(t, reg, "RedactionMap", noopUpcaster)
		s := NewMemoryRedactionMaps(WithMemoryRedactionMapsSchemas(reg))
		m := types.RedactionMap{Entries: map[string]types.Entity{"a@b.c": {Kind: types.EntityEmail, Value: "a@b.c"}}}
		if err := s.Put(ctx, "t", "s1", m); err != nil {
			t.Fatal(err)
		}
		if err := s.SeedRecord(ctx, "redaction/t/s1", RecordEnvelope{Shape: "RedactionMap", SchemaVersion: 1, Payload: mustJSON(t, m)}); err != nil {
			t.Fatal(err)
		}
		before, err := s.StoredRecord(ctx, "redaction/t/s1")
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, "t", "s1")
		if !errors.Is(err, types.ErrSchemaTooOld) || got.Tenant != "" || got.Entries != nil {
			t.Fatalf("Get = %+v, %v; want zero map and ErrSchemaTooOld", got, err)
		}
		after, err := s.StoredRecord(ctx, "redaction/t/s1")
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatalf("stored bytes changed")
		}
	})
}

func TestShapesUpcastMetricEmittedOnce(t *testing.T) {
	reads := map[string]func(t *testing.T, ctx context.Context){
		"State": func(t *testing.T, ctx context.Context) {
			reg := NewSchemaRegistryFor(3)
			schemaUpcasters(t, reg, "State", mapSuffix("goal"))
			s := NewMemorySessionLog(WithMemorySessionSchemas(reg), WithSessionPrincipals(schemaPrincipal))
			if _, err := s.Append(ctx, "s1", 0, types.Message{Role: "user"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetSharedStateMeta(ctx, "s1", 0, json.RawMessage(`{"goal":"book"}`)); err != nil {
				t.Fatal(err)
			}
			seedSessionState(t, s, "s1", `{"goal":"book"}`)
			if _, _, err := s.SharedStateMeta(ctx, "s1"); err != nil {
				t.Fatal(err)
			}
		},
		"AuditRecord": func(t *testing.T, ctx context.Context) {
			reg := NewSchemaRegistryFor(3)
			schemaUpcasters(t, reg, "AuditRecord", mapSuffix("Reason"))
			s := NewMemoryAuditLog(WithMemoryAuditSchemas(reg))
			if err := s.Append(ctx, AuditRecord{Kind: AuditRunStarted, Tenant: "t", SessionID: "s1", Reason: "go"}); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			row := &s.byTenant["t"][0]
			row.raw, _ = json.Marshal(RecordEnvelope{Shape: "AuditRecord", SchemaVersion: 1, Payload: mustJSON(t, row.rec)})
			s.mu.Unlock()
			for _, err := range s.Read(ctx, "s1") {
				if err != nil {
					t.Fatal(err)
				}
			}
		},
		"Entry": func(t *testing.T, ctx context.Context) {
			reg := NewSchemaRegistryFor(3)
			schemaUpcasters(t, reg, "Entry", mapSuffix("Tool"))
			s := NewMemoryJournal(WithMemoryJournalSchemas(reg))
			key := types.CallKey{SessionID: "s1", CallID: "c1"}
			if _, _, err := s.Reserve(ctx, key, "search", Fingerprint("f"), json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			s.byKey[key].raw, _ = json.Marshal(RecordEnvelope{Shape: "Entry", SchemaVersion: 1, Payload: mustJSON(t, s.byKey[key].entry)})
			s.mu.Unlock()
			if _, err := s.ByFingerprint(ctx, "s1", Fingerprint("f")); err != nil {
				t.Fatal(err)
			}
		},
		"Run": func(t *testing.T, ctx context.Context) {
			reg := NewSchemaRegistryFor(3)
			schemaUpcasters(t, reg, "Run", mapSuffix("Flow"))
			s := NewMemoryRuns(WithMemoryRunSchemas(reg))
			if _, err := s.Start(ctx, Run{SessionID: "s1", RunID: "r1", Flow: "book"}, LeaseTTL); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			s.runs["r1"].raw, _ = json.Marshal(RecordEnvelope{Shape: "Run", SchemaVersion: 1, Payload: mustJSON(t, s.runs["r1"].run)})
			s.mu.Unlock()
			if _, err := s.ByID(ctx, "r1"); err != nil {
				t.Fatal(err)
			}
		},
		"Event": func(t *testing.T, ctx context.Context) {
			reg := NewSchemaRegistryFor(3)
			schemaUpcasters(t, reg, "Event", mapSuffixEvent("MessageID"))
			s := NewMemoryEventLog(WithMemoryEventLogSchemas(reg))
			e := Event{Payload: types.SteerApplied{MessageID: "m1"}}
			e.Meta.RunID = "r1"
			if err := s.Append(ctx, "r1", e); err != nil {
				t.Fatal(err)
			}
			rec := eventRecord{Kind: "steer_applied", Payload: mustJSON(t, e.Payload), Meta: e.Meta}
			if err := s.SeedRecord(ctx, "event/r1/1", RecordEnvelope{Shape: "Event", SchemaVersion: 1, Payload: mustJSON(t, rec)}); err != nil {
				t.Fatal(err)
			}
			for _, err := range s.Read(ctx, "r1", 0) {
				if err != nil {
					t.Fatal(err)
				}
			}
		},
		"RedactionMap": func(t *testing.T, ctx context.Context) {
			reg := NewSchemaRegistryFor(3)
			schemaUpcasters(t, reg, "RedactionMap", mapSuffix("Tenant"))
			s := NewMemoryRedactionMaps(WithMemoryRedactionMapsSchemas(reg))
			m := types.RedactionMap{Entries: map[string]types.Entity{"a@b.c": {Kind: types.EntityEmail, Value: "a@b.c"}}}
			if err := s.Put(ctx, "t", "s1", m); err != nil {
				t.Fatal(err)
			}
			if err := s.SeedRecord(ctx, "redaction/t/s1", RecordEnvelope{Shape: "RedactionMap", SchemaVersion: 1, Payload: mustJSON(t, m)}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Get(ctx, "t", "s1"); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			telemetry := &schemaTelemetry{}
			ctx := types.WithTelemetry(context.Background(), telemetry)
			read(t, ctx)
			if len(telemetry.metrics) != 1 {
				t.Fatalf("metrics: got %d, want 1: %#v", len(telemetry.metrics), telemetry.metrics)
			}
			metric := telemetry.metrics[0]
			if metric.name != "gohan.schema.upcast" || metric.attrs[0] != (types.Int("from", 1)) || metric.attrs[1] != (types.Int("to", 3)) {
				t.Fatalf("metric: %#v", metric)
			}
		})
	}
}
