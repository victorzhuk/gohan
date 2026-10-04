package gohan

import (
	"context"

	"github.com/victorzhuk/gohan/core/types"
)

// requirePrincipal is the seam check every entry point (Send, Invoke,
// Resume) runs before emitting RunStarted and before any store access. It
// fails with ErrNoPrincipal unless the flow was built with anonymous
// invocation allowed.
func requirePrincipal(ctx context.Context, allowAnonymous bool) error {
	if allowAnonymous {
		return nil
	}
	if _, ok := PrincipalFrom(ctx); !ok {
		return types.ErrNoPrincipal
	}
	return nil
}
