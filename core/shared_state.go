package gohan

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// errStateOutsideRun marks SetSharedState on a context that carries no
// shared-state scope, i.e. outside a run.
var errStateOutsideRun = errors.New("gohan: shared state outside a run")

// stateScope is the run-scoped shared-state handle placed in ctx: the
// session's metadata surface, the session id and the currently loaded
// value and version.
type stateScope struct {
	meta      stores.SessionStateMeta
	sessionID string
	value     json.RawMessage
	version   int64
}

const ctxSharedState ctxKey = iota + 1

// WithSessionState loads the session's shared state from meta and returns
// a context that carries it. Send and Resume call this before driving a
// run, so SharedState and SetSharedState work on every pod the session
// metadata is reachable from.
func WithSessionState(ctx context.Context, meta stores.SessionStateMeta, sessionID string) (context.Context, error) {
	value, version, err := meta.SharedStateMeta(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return context.WithValue(ctx, ctxSharedState, &stateScope{
		meta:      meta,
		sessionID: sessionID,
		value:     value,
		version:   version,
	}), nil
}

const ctxStatePatch ctxKey = ctxSharedState + 1

// WithStatePatch binds the RFC 6902 patch one SetSharedState call emits.
// std/state computes it; core carries the type and never diffs.
func WithStatePatch(ctx context.Context, patch []types.PatchOp) context.Context {
	return context.WithValue(ctx, ctxStatePatch, patch)
}

func statePatchFrom(ctx context.Context) []types.PatchOp {
	patch, _ := ctx.Value(ctxStatePatch).([]types.PatchOp)
	return patch
}

// SharedState reports the run's shared state decoded into S and its
// version. ok is false outside a run; inside a run before the first
// SetSharedState it reports the zero value at version 0.
func SharedState[S any](ctx context.Context) (S, int64, bool) {
	var zero S
	sc, ok := ctx.Value(ctxSharedState).(*stateScope)
	if !ok {
		return zero, 0, false
	}
	var s S
	if len(sc.value) > 0 {
		if err := json.Unmarshal(sc.value, &s); err != nil {
			return zero, 0, false
		}
	}
	return s, sc.version, true
}

// SetSharedState persists next as the session's shared state under a
// version check and emits one StateChanged carrying the bound patch, if
// any, on the run's event sink. The returned version is monotonic per
// session and survives resume through the session metadata.
func SetSharedState[S any](ctx context.Context, next S) (int64, error) {
	sc, ok := ctx.Value(ctxSharedState).(*stateScope)
	if !ok {
		return 0, errStateOutsideRun
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return 0, err
	}
	version, err := sc.meta.SetSharedStateMeta(ctx, sc.sessionID, sc.version, raw)
	if err != nil {
		return 0, err
	}
	sc.value, sc.version = raw, version
	if sink, ok := types.SinkFrom(ctx); ok {
		sink.Emit(ctx, types.StateChanged{Version: version, Patch: statePatchFrom(ctx)})
	}
	return version, nil
}
