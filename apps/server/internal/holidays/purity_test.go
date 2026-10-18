package holidays

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The calendar is a leaf of the platform: its code imports the time package
// only — nothing of the module, no database, no network, no file system — and
// never touches the clock, called or not, so every day is an input and the
// answer for a day never changes with when it is asked.
func TestCalendar_ImportsTimeOnly(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{"time": true}
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
				t.Errorf("%s imports %q; the calendar imports only %v", name, path, allowed)
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
	if checked < 2 {
		t.Errorf("checked %d files, want the package's two", checked)
	}
}
