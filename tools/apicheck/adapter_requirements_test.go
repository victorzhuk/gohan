package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// adapterModules lists the seven adapter modules by name. Each module pins
// its expected published root module version; the test fails when a manifest
// drifts back to an unpublished or retired version.
var adapterModules = []string{"adkgo", "anthropic", "docker", "eino", "openai", "otel", "postgres"}

var (
	requireLineRe = regexp.MustCompile(`^\s*(?:require\s+)?github\.com/victorzhuk/gohan (v\S+)$`)
	replaceRe     = regexp.MustCompile(`^\s*replace\s`)
)

// expectedRootVersion returns the published root version the adapter named
// mod must pin. adapter/otel consumes the telemetry export contracts first
// published in root v0.3.0; every other adapter pins v0.2.0.
func expectedRootVersion(mod string) string {
	if mod == "otel" {
		return "v0.3.0"
	}
	return "v0.2.0"
}

func TestAdapterPublishedRequirements(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range adapterModules {
		t.Run(mod, func(t *testing.T) {
			dir := filepath.Join(root, "adapter", mod)

			gomod, err := os.Open(filepath.Join(dir, "go.mod"))
			if err != nil {
				t.Fatal(err)
			}
			var (
				scanner   = bufio.NewScanner(gomod)
				required  string
				hasReplac bool
			)
			for scanner.Scan() {
				line := scanner.Text()
				if m := requireLineRe.FindStringSubmatch(line); m != nil {
					required = m[1]
				}
				if replaceRe.MatchString(line) {
					hasReplac = true
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if err := gomod.Close(); err != nil {
				t.Errorf("close go.mod: %v", err)
			}

			want := expectedRootVersion(mod)
			if required != want {
				t.Errorf("go.mod requires github.com/victorzhuk/gohan %s, want %s", required, want)
			}
			if hasReplac {
				t.Errorf("go.mod contains a replace directive")
			}

			gosum, err := os.Open(filepath.Join(dir, "go.sum"))
			if err != nil {
				t.Fatal(err)
			}
			scanner = bufio.NewScanner(gosum)
			goSumEntry := "github.com/victorzhuk/gohan " + want + " h1:"
			var sumFound bool
			for scanner.Scan() {
				if strings.HasPrefix(scanner.Text(), goSumEntry) {
					sumFound = true
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if err := gosum.Close(); err != nil {
				t.Errorf("close go.sum: %v", err)
			}

			if !sumFound {
				t.Errorf("go.sum lacks %q entry", goSumEntry)
			}
		})
	}
}
