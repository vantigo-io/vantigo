package payments_test

import (
	"go/build"
	"strings"
	"testing"
)

// The port and its adapter are platform (D13, module boundaries rules 1, 2
// and 4): the standard library, internal/buildinfo and the port's own
// packages, nothing else — no module, no store, no driver. The one outside
// import is the QR encoder, and only qrsvg, the leaf that renders with it,
// holds it.
func TestPayments_Purity(t *testing.T) {
	t.Parallel()
	const (
		module    = "github.com/vantigo-io/vantigo/server/internal/"
		own       = module + "payments"
		buildinfo = module + "buildinfo"
		encoder   = "github.com/boombuler/barcode"
	)
	for _, dir := range []string{".", "qrsvg", "vipps", "vipps/vippstest"} {
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
			if dir == "qrsvg" && (imp == encoder || strings.HasPrefix(imp, encoder+"/")) {
				continue
			}
			t.Errorf("%s imports %s: only the standard library, internal/buildinfo, internal/payments and, in qrsvg, the QR encoder are allowed", dir, imp)
		}
	}
}
