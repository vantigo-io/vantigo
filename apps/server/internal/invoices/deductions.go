package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the final settlement (invoices work design D7). An a-konto is
// an ordinary invoice; a settlement is an invoice with deduction lines, each
// deducting one earlier invoice of the same customer at one of its VAT codes:
// quantity exactly -1, the amount deducted as a positive unit price (BR-27),
// no discount, deducts_invoice_id frozen on the line. A deduction is taxed at
// its a-konto line's snapshot — category and rate — never at today's rate of
// the code, and is exempt from the checks that judge a code as it stands
// today. What it may take is capped per (a-konto, VAT code): the a-konto's
// lines' net at the code, less what its issued credit notes credited on those
// lines, less what issued settlements' deduction lines took and their issued
// credit notes did not give back. The cap is warned on the draft and refused
// at the issue, where it is read after the counter and the deducted invoices
// are never row-locked (reading 4): every write that changes what an a-konto
// has left is itself an issue, which the counter serialises, and a row lock
// would cycle with the customers merge's newest-first document lock.

// The codes of the settlement's and its credit note's refusals and warnings.
const (
	codeDeductionExceedsInvoice  = "deduction_exceeds_invoice"
	codeDeductionDuplicated      = "deduction_duplicated"
	codeInvoiceTotalNotPositive  = "invoice_total_not_positive"
	codeCreditTotalNegative      = "credit_total_negative"
	codeInvoiceDeducted          = "invoice_deducted"
	codeCreditNoteDeductsNothing = "credit_note_deducts_nothing"
)

// deductionKey is one (deducted invoice, VAT code): what one deduction line
// deducts, and what the cap is kept per.
type deductionKey struct {
	invoiceID int64
	vatCodeID int32
}

// deductible is what one issued invoice of a customer — an a-konto — has at
// one VAT code: its snapshot there, what it has left to deduct, and what
// issued settlements took of it, by number.
type deductible struct {
	invoiceID, number int64
	issueDate         time.Time
	vatCodeID         int32
	taxed             taxedLine
	left              *big.Rat
	taken             *big.Rat
	settlements       []int64
}

// deductedRef is an invoice a settlement deducts, as its PDF and its EHF
// reference it.
type deductedRef struct {
	number    int64
	issueDate time.Time
}

// deductionSnapshots is, per (issued invoice, VAT code) of ids, the VAT
// snapshot its lines at the code were issued with.
func deductionSnapshots(ctx context.Context, q *store.Queries, ids []int64) (map[deductionKey]taxedLine, error) {
	out := map[deductionKey]taxedLine{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.DeductionSnapshot(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the deducted invoices' VAT snapshots: %w", err)
	}
	for _, r := range rows {
		snap, err := snapshotOf(store.InvoicesLine{
			VatRatePercent: r.VatRatePercent, VatCategory: r.VatCategory, SafTCode: r.SafTCode, ExemptionReason: r.ExemptionReason,
		})
		if err != nil {
			return nil, err
		}
		out[deductionKey{r.InvoiceID, r.VatCodeID}] = snap
	}
	return out, nil
}

// deductibleLeft is, per (issued invoice of customerID, VAT code), what the
// invoice has left to deduct (D7), excludeID — the settlement itself — left
// out: the lines' net at the code, less its issued credit notes' credit of
// them, less what issued settlements took net of their issued credit notes.
// Every (invoice, code) is answered, nothing left included, the invoice
// number first. It locks nothing (reading 4).
func deductibleLeft(ctx context.Context, q *store.Queries, customerID int32, excludeID int64) ([]deductible, error) {
	nets, err := q.DeductibleNet(ctx, store.DeductibleNetParams{CustomerID: customerID, ExcludeID: excludeID})
	if err != nil {
		return nil, fmt.Errorf("invoices: read what customer %d's invoices have to deduct: %w", customerID, err)
	}
	takenRows, err := q.DeductedNet(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read what customer %d's invoices had deducted: %w", customerID, err)
	}
	type took struct {
		amount      *big.Rat
		settlements []int64
	}
	taken := make(map[deductionKey]took, len(takenRows))
	for _, r := range takenRows {
		amount, err := ratFromNumeric(r.Taken)
		if err != nil {
			return nil, err
		}
		taken[deductionKey{r.InvoiceID, r.VatCodeID}] = took{amount, r.Settlements}
	}
	ids := make([]int64, 0, len(nets))
	for _, n := range nets {
		if !slices.Contains(ids, n.InvoiceID) {
			ids = append(ids, n.InvoiceID)
		}
	}
	snaps, err := deductionSnapshots(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	out := make([]deductible, 0, len(nets))
	for _, n := range nets {
		key := deductionKey{n.InvoiceID, n.VatCodeID}
		snap, ok := snaps[key]
		if !ok {
			return nil, fmt.Errorf("invoices: issued invoice %d has no VAT snapshot at code %d", n.InvoiceID, n.VatCodeID)
		}
		net, err := ratFromNumeric(n.Net)
		if err != nil {
			return nil, err
		}
		d := deductible{
			invoiceID: n.InvoiceID, number: n.Number, issueDate: n.IssueDate.Time, vatCodeID: n.VatCodeID,
			taxed: snap, left: net, taken: new(big.Rat),
		}
		if t, ok := taken[key]; ok {
			d.taken, d.settlements = t.amount, t.settlements
			d.left = new(big.Rat).Sub(net, t.amount)
		}
		out = append(out, d)
	}
	return out, nil
}

// checkDeductions is the cap over a settlement's lines (D7): each deduction
// line may take at most what its (a-konto, VAT code) has left. It answers
// every line past its cap, by position, so a draft warns and the issue
// refuses with the first.
func checkDeductions(lines []draftLine, left []deductible) []capBreach {
	byKey := make(map[deductionKey]*big.Rat, len(left))
	for _, d := range left {
		byKey[deductionKey{d.invoiceID, d.vatCodeID}] = d.left
	}
	var breaches []capBreach
	for i, l := range lines {
		if l.deductsInvoiceID == nil || l.amounts.net == nil {
			continue
		}
		have, ok := byKey[deductionKey{*l.deductsInvoiceID, l.vatCodeID}]
		if !ok {
			have = new(big.Rat)
		}
		takes := new(big.Rat).Neg(l.amounts.net)
		if takes.Cmp(have) > 0 {
			position := int32(i + 1)
			breaches = append(breaches, capBreach{codeDeductionExceedsInvoice, &position, fmt.Sprintf(
				"Line %d deducts %s, and the invoice it deducts has %s left at its VAT code.", position, takes.FloatString(2), have.FloatString(2))})
		}
	}
	return breaches
}

// isSettlement reports whether lines deduct anything.
func isSettlement(lines []draftLine) bool {
	return slices.ContainsFunc(lines, func(l draftLine) bool { return l.deductsInvoiceID != nil })
}

// deductedIDs is the distinct invoices lines deduct, in their order.
func deductedIDs(lines []draftLine) []int64 {
	var ids []int64
	for _, l := range lines {
		if l.deductsInvoiceID != nil && !slices.Contains(ids, *l.deductsInvoiceID) {
			ids = append(ids, *l.deductsInvoiceID)
		}
	}
	return ids
}

// withDeductionSnapshots gives each deduction line of lines its a-konto
// line's snapshot, which taxedLines taxes it at.
func withDeductionSnapshots(ctx context.Context, q *store.Queries, lines []draftLine) error {
	snaps, err := deductionSnapshots(ctx, q, deductedIDs(lines))
	if err != nil {
		return err
	}
	for i, l := range lines {
		if l.deductsInvoiceID == nil {
			continue
		}
		if snap, ok := snaps[deductionKey{*l.deductsInvoiceID, l.vatCodeID}]; ok {
			lines[i].snapshot = &snap
		}
	}
	return nil
}

// msgNotDeductible is the 400 on a deducted document that is not an issued
// invoice of the draft's customer.
const msgNotDeductible = "Only an issued invoice of the same customer, never this document, a draft or a credit note, is deducted"

// deductionRules are the save's rules for an invoice draft's lines (D7),
// added to errs: a negative quantity only on a deduction line; a deduction's
// quantity exactly -1, its price above 0, no discount and no sources; the
// invoice it deducts an issued invoice of customerID, never selfID (0 on a
// create); its VAT code one that invoice has a line at, whose snapshot the
// line is then taxed at. With the fields sound, one deduction line per
// (invoice, VAT code): a second is the 409 deduction_duplicated, naming it.
func deductionRules(ctx context.Context, q *store.Queries, customerID int32, selfID int64, lines []draftLine,
	errs map[string][]string,
) (map[string][]string, *gen.InvoicesConflictProblem, error) {
	minusOne := big.NewRat(-1, 1)
	for i, l := range lines {
		field := func(name string) string { return fmt.Sprintf("lines[%d].%s", i, name) }
		if l.deductsInvoiceID == nil {
			if l.quantity != nil && l.quantity.Sign() < 0 {
				errs = withFieldError(errs, field("quantity"), "A quantity is greater than 0; only a deduction line (deductsInvoiceId) is negative")
			}
			continue
		}
		if l.quantity != nil && l.quantity.Cmp(minusOne) != 0 {
			errs = withFieldError(errs, field("quantity"), "A deduction line's quantity is -1")
		}
		if l.unitPrice != nil && l.unitPrice.Sign() <= 0 {
			errs = withFieldError(errs, field("unitPrice"), "A deduction line's unit price is the amount deducted, greater than 0")
		}
		if l.discount != nil && l.discount.Sign() != 0 {
			errs = withFieldError(errs, field("discountPercent"), "A deduction line carries no discount")
		}
		if len(l.sources) > 0 {
			errs = withFieldError(errs, field("sources"), "A deduction line bills no work")
		}
	}
	ids := deductedIDs(lines)
	if len(ids) == 0 {
		return errs, nil, nil
	}
	docs, err := q.DeductedDocuments(ctx, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("invoices: read the deducted documents: %w", err)
	}
	valid := map[int64]bool{}
	for _, d := range docs {
		valid[d.ID] = d.Kind == kindInvoice && d.Status == statusIssued && d.CustomerID == customerID && d.ID != selfID
	}
	snaps, err := deductionSnapshots(ctx, q, ids)
	if err != nil {
		return nil, nil, err
	}
	for i, l := range lines {
		if l.deductsInvoiceID == nil {
			continue
		}
		if !valid[*l.deductsInvoiceID] {
			errs = withFieldError(errs, fmt.Sprintf("lines[%d].deductsInvoiceId", i), msgNotDeductible)
			continue
		}
		snap, ok := snaps[deductionKey{*l.deductsInvoiceID, l.vatCodeID}]
		if !ok {
			errs = withFieldError(errs, fmt.Sprintf("lines[%d].vatCodeId", i), "The deducted invoice has no line at this VAT code")
			continue
		}
		lines[i].snapshot = &snap
	}
	if len(errs) > 0 {
		return errs, nil, nil
	}
	seen := map[deductionKey]bool{}
	for i, l := range lines {
		if l.deductsInvoiceID == nil {
			continue
		}
		key := deductionKey{*l.deductsInvoiceID, l.vatCodeID}
		if seen[key] {
			position := int32(i + 1)
			c := conflict(codeDeductionDuplicated, "The invoice is deducted twice", fmt.Sprintf(
				"Line %d deducts the same invoice at the same VAT code as an earlier line; one deduction line per invoice and VAT code.", position))
			c.LinePosition = &position
			return errs, &c, nil
		}
		seen[key] = true
	}
	return errs, nil, nil
}

// settlementWarnings are an invoice draft's settlement warnings (D7): a
// deduction past its cap, and a gross that is not positive — both refused at
// the issue. None for a draft that deducts nothing.
func settlementWarnings(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, lines []draftLine, gross *big.Rat) ([]string, error) {
	if !isSettlement(lines) {
		return nil, nil
	}
	var out []string
	left, err := deductibleLeft(ctx, q, inv.CustomerID, inv.ID)
	if err != nil {
		return nil, err
	}
	if len(checkDeductions(lines, left)) > 0 {
		out = append(out, codeDeductionExceedsInvoice)
	}
	if gross.Sign() <= 0 {
		out = append(out, codeInvoiceTotalNotPositive)
	}
	return out, nil
}

// issueDeductions are a settlement's deductions as its issue decides them,
// read after the counter and never locked (reading 4): each deducted document
// still an issued invoice of this customer, each deduction taxed at its
// a-konto line's snapshot, and the cap. It answers the snapshot per deduction
// line's id, or the refusal: deduction_exceeds_invoice with the line — past
// its cap, or deducting a document that is no longer one of this customer's
// issued invoices, which has nothing left to deduct for it.
func issueDeductions(ctx context.Context, txq *store.Queries, locked store.InvoicesInvoice, lines []store.InvoicesLine,
) (map[int64]taxedLine, *gen.InvoicesConflictProblem, error) {
	drafts, err := storedDraftLines(lines)
	if err != nil {
		return nil, nil, err
	}
	ids := deductedIDs(drafts)
	if len(ids) == 0 {
		return nil, nil, nil
	}
	refuse := func(position int32, detail string) *gen.InvoicesConflictProblem {
		r := cannotIssue(codeDeductionExceedsInvoice, detail)
		r.LinePosition = &position
		return r
	}
	docs, err := txq.DeductedDocuments(ctx, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("invoices: read the deducted documents: %w", err)
	}
	valid := map[int64]bool{}
	for _, d := range docs {
		valid[d.ID] = d.Kind == kindInvoice && d.Status == statusIssued && d.CustomerID == locked.CustomerID && d.ID != locked.ID
	}
	snaps, err := deductionSnapshots(ctx, txq, ids)
	if err != nil {
		return nil, nil, err
	}
	out := map[int64]taxedLine{}
	for i, l := range lines {
		d := drafts[i]
		if d.deductsInvoiceID == nil {
			continue
		}
		snap, ok := snaps[deductionKey{*d.deductsInvoiceID, d.vatCodeID}]
		if !valid[*d.deductsInvoiceID] || !ok {
			return nil, refuse(l.Position, fmt.Sprintf(
				"Line %d deducts document %d, which is no longer an issued invoice of this customer with a line at its VAT code.",
				l.Position, *d.deductsInvoiceID)), nil
		}
		out[l.ID] = snap
	}
	left, err := deductibleLeft(ctx, txq, locked.CustomerID, locked.ID)
	if err != nil {
		return nil, nil, err
	}
	if breaches := checkDeductions(drafts, left); len(breaches) > 0 {
		return nil, refuse(*breaches[0].position, breaches[0].detail), nil
	}
	return out, nil, nil
}

// deductedOf is what a credit note's issue judges per VAT code of its
// original (D7): at a code an issued settlement deducted, the credit may take
// no more than the original has left there; past it is invoice_deducted,
// naming the settlements — credit the settlement first. At a code no
// settlement deducted the line caps alone decide.
func (b creditBook) deductedOf(ctx context.Context, q *store.Queries, lines []creditedLine, t creditTotals) (*gen.InvoicesConflictProblem, error) {
	all, err := deductibleLeft(ctx, q, b.original.CustomerID, 0)
	if err != nil {
		return nil, err
	}
	at := map[int32]deductible{}
	for _, d := range all {
		if d.invoiceID == b.original.ID && d.taken.Sign() > 0 {
			at[d.vatCodeID] = d
		}
	}
	if len(at) == 0 {
		return nil, nil
	}
	credit := map[int32]*big.Rat{}
	var codes []int32
	for i, l := range lines {
		if l.creditsLineID == nil {
			continue
		}
		o, ok := b.lines[*l.creditsLineID]
		if !ok {
			continue
		}
		if _, ok := credit[o.VatCodeID]; !ok {
			credit[o.VatCodeID] = new(big.Rat)
			codes = append(codes, o.VatCodeID)
		}
		credit[o.VatCodeID].Add(credit[o.VatCodeID], t.amounts[i].net)
	}
	slices.Sort(codes)
	for _, code := range codes {
		d, deducted := at[code]
		if !deducted || credit[code].Cmp(d.left) <= 0 {
			continue
		}
		numbers := make([]string, 0, len(d.settlements))
		for _, n := range d.settlements {
			numbers = append(numbers, fmt.Sprint(n))
		}
		return cannotIssue(codeInvoiceDeducted, fmt.Sprintf(
			"This credit note credits %s at a VAT code where the invoice has %s left: settlement %s deducted the rest. Credit the settlement first.",
			credit[code].FloatString(2), d.left.FloatString(2), strings.Join(numbers, ", "))), nil
	}
	return nil, nil
}

// deductedRefsOf is the invoices an invoice's deduction lines deduct, by
// number (D7): what its PDF lists under the references and its EHF as BG-3.
// None for a credit note, whose one preceding invoice is its original.
func deductedRefsOf(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, lines []store.InvoicesLine) ([]deductedRef, error) {
	if inv.Kind != kindInvoice {
		return nil, nil
	}
	var ids []int64
	for _, l := range lines {
		if l.DeductsInvoiceID != nil && !slices.Contains(ids, *l.DeductsInvoiceID) {
			ids = append(ids, *l.DeductsInvoiceID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	docs, err := q.DeductedDocuments(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's deducted invoices: %w", inv.ID, err)
	}
	out := make([]deductedRef, 0, len(docs))
	for _, d := range docs {
		if d.Number == nil {
			return nil, fmt.Errorf("invoices: document %d deducts %d, which has no number", inv.ID, d.ID)
		}
		out = append(out, deductedRef{number: *d.Number, issueDate: d.IssueDate.Time})
	}
	return out, nil
}

// GetInvoicesByIdDeductible What earlier invoices have left to deduct
// (GET /api/v1/invoices/{id}/deductible)
//
// For an invoice draft, what each issued invoice of its customer has left to
// deduct per VAT code (D7, plan reading 31) — the editor's "Deduct earlier
// invoices" step. Read from the documents' own rows; no directory, no lock.
func (s *server) GetInvoicesByIdDeductible(ctx context.Context, req gen.GetInvoicesByIdDeductibleRequestObject) (gen.GetInvoicesByIdDeductibleResponseObject, error) {
	q := store.New(s.deps.Pool)
	inv, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetInvoicesByIdDeductible404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	switch {
	case inv.Status != statusDraft:
		return gen.GetInvoicesByIdDeductible409ApplicationProblemPlusJSONResponse(invoiceIssued()), nil
	case inv.Kind != kindInvoice:
		return gen.GetInvoicesByIdDeductible409ApplicationProblemPlusJSONResponse(conflict(codeCreditNoteDeductsNothing,
			"A credit note deducts nothing", "A credit note reverses its original's lines, deductions included; it deducts no other invoice.")), nil
	}
	left, err := deductibleLeft(ctx, q, inv.CustomerID, inv.ID)
	if err != nil {
		return nil, err
	}
	out := gen.GetInvoicesByIdDeductible200JSONResponse{}
	for _, d := range left {
		if d.left.Sign() <= 0 {
			continue
		}
		out = append(out, gen.InvoicesDeductible{
			InvoiceId: d.invoiceID, Number: d.number, IssueDate: wireDate(d.issueDate), VatCodeId: d.vatCodeID,
			Category: d.taxed.category, RatePercent: floatFromRat(d.taxed.rate, 2), Left: floatFromRat(d.left, 2),
		})
	}
	return out, nil
}
