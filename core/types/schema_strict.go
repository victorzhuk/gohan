package types

import (
	"encoding/json"
	"fmt"
	"sort"
)

// maxStrictEnumValues bounds the enum size a strict-compatible schema may
// carry (ADR-0072).
const maxStrictEnumValues = 50

// ValidateStrictSchema checks a wire schema against the strict-compatibility
// rules: no $ref recursion, object nesting depth at most 4 with the
// top-level object at depth 1, enums of at most 50 values, and every object
// node closed with additionalProperties false plus an explicit required
// array. The map form (additionalProperties carrying the element schema) is
// accepted because the schema walker emits it for map fields. The returned
// error names the JSON path and the violated rule; malformed JSON is an
// error too.
func ValidateStrictSchema(schema json.RawMessage) error {
	var node map[string]any
	if err := json.Unmarshal(schema, &node); err != nil {
		return fmt.Errorf("schema: malformed JSON: %w", err)
	}
	return validateStrictNode(node, 1, "")
}

func validateStrictNode(node map[string]any, depth int, path string) error {
	if node == nil {
		return nil
	}
	if _, ok := node["$ref"]; ok {
		return fmt.Errorf("schema: %s: $ref recursion is not supported", strictPath(path))
	}
	if values, ok := node["enum"].([]any); ok && len(values) > maxStrictEnumValues {
		return fmt.Errorf("schema: %s: enum has %d values, maximum is %d",
			strictPath(path), len(values), maxStrictEnumValues)
	}
	if len(node) == 0 {
		return nil
	}
	t, _ := node["type"].(string)
	_, hasProps := node["properties"]
	if t == "object" || hasProps {
		if depth > maxSchemaDepth {
			return fmt.Errorf("schema: %s: object nesting exceeds maximum depth %d",
				strictPath(path), maxSchemaDepth)
		}
		switch ap := node["additionalProperties"].(type) {
		case bool:
			if ap {
				return fmt.Errorf("schema: %s: object must set additionalProperties to false", strictPath(path))
			}
			if _, ok := node["required"].([]any); !ok {
				return fmt.Errorf("schema: %s: object missing explicit required array", strictPath(path))
			}
		case map[string]any:
			if len(ap) == 0 {
				return fmt.Errorf("schema: %s: additionalProperties must carry the element schema", strictPath(path))
			}
			// The map form closes its values through the element schema.
		default:
			return fmt.Errorf("schema: %s: object missing additionalProperties false", strictPath(path))
		}
	}
	if props, ok := node["properties"].(map[string]any); ok {
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child, ok := props[name].(map[string]any)
			if !ok {
				return fmt.Errorf("schema: %s: property value is not an object", strictChildPath(path, "properties."+name))
			}
			if err := validateStrictNode(child, depth+1, strictChildPath(path, "properties."+name)); err != nil {
				return err
			}
		}
	}
	switch items := node["items"].(type) {
	case map[string]any:
		if err := validateStrictNode(items, depth, strictChildPath(path, "items")); err != nil {
			return err
		}
	case []any:
		return fmt.Errorf("schema: %s: tuple-form items is not supported", strictChildPath(path, "items"))
	default:
		if _, ok := node["items"]; ok {
			return fmt.Errorf("schema: %s: items must be an object", strictChildPath(path, "items"))
		}
	}
	if ap, ok := node["additionalProperties"].(map[string]any); ok && len(ap) > 0 {
		if err := validateStrictNode(ap, depth, strictChildPath(path, "additionalProperties")); err != nil {
			return err
		}
	}
	return nil
}

func strictPath(path string) string {
	if path == "" {
		return "schema"
	}
	return path
}

func strictChildPath(path, part string) string {
	if path == "" {
		return part
	}
	return path + "." + part
}
