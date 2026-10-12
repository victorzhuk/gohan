package gohan_test

import (
	"reflect"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
)

// TestPolicyMirrorsMatchStd pins the core compatibility mirrors to the std
// policy values they mirror: neither side may change without the other.
func TestPolicyMirrorsMatchStd(t *testing.T) {
	if std.SunsetNoticeWindow != gohan.DefaultSunsetNoticeWindow {
		t.Errorf("sunset notice window: std = %s, core mirror = %s",
			std.SunsetNoticeWindow, gohan.DefaultSunsetNoticeWindow)
	}
	if std.ControlWait != permission.DefaultControlWait {
		t.Errorf("control wait: std = %s, core mirror = %s",
			std.ControlWait, permission.DefaultControlWait)
	}
	if std.StreamBuffer != gohan.DefaultStreamBuffer {
		t.Errorf("stream buffer: std = %d, core mirror = %d",
			std.StreamBuffer, gohan.DefaultStreamBuffer)
	}
	want := map[types.LatencyClass][3]time.Duration{
		types.Interactive: {5 * time.Second, 10 * time.Second, 15 * time.Second},
		types.Agentic:     {10 * time.Second, 30 * time.Second, 60 * time.Second},
		types.Batch:       {30 * time.Second, 2 * time.Minute, 5 * time.Minute},
	}
	if !reflect.DeepEqual(std.LatencyTimeouts, want) {
		t.Errorf("std.LatencyTimeouts drifted from the core mirror table: got %v, want %v",
			std.LatencyTimeouts, want)
	}
}
