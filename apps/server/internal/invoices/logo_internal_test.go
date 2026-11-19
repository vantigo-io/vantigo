package invoices

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/logoimage"
)

// The logo's codes are written out in this package (the frontend's catalog
// test reads them as source) and must stay the shared package's.
func TestLogoCodes_AreTheSharedPackages(t *testing.T) {
	t.Parallel()
	for _, c := range [][2]string{
		{codeLogoUnsupportedType, logoimage.CodeUnsupportedType},
		{codeLogoTooLarge, logoimage.CodeTooLarge},
		{codeLogoTooWide, logoimage.CodeTooWide},
		{codeLogoTooSmall, logoimage.CodeTooSmall},
		{codeLogoInvalid, logoimage.CodeInvalid},
	} {
		if c[0] != c[1] {
			t.Errorf("invoices says %q, logoimage says %q", c[0], c[1])
		}
	}
}
