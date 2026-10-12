package chains

import "github.com/victorzhuk/gohan/core/types"

// ExtraKey is the Options.Extra key under which the assembler marks a
// served or written prefix. The charge site reads it to fill the Usage
// fields the pricing seam consumes; it is set after the key is computed,
// so it never enters a key.
const ExtraKey = "gohan.cache"

// Outcome reports one consult of the store: a hit served a stored prefix,
// a repopulating miss wrote one. Tokens is the heuristic estimate of the
// prefix the outcome covers.
type Outcome struct {
	Hit    bool
	Tokens int
}

// Mark returns req with the outcome recorded in Options.Extra. The Extra
// map is always copied, so a stored entry never observes a caller's mark.
func Mark(req types.ModelRequest, o Outcome) types.ModelRequest {
	extra := make(map[string]any, len(req.Options.Extra)+1)
	for k, v := range req.Options.Extra {
		extra[k] = v
	}
	extra[ExtraKey] = o
	req.Options.Extra = extra
	return req
}

// OutcomeOf reports the marked outcome of an assembled request.
func OutcomeOf(req types.ModelRequest) (Outcome, bool) {
	o, ok := req.Options.Extra[ExtraKey].(Outcome)
	return o, ok
}
