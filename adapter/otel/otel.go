// Package otel implements the gohan telemetry port over the OpenTelemetry
// API. The user wires it into the driver with WithTelemetry(otel.New(...));
// core never imports this package, so the port keeps the floor free of
// OpenTelemetry types.
package otel

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/victorzhuk/gohan/core/types"
)

// Telemetry emits gohan's spans and metrics onto a real tracer and meter.
type Telemetry struct {
	tracer   trace.Tracer
	meter    metric.Meter
	counters sync.Map // name -> metric.Int64Counter
	hists    sync.Map // name -> metric.Float64Histogram
}

// New returns the port backed by the given providers.
func New(tp trace.TracerProvider, mp metric.MeterProvider) *Telemetry {
	return &Telemetry{
		tracer: tp.Tracer("github.com/victorzhuk/gohan"),
		meter:  mp.Meter("github.com/victorzhuk/gohan"),
	}
}

// StartSpan opens a span named after the governed chain and returns the ctx
// it travels in plus the function that stamps any late attributes and ends
// it. The core contract calls the end function exactly once.
func (t *Telemetry) StartSpan(ctx context.Context, name string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	ctx, span := t.tracer.Start(ctx, name, trace.WithAttributes(mapAttrs(attrs)...))
	return ctx, func(late ...types.Attr) {
		span.SetAttributes(mapAttrs(late)...)
		span.End()
	}
}

// Count adds n to the counter named name, creating it on first use.
func (t *Telemetry) Count(ctx context.Context, name string, n int64, attrs ...types.Attr) {
	c, ok := t.counter(name)
	if !ok {
		return
	}
	c.Add(ctx, n, metric.WithAttributes(mapAttrs(attrs)...))
}

// Record observes v on the histogram named name, creating it on first use.
func (t *Telemetry) Record(ctx context.Context, name string, v float64, attrs ...types.Attr) {
	h, ok := t.histogram(name)
	if !ok {
		return
	}
	h.Record(ctx, v, metric.WithAttributes(mapAttrs(attrs)...))
}

func (t *Telemetry) counter(name string) (metric.Int64Counter, bool) {
	if c, ok := t.counters.Load(name); ok {
		return c.(metric.Int64Counter), true
	}
	c, err := t.meter.Int64Counter(name)
	if err != nil {
		return nil, false
	}
	actual, _ := t.counters.LoadOrStore(name, c)
	return actual.(metric.Int64Counter), true
}

func (t *Telemetry) histogram(name string) (metric.Float64Histogram, bool) {
	if h, ok := t.hists.Load(name); ok {
		return h.(metric.Float64Histogram), true
	}
	h, err := t.meter.Float64Histogram(name)
	if err != nil {
		return nil, false
	}
	actual, _ := t.hists.LoadOrStore(name, h)
	return actual.(metric.Float64Histogram), true
}

// mapAttrs translates the floor's Attr onto OpenTelemetry attributes. The
// floor limits values to the constructor kinds, so the type switch covers
// every case without reflection.
func mapAttrs(attrs []types.Attr) []attribute.KeyValue {
	kvs := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		switch v := a.Value.(type) {
		case string:
			kvs = append(kvs, attribute.String(a.Key, v))
		case int64:
			kvs = append(kvs, attribute.Int64(a.Key, v))
		case float64:
			kvs = append(kvs, attribute.Float64(a.Key, v))
		case bool:
			kvs = append(kvs, attribute.Bool(a.Key, v))
		default:
			kvs = append(kvs, attribute.String(a.Key, fmt.Sprint(v)))
		}
	}
	return kvs
}
