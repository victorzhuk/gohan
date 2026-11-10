// Command adapterverify verifies release readiness of the adapter modules
// from isolated copies. For every module it copies the directory out of the
// repository, refuses any replace directive in the copied go.mod, requires a
// clean go mod tidy -diff, builds the module and runs its short conformance
// tests with GOWORK=off. It is verification-only: it never creates a tag, a
// release or a publication.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var defaultModules = []string{
	"adapter/anthropic",
	"adapter/docker",
	"adapter/eino",
	"adapter/openai",
	"adapter/otel",
	"adapter/postgres",
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
	if err := set.Parse(args); err != nil {
		return err
	}
	list := []string(mods)
	if len(list) == 0 {
		list = defaultModules
	}

	failed := false
	width := 0
	for _, dir := range list {
		if len(dir) > width {
			width = len(dir)
		}
	}
	for _, dir := range list {
		mod, err := verifyModule(dir, stdout, stderr)
		if err != nil {
			failed = true
			fmt.Fprintf(stdout, "%-*s FAIL %v\n", width, dir, err)
			continue
		}
		fmt.Fprintf(stdout, "%-*s PASS (%s)\n", width, dir, mod)
	}
	if failed {
		return errors.New("one or more adapter modules failed release verification")
	}
	return nil
}

func verifyModule(dir string, stdout, stderr io.Writer) (string, error) {
	copyDir, err := os.MkdirTemp("", "adapterverify-")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(copyDir) }()

	mod, err := copyTree(copyDir, dir)
	if err != nil {
		return "", fmt.Errorf("copy module: %w", err)
	}
	if err := rejectReplace(filepath.Join(copyDir, "go.mod"), dir, mod); err != nil {
		return "", err
	}

	env := append(os.Environ(), "GOWORK=off")
	for _, argv := range [][]string{
		{"mod", "tidy", "-diff"},
		{"build", "./..."},
		{"test", "-short", "-timeout", "5m", "./..."},
	} {
		cmd := exec.Command("go", append([]string{"-C", copyDir}, argv...)...)
		cmd.Env = env
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("go %s: %w", strings.Join(argv, " "), err)
		}
	}
	return mod, nil
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
