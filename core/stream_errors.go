package gohan

import (
	"errors"
	"iter"
)

// ErrStreamInterrupted reports a stream that closed without a Done event and
// without a terminal error tuple, the in-process form of the interrupted
// stream a transport reports as gohan.stream_interrupted.
var ErrStreamInterrupted = errors.New("gohan: stream closed without Done or a terminal error")

// Terminal is the sequence a stream seam yields after a failure: exactly one
// tuple carrying the zero event and err, and no tuple after it. The
// error-tuple protocol makes an error tuple always the last tuple; the seam
// funnels every failure through here so a second tuple after the error
// cannot be emitted.
func Terminal(err error) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		yield(nil, err)
	}
}

// Preflight is the sole-tuple sequence for a refusal that happens before the
// run starts (ErrNoPrincipal, ErrSessionForbidden, ErrRunActive,
// ErrTokenConsumed, ErrTokenMismatch, ErrResumeInsideRun, a lazily surfaced
// Build error): one (nil, err) tuple, no event, no run.
func Preflight(err error) iter.Seq2[Event, error] {
	return Terminal(err)
}
