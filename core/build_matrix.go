package gohan

import (
	"log/slog"
	"sort"
)

// matrixEntry is one flow × profile × strategy resolution the build
// resolved. The names stay internal: the record shape is the spec's
// prose rule, not a declared contract.
type matrixEntry struct {
	Flow             string
	Profile          string
	Fallback         string
	Structured       string
	ParallelTools    bool
	MaxParallelTools int
}

func entryName(s StructuredStrategy) string {
	switch s {
	case StructuredConstrained:
		return "constrained"
	case StructuredToolSchema:
		return "tool_schema"
	default:
		return "none"
	}
}

// logResolvedMatrix writes the one structured record that lists every
// flow × profile × strategy resolution (build.resolved-matrix). Build
// logs it once at startup, at Info, and never emits a metric for it.
func logResolvedMatrix(l *slog.Logger, entries []matrixEntry) {
	if l == nil {
		return
	}
	attrs := make([]any, 0, len(entries))
	for _, e := range entries {
		attrs = append(attrs, slog.Any(e.Flow, e))
	}
	l.Info("resolved strategy matrix", attrs...)
}

// nativeMatrixEntries projects every resolved native definition into the
// startup record, in flow order so the log is deterministic across builds.
func nativeMatrixEntries(native map[string]*resolvedNativeConfig) []matrixEntry {
	entries := make([]matrixEntry, 0, len(native))
	for _, cfg := range native {
		entries = append(entries, matrixEntry{
			Flow:             cfg.flow,
			Profile:          cfg.profile.Name,
			Fallback:         cfg.request.Fallback,
			Structured:       entryName(cfg.plan.Structured),
			ParallelTools:    cfg.plan.ParallelTools,
			MaxParallelTools: cfg.plan.MaxParallelTools,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Flow < entries[j].Flow })
	return entries
}
