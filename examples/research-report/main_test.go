package main

import (
	"os"
	"strings"
	"testing"
)

// TestResearchReportOffline pins the research-report example's offline
// composition: it must compose gohan exactly like examples/excursions —
// gohan.Build over std.Defaults() with memory stores and a scripted model,
// a NativeSpec named research_report carrying ContextPolicy with the
// ClearToolResults projection and the Summarize compactor, a native
// conversation opened with run and event-log stores, one send, and a
// session-log load at the end. The example runs fully offline; the test
// asserts the required example sources exist and carry that composition,
// so it fails until the code stage lands main.go and fixtures.go.
func TestResearchReportOffline(t *testing.T) {
	for _, name := range []string{"main.go", "fixtures.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Errorf("%s: example source absent: %v", name, err)
			continue
		}
		if !strings.Contains(string(src), "package main") {
			t.Errorf("%s: missing package main declaration", name)
		}
	}

	mainSrc, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("main.go: example source absent: %v", err)
	}
	composition := []string{
		"gohan.Build",
		"std.Defaults()",
		"NativeSpec",
		"research_report",
		"ClearToolResults",
		"Compactor",
		"Summarize",
		"WithConversationRuns",
		"WithConversationEventLog",
		"NewNativeConversation",
		"stores.NewMemory",
		"SessionLog",
		"Send(",
		"Load(",
	}
	for _, marker := range composition {
		if !strings.Contains(string(mainSrc), marker) {
			t.Errorf("main.go: offline composition missing %q", marker)
		}
	}
}
