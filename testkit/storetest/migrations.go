package storetest

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// SchemaDump is one install's schema: table or view name to the column
// definitions a read of the catalog produced. Partition children are
// reported under their parent's name, so partition naming differences
// between a fresh and an upgraded install do not surface.
type SchemaDump map[string]string

// MigrationsHarness installs a schema twice: once straight at head, once
// migrated version by version from the first release.
type MigrationsHarness func(ctx context.Context, target SchemaTarget) (SchemaDump, error)

// SchemaTarget selects the install path a harness runs.
type SchemaTarget struct {
	// FromVersion migrates from this version upward; 0 means a fresh
	// install at head.
	FromVersion int64
}

// Migrations compares a fresh head install with one migrated from version
// 1: the dumps must be identical. A difference names the objects that
// drifted between the two paths.
func Migrations(t *testing.T, harness MigrationsHarness) {
	t.Helper()
	ctx := context.Background()
	fresh, err := harness(ctx, SchemaTarget{})
	if err != nil {
		t.Fatalf("fresh install: %v", err)
	}
	upgraded, err := harness(ctx, SchemaTarget{FromVersion: 1})
	if err != nil {
		t.Fatalf("upgrade from version 1: %v", err)
	}
	freshKeys, upgradedKeys := sortedKeys(fresh), sortedKeys(upgraded)
	if strings.Join(freshKeys, ",") != strings.Join(upgradedKeys, ",") {
		t.Fatalf("schema objects differ: fresh=[%s] upgraded=[%s]", strings.Join(freshKeys, ","), strings.Join(upgradedKeys, ","))
	}
	for _, k := range freshKeys {
		if normalize(fresh[k]) != normalize(upgraded[k]) {
			t.Fatalf("%s drifted between a fresh and an upgraded install:\n fresh:   %s\n upgraded: %s", k, normalize(fresh[k]), normalize(upgraded[k]))
		}
	}
}

func normalize(def string) string {
	fields := strings.Fields(def)
	sort.Strings(fields)
	return strings.Join(fields, " ")
}

func sortedKeys(m SchemaDump) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
