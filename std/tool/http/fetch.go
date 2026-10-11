package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/outputs"
)

type FetchInput struct {
	URL string `json:"url"`
}

// FetchOption adjusts Fetch at construction.
type FetchOption func(*fetchConfig)

// fetchConfig carries the wiring the rendered-response cap-and-store step
// uses. Both fields may stay zero: without a store an overflowing response
// is bounded to a head excerpt and a no-store marker.
type fetchConfig struct {
	store stores.OutputStore
	tel   types.Telemetry
}

// WithOutputStore wires the output store a fetched response spills into
// when its rendered text exceeds the inline cap, plus the telemetry that
// receives the gohan.output.stored count. Both parameters may be nil.
func WithOutputStore(store stores.OutputStore, tel types.Telemetry) FetchOption {
	return func(c *fetchConfig) {
		c.store = store
		c.tel = tel
	}
}

// Fetch builds the web_fetch tool. The rendered response is the HTTP
// status line followed by the body; the whole rendered text, status prefix
// included, counts against the inline cap and is what gets stored, so the
// status prefix cannot push inline content past the cap. Overflow goes
// through outputs.StoreOutput: head excerpt plus a truncation marker
// inline, the full rendered text under a ref when a store is wired.
func Fetch(policy types.EgressPolicy, opts ...FetchOption) (gohan.Tool, error) {
	var cfg fetchConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return gohan.NewTool("web_fetch", "Fetch a web page", func(ctx context.Context, in FetchInput) (types.ToolResult, error) {
		client := gohan.EgressClient(ctx)
		if client == nil {
			return types.ToolResult{Outcome: types.Failed, Error: &types.ToolError{Kind: types.Permanent, Message: "web_fetch requires an egress client"}}, nil
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
		if err != nil {
			return types.ToolResult{}, fmt.Errorf("create request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return types.ToolResult{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			// The egress policy time limit ends with the body; a read
			// stopped by it is a bounded refusal, not a transient error.
			if errors.Is(err, context.DeadlineExceeded) {
				return types.ToolResult{}, types.EgressDenied{URL: in.URL, Reason: "timeout"}
			}
			return types.ToolResult{}, err
		}
		run, _ := types.RunInfoFrom(ctx)
		blocks, ref, err := outputs.StoreOutput(ctx, outputs.OutputStoreParams{
			Store:  cfg.store,
			Run:    run,
			Tool:   "web_fetch",
			Stream: "output",
			Data:   []byte(fmt.Sprintf("HTTP %s\n%s", resp.Status, body)),
			Tel:    cfg.tel,
		})
		if err != nil {
			return types.ToolResult{}, err
		}
		return types.ToolResult{Content: blocks, Ref: ref}, nil
	}, gohan.WithEffect(types.ReadOnly), gohan.WithEgress(policy), gohan.WithHTTP())
}
