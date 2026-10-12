package gohan

import (
	"reflect"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// TestDefaultTimeoutsMirror pins the unexported timeout mirror to the same
// table std.LatencyTimeouts documents; the cross-package half lives in
// defaults_parity_test.go.
func TestDefaultTimeoutsMirror(t *testing.T) {
	want := map[types.LatencyClass][3]time.Duration{
		types.Interactive: {5 * time.Second, 10 * time.Second, 15 * time.Second},
		types.Agentic:     {10 * time.Second, 30 * time.Second, 60 * time.Second},
		types.Batch:       {30 * time.Second, 2 * time.Minute, 5 * time.Minute},
	}
	if !reflect.DeepEqual(defaultTimeouts, want) {
		t.Errorf("defaultTimeouts = %v, want %v", defaultTimeouts, want)
	}
}
