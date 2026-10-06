package storetest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type serializedMetric struct {
	name  string
	count int64
	attrs []types.Attr
}

type serializedTelemetry struct{ metrics []serializedMetric }

func (t *serializedTelemetry) StartSpan(ctx context.Context, name string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}
func (t *serializedTelemetry) Count(_ context.Context, name string, n int64, attrs ...types.Attr) {
	t.metrics = append(t.metrics, serializedMetric{name: name, count: n, attrs: attrs})
}
func (t *serializedTelemetry) Record(context.Context, string, float64, ...types.Attr) {}

func serializedPrincipal(context.Context) (types.Principal, bool) {
	return types.Principal{Tenant: "t", Subject: "u"}, true
}

// serializedRegistry builds the registry a fixture read uses: one pure
// upcaster per N->N+1 link, each applying step stamped with the target
// version.
func serializedRegistry(current stores.SchemaVersion, shape string, step func(raw []byte, version int) ([]byte, error)) *stores.SchemaRegistry {
	reg := stores.NewSchemaRegistryFor(current)
	for link := stores.SchemaVersion(1); link < current; link++ {
		link := link
		if err := reg.Register(shape, link, func(raw []byte) ([]byte, error) {
			return step(raw, int(link)+1)
		}); err != nil {
			panic(err)
		}
	}
	return reg
}

func serializedIncompleteRegistry(current stores.SchemaVersion, shape string, step func(raw []byte, version int) ([]byte, error)) *stores.SchemaRegistry {
	reg := stores.NewSchemaRegistryFor(current)
	if err := reg.Register(shape, 1, func(raw []byte) ([]byte, error) {
		return step(raw, 2)
	}); err != nil {
		panic(err)
	}
	return reg
}

// suffixField mutates a top-level JSON string field for the upcast chain.
func suffixField(field string) func([]byte, int) ([]byte, error) {
	return func(raw []byte, version int) ([]byte, error) {
		var record map[string]any
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, err
		}
		value, ok := record[field].(string)
		if !ok {
			return nil, fmt.Errorf("field %q is %T, want a string", field, record[field])
		}
		record[field] = fmt.Sprintf("%s-v%d", value, version)
		return json.Marshal(record)
	}
}

// metaMarker stamps the target version into the message's meta map.
func metaMarker(raw []byte, version int) ([]byte, error) {
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	meta, _ := record["meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta[fmt.Sprintf("v%d", version)] = true
	record["meta"] = meta
	return json.Marshal(record)
}

// serializedEventRecord mirrors the Event stored shape's wire keys.
type serializedEventRecord struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
	Meta    json.RawMessage `json:"meta"`
}

// eventSuffixField mutates a string field inside the event record's payload.
func eventSuffixField(field string) func([]byte, int) ([]byte, error) {
	return func(raw []byte, version int) ([]byte, error) {
		var rec serializedEventRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, err
		}
		updated, err := suffixField(field)(rec.Payload, version)
		if err != nil {
			return nil, err
		}
		rec.Payload = updated
		return json.Marshal(rec)
	}
}

func serializedPayload(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// serializedBinding binds one stored shape to its memory store: prepare
// writes the record through the store's own path, then seeds the version-1
// fixture bytes and returns the identity; read runs the public read path.
type serializedBinding struct {
	shape string
	step  func(raw []byte, version int) ([]byte, error)
	build func(t *testing.T, reg *stores.SchemaRegistry) stores.SerializedRecordStore
	// payload is the version-1 fixture the seed installs.
	payload func(t *testing.T) json.RawMessage
	prepare func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, payload []byte) string
	// read returns the public read's error and fatals on a wrong value.
	read func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, identity string) error
}

var serializedBindings = []serializedBinding{
	{
		shape: "Message",
		step:  metaMarker,
		build: func(t *testing.T, reg *stores.SchemaRegistry) stores.SerializedRecordStore {
			return stores.NewMemorySessionLog(
				stores.WithMemorySessionSchemas(reg),
				stores.WithSessionPrincipals(serializedPrincipal),
			)
		},
		payload: func(t *testing.T) json.RawMessage {
			return json.RawMessage(`{"role":"user","blocks":[{"Kind":"text","Block":{"Text":"hi"}}]}`)
		},
		prepare: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, payload []byte) string {
			session := s.(*stores.MemorySessionLog)
			if _, err := session.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "placeholder"}}}); err != nil {
				t.Fatal(err)
			}
			return seed(t, ctx, s, "Message", "session/s1/message/0", payload)
		},
		read: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, identity string) error {
			hist, err := s.(*stores.MemorySessionLog).Load(ctx, "s1")
			if err != nil {
				return err
			}
			if len(hist.Messages) != 1 {
				t.Fatalf("messages: got %d, want 1", len(hist.Messages))
			}
			msg := hist.Messages[0]
			if msg.Role != types.RoleUser {
				t.Fatalf("role: got %q, want user", msg.Role)
			}
			if text, ok := msg.Blocks[0].(types.Text); !ok || text.Text != "hi" {
				t.Fatalf("block: got %#v, want text %q", msg.Blocks[0], "hi")
			}
			for version := 2; version <= 3; version++ {
				if msg.Meta[fmt.Sprintf("v%d", version)] != true {
					t.Fatalf("meta: got %#v, want v%d set", msg.Meta, version)
				}
			}
			return nil
		},
	},
	{
		shape: "State",
		step:  suffixField("goal"),
		build: func(t *testing.T, reg *stores.SchemaRegistry) stores.SerializedRecordStore {
			return stores.NewMemorySessionLog(
				stores.WithMemorySessionSchemas(reg),
				stores.WithSessionPrincipals(serializedPrincipal),
			)
		},
		payload: func(t *testing.T) json.RawMessage { return json.RawMessage(`{"goal":"book"}`) },
		prepare: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, payload []byte) string {
			session := s.(*stores.MemorySessionLog)
			if _, err := session.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser}); err != nil {
				t.Fatal(err)
			}
			if _, err := session.SetSharedStateMeta(ctx, "s1", 0, json.RawMessage(`{"goal":"book"}`)); err != nil {
				t.Fatal(err)
			}
			return seed(t, ctx, s, "State", "session/s1/state", payload)
		},
		read: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, identity string) error {
			value, version, err := s.(*stores.MemorySessionLog).SharedStateMeta(ctx, "s1")
			if err != nil {
				return err
			}
			if version != 1 {
				t.Fatalf("version: got %d, want 1", version)
			}
			var got map[string]any
			if err := json.Unmarshal(value, &got); err != nil {
				t.Fatal(err)
			}
			if got["goal"] != "book-v2-v3" {
				t.Fatalf("goal: got %v, want book-v2-v3", got["goal"])
			}
			return nil
		},
	},
	{
		shape: "Event",
		step:  eventSuffixField("MessageID"),
		build: func(t *testing.T, reg *stores.SchemaRegistry) stores.SerializedRecordStore {
			return stores.NewMemoryEventLog(stores.WithMemoryEventLogSchemas(reg))
		},
		payload: func(t *testing.T) json.RawMessage {
			meta, err := json.Marshal(types.EventMeta{RunID: "r1", Seq: 1})
			if err != nil {
				t.Fatal(err)
			}
			rec := serializedEventRecord{
				Kind:    "steer_applied",
				Payload: json.RawMessage(`{"MessageID":"m1"}`),
				Meta:    meta,
			}
			return serializedPayload(t, rec)
		},
		prepare: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, payload []byte) string {
			log := s.(*stores.MemoryEventLog)
			var e stores.Event
			e.Payload = types.SteerApplied{MessageID: "m1"}
			e.Meta.RunID = "r1"
			if err := log.Append(ctx, "r1", e); err != nil {
				t.Fatal(err)
			}
			return seed(t, ctx, s, "Event", "event/r1/1", payload)
		},
		read: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, identity string) error {
			var got stores.Event
			for ev, err := range s.(*stores.MemoryEventLog).Read(ctx, "r1", 0) {
				if err != nil {
					return err
				}
				got = ev
			}
			payload, ok := got.Payload.(types.SteerApplied)
			if !ok || payload.MessageID != "m1-v2-v3" {
				t.Fatalf("payload: got %#v, want steer_applied with m1-v2-v3", got.Payload)
			}
			if got.Meta.RunID != "r1" || got.Meta.Seq != 1 {
				t.Fatalf("meta: got %+v, want run r1 seq 1", got.Meta)
			}
			return nil
		},
	},
	{
		shape: "Entry",
		step:  suffixField("Tool"),
		build: func(t *testing.T, reg *stores.SchemaRegistry) stores.SerializedRecordStore {
			return stores.NewMemoryJournal(stores.WithMemoryJournalSchemas(reg))
		},
		payload: func(t *testing.T) json.RawMessage {
			return serializedPayload(t, stores.Entry{State: stores.Reserved, Fingerprint: "f", Tool: "search", Key: "c1"})
		},
		prepare: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, payload []byte) string {
			journal := s.(*stores.MemoryJournal)
			key := types.CallKey{SessionID: "s1", CallID: "c1"}
			if _, _, err := journal.Reserve(ctx, key, "search", "f", json.RawMessage(`{"q":"x"}`)); err != nil {
				t.Fatal(err)
			}
			return seed(t, ctx, s, "Entry", "journal/s1/c1", payload)
		},
		read: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, identity string) error {
			got, err := s.(*stores.MemoryJournal).ByFingerprint(ctx, "s1", "f")
			if err != nil {
				return err
			}
			if len(got) != 1 || got[0].Tool != "search-v2-v3" {
				t.Fatalf("entries: got %+v, want one entry with tool search-v2-v3", got)
			}
			return nil
		},
	},
	{
		shape: "Run",
		step:  suffixField("Flow"),
		build: func(t *testing.T, reg *stores.SchemaRegistry) stores.SerializedRecordStore {
			return stores.NewMemoryRuns(stores.WithMemoryRunSchemas(reg))
		},
		payload: func(t *testing.T) json.RawMessage {
			return serializedPayload(t, stores.Run{SessionID: "s1", RunID: "r1", Flow: "book"})
		},
		prepare: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, payload []byte) string {
			runs := s.(*stores.MemoryRuns)
			if _, err := runs.Start(ctx, stores.Run{SessionID: "s1", RunID: "r1", Flow: "book"}, stores.LeaseTTL); err != nil {
				t.Fatal(err)
			}
			return seed(t, ctx, s, "Run", "run/r1", payload)
		},
		read: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, identity string) error {
			got, err := s.(*stores.MemoryRuns).ByID(ctx, "r1")
			if err != nil {
				return err
			}
			if got.Flow != "book-v2-v3" {
				t.Fatalf("flow: got %q, want book-v2-v3", got.Flow)
			}
			return nil
		},
	},
	{
		shape: "RedactionMap",
		step:  suffixField("Tenant"),
		build: func(t *testing.T, reg *stores.SchemaRegistry) stores.SerializedRecordStore {
			return stores.NewMemoryRedactionMaps(stores.WithMemoryRedactionMapsSchemas(reg))
		},
		payload: func(t *testing.T) json.RawMessage {
			return serializedPayload(t, types.RedactionMap{
				Tenant:  "t",
				Session: "s1",
				Entries: map[string]types.Entity{"a@b.c": {Kind: types.EntityEmail, Value: "a@b.c"}},
			})
		},
		prepare: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, payload []byte) string {
			maps := s.(*stores.MemoryRedactionMaps)
			m := types.RedactionMap{Tenant: "t", Session: "s1", Entries: map[string]types.Entity{"a@b.c": {Kind: types.EntityEmail, Value: "a@b.c"}}}
			if err := maps.Put(ctx, "t", "s1", m); err != nil {
				t.Fatal(err)
			}
			return seed(t, ctx, s, "RedactionMap", "redaction/t/s1", payload)
		},
		read: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, identity string) error {
			got, err := s.(*stores.MemoryRedactionMaps).Get(ctx, "t", "s1")
			if err != nil {
				return err
			}
			if got.Tenant != "t-v2-v3" {
				t.Fatalf("tenant: got %q, want t-v2-v3", got.Tenant)
			}
			if got.Entries["a@b.c"].Kind != types.EntityEmail {
				t.Fatalf("entries: got %+v, want email entity preserved", got.Entries)
			}
			return nil
		},
	},
	{
		shape: "AuditRecord",
		step:  suffixField("Reason"),
		build: func(t *testing.T, reg *stores.SchemaRegistry) stores.SerializedRecordStore {
			return stores.NewMemoryAuditLog(stores.WithMemoryAuditSchemas(reg))
		},
		payload: func(t *testing.T) json.RawMessage {
			return serializedPayload(t, stores.AuditRecord{Kind: stores.AuditRunStarted, Tenant: "t", SessionID: "s1", Reason: "go"})
		},
		prepare: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, payload []byte) string {
			audit := s.(*stores.MemoryAuditLog)
			if err := audit.Append(ctx, stores.AuditRecord{Kind: stores.AuditRunStarted, Tenant: "t", SessionID: "s1", Reason: "go"}); err != nil {
				t.Fatal(err)
			}
			return seed(t, ctx, s, "AuditRecord", "audit/t/0", payload)
		},
		read: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, identity string) error {
			for rec, err := range s.(*stores.MemoryAuditLog).Read(ctx, "s1") {
				if err != nil {
					return err
				}
				if rec.Reason != "go-v2-v3" {
					t.Fatalf("reason: got %q, want go-v2-v3", rec.Reason)
				}
			}
			return nil
		},
	},
	{
		shape: "Checkpoint",
		step:  suffixField("Flow"),
		build: func(t *testing.T, reg *stores.SchemaRegistry) stores.SerializedRecordStore {
			return stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointSchemas(reg))
		},
		payload: func(t *testing.T) json.RawMessage {
			return serializedPayload(t, stores.Checkpoint{RunID: "r1", SessionID: "s1", Flow: "book"})
		},
		prepare: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, payload []byte) string {
			checkpoints := s.(*stores.MemoryCheckpoints)
			token, err := checkpoints.Put(ctx, stores.Checkpoint{RunID: "r1", SessionID: "s1", Flow: "book"})
			if err != nil {
				t.Fatal(err)
			}
			return seed(t, ctx, s, "Checkpoint", "checkpoint/"+string(token), payload)
		},
		read: func(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, identity string) error {
			token := types.ResumeToken(strings.TrimPrefix(identity, "checkpoint/"))
			got, err := s.(*stores.MemoryCheckpoints).Peek(ctx, token)
			if err != nil {
				return err
			}
			if got.Flow != "book-v2-v3" || got.SessionID != "s1" {
				t.Fatalf("checkpoint: got %+v, want flow book-v2-v3 in session s1", got)
			}
			return nil
		},
	},
}

func seed(t *testing.T, ctx context.Context, s stores.SerializedRecordStore, shape, identity string, payload []byte) string {
	t.Helper()
	envelope := stores.RecordEnvelope{Shape: shape, SchemaVersion: 1, Payload: append([]byte(nil), payload...)}
	if err := s.SeedRecord(ctx, identity, envelope); err != nil {
		t.Fatal(err)
	}
	return identity
}

func TestSchemasSerializedPublicRead(t *testing.T) {
	const current = stores.SchemaVersion(3)
	for _, b := range serializedBindings {
		b := b
		t.Run(b.shape, func(t *testing.T) {
			t.Run("historical_fixture_replays_at_current", func(t *testing.T) {
				reg := serializedRegistry(current, b.shape, b.step)
				telemetry := &serializedTelemetry{}
				ctx := types.WithTelemetry(context.Background(), telemetry)
				s := b.build(t, reg)
				payload := b.payload(t)
				identity := b.prepare(t, ctx, s, payload)
				before, err := s.StoredRecord(ctx, identity)
				if err != nil {
					t.Fatal(err)
				}
				if err := b.read(t, ctx, s, identity); err != nil {
					t.Fatal(err)
				}
				after, err := s.StoredRecord(ctx, identity)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatalf("stored bytes rewritten: %s vs %s", before, after)
				}
				if len(telemetry.metrics) != 1 {
					t.Fatalf("metrics: got %d, want 1", len(telemetry.metrics))
				}
				metric := telemetry.metrics[0]
				if metric.name != "gohan.schema.upcast" || metric.count != 1 || len(metric.attrs) != 2 ||
					metric.attrs[0] != (types.Int("from", 1)) || metric.attrs[1] != (types.Int("to", int64(current))) {
					t.Fatalf("metric: %#v", metric)
				}
			})

			t.Run("missing_link_reports_too_old", func(t *testing.T) {
				reg := serializedIncompleteRegistry(current, b.shape, b.step)
				ctx := context.Background()
				s := b.build(t, reg)
				payload := b.payload(t)
				identity := b.prepare(t, ctx, s, payload)
				before, err := s.StoredRecord(ctx, identity)
				if err != nil {
					t.Fatal(err)
				}
				err = b.read(t, ctx, s, identity)
				if !errors.Is(err, types.ErrSchemaTooOld) {
					t.Fatalf("read: got %v, want ErrSchemaTooOld", err)
				}
				after, err := s.StoredRecord(ctx, identity)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatalf("stored bytes rewritten: %s vs %s", before, after)
				}
			})
		})
	}
}
