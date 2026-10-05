package types

import "testing"

func TestCanonicalKeys(t *testing.T) {
	keys := map[string]string{
		KeyFlow:             "gohan.flow",
		KeySessionID:        "gohan.session_id",
		KeyRunID:            "gohan.run_id",
		KeyRootRunID:        "gohan.root_run_id",
		KeyParentRunID:      "gohan.parent_run_id",
		KeyTurn:             "gohan.turn",
		KeyTenant:           "gohan.tenant",
		KeySubject:          "gohan.subject",
		KeyRelease:          "gohan.release",
		KeyVariant:          "gohan.variant",
		KeyModeAttr:         "gohan.mode",
		KeyModelProfile:     "gohan.model.profile",
		KeyModelVersion:     "gohan.model.version",
		KeyModelEndpoint:    "gohan.model.endpoint",
		KeyModelKeyID:       "gohan.model.key_id",
		KeyUsageInput:       "gohan.usage.input",
		KeyUsageCachedInput: "gohan.usage.cached_input",
		KeyUsageCacheWrite:  "gohan.usage.cache_write",
		KeyUsageOutput:      "gohan.usage.output",
		KeyUsageHedgeLoser:  "gohan.usage.hedge_loser",
		KeyCost:             "gohan.cost",
		KeyToolName:         "gohan.tool.name",
		KeyToolEffect:       "gohan.tool.effect",
		KeyToolOutcome:      "gohan.tool.outcome",
		KeyJournalReplayed:  "gohan.journal.replayed",
		KeyGuardStage:       "gohan.guard.stage",
		KeyGuardVerdict:     "gohan.guard.verdict",
		KeyRouterDecision:   "gohan.router.decision",
		KeyRouterConfidence: "gohan.router.confidence",
		KeyApprover:         "gohan.approver",
		KeySuspendReason:    "gohan.suspend.reason",
		KeyNoticeKind:       "gohan.notice.kind",
	}
	for k, want := range keys {
		if k != want {
			t.Errorf("key %q, want %q", k, want)
		}
	}
}

func TestAttrConstructors(t *testing.T) {
	attrs := []Attr{String("a", "x"), Int("b", 2), Float("c", 0.5), Bool("d", true)}
	want := []struct {
		key string
		val any
	}{{"a", "x"}, {"b", int64(2)}, {"c", 0.5}, {"d", true}}
	for i, w := range want {
		if attrs[i].Key != w.key || attrs[i].Value != w.val {
			t.Errorf("attr %d = %+v, want %s %v", i, attrs[i], w.key, w.val)
		}
	}
}
