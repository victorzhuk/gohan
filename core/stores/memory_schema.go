package stores

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/victorzhuk/gohan/core/types"
)

// validateMemoryValue checks value against a strict-compatible schema: type,
// enum, required, properties, additionalProperties false and array items.
// Every failure wraps types.ErrMemoryWriteInvalid.
func validateMemoryValue(schema json.RawMessage, value json.RawMessage) error {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return fmt.Errorf("decode memory value: %w", types.ErrMemoryWriteInvalid)
	}
	var shape map[string]any
	if err := json.Unmarshal(schema, &shape); err != nil {
		return fmt.Errorf("decode memory schema: %w", types.ErrMemoryWriteInvalid)
	}
	if err := validateNode(shape, decoded, ""); err != nil {
		return err
	}
	return nil
}

func validateNode(shape map[string]any, value any, path string) error {
	if enum, ok := shape["enum"].([]any); ok && !memberOf(enum, value) {
		return invalid(path, "value is not one of the enum members")
	}
	types, ok := shape["type"]
	if !ok {
		return nil
	}
	switch names := types.(type) {
	case string:
		if err := validateType(names, shape, value, path); err != nil {
			return err
		}
	case []any:
		for _, name := range names {
			name, ok := name.(string)
			if !ok {
				continue
			}
			if err := validateType(name, shape, value, path); err == nil {
				return nil
			}
		}
		return invalid(path, "value matches none of the declared types")
	}
	return nil
}

func validateType(name string, shape map[string]any, value any, path string) error {
	switch name {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return invalid(path, "value is not an object")
		}
		props, _ := shape["properties"].(map[string]any)
		if strict, ok := shape["additionalProperties"].(bool); ok && !strict {
			for k := range obj {
				if _, declared := props[k]; !declared {
					return invalid(path, "object carries undeclared property "+k)
				}
			}
		}
		if required, ok := shape["required"].([]any); ok {
			for _, r := range required {
				name, ok := r.(string)
				if !ok {
					continue
				}
				if _, present := obj[name]; !present {
					return invalid(path, "object misses required property "+name)
				}
			}
		}
		for name, sub := range props {
			child, present := obj[name]
			if !present {
				continue
			}
			subShape, ok := sub.(map[string]any)
			if !ok {
				return invalid(path, "property "+name+" carries a non-object schema")
			}
			if err := validateNode(subShape, child, join(path, name)); err != nil {
				return err
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return invalid(path, "value is not an array")
		}
		if itemShape, ok := shape["items"].(map[string]any); ok {
			for i, child := range items {
				if err := validateNode(itemShape, child, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return invalid(path, "value is not a string")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return invalid(path, "value is not a boolean")
		}
	case "integer":
		n, ok := value.(float64)
		if !ok || n != math.Trunc(n) {
			return invalid(path, "value is not an integer")
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return invalid(path, "value is not a number")
		}
	default:
		return invalid(path, "schema declares unknown type "+name)
	}
	if err := validateBounds(shape, value, path); err != nil {
		return err
	}
	return nil
}

func validateBounds(shape map[string]any, value any, path string) error {
	n, ok := value.(float64)
	if !ok {
		return nil
	}
	if min, ok := shape["min"].(float64); ok && n < min {
		return invalid(path, "value is below the minimum")
	}
	if max, ok := shape["max"].(float64); ok && n > max {
		return invalid(path, "value is above the maximum")
	}
	return nil
}

func memberOf(enum []any, value any) bool {
	for _, member := range enum {
		if member == value {
			return true
		}
	}
	return false
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func invalid(path, reason string) error {
	if path != "" {
		reason = path + ": " + reason
	}
	return fmt.Errorf("memory value: %s: %w", reason, types.ErrMemoryWriteInvalid)
}
