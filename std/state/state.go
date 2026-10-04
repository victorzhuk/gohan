// Package state computes RFC 6902 JSON Patch operations for shared state
// updates. core carries the PatchOp type and owns the version counter and
// the StateChanged emission; this package turns a (previous, next) pair
// into the patch that transforms one into the other. It holds no state of
// its own and is safe for concurrent use.
package state

import (
	"github.com/victorzhuk/gohan/core/types"
)

// Update returns the StateChanged event body for one shared-state update:
// the given version paired with the RFC 6902 patch that transforms prev
// into next. The caller owns the monotonic version counter and the
// emission; when the values are equal the patch is empty.
func Update(version int64, prev, next any) (types.StateChanged, error) {
	patch, err := Patch(prev, next)
	if err != nil {
		return types.StateChanged{}, err
	}
	return types.StateChanged{Version: version, Patch: patch}, nil
}
