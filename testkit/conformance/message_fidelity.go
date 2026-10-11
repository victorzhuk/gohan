package conformance

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

// MessageCodec is the seam a message bridge satisfies: it converts gohan
// messages to a foreign framework representation and back. It names no
// framework type, so a testkit suite can check any bridge without the
// testkit importing the bridge's module.
type MessageCodec struct {
	// Provider names the adapter whose fidelity the bridge declares.
	// Reasoning blocks of any other provider may be dropped.
	Provider string
	// Encode converts gohan messages to the framework representation.
	Encode func([]types.Message) (any, error)
	// Decode converts the framework representation back to gohan
	// messages.
	Decode func(any) ([]types.Message, error)
}

// TestingTB adapts a *testing.T to the contract's reporter, so an
// external codec suite can run the contract under its own subtest name.
func TestingTB(t *testing.T) conformanceTB { return testingTB{t} }

// MessageFidelity runs the shared message-fidelity contract against the
// codec under the named subtest. The fixture must cover every block kind
// the bridge declares, with Origin, Seq and any blob on the blocks. The
// contract asserts block order, per-block-kind fidelity, no dropped
// source block, Reasoning only through its own provider, and block-base
// and blob preservation across a full round trip.
//
// Legs of runtime.message-round-trip this contract carries:
//
//	eino   passing (M2, adapter/eino)
//	adkgo  pending (M3)
func MessageFidelity(tb conformanceTB, name string, codec MessageCodec, in []types.Message) {
	tb.Helper()
	tb.Run(name, func(tb conformanceTB) {
		tb.Helper()
		for _, problem := range CheckMessageFidelity(codec, in) {
			tb.Errorf("%s", problem)
		}
	})
}

// CheckMessageFidelity round-trips in through the codec and returns one
// string per fidelity problem it observed; an empty result is a passing
// round trip.
func CheckMessageFidelity(codec MessageCodec, in []types.Message) []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	if codec.Encode == nil || codec.Decode == nil {
		return []string{"codec without an encode or decode function"}
	}
	enc, err := codec.Encode(in)
	if err != nil {
		return []string{fmt.Sprintf("encode: %v", err)}
	}
	out, err := codec.Decode(enc)
	if err != nil {
		return []string{fmt.Sprintf("decode: %v", err)}
	}
	if len(out) != len(in) {
		add("decoded %d messages, want %d: the codec dropped or added a message", len(out), len(in))
		return problems
	}
	for i, msg := range in {
		if got := out[i].Role; got != msg.Role {
			add("message %d role = %q, want %q: block order depends on the role order", i, got, msg.Role)
			continue
		}
		if len(out[i].Blocks) != len(msg.Blocks) {
			add("message %d decoded %d blocks, want %d: the codec dropped or added a source block", i, len(out[i].Blocks), len(msg.Blocks))
			continue
		}
		for j, want := range msg.Blocks {
			got := out[i].Blocks[j]
			if reflect.TypeOf(got) != reflect.TypeOf(want) {
				add("message %d block %d kind = %T, want %T", i, j, got, want)
				continue
			}
			if r, ok := got.(types.Reasoning); ok && r.Provider != codec.Provider {
				add("message %d block %d reasoning provider = %q, want %q: reasoning travels only through its own provider", i, j, r.Provider, codec.Provider)
			}
			if !reflect.DeepEqual(got, want) {
				add("message %d block %d (%T) lost fidelity: %s", i, j, want, blockDiff(want, got))
			}
		}
		if !reflect.DeepEqual(out[i].Meta, msg.Meta) {
			add("message %d meta lost fidelity", i)
		}
	}
	return problems
}

// blockDiff names the field group a block lost: block base, blob or
// content.
func blockDiff(want, got types.Block) string {
	wb, gb := blockBaseOf(want), blockBaseOf(got)
	if wb.Origin != gb.Origin || wb.Seq != gb.Seq {
		return "block base (Origin, Seq) was not carried"
	}
	wd, gd := blobOf(want), blobOf(got)
	if wd != gd {
		return "blob fields (Data, URL, Blob) were not carried"
	}
	return "the round trip changed the block value"
}

func blockBaseOf(b types.Block) types.BlockBase {
	switch v := b.(type) {
	case types.Text:
		return v.BlockBase
	case types.Reasoning:
		return v.BlockBase
	case types.Image:
		return v.BlockBase
	case types.Audio:
		return v.BlockBase
	case types.File:
		return v.BlockBase
	case types.Document:
		return v.BlockBase
	case types.ToolUse:
		return v.BlockBase
	case types.ToolResult:
		return v.BlockBase
	case types.Raw:
		return v.BlockBase
	case types.CacheBreak:
		return v.BlockBase
	case types.Compaction:
		return v.BlockBase
	default:
		return types.BlockBase{}
	}
}

func blobOf(b types.Block) string {
	switch v := b.(type) {
	case types.Image:
		return fmt.Sprintf("%x|%s|%+v", v.Data, v.URL, v.Blob)
	case types.Audio:
		return fmt.Sprintf("%x|%s|%+v", v.Data, v.URL, v.Blob)
	case types.File:
		return fmt.Sprintf("%x|%s|%+v", v.Data, v.URL, v.Blob)
	default:
		return ""
	}
}
