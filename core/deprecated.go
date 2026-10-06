package gohan

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"sort"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

const (
	metricSunsetWarning   = "gohan.model.sunset_warning"
	metricDeprecatedCalls = "gohan.model.deprecated_calls"
	metricDeprecatedRoute = "gohan.model.deprecated_routed"
)

// checkSunsets fails the build against a profile whose Sunset falls inside
// the notice window. A zero Sunset means no announced retirement. Under
// warnOnly the failure becomes a warning record and a sunset_warning
// emission.
func checkSunsets(logger *slog.Logger, tel types.Telemetry, window time.Duration, warnOnly bool, profiles map[string]types.ModelProfile) error {
	if window <= 0 {
		window = DefaultSunsetNoticeWindow
	}
	now := time.Now()
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := profiles[name]
		if p.Sunset.IsZero() || now.Add(window).Before(p.Sunset) {
			continue
		}
		succ := p.Successor
		if succ == "" {
			succ = "none"
		}
		if !warnOnly {
			return fmt.Errorf("gohan: profile %q: sunset %s inside %s notice window (successor %s)",
				name, p.Sunset.Format(time.DateOnly), window, succ)
		}
		if logger != nil {
			logger.Warn("gohan: sunset inside notice window",
				"profile", name, "successor", succ, "sunset", p.Sunset.Format(time.DateOnly))
		}
		if tel != nil {
			tel.Count(context.Background(), metricSunsetWarning, 1,
				types.String(types.KeyModelProfile, name))
		}
	}
	return nil
}

// checkSuccessorOptions rejects a flow's static ModelOptions the declared
// successor cannot serve: a knob the successor's Caps lacks fails the build
// naming the option, before any provider call.
func checkSuccessorOptions(flow string, opts types.ModelOptions, p types.ModelProfile, profiles map[string]types.ModelProfile) error {
	if p.Successor == "" {
		return nil
	}
	succ, ok := profiles[p.Successor]
	if !ok {
		return fmt.Errorf("flow %q: profile %q: successor %q not configured", flow, p.Name, p.Successor)
	}
	switch {
	case opts.Temperature != nil && !succ.Caps.Temperature:
		return fmt.Errorf("flow %q: successor %q lacks option %q", flow, p.Successor, "temperature")
	case opts.ToolChoice != "" && opts.ToolChoice != types.ToolChoiceNone && !succ.Caps.Tools:
		return fmt.Errorf("flow %q: successor %q lacks option %q", flow, p.Successor, "tool_choice")
	case len(opts.ResponseSchema) > 0 && !succ.Caps.Constrained:
		return fmt.Errorf("flow %q: successor %q lacks option %q", flow, p.Successor, "response_format")
	}
	return nil
}

// deprecatedRoute wraps one model call so a ClassDeprecated provider error
// is never silent: the call reroutes to the profile's declared successor,
// and without one the original ClassDeprecated error surfaces with no
// fallback assistant message.
func deprecatedRoute(tel types.Telemetry, profile types.ModelProfile, next types.ModelFunc, successor types.ModelFunc) types.ModelFunc {
	count := func(ctx context.Context, name string, attrs ...types.Attr) {
		if tel != nil {
			tel.Count(ctx, name, 1, attrs...)
		}
	}
	return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			next(ctx, req)(func(c types.ModelChunk, err error) bool {
				if err == nil {
					return yield(c, nil)
				}
				me, ok := err.(*types.ModelError)
				if !ok || me.Class != types.ClassDeprecated {
					return yield(c, err)
				}
				count(ctx, metricDeprecatedCalls, types.String(types.KeyModelProfile, profile.Name))
				if successor == nil {
					return yield(c, err)
				}
				count(ctx, metricDeprecatedRoute,
					types.String(types.KeyModelProfile, profile.Name),
					types.String("successor", profile.Successor))
				successor(ctx, req)(yield)
				return false
			})
		}
	}
}
