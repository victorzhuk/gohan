package types

import "context"

// Sink receives the component-level events a governed decorator produces.
// A runtime never emits these itself; it reads the sink from the run-scoped
// ctx.
type Sink interface {
	Emit(ctx context.Context, e Event)
}

const ctxSink ctxKey = iota + 100

// WithSink attaches the run-scoped event sink.
func WithSink(ctx context.Context, s Sink) context.Context {
	return context.WithValue(ctx, ctxSink, s)
}

// SinkFrom reports the event sink in ctx, or ok == false when none was
// attached. A component that finds no sink drops the event and carries on.
func SinkFrom(ctx context.Context) (Sink, bool) {
	s, ok := ctx.Value(ctxSink).(Sink)
	return s, ok
}
