package reminderrules

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The engine is a leaf (plan reading 2): its code imports a few packages of
// the standard library only — nothing of the module, no database driver, no
// network, no file system — and never touches the clock, called or not, so
// "one pure function the four callers share" cannot quietly read the store
// or the time.
func TestReminderRules_ImportsNothingOfTheModule(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{"cmp": true, "fmt": true, "math/big": true, "slices": true, "sort": true, "strings": true, "time": true}
	clock := map[string]bool{"Now": true, "Since": true, "Until": true, "After": true, "AfterFunc": true, "Tick": true, "NewTicker": true, "NewTimer": true, "Sleep": true, "Local": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		timeName := ""
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if !allowed[path] {
				t.Errorf("%s imports %q; the engine imports only %v", name, path, allowed)
			}
			if path == "time" {
				timeName = "time"
				if imp.Name != nil {
					timeName = imp.Name.Name
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && timeName != "" && pkg.Name == timeName && clock[sel.Sel.Name] {
				t.Errorf("%s uses time.%s; every day is an input", name, sel.Sel.Name)
			}
			return true
		})
		checked++
	}
	if checked < 8 {
		t.Errorf("checked %d files, want the package's eight", checked)
	}
}
