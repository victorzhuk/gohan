package main

import (
	"bytes"
	"errors"
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

// writeFixtureModule creates a minimal stdlib-only module; withReplace adds
// the replace directive the verifier must refuse.
func writeFixtureModule(t *testing.T, withReplace bool) string {
	t.Helper()
	dir := t.TempDir()
	gomod := "module fixture.example/verify\n\ngo 1.27.1\n"
	if withReplace {
		gomod += "\nreplace github.com/victorzhuk/gohan => ../../\n"
	}
	files := map[string]string{
		"go.mod": gomod,
		"main.go": "package main\n\nfunc main() {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	return dir
}

func runVerifier(t *testing.T, modDir string) (string, int) {
	t.Helper()
	var stdout bytes.Buffer
	cmd := exec.Command("go", "run", "../adapterverify", "-mod", modDir)
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

	before := gitTags(t)

	refused := writeFixtureModule(t, true)
	out, code := runVerifier(t, refused)
	if code == 0 {
		t.Fatalf("verifier accepted a go.mod with a replace directive; exit 0, output:\n%s", out)
	}
	if !strings.Contains(out, "fixture.example/verify") {
		t.Errorf("refusal output does not name the module fixture.example/verify; output:\n%s", out)
	}
	if after := gitTags(t); after != before {
		t.Fatalf("git tag --list changed during the refused run:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	clean := writeFixtureModule(t, false)
	out, code = runVerifier(t, clean)
	if code != 0 {
		t.Fatalf("verifier refused a clean module copy (positive control failed); exit %d, output:\n%s", code, out)
	}
	if after := gitTags(t); after != before {
		t.Fatalf("git tag --list changed during the passing run:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
