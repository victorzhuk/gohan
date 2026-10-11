package exec

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/outputs"
)

// OutputStoredMetric counts stored large outputs (docs/gohan-spec.md:
// `gohan.output.stored` by tool).
const OutputStoredMetric = "gohan.output.stored"

// OutputCapper turns raw stream bytes into inline blocks: it caps each
// stream at Max bytes with a truncation marker, and stores content larger
// than the cap whole in Store, returning a ref read_output can serve. Tel
// receives one OutputStoredMetric count per stored output, named by tool.
type OutputCapper struct {
	Max   int
	Store stores.OutputStore
	Tel   types.Telemetry
	Run   types.RunInfo
	Tool  string
}

// Cap renders one stream. Under the cap the text block is the data
// unchanged and no ref is produced. Over the cap the inline text is the
// head excerpt plus a marker naming the cap, the dropped bytes and the
// stored ref, so a reader knows bytes were dropped and where the full
// content lives. Nothing is stored and no ref is returned when Store is
// nil or the output fits.
func (c OutputCapper) Cap(ctx context.Context, stream string, data []byte) ([]types.Block, string, error) {
	blocks, ref, err := outputs.StoreOutput(ctx, outputs.OutputStoreParams{
		Store:  c.Store,
		Run:    c.Run,
		Tool:   c.Tool,
		Max:    c.Max,
		Stream: stream,
		Data:   data,
		Tel:    c.Tel,
	})
	if err != nil {
		return nil, "", fmt.Errorf("exec: store %s output: %w", stream, err)
	}
	return blocks, ref, nil
}
