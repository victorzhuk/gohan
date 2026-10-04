package structured

import (
	"encoding/json"

	"github.com/victorzhuk/gohan/core/types"
)

// reasonFirstSlot is the frozen ModelOptions.Extra key the constrained
// schema moves into. No spec field declares it; the name is frozen here.
const reasonFirstSlot = "reason_first"

// ReasonFirst is the structured-output option for a Constrained call: the
// provider runs an unconstrained reasoning phase before the constrained
// output block, so reasoning is never squeezed into the schema.
type ReasonFirst struct{}

// NewReasonFirst freezes the option value the caller passes into Build.
func NewReasonFirst() ReasonFirst {
	return ReasonFirst{}
}

// Options reorders the request: the constrained schema moves from the
// top-level ResponseSchema into the reasoning-first envelope, so the
// reasoning slot comes first on the wire and the constraint applies only to
// the final block. A request without a response schema is returned
// unchanged: there is nothing to reorder.
func (ReasonFirst) Options(o types.ModelOptions) (types.ModelOptions, bool) {
	if len(o.ResponseSchema) == 0 {
		return o, false
	}
	if o.Extra == nil {
		o.Extra = map[string]any{}
	}
	o.Extra[reasonFirstSlot] = json.RawMessage(o.ResponseSchema)
	o.ResponseSchema = nil
	return o, true
}

// Explain reports the resolved flag the driver's Explanation embeds. The
// driver-side Explain entry lands with the runtime row; the string this
// option owns is frozen here.
func (ReasonFirst) Explain() string {
	return "ReasonFirst: on"
}
