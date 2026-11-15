package schema

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// ErrInvalid reports input that does not satisfy the schema.
var ErrInvalid = errors.New("mcp: input does not match the schema")

// Validate checks data against the 2020-12 JSON schema. An empty schema
// accepts anything; a malformed schema is an error, never a panic.
func Validate(schema json.RawMessage, data json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}
	var s *jsonschema.Schema
	if err := json.Unmarshal(schema, &s); err != nil {
		return fmt.Errorf("mcp: invalid schema: %w", err)
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		return fmt.Errorf("mcp: invalid schema: %w", err)
	}
	var v any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &v); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	if err := resolved.Validate(v); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}
