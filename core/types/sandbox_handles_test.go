package types

import (
	"context"
	"testing"
)

type handlesProbe struct{}

func (handlesProbe) Acquire(context.Context, string, WorkspaceOwner) (Workspace, error) {
	return nil, nil
}

func (handlesProbe) Publish(context.Context, string, WorkspaceOwner) (WorkspaceSnapshot, error) {
	return WorkspaceSnapshot{}, nil
}

func (handlesProbe) Release(context.Context, string, WorkspaceOwner) error { return nil }

func (handlesProbe) Sweep(context.Context, int) error { return nil }

var (
	_ SandboxHandles = handlesProbe{}
	_ SandboxHandles = SandboxHandles(handlesProbe{})
)

func TestSandboxHandles(t *testing.T) {
	owner := WorkspaceOwner{RunID: "r1", LeaseGeneration: 2}
	if owner.RunID != "r1" || owner.LeaseGeneration != 2 {
		t.Fatalf("owner = %+v", owner)
	}
	snap := WorkspaceSnapshot{Ref: WorkspaceRef("ref-1"), Generation: 3}
	if snap.Ref != "ref-1" || snap.Generation != 3 {
		t.Fatalf("snapshot = %+v", snap)
	}

	ctx := WithSandboxHandles(context.Background(), handlesProbe{})
	got, ok := SandboxHandlesFrom(ctx)
	if !ok {
		t.Fatal("SandboxHandlesFrom lost the coordinator")
	}
	if _, ok := got.(handlesProbe); !ok {
		t.Fatalf("coordinator type = %T", got)
	}
	if _, ok := SandboxHandlesFrom(context.Background()); ok {
		t.Fatal("bare context resolved a coordinator")
	}
}
