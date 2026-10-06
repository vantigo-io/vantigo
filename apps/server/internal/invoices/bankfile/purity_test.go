package bankfile_test

import (
	"go/build"
	"strings"
	"testing"
)

// bankfile and its builders are leaves (reading 2): the standard library
// and invoices/kid, nothing else of the module — no store, no server — so
// what a line means is decided by the importer, never here.
func TestBankFile_ImportsNothingOfTheModule(t *testing.T) {
	t.Parallel()
	const kidPath = "github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	for _, dir := range []string{".", "bankfiletest"} {
		p, err := build.ImportDir(dir, 0)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		if len(p.Imports) == 0 {
			t.Errorf("%s: no imports read; the test is not looking", dir)
		}
		for _, imp := range p.Imports {
			std := !strings.Contains(strings.SplitN(imp, "/", 2)[0], ".")
			if !std && imp != kidPath {
				t.Errorf("%s imports %s: only the standard library and invoices/kid are allowed", dir, imp)
			}
		}
	}
}
