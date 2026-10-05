package otel_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/victorzhuk/gohan/adapter/otel"
	"github.com/victorzhuk/gohan/core/types"
)

// Span and metric names as the telemetry spec pins them.
const (
	SpanRun            = "invoke_agent"
	SpanChat           = "chat"
	SpanGuard          = "gohan.guard"
	MetricRunRecovered = "gohan.run.recovered"
)

func newTelemetry(t *testing.T) (*otel.Telemetry, *tracetest.SpanRecorder, *sdkmetric.ManualReader) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	mr := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr))
	return otel.New(tp, mp), sr, mr
}

func attrValue(span sdktrace.ReadOnlySpan, key string) attribute.Value {
	for _, kv := range span.Attributes() {
		if kv.Key == attribute.Key(key) {
			return kv.Value
		}
	}
	return attribute.Value{}
}

func findSpan(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

func TestOTelExporter(t *testing.T) {
	t.Run("span tree nests", func(t *testing.T) {
		tel, sr, _ := newTelemetry(t)
		ctx, endRun := tel.StartSpan(context.Background(), SpanRun,
			types.String(types.KeyFlow, "support"),
		)
		_, endChat := tel.StartSpan(ctx, SpanChat)
		endChat()
		endRun()

		spans := sr.Ended()
		run := findSpan(spans, SpanRun)
		chat := findSpan(spans, SpanChat)
		if run == nil || chat == nil {
			t.Fatalf("missing spans: got %d ended", len(spans))
		}
		if chat.Parent().SpanID() != run.SpanContext().SpanID() {
			t.Fatalf("chat span parent %v is not run span %v", chat.Parent().SpanID(), run.SpanContext().SpanID())
		}
		if chat.SpanContext().TraceID() != run.SpanContext().TraceID() {
			t.Fatalf("child trace %v differs from run trace %v", chat.SpanContext().TraceID(), run.SpanContext().TraceID())
		}
	})

	t.Run("span names and canonical keys round-trip", func(t *testing.T) {
		tel, sr, _ := newTelemetry(t)
		_, end := tel.StartSpan(context.Background(), SpanGuard,
			types.String(types.KeyFlow, "support"),
			types.String(types.KeySessionID, "s-1"),
			types.String(types.KeyRunID, "r-1"),
			types.String(types.KeyRootRunID, "root-1"),
			types.String(types.KeyParentRunID, "root-1"),
			types.Int(types.KeyTurn, 3),
			types.String(types.KeyGuardStage, "input"),
			types.Bool(types.KeyGuardVerdict, true),
			types.Float(types.KeyRouterConfidence, 0.75),
		)
		end(types.String(types.KeyToolOutcome, "allow"))

		span := findSpan(sr.Ended(), SpanGuard)
		if span == nil {
			t.Fatal("guard span not ended")
		}
		checks := []struct {
			key   string
			value attribute.Value
		}{
			{types.KeyFlow, attribute.StringValue("support")},
			{types.KeySessionID, attribute.StringValue("s-1")},
			{types.KeyRunID, attribute.StringValue("r-1")},
			{types.KeyRootRunID, attribute.StringValue("root-1")},
			{types.KeyParentRunID, attribute.StringValue("root-1")},
			{types.KeyTurn, attribute.Int64Value(3)},
			{types.KeyGuardStage, attribute.StringValue("input")},
			{types.KeyGuardVerdict, attribute.BoolValue(true)},
			{types.KeyRouterConfidence, attribute.Float64Value(0.75)},
			{types.KeyToolOutcome, attribute.StringValue("allow")},
		}
		for _, c := range checks {
			if got := attrValue(span, c.key); got != c.value {
				t.Errorf("%s = %v, want %v", c.key, got, c.value)
			}
		}
	})

	t.Run("counter records", func(t *testing.T) {
		tel, _, mr := newTelemetry(t)
		ctx := context.Background()
		tel.Count(ctx, MetricRunRecovered, 1, types.String(types.KeyFlow, "support"))
		tel.Count(ctx, MetricRunRecovered, 2, types.String(types.KeyFlow, "triage"))

		var rm metricdata.ResourceMetrics
		if err := mr.Collect(ctx, &rm); err != nil {
			t.Fatalf("collect: %v", err)
		}
		found := false
		for _, m := range rm.ScopeMetrics {
			for _, metric := range m.Metrics {
				if metric.Name != MetricRunRecovered {
					continue
				}
				found = true
				sum, ok := metric.Data.(metricdata.Sum[int64])
				if !ok {
					t.Fatalf("gohan.run.recovered is %T, want Sum[int64]", metric.Data)
				}
				total := int64(0)
				for _, dp := range sum.DataPoints {
					total += dp.Value
				}
				if total != 3 {
					t.Fatalf("sum = %d, want 3", total)
				}
			}
		}
		if !found {
			t.Fatal("gohan.run.recovered not collected")
		}
	})

	t.Run("histogram records", func(t *testing.T) {
		tel, _, mr := newTelemetry(t)
		ctx := context.Background()
		tel.Record(ctx, "gohan.model.ttft", 0.25, types.String(types.KeyFlow, "support"))
		tel.Record(ctx, "gohan.model.ttft", 0.75)

		var rm metricdata.ResourceMetrics
		if err := mr.Collect(ctx, &rm); err != nil {
			t.Fatalf("collect: %v", err)
		}
		var h metricdata.Histogram[float64]
		for _, m := range rm.ScopeMetrics {
			for _, metric := range m.Metrics {
				if metric.Name == "gohan.model.ttft" {
					ok := false
					h, ok = metric.Data.(metricdata.Histogram[float64])
					if !ok {
						t.Fatalf("model.ttft is %T", metric.Data)
					}
				}
			}
		}
		if len(h.DataPoints) == 0 {
			t.Fatal("gohan.model.ttft not collected")
		}
		count, sum := uint64(0), 0.0
		for _, dp := range h.DataPoints {
			count += dp.Count
			sum += dp.Sum
		}
		if count != 2 {
			t.Fatalf("count = %d, want 2", count)
		}
		if sum != 1.0 {
			t.Fatalf("sum = %v, want 1.0", sum)
		}
	})
}
