package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the issue (D6): one serialised transaction that turns a draft
// into a numbered, immutable salgsdokument. The order is the design's and it
// matters. Before the transaction: the draft is read, the object store is
// checked, and an invoice's billing profile is read — no lock is held across a
// directory call. In the transaction: the document is locked, the settings row
// shared, the number allocated, and only then is any rule checked, because the
// counter row is the one thing that serialises two issues and every check
// that depends on other documents must run after it. Any refusal rolls the
// whole transaction back, the number with it. The lock order is always the
// document, then the settings row, then the counter, then — for a credit note
// — the original; nothing else takes these in another order.

// The codes and titles of the issue's refusals.
const (
	codeStorageUnavailable      = "storage_unavailable"
	codeInvoiceChanged          = "invoice_changed"
	codeSellerIncomplete        = "seller_incomplete"
	codeNoLines                 = "no_lines"
	codeDeliveryDateMissing     = "delivery_date_missing"
	codeIssueDateNotAllowed     = "issue_date_not_allowed"
	codeBuyerIncomplete         = "buyer_incomplete"
	codeVatCodeInactive         = "vat_code_inactive"
	codeVatCodeNotValid         = "vat_code_not_valid"
	codeVatNotRegistered        = "vat_not_registered"
	codeCategoryONotAllowed     = "category_o_not_allowed"
	codeReverseChargeNeedsOrgNr = "reverse_charge_needs_org_number"
	codeVatCodesAmbiguous       = "vat_codes_ambiguous"
	storageUnavailableTitle     = "Document storage is unavailable"
	cannotIssueTitle            = "The document cannot be issued"
)

// issueAfterAllocation is called inside the issue's transaction right after
// the number is allocated, so a test can make the transaction fail there and
// prove the number rolls back with it. nil in production.
var issueAfterAllocation func(ctx context.Context, invoiceID int64) error

// cannotIssue is one of the issue's 409s.
func cannotIssue(code, detail string) *gen.InvoicesConflictProblem {
	return ptr(conflict(code, cannotIssueTitle, detail))
}

// issuedLine is one line as it will be issued: its net and the VAT treatment
// its snapshot records.
type issuedLine struct {
	id       int64
	position int32
	taxed    taxedLine
	// amounts, when set, replace the line's own: a credit note's line that
	// returns its original line's last unit takes what that line has left
	// (creditBook.total).
	amounts *lineAmounts
}

// issuePlan is what the checks decided the issue writes: the lines' VAT, and
// for an invoice the buyer snapshot (a credit note's draft already carries its
// original's).
type issuePlan struct {
	lines []issuedLine
	buyer *store.IssueDocumentParams
	// summary, when the checks computed it, is the VAT the issue writes: a
	// credit note's (creditBook.total). nil means summarize over the lines.
	summary *planSummary
}

// planSummary is a document's VAT rows and totals as its issue writes them.
type planSummary struct {
	rows      []vatSummary
	totals    documentTotals
	ambiguous bool
}

// buyerSnapshot is D4's buyer snapshot from the billing profile.
func buyerSnapshot(p *contracts.CustomerBillingProfile) *store.IssueDocumentParams {
	nonEmpty := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	b := &store.IssueDocumentParams{
		BuyerCustomerNumber: &p.CustomerNumber, BuyerType: &p.Type,
		BuyerPeppolID: nonEmpty(p.PeppolID), BuyerGln: nonEmpty(p.GLN),
	}
	name := p.Name
	if p.LegalName != "" {
		name = p.LegalName
	}
	b.BuyerName = &name
	if p.Type == "business" && p.LegalID != "" {
		if p.LegalCountry == "NO" {
			b.BuyerOrganisationNumber = &p.LegalID
		} else if p.LegalCountry != "" {
			b.BuyerForeignID = ptr(p.LegalCountry + p.LegalID)
		}
	}
	if a := p.InvoiceAddress; a != nil {
		b.BuyerAddressLine1, b.BuyerAddressLine2 = nonEmpty(a.Line1), nonEmpty(a.Line2)
		b.BuyerPostalCode, b.BuyerCity = nonEmpty(a.PostalCode), nonEmpty(a.City)
		b.BuyerRegion, b.BuyerCountry = nonEmpty(a.Region), nonEmpty(strings.ToUpper(a.Country))
	}
	language := "nb"
	if p.Language == "en" {
		language = "en"
	}
	b.BuyerLanguage = &language
	return b
}

// buyerComplete is § 5-1-2's buyer: a complete address — line 1, city and
// country, and a postal code too when the country is NO — or an organisation
// number.
func buyerComplete(b *store.IssueDocumentParams) bool {
	if b.BuyerOrganisationNumber != nil {
		return true
	}
	set := func(s *string) bool { return s != nil && *s != "" }
	if !set(b.BuyerAddressLine1) || !set(b.BuyerCity) || !set(b.BuyerCountry) {
		return false
	}
	return *b.BuyerCountry != "NO" || set(b.BuyerPostalCode)
}

// dateList is dates as a person reads them.
func dateList(dates []time.Time) string {
	if len(dates) == 0 {
		return "none"
	}
	out := make([]string, 0, len(dates))
	for _, d := range dates {
		out = append(out, d.Format(time.DateOnly))
	}
	return strings.Join(out, ", ")
}

// invoiceIssueChecks are the checks only an invoice keeps (D6 step 5): the
// customer gates on the profile read before the transaction, the buyer, and
// the VAT codes as they stand on the issue date.
func invoiceIssueChecks(ctx context.Context, txq *store.Queries, profile *contracts.CustomerBillingProfile,
	settings store.InvoicesSetting, issueDate time.Time, lines []store.InvoicesLine,
) (issuePlan, *gen.InvoicesConflictProblem, error) {
	if refusal := customerGate(profile); refusal != nil {
		return issuePlan{}, refusal, nil
	}
	plan := issuePlan{buyer: buyerSnapshot(profile)}
	if !buyerComplete(plan.buyer) {
		return issuePlan{}, cannotIssue(codeBuyerIncomplete,
			"The buyer has neither a complete address nor an organisation number (§ 5-1-2)."), nil
	}
	codes, err := vatCodesOn(ctx, txq, pgDate(issueDate))
	if err != nil {
		return issuePlan{}, nil, err
	}
	for _, l := range lines {
		code := codes[l.VatCodeID]
		if !code.active {
			r := cannotIssue(codeVatCodeInactive, fmt.Sprintf("Line %d's VAT code is no longer offered.", l.Position))
			r.LinePosition = &l.Position
			return issuePlan{}, r, nil
		}
		if code.rate == nil {
			r := cannotIssue(codeVatCodeNotValid, fmt.Sprintf("Line %d's VAT code has no rate on %s.",
				l.Position, issueDate.Format(time.DateOnly)))
			r.LinePosition = &l.Position
			return issuePlan{}, r, nil
		}
		net, err := ratFromNumeric(l.LineNet)
		if err != nil {
			return issuePlan{}, nil, err
		}
		plan.lines = append(plan.lines, issuedLine{id: l.ID, position: l.Position, taxed: taxedLine{
			net: net, category: code.category, rate: code.rate, safT: code.safT, reason: code.reason,
		}})
	}
	for _, l := range plan.lines {
		switch {
		case !settings.VatRegistered && l.taxed.category != "O":
			return issuePlan{}, cannotIssue(codeVatNotRegistered, fmt.Sprintf(
				"The seller is not VAT-registered, so every line is outside the VAT act (category O); line %d is not.", l.position)), nil
		case settings.VatRegistered && l.taxed.category == "O":
			return issuePlan{}, cannotIssue(codeCategoryONotAllowed, fmt.Sprintf(
				"The seller is VAT-registered, so no line may be outside the VAT act (category O); line %d is.", l.position)), nil
		case l.taxed.category == "AE" && plan.buyer.BuyerOrganisationNumber == nil:
			return issuePlan{}, cannotIssue(codeReverseChargeNeedsOrgNr, fmt.Sprintf(
				"Line %d is reverse charge, which needs the buyer's organisation number (§ 5-1-1).", l.position)), nil
		}
	}
	return plan, nil, nil
}

// PostInvoicesByIdIssue Issue a draft
// (POST /api/v1/invoices/{id}/issue)
func (s *server) PostInvoicesByIdIssue(ctx context.Context, req gen.PostInvoicesByIdIssueRequestObject) (gen.PostInvoicesByIdIssueResponseObject, error) {
	q := store.New(s.deps.Pool)
	draft, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesByIdIssue404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	if draft.Status != statusDraft {
		return gen.PostInvoicesByIdIssue409ApplicationProblemPlusJSONResponse(invoiceIssued()), nil
	}
	if !s.storageConfigured {
		status := int32(503)
		code, title := codeStorageUnavailable, storageUnavailableTitle
		detail := "This installation has no object store, so an issued document's PDF could never be stored. Nothing was issued."
		return gen.PostInvoicesByIdIssue503ApplicationProblemPlusJSONResponse(gen.InvoicesConflictProblem{
			Code: &code, Title: &title, Detail: &detail, Status: &status,
		}), nil
	}
	var profile *contracts.CustomerBillingProfile
	if draft.Kind == kindInvoice {
		if profile, err = s.customerProfile(ctx, draft.CustomerID); err != nil {
			return nil, err
		}
	}
	today := businessDay(s.deps.Clock())
	issueDate := today
	if req.Body.IssueDate != nil {
		issueDate = utcDay(req.Body.IssueDate.Time)
	}

	var refusal *gen.InvoicesConflictProblem
	var issued store.InvoicesInvoice
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		// 1. The document, FOR UPDATE: two issues of one draft queue here.
		locked, err := txq.LockInvoice(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDocumentGone
		}
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", req.Id, err)
		}
		if locked.Status != statusDraft {
			refusal = ptr(invoiceIssued())
			return errRefused
		}
		// 2. A merge may have re-pointed the draft since the profile was read.
		if locked.Kind == kindInvoice && locked.CustomerID != draft.CustomerID {
			refusal = ptr(conflict(codeInvoiceChanged, cannotIssueTitle, "The invoice changed; try again."))
			return errRefused
		}
		// 3. The settings row FOR SHARE: the seller snapshot and the start.
		settings, err := txq.ShareSettings(ctx)
		if err != nil {
			return fmt.Errorf("invoices: share the settings: %w", err)
		}
		// 4. The number.
		number, err := txq.AllocateNumber(ctx, settings.SeriesStart)
		if err != nil {
			return fmt.Errorf("invoices: allocate a number: %w", err)
		}
		if issueAfterAllocation != nil {
			if err := issueAfterAllocation(ctx, locked.ID); err != nil {
				return err
			}
		}
		// 5. Only now, the checks.
		lines, err := txq.Lines(ctx, locked.ID)
		if err != nil {
			return fmt.Errorf("invoices: read document %d's lines: %w", locked.ID, err)
		}
		deliveryEnd := deliveryEndOf(locked)
		latest, err := txq.LatestIssueDate(ctx)
		if err != nil {
			return fmt.Errorf("invoices: read the latest issue date: %w", err)
		}
		// A NULL latest (nothing issued yet) is the zero time: no floor.
		var latestDay time.Time
		if latest.Valid {
			latestDay = latest.Time
		}
		allowed := allowedIssueDates(today, deliveryEnd, latestDay)
		switch missing := sellerMissingFields(settings); {
		case len(missing) > 0:
			refusal = cannotIssue(codeSellerIncomplete, "The seller record still lacks: "+strings.Join(missing, ", ")+".")
		case len(lines) == 0:
			refusal = cannotIssue(codeNoLines, "A document needs at least one line.")
		case deliveryEnd.IsZero():
			refusal = cannotIssue(codeDeliveryDateMissing, "A document states when it was delivered: a day or a period (§ 5-1-1 nr. 4).")
		case !containsDate(allowed, issueDate):
			refusal = cannotIssue(codeIssueDateNotAllowed, fmt.Sprintf(
				"This document may be issued today with: %s (§ 5-1-3).", dateList(allowed)))
			dates := make([]openapi_types.Date, 0, len(allowed))
			for _, d := range allowed {
				dates = append(dates, wireDate(d))
			}
			refusal.AllowedIssueDates = &dates
		}
		if refusal != nil {
			return errRefused
		}
		var plan issuePlan
		switch locked.Kind {
		case kindInvoice:
			plan, refusal, err = invoiceIssueChecks(ctx, txq, profile, settings, issueDate, lines)
		case kindCreditNote:
			plan, refusal, err = creditIssueChecks(ctx, txq, locked, lines)
		default:
			return fmt.Errorf("invoices: document %d is a %s, which this module cannot issue", locked.ID, locked.Kind)
		}
		if err != nil {
			return err
		}
		if refusal != nil {
			return errRefused
		}
		exchangeRate, err := ratFromNumeric(locked.ExchangeRate)
		if err != nil {
			return err
		}
		taxed := make([]taxedLine, 0, len(plan.lines))
		for _, l := range plan.lines {
			taxed = append(taxed, l.taxed)
		}
		rows, totals, ambiguous := summarize(taxed, exchangeRate)
		if plan.summary != nil {
			rows, totals, ambiguous = plan.summary.rows, plan.summary.totals, plan.summary.ambiguous
		}
		if ambiguous {
			refusal = cannotIssue(codeVatCodesAmbiguous,
				"Two lines share a VAT category and rate but carry different SAF-T codes, so one VAT summary row could not name its code.")
			return errRefused
		}

		// 6. The writes: the lines' snapshots, the summaries, and last the
		// row itself — the trigger refuses line writes under an issued one.
		for _, l := range plan.lines {
			if a := l.amounts; a != nil {
				p := store.SquareCreditLineParams{ID: l.id}
				for _, c := range []struct {
					dst *pgtype.Numeric
					v   *big.Rat
				}{{&p.LineGross, a.gross}, {&p.LineAllowance, a.allowance}, {&p.LineNet, a.net}} {
					if *c.dst, err = numericFromRat(c.v, 2); err != nil {
						return err
					}
				}
				if err := txq.SquareCreditLine(ctx, p); err != nil {
					return fmt.Errorf("invoices: square line %d: %w", l.id, err)
				}
			}
			rate, err := numericFromRat(l.taxed.rate, 2)
			if err != nil {
				return err
			}
			if err := txq.SnapshotLine(ctx, store.SnapshotLineParams{
				ID: l.id, VatRatePercent: rate, VatCategory: &l.taxed.category, SafTCode: &l.taxed.safT,
				ExemptionReason: l.taxed.reason,
			}); err != nil {
				return fmt.Errorf("invoices: snapshot line %d: %w", l.id, err)
			}
		}
		for _, r := range rows {
			p := store.InsertVatSummaryParams{InvoiceID: locked.ID, VatCategory: r.category, SafTCode: r.safT, ExemptionReason: r.reason}
			for _, c := range []struct {
				dst *pgtype.Numeric
				v   *big.Rat
			}{{&p.RatePercent, r.rate}, {&p.TaxableAmount, r.taxable}, {&p.VatAmount, r.vat}, {&p.VatAmountNok, r.vatNOK}} {
				if *c.dst, err = numericFromRat(c.v, 2); err != nil {
					return err
				}
			}
			if err := txq.InsertVatSummary(ctx, p); err != nil {
				return fmt.Errorf("invoices: write document %d's VAT: %w", locked.ID, err)
			}
		}
		params := store.IssueDocumentParams{}
		if plan.buyer != nil {
			params = *plan.buyer
		}
		params.ID, params.Number, params.IssueDate = locked.ID, &number, pgDate(issueDate)
		if locked.Kind == kindInvoice && locked.PaymentTermsDays != nil {
			params.DueDate = pgDate(issueDate.AddDate(0, 0, int(*locked.PaymentTermsDays)))
		}
		sellerSnapshot(&params, settings)
		if params.NetTotal, params.VatTotal, params.GrossTotal, params.VatTotalNok, err = numerics(totals); err != nil {
			return err
		}
		params.Now, params.IssuedByUserID = s.deps.Clock(), ptr(callerID(ctx))
		if plan.buyer == nil {
			// A credit note keeps the buyer snapshot its draft copied.
			keepBuyer(&params, locked)
		}
		issued, err = txq.IssueDocument(ctx, params)
		if err != nil {
			return fmt.Errorf("invoices: issue document %d: %w", locked.ID, err)
		}
		return nil
	})
	if refusal != nil {
		return gen.PostInvoicesByIdIssue409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if errors.Is(err, errDocumentGone) {
		return gen.PostInvoicesByIdIssue404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	// After the commit, the PDF is stored once (D7). A failure there is
	// logged and never fails the issue; pdfStored says so.
	issued = s.storeAfterIssue(ctx, q, issued)
	resp, err := s.invoiceResponse(ctx, q, issued, nil)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdIssue200JSONResponse(resp), nil
}

// containsDate reports whether d is one of dates.
func containsDate(dates []time.Time, d time.Time) bool {
	for _, x := range dates {
		if x.Equal(d) {
			return true
		}
	}
	return false
}

// sellerSnapshot copies the settings row into the issue's seller snapshot
// (D4). An optional field that is not set is stored as NULL.
func sellerSnapshot(p *store.IssueDocumentParams, s store.InvoicesSetting) {
	optional := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	p.SellerLegalName, p.SellerOrganisationNumber = &s.LegalName, &s.OrganisationNumber
	p.SellerVatRegistered, p.SellerInForetaksregisteret = &s.VatRegistered, &s.InForetaksregisteret
	p.SellerAddressLine1, p.SellerAddressLine2 = &s.AddressLine1, optional(s.AddressLine2)
	p.SellerPostalCode, p.SellerCity, p.SellerCountry = &s.PostalCode, &s.City, &s.Country
	p.SellerBankAccount, p.SellerIban, p.SellerBic = &s.BankAccount, optional(s.Iban), optional(s.Bic)
	p.SellerEmail, p.SellerFooterText = optional(s.Email), optional(s.FooterText)
}

// keepBuyer carries a document's own buyer snapshot through the issue.
func keepBuyer(p *store.IssueDocumentParams, inv store.InvoicesInvoice) {
	p.BuyerCustomerNumber, p.BuyerType, p.BuyerName = inv.BuyerCustomerNumber, inv.BuyerType, inv.BuyerName
	p.BuyerOrganisationNumber, p.BuyerForeignID = inv.BuyerOrganisationNumber, inv.BuyerForeignID
	p.BuyerAddressLine1, p.BuyerAddressLine2 = inv.BuyerAddressLine1, inv.BuyerAddressLine2
	p.BuyerPostalCode, p.BuyerCity, p.BuyerRegion, p.BuyerCountry = inv.BuyerPostalCode, inv.BuyerCity, inv.BuyerRegion, inv.BuyerCountry
	p.BuyerPeppolID, p.BuyerGln, p.BuyerLanguage = inv.BuyerPeppolID, inv.BuyerGln, inv.BuyerLanguage
}
