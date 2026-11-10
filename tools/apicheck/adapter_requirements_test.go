package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// adapterModules lists the six adapter modules by name. Each module pins the
// published root module version; the test fails when a manifest drifts back
// to an unpublished or retired version.
var adapterModules = []string{"anthropic", "docker", "eino", "openai", "otel", "postgres"}

var (
	requireLineRe  = regexp.MustCompile(`^\s*(?:require\s+)?github\.com/victorzhuk/gohan (v\S+)$`)
	replaceRe      = regexp.MustCompile(`^\s*replace\s`)
	goSumEntry     = "github.com/victorzhuk/gohan v0.2.0 h1:"
)

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
			gomod.Close()

			if required != "v0.2.0" {
				t.Errorf("go.mod requires github.com/victorzhuk/gohan %s, want v0.2.0", required)
			}
			if hasReplac {
				t.Errorf("go.mod contains a replace directive")
			}

			gosum, err := os.Open(filepath.Join(dir, "go.sum"))
			if err != nil {
				t.Fatal(err)
			}
			scanner = bufio.NewScanner(gosum)
			var sumFound bool
			for scanner.Scan() {
				if strings.HasPrefix(scanner.Text(), goSumEntry) {
					sumFound = true
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			gosum.Close()

			if !sumFound {
				t.Errorf("go.sum lacks %q entry", goSumEntry)
			}
		})
	}
}
