package gohan

import (
	"context"

	"github.com/victorzhuk/gohan/core/guards"
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
	MetricRunRecovered    = "gohan.run.recovered"
	MetricRunAbandoned    = "gohan.run.abandoned"
	MetricToolUnknown     = "gohan.tool.unknown"
	MetricToolInvalidArgs = "gohan.tool.invalid_args"
)

func withTelemetry(ctx context.Context, tel types.Telemetry) context.Context {
	return types.WithTelemetry(ctx, tel)
}

// telemetryFrom returns the run context's port; nil outside a run, which
// every call site treats as a no-op.
func telemetryFrom(ctx context.Context) types.Telemetry {
	return types.TelemetryFrom(ctx)
}

// startRunSpan opens the run span for the run info in ctx. A nil port
// returns the ctx unchanged with a no-op end, so emission is never a
// panic and never a branch the callers repeat.
func startRunSpan(ctx context.Context, tel types.Telemetry) (context.Context, func(...types.Attr)) {
	info, ok := types.RunInfoFrom(ctx)
	if !ok {
		return ctx, func(...types.Attr) {}
	}
	return startSpan(ctx, tel, SpanRun, runSpanAttrs(info)...)
}

// startSpan opens a child span under the run tree. A nil port returns the
// ctx unchanged with a no-op end, so emission is never a panic and never a
// branch the callers repeat.
func startSpan(ctx context.Context, tel types.Telemetry, name string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	if tel == nil {
		return ctx, func(...types.Attr) {}
	}
	return tel.StartSpan(ctx, name, attrs...)
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

// ConsultGuard runs one guard consultation inside a gohan.guard span, at
// the boundary every Guard implementation passes through. A nil port opens
// no span.
func ConsultGuard(ctx context.Context, g guards.Guard, in guards.GuardInput) (types.Decision[guards.GuardVerdict], error) {
	tel := telemetryFrom(ctx)
	sctx, end := startSpan(ctx, tel, SpanGuard,
		types.String(types.KeyGuardStage, guardStageName(in.Stage)),
	)
	d, err := g.Decide(sctx, in)
	if err != nil {
		end(types.String(types.KeyGuardVerdict, "error"))
		return d, err
	}
	end(types.String(types.KeyGuardVerdict, guardVerdictName(d.Value)))
	return d, nil
}

func guardStageName(s types.GuardStage) string {
	switch s {
	case types.StageInput:
		return "input"
	case types.StageToolResult:
		return "tool_result"
	case types.StageContext:
		return "context"
	default:
		return "output"
	}
}

func guardVerdictName(v guards.GuardVerdict) string {
	switch v.Action {
	case guards.Pass:
		return "pass"
	case guards.Rewrite:
		return "rewrite"
	default:
		return "block"
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
