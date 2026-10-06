package reminderrules

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The engine is a leaf (plan reading 2): its code imports the standard
// library only — nothing of the module, no database driver, no network, no
// file system — and never reads a clock, so "one pure function the four
// callers share" cannot quietly read the store or the time.
func TestReminderRules_ImportsNothingOfTheModule(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{"fmt": true, "math/big": true, "slices": true, "sort": true, "time": true, "cmp": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if !allowed[path] {
				t.Errorf("%s imports %q; the engine imports only %v", name, path, allowed)
			}
		}
		for _, clock := range []string{"time.Now(", "time.Since(", "time.Until(", "time.Local"} {
			if strings.Contains(string(src), clock) {
				t.Errorf("%s reads the clock (%s); every day is an input", name, clock)
			}
		}
		checked++
	}
	if checked < 8 {
		t.Errorf("checked %d files, want the package's eight", checked)
	}
}
