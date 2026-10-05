package telemetry

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// Decorate wraps a telemetry sink so every attribute batch the sink sees
// passes through the Convention first: initial span attributes, the final
// attributes an end callback receives, counters and records. The sink's
// derived context returns unchanged, so parentage travels in the ctx the
// sink built. A nil sink is a wiring error, not a silent no-op.
func Decorate(sink types.Telemetry, convention Convention) (types.Telemetry, error) {
	if sink == nil {
		return nil, fmt.Errorf("gohan/telemetry: Decorate needs a telemetry sink")
	}
	return decorated{sink: sink, convention: convention}, nil
}

type decorated struct {
	sink       types.Telemetry
	convention Convention
}

func (d decorated) StartSpan(ctx context.Context, name string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	sctx, end := d.sink.StartSpan(ctx, name, d.convention.ApplyAttrs(attrs...)...)
	return sctx, func(final ...types.Attr) {
		end(d.convention.ApplyAttrs(final...)...)
	}
}

func (d decorated) Count(ctx context.Context, name string, n int64, attrs ...types.Attr) {
	d.sink.Count(ctx, name, n, d.convention.ApplyAttrs(attrs...)...)
}

func (d decorated) Record(ctx context.Context, name string, v float64, attrs ...types.Attr) {
	d.sink.Record(ctx, name, v, d.convention.ApplyAttrs(attrs...)...)
}
