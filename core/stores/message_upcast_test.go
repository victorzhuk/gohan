package stores

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func insertMetaField(field string) func([]byte) ([]byte, error) {
	return func(raw []byte) ([]byte, error) {
		var record map[string]any
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, err
		}
		meta, _ := record["meta"].(map[string]any)
		if meta == nil {
			meta = map[string]any{}
		}
		meta[field] = true
		record["meta"] = meta
		return json.Marshal(record)
	}
}

func TestMessageUpcast(t *testing.T) {
	reg := NewSchemaRegistryFor(3)
	for _, link := range []struct {
		from  SchemaVersion
		field string
	}{{1, "v2"}, {2, "v3"}} {
		link := link
		if err := reg.Register("Message", link.from, func(raw []byte) ([]byte, error) {
			return insertMetaField(link.field)(raw)
		}); err != nil {
			t.Fatal(err)
		}
	}
	telemetry := &schemaTelemetry{}
	ctx := types.WithTelemetry(context.Background(), telemetry)
	s := NewMemorySessionLog(WithMemorySessionSchemas(reg), WithSessionPrincipals(schemaPrincipal))
	if _, err := s.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "placeholder"}}}); err != nil {
		t.Fatal(err)
	}
	identity := "session/s1/message/0"
	if err := s.SeedRecord(ctx, identity, RecordEnvelope{
		Shape:         "Message",
		SchemaVersion: 1,
		Payload:       json.RawMessage(`{"role":"user","blocks":[{"Kind":"text","Block":{"Text":"hi"}}]}`),
	}); err != nil {
		t.Fatal(err)
	}
	before, err := s.StoredRecord(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	hist, err := s.Load(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist.Messages) != 1 {
		t.Fatalf("messages: got %d, want 1", len(hist.Messages))
	}
	msg := hist.Messages[0]
	if msg.Role != types.RoleUser {
		t.Fatalf("role: got %q, want user", msg.Role)
	}
	text, ok := msg.Blocks[0].(types.Text)
	if !ok || text.Text != "hi" {
		t.Fatalf("block: got %#v, want text %q", msg.Blocks[0], "hi")
	}
	if msg.Meta["v2"] != true || msg.Meta["v3"] != true {
		t.Fatalf("meta: got %#v, want v2 and v3 set", msg.Meta)
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
	if metric.name != "gohan.schema.upcast" || metric.count != 1 || len(metric.attrs) != 2 || metric.attrs[0] != (types.Int("from", 1)) || metric.attrs[1] != (types.Int("to", 3)) {
		t.Fatalf("metric: %#v", metric)
	}
}
