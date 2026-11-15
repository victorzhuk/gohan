// Package lint detects pgx transactions held open across model or tool
// calls. Conditional or nested aliasing of a transaction binding is out
// of scope: only the syntactic binding name is tracked.
package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Boundary names the kind of call the transaction crossed.
type Boundary string

const (
	// Invoke marks a model invocation boundary.
	Invoke Boundary = "Invoke"
	// Call marks a tool call boundary.
	Call Boundary = "Call"
	// Generate marks a generation boundary.
	Generate Boundary = "Generate"
)

var boundaries = map[string]Boundary{
	"Invoke":   Invoke,
	"Call":     Call,
	"Generate": Generate,
}

// Finding reports a transaction binding held across a boundary call.
type Finding struct {
	Pos      token.Position
	Binding  string
	Boundary Boundary
}

// Check parses every Go file under each dir and reports live pgx
// transactions spanning Invoke, Call or Generate calls. A dir ending
// in "/..." is walked recursively; otherwise only its own files count.
func Check(dirs ...string) ([]Finding, error) {
	var findings []Finding
	var firstErr error
	fset := token.NewFileSet()
	for _, dir := range dirs {
		recursive := strings.HasSuffix(dir, "/...")
		root := strings.TrimSuffix(dir, "/...")
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return fmt.Errorf("walk %s: %w", path, err)
			}
			if d.IsDir() {
				if path != root && !recursive {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			findings = append(findings, scanFile(fset, file)...)
			return nil
		})
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	sortFindings(findings)
	return findings, firstErr
}

func sortFindings(fs []Finding) {
	sort.Slice(fs, func(i, j int) bool {
		a, b := fs[i].Pos, fs[j].Pos
		if a.Filename != b.Filename {
			return a.Filename < b.Filename
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
}

// scanFile walks each function declaration, collects tx bindings and
// reports boundary calls inside each binding's live span.
func scanFile(fset *token.FileSet, file *ast.File) []Finding {
	var findings []Finding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		for _, b := range bindings(fn) {
			end := spanEnd(fn, b)
			for _, call := range calls(fn) {
				if call.Pos() < b.pos || call.Pos() >= end {
					continue
				}
				if bd, ok := boundaryOf(call); ok {
					findings = append(findings, Finding{
						Pos:      fset.Position(call.Pos()),
						Binding:  b.name,
						Boundary: bd,
					})
				}
			}
		}
	}
	return findings
}

type binding struct {
	name string
	pos  token.Pos
}

// bindings collects tx bindings in a function: `v, err := recv.Begin(...)`,
// `var v pgx.Tx` declarations, and parameters typed pgx.Tx.
func bindings(fn *ast.FuncDecl) []binding {
	var out []binding
	for _, param := range fn.Type.Params.List {
		if _, ok := pgxTxType(param.Type); ok && len(param.Names) > 0 {
			out = append(out, binding{param.Names[0].Name, param.Pos()})
		}
	}
	for _, stmt := range fn.Body.List {
		switch s := stmt.(type) {
		case *ast.AssignStmt:
			if len(s.Lhs) != 2 || len(s.Rhs) != 1 {
				continue
			}
			call, ok := s.Rhs[0].(*ast.CallExpr)
			if !ok {
				continue
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Begin" {
				if id, ok := s.Lhs[0].(*ast.Ident); ok {
					out = append(out, binding{id.Name, s.Pos()})
				}
			}
		case *ast.DeclStmt:
			gen, ok := s.Decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				if _, ok := pgxTxType(vs.Type); ok {
					for _, name := range vs.Names {
						out = append(out, binding{name.Name, s.Pos()})
					}
				}
			}
		}
	}
	return out
}

// pgxTxType reports whether the type expression prints as pgx.Tx.
func pgxTxType(expr ast.Expr) (string, bool) {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "pgx" || sel.Sel.Name != "Tx" {
		return "", false
	}
	return pkg.Name + "." + sel.Sel.Name, true
}

// calls returns every call expression in the function body.
func calls(fn *ast.FuncDecl) []*ast.CallExpr {
	var out []*ast.CallExpr
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			out = append(out, call)
		}
		return true
	})
	return out
}

// spanEnd returns the position where a binding stops being live: the
// first non-deferred Commit or Rollback call on it, else the function
// body end. A deferred rollback runs at return and never ends the span.
func spanEnd(fn *ast.FuncDecl, b binding) token.Pos {
	end := fn.Body.End()
	deferred := map[token.Pos]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		d, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		if endsSpan(d.Call, b) {
			deferred[d.Call.Pos()] = true
		}
		return true
	})
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || deferred[call.Pos()] {
			return true
		}
		if endsSpan(call, b) {
			end = call.Pos()
		}
		return true
	})
	return end
}

// endsSpan reports whether the call is a Commit or Rollback on the binding.
func endsSpan(call *ast.CallExpr, b binding) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != b.name {
		return false
	}
	return sel.Sel.Name == "Commit" || sel.Sel.Name == "Rollback"
}

// boundaryOf reports whether the call is an Invoke, Call or Generate.
func boundaryOf(call *ast.CallExpr) (Boundary, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	bd, ok := boundaries[sel.Sel.Name]
	return bd, ok
}
