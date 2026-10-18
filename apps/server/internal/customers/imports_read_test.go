package customers

import (
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/fileimport"
)

// This file is the customers import's caps (customers import wizard design
// D2), without a server. The reader itself is internal/fileimport's and tested
// there; what is this module's is the bounds it passes, and the refusals a
// person reads at them, word for word.

func TestImportUploadLimits_RefuseAtTheCustomersCaps(t *testing.T) {
	t.Parallel()
	wide := strings.Repeat("c;", maxImportUploadColumns) + "c\n"
	var tall strings.Builder
	tall.WriteString("name\n")
	for i := 0; i <= maxImportUploadRows; i++ {
		tall.WriteString("x\n")
	}
	for _, tc := range []struct {
		name string
		data string
		want string
	}{
		{"empty", "", "The file is empty; it needs a header row naming its columns"},
		{"too wide", wide, "The file has more than 200 columns, which is more than one import takes"},
		{"too many rows", tall.String(), "The file holds more than 100000 rows, which is more than one import takes; import it in slices of at most 100000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, refusal := fileimport.Read([]byte(tc.data), importUploadLimits)
			if refusal != tc.want {
				t.Errorf("refusal = %q, want %q", refusal, tc.want)
			}
		})
	}
	// Exactly at the caps is read.
	var atCap strings.Builder
	atCap.WriteString(strings.Repeat("c;", maxImportUploadColumns-1) + "c\n")
	for i := 0; i < maxImportUploadRows; i++ {
		atCap.WriteString("x\n")
	}
	file, refusal := fileimport.Read([]byte(atCap.String()), importUploadLimits)
	if refusal != "" || len(file.Header) != maxImportUploadColumns || len(file.Rows) != maxImportUploadRows {
		t.Errorf("at the caps: refusal %q, %d columns, %d rows", refusal, len(file.Header), len(file.Rows))
	}
}
