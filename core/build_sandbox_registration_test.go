package gohan

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

// fakeLifecycleHandles is a minimal SandboxHandles for registration tests.
type fakeLifecycleHandles struct{}

func (fakeLifecycleHandles) Acquire(context.Context, string, types.WorkspaceOwner) (types.Workspace, error) {
	return nil, types.ErrPerSessionUnsupported
}

func (fakeLifecycleHandles) Publish(context.Context, string, types.WorkspaceOwner) (types.WorkspaceSnapshot, error) {
	return types.WorkspaceSnapshot{}, types.ErrPerSessionUnsupported
}

func (fakeLifecycleHandles) Release(context.Context, string, types.WorkspaceOwner) error {
	return types.ErrPerSessionUnsupported
}

func (fakeLifecycleHandles) Sweep(context.Context, int) error { return nil }

func TestBuildWiresSandboxCoordinator(t *testing.T) {
	reg := SandboxRegistration{
		Handles: fakeLifecycleHandles{},
		Policy:  types.SandboxPolicy{Lifecycle: types.PerCall},
	}
	s, err := Build(WithSandboxRegistration(reg))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if s.SandboxHandles() != types.SandboxHandles(reg.Handles) {
		t.Fatalf("stack holds coordinator %v, want the registered instance", s.SandboxHandles())
	}
	if s.SandboxPolicy().Lifecycle != reg.Policy.Lifecycle {
		t.Fatalf("stack policy lifecycle = %v, want %v", s.SandboxPolicy().Lifecycle, reg.Policy.Lifecycle)
	}

	// Without a registration the stack holds no coordinator.
	bare, err := Build()
	if err != nil {
		t.Fatalf("bare build: %v", err)
	}
	if bare.SandboxHandles() != nil {
		t.Fatalf("bare build coordinator = %v, want nil", bare.SandboxHandles())
	}
}

func TestBuildRefusesIncapablePerSession(t *testing.T) {
	t.Run("sandbox.build-refuses-incapable-persession", func(t *testing.T) {
		policy := types.SandboxPolicy{Lifecycle: types.PerSession}

		// A PerSession policy with no coordinator is refused.
		if s, err := Build(WithSandboxRegistration(SandboxRegistration{Policy: policy})); !errors.Is(err, types.ErrPerSessionUnsupported) || s != nil {
			t.Fatalf("no coordinator: stack = %v err = %v, want nil, ErrPerSessionUnsupported", s, err)
		}

		// A coordinator without a durable metadata store on the log is refused.
		reg := SandboxRegistration{Handles: fakeLifecycleHandles{}, Policy: policy}
		if s, err := Build(WithSandboxRegistration(reg)); !errors.Is(err, types.ErrPerSessionUnsupported) || s != nil {
			t.Fatalf("no metadata store: stack = %v err = %v, want nil, ErrPerSessionUnsupported", s, err)
		}
	})
}
