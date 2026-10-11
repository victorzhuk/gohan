// Package stores declares session-history storage and the assembly-time
// projection seam over it.
package stores

import (
	"context"

	"github.com/victorzhuk/gohan/core/types"
)

// Projection returns a reduced view of a session's history for one model
// request. A projection is deterministic for identical (History.Version,
// ModelProfile) inputs and never writes: the seam carries no store handle,
// so there is nothing to persist to. Declared beside History because the
// method set names History and this package already imports core/types.
type Projection interface {
	Project(ctx context.Context, h History, p types.ModelProfile) (History, error)
}

// Compactor folds history into a persisted, smaller form. Unlike a
// Projection it may record its result, so the context policy carries it as
// a separate stage and the persisted-compaction work supplies the only
// implementation.
type Compactor interface {
	Compact(ctx context.Context, h History, p types.ModelProfile) (types.Compaction, error)
}

// ContextPolicy is the ordered projection list a flow applies before its
// caller assembler, plus the optional compactor hook.
type ContextPolicy struct {
	Projections []Projection
	Compactor   Compactor
}
