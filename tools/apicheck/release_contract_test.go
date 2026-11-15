package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// taskBlock extracts the Taskfile block of one task: from its header line to
// the next task header at the same two-space indent.
func taskBlock(taskfile, name string) (string, bool) {
	lines := strings.Split(taskfile, "\n")
	start := -1
	for i, line := range lines {
		if line == "  "+name+":" || strings.HasPrefix(line, "  "+name+": #") {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	for end := start + 1; end < len(lines); end++ {
		line := lines[end]
		if line != "" && !strings.HasPrefix(line, " ") {
			return strings.Join(lines[start:end], "\n"), true
		}
	}
	return strings.Join(lines[start:], "\n"), true
}

func TestReleaseTaskContract(t *testing.T) {
	taskRaw, err := os.ReadFile(filepath.Join("..", "..", "Taskfile.yml"))
	if err != nil {
		t.Fatalf("read Taskfile.yml: %v", err)
	}
	taskfile := string(taskRaw)

	block, ok := taskBlock(taskfile, "adapters:verify")
	if !ok {
		t.Fatal("Taskfile.yml has no adapters:verify task")
	}
	if !strings.Contains(block, "tools/adapterverify") && !strings.Contains(block, "GOWORK=off") {
		t.Error("adapters:verify neither calls the Go verifier (tools/adapterverify) nor sets GOWORK=off")
	}
	for _, banned := range []string{"git tag", "goreleaser", "git push"} {
		if strings.Contains(block, banned) {
			t.Errorf("adapters:verify must be verification-only but contains %q", banned)
		}
	}

	ciRaw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	ci := string(ciRaw)
	if !strings.Contains(ci, "task adapters:verify") {
		t.Error("ci.yml does not invoke the exact task name adapters:verify")
	}
	for _, banned := range []string{"git tag", "goreleaser", "git push"} {
		if strings.Contains(ci, banned) {
			t.Errorf("ci.yml must stay verification-only but contains %q", banned)
		}
	}
}

func gitTags(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "tag", "--list").CombinedOutput()
	if err != nil {
		t.Fatalf("git tag --list: %v", err)
	}
	return string(out)
}

const fixtureModulePath = "fixture.example/verify"

// gohanProxyFile writes a file:// GOPROXY root serving synthetic
// github.com/victorzhuk/gohan modules for the given versions. The stub
// artifact is a serveable stdlib-only package; it proves runner behavior,
// not real adapter conformance.
func gohanProxyFile(t *testing.T, versions ...string) string {
	t.Helper()
	proxy := t.TempDir()
	esc := filepath.Join(proxy, "github.com", "victorzhuk", "gohan", "@v")
	if err := os.MkdirAll(esc, 0o755); err != nil {
		t.Fatalf("create proxy dir: %v", err)
	}
	gomod := "module github.com/victorzhuk/gohan\n\ngo 1.27.1\n"
	stub := fmt.Sprintf("package gohan\n\n// Version is the stub release identity for the verification fixture.\nconst Version = %q\n", versions[len(versions)-1])
	for _, v := range versions {
		prefix := "github.com/victorzhuk/gohan@" + v
		write := func(name, content string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(esc, name), []byte(content), 0o644); err != nil {
				t.Fatalf("write proxy %s: %v", name, err)
			}
		}
		write(v+".mod", gomod)
		write(v+".info", fmt.Sprintf(`{"Version":%q,"Time":"2026-01-01T00:00:00Z"}`, v))
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, entry := range []struct{ name, body string }{
			{prefix + "/go.mod", gomod},
			{prefix + "/gohan.go", stub},
		} {
			w, err := zw.Create(entry.name)
			if err != nil {
				t.Fatalf("zip %s: %v", entry.name, err)
			}
			if _, err := w.Write([]byte(entry.body)); err != nil {
				t.Fatalf("zip %s: %v", entry.name, err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("close zip: %v", err)
		}
		if err := os.WriteFile(filepath.Join(esc, v+".zip"), buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write proxy zip: %v", err)
		}
		list := strings.Join(versions, "\n") + "\n"
		write("list", list)
	}
	return "file://" + proxy
}

// fixtureSpec describes one disposable module copy for the CLI harness.
type fixtureSpec struct {
	dirName      string
	withReplace  bool
	rootVersion  string
	passingTests []string
	skippedTests []string
}

// writeFixtureModule creates a minimal stdlib-only module. A dirName of
// docker makes the verifier require TestSandboxConformance; stub tests with
// those names provide the synthetic positive control. When a root
// requirement is present it pre-seeds go.sum through the fixture proxy so
// the verifier's download cannot be accused of changing manifests.
func writeFixtureModule(t *testing.T, spec fixtureSpec, proxy string) string {
	t.Helper()
	dir := t.TempDir()
	dir = filepath.Join(dir, spec.dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create fixture dir: %v", err)
	}
	gomod := "module " + fixtureModulePath + "\n\ngo 1.27.1\n"
	if spec.rootVersion != "" {
		gomod += "\nrequire github.com/victorzhuk/gohan " + spec.rootVersion + "\n"
	}
	if spec.withReplace {
		gomod += "\nreplace github.com/victorzhuk/gohan => ../../\n"
	}
	files := map[string]string{
		"go.mod":  gomod,
		"main.go": "package main\n\nimport _ \"github.com/victorzhuk/gohan\"\n\nfunc main() {}\n",
	}
	var test strings.Builder
	test.WriteString("package main\n\nimport \"testing\"\n\n")
	for _, name := range spec.passingTests {
		fmt.Fprintf(&test, "func %s(t *testing.T) {}\n\n", name)
	}
	for _, name := range spec.skippedTests {
		fmt.Fprintf(&test, "func %s(t *testing.T) { t.Skip(\"fixture skip\") }\n\n", name)
	}
	if len(spec.passingTests)+len(spec.skippedTests) > 0 {
		files["verify_test.go"] = test.String()
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	if spec.rootVersion != "" && !spec.withReplace {
		cmd := exec.Command("go", "mod", "download", "github.com/victorzhuk/gohan")
		cmd.Dir = dir
		cmd.Env = fixtureEnv(t, proxy, "")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("seed fixture go.sum: %v\n%s", err, out)
		}
	}
	return dir
}

// fixtureEnv builds the environment the verifier and its children run with:
// GOWORK=off, the fixture file:// proxy, sumdb bypassed only for the
// synthetic fixture module, and an isolated module cache.
func fixtureEnv(t *testing.T, proxy, modCache string) []string {
	t.Helper()
	if modCache == "" {
		modCache = t.TempDir()
		t.Cleanup(func() {
			_ = filepath.WalkDir(modCache, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					return os.Chmod(path, 0o755)
				}
				return os.Chmod(path, 0o644)
			})
		})
	}
	return append(os.Environ(),
		"GOWORK=off",
		"GOPROXY="+proxy,
		"GONOSUMDB=github.com/victorzhuk/gohan",
		"GOMODCACHE="+modCache,
	)
}

func runVerifier(t *testing.T, modDir, proxy string) (string, int) {
	t.Helper()
	var stdout bytes.Buffer
	cmd := exec.Command("go", "run", "../adapterverify", "-mod", modDir)
	cmd.Env = fixtureEnv(t, proxy, "")
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run adapterverify: %v\n%s", err, stdout.String())
		}
		code = exitErr.ExitCode()
	}
	return stdout.String(), code
}

func TestReleaseVerificationCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go and git subprocesses")
	}

	proxy := gohanProxyFile(t, "v0.1.0", "v0.2.0")
	before := gitTags(t)

	// replacement-refused: the replace directive must be refused and named.
	refused := writeFixtureModule(t, fixtureSpec{dirName: "verify", withReplace: true, rootVersion: "v0.2.0"}, proxy)
	out, code := runVerifier(t, refused, proxy)
	if code == 0 {
		t.Fatalf("verifier accepted a go.mod with a replace directive; exit 0, output:\n%s", out)
	}
	if !strings.Contains(out, fixtureModulePath) {
		t.Errorf("refusal output does not name the module %s; output:\n%s", fixtureModulePath, out)
	}

	// wrong-root-refused: a root requirement other than v0.2.0 is refused
	// before any command rewrites or fetches it.
	wrongRoot := writeFixtureModule(t, fixtureSpec{dirName: "verify", rootVersion: "v0.1.0"}, proxy)
	out, code = runVerifier(t, wrongRoot, proxy)
	if code == 0 {
		t.Fatalf("verifier accepted a go.mod requiring gohan v0.1.0; exit 0, output:\n%s", out)
	}
	if !strings.Contains(out, "v0.2.0") {
		t.Errorf("wrong-root refusal does not name the required version v0.2.0; output:\n%s", out)
	}

	// otel-expected-root-refused: adapter/otel must pin the published root
	// carrying its telemetry export contracts, and the refusal names it
	// before any command rewrites or fetches the manifest.
	otelWrong := writeFixtureModule(t, fixtureSpec{dirName: "otel", rootVersion: "v0.2.0"}, proxy)
	out, code = runVerifier(t, otelWrong, proxy)
	if code == 0 {
		t.Fatalf("verifier accepted adapter/otel pinning root v0.2.0; exit 0, output:\n%s", out)
	}
	if !strings.Contains(out, "v0.3.0") {
		t.Errorf("otel expected-root refusal does not name the required version v0.3.0; output:\n%s", out)
	}

	// clean-module positive control: direct v0.2.0 root requirement and an
	// observed passing TestSandboxConformance stub are accepted.
	clean := writeFixtureModule(t, fixtureSpec{
		dirName:      "docker",
		rootVersion:  "v0.2.0",
		passingTests: []string{"TestSandboxConformance"},
	}, proxy)
	out, code = runVerifier(t, clean, proxy)
	if code != 0 {
		t.Fatalf("verifier refused a clean module copy (positive control failed); exit %d, output:\n%s", code, out)
	}

	// missing-required-conformance-refused: an unrelated passing test
	// cannot satisfy the required entry point.
	missing := writeFixtureModule(t, fixtureSpec{
		dirName:      "docker",
		rootVersion:  "v0.2.0",
		passingTests: []string{"TestUnrelatedHappiness"},
	}, proxy)
	out, code = runVerifier(t, missing, proxy)
	if code == 0 {
		t.Fatalf("verifier accepted a module whose required TestSandboxConformance never ran; exit 0, output:\n%s", out)
	}
	if !strings.Contains(out, "TestSandboxConformance") {
		t.Errorf("missing-conformance refusal does not name TestSandboxConformance; output:\n%s", out)
	}

	// skipped-conformance-refused: a SKIP is not a pass.
	skipped := writeFixtureModule(t, fixtureSpec{
		dirName:      "docker",
		rootVersion:  "v0.2.0",
		passingTests: []string{"TestOtherHappiness"},
		skippedTests: []string{"TestSandboxConformance"},
	}, proxy)
	out, code = runVerifier(t, skipped, proxy)
	if code == 0 {
		t.Fatalf("verifier accepted a module whose required TestSandboxConformance was skipped; exit 0, output:\n%s", out)
	}
	if !strings.Contains(out, "skipped") {
		t.Errorf("skipped-conformance refusal does not state the skip; output:\n%s", out)
	}

	if after := gitTags(t); after != before {
		t.Fatalf("git tag --list changed during verification:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
