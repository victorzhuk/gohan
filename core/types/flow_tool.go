package types

import "context"

// FlowAsTool is the typed seam a sub-flow is invoked through when its
// parent exposes it as a tool. Any flow value satisfies it structurally,
// so the floor never imports the driver, and the typed In is the only
// value that crosses into the child; the typed Out returns as the
// parent's tool result.
type FlowAsTool[In, Out any] interface {
	Invoke(ctx context.Context, in In) (Out, error)
}
