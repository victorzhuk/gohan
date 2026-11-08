package gohan

import (
	"bytes"
	"encoding/json"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// WithFingerprintFields narrows the call identity a tool's fingerprint pins:
// only the listed top-level argument fields enter the fingerprint, so a
// change in any other field keeps an existing session grant valid. An empty
// list pins every argument.
func WithFingerprintFields(fields ...string) ToolOption {
	return func(c *toolConfig) { c.spec.FingerprintFields = fields }
}

// ToolFingerprint derives the fingerprint one call carries into the journal
// and the grant check: the exact-v1 version prefix, the tool name and the
// canonical JSON of the arguments the spec's FingerprintFields select.
// Canonical form sorts object keys and renders every number by its exact
// value, so equal arguments in any order or spelling share a fingerprint
// and unequal numbers never collapse into one identity.
func ToolFingerprint(spec types.ToolSpec, args json.RawMessage) stores.Fingerprint {
	obj := map[string]any{}
	if canonical, err := types.CanonicalJSONValue(args); err == nil {
		dec := json.NewDecoder(bytes.NewReader(canonical))
		dec.UseNumber()
		var full map[string]any
		if err := dec.Decode(&full); err == nil && full != nil {
			obj = full
		}
	}
	selected := any(obj)
	if len(spec.FingerprintFields) > 0 {
		picked := make(map[string]any, len(spec.FingerprintFields))
		for _, f := range spec.FingerprintFields {
			if v, ok := obj[f]; ok {
				picked[f] = v
			}
		}
		selected = picked
	}
	canonical, err := json.Marshal(selected)
	if err != nil {
		canonical = []byte{}
	}
	return stores.Fingerprint(types.ExactFingerprintVersion + spec.Name + ":" + string(canonical))
}

// fullArgsFingerprint pins the complete argument value: it ignores the
// spec's FingerprintFields, so a change in any argument moves it.
func fullArgsFingerprint(tool string, args json.RawMessage) stores.Fingerprint {
	return ToolFingerprint(types.ToolSpec{Name: tool}, args)
}
