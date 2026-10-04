package gohan

import (
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestRunLimits(t *testing.T) {
	t.Run("limits.unbounded-rejected", func(t *testing.T) {
		_, err := Build(WithLimits("chat", types.RunLimits{}))
		if err == nil {
			t.Fatal("zero limits built without a preset")
		}
		if !strings.Contains(err.Error(), "chat") {
			t.Errorf("err %q does not name the flow", err)
		}
	})

	t.Run("preset-installs-table", func(t *testing.T) {
		s, err := Build(WithLimits("chat", types.InteractiveLimits))
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		l, ok := s.Limits("chat")
		if !ok {
			t.Fatal("preset not installed")
		}
		want := types.InteractiveLimits
		if l.MaxTurns != want.MaxTurns || l.MaxToolCalls != want.MaxToolCalls ||
			l.MaxWallClock != want.MaxWallClock || l.SoftRatio != want.SoftRatio ||
			l.MaxDepth != want.MaxDepth || l.MaxParallelChildren != want.MaxParallelChildren ||
			l.MaxParallelTools != want.MaxParallelTools || l.ConsumerStall != want.ConsumerStall {
			t.Errorf("resolved = %+v, want table %+v", l, want)
		}
	})

	t.Run("zero-field-from-preset", func(t *testing.T) {
		s, err := Build(WithLimits("chat", types.InteractiveLimits))
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		l, _ := s.Limits("chat")
		if l.MaxBlobBytes != 256<<20 {
			t.Errorf("MaxBlobBytes = %d, want the 256 MiB default", l.MaxBlobBytes)
		}
		if l.MaxCost != 0 || l.MaxSandboxSeconds != 0 {
			t.Errorf("unbounded fields = %g/%g, want 0/0", l.MaxCost, l.MaxSandboxSeconds)
		}
	})

	t.Run("partial-set-still-refused", func(t *testing.T) {
		_, err := Build(WithLimits("agent", types.RunLimits{MaxTurns: 50}))
		if err == nil {
			t.Fatal("partially-zero limits built without a preset")
		}
		if !strings.Contains(err.Error(), "agent") {
			t.Errorf("err %q does not name the flow", err)
		}
	})
}
