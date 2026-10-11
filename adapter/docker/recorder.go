package docker

import (
	"sync"

	"github.com/victorzhuk/gohan/core/stores"
)

// Recorder is the adapter's observation sink (D4, D8): the count of
// egress refusals and the audit kinds the governed path reported. It
// carries no URL, path, query, user-info or body (ADR-0156) and emits no
// metric name; the denial-counter reconciliation is the conformance
// change's open item O1.
type Recorder struct {
	mu     sync.Mutex
	denied int
	kinds  []stores.AuditKind
}

// NewRecorder returns an empty Recorder.
func NewRecorder() *Recorder { return &Recorder{} }

// RecordDenial counts one observed refusal and appends the governed
// audit kind.
func (r *Recorder) RecordDenial() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.denied++
	r.kinds = append(r.kinds, stores.AuditEgressDenied)
}

// Reset clears the counts and kinds.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.denied = 0
	r.kinds = nil
}

// Denied is the refusal count.
func (r *Recorder) Denied() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.denied
}

// Kinds is the audit kinds the refusals produced.
func (r *Recorder) Kinds() []stores.AuditKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]stores.AuditKind(nil), r.kinds...)
}
