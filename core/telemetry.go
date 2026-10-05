package gohan

import (
	"context"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Span names core emits. The run span carries the invoke_agent operation
// name; the governed model, tool, guard and router chains open the child
// spans. A Convention layer maps the names and keys to a backend's
// vocabulary, so the tree is identical on every backend.
const (
	SpanRun   = "invoke_agent"
	SpanChat  = "chat"
	SpanTool  = "execute_tool"
	SpanGuard = "gohan.guard"
	SpanPlan  = "gohan.decide"
)

// Metric names core counts from the governed sites.
const (
	MetricRunRecovered = "gohan.run.recovered"
	MetricRunAbandoned = "gohan.run.abandoned"
)

// startRunSpan opens the run span for the run info in ctx. A nil port
// returns the ctx unchanged with a no-op end, so emission is never a
// panic and never a branch the callers repeat.
func startRunSpan(ctx context.Context, tel types.Telemetry) (context.Context, func(...types.Attr)) {
	info, ok := types.RunInfoFrom(ctx)
	if !ok {
		return ctx, func(...types.Attr) {}
	}
	attrs := runSpanAttrs(info)
	if tel == nil {
		return ctx, func(...types.Attr) {}
	}
	sctx, end := tel.StartSpan(ctx, SpanRun, attrs...)
	return sctx, end
}

// runSpanAttrs are the run-tree keys the attribute table places on run,
// model and tool spans alike.
func runSpanAttrs(info types.RunInfo) []types.Attr {
	return []types.Attr{
		types.String(types.KeyFlow, info.Flow),
		types.String(types.KeySessionID, info.SessionID),
		types.String(types.KeyRunID, info.RunID),
		types.String(types.KeyRootRunID, info.RootRunID),
		types.String(types.KeyParentRunID, info.ParentRunID),
		types.Int(types.KeyTurn, int64(info.Turn)),
	}
}

// countRecovered records one successful re-drive after a crash.
func countRecovered(ctx context.Context, tel types.Telemetry, run stores.Run) {
	if tel == nil {
		return
	}
	tel.Count(ctx, MetricRunRecovered, 1,
		types.String(types.KeyFlow, run.Flow),
	)
}

// countAbandoned records one run left Failed with Uncertain because no
// recovery runtime could re-drive it.
func countAbandoned(ctx context.Context, tel types.Telemetry, run stores.Run) {
	if tel == nil {
		return
	}
	tel.Count(ctx, MetricRunAbandoned, 1,
		types.String(types.KeyFlow, run.Flow),
	)
}
