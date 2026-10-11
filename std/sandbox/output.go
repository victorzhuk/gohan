package sandbox

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/outputs"
)

// spiller renders one tool call's output streams against the output
// store. The boundary order is fixed: sanitize for injected secrets
// first, then cap, then store, so the stored document and the inline
// excerpt both derive from sanitized bytes. The inline cap resolves from
// Max, falling back to the tools-contract default outputs.DefaultMaxOutput
// when unset; streams within the cap stay inline, and overflow, an
// adapter-side truncation and returned files go to the store whole.
type spiller struct {
	store    stores.OutputStore
	run      types.RunInfo
	max      int
	sanitize outputs.SanitizeFunc
}

// stream caps one output stream. A truncation flag counts as the cap
// signal even when the delivered bytes fit, because the adapter dropped
// bytes before they arrived, and forces a store write so the delivered
// bytes stay retrievable. Nothing is stored when no store is wired or the
// sanitized stream fits untruncated.
func (s spiller) stream(ctx context.Context, stream string, data []byte, truncated bool) ([]types.Block, error) {
	return s.spill(ctx, stream, data, truncated)
}

// file stores a file the model asked to return whole and returns the head
// excerpt beside the File block whose ref read_output serves.
func (s spiller) file(ctx context.Context, name string, data []byte) ([]types.Block, error) {
	return s.spill(ctx, "file "+name, data, true)
}

func (s spiller) spill(ctx context.Context, stream string, data []byte, over bool) ([]types.Block, error) {
	if len(data) == 0 && !over {
		return nil, nil
	}
	blocks, ref, err := outputs.StoreOutput(ctx, outputs.OutputStoreParams{
		Store:      s.store,
		Run:        s.run,
		Max:        s.max,
		Stream:     stream,
		Data:       data,
		ForceStore: over,
		Sanitize:   s.sanitize,
	})
	if err != nil {
		return nil, fmt.Errorf("store %s: %w", stream, err)
	}
	if ref == "" {
		return blocks, nil
	}
	head, ok := blocks[0].(types.Text)
	if !ok {
		return nil, fmt.Errorf("store %s: unexpected block %T", stream, blocks[0])
	}
	blocks[0] = types.Text{
		BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}},
		Text:      head.Text,
	}
	return append(blocks[:1:1], types.File{
		BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}},
		Blob:      types.Blob{Ref: ref},
	}), nil
}
