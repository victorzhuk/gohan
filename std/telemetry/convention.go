package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/victorzhuk/gohan/core/types"
)

// ContentMapping says what happens to content-bearing attributes under a
// Convention. Content capture is off by default, and ContentNone is that
// default: content keys are dropped before they reach a backend.
type ContentMapping int

const (
	// ContentNone drops every content key.
	ContentNone ContentMapping = iota
	// ContentHashes replaces a content value with its SHA-256 hex digest,
	// keeping correlation without exposure.
	ContentHashes
	// ContentRedacted replaces a content value with a fixed marker.
	ContentRedacted
	// ContentFull passes the content value through unchanged; the caller
	// has already run it through the Redactor.
	ContentFull
)

// Convention maps the canonical gohan.* keys to the attribute names a
// backend reads. Core never changes when the mapping does: renaming a key
// is an edit to the Map function alone.
type Convention struct {
	// SchemaURL pins the semantic-convention schema the mapping was
	// validated against.
	SchemaURL string
	// Map returns the backend names for one canonical key. Returning nil
	// or an empty slice drops the attribute; returning the key itself
	// passes it through unchanged.
	Map func(attr string) []string
	// Content decides the fate of content-bearing attributes.
	Content ContentMapping
}

// Rename returns the backend names for one canonical key. A Convention
// without a Map passes every key through under its own name.
func (c Convention) Rename(attr string) []string {
	if c.Map == nil {
		return []string{attr}
	}
	return c.Map(attr)
}

// ApplyAttrs renames and content-filters a batch of attributes the way an
// adapter does before handing them to a backend. Unmapped keys keep their
// canonical name; a content key is dropped, hashed, redacted or passed
// per the Convention's Content setting.
func (c Convention) ApplyAttrs(attrs ...types.Attr) []types.Attr {
	out := make([]types.Attr, 0, len(attrs))
	for _, a := range attrs {
		names := c.Rename(a.Key)
		content := isContentKey(a.Key)
		if !content {
			for _, n := range names {
				if isContentKey(n) {
					content = true
					break
				}
			}
		}
		if content {
			switch c.Content {
			case ContentNone:
				continue
			case ContentHashes:
				a.Value = hashAttr(a.Value)
			case ContentRedacted:
				a.Value = "[redacted]"
			}
		}
		for _, n := range names {
			mapped := a
			mapped.Key = n
			out = append(out, mapped)
		}
	}
	return out
}

// hashAttr reduces any attribute value to a stable digest, so content
// stays correlatable across spans without being readable.
func hashAttr(v any) string {
	sum := sha256.Sum256([]byte(fmt.Sprint(v)))
	return hex.EncodeToString(sum[:])
}

// isContentKey reports whether an attribute name carries message text,
// tool arguments or tool results. It matches the content spellings of the
// shipped presets plus the gohan.content.* namespace a future capture
// layer may use; the Redactor runs before any ContentFull value arrives.
func isContentKey(key string) bool {
	if strings.HasPrefix(key, "gohan.content.") {
		return true
	}
	switch key {
	case "gen_ai.input.messages", "gen_ai.output.messages",
		"langfuse.observation.input", "langfuse.observation.output":
		return true
	}
	return false
}

// Pinned schema identifiers the presets were validated against. Bumping
// one is the whole cost of a schema revision.
const (
	GenAISchemaURL    = "https://opentelemetry.io/schemas/1.37.0"
	LangfuseSchemaURL = "https://langfuse.com/schemas/2025-01-14"
)

// genAIAttributes is the spec's canonical-to-GenAI table: gohan.session_id
// to gen_ai.conversation.id, the model keys to request/response model and
// provider name, usage to token counts, tool name to gen_ai.tool.name.
func genAIAttributes(attr string) []string {
	switch attr {
	case types.KeySessionID:
		return []string{"gen_ai.conversation.id"}
	case types.KeyModelProfile:
		return []string{"gen_ai.request.model"}
	case types.KeyModelVersion:
		return []string{"gen_ai.response.model"}
	case types.KeyModelEndpoint:
		return []string{"gen_ai.provider.name"}
	case types.KeyUsageInput:
		return []string{"gen_ai.usage.input_tokens"}
	case types.KeyUsageOutput:
		return []string{"gen_ai.usage.output_tokens"}
	case types.KeyToolName:
		return []string{"gen_ai.tool.name"}
	}
	return []string{attr}
}

// langfuseExtraAttributes are the names only Langfuse reads, layered on
// top of the GenAI table.
func langfuseExtraAttributes(attr string) []string {
	switch attr {
	case types.KeySessionID:
		return []string{"langfuse.session.id"}
	case types.KeyRelease:
		return []string{"langfuse.release"}
	case types.KeyModeAttr:
		return []string{"langfuse.trace.tags"}
	case "gohan.prompt.version":
		return []string{"langfuse.prompt.version"}
	case "gohan.prompt.name":
		return []string{"langfuse.prompt.name"}
	}
	return nil
}

// GenAI is the preset for backends speaking the OTel GenAI semantic
// conventions. Content keys are dropped: gen_ai.input.messages and
// gen_ai.output.messages are opt-in, so a caller wanting them sets
// Content to ContentFull after running capture through the Redactor.
func GenAI() Convention {
	return Convention{
		SchemaURL: GenAISchemaURL,
		Map:       genAIAttributes,
		Content:   ContentNone,
	}
}

// Langfuse is the GenAI table plus the langfuse.* session, release, tag
// and prompt names. langfuse.user.id is not mapped here: it is emitted by
// the adapter only when the redactor policy allows it.
func Langfuse() Convention {
	return Convention{
		SchemaURL: LangfuseSchemaURL,
		Map: func(attr string) []string {
			out := genAIAttributes(attr)
			return append(out, langfuseExtraAttributes(attr)...)
		},
		Content: ContentNone,
	}
}
