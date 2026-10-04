package types

import (
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"time"
)

// ModelRequest is the fully assembled request one model call receives.
type ModelRequest struct {
	System   []Block
	Tools    []ToolSpec
	Messages []Message
	Options  ModelOptions
}

// ModelOptions carries the per-call knobs a provider maps onto its API.
type ModelOptions struct {
	MaxTokens      int
	Temperature    *float64
	ToolChoice     ToolChoice
	ResponseSchema json.RawMessage
	Priority       int
	AffinityKey    string
	Extra          map[string]any
}

// ToolChoice governs whether the provider may call tools.
type ToolChoice string

const (
	ToolChoiceAuto ToolChoice = "auto"
	ToolChoiceNone ToolChoice = "none"
	ToolChoiceAny  ToolChoice = "any"
)

// AffinityKeyStrategy selects how a run derives session affinity for routing.
type AffinityKeyStrategy int

const (
	AffinityNone AffinityKeyStrategy = iota
	AffinitySessionHash
	AffinityTenantHash
)

// wireToolSpec carries ToolSpec without Verify: functions have no wire form,
// and the record/replay key hashes only durable fields.
type wireToolSpec struct {
	Name              string
	Description       string
	Schema            json.RawMessage
	Effect            Effect
	RequiredScopes    []string
	Timeout           time.Duration
	ReadBack          string
	MaxOutput         int
	Deferred          bool
	Risk              RiskTier
	Capabilities      Capabilities
	Executor          Executor
	Egress            *EgressPolicy
	FingerprintFields []string
}

// wireModelRequest encodes System blocks through the same {kind, payload}
// codec as message blocks, so an interface field can round trip. The field
// order here is the wire order the record/replay key hashes; it is frozen.
type wireModelRequest struct {
	System   []wireBlock
	Tools    []wireToolSpec
	Messages []Message
	Options  ModelOptions
}

func marshalToolSpecs(ts []ToolSpec) []wireToolSpec {
	if ts == nil {
		return nil
	}
	ws := make([]wireToolSpec, len(ts))
	for i, t := range ts {
		ws[i] = wireToolSpec{
			Name:              t.Name,
			Description:       t.Description,
			Schema:            t.Schema,
			Effect:            t.Effect,
			RequiredScopes:    t.RequiredScopes,
			Timeout:           t.Timeout,
			ReadBack:          t.ReadBack,
			MaxOutput:         t.MaxOutput,
			Deferred:          t.Deferred,
			Risk:              t.Risk,
			Capabilities:      t.Capabilities,
			Executor:          t.Executor,
			Egress:            t.Egress,
			FingerprintFields: t.FingerprintFields,
		}
	}
	return ws
}

func (r ModelRequest) MarshalJSONTo(enc *jsontext.Encoder) error {
	ws, err := encodeBlocks(r.System)
	if err != nil {
		return err
	}
	tools := marshalToolSpecs(r.Tools)
	w := wireModelRequest{System: ws, Tools: tools, Messages: r.Messages, Options: r.Options}
	return jsonv2.MarshalEncode(enc, w)
}

func (r *ModelRequest) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var w wireModelRequest
	if err := jsonv2.UnmarshalDecode(dec, &w); err != nil {
		return err
	}
	bs, err := decodeBlocks(w.System)
	if err != nil {
		return err
	}
	r.System = bs
	if w.Tools != nil {
		r.Tools = make([]ToolSpec, len(w.Tools))
		for i, wt := range w.Tools {
			r.Tools[i] = ToolSpec{
				Name:              wt.Name,
				Description:       wt.Description,
				Schema:            wt.Schema,
				Effect:            wt.Effect,
				RequiredScopes:    wt.RequiredScopes,
				Timeout:           wt.Timeout,
				ReadBack:          wt.ReadBack,
				MaxOutput:         wt.MaxOutput,
				Deferred:          wt.Deferred,
				Risk:              wt.Risk,
				Capabilities:      wt.Capabilities,
				Executor:          wt.Executor,
				Egress:            wt.Egress,
				FingerprintFields: wt.FingerprintFields,
			}
		}
	}
	r.Messages = w.Messages
	r.Options = w.Options
	return nil
}
