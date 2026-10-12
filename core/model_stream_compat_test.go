package gohan

import "testing"

// TestNewModelStreamLegacyShape pins the frozen v1 call shape: the exported
// constructor takes a model and no options, and an un-optioned stream leaves
// the packaged fallbacks active exactly as the pre-option constructor did,
// with option plumbing living on the unexported newModelStream.
func TestNewModelStreamLegacyShape(t *testing.T) {
	s := NewModelStream(nil)
	if s == nil {
		t.Fatal("NewModelStream(nil) returned nil")
	}
	if s.timeouts != nil {
		t.Fatalf("legacy shape set timeouts = %v, want nil fallback", s.timeouts)
	}
	if s.buffer != 0 {
		t.Fatalf("legacy shape set buffer = %d, want 0 fallback", s.buffer)
	}
	internal := newModelStream(nil)
	if len(internal.timeouts) != len(s.timeouts) || internal.buffer != s.buffer {
		t.Fatalf("exported and internal no-option constructors diverge: %v/%d vs %v/%d",
			internal.timeouts, internal.buffer, s.timeouts, s.buffer)
	}
}
