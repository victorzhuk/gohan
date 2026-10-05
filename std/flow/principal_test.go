package flow

import (
	"context"

	"github.com/victorzhuk/gohan/core/types"
)

func gohanctx() context.Context {
	return types.WithPrincipal(context.Background(), types.Principal{Subject: "tester"})
}
