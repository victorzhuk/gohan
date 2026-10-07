package gohan

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// neverContentAssemble mirrors the request assembly's population duty: the
// run's cost tags ride the request's metadata channel. The internal test
// cannot import std, so the seam under test is the field, not the preset.
func neverContentAssemble(tags types.CostTags) func(context.Context, types.AssembleInput) (types.ModelRequest, error) {
	return func(context.Context, types.AssembleInput) (types.ModelRequest, error) {
		return types.ModelRequest{Metadata: types.RequestMetadata{CostTags: tags}}, nil
	}
}

// scanCostTags fails if any sentinel appears anywhere in s.
func scanCostTags(t *testing.T, where, s string, sentinels []string) {
	t.Helper()
	for _, tag := range sentinels {
		if strings.Contains(s, tag) {
			t.Fatalf("cost tag %q reached %s:\n%s", tag, where, s)
		}
	}
}

func TestMetadataNeverContent(t *testing.T) {
	t.Run("costTagsNeverAppearInContentCapture", func(t *testing.T) {
		// Sentinels no honest log record or span attribute could contain.
		tags := types.CostTags{
			Feature:     "meta-never-content-feature",
			Environment: "meta-never-content-env",
			CostCenter:  "meta-never-content-cc",
		}
		sentinels := []string{tags.Feature, tags.Environment, tags.CostCenter}

		var logBuf bytes.Buffer
		tel := &fakeTelemetry{}
		info := types.RunInfo{
			Flow:      "chat",
			SessionID: "s1",
			RunID:     "r1",
			RootRunID: "r1",
			CostTags:  tags,
		}
		spec := NativeSpec{
			Request:  FlowRequest{Name: "chat"},
			Profile:  "primary",
			Assemble: neverContentAssemble(tags),
		}
		stack, err := Build(
			AllowAnonymous(),
			WithTelemetry(tel),
			WithLogger(captureLogger(&logBuf)),
			WithModels(fakeModel{profile: types.ModelProfile{Name: "primary"}}),
			WithNativeAgent(spec),
			WithStores(stores.Stores{
				SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
				Runs:        stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom)),
				Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
			}),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		conv, err := NewNativeConversation(stack, "chat",
			WithConversationRuns(stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		)
		if err != nil {
			t.Fatalf("new conversation: %v", err)
		}
		ctx := types.WithRunInfo(
			types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"}),
			info,
		)
		for _, sendErr := range conv.Send(ctx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}) {
			if sendErr != nil {
				t.Fatalf("send: %v", sendErr)
			}
		}

		// The tags are values for the provider's metadata channel, never
		// content: no record the run logged at Debug carries them.
		scanCostTags(t, "the log", logBuf.String(), sentinels)

		// ... and no span the run opened carries them, in any name or
		// attribute value the telemetry port saw.
		if len(tel.spans) == 0 {
			t.Fatal("run opened no spans")
		}
		for _, sp := range tel.spans {
			scanCostTags(t, fmt.Sprintf("span %q name", sp.name), sp.name, sentinels)
			for _, a := range sp.attrs {
				scanCostTags(t, fmt.Sprintf("span %q attr %s", sp.name, a.Key), a.Key, sentinels)
				scanCostTags(t, fmt.Sprintf("span %q attr %s value", sp.name, a.Key), fmt.Sprintf("%v", a.Value), sentinels)
			}
		}

		// The log side is exercised at Debug through the same per-run
		// logger the stack installs: the widest records a run can emit
		// still attach only canonical attributes.
		run := runLogger(captureLogger(&logBuf), info)
		run.Info("run started")
		run.Debug("model step finished")
		run.Debug("tool executed")
		scanCostTags(t, "the per-run logger", logBuf.String(), sentinels)
		if !strings.Contains(logBuf.String(), "level=DEBUG") {
			t.Fatalf("no debug records emitted:\n%s", logBuf.String())
		}
	})
}
