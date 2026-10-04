package structured

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/victorzhuk/gohan/core/types"
)

// strictMarker is the frozen wire name a provider with strict mode
// receives. No spec field declares it; the name is frozen here.
const strictMarker = "strict"

// ToolSchema is the structured-output strategy that derives the argument
// schema once and lets a provider with strict mode enforce it natively.
type ToolSchema struct {
	Strict bool
}

// NewToolSchema freezes the strategy value the caller passes into Build.
// strict requests the strict marker on the derived schema.
func NewToolSchema(strict bool) ToolSchema {
	return ToolSchema{Strict: strict}
}

// Schema derives the strict schema for in. It reuses the floor's
// SchemaBuilder: the derivation rules, tag set and shape limits are the
// floor's, never duplicated here. When Strict is set, the top-level schema
// carries strict: true next to additionalProperties: false and the explicit
// required list the builder already emits.
func (s ToolSchema) Schema(in reflect.Type, opts ...types.SchemaOption) (json.RawMessage, error) {
	raw, err := types.NewSchemaBuilder(opts...).Schema(in)
	if err != nil {
		return nil, fmt.Errorf("tool schema: %w: %s", types.ErrStructuredOutput, err)
	}
	if !s.Strict {
		return raw, nil
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("tool schema: %w: %s", types.ErrStructuredOutput, err)
	}
	schema[strictMarker] = true
	out, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("tool schema: %w: %s", types.ErrStructuredOutput, err)
	}
	return json.RawMessage(out), nil
}
