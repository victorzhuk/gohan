package gohantest

import (
	"testing"
	"testing/synctest"
)

// leakStub stands in for *testing.T so a test can observe the leak check
// failing without failing itself.
type leakStub struct {
	failed   bool
	messages []string
}

func (s *leakStub) Helper() {}

func (s *leakStub) Errorf(format string, args ...any) {
	s.failed = true
	s.messages = append(s.messages, format)
}

func (s *leakStub) Failed() bool { return s.failed }

// TestLeakCheckInBubble pins the profile's synctest correctness: the check
// spins rather than sleeping, so a drained function inside a bubble passes
// and the check spawns no goroutine of its own.
func TestLeakCheckInBubble(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		LeakCheck(t, func() {
			done := make(chan struct{})
			go func() { close(done) }()
			<-done
		})
	})
}
