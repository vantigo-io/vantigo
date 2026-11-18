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
// the fact's own transaction), the fact and what it is built from (factInputs:
// the invoice and its credit notes from the journal, the vouchers already
// posted that it turns back or scales), and answer a Voucher for Post, nil
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
	// The customer's credit (invoices phase 6 design I16): an overpayment
	// kept, a credit note's surplus moved off its invoice, credit applied to
	// an invoice, credit refunded, a movement reversed, and a balance a
	// customer merge moved. Each posts on the customer's credit item.
	salesRuleCreditOverpayment = "sales.credit_overpayment"
	salesRuleCreditNoteMoved   = "sales.credit_note_moved"
	salesRuleCreditApplied     = "sales.credit_applied"
	salesRuleCreditRefunded    = "sales.credit_refunded"
	salesRuleCreditReversed    = "sales.credit_reversed"
	salesRuleCreditMerged      = "sales.credit_merged"
	// salesRuleExchangeDifference posts the NOK rest a closing credit note
	// left on an invoice in another currency (invoices phase 6, review M1).
	salesRuleExchangeDifference = "sales.exchange_difference"

	salesRuleVersion int32 = 1
	// salesRulePaymentVersion is the version of the payments' rules —
	// sales.payment, sales.charge_payment, sales.recovered and their
	// removals: version 2 posts the bank side to the ledger account of the
	// bank account the payment was received on (A26), version 1 always to
	// the bank role's.
	salesRulePaymentVersion int32 = 2
)

// The reasons a fact waits under Needs attention, besides an engine
// refusal's own code.
const (
	reasonTaxCodeUnknown  = "tax_code_unknown"
	reasonUnknownKind     = "unknown_kind"
	reasonDocumentUnknown = "document_unknown"
	reasonNoRule          = "posting_rule_missing"
	// reasonWaitingOn is a document or fact of an invoice another row under
	// Needs attention is about (R15): it waits behind that row, and posts
	// after it.
	reasonWaitingOn = "waiting_on"
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
	// bankLedgers is each active bank account's ledger account by its
	// account number (A26): a payment from Invoices posts to the one its
	// receiving account names, else to the bank role's.
	bankLedgers map[string]int32
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
	ledgers, err := q.BankLedgers(ctx)
	if err != nil {
		return salesBooks{}, err
	}
	b := salesBooks{language: settings.ChartLanguage, booksStart: settings.BooksStart, openingSeq: settings.OpeningAsOfSeq,
		taxCodes: map[string]store.TaxCodesByCodeRow{}, rules: rules, bankLedgers: map[string]int32{}}
	for _, c := range codes {
		b.taxCodes[c.Code] = c
	}
	for _, l := range ledgers {
		b.bankLedgers[l.AccountNumber] = l.LedgerAccountID
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
// standard tax code, then the default rule of its tax code. A rule whose
// account is no longer an income account cannot place a line (R15): the
// fact waits under Needs attention until the rule is pointed elsewhere.
func (b salesBooks) revenueRule(l contracts.JournalLine) (store.SalesRulesRow, error) {
	for _, match := range []string{"line_kind", "tax_category", "tax_code", "default"} {
		for _, r := range b.rules {
			if r.Match != match {
				continue
			}
			matched := false
			switch match {
			case "line_kind":
				matched = l.Kind != "" && deref(r.LineKind) == l.Kind
			case "tax_category":
				matched = l.TaxCategoryID != nil && r.TaxCategoryID != nil && *r.TaxCategoryID == *l.TaxCategoryID
			default:
				matched = deref(r.TaxCode) == l.SAFT
			}
			if !matched {
				continue
			}
			if r.AccountKind != "income" {
				return store.SalesRulesRow{}, &unpostable{reason: codeAccountKindMismatch,
					detail: fmt.Sprintf("The posting rule %q points at account %s, which is not an income account; point it at one under Posting rules.",
						r.Name, r.AccountNumber)}
			}
			return r, nil
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
			ProjectID: l.ProjectID, PostingRuleID: &r.ID, PostingRuleVersion: &r.Version,
			Text: b.text("Line %d · rule %s v%d", "Linje %d · regel %s v%d", l.Position, r.Name, r.Version)})
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
	// A document in another currency (invoices phase 6 design I27): the
	// receivable carries its currency, its signed gross in it and the rate;
	// revenue and VAT post in NOK alone. Its NOK stays Σ BaseNOK + Σ VATNOK.
	if d.Currency != "" && d.Currency != "NOK" {
		gross, err := amountOf(d.Gross)
		if err != nil {
			return nil, err
		}
		rate, ok := new(big.Rat).SetString(d.ExchangeRate)
		if !ok {
			return nil, &unpostable{reason: reasonDocumentUnknown, detail: "The document in " + d.Currency + " carries no rate."}
		}
		lines[0].Currency, lines[0].CurrencyAmount, lines[0].ExchangeRate = d.Currency, signed(gross), rate
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
	if f.Kind == contracts.FactCredit || f.Kind == contracts.FactCreditMerged {
		// A credit fact is in the opening's credit when it is in the
		// customer's credit item's CreditsAt page (I16).
		item, err := q.CustomerCreditItem(ctx, &f.CustomerID)
		switch {
		case noRows(err):
		case err != nil:
			return false, err
		case item.OpeningAsOfSeq != nil:
			threshold = item.OpeningAsOfSeq
		}
		return threshold != nil && f.Seq > *threshold, nil
	}
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

// storedVoucher is a voucher already in the books, read back: its id and its
// postings as lines.
type storedVoucher struct {
	id    int64
	lines []Line
}

// factInputs is what a fact's voucher is built from besides the fact: the
// invoice it is about and that invoice's credit notes, read from the journal
// for a write-off's VAT correction (nil otherwise); the write-off's own
// voucher, for a recovery; and the voucher of the registration an undo or a
// removal of a recovery turns back. A voucher not posted is nil.
type factInputs struct {
	invoice  *contracts.JournalDocument
	credits  []contracts.JournalDocument
	writeOff *storedVoucher
	original *storedVoucher
}

// factVoucher is the voucher of a fact of the log (A13, R4, R5, R9, R14, R15).
// A removal is its registration's voucher with every sign turned, dated the
// removal's day; the undoing of a write-off and the removal of a recovery turn
// back the voucher stored for it, line for line, whatever the accounts say
// now. A date before the books start or in a locked period is the engine's to
// move (A8, R5).
func factVoucher(b salesBooks, f contracts.JournalFact, in factInputs) (*Voucher, error) {
	// Credit applied is a payment of source credit, and its applied
	// movement's FactCredit moves the credit (I16, plan reading 7): the
	// payment and its removal post nothing, so the money moves once.
	if (f.Kind == contracts.FactPayment || f.Kind == contracts.FactPaymentRemoved) && f.Source == contracts.PaymentSourceCredit {
		return nil, nil
	}
	if f.Kind == contracts.FactCredit || f.Kind == contracts.FactCreditMerged {
		return creditVoucher(b, f, in.original)
	}
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
	bank := b.bankLine(f, in.original)
	if f.BankAccount != "" || f.BankReference != "" {
		switch f.Kind {
		case contracts.FactPayment, contracts.FactChargePayment, contracts.FactRecoveredPayment:
			v.BankRef = &BankRef{Account: f.BankAccount, Reference: f.BankReference, Source: f.Source}
		}
	}
	switch f.Kind {
	case contracts.FactPayment, contracts.FactPaymentRemoved:
		v.Kind, v.Rule = "customer_payment", salesRulePayment
		v.Description = b.text("Payment of invoice %d", "Innbetaling på faktura %d", f.InvoiceNumber)
		v.Lines = []Line{withAmount(bank, amount), receivables(neg(amount))}
		// In currency (I24, I27): the receivable takes what the payment
		// cleared, with its currency, and the difference to what arrived
		// posts to currency gain or loss.
		if err := inCurrency(b, &v.Lines, 1, f.Currency, f.Amount, f.ExchangeDifferenceNOK); err != nil {
			return nil, err
		}
		if f.Kind == contracts.FactPaymentRemoved {
			v.Rule = salesRulePaymentRemoved
			v.Description = b.text("Payment of invoice %d removed", "Innbetaling på faktura %d fjernet", f.InvoiceNumber)
			turn(v.Lines)
			if in.original != nil {
				v.TurnsBack = &in.original.id
			}
		}
	case contracts.FactChargePayment, contracts.FactChargePaymentRemoved:
		tc, err := b.taxCode(chargeTaxCode)
		if err != nil {
			return nil, err
		}
		v.Kind, v.Rule = "charge", salesRuleCharge
		v.Description = b.text("Reminder charges paid, invoice %d", "Purrekostnader betalt, faktura %d", f.InvoiceNumber)
		v.Lines = []Line{withAmount(bank, amount), {Role: roleChargeIncome, Amount: neg(amount), TaxCodeID: &tc.ID, TaxRole: "base"}}
		if f.Kind == contracts.FactChargePaymentRemoved {
			v.Rule = salesRuleChargeRemoved
			v.Description = b.text("Reminder charges payment removed, invoice %d", "Betaling av purrekostnader fjernet, faktura %d", f.InvoiceNumber)
			turn(v.Lines)
			if in.original != nil {
				v.TurnsBack = &in.original.id
			}
		}
	case contracts.FactWriteOff, contracts.FactWriteOffUndone:
		if f.WriteOff == nil {
			return nil, &unpostable{reason: reasonDocumentUnknown, detail: "The write-off's fact names no write-off."}
		}
		v.Kind = "write_off"
		v.Description = b.text("Write-off of invoice %d", "Avskrivning av faktura %d", f.InvoiceNumber)
		if f.Kind == contracts.FactWriteOffUndone && in.original != nil {
			v.Rule = salesRuleWriteOffUndone
			v.Description = b.text("Write-off of invoice %d undone", "Avskrivning av faktura %d angret", f.InvoiceNumber)
			turnBack(v, in.original, b.text("Undone in Invoices", "Angret i Fakturering"))
			break
		}
		loss, rule, err := writeOffLines(b, f.WriteOff, amount, in.invoice, in.credits)
		if err != nil {
			return nil, err
		}
		v.Rule = rule
		v.Lines = append([]Line{receivables(neg(amount))}, loss...)
		// Off NOK the receivable carries what the write-off closed in the
		// currency (I27), so a closed invoice's currency sums reach 0.
		if err := withCurrency(&v.Lines[0], f.Currency, f.Amount); err != nil {
			return nil, err
		}
		if f.Kind == contracts.FactWriteOffUndone {
			// A write-off the books never posted — made before they started —
			// is undone as it would have been posted.
			v.Rule = salesRuleWriteOffUndone
			v.Description = b.text("Write-off of invoice %d undone", "Avskrivning av faktura %d angret", f.InvoiceNumber)
			turn(v.Lines)
		}
	case contracts.FactRecoveredPayment, contracts.FactRecoveredPaymentRemoved:
		if f.WriteOff == nil {
			return nil, &unpostable{reason: reasonDocumentUnknown, detail: "The recovered payment names no write-off."}
		}
		v.Kind, v.Rule = "customer_payment", salesRuleRecovered
		v.Description = b.text("Recovered on written-off invoice %d", "Innbetalt på avskrevet faktura %d", f.InvoiceNumber)
		if f.Kind == contracts.FactRecoveredPaymentRemoved && in.original != nil {
			v.Rule = salesRuleRecoveredRemoved
			v.Description = b.text("Recovery on invoice %d removed", "Innbetaling på avskrevet faktura %d fjernet", f.InvoiceNumber)
			turnBack(v, in.original, b.text("Removed in Invoices", "Fjernet i Fakturering"))
			break
		}
		written, err := amountOf(f.WriteOff.AmountNOK)
		if err != nil {
			return nil, err
		}
		before := new(big.Rat)
		if f.WriteOff.RecoveredBeforeNOK != "" {
			if before, err = amountOf(f.WriteOff.RecoveredBeforeNOK); err != nil {
				return nil, err
			}
		}
		var loss []Line
		if in.writeOff != nil {
			// The write-off's own voucher, as posted: its debits are what a
			// recovery turns back in proportion.
			for _, l := range in.writeOff.lines {
				if l.OpenItemID == nil {
					loss = append(loss, l)
				}
			}
		} else if loss, _, err = writeOffLines(b, f.WriteOff, written, in.invoice, in.credits); err != nil {
			return nil, err
		}
		if f.Kind == contracts.FactRecoveredPaymentRemoved {
			// A recovery the books never posted — made before they started —
			// is removed as if it were the write-off's only one.
			before = new(big.Rat)
		}
		// Off NOK the recovery scales on what it cleared of the write-off's
		// NOK, and what arrived beyond or short of it is a currency gain or
		// loss (invoices phase 6, review M2).
		diff := new(big.Rat)
		if f.ExchangeDifferenceNOK != "" {
			if diff, err = amountOf(f.ExchangeDifferenceNOK); err != nil {
				return nil, err
			}
		}
		cleared := sub(amount, diff)
		v.Lines = append([]Line{withAmount(bank, amount)}, scaleRecovery(loss, before, cleared, written)...)
		// A removal turns the whole voucher below, the difference with it.
		switch diff.Sign() {
		case 1:
			v.Lines = append(v.Lines, Line{Role: roleCurrencyGain, Amount: neg(diff), Text: b.text("Currency difference", "Valutadifferanse")})
		case -1:
			v.Lines = append(v.Lines, Line{Role: roleCurrencyLoss, Amount: neg(diff), Text: b.text("Currency difference", "Valutadifferanse")})
		}
		if f.Kind == contracts.FactRecoveredPaymentRemoved {
			v.Rule = salesRuleRecoveredRemoved
			v.Description = b.text("Recovery on invoice %d removed", "Innbetaling på avskrevet faktura %d fjernet", f.InvoiceNumber)
			turn(v.Lines)
		}
	case contracts.FactExchangeDifference:
		// The NOK rest a closing credit note left (invoices phase 6, review
		// M1): the invoice's item cleared, the difference to currency gain
		// or loss.
		v.Kind, v.Rule = "customer_payment", salesRuleExchangeDifference
		v.Description = b.text("Currency difference on invoice %d", "Valutadifferanse på faktura %d", f.InvoiceNumber)
		diff, err := amountOf(f.ExchangeDifferenceNOK)
		if err != nil {
			return nil, err
		}
		role := roleCurrencyGain
		if diff.Sign() < 0 {
			role = roleCurrencyLoss
		}
		v.Lines = []Line{receivables(new(big.Rat).Set(diff)), {Role: role, Amount: neg(diff),
			Text: b.text("Currency difference", "Valutadifferanse")}}
	default:
		return nil, &unpostable{reason: reasonUnknownKind,
			detail: fmt.Sprintf("Invoices' journal holds a fact of kind %q, which this version does not post.", f.Kind)}
	}
	switch f.Kind {
	case contracts.FactPayment, contracts.FactPaymentRemoved, contracts.FactChargePayment, contracts.FactChargePaymentRemoved,
		contracts.FactRecoveredPayment, contracts.FactRecoveredPaymentRemoved:
		v.RuleVersion = salesRulePaymentVersion
	}
	return v, nil
}

// creditVoucher is the voucher of a credit fact (invoices phase 6 design
// I16, plan reading 4), every amount the movement's NOK, on the customer's
// credit item:
//
//   - sales.credit_overpayment: the bank debited — the bank line's account,
//     else the bank role's — the credit item credited;
//   - sales.credit_note_moved: the invoice's item debited, which clears the
//     negative open amount the credit note left, the credit item credited —
//     zero on receivables;
//   - sales.credit_applied: the credit item debited, the invoice's item
//     credited (the payment of source credit posts nothing);
//   - sales.credit_refunded: the credit item debited, the ledger account of
//     the bank account the refund left from credited, else the bank role's;
//   - sales.credit_reversed: the voucher the reversed movement posted turned
//     back line for line when the reversal is all of it, and otherwise — a
//     credit note's surplus returned in part — or when it never posted, the
//     reversed movement's lines at the reversal's amount with every sign
//     turned;
//   - sales.credit_merged: the absorbed customer's credit item debited, the
//     survivor's credited.
func creditVoucher(b salesBooks, f contracts.JournalFact, original *storedVoucher) (*Voucher, error) {
	c := f.Credit
	if c == nil {
		return nil, &unpostable{reason: reasonDocumentUnknown, detail: "The credit fact names no movement."}
	}
	amount, err := amountOf(f.AmountNOK)
	if err != nil {
		return nil, err
	}
	v := &Voucher{Kind: "customer_payment", Date: f.Date, DocumentDate: f.Date, Source: factSource(f), RuleVersion: salesRuleVersion}
	if f.InvoiceID != 0 {
		v.SourceDocumentID = &f.InvoiceID
	}
	credit := func(customer int32, a *big.Rat) Line {
		return Line{Role: roleReceivables, Amount: a, CustomerID: &customer, Item: &OpenItemSpec{CustomerCredit: true}}
	}
	invoice := func(a *big.Rat) Line {
		return Line{Role: roleReceivables, Amount: a, CustomerID: &f.CustomerID, SourceDocumentID: &f.InvoiceID,
			Item: &OpenItemSpec{DocumentNumber: fmt.Sprint(f.InvoiceNumber)}}
	}
	if f.Kind == contracts.FactCreditMerged {
		v.Rule = salesRuleCreditMerged
		v.Description = b.text("Customer credit moved by a customer merge", "Kundekreditt flyttet ved sammenslåing av kunder")
		v.Lines = []Line{credit(c.FromCustomerID, amount), credit(f.CustomerID, neg(amount))}
		return v, nil
	}
	kind := c.Kind
	reversal := kind == contracts.CreditReversed
	if reversal {
		kind = c.ReversedKind
	}
	switch kind {
	case contracts.CreditOverpayment:
		v.Rule, v.RuleVersion = salesRuleCreditOverpayment, salesRulePaymentVersion
		v.Description = b.text("Overpayment of invoice %d kept as credit", "Overbetaling på faktura %d holdt som kreditt", f.InvoiceNumber)
		v.Lines = []Line{withAmount(b.bankLine(f, nil), amount), credit(f.CustomerID, neg(amount))}
		if !reversal && (f.BankAccount != "" || f.BankReference != "") {
			v.BankRef = &BankRef{Account: f.BankAccount, Reference: f.BankReference, Source: f.Source}
		}
	case contracts.CreditNoteMoved:
		v.Rule = salesRuleCreditNoteMoved
		v.Description = b.text("Credit note surplus of invoice %d moved to customer credit", "Kreditnotaoverskudd på faktura %d flyttet til kundekreditt", f.InvoiceNumber)
		v.Lines = []Line{invoice(amount), credit(f.CustomerID, neg(amount))}
		if err := withCurrency(&v.Lines[0], c.Currency, c.Amount); err != nil {
			return nil, err
		}
	case contracts.CreditApplied:
		v.Rule = salesRuleCreditApplied
		v.Description = b.text("Customer credit applied to invoice %d", "Kundekreditt brukt på faktura %d", f.InvoiceNumber)
		v.Lines = []Line{credit(f.CustomerID, amount), invoice(neg(amount))}
		// The credit leaves at what it was carried at, the invoice's item
		// takes what it cleared (plan reading 7).
		if err := inCurrency(b, &v.Lines, 1, c.Currency, c.Amount, c.ExchangeDifferenceNOK); err != nil {
			return nil, err
		}
	case contracts.CreditRefunded:
		v.Rule, v.RuleVersion = salesRuleCreditRefunded, salesRulePaymentVersion
		v.Description = b.text("Customer credit refunded", "Kundekreditt tilbakebetalt")
		bank := Line{Role: roleBank}
		if id, ok := b.bankLedgers[c.RefundBankAccount]; ok && c.RefundBankAccount != "" {
			bank = Line{AccountID: id}
		}
		v.Lines = []Line{credit(f.CustomerID, amount), withAmount(bank, neg(amount))}
		// The bank gives what left; the difference to the credit's carrying
		// NOK is a currency gain or loss (plan reading 9).
		if err := inCurrency(b, &v.Lines, 1, "", "", c.ExchangeDifferenceNOK); err != nil {
			return nil, err
		}
	default:
		return nil, &unpostable{reason: reasonUnknownKind,
			detail: fmt.Sprintf("Invoices' journal holds a credit movement of kind %q, which this version does not post.", c.Kind)}
	}
	if !reversal {
		return v, nil
	}
	v.Rule, v.RuleVersion = salesRuleCreditReversed, salesRuleVersion
	v.Description = b.text("Reversal: ", "Tilbakeføring: ") + v.Description
	if original != nil && storedTotal(original).Cmp(amount) == 0 {
		turnBack(v, original, b.text("Removed in Invoices", "Fjernet i Fakturering"))
		v.BankRef = nil
		return v, nil
	}
	if original != nil && len(original.lines) > 0 && kind == contracts.CreditOverpayment {
		// The bank side the overpayment posted to, whatever the accounts
		// say now.
		v.Lines[0].AccountID, v.Lines[0].Role = original.lines[0].AccountID, ""
	}
	turn(v.Lines)
	v.BankRef = nil
	return v, nil
}

// storedTotal is what a stored voucher moves: the sum of its debits.
func storedTotal(v *storedVoucher) *big.Rat {
	total := new(big.Rat)
	for _, l := range v.lines {
		if l.Amount.Sign() > 0 {
			total.Add(total, l.Amount)
		}
	}
	return total
}

// bankLine is the bank side of a payment fact (A26, R11, rule
// sales.payment v2): a Vipps payment's clearing account; a removal's the
// account its registration posted to, when the books have it, so a removal
// always turns back what was posted; else the ledger account of the bank
// account whose number is the receiving account the bank line named; else
// the bank role's.
func (b salesBooks) bankLine(f contracts.JournalFact, original *storedVoucher) Line {
	removal := f.Kind == contracts.FactPaymentRemoved || f.Kind == contracts.FactChargePaymentRemoved ||
		f.Kind == contracts.FactRecoveredPaymentRemoved
	switch {
	case f.Source == contracts.PaymentSourceVipps:
		return Line{Role: roleClearingVipps}
	case removal && original != nil && len(original.lines) > 0:
		return Line{AccountID: original.lines[0].AccountID}
	}
	if id, ok := b.bankLedgers[f.BankAccount]; ok && f.BankAccount != "" {
		return Line{AccountID: id}
	}
	return Line{Role: roleBank}
}

// withAmount is l with amount.
func withAmount(l Line, amount *big.Rat) Line {
	l.Amount = amount
	return l
}

// turn turns every line's sign, and a tax line's base and a currency
// amount with it.
func turn(lines []Line) {
	for i := range lines {
		lines[i].Amount = neg(lines[i].Amount)
		if lines[i].TaxBase != nil {
			lines[i].TaxBase = neg(lines[i].TaxBase)
		}
		if lines[i].CurrencyAmount != nil {
			lines[i].CurrencyAmount = neg(lines[i].CurrencyAmount)
		}
	}
}

// withCurrency gives a receivables line of a document in currency its
// currency triple (I27): amount in it, signed as the line, and the rate the
// line's NOK is of it. NOK, or no amount, leaves the line as it is.
func withCurrency(l *Line, currency, amount string) error {
	if currency == "" || currency == "NOK" || amount == "" || l.Amount == nil || l.Amount.Sign() == 0 {
		return nil
	}
	a, err := amountOf(amount)
	if err != nil {
		return err
	}
	a = abs(a)
	if a.Sign() == 0 {
		return nil
	}
	cur := new(big.Rat).Set(a)
	if l.Amount.Sign() < 0 {
		cur = neg(cur)
	}
	rate, _ := new(big.Rat).SetString(new(big.Rat).Quo(abs(l.Amount), a).FloatString(6))
	l.Currency, l.CurrencyAmount, l.ExchangeRate = currency, cur, rate
	return nil
}

// inCurrency adjusts a payment's or a credit's voucher for an exchange
// difference (invoices phase 6 design I24, I27; plan readings 7 and 9):
// (*lines)[counter], the side that is not the money's, moves by difference —
// what arrived less what the receivable cleared, positive a gain — and the
// difference posts to currency_gain (a credit) or currency_loss (a debit),
// the roles supplier payments use (8060, 8160). With currency other than ""
// and NOK the counter line, a receivable, carries amount in that currency at
// the rate it cleared at. A difference of "0.00" or "" changes no amount.
func inCurrency(b salesBooks, lines *[]Line, counter int, currency, amount, difference string) error {
	diff := new(big.Rat)
	if difference != "" {
		var err error
		if diff, err = amountOf(difference); err != nil {
			return err
		}
	}
	l := &(*lines)[counter]
	l.Amount = add(l.Amount, diff)
	if err := withCurrency(l, currency, amount); err != nil {
		return err
	}
	text := b.text("Currency difference", "Valutadifferanse")
	switch diff.Sign() {
	case 1:
		*lines = append(*lines, Line{Role: roleCurrencyGain, Amount: neg(diff), Text: text})
	case -1:
		*lines = append(*lines, Line{Role: roleCurrencyLoss, Amount: neg(diff), Text: text})
	}
	return nil
}

// turnBack makes v the reversal of a stored voucher (R15): its postings line
// for line with every sign turned, on the accounts, parties, items and tax
// codes they were posted with — an account inactive since is allowed, as a
// reversal's — and the voucher it reverses named with reason.
func turnBack(v *Voucher, stored *storedVoucher, reason string) {
	v.Lines = make([]Line, 0, len(stored.lines))
	for _, l := range stored.lines {
		l.Amount = neg(l.Amount)
		if l.TaxBase != nil {
			l.TaxBase = neg(l.TaxBase)
		}
		v.Lines = append(v.Lines, l)
	}
	v.Reverses, v.ReversalReason = &stored.id, reason
}

// vatKey is a VAT summary's code and rate: what a credit note's summary is
// taken from (R15).
type vatKey struct{ saft, rate string }

// remainingSummaries is the invoice's VAT summaries less its credit notes',
// per code and rate, in the invoice's order (R15): what a write-off's VAT
// correction is shared over, since what a credit note took back is no longer
// owed.
func remainingSummaries(doc *contracts.JournalDocument, credits []contracts.JournalDocument) ([]contracts.JournalVATSummary, error) {
	type sums struct{ base, vat *big.Rat }
	left := map[vatKey]*sums{}
	var order []vatKey
	take := func(v contracts.JournalVATSummary, sign int64) error {
		base, err := amountOf(v.BaseNOK)
		if err != nil {
			return err
		}
		vat, err := amountOf(v.VATNOK)
		if err != nil {
			return err
		}
		k := vatKey{v.SAFT, v.RatePercent}
		s, ok := left[k]
		if !ok {
			s = &sums{base: new(big.Rat), vat: new(big.Rat)}
			left[k] = s
			order = append(order, k)
		}
		f := big.NewRat(sign, 1)
		s.base.Add(s.base, new(big.Rat).Mul(base, f))
		s.vat.Add(s.vat, new(big.Rat).Mul(vat, f))
		return nil
	}
	for _, v := range doc.VATSummaries {
		if err := take(v, 1); err != nil {
			return nil, err
		}
	}
	for _, c := range credits {
		for _, v := range c.VATSummaries {
			if err := take(v, -1); err != nil {
				return nil, err
			}
		}
	}
	out := make([]contracts.JournalVATSummary, 0, len(order))
	for _, k := range order {
		out = append(out, contracts.JournalVATSummary{SAFT: k.saft, RatePercent: k.rate,
			BaseNOK: left[k].base.FloatString(2), VATNOK: left[k].vat.FloatString(2)})
	}
	return out, nil
}

// writeOffLines is the debit side of a write-off of amount (R4, R15), and the
// rule it follows: a rounding to the rounding account; a loss or other
// without the correction to bad debt, whole; with it, amount shared over what
// is left of the invoice's VAT summaries once its credit notes' are taken off,
// per code and rate, in proportion to their gross, each share's VAT debited
// to its code's VAT account and the rest to bad debt, the VAT and its base
// marked for the return's tapPåKrav. Payments are in proportion: they are not
// taken off. The shares are rounded to øre and the last summary takes what
// rounding left, so they sum to amount.
func writeOffLines(b salesBooks, w *contracts.JournalWriteOff, amount *big.Rat, doc *contracts.JournalDocument, credits []contracts.JournalDocument) ([]Line, string, error) {
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
	summaries, err := remainingSummaries(doc, credits)
	if err != nil {
		return nil, "", err
	}
	for _, s := range summaries {
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

// scaleRecovery is a write-off's debit lines scaled to a recovery (R4, R15),
// every sign turned — the recovery credits bad debt and gives the VAT back in
// the write-off's proportion. It scales cumulatively: each line is its share
// of what is recovered with this one, before + recovered, less its share of
// what was recovered before, each share rounded to øre, so the recoveries of
// a write-off add up to its lines exactly once all of it is recovered. What
// rounding leaves goes on a bad debt line with no tax code — the first such
// line, or one of its own — never on a VAT line.
func scaleRecovery(lines []Line, before, recovered, written *big.Rat) []Line {
	share := func(r, of *big.Rat) *big.Rat {
		return docmath.Round2(new(big.Rat).Quo(new(big.Rat).Mul(r, of), written))
	}
	after := add(before, recovered)
	out := make([]Line, 0, len(lines)+1)
	left := new(big.Rat).Set(recovered)
	plain := -1
	for _, l := range lines {
		part := sub(share(l.Amount, after), share(l.Amount, before))
		left = sub(left, part)
		l.Amount = neg(part)
		if l.TaxBase != nil {
			l.TaxBase = neg(sub(share(l.TaxBase, after), share(l.TaxBase, before)))
		}
		if plain < 0 && l.TaxCodeID == nil && l.TaxRole != "tax" {
			plain = len(out)
		}
		out = append(out, l)
	}
	if left.Sign() != 0 {
		if plain < 0 {
			out = append(out, Line{Role: roleBadDebt, Amount: new(big.Rat)})
			plain = len(out) - 1
		}
		out[plain].Amount = sub(out[plain].Amount, left)
	}
	return out
}
