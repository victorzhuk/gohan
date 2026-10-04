package gohan

import (
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
// and the grant check: the tool name plus the canonical JSON of the
// arguments the spec's FingerprintFields select. Canonical form sorts object
// keys, so equal arguments in any order share a fingerprint.
func ToolFingerprint(spec types.ToolSpec, args json.RawMessage) stores.Fingerprint {
	var full map[string]any
	selected := any(args)
	if err := json.Unmarshal(args, &full); err != nil || full == nil {
		selected = map[string]any{}
	} else if len(spec.FingerprintFields) > 0 {
		picked := make(map[string]any, len(spec.FingerprintFields))
		for _, f := range spec.FingerprintFields {
			if v, ok := full[f]; ok {
				picked[f] = v
			}
		}
		selected = picked
	}
	canonical, err := json.Marshal(selected)
	if err != nil {
		canonical = []byte{}
	}
	return stores.Fingerprint(spec.Name + ":" + string(canonical))
}
