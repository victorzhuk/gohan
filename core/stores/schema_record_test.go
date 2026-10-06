package stores

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

type schemaMetric struct {
	name  string
	count int64
	attrs []types.Attr
}

type schemaTelemetry struct{ metrics []schemaMetric }

func (t *schemaTelemetry) StartSpan(ctx context.Context, name string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}
func (t *schemaTelemetry) Count(_ context.Context, name string, n int64, attrs ...types.Attr) {
	t.metrics = append(t.metrics, schemaMetric{name: name, count: n, attrs: attrs})
}
func (t *schemaTelemetry) Record(context.Context, string, float64, ...types.Attr) {}

func TestDecodeRecordUpcast(t *testing.T) {
	reg := NewSchemaRegistryFor(3)
	for version, suffix := range map[SchemaVersion]string{1: `,"v2":true`, 2: `,"v3":true`} {
		suffix := suffix
		if err := reg.Register("Message", version, func(raw []byte) ([]byte, error) {
			return append(raw[:len(raw)-1], suffix+"}"...), nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	payload := json.RawMessage(`{"text":"hi"}`)
	env := RecordEnvelope{Shape: "Message", SchemaVersion: 1, Payload: payload}
	telemetry := &schemaTelemetry{}
	ctx := types.WithTelemetry(context.Background(), telemetry)
	got, err := DecodeRecord[map[string]any](ctx, reg, env)
	if err != nil {
		t.Fatal(err)
	}
	if got["v2"] != true || got["v3"] != true {
		t.Fatalf("decoded record: %#v", got)
	}
	if string(env.Payload) != `{"text":"hi"}` {
		t.Fatalf("payload changed: %s", env.Payload)
	}
	if len(telemetry.metrics) != 1 {
		t.Fatalf("metrics: got %d, want 1", len(telemetry.metrics))
	}
	metric := telemetry.metrics[0]
	if metric.name != "gohan.schema.upcast" || metric.count != 1 || len(metric.attrs) != 2 || metric.attrs[0] != (types.Int("from", 1)) || metric.attrs[1] != (types.Int("to", 3)) {
		t.Fatalf("metric: %#v", metric)
	}
}

func TestDecodeRecordIncompleteChain(t *testing.T) {
	reg := NewSchemaRegistryFor(3)
	if err := reg.Register("Message", 1, func(raw []byte) ([]byte, error) { return raw, nil }); err != nil {
		t.Fatal(err)
	}
	telemetry := &schemaTelemetry{}
	ctx := types.WithTelemetry(context.Background(), telemetry)
	got, err := DecodeRecord[map[string]any](ctx, reg, RecordEnvelope{Shape: "Message", SchemaVersion: 1, Payload: []byte(`{"stale":true}`)})
	if !errors.Is(err, types.ErrSchemaTooOld) || got != nil {
		t.Fatalf("DecodeRecord: got %#v, %v; want nil and ErrSchemaTooOld", got, err)
	}
	if len(telemetry.metrics) != 0 {
		t.Fatalf("failed chain emitted metrics: %#v", telemetry.metrics)
	}
}
