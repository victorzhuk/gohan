package gohan

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type egressTelemetry struct {
	count int
	attrs map[string]any
}

func (*egressTelemetry) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (t *egressTelemetry) Count(_ context.Context, name string, n int64, attrs ...types.Attr) {
	if name != "gohan.egress.requests" {
		return
	}
	t.count += int(n)
	t.attrs = make(map[string]any, len(attrs))
	for _, attr := range attrs {
		t.attrs[attr.Key] = attr.Value
	}
}

func (*egressTelemetry) Record(context.Context, string, float64, ...types.Attr) {}

func TestToolCallEgressDenied(t *testing.T) {
	for _, withAudit := range []bool{true, false} {
		t.Run(map[bool]string{true: "with audit", false: "without audit"}[withAudit], func(t *testing.T) {
			const refusal = "gohan: egress to https://user:secret@example.test/path?token=secret refused: private range"
			tool, err := NewTool("fetch_secret", "fetch data", func(context.Context, struct{}) (struct{}, error) {
				return struct{}{}, types.EgressDenied{URL: "https://user:secret@example.test/path?token=secret", Reason: "private range"}
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx := types.WithRunInfo(context.Background(), types.RunInfo{SessionID: "session-1", RunID: "run-1"})
			telemetry := &egressTelemetry{}
			ctx = types.WithTelemetry(ctx, telemetry)
			var log *stores.MemoryAuditLog
			if withAudit {
				log = stores.NewMemoryAuditLog()
				ctx = WithDecisionAudit(ctx, log)
			}
			set := ToolSetFunc(func(string) (types.Tool, bool) { return tool, true })
			result, err := CallTool(ctx, set, "fetch_secret", jsontext.Value(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if result.Outcome != types.Failed || result.Error == nil || result.Error.Kind != types.Permanent || result.Error.Message != refusal {
				t.Fatalf("result = %#v", result)
			}
			if telemetry.count != 1 || telemetry.attrs["tool"] != "fetch_secret" || telemetry.attrs["host"] != "example.test" || telemetry.attrs["decision"] != "denied" {
				t.Fatalf("telemetry = %#v", telemetry)
			}
			if !withAudit {
				return
			}
			var found bool
			for record, err := range log.Read(ctx, "session-1") {
				if err != nil {
					t.Fatal(err)
				}
				if record.Kind != stores.AuditEgressDenied {
					continue
				}
				found = true
				if record.Tool != "fetch_secret" || record.RunID != "run-1" || record.SessionID != "session-1" || record.Host != "example.test" || record.Reason != "private range" {
					t.Fatalf("record = %#v", record)
				}
				encoded, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if url := string(encoded); url == refusal || containsSecret(url) {
					t.Fatalf("record contains refusal data: %s", url)
				}
			}
			if !found {
				t.Fatal("egress audit record missing")
			}
		})
	}
}

func containsSecret(s string) bool {
	for _, secret := range []string{"user:secret", "secret@", "token=", "/path"} {
		if strings.Contains(s, secret) {
			return true
		}
	}
	return false
}
