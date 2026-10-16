package main

import (
	"context"
	"testing"
)

// TestResearchReportOffline pins chunk c-compose's contract before its
// code exists: the composed run calls search_tools, reads the discovered
// sources, writes a progress note before the fan-out, compacts across the
// budget and finishes the report, whose Requests carry the session log's
// full cleared results. It fails to compile until Report carries Requests.
func TestResearchReportOffline(t *testing.T) {
	rep, err := Compose(context.Background(), "windowed joins and exactly-once sinks")
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(rep.Requests) == 0 {
		t.Fatal("the report carries no requests")
	}
}
