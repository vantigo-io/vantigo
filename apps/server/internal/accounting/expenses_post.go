package accounting

// Expenses post themselves (A20 as amended by R2): the catch-up's third
// stream reads Expenses' fact log (contracts.ExpenseJournal) after its own
// cursor, and each fact posts in its own transaction with the source key
// (expenses, <subject>.<occurrence>, the subject's id, the fact's seq) — so
// approve, unapprove, approve is three vouchers: the cost, its reversal, the
// cost again.
//
//   - approved, a line the employee is owed (an outlay they paid, mileage, a
//     per diem): debit the category's account — the expense rule's, else the
//     default chart's 7700 Other costs — credit employee_payables;
//   - approved, a line the company paid: debit the category's account,
//     credit bank;
//   - reimbursed: debit employee_payables, credit bank, by what the
//     standing approval credited employee_payables;
//   - unapproved and reimbursement_undone: the voucher of the fact undone,
//     read back, every line turned (R15).
//
// VAT is deducted only when the category's rule allows it, the books are
// VAT-registered, the line is an outlay in NOK with VAT recorded, and VAT
// over net is a current input rate within one øre per hundred kroner (and at
// least one øre); the line then posts its net on the account with that
// rate's input code and its VAT on the code's VAT account. Otherwise it
// posts gross with no deduction, and a VAT a deductible category did not get
// deducted is listed under Needs attention as vat_not_deducted, a row that
// posted and never blocks. A line in another currency waits as
// amount_in_nok_needed until a person enters its NOK amount; an expense of
// Expenses' own supplier_invoice kind waits as enter_as_supplier_invoice,
// for Make a supplier invoice. A fact dated before the books start is
// skipped. What waits, waits in order: a fact of an entry or claim with an
// earlier open row waits behind it (waiting_on).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/docmath"
)

// The source module of every expense voucher.
const sourceExpenses = "expenses"

// The system rules an expense fact posts by, with their version (A14).
const (
	expenseRuleApproved                  = "expense.approved"
	expenseRuleUnapproved                = "expense.unapproved"
	expenseRuleReimbursed                = "expense.reimbursed"
	expenseRuleReimbursementUndone       = "expense.reimbursement_undone"
	expenseRuleVersion             int32 = 1
)

// The reasons an expense fact is under Needs attention, besides the sales
// stream's and an engine refusal's.
const (
	reasonAmountInNOKNeeded      = "amount_in_nok_needed"
	reasonEnterAsSupplierInvoice = "enter_as_supplier_invoice"
	reasonVATNotDeducted         = "vat_not_deducted"
	roleEmployeePayables         = "employee_payables"
	// fallbackExpenseAccountNumber is the default chart's Other costs, where
	// an expense of a category no rule names is debited.
	fallbackExpenseAccountNumber = "7700"
)

// readExpenseStream reads up to limit facts of Expenses' log after position,
// with no transaction open.
func readExpenseStream(ctx context.Context, journal contracts.ExpenseJournal, position int64, limit int) ([]sourceItem, error) {
	if journal == nil {
		return nil, nil
	}
	facts, err := journal.Facts(ctx, position, limit)
	if err != nil {
		return nil, err
	}
	out := make([]sourceItem, 0, len(facts))
	for i := range facts {
		out = append(out, sourceItem{stream: streamExpenses, position: facts[i].Seq, expense: &facts[i]})
	}
	return out, nil
}

// expenseSource is an expense fact's source key: the part is its seq (R2).
func expenseSource(f contracts.ExpenseFact) *Source {
	return &Source{Module: sourceExpenses, Kind: f.Subject + "." + f.Occurrence, ID: f.SubjectID,
		Part: strconv.FormatInt(f.Seq, 10)}
}

// expenseOrderKey is what an expense fact waits behind: its entry or claim.
func expenseOrderKey(f contracts.ExpenseFact) string {
	return fmt.Sprintf("%s:%d", f.Subject, f.SubjectID)
}

// expenseNOKTotal is the fact's lines' gross summed, when every line is in
// NOK.
func expenseNOKTotal(f contracts.ExpenseFact) (string, bool) {
	total := new(big.Rat)
	for _, l := range f.Lines {
		if l.Currency != currency {
			return "", false
		}
		g, ok := parseAmount(l.Gross)
		if !ok {
			return "", false
		}
		total.Add(total, g)
	}
	return money(total), true
}

// expenseLineNote is, on a row under Needs attention, one line of the fact
// in another currency and the NOK amount a person entered for it.
type expenseLineNote struct {
	Kind      string  `json:"kind"`
	Currency  string  `json:"currency"`
	Gross     string  `json:"gross"`
	AmountNOK *string `json:"amountNok"`
}

// parseLineNotes is a row's nok_amounts.
func parseLineNotes(text string) map[string]expenseLineNote {
	out := map[string]expenseLineNote{}
	if text == "" {
		return out
	}
	_ = json.Unmarshal([]byte(text), &out)
	return out
}

// noteAmounts is the NOK amounts a row's notes carry, by entry.
func noteAmounts(text string) map[int64]*big.Rat {
	out := map[int64]*big.Rat{}
	for k, n := range parseLineNotes(text) {
		id, err := strconv.ParseInt(k, 10, 64)
		if err != nil || n.AmountNOK == nil {
			continue
		}
		if a, ok := parseAmount(*n.AmountNOK); ok {
			out[id] = a
		}
	}
	return out
}

// expenseBooks is what the books hold that an expense voucher is built
// against.
type expenseBooks struct {
	language   string
	booksStart time.Time
	registered bool
	rules      map[int32]store.ExpenseRulesRow
	fallback   *int32
	rates      []store.InputRatesRow
}

// readExpenseBooks reads them on q, the fact's own transaction, the input
// rates at the fact's day.
func readExpenseBooks(ctx context.Context, q *store.Queries, on time.Time) (expenseBooks, error) {
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return expenseBooks{}, err
	}
	b := expenseBooks{language: settings.ChartLanguage, booksStart: settings.BooksStart, registered: settings.VatTerm != "none",
		rules: map[int32]store.ExpenseRulesRow{}}
	rules, err := q.ExpenseRules(ctx)
	if err != nil {
		return b, err
	}
	for _, r := range rules {
		if r.ExpenseCategoryID != nil {
			b.rules[*r.ExpenseCategoryID] = r
		}
	}
	id, err := q.AccountByNumber(ctx, fallbackExpenseAccountNumber)
	switch {
	case noRows(err):
	case err != nil:
		return b, err
	default:
		b.fallback = &id
	}
	if b.rates, err = q.InputRates(ctx, on); err != nil {
		return b, err
	}
	return b, nil
}

func (b expenseBooks) text(en, nb string, args ...any) string {
	return salesBooks{language: b.language}.text(en, nb, args...)
}

// inferredCode is the input code an expense's VAT matches (R2): VAT over net
// equal to a current input rate within one øre per hundred kroner, and at
// least one øre, the nearest such rate; nil when none matches.
func inferredCode(rates []store.InputRatesRow, gross, vat *big.Rat) *store.InputRatesRow {
	net := sub(gross, vat)
	if vat.Sign() <= 0 || net.Sign() <= 0 {
		return nil
	}
	tolerance := new(big.Rat).Quo(net, big.NewRat(10000, 1))
	if tolerance.Cmp(big.NewRat(1, 100)) < 0 {
		tolerance = big.NewRat(1, 100)
	}
	var best *store.InputRatesRow
	var bestDiff *big.Rat
	for i := range rates {
		expected := docmath.Round2(new(big.Rat).Quo(new(big.Rat).Mul(net, mustRat(rates[i].RatePercent)), hundred))
		diff := abs(sub(vat, expected))
		if diff.Cmp(tolerance) > 0 {
			continue
		}
		if best == nil || diff.Cmp(bestDiff) < 0 {
			best, bestDiff = &rates[i], diff
		}
	}
	return best
}

// expenseCost is an approval's voucher (R2) and what it says beside it: the
// lines not deducted although their category allows it.
type expenseCost struct {
	voucher     *Voucher
	notDeducted []int64
}

// expenseApprovalVoucher is the cost of an approved expense or claim: per
// line its category's account debited — net with the inferred input code and
// its VAT on the code's VAT account, or gross — and employee_payables or bank
// credited. nil when the lines are all zero.
func expenseApprovalVoucher(b expenseBooks, f contracts.ExpenseFact, nok map[int64]*big.Rat) (expenseCost, error) {
	var lines []Line
	var notDeducted []int64
	var waiting []string
	for _, l := range f.Lines {
		if l.Kind == contracts.ExpenseKindSupplierInvoice {
			return expenseCost{}, &unpostable{reason: reasonEnterAsSupplierInvoice,
				detail: b.text("An expense of the supplier invoice kind is entered as a supplier invoice in Accounting: use Make a supplier invoice.",
					"En utgift av typen leverandørfaktura føres som leverandørfaktura i Regnskap: bruk Lag leverandørfaktura.")}
		}
		gross, ok := parseAmount(l.Gross)
		if !ok {
			return expenseCost{}, fmt.Errorf("accounting: expense %d's gross %q", l.EntryID, l.Gross)
		}
		foreign := l.Currency != currency
		if foreign {
			amount, ok := nok[l.EntryID]
			if !ok {
				waiting = append(waiting, fmt.Sprintf("%d (%s %s)", l.EntryID, l.Gross, l.Currency))
				continue
			}
			gross = amount
		}
		if gross.Sign() == 0 {
			continue
		}
		account, deductible, rule, err := b.expenseAccount(l)
		if err != nil {
			return expenseCost{}, err
		}
		text := b.text("Expense %d", "Utgift %d", l.EntryID)
		if l.CategoryName != "" {
			text += " · " + l.CategoryName
		}
		if rule != nil {
			text += b.text(" · rule %s v%d", " · regel %s v%d", rule.Name, rule.Version)
		}
		cost := Line{AccountID: account, Amount: gross, ProjectID: l.ProjectID, Text: text}
		if rule != nil {
			cost.PostingRuleID, cost.PostingRuleVersion = &rule.ID, &rule.Version
		}
		vat, hasVAT := parseAmount(l.VAT)
		withVAT := hasVAT && vat.Sign() > 0 && l.Kind == contracts.ExpenseKindOutlay && !foreign
		if withVAT && deductible && b.registered {
			if code := inferredCode(b.rates, gross, vat); code != nil {
				net := sub(gross, vat)
				cost.Amount, cost.TaxCodeID, cost.TaxRole = net, &code.ID, "base"
				lines = append(lines, cost, Line{AccountID: code.VatAccountID, Amount: vat, TaxCodeID: &code.ID, TaxRole: "tax",
					TaxBase: net, ProjectID: l.ProjectID, Text: text})
			} else {
				notDeducted = append(notDeducted, l.EntryID)
				lines = append(lines, cost)
			}
		} else {
			lines = append(lines, cost)
		}
		credit := Line{Role: roleEmployeePayables, Amount: neg(gross), Text: text}
		if !l.OwesEmployee() {
			credit.Role = roleBank
		}
		lines = append(lines, credit)
	}
	if len(waiting) > 0 {
		return expenseCost{}, &unpostable{reason: reasonAmountInNOKNeeded,
			detail: b.text("Enter the amount in NOK of expense %s, from the bank statement.",
				"Før inn beløpet i NOK for utgift %s, fra kontoutskriften.", strings.Join(waiting, ", "))}
	}
	if len(lines) == 0 {
		return expenseCost{}, nil
	}
	v := &Voucher{Kind: "expense", Date: f.Date, DocumentDate: f.Date, Source: expenseSource(f), Rule: expenseRuleApproved,
		RuleVersion: expenseRuleVersion, Lines: lines}
	v.Description = b.subjectText(f, "Expense %d approved", "Utgift %d godkjent", "Travel claim %d approved", "Reiseregning %d godkjent")
	return expenseCost{voucher: v, notDeducted: notDeducted}, nil
}

// subjectText is a description naming the entry or the claim.
func (b expenseBooks) subjectText(f contracts.ExpenseFact, entryEN, entryNB, claimEN, claimNB string) string {
	if f.Subject == contracts.SubjectClaim {
		return b.text(claimEN, claimNB, f.SubjectID)
	}
	return b.text(entryEN, entryNB, f.SubjectID)
}

// expenseAccount is the account an expense line is debited to and whether
// its VAT may be deducted (A14, R2): the expense rule of its category, which
// must name an expense account, or the default chart's 7700, which deducts
// nothing. Mileage and per diem have no category and go to 7700 too unless
// a rule names theirs.
func (b expenseBooks) expenseAccount(l contracts.ExpenseLine) (int32, bool, *store.ExpenseRulesRow, error) {
	if l.CategoryID != nil {
		if r, ok := b.rules[*l.CategoryID]; ok {
			if r.AccountKind != "expense" {
				return 0, false, nil, &unpostable{reason: codeAccountKindMismatch,
					detail: fmt.Sprintf("The posting rule %q points at account %s, which is not an expense account; point it at one under Posting rules.",
						r.Name, r.AccountNumber)}
			}
			return r.AccountID, r.VatDeductible, &r, nil
		}
	}
	if b.fallback == nil {
		return 0, false, nil, &unpostable{reason: reasonNoRule,
			detail: fmt.Sprintf("No posting rule matches the category of expense %d, and the chart has no account %s; add a rule under Posting rules.",
				l.EntryID, fallbackExpenseAccountNumber)}
	}
	return *b.fallback, false, nil, nil
}

// expenseReimbursementVoucher pays out what the standing approval credited
// employee_payables: from its stored voucher when the books have one, else
// — an approval before the books — from the fact's lines the employee is
// owed. nil when it comes to nothing.
func expenseReimbursementVoucher(ctx context.Context, q *store.Queries, b expenseBooks, f contracts.ExpenseFact, nok map[int64]*big.Rat) (*Voucher, error) {
	owed := new(big.Rat)
	cost, err := q.LatestExpenseCost(ctx, store.LatestExpenseCostParams{SourceKind: ptr(f.Subject + "." + contracts.ExpenseApproved),
		SourceID: &f.SubjectID})
	switch {
	case noRows(err):
		var waiting []string
		for _, l := range f.Lines {
			if !l.OwesEmployee() {
				continue
			}
			gross, ok := parseAmount(l.Gross)
			if !ok {
				return nil, fmt.Errorf("accounting: expense %d's gross %q", l.EntryID, l.Gross)
			}
			if l.Currency != currency {
				amount, ok := nok[l.EntryID]
				if !ok {
					waiting = append(waiting, fmt.Sprintf("%d (%s %s)", l.EntryID, l.Gross, l.Currency))
					continue
				}
				gross = amount
			}
			owed.Add(owed, gross)
		}
		if len(waiting) > 0 {
			return nil, &unpostable{reason: reasonAmountInNOKNeeded,
				detail: b.text("Enter the amount in NOK reimbursed for expense %s.", "Før inn beløpet i NOK som ble refundert for utgift %s.",
					strings.Join(waiting, ", "))}
		}
	case err != nil:
		return nil, err
	default:
		total, err := q.VoucherRoleTotal(ctx, store.VoucherRoleTotalParams{VoucherID: cost, Role: roleEmployeePayables})
		if err != nil {
			return nil, err
		}
		owed = neg(mustRat(total))
	}
	if owed.Sign() <= 0 {
		return nil, nil
	}
	text := b.subjectText(f, "Expense %d reimbursed", "Utgift %d refundert", "Travel claim %d reimbursed", "Reiseregning %d refundert")
	if f.ReimbursementReference != "" {
		text += " · " + f.ReimbursementReference
	}
	return &Voucher{Kind: "reimbursement", Date: f.Date, DocumentDate: f.Date, Description: truncate(text, 500),
		Source: expenseSource(f), Rule: expenseRuleReimbursed, RuleVersion: expenseRuleVersion,
		Lines: []Line{{Role: roleEmployeePayables, Amount: owed, Text: text}, {Role: roleBank, Amount: neg(owed), Text: text}}}, nil
}

// expenseUndoVoucher turns back the voucher the undone fact posted as (R15):
// nil when the books never posted it.
func expenseUndoVoucher(ctx context.Context, q *store.Queries, b expenseBooks, f contracts.ExpenseFact) (*Voucher, error) {
	undone := contracts.ExpenseApproved
	kind, rule := "expense", expenseRuleUnapproved
	description := b.subjectText(f, "Expense %d unapproved", "Utgift %d ikke lenger godkjent", "Travel claim %d unapproved",
		"Reiseregning %d ikke lenger godkjent")
	if f.Occurrence == contracts.ExpenseReimbursementUndone {
		undone, kind, rule = contracts.ExpenseReimbursed, "reimbursement", expenseRuleReimbursementUndone
		description = b.subjectText(f, "Reimbursement of expense %d undone", "Refusjon av utgift %d angret",
			"Reimbursement of travel claim %d undone", "Refusjon av reiseregning %d angret")
	}
	if f.UndoesSeq == 0 {
		return nil, nil
	}
	rows, err := q.SourcePostingsByKey(ctx, store.SourcePostingsByKeyParams{SourceModule: ptr(sourceExpenses),
		SourceKind: ptr(f.Subject + "." + undone), SourceID: &f.SubjectID, SourcePart: strconv.FormatInt(f.UndoesSeq, 10)})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	stored := &storedVoucher{id: rows[0].VoucherID}
	for _, p := range rows {
		stored.lines = append(stored.lines, postingLine(p.AccountID, p.Amount, p.TaxCodeID, p.TaxRole, p.TaxBase,
			p.TaxSpecification, p.CustomerID, p.SupplierID, p.OpenItemID, p.ProjectID, p.Description, p.PostingRuleID,
			p.PostingRuleVersion, p.Currency, p.CurrencyAmount, p.ExchangeRate))
	}
	v := &Voucher{Kind: kind, Date: f.Date, DocumentDate: f.Date, Description: truncate(description, 500), Source: expenseSource(f),
		Rule: rule, RuleVersion: expenseRuleVersion}
	turnBack(v, stored, b.text("Undone in Expenses", "Angret i Utgifter"))
	return v, nil
}

// undoneSource is the source key of the fact an undoing fact undoes.
func undoneSource(f contracts.ExpenseFact) *Source {
	undone := contracts.ExpenseApproved
	if f.Occurrence == contracts.ExpenseReimbursementUndone {
		undone = contracts.ExpenseReimbursed
	}
	return &Source{Module: sourceExpenses, Kind: f.Subject + "." + undone, ID: f.SubjectID,
		Part: strconv.FormatInt(f.UndoesSeq, 10)}
}

// postExpense posts one fact of Expenses' log on tx (A20, R2), as postSource
// does a sales fact: a voucher through the engine with its source key;
// nothing when it is before the books or comes to nothing; a row under Needs
// attention when it cannot be posted, or when its entry or claim has an
// earlier row there (waiting_on). An undoing of a fact that is itself still
// waiting under Needs attention resolves that row instead: what was never
// posted needs no reversal.
func postExpense(ctx context.Context, tx pgx.Tx, it sourceItem, now time.Time) (outcome, error) {
	q := store.New(tx)
	f := it.expense
	b, err := readExpenseBooks(ctx, q, f.Date)
	if err != nil {
		return outcomeSkipped, err
	}
	if f.Date.Before(b.booksStart) {
		return outcomeSkipped, nil
	}
	src := itemSource(it)
	already, posted, err := postedSource(ctx, q, src)
	if err != nil {
		return outcomeSkipped, err
	}
	if posted {
		return outcomePosted, q.MarkUnpostedPosted(ctx, store.MarkUnpostedPostedParams{VoucherID: &already.ID, Now: &now,
			SourceModule: src.Module, SourceKind: src.Kind, SourceID: src.ID, SourcePart: src.Part})
	}
	undoing := f.Occurrence == contracts.ExpenseUnapproved || f.Occurrence == contracts.ExpenseReimbursementUndone
	if undoing && f.UndoesSeq != 0 {
		u := undoneSource(*f)
		_, err := q.OpenUnpostedBySource(ctx, store.OpenUnpostedBySourceParams{SourceModule: u.Module, SourceKind: u.Kind,
			SourceID: u.ID, SourcePart: u.Part})
		switch {
		case noRows(err):
		case err != nil:
			return outcomeSkipped, err
		default:
			detail := b.text("Undone in Expenses before it was posted; nothing to post.",
				"Angret i Utgifter før det ble bokført; ingenting å bokføre.")
			if err := q.ResolveUnpostedBySource(ctx, store.ResolveUnpostedBySourceParams{Now: &now, Detail: detail,
				SourceModule: u.Module, SourceKind: u.Kind, SourceID: u.ID, SourcePart: u.Part}); err != nil {
				return outcomeSkipped, err
			}
			return outcomeSkipped, nil
		}
	}
	key := expenseOrderKey(*f)
	blocker, err := q.UnpostedBlockerByKey(ctx, store.UnpostedBlockerByKeyParams{OrderKey: &key, SourceModule: src.Module,
		SourceKind: src.Kind, SourceID: src.ID, SourcePart: src.Part})
	switch {
	case noRows(err):
	case err != nil:
		return outcomeSkipped, err
	default:
		detail := fmt.Sprintf("Waits for the %s of %s, which is under Needs attention: fix and retry that first.",
			blocker.SourceKind, key)
		return outcomeUnposted, recordUnposted(ctx, q, it, reasonWaitingOn, detail, &blocker.ID, now)
	}
	var v *Voucher
	var notDeducted []int64
	switch f.Occurrence {
	case contracts.ExpenseApproved:
		var cost expenseCost
		cost, err = expenseApprovalVoucher(b, *f, it.nok)
		v, notDeducted = cost.voucher, cost.notDeducted
	case contracts.ExpenseReimbursed:
		v, err = expenseReimbursementVoucher(ctx, q, b, *f, it.nok)
	case contracts.ExpenseUnapproved, contracts.ExpenseReimbursementUndone:
		v, err = expenseUndoVoucher(ctx, q, b, *f)
	default:
		err = &unpostable{reason: reasonUnknownKind,
			detail: fmt.Sprintf("Expenses' journal holds a fact of occurrence %q, which this version does not post.", f.Occurrence)}
	}
	var why *unpostable
	if errors.As(err, &why) {
		if err := recordUnposted(ctx, q, it, why.reason, why.detail, nil, now); err != nil {
			return outcomeSkipped, err
		}
		if why.reason == reasonAmountInNOKNeeded {
			if err := noteForeignLines(ctx, q, src, *f); err != nil {
				return outcomeSkipped, err
			}
		}
		return outcomeUnposted, nil
	}
	if err != nil {
		return outcomeSkipped, err
	}
	if v == nil {
		return outcomeSkipped, nil
	}
	v.At = now
	voucher, err := Post(ctx, tx, *v)
	var refused *refusal
	if errors.As(err, &refused) {
		return outcomeUnposted, recordUnposted(ctx, q, it, refusalReason(refused), refusalDetail(refused), nil, now)
	}
	if err != nil {
		return outcomeSkipped, err
	}
	if err := q.MarkUnpostedPosted(ctx, store.MarkUnpostedPostedParams{VoucherID: &voucher.ID, Now: &now,
		SourceModule: src.Module, SourceKind: src.Kind, SourceID: src.ID, SourcePart: src.Part}); err != nil {
		return outcomeSkipped, err
	}
	if len(notDeducted) > 0 && !voucher.Repeat {
		ids := make([]string, 0, len(notDeducted))
		for _, id := range notDeducted {
			ids = append(ids, strconv.FormatInt(id, 10))
		}
		detail := b.text("The VAT of expense %s was not deducted: it matches no input rate. Reverse and re-enter it if it should be.",
			"MVA-en på utgift %s ble ikke trukket fra: den passer ingen inngående sats. Tilbakefør og før på nytt om den skal det.",
			strings.Join(ids, ", "))
		p := store.RecordInformationalParams{Stream: it.stream, Position: it.position, SourceModule: src.Module,
			SourceKind: src.Kind, SourceID: src.ID, SourcePart: src.Part, FactDate: &f.Date, Reason: reasonVATNotDeducted,
			Detail: truncate(detail, 500), Now: now, VoucherID: &voucher.ID, OrderKey: &key}
		if total, ok := expenseNOKTotal(*f); ok {
			p.Amount = &total
		}
		if err := q.RecordInformational(ctx, p); err != nil {
			return outcomeSkipped, err
		}
	}
	return outcomePosted, nil
}

// noteForeignLines keeps, on the row of a fact waiting for its NOK amounts,
// the lines in another currency the person is asked about.
func noteForeignLines(ctx context.Context, q *store.Queries, src *Source, f contracts.ExpenseFact) error {
	notes := map[string]expenseLineNote{}
	for _, l := range f.Lines {
		if l.Currency == currency {
			continue
		}
		if f.Occurrence == contracts.ExpenseReimbursed && !l.OwesEmployee() {
			continue
		}
		notes[strconv.FormatInt(l.EntryID, 10)] = expenseLineNote{Kind: l.Kind, Currency: l.Currency, Gross: l.Gross}
	}
	data, err := json.Marshal(notes)
	if err != nil {
		return err
	}
	return q.InitUnpostedLines(ctx, store.InitUnpostedLinesParams{Lines: string(data), SourceModule: src.Module,
		SourceKind: src.Kind, SourceID: src.ID, SourcePart: src.Part})
}

// sortedEntryIDs is the notes' entries in order.
func sortedEntryIDs(notes map[string]expenseLineNote) []string {
	keys := make([]string, 0, len(notes))
	for k := range notes {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		x, _ := strconv.ParseInt(a, 10, 64)
		y, _ := strconv.ParseInt(b, 10, 64)
		return int(x - y)
	})
	return keys
}
