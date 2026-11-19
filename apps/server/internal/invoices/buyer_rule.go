package invoices

import (
	"context"
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/creditlimit"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is delivery E-2's two rules on the issue (invoices phase 6 design
// I37, I38; plan docs/superpowers/plans/2026-11-18-invoices-phase6-e-plan.md,
// steps E12, E13).
//
// The 2028 buyer rule. Bokføringsforskriften § 5-1-2 first paragraph, as
// amended by FOR-2026-09-29-1933, from 1 January 2028: «Ved salg til
// bokføringspliktig kjøper skal kjøpers organisasjonsnummer alltid angis.» An
// invoice or a credit note to a buyer the customer directory says is
// bookkeeping-obligated, without the buyer's organisation number, is refused
// from that day (buyer_org_number_required), judged on the document's issue
// date, and warned of before it (buyer_org_number_missing). The reverse-charge
// refusal (reverse_charge_needs_org_number) stays: it is now a special case of
// this one. A buyer whose obligation is unknown is judged as not obligated, and
// § 5-1-2's address alternative still serves it.
//
// The credit limit: the customer's exposure in NOK — Invoices' own open receivables at
// their NOK, Orders' confirmed uninvoiced through contracts.OrderBacklog, and a draft's own
// gross — judged by creditlimit, the one definition. Invoices only ever warns
// (credit_limit_exceeded): the refusal is Orders' at confirm.

// buyerOrgNumberFrom is the day § 5-1-2's amended first paragraph takes effect
// (FOR-2026-09-29-1933). A var, so a test can move it, as b2bDutyFrom.
var buyerOrgNumberFrom = time.Date(2028, time.January, 1, 0, 0, 0, 0, time.UTC)

// The codes of the 2028 rule and the credit limit.
const (
	codeBuyerOrgNumberRequired   = "buyer_org_number_required"
	warningBuyerOrgNumberMissing = "buyer_org_number_missing"
	warningCreditLimitExceeded   = "credit_limit_exceeded"
)

// buyerOrgNumberRequiredDetail is the refusal's words, citing the rule.
const buyerOrgNumberRequiredDetail = "The buyer is bookkeeping-obligated, and from 1 January 2028 its organisation number must be on the invoice (bokføringsforskriften § 5-1-2)."

// bookkeepingObligated is whether the directory says the buyer is
// bookkeeping-obligated. Unknown (nil) is not.
func bookkeepingObligated(p *contracts.CustomerBillingProfile) bool {
	return p != nil && p.BookkeepingObligated != nil && *p.BookkeepingObligated
}

// buyerOrgNumberRule is the 2028 rule on one document: for an obligated buyer
// without the organisation number, a refusal from buyerOrgNumberFrom, judged
// on issueDate, and a warning code before it. A business whose obligation is
// unknown — the register has not said — is warned of from that day, never
// refused: it may well be obligated, and nothing here can tell. Neither when
// the document carries the organisation number, or the buyer is known not to
// be obligated, or is a person not known to be.
func buyerOrgNumberRule(p *contracts.CustomerBillingProfile, orgNumber *string, issueDate time.Time) (*gen.InvoicesConflictProblem, string) {
	if p == nil || (orgNumber != nil && *orgNumber != "") {
		return nil, ""
	}
	if !bookkeepingObligated(p) {
		if p.BookkeepingObligated == nil && p.Type == "business" && !issueDate.Before(buyerOrgNumberFrom) {
			return nil, warningBuyerOrgNumberMissing
		}
		return nil, ""
	}
	if !issueDate.Before(buyerOrgNumberFrom) {
		return cannotIssue(codeBuyerOrgNumberRequired, buyerOrgNumberRequiredDetail), ""
	}
	return nil, warningBuyerOrgNumberMissing
}

// draftBuyerWarning is the rule as a draft shows it before its issue, judged
// on today: the warning before 2028, the refusal's code from it.
func draftBuyerWarning(p *contracts.CustomerBillingProfile, orgNumber *string, today time.Time) string {
	refusal, warning := buyerOrgNumberRule(p, orgNumber, today)
	if refusal != nil {
		return codeBuyerOrgNumberRequired
	}
	return warning
}

// creditExposure is the customer's credit judged with extra — a draft's own
// gross, or nothing once issued — nil when the customer has no limit. The
// open invoices are read from this module's own tables, the order backlog
// through deps.OrderBacklog, both on the pool: never call it inside a
// transaction.
func (s *server) creditExposure(ctx context.Context, p *contracts.CustomerBillingProfile, extra ...contracts.CurrencyAmount) (*gen.InvoicesCreditExposure, error) {
	if p == nil || p.CreditLimit == nil {
		return nil, nil
	}
	totals, err := newCustomerReceivables(s.deps).ReceivablesForCustomer(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	var backlog []contracts.CurrencyAmount
	if s.deps.OrderBacklog != nil {
		noteContractCall(ctx, "OrderBacklog.UninvoicedForCustomer")
		if backlog, err = s.deps.OrderBacklog.UninvoicedForCustomer(ctx, p.ID); err != nil {
			return nil, fmt.Errorf("invoices: read customer %d's order backlog: %w", p.ID, err)
		}
	}
	// The orders' and the draft's other currencies at today's rate; the
	// invoices count at their own NOK (open_nok).
	rates, err := creditlimit.RatesFor(ctx, s.deps.ExchangeRates, s.deps.Clock(),
		append(creditlimit.Currencies(backlog), creditlimit.Currencies(extra)...)...)
	if err != nil {
		return nil, err
	}
	exposure, err := creditlimit.Exposure(&totals, backlog, rates, extra...)
	if err != nil {
		return nil, err
	}
	exceeded, err := creditlimit.Exceeds(p.CreditLimit, exposure)
	if err != nil {
		return nil, err
	}
	out := &gen.InvoicesCreditExposure{
		Limit: *p.CreditLimit, ExposureNok: exposure.NOK, Exceeded: exceeded, OrdersCounted: s.deps.OrderBacklog != nil,
		Uncounted: make([]gen.InvoicesCreditAmount, 0, len(exposure.Uncounted)),
	}
	for _, u := range exposure.Uncounted {
		out.Uncounted = append(out.Uncounted, gen.InvoicesCreditAmount{Currency: u.Currency, Amount: u.Amount})
	}
	return out, nil
}

// withDraftCredit is a draft invoice's credit, its gross counted: the figures
// on resp.CreditLimit and the warning when they pass the limit. A read that fails
// is logged and shows nothing: a warning must not fail the draft's read.
func (s *server) withDraftCredit(ctx context.Context, inv store.InvoicesInvoice, p *contracts.CustomerBillingProfile, resp *gen.InvoicesInvoiceResponse) {
	gross := contracts.CurrencyAmount{Currency: inv.Currency, Amount: fmt.Sprintf("%.2f", resp.GrossTotal)}
	credit, err := s.creditExposure(ctx, p, gross)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: the customer's credit could not be judged", "invoice_id", inv.ID, "error", err.Error())
		return
	}
	s.attachCredit(credit, resp)
}

// attachCredit puts credit on resp, and the warning when it is exceeded.
func (s *server) attachCredit(credit *gen.InvoicesCreditExposure, resp *gen.InvoicesInvoiceResponse) {
	if credit == nil {
		return
	}
	resp.CreditLimit = credit
	if credit.Exceeded {
		resp.Warnings = append(resp.Warnings, warningCreditLimitExceeded)
	}
}

// withIssueWarnings are the issue's answer's warnings of this file, after the
// commit: buyer_org_number_missing before 2028, judged on the document's issue
// date, and — for an invoice — the credit as it now stands, the document
// counted among the open ones.
func (s *server) withIssueWarnings(ctx context.Context, out issueOutcome, resp *gen.InvoicesInvoiceResponse) {
	if _, warning := buyerOrgNumberRule(out.profile, out.doc.BuyerOrganisationNumber, out.doc.IssueDate.Time); warning != "" {
		resp.Warnings = append(resp.Warnings, warning)
	}
	if out.doc.Kind != kindInvoice {
		return
	}
	credit, err := s.creditExposure(ctx, out.profile)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: the customer's credit could not be judged", "invoice_id", out.doc.ID, "error", err.Error())
		return
	}
	s.attachCredit(credit, resp)
}

// withDraftBuyerWarning is the 2028 rule on an invoice draft, judged on today
// with the buyer snapshot its issue would take.
func withDraftBuyerWarning(p *contracts.CustomerBillingProfile, today time.Time, resp *gen.InvoicesInvoiceResponse) {
	if p == nil {
		return
	}
	if code := draftBuyerWarning(p, buyerSnapshot(p).BuyerOrganisationNumber, today); code != "" {
		resp.Warnings = append(resp.Warnings, code)
	}
}
