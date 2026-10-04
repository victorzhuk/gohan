package types

import (
	"context"
	stdjson "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strconv"
	"time"
)

type StopReason string

const (
	StopCompleted       StopReason = "completed"
	StopSuspended       StopReason = "suspended"
	StopLimit           StopReason = "limit"
	StopGuardBlocked    StopReason = "guard_blocked"
	StopCancelled       StopReason = "cancelled"
	StopFailed          StopReason = "failed"
	StopShadowSuspended StopReason = "shadow_suspended"
	StopHandedOff       StopReason = "handed_off"
)

// Event is the payload half of a run event. The harness pairs each payload
// with an EventMeta when it appends to the EventLog and when it delivers the
// event on a stream, so payloads carry no run identity of their own.
type Event interface{ isEvent() }

type TextDelta struct {
	Turn      int
	MessageID string
	Delta     string
}

type ReasoningDelta struct {
	Turn      int
	MessageID string
	Delta     string
}

type AssistantMessage struct {
	Turn     int
	Message  Message
	Usage    *Usage
	Operator bool
}

type ToolArgsDelta struct {
	Turn   int
	CallID string
	Name   string
	Delta  string
}

type ResultDelta struct {
	Turn      int
	MessageID string
	Delta     string
}

type ToolStarted struct {
	Turn int
	Call ToolUse
}

type ToolFinished struct {
	Turn     int
	Result   ToolResult
	Replayed bool
}

type Suspended struct {
	Token   ResumeToken
	Reason  SuspendReason
	Payload any
	WakeAt  time.Time
}

type GuardBlocked struct {
	Stage  GuardStage
	Reason string
}

type LimitWarning struct {
	Limit string
	Ratio float64
}

type Compacted struct {
	FromVersion  int64
	ToVersion    int64
	TokensBefore int
	TokensAfter  int
	Policy       string
}

type StateChanged struct {
	Version int64
	Patch   []PatchOp
}

type FeedbackRecorded struct {
	Target FeedbackTarget
	Name   string
	Value  any
	Source FeedbackSource
}

type SteerApplied struct {
	MessageID string
}

func (TextDelta) isEvent()        {}
func (ReasoningDelta) isEvent()   {}
func (AssistantMessage) isEvent() {}
func (ToolArgsDelta) isEvent()    {}
func (ResultDelta) isEvent()      {}
func (ToolStarted) isEvent()      {}
func (ToolFinished) isEvent()     {}
func (Suspended) isEvent()        {}
func (GuardBlocked) isEvent()     {}
func (LimitWarning) isEvent()     {}
func (Compacted) isEvent()        {}
func (StateChanged) isEvent()     {}
func (FeedbackRecorded) isEvent() {}
func (SteerApplied) isEvent()     {}
func (Done) isEvent()             {}

// EventMeta travels with every Event. The harness pairs a payload with its
// meta when it appends the event to the EventLog and when it delivers it on a
// stream.
type EventMeta struct {
	SessionID   string
	RunID       string
	RootRunID   string
	ParentRunID string
	Depth       int
	Flow        string
	Seq         int64
	Time        time.Time
}

type NoticeKind int

// String renders the notice body field `kind`: "finished" | "failed" |
// "cancelled" | "suspended", in the order of the constants below.
const (
	NoticeFinished NoticeKind = iota
	NoticeFailed
	NoticeCancelled
	NoticeSuspended
)

func (k NoticeKind) String() string {
	switch k {
	case NoticeFinished:
		return "finished"
	case NoticeFailed:
		return "failed"
	case NoticeCancelled:
		return "cancelled"
	case NoticeSuspended:
		return "suspended"
	default:
		return "NoticeKind(" + strconv.Itoa(int(k)) + ")"
	}
}

type RunNotice struct {
	ID        string
	Kind      NoticeKind
	Tenant    string
	SessionID string
	RunID     string
	Reason    string
	At        time.Time
}

func (n RunNotice) MarshalJSONTo(enc *jsontext.Encoder) error {
	w := struct {
		ID        string    `json:"id"`
		Kind      string    `json:"kind"`
		Tenant    string    `json:"tenant"`
		SessionID string    `json:"session_id"`
		RunID     string    `json:"run_id"`
		Reason    string    `json:"reason"`
		At        time.Time `json:"at"`
	}{
		ID:        n.ID,
		Kind:      n.Kind.String(),
		Tenant:    n.Tenant,
		SessionID: n.SessionID,
		RunID:     n.RunID,
		Reason:    n.Reason,
		At:        n.At,
	}
	return json.MarshalEncode(enc, &w)
}

func (n *RunNotice) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var w struct {
		ID        string    `json:"id"`
		Kind      string    `json:"kind"`
		Tenant    string    `json:"tenant"`
		SessionID string    `json:"session_id"`
		RunID     string    `json:"run_id"`
		Reason    string    `json:"reason"`
		At        time.Time `json:"at"`
	}
	if err := json.UnmarshalDecode(dec, &w); err != nil {
		return err
	}
	switch w.Kind {
	case "finished":
		n.Kind = NoticeFinished
	case "failed":
		n.Kind = NoticeFailed
	case "cancelled":
		n.Kind = NoticeCancelled
	case "suspended":
		n.Kind = NoticeSuspended
	default:
		return fmt.Errorf("gohan: unknown notice kind %q", w.Kind)
	}
	n.ID = w.ID
	n.Tenant = w.Tenant
	n.SessionID = w.SessionID
	n.RunID = w.RunID
	n.Reason = w.Reason
	n.At = w.At
	return nil
}

type Notifier interface {
	Notify(ctx context.Context, n RunNotice) error
}

// CallKey identifies a single tool call within a session.
type CallKey struct {
	SessionID string
	CallID    string
}

// PatchOp is one RFC 6902 operation in a StateChanged patch. core carries the
// type only; std/state computes the patches.
type PatchOp struct {
	Op    string
	Path  string
	Value any
}

type FeedbackTarget struct {
	SessionID string
	RunID     string
	MessageID string
}

type FeedbackSource int

const (
	Explicit FeedbackSource = iota
	Implicit
)

type Done struct {
	Reason    StopReason
	Seq       int64
	Usage     Usage
	Cost      float64
	Uncertain []CallKey
	Result    stdjson.RawMessage
}
