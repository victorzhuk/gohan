package exec

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// DefaultMaxOutput is the per-stream inline cap (tools contract default):
// bytes beyond it are dropped from the inline result, and content larger
// than the cap is stored whole with a retrievable ref.
const DefaultMaxOutput = 64 * 1024

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
	max := c.Max
	if max <= 0 {
		max = DefaultMaxOutput
	}
	if len(data) <= max {
		return []types.Block{types.Text{Text: string(data)}}, "", nil
	}
	ref := ""
	if c.Store != nil {
		var err error
		ref, err = c.Store.Put(ctx, c.Run, []types.Block{types.Text{Text: string(data)}})
		if err != nil {
			return nil, "", fmt.Errorf("exec: store %s output: %w", stream, err)
		}
		if c.Tel != nil {
			c.Tel.Count(ctx, OutputStoredMetric, 1, types.String(types.KeyToolName, c.Tool))
		}
	}
	dropped := len(data) - max
	marker := fmt.Sprintf("\n[%s truncated at %d bytes, %d bytes dropped; full output ref: %s]",
		stream, max, dropped, ref)
	head := string(data[:max]) + marker
	return []types.Block{types.Text{Text: head}}, ref, nil
}
