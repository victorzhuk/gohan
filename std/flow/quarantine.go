package flow

import (
	"context"
	"encoding/json"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/structured"
)

// Quarantine returns a Flow over document blocks that runs the Extract
// decode, schema and repair path but never offers a tool. On success it
// returns the validated value together with the distinct Origin values of
// the input blocks it read; the call path, not the tool author, sets
// provenance, so the driver stamps the result origin. Construction
// problems surface at Invoke, before any provider call.
func Quarantine[Out any](s *gohan.Stack, profile string, opts ...Option) gohan.Flow[[]types.Block, types.Quarantined[Out]] {
	c := newConfig(opts)
	schema, schemaErr := schemaFor[Out]()
	if c.schema != nil {
		schema, schemaErr = c.schema, nil
	}
	rc, repair, buildErr := bind(s, profile, "flow.quarantine")
	if buildErr == nil {
		buildErr = schemaErr
	}
	return gohan.FlowFunc[[]types.Block, types.Quarantined[Out]]("flow.quarantine", func(ctx context.Context, in []types.Block) (types.Quarantined[Out], error) {
		if buildErr != nil {
			return types.Quarantined[Out]{}, buildErr
		}
		var out Out
		err := call(ctx, rc, repair, schema, in, c, rc.Constrained, func(text string) (any, error) {
			if err := json.Unmarshal([]byte(text), &out); err != nil {
				return nil, err
			}
			if err := structured.Validate(out); err != nil {
				return nil, err
			}
			return out, nil
		})
		if err != nil {
			return types.Quarantined[Out]{}, err
		}
		return types.Quarantined[Out]{Value: out, Origins: blockOrigins(in)}, nil
	})
}

// blockOrigins collects the distinct Origin values of the input blocks,
// keeping first-seen order.
func blockOrigins(in []types.Block) []types.Origin {
	var origins []types.Origin
	seen := map[types.Origin]bool{}
	for _, b := range in {
		o := b.BlockOrigin()
		if seen[o] {
			continue
		}
		seen[o] = true
		origins = append(origins, o)
	}
	return origins
}
