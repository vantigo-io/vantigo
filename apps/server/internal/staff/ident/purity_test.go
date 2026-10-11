package ident_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ident is a leaf (plan reading 7): the standard library only — nothing of the
// staff module, no other module, no platform package, tests included — so the
// checks can move to a platform package one day without taking anything with
// them, and never read the clock, so a birth date is judged on its digits alone.
func TestIdent_ImportsOnlyTheStandardLibrary(t *testing.T) {
	t.Parallel()
	clock := map[string]bool{"Now": true, "Since": true, "Until": true, "After": true, "AfterFunc": true, "Tick": true, "NewTicker": true, "NewTimer": true, "Sleep": true, "Local": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	const self = "github.com/vantigo-io/vantigo/server/internal/staff/ident"
	code := 0
	for _, name := range files {
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		timeName := ""
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.Contains(strings.SplitN(path, "/", 2)[0], ".") && path != self {
				t.Errorf("%s imports %q; ident imports the standard library only", name, path)
			}
			if path == "time" {
				timeName = "time"
				if imp.Name != nil {
					timeName = imp.Name.Name
				}
			}
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && timeName != "" && pkg.Name == timeName && clock[sel.Sel.Name] {
					t.Errorf("%s uses time.%s; the checks never read the clock", name, sel.Sel.Name)
				}
			}
			return true
		})
		code++
	}
	if code < 3 {
		t.Errorf("checked %d non-test files, want the package's three", code)
	}
}
