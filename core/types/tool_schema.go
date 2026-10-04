package types

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// maxSchemaDepth bounds how deeply the schema walker recurses into nested
// In types. The tools spec allows nested structs, slices and maps to depth 4.
const maxSchemaDepth = 4

// SchemaBuilder derives the JSON schema a ToolSpec carries from an In struct
// type, once at construction. It knows nothing about how tools are declared:
// the caller maps NewTool options onto it.
type SchemaBuilder struct {
	allowRaw bool
	identity IdentityFieldExcluder
}

// IdentityFieldExcluder is the consumer-owned port the driver's
// *IdentityFieldMatcher satisfies. Declared here because core imports
// types, never the reverse.
type IdentityFieldExcluder interface {
	Match(name string) bool
}

// SchemaOption adjusts a SchemaBuilder.
type SchemaOption func(*SchemaBuilder)

// WithRawArgs allows untyped argument fields (`any`, `map[string]any`,
// `json.RawMessage`) in the schema.
func WithRawArgs() SchemaOption {
	return func(b *SchemaBuilder) { b.allowRaw = true }
}

// WithIdentityFields excludes the matched identity fields from the schema:
// identity is injected by the runtime, never set by the model.
func WithIdentityFields(m IdentityFieldExcluder) SchemaOption {
	return func(b *SchemaBuilder) { b.identity = m }
}

// NewSchemaBuilder returns a SchemaBuilder for the given options.
func NewSchemaBuilder(opts ...SchemaOption) *SchemaBuilder {
	b := &SchemaBuilder{}
	for _, o := range opts {
		o(b)
	}
	return b
}

var rawMessageType = reflect.TypeFor[json.RawMessage]()

// Schema derives the schema for in, which must be a struct type. The result
// is strict-compatible: additionalProperties false, an explicit required
// list, no $ref recursion. Field errors name the field path so Build can
// prefix the tool name.
func (b *SchemaBuilder) Schema(in reflect.Type) (json.RawMessage, error) {
	if in.Kind() == reflect.Pointer {
		in = in.Elem()
	}
	if in.Kind() != reflect.Struct {
		return nil, fmt.Errorf("schema: In must be a struct, got %s", in)
	}
	props, required, err := b.walk(in, 1)
	if err != nil {
		return nil, err
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}
	if required == nil {
		schema["required"] = []string{}
	}
	out, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	return json.RawMessage(out), nil
}

// jsonName returns the encoding/json/v2 wire name of a field: the json tag
// name when present, otherwise the Go field name.
func jsonName(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return f.Name
	}
	return name
}

func (b *SchemaBuilder) walk(t reflect.Type, depth int) (map[string]any, []string, error) {
	props := map[string]any{}
	var required []string
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag, hasTag := f.Tag.Lookup("json")
		if hasTag {
			name, _, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue
			}
		}
		name := jsonName(f)
		if b.identity != nil && b.identity.Match(name) {
			continue
		}
		fieldSchema, optional, err := b.field(f, name, depth)
		if err != nil {
			return nil, nil, err
		}
		props[name] = fieldSchema
		if !optional {
			required = append(required, name)
		}
	}
	return props, required, nil
}

func (b *SchemaBuilder) field(f reflect.StructField, name string, depth int) (map[string]any, bool, error) {
	optional := strings.Contains(f.Tag.Get("json"), ",omitempty")
	if depth > maxSchemaDepth {
		return nil, false, fmt.Errorf("schema: field %q exceeds maximum nesting depth %d", name, maxSchemaDepth)
	}
	ft := f.Type
	for ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}
	schema, err := b.kind(ft, name, depth)
	if err != nil {
		return nil, false, err
	}
	if d := f.Tag.Get("desc"); d != "" {
		schema["description"] = d
	}
	if e := f.Tag.Get("enum"); e != "" {
		values := strings.Split(e, ",")
		schema["enum"] = values
	}
	schema, err = bounds(f, ft, schema, name)
	if err != nil {
		return nil, false, err
	}
	if p := f.Tag.Get("pattern"); p != "" {
		if _, err := regexp.Compile(p); err != nil {
			return nil, false, fmt.Errorf("schema: field %q has invalid pattern: %w", name, err)
		}
		schema["pattern"] = p
	}
	return schema, optional, nil
}

func bounds(f reflect.StructField, ft reflect.Type, schema map[string]any, name string) (map[string]any, error) {
	lengthBound := ft.Kind() == reflect.String || ft.Kind() == reflect.Slice || ft.Kind() == reflect.Map
	for _, bound := range [...]struct{ tag, key string }{{"min", "minimum"}, {"max", "maximum"}} {
		raw := f.Tag.Get(bound.tag)
		if raw == "" {
			continue
		}
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("schema: field %q has invalid %s %q", name, bound.tag, raw)
		}
		if lengthBound {
			schema[bound.tag+"Length"] = int(n)
		} else {
			schema[bound.key] = n
		}
	}
	return schema, nil
}

func (b *SchemaBuilder) kind(t reflect.Type, path string, depth int) (map[string]any, error) {
	if t == rawMessageType {
		return b.untyped(t, path)
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 && t.Kind() == reflect.Slice {
			// []byte marshals as a base64 string under encoding/json.
			return map[string]any{"type": "string"}, nil
		}
		elem, err := b.kind(t.Elem(), path+"[]", depth)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": elem}, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("schema: field %q: map keys must be string, got %s", path, t.Key())
		}
		elem, err := b.kind(t.Elem(), path, depth)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "object", "additionalProperties": elem}, nil
	case reflect.Struct:
		props, required, err := b.walk(t, depth+1)
		if err != nil {
			return nil, fmt.Errorf("schema: field %q: %w", path, err)
		}
		return map[string]any{
			"type":                 "object",
			"properties":           props,
			"required":             required,
			"additionalProperties": false,
		}, nil
	case reflect.Interface:
		return b.untyped(t, path)
	default:
		return nil, fmt.Errorf("schema: field %q: unsupported type %s", path, t)
	}
}

// untyped rejects fields without a closed schema unless the tool allows raw
// arguments.
func (b *SchemaBuilder) untyped(t reflect.Type, path string) (map[string]any, error) {
	if b.allowRaw {
		return map[string]any{}, nil
	}
	return nil, fmt.Errorf("schema: field %q: untyped %s requires WithRawArgs", path, t)
}
