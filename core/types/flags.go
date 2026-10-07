package types

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrFlagDrift is the replay assertion's error class: a run's recorded
// frozen snapshot cannot be honoured by the current provider. Replay
// returns it instead of re-evaluating, before any model call.
var ErrFlagDrift = errors.New("gohan: frozen flag snapshot cannot be honoured")

// FlagDrift names the key whose recorded value the provider no longer
// reports; it wraps ErrFlagDrift.
type FlagDrift struct{ Key string }

func (e FlagDrift) Error() string {
	return fmt.Sprintf("gohan: flag %s drifted from the recorded snapshot", e.Key)
}

func (e FlagDrift) Unwrap() error { return ErrFlagDrift }

// FlagContext targets one flag evaluation.
type FlagContext struct {
	Tenant  string
	Subject string
	Session string
	Flow    string
}

// Freshness reports when a live evaluation happened and whether it fell
// back to the provider default.
type Freshness struct {
	EvaluatedAt time.Time
	FromDefault bool
}

// Flags is the flags port. Core declares it; the provider that answers it
// lives in std/flags or an adapter.
type Flags interface {
	Bool(ctx context.Context, key string, def bool, fc FlagContext) (bool, Freshness, error)
	String(ctx context.Context, key string, def string, fc FlagContext) (string, Freshness, error)
	Int(ctx context.Context, key string, def int64, fc FlagContext) (int64, Freshness, error)
}
