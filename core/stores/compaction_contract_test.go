package stores_test

import (
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestCompactionPublicationIdentity(t *testing.T) {
	first := stores.CompactionPublicationID("s1", "run1", 7)
	if again := stores.CompactionPublicationID("s1", "run1", 7); again != first {
		t.Fatalf("same triple produced %q then %q", first, again)
	}
	distinct := []struct {
		session string
		run     string
		from    int64
	}{
		{"s2", "run1", 7},
		{"s1", "run2", 7},
		{"s1", "run1", 8},
		{"", "", 0},
	}
	seen := map[string]string{first: "baseline"}
	for _, d := range distinct {
		id := stores.CompactionPublicationID(d.session, d.run, d.from)
		if prev, ok := seen[id]; ok {
			t.Fatalf("triple (%q,%q,%d) collided with %s", d.session, d.run, d.from, prev)
		}
		seen[id] = d.session + "/" + d.run
	}
}

func TestCompactionSummarySHAStable(t *testing.T) {
	summary := []types.Block{
		types.Text{Text: "decided to use sqlite"},
		types.Text{Text: "open questions: none"},
	}
	first, err := stores.CompactionSummarySHA(summary)
	if err != nil {
		t.Fatalf("CompactionSummarySHA: %v", err)
	}
	if again, err := stores.CompactionSummarySHA(summary); err != nil || again != first {
		t.Fatalf("same summary produced %q then (%q, %v)", first, again, err)
	}
	other, err := stores.CompactionSummarySHA([]types.Block{types.Text{Text: "decided to use postgres"}})
	if err != nil {
		t.Fatalf("CompactionSummarySHA: %v", err)
	}
	if other == first {
		t.Fatal("distinct summaries produced the same checksum")
	}
	if _, err := stores.CompactionSummarySHA(nil); err != nil {
		t.Fatalf("empty summary: %v", err)
	}
}

func TestCloneCompactionAudit(t *testing.T) {
	if got := stores.CloneCompactionAudit(nil); got != nil {
		t.Fatalf("CloneCompactionAudit(nil) = %+v, want nil", got)
	}
	orig := &stores.CompactionAudit{
		PublicationID: "pub-1",
		FromVersion:   1,
		ToVersion:     9,
		TokensBefore:  100,
		TokensAfter:   20,
		Policy:        "summarize",
		SummarySHA:    "abc",
	}
	clone := stores.CloneCompactionAudit(orig)
	if clone == orig {
		t.Fatal("clone aliases the original pointer")
	}
	orig.PublicationID = "pub-2"
	orig.SummarySHA = "xyz"
	if clone.PublicationID != "pub-1" || clone.SummarySHA != "abc" {
		t.Fatalf("clone observed original mutation: %+v", clone)
	}
	clone.ToVersion = 99
	if orig.ToVersion != 9 {
		t.Fatalf("original observed clone mutation: %+v", orig)
	}
}
