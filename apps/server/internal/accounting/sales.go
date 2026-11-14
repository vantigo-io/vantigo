package accounting

// What Invoices' sales journal posts as (A13 as amended by R3, R4, R5, R9,
// R14): the voucher each issued document and each fact of the log becomes.
// Every rule here is named and versioned on the voucher it makes (the
// system's own rules, salesRule*), and each revenue line names the posting
// rule (A14) that chose its account; the reference page documents them all,
// which is the description of system-generated entries bokføringsloven § 6
// asks for. A change to a rule here is a new version.
//
// The builders are pure: they take what the books hold (salesBooks, read in
// the fact's own transaction) and the fact, and answer a Voucher for Post, nil
// when there is nothing to post, or an *unpostable naming why the fact cannot
// be posted — which goes under Needs attention while the cursor moves on
// (A15).

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/docmath"
)

// The source module of every sales voucher, and its source kinds: a
// document's kind, or a fact's.
const sourceInvoices = "invoices"

// The system's sales rules and their versions (A14): what each kind of fact
// posts as. The reference page documents each.
const (
	salesRuleInvoice          = "sales.invoice"
	salesRuleCreditNote       = "sales.credit_note"
	salesRulePayment          = "sales.payment"
	salesRulePaymentRemoved   = "sales.payment_removed"
	salesRuleCharge           = "sales.charge_payment"
	salesRuleChargeRemoved    = "sales.charge_payment_removed"
	salesRuleWriteOff         = "sales.write_off"
	salesRuleWriteOffVAT      = "sales.write_off_vat"
	salesRuleWriteOffRounding = "sales.write_off_rounding"
	salesRuleWriteOffUndone   = "sales.write_off_undone"
	salesRuleRecovered        = "sales.recovered"
	salesRuleRecoveredRemoved = "sales.recovered_removed"

	salesRuleVersion int32 = 1
)

// The reasons a fact waits under Needs attention, besides an engine
// refusal's own code.
const (
	reasonTaxCodeUnknown  = "tax_code_unknown"
	reasonUnknownKind     = "unknown_kind"
	reasonDocumentUnknown = "document_unknown"
	reasonNoRule          = "posting_rule_missing"
)

// The specification a loss's VAT taken back is reported on (A23, R4).
const specificationLoss = "tapPåKrav"

// The roles the sales vouchers post to besides the control accounts.
const (
	roleBank          = "bank"
	roleClearingVipps = "clearing_vipps"
	roleChargeIncome  = "charge_income"
	roleBadDebt       = "bad_debt"
	roleRounding      = "rounding"
)

// chargeTaxCode is the code a charge payment posts with (R9, R14): no VAT
// treatment, left out of the return.
const chargeTaxCode = "7"

// unpostable is a fact that cannot be posted, and why.
type unpostable struct {
	reason, detail string
}

func (u *unpostable) Error() string { return "accounting: unpostable: " + u.reason + ": " + u.detail }

// salesBooks is what the books hold that a sales voucher is built against.
type salesBooks struct {
	language   string
	booksStart time.Time
	// openingSeq is settings.opening_as_of_seq (R14).
	openingSeq *int64
	taxCodes   map[string]store.TaxCodesByCodeRow
	rules      []store.SalesRulesRow
}

// readSalesBooks reads them on q, the fact's own transaction.
func readSalesBooks(ctx context.Context, q *store.Queries) (salesBooks, error) {
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return salesBooks{}, err
	}
	codes, err := q.TaxCodesByCode(ctx)
	if err != nil {
		return salesBooks{}, err
	}
	rules, err := q.SalesRules(ctx)
	if err != nil {
		return salesBooks{}, err
	}
	b := salesBooks{language: settings.ChartLanguage, booksStart: settings.BooksStart, openingSeq: settings.OpeningAsOfSeq,
		taxCodes: map[string]store.TaxCodesByCodeRow{}, rules: rules}
	for _, c := range codes {
		b.taxCodes[c.Code] = c
	}
	return b, nil
}

// text is the description in the chart's language.
func (b salesBooks) text(en, nb string, args ...any) string {
	if b.language == "nb" {
		return fmt.Sprintf(nb, args...)
	}
	return fmt.Sprintf(en, args...)
}

// taxCode is the active tax code with that standard code.
func (b salesBooks) taxCode(code string) (store.TaxCodesByCodeRow, error) {
	tc, ok := b.taxCodes[code]
	if !ok || !tc.Active {
		return store.TaxCodesByCodeRow{}, &unpostable{reason: reasonTaxCodeUnknown,
			detail: fmt.Sprintf("The standard tax code %q is not an active tax code in the books.", code)}
	}
	return tc, nil
}

// revenueRule is the posting rule a sales line's net is credited by (A14):
// the first that matches of its line kind, its product's tax category and its
// standard tax code, then the default rule of its tax code.
func (b salesBooks) revenueRule(l contracts.JournalLine) (store.SalesRulesRow, error) {
	for _, match := range []string{"line_kind", "tax_category", "tax_code", "default"} {
		for _, r := range b.rules {
			if r.Match != match {
				continue
			}
			switch match {
			case "line_kind":
				if l.Kind != "" && deref(r.LineKind) == l.Kind {
					return r, nil
				}
			case "tax_category":
				if l.TaxCategoryID != nil && r.TaxCategoryID != nil && *r.TaxCategoryID == *l.TaxCategoryID {
					return r, nil
				}
			default:
				if deref(r.TaxCode) == l.SAFT {
					return r, nil
				}
			}
		}
	}
	return store.SalesRulesRow{}, &unpostable{reason: reasonNoRule,
		detail: fmt.Sprintf("No posting rule matches line %d (tax code %q); add one under Posting rules.", l.Position, l.SAFT)}
}

// amountOf is a contract amount as an exact number.
func amountOf(text string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, fmt.Errorf("accounting: %q is not an amount", text)
	}
	return r, nil
}

// documentSource is a document's source key.
func documentSource(d contracts.JournalDocument) *Source {
	return &Source{Module: sourceInvoices, Kind: d.Kind, ID: d.ID}
}

// factSource is a fact's: its kind and the row it is about, which one fact of
// each kind names (Invoices' ux_journal_facts_subject).
func factSource(f contracts.JournalFact) *Source {
	return &Source{Module: sourceInvoices, Kind: f.Kind, ID: f.SubjectID}
}

// documentVoucher is the voucher of an issued invoice or credit note (A13,
// R3): receivables debited for the summaries' bases and VAT on the invoice's
// item (a credit note on the item of the invoice it credits), each line's net
// credited to its rule's account with its tax code, each summary's VAT to its
// code's VAT account. A difference between the lines' nets and the summaries'
// bases goes on the largest revenue line, so the voucher follows the
// document's own totals; a credit note turns every sign. nil when the
// document is dated before the books start, or has nothing but zeros.
func documentVoucher(b salesBooks, d contracts.JournalDocument) (*Voucher, error) {
	if d.IssueDate.Before(b.booksStart) {
		return nil, nil
	}
	sign := big.NewRat(1, 1)
	kind, rule := "sales_invoice", salesRuleInvoice
	description := b.text("Invoice %d", "Faktura %d", d.Number)
	item := d.ID
	if d.Kind == contracts.JournalCreditNote {
		sign = big.NewRat(-1, 1)
		kind, rule = "sales_credit_note", salesRuleCreditNote
		description = b.text("Credit note %d for invoice %d", "Kreditnota %d til faktura %d", d.Number, d.CreditsNumber)
		if d.CreditsID != 0 {
			item = d.CreditsID
		}
	}
	signed := func(r *big.Rat) *big.Rat { return new(big.Rat).Mul(r, sign) }

	var revenue []Line
	nets := new(big.Rat)
	largest := -1
	for _, l := range d.Lines {
		net, err := amountOf(l.NetNOK)
		if err != nil {
			return nil, err
		}
		if net.Sign() == 0 {
			continue
		}
		tc, err := b.taxCode(l.SAFT)
		if err != nil {
			return nil, err
		}
		r, err := b.revenueRule(l)
		if err != nil {
			return nil, err
		}
		nets.Add(nets, net)
		revenue = append(revenue, Line{AccountID: r.AccountID, Amount: neg(signed(net)), TaxCodeID: &tc.ID, TaxRole: "base",
			ProjectID: l.ProjectID, Text: b.text("Line %d · rule %s v%d", "Linje %d · regel %s v%d", l.Position, r.Name, r.Version)})
		if largest < 0 || abs(net).Cmp(abs(neg(revenue[largest].Amount))) > 0 {
			largest = len(revenue) - 1
		}
	}
	bases, vats := new(big.Rat), new(big.Rat)
	var taxes []Line
	for _, v := range d.VATSummaries {
		base, err := amountOf(v.BaseNOK)
		if err != nil {
			return nil, err
		}
		vat, err := amountOf(v.VATNOK)
		if err != nil {
			return nil, err
		}
		bases.Add(bases, base)
		vats.Add(vats, vat)
		if vat.Sign() == 0 {
			continue
		}
		tc, err := b.taxCode(v.SAFT)
		if err != nil {
			return nil, err
		}
		if tc.VatAccountID == nil {
			return nil, &unpostable{reason: reasonTaxCodeUnknown,
				detail: fmt.Sprintf("The tax code %s has no VAT account, and the document carries VAT on it.", v.SAFT)}
		}
		taxes = append(taxes, Line{AccountID: *tc.VatAccountID, Amount: neg(signed(vat)), TaxCodeID: &tc.ID, TaxRole: "tax",
			TaxBase: neg(signed(base)), Text: b.text("VAT %s %%", "MVA %s %%", v.RatePercent)})
	}
	if diff := sub(bases, nets); diff.Sign() != 0 && largest >= 0 {
		revenue[largest].Amount = sub(revenue[largest].Amount, signed(diff))
	}
	total := add(bases, vats)
	if total.Sign() == 0 && len(revenue) == 0 && len(taxes) == 0 {
		return nil, nil
	}
	lines := []Line{{Role: roleReceivables, Amount: signed(total), CustomerID: &d.CustomerID, SourceDocumentID: &item,
		Item: &OpenItemSpec{DocumentNumber: fmt.Sprint(d.Number), DocumentDate: &d.IssueDate, DueDate: d.DueDate}}}
	if d.Kind == contracts.JournalCreditNote {
		// The credit note lands on its invoice's item; that item, when the
		// credit note makes it, is the invoice's.
		lines[0].Item = &OpenItemSpec{DocumentNumber: fmt.Sprint(d.CreditsNumber)}
	}
	lines = append(lines, revenue...)
	lines = append(lines, taxes...)
	return &Voucher{Kind: kind, Date: d.IssueDate, DocumentDate: d.IssueDate, Description: description,
		Source: documentSource(d), SourceDocumentID: &item, Rule: rule, RuleVersion: salesRuleVersion, Lines: lines}, nil
}

// factInBooks is whether a fact belongs in the books (R5, R14): dated on or
// after books_start, or after the opening's snapshot — the invoice's opening
// item's AsOfSeq when it has one, else the settings' — and so not already in
// the opening amount. A fact dated before books_start with no opening seq is
// the old books'.
func factInBooks(ctx context.Context, q *store.Queries, b salesBooks, f contracts.JournalFact) (bool, error) {
	if !f.Date.Before(b.booksStart) {
		return true, nil
	}
	threshold := b.openingSeq
	item, err := q.OpenItemOpeningSeq(ctx, &f.InvoiceID)
	switch {
	case noRows(err):
	case err != nil:
		return false, err
	case item.OpeningAsOfSeq != nil:
		threshold = item.OpeningAsOfSeq
	}
	return threshold != nil && f.Seq > *threshold, nil
}

// factVoucher is the voucher of a fact of the log (A13, R4, R5, R9, R14). doc
// is the invoice the fact is about, read for a write-off's VAT correction and
// nil otherwise. A removal is its registration's voucher with every sign
// turned, dated the removal's day; a date before the books start or in a
// locked period is the engine's to move (A8, R5).
func factVoucher(b salesBooks, f contracts.JournalFact, doc *contracts.JournalDocument) (*Voucher, error) {
	amount, err := amountOf(f.AmountNOK)
	if err != nil {
		return nil, err
	}
	v := &Voucher{Date: f.Date, DocumentDate: f.Date, Source: factSource(f), SourceDocumentID: &f.InvoiceID,
		RuleVersion: salesRuleVersion}
	receivables := func(a *big.Rat) Line {
		return Line{Role: roleReceivables, Amount: a, CustomerID: &f.CustomerID, SourceDocumentID: &f.InvoiceID,
			Item: &OpenItemSpec{DocumentNumber: fmt.Sprint(f.InvoiceNumber)}}
	}
	bank := roleBank
	if f.Source == contracts.PaymentSourceVipps {
		bank = roleClearingVipps
	}
	switch f.Kind {
	case contracts.FactPayment, contracts.FactPaymentRemoved:
		v.Kind, v.Rule = "customer_payment", salesRulePayment
		v.Description = b.text("Payment of invoice %d", "Innbetaling på faktura %d", f.InvoiceNumber)
		v.Lines = []Line{{Role: bank, Amount: amount}, receivables(neg(amount))}
		if f.Kind == contracts.FactPaymentRemoved {
			v.Rule = salesRulePaymentRemoved
			v.Description = b.text("Payment of invoice %d removed", "Innbetaling på faktura %d fjernet", f.InvoiceNumber)
			turn(v.Lines)
		}
	case contracts.FactChargePayment, contracts.FactChargePaymentRemoved:
		tc, err := b.taxCode(chargeTaxCode)
		if err != nil {
			return nil, err
		}
		v.Kind, v.Rule = "charge", salesRuleCharge
		v.Description = b.text("Reminder charges paid, invoice %d", "Purrekostnader betalt, faktura %d", f.InvoiceNumber)
		v.Lines = []Line{{Role: bank, Amount: amount}, {Role: roleChargeIncome, Amount: neg(amount), TaxCodeID: &tc.ID, TaxRole: "base"}}
		if f.Kind == contracts.FactChargePaymentRemoved {
			v.Rule = salesRuleChargeRemoved
			v.Description = b.text("Reminder charges payment removed, invoice %d", "Betaling av purrekostnader fjernet, faktura %d", f.InvoiceNumber)
			turn(v.Lines)
		}
	case contracts.FactWriteOff, contracts.FactWriteOffUndone:
		if f.WriteOff == nil {
			return nil, &unpostable{reason: reasonDocumentUnknown, detail: "The write-off's fact names no write-off."}
		}
		loss, rule, err := writeOffLines(b, f.WriteOff, amount, doc)
		if err != nil {
			return nil, err
		}
		v.Kind, v.Rule = "write_off", rule
		v.Description = b.text("Write-off of invoice %d", "Avskrivning av faktura %d", f.InvoiceNumber)
		v.Lines = append([]Line{receivables(neg(amount))}, loss...)
		if f.Kind == contracts.FactWriteOffUndone {
			v.Rule = salesRuleWriteOffUndone
			v.Description = b.text("Write-off of invoice %d undone", "Avskrivning av faktura %d angret", f.InvoiceNumber)
			turn(v.Lines)
		}
	case contracts.FactRecoveredPayment, contracts.FactRecoveredPaymentRemoved:
		if f.WriteOff == nil {
			return nil, &unpostable{reason: reasonDocumentUnknown, detail: "The recovered payment names no write-off."}
		}
		written, err := amountOf(f.WriteOff.AmountNOK)
		if err != nil {
			return nil, err
		}
		loss, _, err := writeOffLines(b, f.WriteOff, written, doc)
		if err != nil {
			return nil, err
		}
		v.Kind, v.Rule = "customer_payment", salesRuleRecovered
		v.Description = b.text("Recovered on written-off invoice %d", "Innbetalt på avskrevet faktura %d", f.InvoiceNumber)
		v.Lines = append([]Line{{Role: bank, Amount: amount}}, scaleTurned(loss, amount, written)...)
		if f.Kind == contracts.FactRecoveredPaymentRemoved {
			v.Rule = salesRuleRecoveredRemoved
			v.Description = b.text("Recovery on invoice %d removed", "Innbetaling på avskrevet faktura %d fjernet", f.InvoiceNumber)
			turn(v.Lines)
		}
	default:
		return nil, &unpostable{reason: reasonUnknownKind,
			detail: fmt.Sprintf("Invoices' journal holds a fact of kind %q, which this version does not post.", f.Kind)}
	}
	return v, nil
}

// turn turns every line's sign, and a tax line's base with it.
func turn(lines []Line) {
	for i := range lines {
		lines[i].Amount = neg(lines[i].Amount)
		if lines[i].TaxBase != nil {
			lines[i].TaxBase = neg(lines[i].TaxBase)
		}
	}
}

// writeOffLines is the debit side of a write-off of amount (R4), and the
// rule it follows: a rounding to the rounding account; a loss or other
// without the correction to bad debt, whole; with it, amount shared over the
// invoice's VAT summaries in proportion to their gross, each share's VAT
// debited to its code's VAT account and the rest to bad debt, the VAT and its
// base marked for the return's tapPåKrav. The shares are rounded to øre and
// the last summary takes what rounding left, so they sum to amount.
func writeOffLines(b salesBooks, w *contracts.JournalWriteOff, amount *big.Rat, doc *contracts.JournalDocument) ([]Line, string, error) {
	switch {
	case w.Reason == contracts.WriteOffRounding:
		return []Line{{Role: roleRounding, Amount: new(big.Rat).Set(amount)}}, salesRuleWriteOffRounding, nil
	case !w.VATCorrection:
		return []Line{{Role: roleBadDebt, Amount: new(big.Rat).Set(amount)}}, salesRuleWriteOff, nil
	case doc == nil:
		return nil, "", &unpostable{reason: reasonDocumentUnknown,
			detail: "The written-off invoice is not in Invoices' journal, so its VAT cannot be shared."}
	}
	type summary struct {
		code       store.TaxCodesByCodeRow
		gross, vat *big.Rat
	}
	var rows []summary
	total := new(big.Rat)
	for _, s := range doc.VATSummaries {
		base, err := amountOf(s.BaseNOK)
		if err != nil {
			return nil, "", err
		}
		vat, err := amountOf(s.VATNOK)
		if err != nil {
			return nil, "", err
		}
		gross := add(base, vat)
		if gross.Sign() == 0 {
			continue
		}
		tc, err := b.taxCode(s.SAFT)
		if err != nil {
			return nil, "", err
		}
		if vat.Sign() != 0 && tc.VatAccountID == nil {
			return nil, "", &unpostable{reason: reasonTaxCodeUnknown,
				detail: fmt.Sprintf("The tax code %s has no VAT account to take the VAT back on.", s.SAFT)}
		}
		rows = append(rows, summary{code: tc, gross: gross, vat: vat})
		total.Add(total, gross)
	}
	if total.Sign() == 0 {
		return []Line{{Role: roleBadDebt, Amount: new(big.Rat).Set(amount)}}, salesRuleWriteOffVAT, nil
	}
	var out []Line
	left := new(big.Rat).Set(amount)
	for i, r := range rows {
		share := left
		if i < len(rows)-1 {
			share = docmath.Round2(new(big.Rat).Quo(new(big.Rat).Mul(amount, r.gross), total))
		}
		left = sub(left, share)
		vat := docmath.Round2(new(big.Rat).Quo(new(big.Rat).Mul(share, r.vat), r.gross))
		base := sub(share, vat)
		if vat.Sign() == 0 {
			out = append(out, Line{Role: roleBadDebt, Amount: base})
			continue
		}
		out = append(out,
			Line{Role: roleBadDebt, Amount: base, TaxCodeID: &r.code.ID, TaxRole: "base", TaxSpecification: specificationLoss},
			Line{AccountID: *r.code.VatAccountID, Amount: vat, TaxCodeID: &r.code.ID, TaxRole: "tax", TaxBase: base,
				TaxSpecification: specificationLoss})
	}
	return out, salesRuleWriteOffVAT, nil
}

// scaleTurned is a write-off's debit lines scaled to a recovery (R4): each
// line times recovered / written, rounded to øre, the last line taking what
// rounding left so they sum to recovered, every sign turned — the recovery
// credits bad debt and gives the VAT back in the write-off's proportion.
func scaleTurned(lines []Line, recovered, written *big.Rat) []Line {
	out := make([]Line, 0, len(lines))
	left := new(big.Rat).Set(recovered)
	for i, l := range lines {
		part := left
		if i < len(lines)-1 {
			part = docmath.Round2(new(big.Rat).Quo(new(big.Rat).Mul(l.Amount, recovered), written))
		}
		left = sub(left, part)
		l.Amount = neg(part)
		if l.TaxBase != nil {
			l.TaxBase = neg(docmath.Round2(new(big.Rat).Quo(new(big.Rat).Mul(l.TaxBase, recovered), written)))
		}
		out = append(out, l)
	}
	return out
}
