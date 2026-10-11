package docker

import (
	"context"
	"testing"
)

func newTestSandbox(t *testing.T) *Sandbox {
	t.Helper()
	sb, err := New(context.Background(), "")
	if err != nil {
		t.Skipf("docker daemon unavailable: %v", err)
	}
	t.Cleanup(func() { _ = sb.c.Close() })
	return sb
}
