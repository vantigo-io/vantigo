package contracts

import "strconv"

// The events the expenses module publishes onto the platform outbox
// (accounting design A20, R2). Each is published by the trigger that writes
// the fact to expenses.journal_facts (migration 00184), at the commit of the
// transaction that approved, unapproved, reimbursed or undid the reimbursement
// of a standalone expense or a travel claim, under its row lock. A payload
// carries the fact's seq, the subject's id, the business day and, for an
// expense, its currency and gross — never a name (O3).
// contracts.ExpenseJournal's Facts is the source; these only make a reader
// prompt.

// ExpenseEntryEvent is the payload of expenses.entry.approved,
// expenses.entry.unapproved, expenses.entry.reimbursed and
// expenses.entry.reimbursement_undone, v1. Occurrence names which.
type ExpenseEntryEvent struct {
	Occurrence  string `json:"-"`
	Seq         int64  `json:"seq"`
	EntryID     int64  `json:"entryId"`
	Date        string `json:"date"`
	Currency    string `json:"currency"`
	GrossAmount string `json:"grossAmount"`
}

func (e ExpenseEntryEvent) EventName() string   { return "expenses.entry." + e.Occurrence }
func (ExpenseEntryEvent) EventVersion() int     { return 1 }
func (e ExpenseEntryEvent) AggregateID() string { return strconv.FormatInt(e.EntryID, 10) }

// ExpenseClaimEvent is the payload of expenses.claim.approved,
// expenses.claim.unapproved, expenses.claim.reimbursed and
// expenses.claim.reimbursement_undone, v1: Lines is how many lines the claim
// had when the fact committed.
type ExpenseClaimEvent struct {
	Occurrence string `json:"-"`
	Seq        int64  `json:"seq"`
	ClaimID    int64  `json:"claimId"`
	Date       string `json:"date"`
	Lines      int    `json:"lines"`
}

func (e ExpenseClaimEvent) EventName() string   { return "expenses.claim." + e.Occurrence }
func (ExpenseClaimEvent) EventVersion() int     { return 1 }
func (e ExpenseClaimEvent) AggregateID() string { return strconv.FormatInt(e.ClaimID, 10) }

// ExpenseEventNames is every event name Expenses' fact log publishes, entry
// and claim, in occurrence order.
var ExpenseEventNames = []string{
	"expenses.entry.approved", "expenses.entry.unapproved", "expenses.entry.reimbursed",
	"expenses.entry.reimbursement_undone",
	"expenses.claim.approved", "expenses.claim.unapproved", "expenses.claim.reimbursed",
	"expenses.claim.reimbursement_undone",
}
