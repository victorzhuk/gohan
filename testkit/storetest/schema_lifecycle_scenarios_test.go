package storetest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func serializedBindingByShape(t *testing.T, shape string) serializedBinding {
	t.Helper()
	for _, b := range serializedBindings {
		if b.shape == shape {
			return b
		}
	}
	t.Fatalf("no serialized binding for shape %q", shape)
	return serializedBinding{}
}

func TestSchemaLifecycle(t *testing.T) {
	const current = stores.SchemaVersion(3)

	t.Run("lifecycle.upcast-on-read", func(t *testing.T) {
		b := serializedBindingByShape(t, "Message")
		reg := serializedRegistry(current, b.shape, b.step)
		telemetry := &serializedTelemetry{}
		ctx := types.WithTelemetry(context.Background(), telemetry)
		s := b.build(t, reg)
		identity := b.prepare(t, ctx, s, b.payload(t))
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

		incomplete := serializedIncompleteRegistry(current, b.shape, b.step)
		ctx = context.Background()
		s = b.build(t, incomplete)
		identity = b.prepare(t, ctx, s, b.payload(t))
		before, err = s.StoredRecord(ctx, identity)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.read(t, ctx, s, identity); !errors.Is(err, types.ErrSchemaTooOld) {
			t.Fatalf("read: got %v, want ErrSchemaTooOld", err)
		}
		after, err = s.StoredRecord(ctx, identity)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("stored bytes rewritten: %s vs %s", before, after)
		}
	})

	t.Run("lifecycle.historical-fixtures", func(t *testing.T) {
		for _, b := range serializedBindings {
			b := b
			t.Run(b.shape, func(t *testing.T) {
				reg := serializedRegistry(current, b.shape, b.step)
				ctx := context.Background()
				s := b.build(t, reg)
				payload := b.payload(t)
				identity := b.prepare(t, ctx, s, payload)

				stored, err := s.StoredRecord(ctx, identity)
				if err != nil {
					t.Fatal(err)
				}
				var envelope stores.RecordEnvelope
				if err := json.Unmarshal(stored, &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.SchemaVersion != 1 {
					t.Fatalf("schema_version: got %d, want 1", envelope.SchemaVersion)
				}
				if envelope.Shape != b.shape {
					t.Fatalf("shape: got %q, want %q", envelope.Shape, b.shape)
				}

				replay := serializedRegistry(current, b.shape, b.step)
				s = b.build(t, replay)
				identity = b.prepare(t, ctx, s, payload)
				if err := b.read(t, ctx, s, identity); err != nil {
					t.Fatalf("fixture replay: %v", err)
				}
			})
		}
	})
}
