package types

import (
	"errors"

	"reflect"

	"encoding/json/jsontext"
	"encoding/json/v2"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	ID     string
	Role   Role
	Blocks []Block
	Meta   map[string]any
}

type OriginKind int

const (
	OriginSystem OriginKind = iota
	OriginUser
	OriginModel
	OriginTool
	OriginProvider
	OriginOperator
)

type Origin struct {
	Kind OriginKind
	Name string
}

type Block interface {
	isBlock()
	BlockOrigin() Origin
}

// BlockBase is embedded by every block; Origin and Seq live here.
type BlockBase struct {
	Origin Origin
	Seq    int64
}

func (b BlockBase) BlockOrigin() Origin { return b.Origin }

type Text struct {
	BlockBase
	Text string
}
type Reasoning struct {
	BlockBase
	Text      string
	Signature []byte
	Provider  string
}
type Blob struct {
	Ref    string
	SHA256 string
	Bytes  int64
}
type Image struct {
	BlockBase
	MIME string
	Data []byte
	URL  string
	Blob Blob
}
type Audio struct {
	BlockBase
	MIME string
	Data []byte
	URL  string
	Blob Blob
}
type File struct {
	BlockBase
	MIME string
	Name string
	Data []byte
	URL  string
	Blob Blob
}

type Document struct {
	BlockBase
	Content []Block
	Source  string
	Meta    map[string]string
}
type ToolUse struct {
	BlockBase
	ID   string
	Name string
	Args jsontext.Value
}
type ToolResult struct {
	BlockBase
	ID      string
	Content []Block
	Outcome Outcome
	Error   *ToolError
	Ref     string
}
type CacheBreak struct{ BlockBase }
type Raw struct {
	BlockBase
	Provider string
	Value    any
}
type Compaction struct {
	BlockBase
	CoversUpTo int64
	Kind       CompactionKind
	Summary    []Block
	Opaque     Raw
	Model      string
	Tokens     int
}

type CompactionKind int

const (
	CompactionText CompactionKind = iota + 1
	CompactionOpaque
)

var ErrBlobTooLarge = errors.New("gohan: block exceeds the profile's blob limit")

func (Text) isBlock()       {}
func (Reasoning) isBlock()  {}
func (Image) isBlock()      {}
func (Audio) isBlock()      {}
func (File) isBlock()       {}
func (Document) isBlock()   {}
func (ToolUse) isBlock()    {}
func (ToolResult) isBlock() {}
func (CacheBreak) isBlock() {}
func (Raw) isBlock()        {}
func (Compaction) isBlock() {}

type Outcome int

const (
	Succeeded Outcome = iota
	Failed
	Unknown
)

type ErrorKind int

const (
	Permanent ErrorKind = iota
	Retryable
	OutcomeUnknown
)

type ToolError struct {
	Kind    ErrorKind
	Message string
}

// Wire encoding: an interface field cannot unmarshal without a concrete type,
// so blocks travel as {kind, payload} pairs keyed by the spec's BlockKind
// strings. Nested block slices (Document, ToolResult, Compaction.Summary) reuse
// the same codec.
type BlockKind string

const (
	KindText       BlockKind = "text"
	KindReasoning  BlockKind = "reasoning"
	KindImage      BlockKind = "image"
	KindAudio      BlockKind = "audio"
	KindFile       BlockKind = "file"
	KindDocument   BlockKind = "document"
	KindToolUse    BlockKind = "tool_use"
	KindToolResult BlockKind = "tool_result"
	KindCacheBreak BlockKind = "cache_break"
	KindRaw        BlockKind = "raw"
	KindCompaction BlockKind = "compaction"
)

type wireBlock struct {
	Kind  string
	Block jsontext.Value
}

func blockKind(b Block) string {
	switch b.(type) {
	case Text:
		return string(KindText)
	case Reasoning:
		return string(KindReasoning)
	case Image:
		return string(KindImage)
	case Audio:
		return string(KindAudio)
	case File:
		return string(KindFile)
	case Document:
		return string(KindDocument)
	case ToolUse:
		return string(KindToolUse)
	case ToolResult:
		return string(KindToolResult)
	case CacheBreak:
		return string(KindCacheBreak)
	case Raw:
		return string(KindRaw)
	case Compaction:
		return string(KindCompaction)
	}
	return ""
}

func encodeBlocks(bs []Block) ([]wireBlock, error) {
	ws := make([]wireBlock, len(bs))
	for i, b := range bs {
		raw, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		ws[i] = wireBlock{blockKind(b), raw}
	}
	return ws, nil
}

func decodeBlocks(ws []wireBlock) ([]Block, error) {
	bs := make([]Block, len(ws))
	for i, w := range ws {
		var b Block
		switch BlockKind(w.Kind) {
		case KindText:
			b = new(Text)
		case KindReasoning:
			b = new(Reasoning)
		case KindImage:
			b = new(Image)
		case KindAudio:
			b = new(Audio)
		case KindFile:
			b = new(File)
		case KindDocument:
			b = new(Document)
		case KindToolUse:
			b = new(ToolUse)
		case KindToolResult:
			b = new(ToolResult)
		case KindCacheBreak:
			b = new(CacheBreak)
		case KindRaw:
			b = new(Raw)
		case KindCompaction:
			b = new(Compaction)
		default:
			return nil, errors.New("gohan: unknown block kind " + string(w.Kind))
		}
		if err := json.Unmarshal(w.Block, b); err != nil {
			return nil, err
		}
		// Unmarshal needs the pointer; store the value so round trips keep
		// the declared value receivers and block identity.
		bs[i] = reflect.Indirect(reflect.ValueOf(b)).Interface().(Block)
	}
	return bs, nil
}

type wireMessage struct {
	ID     string
	Role   Role
	Blocks []wireBlock
	Meta   map[string]any
}

func (m Message) MarshalJSONTo(enc *jsontext.Encoder) error {
	ws, err := encodeBlocks(m.Blocks)
	if err != nil {
		return err
	}
	return json.MarshalEncode(enc, wireMessage{m.ID, m.Role, ws, m.Meta})
}

func (m *Message) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var w wireMessage
	if err := json.UnmarshalDecode(dec, &w); err != nil {
		return err
	}
	bs, err := decodeBlocks(w.Blocks)
	if err != nil {
		return err
	}
	m.ID, m.Role, m.Blocks, m.Meta = w.ID, w.Role, bs, w.Meta
	return nil
}

func (d Document) MarshalJSONTo(enc *jsontext.Encoder) error {
	cs, err := encodeBlocks(d.Content)
	if err != nil {
		return err
	}
	return json.MarshalEncode(enc, struct {
		BlockBase
		Content []wireBlock
		Source  string
		Meta    map[string]string
	}{d.BlockBase, cs, d.Source, d.Meta})
}

func (d *Document) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var w struct {
		BlockBase
		Content []wireBlock
		Source  string
		Meta    map[string]string
	}
	if err := json.UnmarshalDecode(dec, &w); err != nil {
		return err
	}
	cs, err := decodeBlocks(w.Content)
	if err != nil {
		return err
	}
	d.BlockBase, d.Content, d.Source, d.Meta = w.BlockBase, cs, w.Source, w.Meta
	return nil
}

func (r ToolResult) MarshalJSONTo(enc *jsontext.Encoder) error {
	cs, err := encodeBlocks(r.Content)
	if err != nil {
		return err
	}
	return json.MarshalEncode(enc, struct {
		BlockBase
		ID      string
		Content []wireBlock
		Outcome Outcome
		Error   *ToolError
		Ref     string
	}{r.BlockBase, r.ID, cs, r.Outcome, r.Error, r.Ref})
}

func (r *ToolResult) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var w struct {
		BlockBase
		ID      string
		Content []wireBlock
		Outcome Outcome
		Error   *ToolError
		Ref     string
	}
	if err := json.UnmarshalDecode(dec, &w); err != nil {
		return err
	}
	cs, err := decodeBlocks(w.Content)
	if err != nil {
		return err
	}
	r.BlockBase, r.ID, r.Content, r.Outcome, r.Error, r.Ref =
		w.BlockBase, w.ID, cs, w.Outcome, w.Error, w.Ref
	return nil
}

func (c Compaction) MarshalJSONTo(enc *jsontext.Encoder) error {
	ss, err := encodeBlocks(c.Summary)
	if err != nil {
		return err
	}
	return json.MarshalEncode(enc, struct {
		BlockBase
		CoversUpTo int64
		Kind       CompactionKind
		Summary    []wireBlock
		Opaque     Raw
		Model      string
		Tokens     int
	}{c.BlockBase, c.CoversUpTo, c.Kind, ss, c.Opaque, c.Model, c.Tokens})
}

func (c *Compaction) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var w struct {
		BlockBase
		CoversUpTo int64
		Kind       CompactionKind
		Summary    []wireBlock
		Opaque     Raw
		Model      string
		Tokens     int
	}
	if err := json.UnmarshalDecode(dec, &w); err != nil {
		return err
	}
	ss, err := decodeBlocks(w.Summary)
	if err != nil {
		return err
	}
	c.BlockBase, c.CoversUpTo, c.Kind, c.Summary, c.Opaque, c.Model, c.Tokens =
		w.BlockBase, w.CoversUpTo, w.Kind, ss, w.Opaque, w.Model, w.Tokens
	return nil
}
