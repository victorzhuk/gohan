package gohan

import "iter"

// Collect drains seq and returns everything yielded before a terminal error
// together with that error. The protocol guarantees the error tuple carries
// the zero T, so nothing is appended for it.
func Collect[T any](seq iter.Seq2[T, error]) ([]T, error) {
	var vs []T
	for v, err := range seq {
		if err != nil {
			return vs, err
		}
		vs = append(vs, v)
	}
	return vs, nil
}

// Last drains an event stream and returns its Done event, or the terminal
// error. A stream that closes without Done and without a terminal error
// violates the protocol and reports ErrStreamInterrupted.
func Last(seq iter.Seq2[Event, error]) (Done, error) {
	var done Done
	saw := false
	for e, err := range seq {
		if err != nil {
			return Done{}, err
		}
		if d, ok := e.(Done); ok {
			done, saw = d, true
		}
	}
	if !saw {
		return Done{}, ErrStreamInterrupted
	}
	return done, nil
}

// Drain consumes seq and returns only the terminal error, nil when the
// stream completes. It is the helper for a caller that discards the values.
func Drain[T any](seq iter.Seq2[T, error]) error {
	for _, err := range seq {
		if err != nil {
			return err
		}
	}
	return nil
}
