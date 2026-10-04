package gohan

import (
	"errors"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

var (
	// ErrFidelityUndeclared names a block kind the profile's fidelity
	// matrix leaves undeclared.
	ErrFidelityUndeclared = errors.New("gohan: fidelity undeclared for block kind")
	// ErrFidelityDropped names a block kind the profile drops without
	// the flow's opt-in.
	ErrFidelityDropped = errors.New("gohan: block kind dropped by profile")
	// ErrOpaqueCompaction names adapter-side opaque compaction whose
	// output cannot follow the flow across providers.
	ErrOpaqueCompaction = errors.New("gohan: opaque compaction cannot cross providers")
)

// defaultPreserved are the kinds every profile carries without a
// declaration; the spec fixes them as Preserved.
var defaultPreserved = map[types.BlockKind]bool{
	types.KindText:       true,
	types.KindToolUse:    true,
	types.KindToolResult: true,
}

// CheckFidelity is the build-time fidelity gate: every block kind the flow
// can emit must be declared in the profile's Caps.Fidelity, and a declared
// Dropped kind fails the build unless the flow opts in via AllowDrop. The
// gate runs against the primary profile and, when the flow names one, its
// fallback — the flow can reach either with a request.
func (s *Stack) CheckFidelity(req FlowRequest, profiles map[string]types.ModelProfile, primary string) error {
	p, ok := profiles[primary]
	if !ok {
		return fmt.Errorf("flow %q: profile %q not configured", req.Name, primary)
	}
	if err := s.checkFidelityProfile(req, p); err != nil {
		return err
	}
	if req.Fallback == "" {
		return nil
	}
	fb, ok := profiles[req.Fallback]
	if !ok {
		return fmt.Errorf("flow %q: fallback profile %q: %w", req.Name, req.Fallback, ErrFallbackUnknown)
	}
	if err := s.checkFidelityProfile(req, fb); err != nil {
		return err
	}
	if req.ProviderCompact &&
		p.Caps.Compaction == types.CompactionOpaqueCap &&
		p.Name != fb.Name {
		return fmt.Errorf("flow %q: profile %q: strategy %q: %w",
			req.Name, p.Name, "provider_compact", ErrOpaqueCompaction)
	}
	return nil
}

func (s *Stack) checkFidelityProfile(req FlowRequest, p types.ModelProfile) error {
	for _, kind := range req.Blocks {
		if defaultPreserved[kind] {
			continue
		}
		f, ok := p.Caps.Fidelity[kind]
		if !ok {
			return fmt.Errorf("flow %q: profile %q: kind %q: %w",
				req.Name, p.Name, kind, ErrFidelityUndeclared)
		}
		if f == types.Dropped && !containsKind(req.AllowDrop, kind) {
			return fmt.Errorf("flow %q: profile %q: kind %q: %w",
				req.Name, p.Name, kind, ErrFidelityDropped)
		}
	}
	return nil
}

func containsKind(kinds []types.BlockKind, kind types.BlockKind) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}
