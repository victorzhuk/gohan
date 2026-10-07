package gohan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// WithFlags binds the flags provider. A flow built without one evaluates
// no flags: no frozen snapshot is taken and the live-control slot and the
// model-chain step install only when a provider is wired.
func WithFlags(f types.Flags) Option {
	return func(c *config) error {
		c.flags = f
		return nil
	}
}

// WithFrozenFlags declares the frozen flag keys and their defaults. The
// values are evaluated once at run start with the session hash as the
// targeting key; every evaluation inside the run reads the snapshot.
func WithFrozenFlags(frozen map[string]any) Option {
	f := make(map[string]any, len(frozen))
	for k, v := range frozen {
		f[k] = v
	}
	return func(c *config) error {
		c.frozen = f
		return nil
	}
}

// Flags returns the flags provider Build resolved.
func (s *Stack) Flags() types.Flags { return s.flags }

// FrozenFlags returns the frozen class Build resolved.
func (s *Stack) FrozenFlags() map[string]any { return s.frozen }

// TargetingKey hashes the session into the sticky targeting key the frozen
// class evaluates with.
func TargetingKey(session string) string {
	sum := sha256.Sum256([]byte(session))
	return hex.EncodeToString(sum[:])
}

// SnapshotFlags evaluates the frozen class once with the session hash as
// the targeting key and returns the snapshot the run, the checkpoint and
// the run-start audit record carry.
func SnapshotFlags(ctx context.Context, f types.Flags, fc types.FlagContext, frozen map[string]any) (map[string]any, error) {
	target := fc
	target.Session = TargetingKey(fc.Session)
	snap := make(map[string]any, len(frozen))
	for key, def := range frozen {
		switch def := def.(type) {
		case bool:
			v, _, err := f.Bool(ctx, key, def, target)
			if err != nil {
				return nil, fmt.Errorf("flag %s: %w", key, err)
			}
			snap[key] = v
		case string:
			v, _, err := f.String(ctx, key, def, target)
			if err != nil {
				return nil, fmt.Errorf("flag %s: %w", key, err)
			}
			snap[key] = v
		case int64:
			v, _, err := f.Int(ctx, key, def, target)
			if err != nil {
				return nil, fmt.Errorf("flag %s: %w", key, err)
			}
			snap[key] = v
		case int:
			v, _, err := f.Int(ctx, key, int64(def), target)
			if err != nil {
				return nil, fmt.Errorf("flag %s: %w", key, err)
			}
			snap[key] = v
		default:
			return nil, fmt.Errorf("flag %s: unsupported frozen type %T", key, def)
		}
	}
	return snap, nil
}

// AssertFlags asserts a recorded frozen snapshot against the current
// provider. Any key the provider cannot honour at its recorded value is
// flag drift: replay returns the error before any model call instead of
// re-evaluating.
func AssertFlags(snap map[string]any, f types.Flags, fc types.FlagContext) error {
	target := fc
	target.Session = TargetingKey(fc.Session)
	for key, want := range snap {
		drift := func(err error) error {
			if err != nil {
				return fmt.Errorf("flag %s: %w: %w", key, err, types.ErrFlagDrift)
			}
			return types.FlagDrift{Key: key}
		}
		switch want := want.(type) {
		case bool:
			got, _, err := f.Bool(context.Background(), key, !want, target)
			if err != nil || got != want {
				return drift(err)
			}
		case string:
			got, _, err := f.String(context.Background(), key, want+"-drift", target)
			if err != nil || got != want {
				return drift(err)
			}
		case int64:
			got, _, err := f.Int(context.Background(), key, want+1, target)
			if err != nil || got != want {
				return drift(err)
			}
		case int:
			got, _, err := f.Int(context.Background(), key, int64(want)+1, target)
			if err != nil || got != int64(want) {
				return drift(err)
			}
		default:
			return types.FlagDrift{Key: key}
		}
	}
	return nil
}
