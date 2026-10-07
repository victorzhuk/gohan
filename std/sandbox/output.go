package sandbox

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// DefaultMaxOutput is the per-stream inline cap: content beyond it goes
// to the OutputStore and the result carries a head excerpt beside a File
// block whose ref read_output serves.
const DefaultMaxOutput = 64 << 10

// spiller renders one tool call's output streams against the output
// store: streams within the cap stay inline; overflow, an adapter-side
// truncation and returned files go to the store whole.
type spiller struct {
	store stores.OutputStore
	run   types.RunInfo
	max   int
}

// stream caps one output stream. A truncation flag counts as the cap
// signal even when the delivered bytes fit, because the adapter dropped
// bytes before they arrived. Nothing is stored when no store is wired or
// the stream fits untruncated.
func (s spiller) stream(ctx context.Context, stream string, data []byte, truncated bool) ([]types.Block, error) {
	return s.spill(ctx, stream, data, truncated)
}

// file stores a file the model asked to return whole and returns the head
// excerpt beside the File block whose ref read_output serves.
func (s spiller) file(ctx context.Context, name string, data []byte) ([]types.Block, error) {
	return s.spill(ctx, "file "+name, data, true)
}

func (s spiller) spill(ctx context.Context, stream string, data []byte, over bool) ([]types.Block, error) {
	max := s.max
	if max <= 0 {
		max = DefaultMaxOutput
	}
	if s.store == nil || (len(data) <= max && !over) {
		if len(data) == 0 {
			return nil, nil
		}
		return []types.Block{types.Text{Text: string(data)}}, nil
	}
	ref, err := s.store.Put(ctx, s.run, []types.Block{types.Text{Text: string(data)}})
	if err != nil {
		return nil, fmt.Errorf("store %s: %w", stream, err)
	}
	dropped := len(data) - max
	if dropped < 0 {
		dropped = 0
	}
	head := data
	if len(head) > max {
		head = head[:max]
	}
	marker := fmt.Sprintf("\n[%s truncated at %d bytes, %d bytes dropped; full output ref: %s]",
		stream, max, dropped, ref)
	return []types.Block{
		types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}}, Text: string(head) + marker},
		types.File{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}}, Blob: types.Blob{Ref: ref}},
	}, nil
}
