package bankfeed_test

import (
	"go/build"
	"strings"
	"testing"
)

// The port and its adapter are platform (bank feed design F1, module
// boundaries rules 1, 2 and 4): the standard library, internal/buildinfo and
// the port's own packages, nothing else — no module, no store, no driver.
func TestBankfeed_Purity(t *testing.T) {
	t.Parallel()
	const (
		module    = "github.com/vantigo-io/vantigo/server/internal/"
		own       = module + "bankfeed"
		buildinfo = module + "buildinfo"
	)
	for _, dir := range []string{".", "folio", "folio/foliotest"} {
		p, err := build.ImportDir(dir, 0)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		if len(p.Imports) == 0 {
			t.Errorf("%s: no imports read; the test is not looking", dir)
		}
		for _, imp := range p.Imports {
			std := !strings.Contains(strings.SplitN(imp, "/", 2)[0], ".")
			if std || imp == buildinfo || imp == own || strings.HasPrefix(imp, own+"/") {
				continue
			}
			t.Errorf("%s imports %s: only the standard library, internal/buildinfo and internal/bankfeed are allowed", dir, imp)
		}
	}
}
