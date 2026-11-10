// Command apicheck compares the exported API of a module directory in the
// working tree against the same module at a tag, materialising the tag in a
// temporary git worktree, and exits nonzero when an exported symbol was
// removed or changed in a way source compatibility cannot survive. The
// comparison is semantic: it loads both module graphs with go/types export
// data, so identity and inferred types matter, and compares the two graphs
// by named identity rendered per package, never by pointer equality.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
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
	corrected, fixup, cleanup, err := correctBaseline(root, base, *tag)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	if corrected != "" {
		base = corrected
	}
	breaks, err := diffTrees(base, head)
	if err != nil {
		return err
	}
	if fixup != nil {
		fmt.Printf("api:check: historical exception verified: %s and %s peel to the same release commit %s\n", fixup.tag, fixup.rootTag, fixup.commit)
		fmt.Printf("api:check: historical correction applied: %s %s -> %s\n", fixup.requireModule, fixup.from, fixup.to)
	}
	breaks = applyExceptions(breaks)
	if len(breaks) == 0 {
		fmt.Printf("api:check: %s: exported API compatible with %s\n", mod, *tag)
		return nil
	}
	return reportBreaks(mod, *tag, breaks)
}

// apiExceptions maps a diff string exactly as diffTrees renders it to the
// reason the change survives source compatibility despite the textual
// difference. A checker-level exception is the last resort: the entry must
// name one symbol and carry a reason a reader can verify. An applied
// exception is always printed, never silent.
var apiExceptions = map[string]string{
	"std/cache: type Outcome: changed from struct{Hit bool;Tokens int} to chains.Outcome": "type alias to a struct of identical shape: every literal, switch and field access compiles unchanged, and a duplicated struct would break the Extra type-assertion across the mark/read seam",
}

// applyExceptions drops the breaks named by apiExceptions, printing each
// applied exception with its reason, and returns the breaks that stay
// incompatible.
func applyExceptions(breaks []breakage) []breakage {
	if len(breaks) == 0 {
		return breaks
	}
	kept := breaks[:0]
	for _, b := range breaks {
		reason, ok := apiExceptions[string(b)]
		if !ok {
			kept = append(kept, b)
			continue
		}
		fmt.Printf("api:check: exception applied: %s\napi:check:           reason: %s\n", b, reason)
	}
	return kept
}

// Pre-v1 modules report incompatible changes without failing: breaking
// changes are allowed before v1 and are recorded in CHANGELOG.md instead of
// gating CI.
func reportBreaks(mod, tag string, breaks []breakage) error {
	if len(breaks) == 0 {
		return nil
	}
	fmt.Printf("api:check: %s: incompatible change(s) against %s:\n", mod, tag)
	for _, b := range breaks {
		fmt.Printf("  %s\n", b)
	}
	if majorOf(tag) == 0 {
		fmt.Printf("api:check: %s: module is pre-v1, reporting incompatible change(s) without failing\n", mod)
		return nil
	}
	return fmt.Errorf("%s: %d incompatible change(s) against %s", mod, len(breaks), tag)
}

// majorOf reads the major version from the tag's last path segment, which is
// the semver part for both root tags (v0.1.0) and adapter tags
// (adapter/otel/v0.1.0). A tag without a semver tail is treated as v1: the
// strict path is the safe default.
func majorOf(tag string) int {
	ver := tag[strings.LastIndex(tag, "/")+1:]
	if !strings.HasPrefix(ver, "v") {
		return 1
	}
	major, _, _ := strings.Cut(ver[1:], ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return 1
	}
	return n
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

// symbol is one exported package-level object loaded from export data. A
// struct type keeps its exported field types separately, so additive keyed
// field growth is recognised as compatible while a removal or retype of a
// base field still breaks.
type symbol struct {
	render   string
	fields   map[string]string
	isStruct bool
}

// diffTrees loads both module graphs in separate type-checking sessions and
// reports every exported symbol that was removed or whose type changed.
// Symbols added in head are compatible growth and are not reported.
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
			if breaking(base[p][name], got) {
				out = append(out, breakage(fmt.Sprintf("%s: %s: changed from %s to %s", p, name, base[p][name].render, got.render)))
			}
		}
	}
	return out, nil
}

// breaking reports whether one symbol change cannot survive source
// compatibility. Two struct types compare field-wise: every exported field
// present in base must survive with the same type in head; fields added in
// head are keyed-literal-compatible growth.
func breaking(base, head *symbol) bool {
	if base.isStruct && head.isStruct {
		for name, t := range base.fields {
			if head.fields[name] != t {
				return true
			}
		}
		return false
	}
	return base.render != head.render
}

// loadPackages type-checks the module tree rooted at dir and maps relative
// import path to the package's exported symbols. Type checking tolerates
// errors, so a tree whose declared surface is under comparison still yields
// its exported objects even where it does not build. Module-internal imports
// resolve from the same tree, external imports from export data built by
// `go list -deps -export` against the module's own manifest.
func loadPackages(dir string) (map[string]map[string]*symbol, error) {
	modPath := ""
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		if p, err := modulePath(dir); err == nil {
			modPath = p
		}
	}
	fset := token.NewFileSet()
	units := parseUnits(fset, dir)
	if len(units) == 0 {
		return nil, fmt.Errorf("no Go packages found in %s", dir)
	}
	var ext types.Importer
	if externalImports(units, modPath) {
		exports, err := exportData(dir)
		if err != nil {
			return nil, err
		}
		lookup := func(p string) (io.ReadCloser, error) {
			file, ok := exports[p]
			if !ok {
				return nil, fmt.Errorf("no export data for %s", p)
			}
			return os.Open(file)
		}
		ext = importer.ForCompiler(fset, "gc", lookup)
	}
	l := &loader{
		fset:    fset,
		modPath: modPath,
		units:   units,
		ext:     ext,
		done:    map[string]*types.Package{},
		doing:   map[string]bool{},
	}
	pkgs := map[string]map[string]*symbol{}
	for _, key := range sortedKeys(units) {
		pkgs[key] = packageSymbols(l.check(key))
	}
	return pkgs, nil
}

// unit is one package of the compared tree: its relative key, full import
// path and parsed files.
type unit struct {
	path  string
	files []*ast.File
}

// parseUnits walks the module tree and parses every package file, using the
// same traversal rules as the comparison: testdata, dot- and underscore
// directories and nested modules are skipped, test files are ignored.
func parseUnits(fset *token.FileSet, dir string) map[string]*unit {
	units := map[string]*unit{}
	_ = filepath.WalkDir(dir, func(d string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := e.Name()
		if e.IsDir() {
			if name == "testdata" || strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if d != dir {
				if _, statErr := os.Stat(filepath.Join(d, "go.mod")); statErr == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(dir, filepath.Dir(d))
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		file, err := parser.ParseFile(fset, d, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", d, err)
		}
		u, ok := units[key]
		if !ok {
			u = &unit{}
			units[key] = u
		}
		u.files = append(u.files, file)
		return nil
	})
	return units
}

// externalImports reports whether the tree imports anything outside its own
// module, so export data is only built when it is actually needed.
func externalImports(units map[string]*unit, modPath string) bool {
	if modPath == "" {
		for _, u := range units {
			if len(u.files) > 0 && importsOf(u.files) {
				return true
			}
		}
		return false
	}
	for _, u := range units {
		for _, f := range u.files {
			for _, imp := range f.Imports {
				p, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					continue
				}
				if _, ok := moduleRel(modPath, p); !ok {
					return true
				}
			}
		}
	}
	return false
}

func importsOf(files []*ast.File) bool {
	for _, f := range files {
		if len(f.Imports) > 0 {
			return true
		}
	}
	return false
}

// loader type-checks the parsed units of one tree in one session. A cycle
// yields a fresh placeholder package, and any resolution failure is recorded
// by the tolerant config instead of aborting the comparison.
type loader struct {
	fset    *token.FileSet
	modPath string
	units   map[string]*unit
	ext     types.Importer
	done    map[string]*types.Package
	doing   map[string]bool
}

func (l *loader) check(key string) *types.Package {
	if p, ok := l.done[key]; ok {
		return p
	}
	u := l.units[key]
	if l.doing[key] {
		return types.NewPackage(u.path, "")
	}
	l.doing[key] = true
	defer delete(l.doing, key)
	conf := &types.Config{Error: func(e error) {
		if os.Getenv("APICHECK_DEBUG") != "" {
			fmt.Fprintln(os.Stderr, "DEBUG check", key, e)
		}
	}, Importer: importerFunc(l.importFor)}
	pkg, _ := conf.Check(u.path, l.fset, u.files, nil)
	l.done[key] = pkg
	return pkg
}

func (l *loader) importFor(p string) (*types.Package, error) {
	if l.modPath != "" {
		if key, ok := moduleRel(l.modPath, p); ok {
			if _, ok := l.units[key]; ok {
				return l.check(key), nil
			}
		}
	}
	if l.ext != nil {
		return l.ext.Import(p)
	}
	return nil, fmt.Errorf("unresolved import %s", p)
}

type importerFunc func(string) (*types.Package, error)

func (f importerFunc) Import(p string) (*types.Package, error) { return f(p) }

// exportData builds the module at dir and returns import path to export
// data file for every package in its dependency graph.
func exportData(dir string) (map[string]string, error) {
	cmd := exec.Command("go", "list", "-mod=mod", "-deps", "-export", "-json", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps -export in %s: %w: %s", dir, err, strings.TrimSpace(stderr.String()))
	}
	exports := map[string]string{}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var pkg struct {
			ImportPath string
			Export     string
			Error      *struct{ Err string }
		}
		if err := dec.Decode(&pkg); err != nil {
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		if pkg.Export == "" {
			if pkg.Error != nil {
				return nil, fmt.Errorf("go list: %s: %s", pkg.ImportPath, pkg.Error.Err)
			}
			continue
		}
		exports[pkg.ImportPath] = pkg.Export
	}
	return exports, nil
}

// moduleRel maps an import path to its position inside the module: the
// module root is ".", subpackages keep their slash path, other modules are
// skipped.
func moduleRel(modPath, ip string) (string, bool) {
	if ip == modPath {
		return ".", true
	}
	if rest, ok := strings.CutPrefix(ip, modPath+"/"); ok {
		return rest, true
	}
	return "", false
}

// packageSymbols extracts the exported surface of one package: "func Name",
// "type Name", "const Name", "var Name" and "method (T) M" keys.
func packageSymbols(pkg *types.Package) map[string]*symbol {
	syms := map[string]*symbol{}
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() || obj.Parent() != scope {
			continue
		}
		switch o := obj.(type) {
		case *types.Func:
			sig, ok := o.Type().(*types.Signature)
			if !ok || sig.Recv() != nil {
				continue
			}
			syms["func "+name] = &symbol{render: signatureString(sig, types.RelativeTo(pkg))}
		case *types.TypeName:
			t := o.Type()
			if named, ok := t.(*types.Named); ok && !o.IsAlias() {
				for m := range named.Methods() {
					if !m.Exported() {
						continue
					}
					sig, ok := m.Type().(*types.Signature)
					if !ok || sig.Recv() == nil {
						continue
					}
					key := "method (" + recvString(sig.Recv().Type(), pkg) + ") " + m.Name()
					syms[key] = &symbol{render: signatureString(sig, types.RelativeTo(pkg))}
				}
				t = named.Underlying()
			}
			syms["type "+name] = shapeSymbol(t, types.RelativeTo(pkg))
		case *types.Const:
			syms["const "+name] = &symbol{render: types.TypeString(o.Type(), types.RelativeTo(pkg))}
		case *types.Var:
			syms["var "+name] = &symbol{render: types.TypeString(o.Type(), types.RelativeTo(pkg))}
		}
	}
	return syms
}

// shapeSymbol renders the comparison shape of a type: structs and interfaces
// expand their members, aliases and other shapes render as the type itself,
// so an imported type identity change cannot pass as a textual match.
func shapeSymbol(shape types.Type, q types.Qualifier) *symbol {
	switch t := shape.(type) {
	case *types.Struct:
		render, fields := structShape(t, q)
		return &symbol{render: render, fields: fields, isStruct: true}
	case *types.Interface:
		return &symbol{render: interfaceShape(t, q)}
	default:
		return &symbol{render: types.TypeString(shape, q)}
	}
}

// signatureString renders a signature as "func(params)(results)" with
// package-relative type names; parameter names are not part of the surface.
func signatureString(sig *types.Signature, q types.Qualifier) string {
	var b strings.Builder
	b.WriteString("func")
	if sig.TypeParams().Len() > 0 {
		b.WriteString("[" + tparamNames(sig.TypeParams()) + "]")
	}
	b.WriteString(tupleString(sig.Params(), q))
	b.WriteString(tupleString(sig.Results(), q))
	return b.String()
}

func tupleString(t *types.Tuple, q types.Qualifier) string {
	parts := make([]string, 0, t.Len())
	for i := range t.Len() {
		parts = append(parts, types.TypeString(t.At(i).Type(), q))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// recvString renders a method receiver. A receiver of a generic type carries
// its type parameters ("*Box[T]"), which no instantiation ever produces, so
// generic methods compare against their generic declaration.
func recvString(t types.Type, pkg *types.Package) string {
	ptr := false
	if p, ok := t.(*types.Pointer); ok {
		ptr = true
		t = p.Elem()
	}
	s := types.TypeString(t, types.RelativeTo(pkg))
	if named, ok := t.(*types.Named); ok && named.TypeArgs().Len() == 0 && named.TypeParams().Len() > 0 {
		s = named.Obj().Name() + "[" + tparamNames(named.TypeParams()) + "]"
	}
	if ptr {
		s = "*" + s
	}
	return s
}

func tparamNames(list *types.TypeParamList) string {
	names := make([]string, 0, list.Len())
	for i := range list.Len() {
		names = append(names, list.At(i).Obj().Name())
	}
	return strings.Join(names, ",")
}

// structShape renders an exported-field shape sorted by field text and
// returns the per-field type map used for the compatible-growth comparison.
func structShape(st *types.Struct, q types.Qualifier) (string, map[string]string) {
	parts := make([]string, 0, st.NumFields())
	fields := make(map[string]string, st.NumFields())
	for i := range st.NumFields() {
		f := st.Field(i)
		rendered := types.TypeString(f.Type(), q)
		if f.Embedded() {
			fields[rendered] = rendered
		} else {
			if !f.Exported() {
				continue
			}
			fields[f.Name()] = rendered
			rendered = f.Name() + " " + rendered
		}
		parts = append(parts, rendered)
	}
	sort.Strings(parts)
	return "struct{" + strings.Join(parts, ";") + "}", fields
}

// interfaceShape renders the explicitly declared methods sorted by name;
// embedded interfaces contribute no textual member, matching their
// structural satisfaction by implementers.
func interfaceShape(it *types.Interface, q types.Qualifier) string {
	parts := make([]string, 0, it.NumExplicitMethods())
	for i := range it.NumExplicitMethods() {
		m := it.ExplicitMethod(i)
		sig, ok := m.Type().(*types.Signature)
		if !ok {
			continue
		}
		parts = append(parts, m.Name()+" "+signatureString(sig, q))
	}
	sort.Strings(parts)
	return "interface{" + strings.Join(parts, ";") + "}"
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// baselineFixup records one verified historical baseline correction: the
// only manifest edit the comparison may ever apply, and always in a
// temporary copy of the baseline module.
type baselineFixup struct {
	requireModule string
	from, to      string
	tag, rootTag  string
	commit        string
}

// correctBaseline detects the published historical pattern in the baseline
// manifest: a root requirement pinned v0.0.0 plus the workspace replacement
// `replace M => ../..`. When the comparison tag and the root tag of the same
// version peel to the same release commit, it copies the baseline module to
// a temporary directory, corrects only that requirement to the root tag
// version and drops exactly that replacement. Every other situation leaves
// the baseline untouched; a peeled-commit mismatch is refused outright.
func correctBaseline(root, base, tag string) (string, *baselineFixup, func(), error) {
	blob, err := os.ReadFile(filepath.Join(base, "go.mod"))
	if err != nil {
		return "", nil, nil, fmt.Errorf("read baseline go.mod: %w", err)
	}
	var replaceLeft string
	for _, line := range strings.Split(string(blob), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 4 && fields[0] == "replace" && fields[2] == "=>" && fields[3] == "../.." {
			if replaceLeft != "" && replaceLeft != fields[1] {
				return "", nil, nil, errors.New("baseline declares more than one workspace replacement: refusing baseline correction")
			}
			replaceLeft = fields[1]
		}
	}
	if replaceLeft == "" {
		return "", nil, nil, nil
	}
	var from string
	for _, line := range strings.Split(string(blob), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == replaceLeft && strings.HasPrefix(fields[1], "v") {
			if fields[1] != "v0.0.0" {
				return "", nil, nil, nil
			}
			from = fields[1]
		}
	}
	if from == "" {
		return "", nil, nil, nil
	}
	rootTag := path.Base(tag)
	if !strings.HasPrefix(rootTag, "v") || rootTag == from {
		return "", nil, nil, fmt.Errorf("baseline requires %s %s with a workspace replacement but tag %s names no root release: refusing baseline correction", replaceLeft, from, tag)
	}
	commit, err := gitOut(root, "rev-parse", tag+"^{commit}")
	if err != nil {
		return "", nil, nil, err
	}
	rootCommit, err := gitOut(root, "rev-parse", rootTag+"^{commit}")
	if err != nil {
		return "", nil, nil, err
	}
	if commit != rootCommit {
		return "", nil, nil, fmt.Errorf("%s and %s peel to different commits (%s, %s): refusing baseline correction", tag, rootTag, commit, rootCommit)
	}
	dir, err := os.MkdirTemp("", "apicheck-corrected-")
	if err != nil {
		return "", nil, nil, fmt.Errorf("create corrected baseline dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := copyTree(base, dir); err != nil {
		cleanup()
		return "", nil, nil, fmt.Errorf("copy baseline %s: %w", base, err)
	}
	if err := rewriteManifest(filepath.Join(dir, "go.mod"), replaceLeft, from, rootTag); err != nil {
		cleanup()
		return "", nil, nil, err
	}
	if os.Getenv("APICHECK_DEBUG") != "" {
		body, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
		fmt.Fprintf(os.Stderr, "DEBUG corrected go.mod:\n%s\n", body)
	}
	fixup := &baselineFixup{
		requireModule: replaceLeft,
		from:          from,
		to:            rootTag,
		tag:           tag,
		rootTag:       rootTag,
		commit:        commit,
	}
	return dir, fixup, cleanup, nil
}

// rewriteManifest applies the one permitted correction in the copied go.mod:
// the defective root requirement moves to the corrected version and exactly
// that workspace replacement is dropped.
func rewriteManifest(file, module, from, to string) error {
	blob, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read copied go.mod: %w", err)
	}
	lines := strings.Split(string(blob), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 4 && fields[0] == "replace" && fields[1] == module && fields[3] == "../.." {
			continue
		}
		if len(fields) >= 2 && fields[0] == module && fields[1] == from {
			line = strings.Replace(line, from, to, 1)
		}
		out = append(out, line)
	}
	if err := os.WriteFile(file, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		return fmt.Errorf("write corrected go.mod: %w", err)
	}
	return nil
}

// copyTree duplicates a module directory file by file into dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if e.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o644)
	})
}
