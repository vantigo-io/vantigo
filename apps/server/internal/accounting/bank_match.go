package accounting

// Reconciliation (A28, R5, R11): each statement line ends matched to the
// postings on its ledger account that are it, or stays open. A match links
// the postings that are there, or posts the voucher its suggestion showed —
// through Post, as a bank voucher with its own source key, refused when its
// day is locked — and a posting is matched to one line at most (a unique
// index). Every accept, code and undo runs in one transaction that takes the
// line FOR UPDATE first; the same request key again answers the match it
// made, so a double click posts once, and a second person's accept of the
// same line is refused. An undo, while the line's day is open, reverses what
// the match posted, or unlinks.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
)

// maxReconcileDays bounds a reconciliation's period.
const maxReconcileDays = 366

// accountOf reads the bank account a line is on.
func accountOf(ctx context.Context, q *store.Queries, id int64) (bankAccountOf, error) {
	r, err := q.GetBankAccount(ctx, id)
	if err != nil {
		return bankAccountOf{}, err
	}
	return bankAccountOf{id: r.ID, name: r.Name, number: r.AccountNumber, ledger: r.LedgerAccountID}, nil
}

// lineNames is what a view of suggestions names: accounts and tax codes.
type lineNames struct {
	accounts map[int32]store.AccountsForPostingRow
	codes    map[int32]string
	roles    map[string]int32
}

// namesFor reads the names of every account and tax code the suggestions'
// vouchers post to.
func namesFor(ctx context.Context, q *store.Queries, roles map[string]int32, suggestions ...[]suggestion) (lineNames, error) {
	out := lineNames{accounts: map[int32]store.AccountsForPostingRow{}, codes: map[int32]string{}, roles: roles}
	var ids []int32
	need := false
	for _, list := range suggestions {
		for _, sg := range list {
			if sg.voucher == nil {
				continue
			}
			for _, l := range sg.voucher.Lines {
				id := l.AccountID
				if id == 0 {
					id = roles[l.Role]
				}
				ids = append(ids, id)
				if l.TaxCodeID != nil {
					need = true
				}
			}
		}
	}
	if len(ids) > 0 {
		rows, err := q.AccountsForPosting(ctx, ids)
		if err != nil {
			return out, err
		}
		for _, r := range rows {
			out.accounts[r.ID] = r
		}
	}
	if need {
		codes, err := q.TaxCodesByCode(ctx)
		if err != nil {
			return out, err
		}
		for _, c := range codes {
			out.codes[c.ID] = c.Code
		}
	}
	return out, nil
}

// lineView is a line as the contract answers it.
func lineView(l stmtLine) gen.AccountingBankLine {
	out := gen.AccountingBankLine{Id: l.id, StatementId: l.statementID, BankAccountId: l.bankAccountID, BookedOn: apiDate(l.booked),
		ValueOn: apiDatePtr(l.value), Amount: money(l.amount), Text: l.text, Reference: l.reference, CounterpartyName: l.cpName,
		CounterpartyAccount: l.cpAccount, Kind: l.kind, Reversal: l.reversal, Source: l.source, State: l.state,
		Batch: []gen.AccountingBankBatchItem{}, Suggestions: []gen.AccountingBankSuggestion{}, CustomerPayment: l.customerPayment()}
	if l.kid != "" {
		out.Kid = ptr(l.kid)
	}
	if l.bankID != "" {
		out.BankId = ptr(l.bankID)
	}
	if l.batch != nil {
		out.Batch = l.batch
	}
	return out
}

// matchViews is the standing matches named, with their postings.
func matchViews(ctx context.Context, q *store.Queries, matches []store.AccountingStatementMatch) (map[int64]gen.AccountingBankMatch, error) {
	out := map[int64]gen.AccountingBankMatch{}
	if len(matches) == 0 {
		return out, nil
	}
	ids := make([]int64, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m.ID)
		out[m.ID] = gen.AccountingBankMatch{Id: m.ID, Kind: m.Kind, Reason: m.Reason, VoucherId: m.VoucherID, MatchedAt: m.MatchedAt,
			MatchedByUserId: openapi_types.UUID(m.MatchedByUserID), Postings: []gen.AccountingBankMatchedPosting{}}
	}
	rows, err := q.MatchPostings(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		m := out[r.MatchID]
		m.Postings = append(m.Postings, gen.AccountingBankMatchedPosting{PostingId: r.PostingID, VoucherId: r.VoucherID,
			VoucherDisplay: display(r.FiscalYear, r.Number), VoucherDate: apiDate(r.VoucherDate), VoucherKind: r.Kind,
			Description: r.Description, Amount: money(mustRat(r.Amount))})
		out[r.MatchID] = m
	}
	return out, nil
}

// lineOne is one line as an accept, a code or an undo answers it: with its
// match, or with its suggestions while open.
func (s *server) lineOne(ctx context.Context, id int64) (gen.AccountingBankLine, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetStatementLine(ctx, id)
	if noRows(err) {
		return gen.AccountingBankLine{}, notFound()
	}
	if err != nil {
		return gen.AccountingBankLine{}, err
	}
	l := lineFromRow(row)
	out := lineView(l)
	m, err := q.StandingMatchOfLine(ctx, id)
	switch {
	case err == nil:
		views, err := matchViews(ctx, q, []store.AccountingStatementMatch{m})
		if err != nil {
			return out, err
		}
		v := views[m.ID]
		out.Match = &v
		return out, nil
	case !noRows(err):
		return out, err
	}
	account, err := accountOf(ctx, q, l.bankAccountID)
	if err != nil {
		return out, err
	}
	sg, err := newSuggester(ctx, q, account, l.booked, l.booked)
	if err != nil {
		return out, err
	}
	list, err := sg.suggest(ctx, l)
	if err != nil {
		return out, err
	}
	names, err := namesFor(ctx, q, sg.roles, list)
	if err != nil {
		return out, err
	}
	for _, x := range list {
		out.Suggestions = append(out.Suggestions, suggestionView(x, names.accounts, names.codes, names.roles))
	}
	return out, nil
}

// GetAccountingBankReconciliation is a bank account's lines in a period, each
// matched or open with its suggestions, and the balance foot.
func (s *server) GetAccountingBankReconciliation(ctx context.Context, req gen.GetAccountingBankReconciliationRequestObject) (gen.GetAccountingBankReconciliationResponseObject, error) {
	from, to := day(req.Params.From), day(req.Params.To)
	if from.After(to) || to.Sub(from) > maxReconcileDays*24*time.Hour {
		return nil, invalidField("The period cannot be read", "to", codePeriodInvalid, "From on or before to, at most 366 days.")
	}
	var state *string
	if req.Params.State != nil {
		if *req.Params.State != "open" && *req.Params.State != "matched" {
			return nil, invalidField("The period cannot be read", "state", "", "open or matched.")
		}
		state = req.Params.State
	}
	q := store.New(s.deps.Pool)
	row, err := q.GetBankAccount(ctx, req.Id)
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	account := bankAccountOf{id: row.ID, name: row.Name, number: row.AccountNumber, ledger: row.LedgerAccountID}
	rows, err := q.ListStatementLines(ctx, store.ListStatementLinesParams{BankAccountID: req.Id, FromDay: from, ToDay: to, State: state})
	if err != nil {
		return nil, err
	}
	sg, err := newSuggester(ctx, q, account, from, to)
	if err != nil {
		return nil, err
	}
	out := gen.GetAccountingBankReconciliation200JSONResponse{BankAccount: bankAccountView(row), From: apiDate(from), To: apiDate(to),
		Lines: []gen.AccountingBankLine{}}
	var matches []store.AccountingStatementMatch
	var lists [][]suggestion
	lines := make([]stmtLine, 0, len(rows))
	for _, r := range rows {
		l := stmtLine{id: r.ID, statementID: r.StatementID, bankAccountID: r.BankAccountID, booked: r.BookedOn, value: r.ValueOn,
			amount: mustRat(r.Amount), text: r.Text, reference: r.Reference, kid: deref(r.Kid), cpName: r.CounterpartyName,
			cpAccount: r.CounterpartyAccount, bankID: deref(r.BankID), kind: r.Kind, state: r.State, source: r.StatementSource,
			reversal: r.Reversal, batch: batchOf(r.Batch)}
		lines = append(lines, l)
		if r.MatchID != nil {
			matches = append(matches, store.AccountingStatementMatch{ID: *r.MatchID, Kind: deref(r.MatchKind), Reason: deref(r.MatchReason),
				VoucherID: r.MatchVoucherID, MatchedAt: deref(r.MatchedAt), MatchedByUserID: deref(r.MatchedByUserID)})
			lists = append(lists, nil)
			continue
		}
		list, err := sg.suggest(ctx, l)
		if err != nil {
			return nil, err
		}
		lists = append(lists, list)
	}
	views, err := matchViews(ctx, q, matches)
	if err != nil {
		return nil, err
	}
	names, err := namesFor(ctx, q, sg.roles, lists...)
	if err != nil {
		return nil, err
	}
	for i, l := range lines {
		v := lineView(l)
		if r := rows[i]; r.MatchID != nil {
			m := views[*r.MatchID]
			v.Match = &m
		}
		for _, x := range lists[i] {
			v.Suggestions = append(v.Suggestions, suggestionView(x, names.accounts, names.codes, names.roles))
		}
		out.Lines = append(out.Lines, v)
	}
	// The foot: the statement's and the ledger's balance at the period's
	// end, and the ledger's postings in the period no line holds.
	foot, err := s.foot(ctx, q, account, to)
	if err != nil {
		return nil, err
	}
	foot.UnmatchedPostings = []gen.AccountingBankMatchedPosting{}
	for _, c := range sg.candidates {
		if c.VoucherDate.Before(from) || c.VoucherDate.After(to) {
			continue
		}
		foot.UnmatchedPostingsTotal++
		if len(foot.UnmatchedPostings) < 200 {
			foot.UnmatchedPostings = append(foot.UnmatchedPostings, candidateView(c))
		}
	}
	out.Foot = foot
	return out, nil
}

// foot is the balances at the end of a day: the statement's, the ledger's
// and the difference.
func (s *server) foot(ctx context.Context, q *store.Queries, account bankAccountOf, through time.Time) (gen.AccountingBankFoot, error) {
	ledger, err := q.AccountBalanceThrough(ctx, store.AccountBalanceThroughParams{AccountID: account.ledger, Through: through})
	if err != nil {
		return gen.AccountingBankFoot{}, err
	}
	stmt, err := q.StatementBalanceAt(ctx, store.StatementBalanceAtParams{BankAccountID: account.id, Through: through})
	if err != nil {
		return gen.AccountingBankFoot{}, err
	}
	out := gen.AccountingBankFoot{LedgerBalance: money(mustRat(ledger))}
	if stmt.HasBalance {
		b := mustRat(stmt.Balance)
		out.StatementBalance = ptr(money(b))
		out.Difference = ptr(money(sub(b, mustRat(ledger))))
	}
	return out, nil
}

// bankStatus is every active bank account's reconciliation at the end of a
// day: what locking a month or closing a VAT term warns about.
func (s *server) bankStatus(ctx context.Context, through time.Time) (gen.AccountingBankStatus, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.ListBankAccounts(ctx)
	if err != nil {
		return gen.AccountingBankStatus{}, err
	}
	out := gen.AccountingBankStatus{Through: apiDate(through), Reconciled: true, Accounts: []gen.AccountingBankStatusAccount{}}
	for _, r := range rows {
		if !r.Active {
			continue
		}
		account := bankAccountOf{id: r.ID, name: r.Name, number: r.AccountNumber, ledger: r.LedgerAccountID}
		foot, err := s.foot(ctx, q, account, through)
		if err != nil {
			return out, err
		}
		open, err := q.OpenLinesThrough(ctx, store.OpenLinesThroughParams{BankAccountID: r.ID, Through: through})
		if err != nil {
			return out, err
		}
		a := gen.AccountingBankStatusAccount{BankAccountId: r.ID, Name: r.Name, OpenLines: open, StatementBalance: foot.StatementBalance,
			LedgerBalance: foot.LedgerBalance, Difference: foot.Difference}
		a.Reconciled = open == 0 && (a.Difference == nil || mustRat(*a.Difference).Sign() == 0)
		if !a.Reconciled {
			out.Reconciled = false
		}
		out.Accounts = append(out.Accounts, a)
	}
	return out, nil
}

// GetAccountingBankStatus is the reconciliation at a day.
func (s *server) GetAccountingBankStatus(ctx context.Context, req gen.GetAccountingBankStatusRequestObject) (gen.GetAccountingBankStatusResponseObject, error) {
	out, err := s.bankStatus(ctx, day(req.Params.Through))
	if err != nil {
		return nil, err
	}
	return gen.GetAccountingBankStatus200JSONResponse(out), nil
}

// unreconciledRefusal is the lock's refusal to move over bank lines not
// matched or balances that differ, unless confirmed (A28).
func (s *server) unreconciledRefusal(ctx context.Context, through time.Time) error {
	st, err := s.bankStatus(ctx, through)
	if err != nil || st.Reconciled {
		return err
	}
	var parts []string
	for _, a := range st.Accounts {
		if a.Reconciled {
			continue
		}
		part := fmt.Sprintf("%s: %d lines not matched", a.Name, a.OpenLines)
		if a.Difference != nil && mustRat(*a.Difference).Sign() != 0 {
			part += ", the statement differs from the ledger by " + *a.Difference
		}
		parts = append(parts, part)
	}
	return conflict(codeBankUnreconciled, "The bank is not reconciled through "+through.Format(time.DateOnly),
		strings.Join(parts, "; ")+". Lock the month anyway only knowingly.")
}

// lineLock takes a line FOR UPDATE and reads it, with its standing match.
func lineLock(ctx context.Context, q *store.Queries, id int64) (stmtLine, *store.AccountingStatementMatch, error) {
	if _, err := q.LockStatementLine(ctx, id); noRows(err) {
		return stmtLine{}, nil, notFound()
	} else if err != nil {
		return stmtLine{}, nil, err
	}
	row, err := q.GetStatementLine(ctx, id)
	if err != nil {
		return stmtLine{}, nil, err
	}
	m, err := q.StandingMatchOfLine(ctx, id)
	switch {
	case noRows(err):
		return lineFromRow(row), nil, nil
	case err != nil:
		return stmtLine{}, nil, err
	}
	return lineFromRow(row), &m, nil
}

// alreadyMatched answers a line that has a standing match: the same key's
// is the request answered again, another's a conflict.
func alreadyMatched(m *store.AccountingStatementMatch, key string) (bool, error) {
	if m == nil {
		return false, nil
	}
	if m.RequestKey == key {
		return true, nil
	}
	return false, conflict(codeBankLineMatched, "The line is matched already", "Another match stands on it; undo it first.")
}

// matchRecord is what a match writes.
type matchRecord struct {
	kind, key, reason string
	voucher           *int64
	supplierPayment   *int64
	rule              *int64
	ruleVersion       *int32
	postings          []int64
}

// recordMatch writes a match and its links on tx, and the line matched. A
// posting another line holds is refused bank_posting_matched.
func recordMatch(ctx context.Context, tx pgx.Tx, lineID int64, m matchRecord, by uuid.UUID, at time.Time) error {
	q := store.New(tx)
	id, err := q.InsertMatch(ctx, store.InsertMatchParams{StatementLineID: lineID, Kind: m.kind, RequestKey: truncate(m.key, 200),
		Reason: truncate(m.reason, 500), VoucherID: m.voucher, SupplierPaymentID: m.supplierPayment, BankRuleID: m.rule,
		BankRuleVersion: m.ruleVersion, Now: at, UserID: by})
	if err != nil {
		return err
	}
	for _, p := range m.postings {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		if err := store.New(sp).InsertMatchPosting(ctx, store.InsertMatchPostingParams{MatchID: id, PostingID: p}); err != nil {
			_ = sp.Rollback(ctx)
			if violates(err, "ux_statement_match_postings_posting") {
				return conflict(codeBankPostingMatched, "A posting is matched to another line",
					"Another line was matched to the same posting meanwhile. Read the line again.")
			}
			return err
		}
		if err := sp.Commit(ctx); err != nil {
			return err
		}
	}
	return q.SetStatementLineState(ctx, store.SetStatementLineStateParams{ID: lineID, State: "matched"})
}

// postingsOn is a voucher's postings on an account.
func postingsOn(ctx context.Context, q *store.Queries, voucher int64, account int32) ([]int64, error) {
	rows, err := q.PostingsOnAccount(ctx, store.PostingsOnAccountParams{VoucherID: voucher, AccountID: account})
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out, nil
}

// postBankVoucher posts a match's voucher on tx as the person's, with the
// line's source key, dated the line's day — refused when that day is before
// the books or locked.
func (s *server) postBankVoucher(ctx context.Context, tx pgx.Tx, q *store.Queries, l stmtLine, v Voucher, by uuid.UUID) (Posted, error) {
	v.Date, v.DocumentDate = l.booked, l.booked
	v.Source = &Source{Module: sourceAccounting, Kind: sourceBankMatch, ID: l.id, Part: uuid.NewString()}
	v.RuleVersion = bankRuleVersion
	v.PostedBy, v.At = &by, s.now()
	if v.Description == "" {
		v.Description = truncate(firstNonEmpty(l.text, l.cpName, "Bank"), 500)
	}
	posted, err := Post(ctx, tx, v)
	if err != nil {
		return Posted{}, err
	}
	return posted, refuseMoved(ctx, q, posted, l.booked)
}

// accept accepts a line's suggestion by its key on tx (A28): computed again
// under the line's lock, refused stale when it no longer holds.
func (s *server) accept(ctx context.Context, tx pgx.Tx, q *store.Queries, lineID int64, key string, by uuid.UUID) error {
	l, standing, err := lineLock(ctx, q, lineID)
	if err != nil {
		return err
	}
	if done, err := alreadyMatched(standing, key); done || err != nil {
		return err
	}
	if key == suggestInvoices {
		return customerPaymentRefusal()
	}
	account, err := accountOf(ctx, q, l.bankAccountID)
	if err != nil {
		return err
	}
	sg, err := newSuggester(ctx, q, account, l.booked, l.booked)
	if err != nil {
		return err
	}
	list, err := sg.suggest(ctx, l)
	if err != nil {
		return err
	}
	var chosen *suggestion
	for i := range list {
		if list[i].key == key {
			chosen = &list[i]
		}
	}
	if chosen == nil || chosen.kind == suggestInvoices {
		if l.customerPayment() {
			return customerPaymentRefusal()
		}
		return conflict(codeBankSuggestionStale, "The suggestion no longer holds", "Read the line again: what it may be has changed.")
	}
	rec := matchRecord{kind: chosen.kind, key: key, reason: chosen.reason}
	switch chosen.kind {
	case matchFeed, matchPosting, matchBatch:
		ids := make([]int64, 0, len(chosen.postings))
		for _, c := range chosen.postings {
			ids = append(ids, c.ID)
		}
		slices.Sort(ids)
		if _, err := q.LockPostings(ctx, ids); err != nil {
			return err
		}
		rec.postings = ids
	case matchSupplier:
		posted, paymentID, err := s.paySupplierInvoice(ctx, tx, q, *chosen.supplierInvoice, supplierPayment{date: l.booked,
			paidNOK: chosen.paid, bankAccountID: &account.ledger, by: by, at: s.now()})
		if err != nil {
			return err
		}
		rec.voucher, rec.supplierPayment = &posted.ID, &paymentID
		if rec.postings, err = postingsOn(ctx, q, posted.ID, account.ledger); err != nil {
			return err
		}
	default:
		if chosen.kind == matchTransfer && chosen.otherLine != nil {
			// Both lines are taken, in id order, before anything is posted.
			if _, err := q.LockStatementLine(ctx, *chosen.otherLine); err != nil {
				return err
			}
			if _, err := q.StandingMatchOfLine(ctx, *chosen.otherLine); err == nil {
				return conflict(codeBankSuggestionStale, "The other line is matched already", "Read the line again.")
			} else if !noRows(err) {
				return err
			}
		}
		posted, err := s.postBankVoucher(ctx, tx, q, l, *chosen.voucher, by)
		if err != nil {
			return err
		}
		rec.voucher = &posted.ID
		if chosen.ruleID != nil {
			rec.rule, rec.ruleVersion = chosen.ruleID, &chosen.ruleVersion
		}
		if rec.postings, err = postingsOn(ctx, q, posted.ID, account.ledger); err != nil {
			return err
		}
		if chosen.kind == matchTransfer && chosen.otherLine != nil {
			other, err := postingsOn(ctx, q, posted.ID, chosen.otherLedger)
			if err != nil {
				return err
			}
			if err := recordMatch(ctx, tx, *chosen.otherLine, matchRecord{kind: matchTransfer, key: key + ":other", reason: rec.reason,
				voucher: &posted.ID, postings: other}, by, s.now()); err != nil {
				return err
			}
		}
	}
	return recordMatch(ctx, tx, l.id, rec, by, s.now())
}

// customerPaymentRefusal is R11's refusal.
func customerPaymentRefusal() error {
	return conflict(codeBankCustomerPayment, "A customer payment is registered in Invoices",
		"Money in from a customer is registered on the invoice in Invoices — through its bank import, or by hand — and its posting then matches this line. It is never coded as income here.")
}

// AcceptAccountingBankSuggestion accepts one suggestion.
func (s *server) AcceptAccountingBankSuggestion(ctx context.Context, req gen.AcceptAccountingBankSuggestionRequestObject) (gen.AcceptAccountingBankSuggestionResponseObject, error) {
	if req.Body == nil || strings.TrimSpace(req.Body.Key) == "" {
		return nil, invalidField("The suggestion cannot be accepted", "key", "", "The suggestion's key is required.")
	}
	by := callerID(ctx)
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		return s.accept(ctx, tx, q, req.Id, req.Body.Key, by)
	})
	if deadlocked(err) {
		// Two people matched the two sides of one transfer at once.
		err = conflict(codeBankLineMatched, "The line is being matched", "Someone matched it at the same moment. Read it again.")
	}
	if err != nil {
		return nil, err
	}
	out, err := s.lineOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.AcceptAccountingBankSuggestion200JSONResponse(out), nil
}

// AcceptAccountingBankExact accepts each named line's lone exact suggestion,
// each in its own transaction.
func (s *server) AcceptAccountingBankExact(ctx context.Context, req gen.AcceptAccountingBankExactRequestObject) (gen.AcceptAccountingBankExactResponseObject, error) {
	if req.Body == nil || len(req.Body.LineIds) == 0 || len(req.Body.LineIds) > 500 {
		return nil, invalidField("The suggestions cannot be accepted", "lineIds", "", "From 1 to 500 lines.")
	}
	by := callerID(ctx)
	out := gen.AcceptAccountingBankExact200JSONResponse{Accepted: []int64{}, Skipped: []int64{}}
	q := store.New(s.deps.Pool)
	for _, id := range req.Body.LineIds {
		key, err := s.exactKey(ctx, q, id)
		if err != nil {
			return nil, err
		}
		if key == "" {
			out.Skipped = append(out.Skipped, id)
			continue
		}
		err = s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error { return s.accept(ctx, tx, q, id, key, by) })
		var refused *refusal
		switch {
		case errors.As(err, &refused), deadlocked(err):
			out.Skipped = append(out.Skipped, id)
		case err != nil:
			return nil, err
		default:
			out.Accepted = append(out.Accepted, id)
		}
	}
	return out, nil
}

// exactKey is an open line's lone exact suggestion's key, "" when it has none.
func (s *server) exactKey(ctx context.Context, q *store.Queries, id int64) (string, error) {
	row, err := q.GetStatementLine(ctx, id)
	if noRows(err) {
		return "", nil
	}
	if err != nil || row.State != "open" {
		return "", err
	}
	l := lineFromRow(row)
	account, err := accountOf(ctx, q, l.bankAccountID)
	if err != nil {
		return "", err
	}
	sg, err := newSuggester(ctx, q, account, l.booked, l.booked)
	if err != nil {
		return "", err
	}
	list, err := sg.suggest(ctx, l)
	if err != nil || len(list) == 0 || !list[0].exact {
		return "", err
	}
	return list[0].key, nil
}

// CodeAccountingBankLine posts a bank voucher for the line with the
// accounts the person gives (A28, R5, R11).
func (s *server) CodeAccountingBankLine(ctx context.Context, req gen.CodeAccountingBankLineRequestObject) (gen.CodeAccountingBankLineResponseObject, error) {
	const title = "The line cannot be coded"
	if req.Body == nil {
		return nil, invalidField(title, "body", "", "A body is required.")
	}
	b := *req.Body
	if strings.TrimSpace(b.Key) == "" {
		return nil, invalidField(title, "key", "", "A request key is required.")
	}
	by := callerID(ctx)
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		l, standing, err := lineLock(ctx, q, req.Id)
		if err != nil {
			return err
		}
		if done, err := alreadyMatched(standing, "code:"+b.Key); done || err != nil {
			return err
		}
		if l.customerPayment() {
			return customerPaymentRefusal()
		}
		account, err := accountOf(ctx, q, l.bankAccountID)
		if err != nil {
			return err
		}
		input := gen.AccountingJournalInput{VoucherDate: apiDate(l.booked), Entry: b.Entry,
			Description: strings.TrimSpace(deref(b.Description))}
		if input.Description == "" {
			input.Description = truncate(firstNonEmpty(l.text, l.cpName, "Bank"), 500)
		}
		if input.Entry == nil {
			input.Entry = ptr(gen.Gross)
		}
		input.Lines = append(input.Lines, gen.AccountingJournalLineInput{AccountId: account.ledger, Amount: money(l.amount),
			Text: ptr(truncate(l.text, 500))})
		var accountIDs []int32
		for i, cl := range b.Lines {
			if cl.OpenItemId != nil {
				if err := settleableItem(ctx, q, *cl.OpenItemId, fmt.Sprintf("lines[%d].openItemId", i)); err != nil {
					return err
				}
			}
			accountIDs = append(accountIDs, cl.AccountId)
			input.Lines = append(input.Lines, gen.AccountingJournalLineInput(cl))
		}
		if l.amount.Sign() > 0 {
			// Money in coded to a sales income account is a customer payment
			// that belongs in Invoices (R11).
			accounts, err := q.AccountsForPosting(ctx, accountIDs)
			if err != nil {
				return err
			}
			for _, a := range accounts {
				if a.Kind == "income" && strings.HasPrefix(a.Number, "3") {
					return customerPaymentRefusal()
				}
			}
		}
		postings, err := expandJournal(ctx, q, input)
		if err != nil {
			return renumberLines(err)
		}
		v := Voucher{Kind: "bank", Rule: bankRuleCoded, Description: input.Description}
		for _, p := range postings {
			v.Lines = append(v.Lines, p.line)
		}
		posted, err := s.postBankVoucher(ctx, tx, q, l, v, by)
		if err != nil {
			return renumberLines(err)
		}
		ids, err := postingsOn(ctx, q, posted.ID, account.ledger)
		if err != nil {
			return err
		}
		return recordMatch(ctx, tx, l.id, matchRecord{kind: matchCoded, key: "code:" + b.Key, voucher: &posted.ID, postings: ids,
			reason: "Coded"}, by, s.now())
	})
	if err != nil {
		return nil, err
	}
	out, err := s.lineOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.CodeAccountingBankLine201JSONResponse(out), nil
}

// renumberLines points a refusal's field errors at the body's lines: the
// journal's line 0 is the bank's, so its lines[n] is the body's lines[n-1].
func renumberLines(err error) error {
	var r *refusal
	if !errors.As(err, &r) || r.problem.Errors == nil {
		return err
	}
	out := map[string][]string{}
	for k, v := range *r.problem.Errors {
		if rest, ok := strings.CutPrefix(k, "lines["); ok {
			if i := strings.Index(rest, "]"); i > 0 {
				if n, perr := strconv.Atoi(rest[:i]); perr == nil {
					if n == 0 {
						k = "amount" + rest[i+1:]
					} else {
						k = fmt.Sprintf("lines[%d]%s", n-1, rest[i+1:])
					}
				}
			}
		}
		out[k] = append(out[k], v...)
	}
	r.problem.Errors = &out
	return err
}

// settleableItem refuses an open item a bank line may not settle (R5): one
// that stands for a document in Invoices, or a supplier invoice's.
func settleableItem(ctx context.Context, q *store.Queries, id int64, field string) error {
	item, err := q.GetOpenItem(ctx, id)
	if noRows(err) {
		return invalidField("The line cannot be coded", field, codeOpenItemUnknown, "No such open item.")
	}
	if err != nil {
		return err
	}
	if item.SourceDocumentID != nil {
		return conflict(codeSettlementSourceDoc, "The item is paid in Invoices",
			"It stands for an invoice in Invoices: register the payment on the invoice there.")
	}
	if item.Kind == "supplier" {
		if _, err := q.SupplierInvoiceOfItem(ctx, item.ID); err == nil {
			return conflict(codeSettlementSupplierInv, "The item is a supplier invoice's",
				"Accept the supplier invoice's suggestion: the payment is registered on the invoice.")
		} else if !noRows(err) {
			return err
		}
	}
	return nil
}

// UndoAccountingBankMatch undoes a line's match while its day is open.
func (s *server) UndoAccountingBankMatch(ctx context.Context, req gen.UndoAccountingBankMatchRequestObject) (gen.UndoAccountingBankMatchResponseObject, error) {
	by := callerID(ctx)
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		return s.undo(ctx, tx, q, req.Id, by)
	})
	if err != nil {
		return nil, err
	}
	out, err := s.lineOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.UndoAccountingBankMatch200JSONResponse(out), nil
}

// undo undoes a line's match on tx: a supplier payment removed through its
// own path, another voucher reversed and every match standing on it undone
// (a transfer's other line too), or the links let go.
func (s *server) undo(ctx context.Context, tx pgx.Tx, q *store.Queries, lineID int64, by uuid.UUID) error {
	l, m, err := lineLock(ctx, q, lineID)
	if err != nil {
		return err
	}
	if m == nil {
		return conflict(codeBankLineNotMatched, "The line is not matched", "")
	}
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return err
	}
	if settings.LockedThrough != nil && !l.booked.After(*settings.LockedThrough) {
		return conflict(codePeriodLocked, "The period is locked",
			"The books are locked through "+settings.LockedThrough.Format(time.DateOnly)+"; a match in it stands.")
	}
	matches := []store.AccountingStatementMatch{*m}
	var undoVoucher *int64
	switch {
	case m.SupplierPaymentID != nil:
		invoice, err := q.SupplierPaymentInvoice(ctx, *m.SupplierPaymentID)
		if err != nil {
			return err
		}
		posted, err := s.removeSupplierPayment(ctx, tx, q, invoice, *m.SupplierPaymentID, by)
		if err != nil {
			return err
		}
		undoVoucher = &posted.ID
	case m.VoucherID != nil:
		others, err := q.StandingMatchesOfVoucher(ctx, *m.VoucherID)
		if err != nil {
			return err
		}
		for _, o := range others {
			if o.ID == m.ID {
				continue
			}
			if _, err := q.LockStatementLine(ctx, o.StatementLineID); err != nil {
				return err
			}
			matches = append(matches, o)
		}
		v, err := q.GetVoucher(ctx, *m.VoucherID)
		if err != nil {
			return err
		}
		rev, err := reversal(ctx, q, *m.VoucherID, v.VoucherDate, "Bank match undone", settings.ChartLanguage, &by)
		if err != nil {
			return err
		}
		rev.Source = &Source{Module: sourceAccounting, Kind: sourceBankMatchUndone, ID: m.ID}
		rev.Rule, rev.RuleVersion, rev.At = bankRuleUndone, bankRuleVersion, s.now()
		posted, err := Post(ctx, tx, rev)
		if err != nil {
			return err
		}
		if err := refuseMoved(ctx, q, posted, v.VoucherDate); err != nil {
			return err
		}
		undoVoucher = &posted.ID
	}
	for _, x := range matches {
		if err := q.UndoMatch(ctx, store.UndoMatchParams{ID: x.ID, Now: ptr(s.now()), UserID: &by, UndoVoucherID: undoVoucher}); err != nil {
			return err
		}
		if err := q.UnstandMatchPostings(ctx, x.ID); err != nil {
			return err
		}
		if err := q.SetStatementLineState(ctx, store.SetStatementLineStateParams{ID: x.StatementLineID, State: "open"}); err != nil {
			return err
		}
	}
	return nil
}
