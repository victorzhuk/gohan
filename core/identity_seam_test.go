package gohan

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestPrincipalSeam(t *testing.T) {
	t.Run("identity.no-principal", func(t *testing.T) {
		err := requirePrincipal(context.Background(), false)
		if !errors.Is(err, types.ErrNoPrincipal) {
			t.Fatalf("flow without AllowAnonymous invoked without a principal: got %v, want ErrNoPrincipal", err)
		}

		if err := requirePrincipal(context.Background(), true); err != nil {
			t.Fatalf("flow with AllowAnonymous invoked without a principal: got %v, want nil", err)
		}

		if err := requirePrincipal(WithPrincipal(context.Background(), types.Principal{Subject: "u"}), false); err != nil {
			t.Fatalf("flow invoked with a principal: got %v, want nil", err)
		}
	})

	t.Run("identity.no-principal-at-seam", func(t *testing.T) {
		// The seam refuses before any event or store access; the check takes
		// only the context, so nothing downstream can have been reached.
		if err := requirePrincipal(WithIdempotencyKey(context.Background(), "k"), false); !errors.Is(err, types.ErrNoPrincipal) {
			t.Fatalf("principal-less ctx with other values: got %v, want ErrNoPrincipal", err)
		}
	})
}
