package gohan

import "sync/atomic"

// DefaultStreamBuffer is the per-call chunk capacity: a provider that
// produces more than this while the consumer stalls blocks on its next
// read instead of racing ahead (streams, *Slow consumers* rule 2).
const DefaultStreamBuffer = 64

// MetricStreamBufferFull names the counter a transport reads to report a
// provider read that blocked on a full buffer.
const MetricStreamBufferFull = "gohan.stream.buffer_full"

// StreamBuffer is the bounded hand-off between the provider-read helper
// goroutine and the consumer's yield. A send into a full buffer blocks
// the provider read; no chunk is dropped or reordered, and every blocked
// send is counted.
type StreamBuffer struct {
	ch     chan pulled
	full   atomic.Int64
	onFull func()
}

// NewStreamBuffer builds a buffer of capacity chunks; a non-positive
// capacity falls back to DefaultStreamBuffer.
func NewStreamBuffer(capacity int) *StreamBuffer {
	if capacity <= 0 {
		capacity = DefaultStreamBuffer
	}
	return &StreamBuffer{ch: make(chan pulled, capacity)}
}

// send delivers p. It returns false only when done closed first; the
// chunk the caller held is dropped with the whole call.
func (b *StreamBuffer) send(p pulled, done <-chan struct{}) bool {
	select {
	case b.ch <- p:
		return true
	default:
	}
	if b.onFull != nil {
		b.onFull()
	}
	select {
	case b.ch <- p:
		return true
	case <-done:
		return false
	}
}

// close releases the buffer's producer side. Chunk values already in the
// buffer stay readable; a receive on the closed channel reports not-ok
// once it is drained.
func (b *StreamBuffer) close() { close(b.ch) }

// FullCount reports how many provider reads blocked on a full buffer
// under MetricStreamBufferFull.
func (b *StreamBuffer) FullCount() int64 { return b.full.Load() }
