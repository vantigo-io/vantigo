package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the credit note (D8, § 5-2-7): the correction the law requires,
// in the same series as the invoice it reverses. A credit-note draft is a copy
// of an issued invoice; it may only take lines away and lower amounts, and at
// its issue the caps — per original line, and on the headline — are decided
// under the original's lock. A credit note skips every gate meant for new
// invoices, because an invoice to a customer since archived, anonymised or
// blocked must still be correctable, and it re-looks up no VAT rate: it
// reverses the original's treatment, rate for rate.

// The codes and titles of the credit note's refusals and warnings.
const (
	codeCreditNoteNotCreditable = "credit_note_not_creditable"
	codeInvoiceFullyCredited    = "invoice_fully_credited"
	codeCreditExceedsLine       = "credit_exceeds_line"
	codeCreditExceedsInvoice    = "credit_exceeds_invoice"
	cannotCreditTitle           = "The invoice cannot be credited"
)

// creditedGross is what an invoice's issued credit notes credit, gross.
func creditedGross(ctx context.Context, q *store.Queries, originalID int64) (*big.Rat, error) {
	n, err := q.CreditedGross(ctx, &originalID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read what document %d is credited: %w", originalID, err)
	}
	return ratFromNumeric(n)
}

// uncredited is an issued invoice's gross less what its issued credit notes
// credit.
func uncredited(ctx context.Context, q *store.Queries, original store.InvoicesInvoice) (credited, left *big.Rat, err error) {
	credited, err = creditedGross(ctx, q, original.ID)
	if err != nil {
		return nil, nil, err
	}
	gross, err := ratFromNumeric(original.GrossTotal)
	if err != nil {
		return nil, nil, err
	}
	return credited, new(big.Rat).Sub(gross, credited), nil
}

// PostInvoicesByIdCredit Create a credit-note draft
// (POST /api/v1/invoices/{id}/credit)
func (s *server) PostInvoicesByIdCredit(ctx context.Context, req gen.PostInvoicesByIdCreditRequestObject) (gen.PostInvoicesByIdCreditResponseObject, error) {
	q := store.New(s.deps.Pool)
	original, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesByIdCredit404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	switch {
	case original.Status != statusIssued:
		return gen.PostInvoicesByIdCredit409ApplicationProblemPlusJSONResponse(conflict(codeInvoiceDraft, cannotCreditTitle,
			"A draft is not a sales document: change or delete it instead.")), nil
	case original.Kind == kindCreditNote:
		return gen.PostInvoicesByIdCredit409ApplicationProblemPlusJSONResponse(conflict(codeCreditNoteNotCreditable, cannotCreditTitle,
			"A credit note is never itself credited.")), nil
	}
	_, left, err := uncredited(ctx, q, original)
	if err != nil {
		return nil, err
	}
	if left.Sign() <= 0 {
		return gen.PostInvoicesByIdCredit409ApplicationProblemPlusJSONResponse(conflict(codeInvoiceFullyCredited, cannotCreditTitle,
			"This invoice is already credited in full.")), nil
	}
	var created store.InvoicesInvoice
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		created, err = txq.InsertCreditDraft(ctx, store.InsertCreditDraftParams{
			OriginalID: original.ID, CreatedByUserID: callerID(ctx), Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("invoices: create a credit note for %d: %w", original.ID, err)
		}
		return txq.CopyLinesToCredit(ctx, store.CopyLinesToCreditParams{CreditID: created.ID, OriginalID: original.ID})
	})
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, q, created, nil)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdCredit201JSONResponse(resp), nil
}

// originalLines are an invoice's lines by id.
func originalLines(ctx context.Context, q *store.Queries, originalID int64) (map[int64]store.InvoicesLine, error) {
	lines, err := q.Lines(ctx, originalID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's lines: %w", originalID, err)
	}
	out := make(map[int64]store.InvoicesLine, len(lines))
	for _, l := range lines {
		out[l.ID] = l
	}
	return out, nil
}

// snapshotOf is an issued line's VAT treatment, as a credit of it carries it.
func snapshotOf(l store.InvoicesLine) (taxedLine, error) {
	rate, err := ratFromNumeric(l.VatRatePercent)
	if err != nil {
		return taxedLine{}, err
	}
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	return taxedLine{category: deref(l.VatCategory), rate: rate, safT: deref(l.SafTCode), reason: l.ExemptionReason}, nil
}

// creditBook is what an invoice has had credited, read once per request and,
// at issue, under the original's lock: the original, its lines by id, and per
// line what its issued credit notes credit.
type creditBook struct {
	original store.InvoicesInvoice
	lines    map[int64]store.InvoicesLine
	credited map[int64]lineCredit
}

// lineCredit is how much of one original line the issued credit notes
// credit, and whether every one of them was a return at the line's own price
// and discount.
type lineCredit struct {
	quantity, net, gross, allowance *big.Rat
	returns                         bool
}

// readCreditBook reads an invoice's credit book.
func readCreditBook(ctx context.Context, q *store.Queries, original store.InvoicesInvoice) (creditBook, error) {
	lines, err := originalLines(ctx, q, original.ID)
	if err != nil {
		return creditBook{}, err
	}
	rows, err := q.CreditedPerLine(ctx, &original.ID)
	if err != nil {
		return creditBook{}, fmt.Errorf("invoices: read what document %d's lines are credited: %w", original.ID, err)
	}
	credited := make(map[int64]lineCredit, len(rows))
	for _, r := range rows {
		c := lineCredit{returns: r.Returns}
		if c.quantity, c.net, err = numericPair(r.Quantity, r.Net); err != nil {
			return creditBook{}, err
		}
		if c.gross, c.allowance, err = numericPair(r.Gross, r.Allowance); err != nil {
			return creditBook{}, err
		}
		credited[r.LineID] = c
	}
	return creditBook{original: original, lines: lines, credited: credited}, nil
}

// bookOf reads the credit book of the invoice a credit note credits.
func bookOf(ctx context.Context, q *store.Queries, credit store.InvoicesInvoice) (creditBook, error) {
	original, err := q.GetInvoice(ctx, *credit.CreditsInvoiceID)
	if err != nil {
		return creditBook{}, fmt.Errorf("invoices: read credit note %d's original: %w", credit.ID, err)
	}
	return readCreditBook(ctx, q, original)
}

// creditedLine is one line of a credit note as the reversal rule reads it.
type creditedLine struct {
	position                      int32
	creditsLineID                 *int64
	quantity, unitPrice, discount *big.Rat
	amounts                       lineAmounts
}

// creditedLines are a draft's parsed lines as the reversal rule reads them.
func creditedLines(lines []draftLine) []creditedLine {
	out := make([]creditedLine, 0, len(lines))
	for i, l := range lines {
		out = append(out, creditedLine{
			position: int32(i + 1), creditsLineID: l.creditsLineID,
			quantity: l.quantity, unitPrice: l.unitPrice, discount: l.discount, amounts: l.amounts,
		})
	}
	return out
}

// storedCreditedLines are a credit note's stored lines as the reversal rule
// reads them.
func storedCreditedLines(lines []store.InvoicesLine) ([]creditedLine, error) {
	out := make([]creditedLine, 0, len(lines))
	for _, l := range lines {
		c := creditedLine{position: l.Position, creditsLineID: l.CreditsLineID}
		var err error
		if c.quantity, c.unitPrice, err = numericPair(l.Quantity, l.UnitPrice); err != nil {
			return nil, err
		}
		if c.discount, err = ratFromNumeric(l.DiscountPercent); err != nil {
			return nil, err
		}
		if c.amounts.gross, c.amounts.allowance, err = numericPair(l.LineGross, l.LineAllowance); err != nil {
			return nil, err
		}
		if c.amounts.net, err = ratFromNumeric(l.LineNet); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// finalReversal reports whether a credit note is the invoice's last: with the
// issued credit notes before it, it credits every line's whole quantity —
// exactly, never past it — and every credit of every line, theirs and its
// own, is a return at the line's own price and discount. Only then is what the parts miss of the whole the
// øre their roundings lost. A price reduction ("prisavslag") is never part of
// a final reversal: its credit was a choice, not a rounding, and squaring
// after it would credit the reduction again.
func (b creditBook) finalReversal(lines []creditedLine) (bool, error) {
	if len(b.lines) == 0 {
		return false, nil
	}
	quantity := map[int64]*big.Rat{}
	for _, l := range lines {
		if l.creditsLineID == nil || l.quantity == nil || l.unitPrice == nil || l.discount == nil {
			return false, nil
		}
		o, ok := b.lines[*l.creditsLineID]
		if !ok {
			return false, nil
		}
		price, err := ratFromNumeric(o.UnitPrice)
		if err != nil {
			return false, err
		}
		discount, err := ratFromNumeric(o.DiscountPercent)
		if err != nil {
			return false, err
		}
		if l.unitPrice.Cmp(price) != 0 || l.discount.Cmp(discount) != 0 {
			return false, nil
		}
		if quantity[o.ID] == nil {
			quantity[o.ID] = new(big.Rat)
		}
		quantity[o.ID].Add(quantity[o.ID], l.quantity)
	}
	for id, o := range b.lines {
		total := new(big.Rat)
		if q, ok := quantity[id]; ok {
			total.Add(total, q)
		}
		if c, ok := b.credited[id]; ok {
			if !c.returns {
				return false, nil
			}
			total.Add(total, c.quantity)
		}
		want, err := ratFromNumeric(o.Quantity)
		if err != nil {
			return false, err
		}
		if total.Cmp(want) != 0 {
			return false, nil // short of it, or past it, which the line cap refuses
		}
	}
	return true, nil
}

// remaining is what an original line has left to credit: its gross,
// allowance and net less what the issued credit notes credited.
func (b creditBook) remaining(o store.InvoicesLine) (lineAmounts, error) {
	gross, allowance, err := numericPair(o.LineGross, o.LineAllowance)
	if err != nil {
		return lineAmounts{}, err
	}
	if c, ok := b.credited[o.ID]; ok {
		gross.Sub(gross, c.gross)
		allowance.Sub(allowance, c.allowance)
	}
	return lineAmounts{gross: gross, allowance: allowance, net: new(big.Rat).Sub(gross, allowance)}, nil
}

// creditTotals is a credit note as it will be issued: each line's amounts,
// taxed at its original line's snapshot, the VAT rows and the totals.
type creditTotals struct {
	amounts   []lineAmounts
	taxed     []taxedLine
	rows      []vatSummary
	totals    documentTotals
	ambiguous bool
	final     bool
}

// total is a credit note's amounts and VAT (D5, D8). A credit note rounds as
// any document does: each line on its own quantity and price, the VAT per rate
// on the sum of its lines' nets. The final reversal (finalReversal) is the
// exception, because each partial credit was rounded on its own and those
// roundings need not add up to the original's — 3 × 31.66 is not 94.99. So
// the last credit note takes the rest: each of its lines the gross, allowance
// and net its original line has left, and each row of the original the
// taxable amount and the VAT (NOK too) the original charged less what the
// issued credit notes reversed. A row every line of which was credited before
// still carries what is left of it, on no line of this note. The credits then
// sum to exactly what was charged, øre for øre. The response, the preview, the
// save and the issue all total a credit note here; the issue under the
// original's lock.
func (b creditBook) total(ctx context.Context, q *store.Queries, lines []creditedLine, exchangeRate *big.Rat) (creditTotals, error) {
	final, err := b.finalReversal(lines)
	if err != nil {
		return creditTotals{}, err
	}
	out := creditTotals{final: final}
	for _, l := range lines {
		amounts := l.amounts
		var taxed taxedLine
		if l.creditsLineID != nil {
			if o, ok := b.lines[*l.creditsLineID]; ok {
				if taxed, err = snapshotOf(o); err != nil {
					return creditTotals{}, err
				}
				if final {
					if amounts, err = b.remaining(o); err != nil {
						return creditTotals{}, err
					}
				}
			}
		}
		if taxed.rate == nil {
			taxed.rate = new(big.Rat)
		}
		taxed.net = amounts.net
		out.amounts = append(out.amounts, amounts)
		out.taxed = append(out.taxed, taxed)
	}
	out.rows, out.totals, out.ambiguous = summarize(out.taxed, exchangeRate)
	if !final {
		return out, nil
	}
	if out.rows, out.totals, err = b.remainingRows(ctx, q, out.rows); err != nil {
		return creditTotals{}, err
	}
	return out, nil
}

// remainingRows are a final credit note's VAT rows: per row of the original,
// what it charged less what the issued credit notes reversed. own, the rows
// the note's own lines make, is kept for a row the original lacks, which
// cannot happen: every line carries its original line's treatment.
func (b creditBook) remainingRows(ctx context.Context, q *store.Queries, own []vatSummary) ([]vatSummary, documentTotals, error) {
	charged, err := q.VatSummaries(ctx, b.original.ID)
	if err != nil {
		return nil, documentTotals{}, fmt.Errorf("invoices: read document %d's VAT: %w", b.original.ID, err)
	}
	reversed, err := q.CreditedVatPerRate(ctx, &b.original.ID)
	if err != nil {
		return nil, documentTotals{}, fmt.Errorf("invoices: read what document %d's VAT is credited: %w", b.original.ID, err)
	}
	type key struct{ category, rate string }
	back := map[key]store.CreditedVatPerRateRow{}
	for _, r := range reversed {
		rate, err := ratFromNumeric(r.RatePercent)
		if err != nil {
			return nil, documentTotals{}, err
		}
		back[key{r.VatCategory, rate.FloatString(2)}] = r
	}
	var rows []vatSummary
	seen := map[key]bool{}
	for _, c := range charged {
		row := vatSummary{category: c.VatCategory, safT: c.SafTCode, reason: c.ExemptionReason}
		if row.rate, err = ratFromNumeric(c.RatePercent); err != nil {
			return nil, documentTotals{}, err
		}
		if row.taxable, err = ratFromNumeric(c.TaxableAmount); err != nil {
			return nil, documentTotals{}, err
		}
		if row.vat, row.vatNOK, err = numericPair(c.VatAmount, c.VatAmountNok); err != nil {
			return nil, documentTotals{}, err
		}
		k := key{row.category, row.rate.FloatString(2)}
		seen[k] = true
		if r, ok := back[k]; ok {
			taxable, err := ratFromNumeric(r.Taxable)
			if err != nil {
				return nil, documentTotals{}, err
			}
			vat, vatNOK, err := numericPair(r.Vat, r.VatNok)
			if err != nil {
				return nil, documentTotals{}, err
			}
			row.taxable.Sub(row.taxable, taxable)
			row.vat.Sub(row.vat, vat)
			row.vatNOK.Sub(row.vatNOK, vatNOK)
		}
		if row.taxable.Sign() == 0 && row.vat.Sign() == 0 && row.vatNOK.Sign() == 0 && !rowOf(own, row.category, k.rate) {
			continue // a row the credits before squared already
		}
		rows = append(rows, row)
	}
	for _, r := range own {
		if !seen[key{r.category, r.rate.FloatString(2)}] {
			rows = append(rows, r)
		}
	}
	totals := documentTotals{net: new(big.Rat), vat: new(big.Rat), gross: new(big.Rat), vatNOK: new(big.Rat)}
	for _, r := range rows {
		totals.net.Add(totals.net, r.taxable)
		totals.vat.Add(totals.vat, r.vat)
		totals.vatNOK.Add(totals.vatNOK, r.vatNOK)
	}
	totals.gross.Add(totals.net, totals.vat)
	sortSummaries(rows)
	return rows, totals, nil
}

// rowOf reports whether rows has a row for (category, rate).
func rowOf(rows []vatSummary, category, rate string) bool {
	for _, r := range rows {
		if r.category == category && r.rate.FloatString(2) == rate {
			return true
		}
	}
	return false
}

// creditDraft is a credit-note draft totalled as its issue will total it,
// with its caps as warnings (creditBook.caps): what its response and its preview
// show.
type creditDraft struct {
	book     creditBook
	totals   creditTotals
	breaches []capBreach
}

// readCreditDraft totals a stored credit-note draft, reading its original's
// credit book once.
func readCreditDraft(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, stored []store.InvoicesLine) (creditDraft, error) {
	book, err := bookOf(ctx, q, inv)
	if err != nil {
		return creditDraft{}, err
	}
	lines, err := storedCreditedLines(stored)
	if err != nil {
		return creditDraft{}, err
	}
	exchangeRate, err := ratFromNumeric(inv.ExchangeRate)
	if err != nil {
		return creditDraft{}, err
	}
	totals, err := book.total(ctx, q, lines, exchangeRate)
	if err != nil {
		return creditDraft{}, err
	}
	breaches, err := book.caps(ctx, q, lines, totals)
	if err != nil {
		return creditDraft{}, err
	}
	return creditDraft{book: book, totals: totals, breaches: breaches}, nil
}

// caps is D8's two caps over a credit note's lines, as total amounts them:
// per original line, the quantity and the net credited by the issued credit
// notes and this one may not pass the original's; and the headline, this
// one's gross may not pass what the original has left. It answers the first
// line over its cap and the headline, so a draft warns of both and the issue
// refuses with the first; none when both hold.
func (b creditBook) caps(ctx context.Context, q *store.Queries, lines []creditedLine, t creditTotals) ([]capBreach, error) {
	var breaches []capBreach
	for i, l := range lines {
		if l.creditsLineID == nil {
			continue
		}
		o, ok := b.lines[*l.creditsLineID]
		if !ok {
			continue
		}
		maxQty, maxNet, err := numericPair(o.Quantity, o.LineNet)
		if err != nil {
			return nil, err
		}
		qty, net := new(big.Rat).Set(l.quantity), new(big.Rat).Set(t.amounts[i].net)
		if a, ok := b.credited[o.ID]; ok {
			qty.Add(qty, a.quantity)
			net.Add(net, a.net)
		}
		if qty.Cmp(maxQty) > 0 || net.Cmp(maxNet) > 0 {
			position := l.position
			breaches = append(breaches, capBreach{codeCreditExceedsLine, &position, fmt.Sprintf(
				"Line %d credits more of the original's line %d than it had, counting the credit notes already issued.", l.position, o.Position)})
			break
		}
	}
	_, left, err := uncredited(ctx, q, b.original)
	if err != nil {
		return nil, err
	}
	if t.totals.gross.Cmp(left) > 0 {
		breaches = append(breaches, capBreach{codeCreditExceedsInvoice, nil, fmt.Sprintf(
			"This credit note is %s, and the invoice has %s left to credit.", t.totals.gross.FloatString(2), left.FloatString(2))})
	}
	return breaches, nil
}

// capBreach is one cap a credit note's lines breach: the refusal code, the
// credit note's line position a line cap names, and the message.
type capBreach struct {
	code     string
	position *int32
	detail   string
}

// numericPair reads two numeric columns.
func numericPair(a, b pgtype.Numeric) (*big.Rat, *big.Rat, error) {
	x, err := ratFromNumeric(a)
	if err != nil {
		return nil, nil, err
	}
	y, err := ratFromNumeric(b)
	if err != nil {
		return nil, nil, err
	}
	return x, y, nil
}

// creditIssueChecks are the checks only a credit note keeps (D6 step 5): the
// original locked FOR UPDATE — the last lock of the issue's order — then its
// credit book read under that lock, after the counter, so every credit note
// issued before this one is seen; the credit note totalled by total, whose
// amounts and VAT the issue writes as they are; and both caps.
func creditIssueChecks(ctx context.Context, txq *store.Queries, locked store.InvoicesInvoice, lines []store.InvoicesLine) (issuePlan, *gen.InvoicesConflictProblem, error) {
	original, err := txq.LockInvoice(ctx, *locked.CreditsInvoiceID)
	if err != nil {
		return issuePlan{}, nil, fmt.Errorf("invoices: lock the original %d: %w", *locked.CreditsInvoiceID, err)
	}
	book, err := readCreditBook(ctx, txq, original)
	if err != nil {
		return issuePlan{}, nil, err
	}
	for _, l := range lines {
		if _, ok := book.lines[derefID(l.CreditsLineID)]; !ok {
			return issuePlan{}, nil, fmt.Errorf("invoices: credit note %d's line %d credits no line of %d", locked.ID, l.ID, original.ID)
		}
	}
	credited, err := storedCreditedLines(lines)
	if err != nil {
		return issuePlan{}, nil, err
	}
	exchangeRate, err := ratFromNumeric(locked.ExchangeRate)
	if err != nil {
		return issuePlan{}, nil, err
	}
	t, err := book.total(ctx, txq, credited, exchangeRate)
	if err != nil {
		return issuePlan{}, nil, err
	}
	breaches, err := book.caps(ctx, txq, credited, t)
	if err != nil {
		return issuePlan{}, nil, err
	}
	if len(breaches) > 0 {
		r := cannotIssue(breaches[0].code, breaches[0].detail)
		r.LinePosition = breaches[0].position
		return issuePlan{}, r, nil
	}
	plan := issuePlan{summary: &planSummary{rows: t.rows, totals: t.totals, ambiguous: t.ambiguous}}
	for i, l := range lines {
		issued := issuedLine{id: l.ID, position: l.Position, taxed: t.taxed[i]}
		if t.final {
			issued.amounts = &t.amounts[i]
		}
		plan.lines = append(plan.lines, issued)
	}
	return plan, nil, nil
}

func derefID(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

// putCreditDraft is PUT /invoices/{id} for a credit-note draft (D8): only what
// a correction may change — lines removed, a quantity or a unit price
// lowered, a description, the note and the internal note edited. Everything
// else it copied from its original stays, and each attempt to change it is a
// 400 on the field. The caps only warn here; the issue decides them.
func (s *server) putCreditDraft(ctx context.Context, q *store.Queries, current store.InvoicesInvoice, body gen.InvoicesInvoiceRequest) (gen.PutInvoicesByIdResponseObject, error) {
	in, errs := parseDraft(body, current.Currency)
	add := func(field, msg string) { errs = withFieldError(errs, field, msg) }
	if in.customerID != current.CustomerID {
		add("customerId", "A credit note's customer is its original's")
	}
	if in.paymentTermsDays != nil {
		add("paymentTermsDays", "A credit note has no payment terms")
	}
	if !sameDate(in.deliveryDate, current.DeliveryDate) || !sameDate(in.deliveryFrom, current.DeliveryFrom) || !sameDate(in.deliveryTo, current.DeliveryTo) {
		add("deliveryDate", "A credit note keeps its original's delivery")
	}
	line1, line2, postal, city, country := addressColumns(in.address)
	if !sameText(line1, current.DeliveryAddressLine1) || !sameText(line2, current.DeliveryAddressLine2) ||
		!sameText(postal, current.DeliveryPostalCode) || !sameText(city, current.DeliveryCity) || !sameText(country, current.DeliveryCountry) {
		add("deliveryAddress", "A credit note keeps its original's place of delivery")
	}
	yourReference := ""
	if in.yourReference != nil {
		yourReference = *in.yourReference
	}
	for _, ref := range []struct{ field, got, want string }{
		{"yourReference", yourReference, current.YourReference},
		{"ourReference", in.ourReference, current.OurReference},
		{"orderReference", in.orderReference, current.OrderReference},
	} {
		if ref.got != ref.want {
			add(ref.field, "A credit note keeps its original's references")
		}
	}
	book, err := bookOf(ctx, q, current)
	if err != nil {
		return nil, err
	}
	originals := book.lines
	seen := map[int64]bool{}
	for i, l := range in.lines {
		field := func(name string) string { return fmt.Sprintf("lines[%d].%s", i, name) }
		if l.creditsLineID == nil {
			add(field("creditsLineId"), "A credit note only credits the original's lines; it adds none")
			continue
		}
		o, ok := originals[*l.creditsLineID]
		switch {
		case !ok:
			add(field("creditsLineId"), "This is not a line of the original")
			continue
		case seen[o.ID]:
			add(field("creditsLineId"), "An original line is credited once per credit note")
			continue
		}
		seen[o.ID] = true
		if l.vatCodeID != o.VatCodeID {
			add(field("vatCodeId"), "A credit note keeps the original line's VAT code")
		}
		if l.unit != o.Unit {
			add(field("unit"), "A credit note keeps the original line's unit")
		}
		if l.quantity == nil || l.unitPrice == nil || l.discount == nil {
			continue
		}
		oQty, oPrice, err := numericPair(o.Quantity, o.UnitPrice)
		if err != nil {
			return nil, err
		}
		oDiscount, err := ratFromNumeric(o.DiscountPercent)
		if err != nil {
			return nil, err
		}
		if l.quantity.Cmp(oQty) > 0 {
			add(field("quantity"), "A credit note may lower a quantity, never raise it")
		}
		if l.unitPrice.Cmp(oPrice) > 0 {
			add(field("unitPrice"), "A credit note may lower a price, never raise it")
		}
		if l.discount.Cmp(oDiscount) < 0 {
			add(field("discountPercent"), "A credit note may not lower a discount")
		}
	}
	if len(errs) > 0 {
		return gen.PutInvoicesById400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle, errs)), nil
	}
	exchangeRate, err := ratFromNumeric(current.ExchangeRate)
	if err != nil {
		return nil, err
	}
	t, err := book.total(ctx, q, creditedLines(in.lines), exchangeRate)
	if err != nil {
		return nil, err
	}
	// The lines are stored as total amounts them: a final note's with what
	// their original lines have left. A credit note issued in between may
	// change that; the issue totals it again under the original's lock.
	for i := range in.lines {
		in.lines[i].amounts = t.amounts[i]
	}
	saved, refusal, err := s.saveDraft(ctx, current.ID, *body.Revision, in, t.totals)
	if err != nil {
		return nil, err
	}
	if refusal != nil {
		return gen.PutInvoicesById409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	resp, err := s.invoiceResponse(ctx, q, saved, nil)
	if err != nil {
		return nil, err
	}
	return gen.PutInvoicesById200JSONResponse(resp), nil
}

func sameDate(a, b pgtype.Date) bool {
	return a.Valid == b.Valid && (!a.Valid || a.Time.Equal(b.Time))
}

func sameText(a, b *string) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

// creditLinks are a document's credit links (D4): on an issued invoice, what
// its issued credit notes credit, what it has left, and every credit note,
// drafts included; on a credit note, the invoice it credits. On a
// credit-note draft, the caps as warnings: draft is that draft as
// readCreditDraft totalled it, nil for any other document.
func creditLinks(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, resp *gen.InvoicesInvoiceResponse, draft *creditDraft) error {
	if inv.Kind == kindInvoice {
		if inv.Status != statusIssued {
			return nil
		}
		credited, left, err := uncredited(ctx, q, inv)
		if err != nil {
			return err
		}
		resp.CreditedAmount, resp.UncreditedAmount = ptr(floatFromRat(credited, 2)), ptr(floatFromRat(left, 2))
		notes, err := q.CreditNotesOf(ctx, &inv.ID)
		if err != nil {
			return fmt.Errorf("invoices: read document %d's credit notes: %w", inv.ID, err)
		}
		refs := make([]gen.InvoicesCreditNoteRef, 0, len(notes))
		for _, n := range notes {
			gross, err := floatFromNumeric(n.GrossTotal)
			if err != nil {
				return err
			}
			refs = append(refs, gen.InvoicesCreditNoteRef{
				Id: n.ID, Number: n.Number, IssueDate: wireDateOf(n.IssueDate), GrossTotal: gross, Status: n.Status,
			})
		}
		resp.CreditNotes = &refs
		return nil
	}
	var original store.InvoicesInvoice
	if draft != nil {
		original = draft.book.original
		for _, b := range draft.breaches {
			resp.Warnings = append(resp.Warnings, b.code)
		}
	} else {
		var err error
		if original, err = q.GetInvoice(ctx, *inv.CreditsInvoiceID); err != nil {
			return fmt.Errorf("invoices: read credit note %d's original: %w", inv.ID, err)
		}
	}
	if original.Number != nil {
		resp.Credits = &gen.InvoicesCreditsRef{Id: original.ID, Number: *original.Number, IssueDate: wireDate(original.IssueDate.Time)}
	}
	return nil
}
