// Command adapterverify verifies release readiness of the adapter modules
// from isolated copies. For every module it copies the directory out of the
// repository, refuses any replace directive and any root requirement other
// than github.com/victorzhuk/gohan v0.2.0, requires a clean go mod tidy
// -diff, downloads and verifies the declared published dependencies with
// default checksum verification, builds the module and runs its conformance
// tests (never in short mode, with -tags full for PostgreSQL) with
// GOWORK=off. It parses the go test -json events and fails unless every
// required conformance entry point was observed passing. It is
// verification-only: it never creates a tag, a release or a publication.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const rootModulePath = "github.com/victorzhuk/gohan"

var defaultModules = []string{
	"adapter/anthropic",
	"adapter/adkgo",
	"adapter/docker",
	"adapter/eino",
	"adapter/openai",
	"adapter/otel",
	"adapter/postgres",
}

// requiredConformance maps a module directory base name to the conformance
// entry points that must be observed passing in the go test -json events.
var requiredConformance = map[string][]string{
	"anthropic": {"TestModelConformance"},
	"adkgo":     {"TestConformance"},
	"docker":    {"TestSandboxConformance"},
	"eino":      {"TestConformance"},
	"openai":    {"TestModelConformance"},
	"otel":      {"TestOTelExporter"},
	"postgres": {
		"TestSessionLog",
		"TestCheckpoints",
		"TestRuns",
		"TestJournal",
		"TestAuditLog",
		"TestEventLog",
		"TestOutputs",
		"TestOutputsForkCascade",
		"TestNotes",
		"TestRedaction",
	},
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "adapterverify:", err)
		os.Exit(1)
	}
}

type moduleList []string

func (m *moduleList) String() string { return strings.Join(*m, ",") }

func (m *moduleList) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func run(args []string, stdout, stderr io.Writer) error {
	var mods moduleList
	set := flag.NewFlagSet("adapterverify", flag.ContinueOnError)
	set.Var(&mods, "mod", "adapter module directory to verify (repeatable; defaults to all six)")
	deadline := set.Duration("deadline", 20*time.Minute, "aggregate deadline for the whole verification run")
	if err := set.Parse(args); err != nil {
		return err
	}
	list := []string(mods)
	if len(list) == 0 {
		list = defaultModules
	}

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(*deadline))
	defer cancel()

	failed := false
	width := 0
	for _, dir := range list {
		if len(dir) > width {
			width = len(dir)
		}
	}
	for _, dir := range list {
		mod, err := verifyModule(ctx, dir, stdout, stderr)
		if err != nil {
			failed = true
			if _, ferr := fmt.Fprintf(stdout, "%-*s FAIL %v\n", width, dir, err); ferr != nil {
				return ferr
			}
			continue
		}
		if _, ferr := fmt.Fprintf(stdout, "%-*s PASS (%s)\n", width, dir, mod); ferr != nil {
			return ferr
		}
	}
	if failed {
		return errors.New("release verification incomplete: one or more adapter modules failed verification")
	}
	return nil
}

// goTestEvent is one event of go test -json output.
type goTestEvent struct {
	Action  string
	Package string
	Test    string
}

func verifyModule(ctx context.Context, dir string, stdout, stderr io.Writer) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("aggregate deadline expired before %s: %w", dir, err)
	}
	copyDir, err := os.MkdirTemp("", "adapterverify-")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(copyDir) }()

	mod, err := copyTree(copyDir, dir)
	if err != nil {
		return "", fmt.Errorf("copy module: %w", err)
	}
	goModPath := filepath.Join(copyDir, "go.mod")
	goSumPath := filepath.Join(copyDir, "go.sum")
	if err := rejectReplace(goModPath, dir, mod); err != nil {
		return "", err
	}
	if err := requireRootV2(goModPath, dir, mod); err != nil {
		return "", err
	}

	srcMod, srcSum, err := readManifests(dir, goModPath, goSumPath)
	if err != nil {
		return "", err
	}

	env := append(os.Environ(), "GOWORK=off")
	argvFor := func(argv []string) []string { return append([]string{"-C", copyDir}, argv...) }
	run := func(label string, argv []string) error {
		cmd := exec.CommandContext(ctx, "go", argvFor(argv)...)
		cmd.Env = env
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("go %s: aggregate release-verification deadline expired: %w", label, ctx.Err())
			}
			return fmt.Errorf("go %s: %w", label, err)
		}
		return nil
	}

	// Dependency contract: tidy cleanliness, download and verify of the
	// declared published dependencies, and module-graph inspection.
	for _, argv := range [][]string{
		{"mod", "tidy", "-diff"},
		{"mod", "download"},
		{"mod", "verify"},
	} {
		if err := run(strings.Join(argv, " "), argv); err != nil {
			return "", err
		}
	}
	if err := inspectModuleGraph(ctx, copyDir, env, dir, mod, stderr); err != nil {
		return "", err
	}

	if err := run("build -p 2 ./...", []string{"build", "-p", "2", "./..."}); err != nil {
		return "", err
	}

	passed, skipped, err := runConformance(ctx, copyDir, dir, env, stdout, stderr)
	if err != nil {
		return "", err
	}

	if err := compareManifests(dir, goModPath, goSumPath, srcMod, srcSum); err != nil {
		return "", err
	}

	base := filepath.Base(dir)
	for _, entry := range requiredConformance[base] {
		switch {
		case skipped[entry]:
			return "", fmt.Errorf("%s (%s): required conformance entry %s was skipped; release verification requires it to execute and pass", dir, mod, entry)
		case !passed[entry]:
			return "", fmt.Errorf("%s (%s): required conformance entry %s has no passing test event; release verification requires it to execute and pass", dir, mod, entry)
		}
	}
	return mod, nil
}

// readManifests captures the source and copied manifest bytes for the
// before/after comparison.
func readManifests(dir, copyGoMod, copyGoSum string) (srcMod, srcSum []byte, err error) {
	srcMod, err = os.ReadFile(copyGoMod)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: read copied go.mod: %w", dir, err)
	}
	srcSum, err = os.ReadFile(copyGoSum)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("%s: read copied go.sum: %w", dir, err)
	}
	return srcMod, srcSum, nil
}

// compareManifests refuses any change the dependency commands made to the
// isolated copy's go.mod or go.sum.
func compareManifests(dir, copyGoMod, copyGoSum string, srcMod, srcSum []byte) error {
	afterMod, err := os.ReadFile(copyGoMod)
	if err != nil {
		return fmt.Errorf("%s: re-read copied go.mod: %w", dir, err)
	}
	if !bytes.Equal(srcMod, afterMod) {
		return fmt.Errorf("%s: dependency commands modified the copied go.mod; release verification requires byte-identical manifests", dir)
	}
	afterSum, err := os.ReadFile(copyGoSum)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s: re-read copied go.sum: %w", dir, err)
	}
	if !bytes.Equal(srcSum, afterSum) {
		return fmt.Errorf("%s: dependency commands modified the copied go.sum; release verification requires byte-identical manifests", dir)
	}
	return nil
}

// runConformance runs the module's tests non-short with JSON events and
// returns the top-level tests observed passing and skipped.
func runConformance(ctx context.Context, copyDir, dir string, env []string, stdout, stderr io.Writer) (map[string]bool, map[string]bool, error) {
	argv := []string{"test", "-json", "-timeout", "10m", "-p", "2", "-parallel", "2"}
	if filepath.Base(dir) == "postgres" {
		argv = append(argv, "-tags", "full")
	}
	argv = append(argv, "./...")
	var events bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", append([]string{"-C", copyDir}, argv...)...)
	cmd.Env = env
	cmd.Stdout = io.MultiWriter(stdout, &events)
	cmd.Stderr = stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, nil, fmt.Errorf("go test: aggregate release-verification deadline expired: %w", ctx.Err())
	}

	passed := map[string]bool{}
	skipped := map[string]bool{}
	dec := json.NewDecoder(&events)
	for {
		var ev goTestEvent
		if err := dec.Decode(&ev); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, nil, fmt.Errorf("%s: parse go test -json events: %w", dir, err)
		}
		if ev.Test == "" || strings.Contains(ev.Test, "/") {
			continue
		}
		switch ev.Action {
		case "pass":
			passed[ev.Test] = true
		case "skip":
			skipped[ev.Test] = true
		}
	}
	if runErr != nil {
		return nil, nil, fmt.Errorf("go %s: %w", strings.Join(argv, " "), runErr)
	}
	return passed, skipped, nil
}

// listedModule is one object of the go list -m -json all stream.
type listedModule struct {
	Path     string
	Version  string
	Indirect bool
	Replace  *struct {
		Path string
	}
	Error *struct {
		Err string
	}
}

// inspectModuleGraph runs go list -m -json all in the isolated copy and
// refuses replacement metadata, module errors, and any root selection other
// than the direct v0.2.0 requirement.
func inspectModuleGraph(ctx context.Context, copyDir string, env []string, dir, mod string, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "go", "-C", copyDir, "list", "-m", "-json", "all")
	cmd.Env = env
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("go list -m: aggregate release-verification deadline expired: %w", ctx.Err())
		}
		return fmt.Errorf("go list -m -json all: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	sawRoot := false
	for {
		var m listedModule
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("%s (%s): parse go list -m -json all: %w", dir, mod, err)
		}
		if m.Replace != nil {
			return fmt.Errorf("%s (%s): module graph selects %s through a replacement; an isolated release verification refuses it", dir, mod, m.Path)
		}
		if m.Error != nil {
			return fmt.Errorf("%s (%s): module graph error for %s: %s", dir, mod, m.Path, m.Error.Err)
		}
		if m.Path == rootModulePath {
			sawRoot = true
			if m.Indirect {
				return fmt.Errorf("%s (%s): %s is an indirect requirement; release verification requires a direct v0.2.0 requirement", dir, mod, rootModulePath)
			}
			if m.Version != "v0.2.0" {
				return fmt.Errorf("%s (%s): selected %s version is %s; release verification requires v0.2.0", dir, mod, rootModulePath, m.Version)
			}
		}
	}
	if !sawRoot {
		return fmt.Errorf("%s (%s): module graph does not select %s; release verification requires a direct v0.2.0 requirement", dir, mod, rootModulePath)
	}
	return nil
}

// requireRootV2 reads the copied go.mod and refuses anything other than a
// direct github.com/victorzhuk/gohan v0.2.0 requirement before any command
// runs, so a wrong requirement is never rewritten or fetched.
func requireRootV2(path, dir, mod string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	inBlock := false
	found := false
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case inBlock && trimmed == ")":
			inBlock = false
		case strings.HasPrefix(trimmed, "require ") && strings.HasSuffix(trimmed, "("):
			inBlock = true
		case strings.HasPrefix(trimmed, "require "):
			if matches, err := requireLine(strings.TrimPrefix(trimmed, "require "), i, dir, mod); err != nil {
				return err
			} else if matches {
				found = true
			}
		case inBlock:
			if matches, err := requireLine(trimmed, i, dir, mod); err != nil {
				return err
			} else if matches {
				found = true
			}
		}
	}
	if !found {
		return fmt.Errorf("%s (%s): go.mod has no direct %s v0.2.0 requirement; release verification requires it", dir, mod, rootModulePath)
	}
	return nil
}

// requireLine reports whether the require line names the root module and
// refuses an indirect annotation or a version other than v0.2.0.
func requireLine(trimmed string, i int, dir, mod string) (bool, error) {
	fields := strings.Fields(trimmed)
	if len(fields) < 2 || fields[0] != rootModulePath {
		return false, nil
	}
	for _, f := range fields[2:] {
		if f == "//" {
			break
		}
		if f == "indirect" {
			return false, fmt.Errorf("%s (%s): %s is an indirect requirement at line %d; release verification requires a direct v0.2.0 requirement", dir, mod, rootModulePath, i+1)
		}
	}
	if fields[1] != "v0.2.0" {
		return false, fmt.Errorf("%s (%s): go.mod requires %s %s at line %d; release verification requires v0.2.0", dir, mod, rootModulePath, fields[1], i+1)
	}
	return true, nil
}

// rejectReplace reads the copied go.mod and refuses any replace directive,
// single or in a block, naming the module directory and module path.
func rejectReplace(path, dir, mod string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	inBlock := false
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "replace") && strings.HasSuffix(trimmed, "("):
			inBlock = true
		case inBlock && trimmed == ")":
			inBlock = false
		case inBlock || strings.HasPrefix(trimmed, "replace"):
			return fmt.Errorf("%s (%s): go.mod declares a replace directive at line %d; an isolated release verification refuses it", dir, mod, i+1)
		}
	}
	return nil
}

// copyTree copies the module directory to dst and returns the module path
// declared in the copied go.mod.
func copyTree(dst, src string) (string, error) {
	mod := ""
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
			return err
		}
		if d.Name() == "go.mod" && mod == "" {
			mod = modulePathOf(string(data))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if mod == "" {
		return "", fmt.Errorf("%s: no module directive in go.mod", src)
	}
	return mod, nil
}

func modulePathOf(data string) string {
	for _, line := range strings.Split(data, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}
