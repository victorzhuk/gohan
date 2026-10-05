package http

import (
	"context"
	"fmt"
	"io"
	"net/http"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
)

type FetchInput struct {
	URL string `json:"url"`
}

func Fetch(policy types.EgressPolicy) (gohan.Tool, error) {
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
			return types.ToolResult{}, err
		}
		return types.ToolResult{Content: []types.Block{types.Text{Text: fmt.Sprintf("HTTP %s\n%s", resp.Status, body)}}}, nil
	}, gohan.WithEffect(types.ReadOnly), gohan.WithEgress(policy), gohan.WithHTTP())
}
