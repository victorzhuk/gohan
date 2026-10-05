package gohan

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func loggingRunInfo() types.RunInfo {
	return types.RunInfo{
		Flow:      "agent",
		SessionID: "sess-1",
		RunID:     "run-1",
		RootRunID: "run-root",
		Turn:      3,
		Principal: types.Principal{Tenant: "acme", Subject: "user-9"},
		ReleaseID: "r7",
		Variant:   "b",
	}
}

func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// forbiddenContent is the widest set of content-bearing values a run
// touches: message text, tool arguments, a tool result, a prompt, the
// credential token and a Raw provider payload.
type forbiddenContent struct {
	Text      string
	ToolArgs  string
	ToolResp  string
	Prompt    string
	CredToken string
	RawValue  string
}

func sampleContent() forbiddenContent {
	return forbiddenContent{
		Text:      "the launch codes are in the vault",
		ToolArgs:  `{"query":"vault location"}`,
		ToolResp:  "vault located at deck seven",
		Prompt:    "you are a helpful agent",
		CredToken: "sk-secret-token-9f2",
		RawValue:  `{"provider_payload":"internal"}`,
	}
}

func assertNoContent(t *testing.T, buf *bytes.Buffer, c forbiddenContent) {
	t.Helper()
	out := buf.String()
	for _, s := range []string{c.Text, c.ToolArgs, c.ToolResp, c.Prompt, c.CredToken, c.RawValue} {
		if strings.Contains(out, s) {
			t.Fatalf("content %q reached the log:\n%s", s, out)
		}
	}
}

func TestTelemetryLogging(t *testing.T) {
	t.Run("telemetry.no-content-in-logs", func(t *testing.T) {
		var buf bytes.Buffer
		base := captureLogger(&buf)
		run := runLogger(base, loggingRunInfo())

		c := sampleContent()
		cred := types.Credential{Token: c.CredToken}

		// The widest possible records: run start, finish, a per-step and a
		// per-tool record at Debug. Only events and canonical attributes are
		// attached; the content values above are never passed to the logger.
		run.Info("run started")
		run.Debug("model step finished", slog.Int64(types.KeyUsageInput, 12))
		run.Debug("tool executed",
			slog.String(types.KeyToolName, "search"),
			slog.String(types.KeyToolOutcome, "ok"),
		)
		run.Info("run finished", slog.Int64(types.KeyUsageOutput, 34))

		// Defence in depth: even an accidental String of the credential
		// carries no payload, so a future call site that reaches for one
		// still cannot leak the token.
		run.Debug("credential resolved", slog.String("credential", cred.String()))

		assertNoContent(t, &buf, c)

		// The records still landed at Debug, so the assertion is not
		// vacuous.
		if !strings.Contains(buf.String(), "level=DEBUG") {
			t.Fatalf("no debug records emitted:\n%s", buf.String())
		}
	})

	t.Run("canonical attributes present", func(t *testing.T) {
		var buf bytes.Buffer
		run := runLogger(captureLogger(&buf), loggingRunInfo())
		run.Info("run started")

		out := buf.String()
		for _, want := range []string{
			"gohan.flow=agent",
			"gohan.session_id=sess-1",
			"gohan.run_id=run-1",
			"gohan.root_run_id=run-root",
			"gohan.turn=3",
			"gohan.mode=primary",
			"gohan.tenant=acme",
			"gohan.subject=user-9",
			"gohan.release=r7",
			"gohan.variant=b",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("attribute %q missing:\n%s", want, out)
			}
		}
	})

	t.Run("nested logger keeps level and handler", func(t *testing.T) {
		var buf bytes.Buffer
		base := captureLogger(&buf)
		run := runLogger(base, loggingRunInfo())
		nested := run.With(slog.String(types.KeyToolName, "search"))

		nested.Debug("tool executed")

		out := buf.String()
		if !strings.Contains(out, "level=DEBUG") {
			t.Fatalf("nested logger lost the debug level:\n%s", out)
		}
		if !strings.Contains(out, "gohan.run_id=run-1") || !strings.Contains(out, "gohan.tool.name=search") {
			t.Fatalf("nested logger lost run attributes or the step attribute:\n%s", out)
		}
		if !strings.Contains(out, `msg="tool executed"`) {
			t.Fatalf("text handler replaced:\n%s", out)
		}
	})
}
