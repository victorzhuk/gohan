// Command apicheck compares the exported API of a module directory in the
// working tree against the same module at a tag, materialising the tag in a
// temporary git worktree, and exits nonzero when an exported symbol was
// removed or changed in a way source compatibility cannot survive. It never
// touches the network or the module cache.
package main

import (
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "apicheck:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("apicheck", flag.ContinueOnError)
	dir := fs.String("dir", ".", "module directory inside the repository")
	tag := fs.String("tag", "", "baseline tag to diff against (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tag == "" {
		fs.Usage()
		return errors.New("-tag is required")
	}

	root, err := gitOut(".", "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	modDir, err := relToRoot(root, *dir)
	if err != nil {
		return err
	}

	work, err := os.MkdirTemp("", "apicheck-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()
	wt, err := gitWorktree(root, *tag, work)
	if err != nil {
		return err
	}
	defer func() { _, _ = gitOut(root, "worktree", "remove", "--force", wt) }()

	mod, err := modulePath(filepath.Join(root, modDir))
	if err != nil {
		return err
	}
	base := filepath.Join(wt, modDir)
	head := filepath.Join(root, modDir)
	breaks, err := diffTrees(base, head)
	if err != nil {
		return err
	}
	if len(breaks) == 0 {
		fmt.Printf("api:check: %s: exported API compatible with %s\n", mod, *tag)
		return nil
	}
	fmt.Printf("api:check: %s: incompatible change(s) against %s:\n", mod, *tag)
	for _, b := range breaks {
		fmt.Printf("  %s\n", b)
	}
	return fmt.Errorf("%s: %d incompatible change(s) against %s", mod, len(breaks), *tag)
}

func relToRoot(root, dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("resolve %s against %s: %w", dir, root, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the repository", dir)
	}
	return rel, nil
}

func modulePath(dir string) (string, error) {
	blob, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod in %s: %w", dir, err)
	}
	for _, line := range strings.Split(string(blob), "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(name), nil
		}
	}
	return "", fmt.Errorf("no module directive in %s/go.mod", dir)
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func gitWorktree(root, sha, work string) (string, error) {
	dir := filepath.Join(work, "base")
	if _, err := gitOut(root, "worktree", "add", "--detach", dir, sha); err != nil {
		return "", err
	}
	return dir, nil
}

// break is one incompatible change, rendered as "importpath: symbol: why".
type breakage string

// diffTrees walks both module trees and reports every exported symbol that
// was removed or whose signature changed. Symbols added in head are
// compatible growth and are not reported.
func diffTrees(baseDir, headDir string) ([]breakage, error) {
	base, err := loadPackages(baseDir)
	if err != nil {
		return nil, fmt.Errorf("load base %s: %w", baseDir, err)
	}
	head, err := loadPackages(headDir)
	if err != nil {
		return nil, fmt.Errorf("load head %s: %w", headDir, err)
	}
	var out []breakage
	for _, p := range sortedKeys(base) {
		syms, ok := head[p]
		if !ok {
			for _, name := range sortedKeys(base[p]) {
				out = append(out, breakage(fmt.Sprintf("%s: %s: package removed", p, name)))
			}
			continue
		}
		for _, name := range sortedKeys(base[p]) {
			got, ok := syms[name]
			if !ok {
				out = append(out, breakage(fmt.Sprintf("%s: %s: removed", p, name)))
				continue
			}
			if got != base[p][name] {
				out = append(out, breakage(fmt.Sprintf("%s: %s: changed from %s to %s", p, name, base[p][name], got)))
			}
		}
	}
	return out, nil
}

// loadPackages maps import path to the module's exported symbol signatures,
// keyed as "func Name", "type Name", "const Name", "var Name" or
// "method (T) M".
func loadPackages(root string) (map[string]map[string]string, error) {
	pkgs := map[string]map[string]string{}
	err := filepath.WalkDir(root, func(d string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := e.Name()
		if e.IsDir() {
			if name == "testdata" || strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(d))
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, d, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", d, err)
		}
		ip := path.Join(".", filepath.ToSlash(rel))
		if ip == "." {
			ip = moduleRootImport
		}
		if _, ok := pkgs[ip]; !ok {
			pkgs[ip] = map[string]string{}
		}
		collect(file, pkgs[ip])
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pkgs, nil
}

func collect(f *ast.File, syms map[string]string) {
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			name := decl.Name.Name
			if !ast.IsExported(name) {
				continue
			}
			if decl.Recv == nil {
				syms["func "+name] = signature(decl)
				continue
			}
			if recv, ok := recvType(decl.Recv.List[0].Type); ok && ast.IsExported(strings.TrimPrefix(recv, "*")) {
				method := *decl
				method.Recv = nil
				syms["method ("+recv+") "+name] = signature(&method)
			}
		case *ast.GenDecl:
			for _, s := range decl.Specs {
				switch spec := s.(type) {
				case *ast.TypeSpec:
					if ast.IsExported(spec.Name.Name) {
						syms["type "+spec.Name.Name] = typeShape(spec)
					}
				case *ast.ValueSpec:
					for _, n := range spec.Names {
						if ast.IsExported(n.Name) {
							kind := "var "
							if decl.Tok == token.CONST {
								kind = "const "
							}
							syms[kind+n.Name] = exprString(spec.Type)
						}
					}
				}
			}
		}
	}
}

func signature(fn *ast.FuncDecl) string {
	var b strings.Builder
	if fn.Recv != nil {
		b.WriteString("(")
		b.WriteString(exprString(fn.Recv.List[0].Type))
		b.WriteString(") ")
	}
	b.WriteString("func")
	if fn.Type.TypeParams != nil && len(fn.Type.TypeParams.List) > 0 {
		b.WriteString(fieldList(fn.Type.TypeParams))
	}
	b.WriteString(fieldList(fn.Type.Params))
	b.WriteString(fieldList(fn.Type.Results))
	return b.String()
}

func fieldList(fl *ast.FieldList) string {
	if fl == nil || len(fl.List) == 0 {
		return "()"
	}
	parts := make([]string, 0, len(fl.List))
	for _, f := range fl.List {
		t := exprString(f.Type)
		if len(f.Names) == 0 {
			parts = append(parts, t)
			continue
		}
		for _, n := range f.Names {
			parts = append(parts, n.Name+" "+t)
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func typeShape(spec *ast.TypeSpec) string {
	var b strings.Builder
	if spec.TypeParams != nil && len(spec.TypeParams.List) > 0 {
		b.WriteString(fieldList(spec.TypeParams))
	}
	switch t := spec.Type.(type) {
	case *ast.StructType:
		b.WriteString("struct")
		names := make([]string, 0, len(t.Fields.List))
		for _, f := range t.Fields.List {
			// Only exported fields are part of the source surface; keyed
			// literals keep unexported field changes invisible to callers.
			if len(f.Names) > 0 && !ast.IsExported(f.Names[0].Name) {
				continue
			}
			name := exprString(f.Type)
			if len(f.Names) > 0 {
				name = strings.Join(namesOf(f.Names), ",") + " " + exprString(f.Type)
			}
			names = append(names, name)
		}
		sort.Strings(names)
		b.WriteString("{" + strings.Join(names, ";") + "}")
	case *ast.InterfaceType:
		b.WriteString("interface")
		names := make([]string, 0, len(t.Methods.List))
		for _, f := range t.Methods.List {
			if ft, ok := f.Type.(*ast.FuncType); ok {
				fake := &ast.FuncDecl{Name: f.Names[0], Type: ft}
				names = append(names, f.Names[0].Name+" "+signature(fake))
				continue
			}
			names = append(names, exprString(f.Type))
		}
		sort.Strings(names)
		b.WriteString("{" + strings.Join(names, ";") + "}")
	default:
		b.WriteString(exprString(spec.Type))
	}
	return b.String()
}

func namesOf(ids []*ast.Ident) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.Name
	}
	return out
}

func recvType(e ast.Expr) (string, bool) {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name, true
	case *ast.StarExpr:
		base, ok := recvType(t.X)
		return "*" + base, ok
	}
	return "", false
}

// exprString renders a type expression. A nil type (untyped declaration) is
// its absence, which matters when a const or var gains or drops a type.
func exprString(e ast.Expr) string {
	if e == nil {
		return "<none>"
	}
	var b strings.Builder
	if err := printer.Fprint(&b, token.NewFileSet(), e); err != nil {
		return "<unprintable>"
	}
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

const moduleRootImport = "."
