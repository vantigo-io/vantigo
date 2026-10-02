package invoices_test

import (
	"maps"
	"math/big"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// A document's state is one rule written twice on purpose (D3):
// invoices.document_state, the SQL function migration 00035 defines and the
// list, the stats and the customer tab filter with, and documentState, its Go
// mirror, which answers the state on one response. This asks both about every
// combination of kind, status, gross nothing or something, credited below,
// equal to and above gross, paid nothing, below, equal to and above what is
// open, and a due date before, on and after today — or none — and fails on any
// pair they disagree about. It also proves the grid reaches every state the
// list filters by, so a branch neither side can reach is not agreed vacuously.
func TestDocumentState_TheGoMirrorAgreesWithTheSQLFunction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	today := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	yesterday, tomorrow := today.AddDate(0, 0, -1), today.AddDate(0, 0, 1)

	reached := map[string]bool{}
	for _, kind := range []string{"invoice", "credit_note"} {
		for _, status := range []string{"draft", "issued"} {
			for _, gross := range []string{"0", "1000"} {
				for _, credited := range []string{"0", "400", "1000", "1200"} {
					for _, paid := range []string{"0", "300", "600", "1000"} {
						for _, due := range []*time.Time{&yesterday, &today, &tomorrow, nil} {
							var dueText *string
							if due != nil {
								text := due.Format(time.DateOnly)
								dueText = &text
							}
							inSQL := modtest.One[string](t, h.Harness,
								`SELECT invoices.document_state($1, $2, $3::numeric, $4::numeric, $5::numeric, $6::date, $7::date)`,
								kind, status, gross, credited, paid, dueText, today.Format(time.DateOnly))
							inGo := invoices.DocumentState(kind, status, decimalOf(t, gross), decimalOf(t, credited), decimalOf(t, paid), due, today)
							if inGo != inSQL {
								dueName := "none"
								if dueText != nil {
									dueName = *dueText
								}
								t.Errorf("%s %s, gross %s, credited %s, paid %s, due %s: Go says %q, SQL says %q",
									status, kind, gross, credited, paid, dueName, inGo, inSQL)
							}
							reached[inSQL] = true
						}
					}
				}
			}
		}
	}
	for _, state := range append([]string{"draft", "issued"}, invoices.InvoiceStates...) {
		if !reached[state] {
			t.Errorf("no combination reaches %q", state)
		}
	}
	if len(reached) != 7 {
		t.Errorf("the grid reaches %v, want exactly the seven states", slices.Sorted(maps.Keys(reached)))
	}
}

// decimalOf is a decimal text as the exact number it names.
func decimalOf(t *testing.T, text string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		t.Fatalf("%q is not a decimal", text)
	}
	return r
}
