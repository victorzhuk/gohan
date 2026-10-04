package types

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
)

// TaintAction is the gate's verdict for a call whose arguments carry
// untrusted content.
type TaintAction int

const (
	TaintAllow TaintAction = iota
	TaintAsk
	TaintDeny
)

// ArgTaint records one argument whose value occurs verbatim in untrusted
// session content, with the origins of the blocks it matched.
type ArgTaint struct {
	Arg     string
	Origins []Origin
	Match   string
}

// TaintDenied reports the argument that made a call refuse to execute.
type TaintDenied struct {
	Arg     string
	Origins []Origin
}

func (e TaintDenied) Error() string {
	return fmt.Sprintf("gohan: argument %s carries untrusted content", e.Arg)
}

// TaintHook computes the taint verdict for a call's arguments. Core declares
// the slot only; the deterministic matcher and its policy are std/taint and
// land later, so a gate wired without a hook skips taint enforcement.
type TaintHook func(ctx context.Context, spec ToolSpec, args jsontext.Value) (TaintAction, []ArgTaint)
