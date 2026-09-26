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

// creditTaxedLines are a credit note's lines taxed with their original lines'
// snapshots — never the rates in force today (D8).
func creditTaxedLines(lines []draftLine, credits []*int64, originals map[int64]store.InvoicesLine) ([]taxedLine, error) {
	out := make([]taxedLine, 0, len(lines))
	for i, l := range lines {
		var taxed taxedLine
		if credits[i] != nil {
			if o, ok := originals[*credits[i]]; ok {
				var err error
				if taxed, err = snapshotOf(o); err != nil {
					return nil, err
				}
			}
		}
		if taxed.rate == nil {
			taxed.rate = new(big.Rat)
		}
		taxed.net = l.amounts.net
		out = append(out, taxed)
	}
	return out, nil
}

// creditDraftTaxedLines taxes a credit-note draft's lines as its issue will:
// it reverses its original's treatment, so each line takes the original
// line's snapshot rate, never today's (D8). The draft's response and its
// preview both total it so.
func creditDraftTaxedLines(ctx context.Context, q *store.Queries, originalID int64, stored []store.InvoicesLine, lines []draftLine) ([]taxedLine, error) {
	originals, err := originalLines(ctx, q, originalID)
	if err != nil {
		return nil, err
	}
	credits := make([]*int64, 0, len(stored))
	for _, l := range stored {
		credits = append(credits, l.CreditsLineID)
	}
	return creditTaxedLines(lines, credits, originals)
}

// creditCaps is D8's two caps over a credit note's lines: per original line,
// the quantity and the net credited by the issued credit notes and this one
// may not pass the original's; and the headline, this one's gross may not pass
// what the original has left. It answers every cap the lines breach — the
// first line over its cap, then the headline — so a draft warns of both and
// the issue refuses with the first; none when both hold.
func creditCaps(ctx context.Context, q *store.Queries, original store.InvoicesInvoice, lines []store.InvoicesLine, gross *big.Rat) ([]capBreach, error) {
	originals, err := originalLines(ctx, q, original.ID)
	if err != nil {
		return nil, err
	}
	rows, err := q.CreditedPerLine(ctx, &original.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read what document %d's lines are credited: %w", original.ID, err)
	}
	type credited struct{ quantity, net *big.Rat }
	already := map[int64]credited{}
	for _, r := range rows {
		qty, err := ratFromNumeric(r.Quantity)
		if err != nil {
			return nil, err
		}
		net, err := ratFromNumeric(r.Net)
		if err != nil {
			return nil, err
		}
		already[r.LineID] = credited{qty, net}
	}
	var breaches []capBreach
	for _, l := range lines {
		if l.CreditsLineID == nil {
			continue
		}
		o, ok := originals[*l.CreditsLineID]
		if !ok {
			continue
		}
		qty, net, err := numericPair(l.Quantity, l.LineNet)
		if err != nil {
			return nil, err
		}
		maxQty, maxNet, err := numericPair(o.Quantity, o.LineNet)
		if err != nil {
			return nil, err
		}
		if a, ok := already[o.ID]; ok {
			qty.Add(qty, a.quantity)
			net.Add(net, a.net)
		}
		if qty.Cmp(maxQty) > 0 || net.Cmp(maxNet) > 0 {
			position := l.Position
			breaches = append(breaches, capBreach{codeCreditExceedsLine, &position, fmt.Sprintf(
				"Line %d credits more of the original's line %d than it had, counting the credit notes already issued.", l.Position, o.Position)})
			break
		}
	}
	_, left, err := uncredited(ctx, q, original)
	if err != nil {
		return nil, err
	}
	if gross.Cmp(left) > 0 {
		breaches = append(breaches, capBreach{codeCreditExceedsInvoice, nil, fmt.Sprintf(
			"This credit note is %s, and the invoice has %s left to credit.", gross.FloatString(2), left.FloatString(2))})
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
// original locked FOR UPDATE — the last lock of the issue's order — and both
// caps under it. Its lines are taxed with their original lines' snapshots.
func creditIssueChecks(ctx context.Context, txq *store.Queries, locked store.InvoicesInvoice, lines []store.InvoicesLine) (issuePlan, *gen.InvoicesConflictProblem, error) {
	original, err := txq.LockInvoice(ctx, *locked.CreditsInvoiceID)
	if err != nil {
		return issuePlan{}, nil, fmt.Errorf("invoices: lock the original %d: %w", *locked.CreditsInvoiceID, err)
	}
	originals, err := originalLines(ctx, txq, original.ID)
	if err != nil {
		return issuePlan{}, nil, err
	}
	plan := issuePlan{}
	taxed := make([]taxedLine, 0, len(lines))
	for _, l := range lines {
		o, ok := originals[derefID(l.CreditsLineID)]
		if !ok {
			return issuePlan{}, nil, fmt.Errorf("invoices: credit note %d's line %d credits no line of %d", locked.ID, l.ID, original.ID)
		}
		t, err := snapshotOf(o)
		if err != nil {
			return issuePlan{}, nil, err
		}
		if t.net, err = ratFromNumeric(l.LineNet); err != nil {
			return issuePlan{}, nil, err
		}
		plan.lines = append(plan.lines, issuedLine{id: l.ID, position: l.Position, taxed: t})
		taxed = append(taxed, t)
	}
	exchangeRate, err := ratFromNumeric(locked.ExchangeRate)
	if err != nil {
		return issuePlan{}, nil, err
	}
	_, totals, _ := summarize(taxed, exchangeRate)
	breaches, err := creditCaps(ctx, txq, original, lines, totals.gross)
	if err != nil {
		return issuePlan{}, nil, err
	}
	if len(breaches) > 0 {
		r := cannotIssue(breaches[0].code, breaches[0].detail)
		r.LinePosition = breaches[0].position
		return issuePlan{}, r, nil
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
	originals, err := originalLines(ctx, q, *current.CreditsInvoiceID)
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	credits := make([]*int64, 0, len(in.lines))
	for i, l := range in.lines {
		field := func(name string) string { return fmt.Sprintf("lines[%d].%s", i, name) }
		credits = append(credits, l.creditsLineID)
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
	taxed, err := creditTaxedLines(in.lines, credits, originals)
	if err != nil {
		return nil, err
	}
	exchangeRate, err := ratFromNumeric(current.ExchangeRate)
	if err != nil {
		return nil, err
	}
	_, totals, _ := summarize(taxed, exchangeRate)
	saved, refusal, err := s.saveDraft(ctx, current.ID, *body.Revision, in, totals)
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
// credit-note draft, the caps as warnings.
func creditLinks(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, resp *gen.InvoicesInvoiceResponse) error {
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
	original, err := q.GetInvoice(ctx, *inv.CreditsInvoiceID)
	if err != nil {
		return fmt.Errorf("invoices: read credit note %d's original: %w", inv.ID, err)
	}
	if original.Number != nil {
		resp.Credits = &gen.InvoicesCreditsRef{Id: original.ID, Number: *original.Number, IssueDate: wireDate(original.IssueDate.Time)}
	}
	if inv.Status != statusDraft {
		return nil
	}
	lines, err := q.Lines(ctx, inv.ID)
	if err != nil {
		return err
	}
	breaches, err := creditCaps(ctx, q, original, lines, ratFromFloat(resp.GrossTotal))
	if err != nil {
		return err
	}
	for _, b := range breaches {
		resp.Warnings = append(resp.Warnings, b.code)
	}
	return nil
}
