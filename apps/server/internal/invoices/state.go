package invoices

import (
	"math/big"
	"time"
)

// This file is a document's derived state (D3): computed, never stored. The
// rule lives in SQL as invoices.document_state (migration 00035), which the
// list, the stats and the customer tab filter with; documentState is its Go
// mirror for one response at a time, never over a list, and
// TestDocumentState_TheGoMirrorAgreesWithTheSQLFunction holds the two to each
// other over every combination. Change one and the test makes you change the
// other.

// The states on the wire, snake_case as every multi-word wire value is.
const (
	stateDraft         = "draft"
	stateIssued        = "issued"
	stateCredited      = "credited"
	statePaid          = "paid"
	stateOverdue       = "overdue"
	statePartiallyPaid = "partially_paid"
	stateOpen          = "open"
)

// invoiceStates are the states an issued invoice can be in: the values the
// list's state filter accepts. A draft and a credit note never match one.
var invoiceStates = []string{stateOpen, statePartiallyPaid, stateOverdue, statePaid, stateCredited}

// documentState is a document's state, the first match winning: a draft; an
// issued credit note; an invoice its issued credit notes cover (credited > 0,
// so an invoice of free lines, gross 0, falls to paid); nothing left open
// (gross − credited − paid ≤ 0); past its due date; partly paid; open.
// credited is the issued credit notes' gross (0 for a credit note), paid the
// live payments' sum, and today the Oslo business day (businessDay). A nil
// dueDate is never overdue, as NULL < today is not true in SQL. A draft's and
// a credit note's state reads none of the figures, which may then be nil.
func documentState(kind, status string, gross, credited, paid *big.Rat, dueDate *time.Time, today time.Time) string {
	switch {
	case status == statusDraft:
		return stateDraft
	case kind == kindCreditNote:
		return stateIssued
	}
	open := new(big.Rat).Sub(gross, credited)
	open.Sub(open, paid)
	switch {
	case credited.Sign() > 0 && credited.Cmp(gross) >= 0:
		return stateCredited
	case open.Sign() <= 0:
		return statePaid
	case dueDate != nil && dueDate.Before(today):
		return stateOverdue
	case paid.Sign() > 0:
		return statePartiallyPaid
	default:
		return stateOpen
	}
}
