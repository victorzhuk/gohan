package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The historical-baseline exception is confined to the published
// adapter/otel/v0.1.0 pair: its manifest declares github.com/victorzhuk/gohan
// v0.0.0 with the workspace replacement `replace github.com/victorzhuk/gohan
// => ../..`, and adapter/otel/v0.1.0 and root v0.1.0 peel to the same release
// commit. The comparison may then correct only that root requirement to the
// published root v0.1.0, dropping exactly that replacement, in a temporary
// comparison copy. Every other dependency stays unchanged, the evidence is
// printed, and any unverified pair or unrelated load error is refused without
// applying the exception. Fixture tags live only in disposable repositories;
// the checker itself must not create or mutate tags.

const (
	fixtureRootModule    = "github.com/victorzhuk/gohan"
	fixtureAdapterModule = "github.com/victorzhuk/gohan/adapter/otel"
	fixtureExtraModule   = "example.com/extra"

	fixtureAdapterTag = "adapter/otel/v0.1.0"
	fixtureRootTag    = "v0.1.0"

	fixtureDefectiveRequirement = "v0.0.0"
	fixtureCorrectedRequirement = "v0.1.0"
)

const fixtureRootGoMod = "module github.com/victorzhuk/gohan\n\ngo 1.27.1\n"

const fixtureRootGoFile = "package gohan\n\n// Hello is the minimal exported surface the fixture baseline imports.\nfunc Hello() string { return \"hello\" }\n"

const fixtureExtraGoMod = "module example.com/extra\n\ngo 1.27.1\n"

const fixtureExtraGoFile = "package extra\n\nconst Name = \"extra\"\n"

// adapterGoMod mirrors the real published manifest: the defective v0.0.0
// requirement plus the workspace replacement, alongside an unrelated
// dependency whose version the correction must leave untouched.
const fixtureAdapterGoMod = "module github.com/victorzhuk/gohan/adapter/otel\n\ngo 1.27.1\n\nrequire (\n\texample.com/extra v1.0.0\n\tgithub.com/victorzhuk/gohan " + fixtureDefectiveRequirement + "\n)\n\nreplace github.com/victorzhuk/gohan => ../..\n"

const fixtureAdapterGoFile = "package otel\n\nimport (\n\t\"example.com/extra\"\n\n\t\"github.com/victorzhuk/gohan\"\n)\n\n// Span names the fixture's exported surface and both dependencies.\nfunc Span() string { return gohan.Hello() + extra.Name }\n"

// writeProxyModule materialises one module in the file-backed proxy layout:
// <module>/@v/<version>.info, .mod and .zip with the mandatory module@version
// directory prefix inside the zip.
func writeProxyModule(t *testing.T, proxyDir, module, version string, files map[string]string) {
	t.Helper()
	esc := module
	vDir := filepath.Join(proxyDir, esc, "@v")
	if err := os.MkdirAll(vDir, 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := json.Marshal(map[string]string{"Version": version, "Time": "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{".info", ".mod"} {
		name := filepath.Join(vDir, version+ext)
		body := string(info)
		if ext == ".mod" {
			body = files["go.mod"]
		}
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	zipPath := filepath.Join(vDir, version+".zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close zip: %v", err)
		}
	}()
	zw := zip.NewWriter(f)
	prefix := module + "@" + version
	for _, name := range []string{"go.mod", "go.sum"} {
		body, ok := files[name]
		if !ok {
			continue
		}
		w, err := zw.Create(prefix + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"gohan.go", "extra.go"} {
		body, ok := files[name]
		if !ok {
			continue
		}
		w, err := zw.Create(prefix + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func boundedCtx(t *testing.T, max time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	deadline, ok := t.Deadline()
	if !ok || time.Until(deadline)-5*time.Second > max {
		return context.WithTimeout(t.Context(), max)
	}
	return context.WithTimeout(t.Context(), time.Until(deadline)-5*time.Second)
}

// gitFixture runs one git command inside the disposable repository with the
// fixture's own commit identity and the contract's 10-second cap.
func gitFixture(t *testing.T, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := boundedCtx(t, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.com", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.com")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out))
}

// goFixture runs one bounded go command and returns combined output.
func goFixture(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	ctx, cancel := boundedCtx(t, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	if err := cmd.Run(); err != nil {
		t.Fatalf("go %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(combined.String()))
	}
	return combined.String()
}

// writeFixtureRepo creates the disposable repository: root module plus
// adapter/otel with the defective v0.0.0 requirement and workspace
// replacement. rootAtSecondCommit moves the root v0.1.0 tag to a second
// commit to build the mismatched-commit refusal fixture; the adapter tag
// always stays on the first release commit.
func writeFixtureRepo(t *testing.T, rootAtSecondCommit bool) (repoDir, proxyDir, releaseCommit string) {
	t.Helper()
	repoDir = t.TempDir()
	gitFixture(t, repoDir, "init", "-q")

	rootFiles := map[string]string{"go.mod": fixtureRootGoMod, "gohan.go": fixtureRootGoFile}
	for name, body := range rootFiles {
		if err := os.WriteFile(filepath.Join(repoDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	adapterDir := filepath.Join(repoDir, "adapter", "otel")
	if err := os.MkdirAll(adapterDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adapterDir, "go.mod"), []byte(fixtureAdapterGoMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adapterDir, "otel.go"), []byte(fixtureAdapterGoFile), 0o644); err != nil {
		t.Fatal(err)
	}

	proxyDir = t.TempDir()
	writeProxyModule(t, proxyDir, fixtureRootModule, fixtureCorrectedRequirement, map[string]string{
		"go.mod": fixtureRootGoMod, "gohan.go": fixtureRootGoFile,
	})
	writeProxyModule(t, proxyDir, fixtureExtraModule, "v1.0.0", map[string]string{
		"go.mod": fixtureExtraGoMod, "extra.go": fixtureExtraGoFile,
	})
	proxyEnv := append(proxyEnv(t, "file://"+proxyDir), "GOFLAGS=-mod=mod -p=2")
	// Record the unrelated dependency's sums in the committed go.sum; the
	// corrected requirement v0.1.0 resolves through -mod=mod at comparison
	// time, mirroring the published manifest whose sums pin other modules.
	goFixture(t, adapterDir, proxyEnv, "mod", "download", "example.com/extra")

	gitFixture(t, repoDir, "add", ".")
	gitFixture(t, repoDir, "commit", "-q", "-m", "chore(fixture): fixture release commit")
	releaseCommit = gitFixture(t, repoDir, "rev-parse", "HEAD")
	gitFixture(t, repoDir, "tag", fixtureAdapterTag)
	gitFixture(t, repoDir, "tag", fixtureRootTag)

	if rootAtSecondCommit {
		if err := os.WriteFile(filepath.Join(repoDir, "gohan.go"), []byte(fixtureRootGoFile+"\n// Later fixes the fixture root ships after the adapter release.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitFixture(t, repoDir, "add", ".")
		gitFixture(t, repoDir, "commit", "-q", "-m", "chore(fixture): fixture second commit")
		gitFixture(t, repoDir, "tag", "-f", fixtureRootTag)
	}
	return repoDir, proxyDir, releaseCommit
}

func proxyEnv(t *testing.T, proxy string) []string {
	t.Helper()
	return append(os.Environ(),
		"GOWORK=off",
		"GOPROXY="+proxy,
		"GOSUMDB=off",
	)
}

// buildChecker builds the real tools/apicheck executable once for the fixture
// group, per the contract's subprocess bounds.
func buildChecker(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "apicheck")
	goFixture(t, root, append(os.Environ(), "GOFLAGS="+strings.TrimSpace(os.Getenv("GOFLAGS")+" -p=2")), "build", "-p", "2", "-o", bin, "./tools/apicheck")
	return bin
}

// runChecker invokes the built checker inside the disposable repository with
// the contract's invocation environment and returns combined output and exit
// status. Setup and infrastructure failures abort the test; only the
// checker's own exit status is a comparison result.
func runChecker(t *testing.T, bin, repoDir, proxy, dir, tag string) (string, int) {
	t.Helper()
	ctx, cancel := boundedCtx(t, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-dir", dir, "-tag", tag)
	cmd.Dir = repoDir
	cmd.Env = append(proxyEnv(t, proxy), "GOFLAGS=-mod=mod "+strings.TrimSpace(os.Getenv("GOFLAGS")+" -p=2"))
	cmd.WaitDelay = 5 * time.Second
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	status := 0
	if exit, ok := err.(*exec.ExitError); ok {
		status = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run apicheck: %v: %s", err, strings.TrimSpace(combined.String()))
	}
	return combined.String(), status
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestHistoricalBaselineCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the apicheck subprocess")
	}
	bin := buildChecker(t)

	t.Run("verified-correction", func(t *testing.T) {
		repoDir, proxyDir, releaseCommit := writeFixtureRepo(t, false)
		adapterDir := filepath.Join(repoDir, "adapter", "otel")
		gomodBefore := readBytes(t, filepath.Join(adapterDir, "go.mod"))
		gosumBefore := readBytes(t, filepath.Join(adapterDir, "go.sum"))
		adapterRefBefore := gitFixture(t, repoDir, "rev-parse", fixtureAdapterTag)
		rootRefBefore := gitFixture(t, repoDir, "rev-parse", fixtureRootTag)

		out, status := runChecker(t, bin, repoDir, "file://"+proxyDir, "adapter/otel", fixtureAdapterTag)

		if status != 0 {
			t.Fatalf("verified pair failed: exit %d: %s", status, out)
		}
		if !strings.Contains(out, fixtureAdapterTag) {
			t.Errorf("output does not name the verified adapter tag %s: %s", fixtureAdapterTag, out)
		}
		if !strings.Contains(out, releaseCommit) {
			t.Errorf("output does not name the verified release commit %s: %s", releaseCommit, out)
		}
		if !strings.Contains(out, fixtureDefectiveRequirement) {
			t.Errorf("output does not print the original root requirement %s: %s", fixtureDefectiveRequirement, out)
		}
		if !strings.Contains(out, fixtureCorrectedRequirement) {
			t.Errorf("output does not print the corrected root requirement %s: %s", fixtureCorrectedRequirement, out)
		}
		if !strings.Contains(out, "historical") {
			t.Errorf("output does not report the applied historical exception: %s", out)
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "correct") && strings.Contains(line, fixtureExtraModule) {
				t.Errorf("correction evidence names the unrelated dependency %s: %s", fixtureExtraModule, line)
			}
		}
		if !bytes.Equal(gomodBefore, readBytes(t, filepath.Join(adapterDir, "go.mod"))) {
			t.Error("original go.mod was modified by the comparison")
		}
		if !bytes.Equal(gosumBefore, readBytes(t, filepath.Join(adapterDir, "go.sum"))) {
			t.Error("original go.sum was modified by the comparison")
		}
		if got := gitFixture(t, repoDir, "rev-parse", fixtureAdapterTag); got != adapterRefBefore {
			t.Errorf("adapter tag moved: %s -> %s", adapterRefBefore, got)
		}
		if got := gitFixture(t, repoDir, "rev-parse", fixtureRootTag); got != rootRefBefore {
			t.Errorf("root tag moved: %s -> %s", rootRefBefore, got)
		}
	})

	t.Run("mismatched-commit-refused", func(t *testing.T) {
		repoDir, proxyDir, _ := writeFixtureRepo(t, true)
		out, status := runChecker(t, bin, repoDir, "file://"+proxyDir, "adapter/otel", fixtureAdapterTag)
		if status == 0 {
			t.Fatalf("mismatched peel commits accepted: %s", out)
		}
		if strings.Contains(out, "historical") {
			t.Errorf("exception evidence printed for an unverified pair: %s", out)
		}
		if strings.TrimSpace(out) == "" {
			t.Error("refusal produced no diagnostic output")
		}
	})

	t.Run("unrelated-load-error", func(t *testing.T) {
		repoDir, proxyDir, _ := writeFixtureRepo(t, false)
		adapterGo := filepath.Join(repoDir, "adapter", "otel", "otel.go")
		body := readBytes(t, adapterGo)
		broken := strings.Replace(string(body), "\"example.com/extra\"", "\"example.com/missing\"", 1)
		if broken == string(body) {
			t.Fatal("fixture import not found")
		}
		if err := os.WriteFile(adapterGo, []byte(broken), 0o644); err != nil {
			t.Fatal(err)
		}
		out, status := runChecker(t, bin, repoDir, "file://"+proxyDir, "adapter/otel", fixtureAdapterTag)
		if status == 0 {
			t.Fatalf("unrelated load error accepted: %s", out)
		}
		if strings.Contains(out, "historical") {
			t.Errorf("exception evidence printed despite an unrelated load error: %s", out)
		}
		if strings.TrimSpace(out) == "" {
			t.Error("load refusal produced no diagnostic output")
		}
	})
}
