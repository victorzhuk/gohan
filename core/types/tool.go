package types

import (
	"context"
	"encoding/json"
	"time"
)

// Effect classifies what a tool call does to the world outside the run.
type Effect int

const (
	ReadOnly Effect = iota
	Idempotent
	SideEffect
)

// RiskTier is the risk classification a tool carries into the approval gate.
type RiskTier int

const (
	RiskLow RiskTier = iota
	RiskMedium
	RiskHigh
)

// Trust distinguishes tools registered from own code against tools imported
// from an external source.
type Trust int

const (
	Trusted Trust = iota
	Untrusted
)

// Executor names who runs a tool call.
type Executor int

const (
	ByHarness Executor = iota
	ByProvider
)

// Capabilities records what a tool is able to reach, derived at build time.
type Capabilities struct {
	Exfil       bool
	PrivateRead bool
}

// PrivateRanges says whether a network tool may reach private address space.
type PrivateRanges int

const (
	DenyPrivate PrivateRanges = iota
	AllowPrivate
)

// EgressPolicy is the one vocabulary for where a tool may connect.
type EgressPolicy struct {
	Allow         []string
	Schemes       []string
	PrivateRanges PrivateRanges
	MaxRedirects  int
	MaxBytes      int64
	Timeout       time.Duration
}

// EgressDenied is returned to the model when a connection is refused by policy.
type EgressDenied struct {
	URL    string
	Reason string
}

// ToolSpec is the full declaration of one tool. A Tool value holds one spec
// built once before Build and shared by every run.
type ToolSpec struct {
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
	Verify            func(ctx context.Context, args json.RawMessage, r ToolResult) (Outcome, error)
}

// ToolOption adjusts a ToolSpec at construction.
type ToolOption func(*ToolSpec)

// Tool is the port every tool implements. A value is built once and shared by
// every run: Call must be safe for concurrent use, and per-request state comes
// only from ctx and args.
type Tool interface {
	Spec() ToolSpec
	Call(ctx context.Context, args json.RawMessage) (ToolResult, error)
}
