// Package tokens provides a deterministic token estimator used as the
// default TokenEstimator by the std presets.
package tokens

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"

	"github.com/victorzhuk/gohan/core/types"
)

// imageTokens is frozen because the spec routes the image-token rule through
// a Caps formula field that does not exist yet; a real provider formula
// replaces this constant when Caps grows the field.
const imageTokens = 85

// overheadTokens covers per-message and per-block framing the text rules do
// not capture.
const overheadTokens = 4

// Heuristic is the default deterministic TokenEstimator: identical input
// always yields an identical count, and the count is monotonic in the length
// of any text input.
var Heuristic types.TokenEstimator = estimator{}

type estimator struct{}

func (estimator) Estimate(req types.ModelRequest, caps types.Caps) int {
	n := 0
	n += blocksTokens(req.System)
	for _, m := range req.Messages {
		n += overheadTokens
		n += len(m.ID) / 4
		n += blocksTokens(m.Blocks)
	}
	for _, t := range req.Tools {
		n += overheadTokens
		n += len(t.Name) / 4
		n += len(t.Description) / 4
		n += jsonTokens(t.Schema)
	}
	_ = caps
	return n
}

func blocksTokens(bs []types.Block) int {
	n := 0
	for _, b := range bs {
		n += blockTokens(b)
	}
	return n
}

func blockTokens(b types.Block) int {
	n := overheadTokens
	switch v := b.(type) {
	case types.Text:
		n += textTokens(v.Text)
	case types.Reasoning:
		n += textTokens(v.Text)
	case types.Image:
		// Frozen constant; see imageTokens for why.
		n += imageTokens
	case types.Audio:
		n += int(v.Blob.Bytes) / 4
	case types.File:
		n += int(v.Blob.Bytes) / 4
	case types.Document:
		n += blocksTokens(v.Content)
	case types.ToolUse:
		n += jsonTokens(v.Args)
	case types.ToolResult:
		n += blocksTokens(v.Content)
	case types.Raw:
		n += textTokens(rawText(v.Value))
	case types.CacheBreak:
		// A marker block: framing only.
	case types.Compaction:
		n += blocksTokens(v.Summary)
	}
	return n
}

// textTokens applies the spec's per-text rules: JSON-like text bytes/3,
// text with more than a quarter non-ASCII bytes bytes/2, otherwise bytes/4.
// Division rounds up so the estimate is monotonic in length.
func textTokens(s string) int {
	if jsonLike(s) {
		return div(len(s), 3)
	}
	return div(len(s), densityDivisor(s))
}

func jsonTokens(raw []byte) int {
	return div(len(raw), 3)
}

func densityDivisor(s string) int {
	nonASCII := 0
	for _, r := range s {
		if r >= utf8.RuneSelf {
			nonASCII += utf8.RuneLen(r)
		}
	}
	if 4*nonASCII > len(s) {
		return 2
	}
	return 4
}

func jsonLike(s string) bool {
	t := bytes.TrimSpace([]byte(s))
	return len(t) > 0 && (t[0] == '{' || t[0] == '[')
}

func rawText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func div(n, d int) int {
	if n <= 0 {
		return 0
	}
	return (n + d - 1) / d
}
