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
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
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
// whole transaction back, the number with it — kid_length_exceeded too,
// judged once the number is known. The lock order is always the
// document, then the settings row, then the counter, then — for a credit note
// — the original; nothing else takes these in another order. An invoice that
// bills work then has it stamped invoiced by its modules' holders, on this
// transaction, after every check and before the first write (writeback.go):
// their rows are locked last, Projects', then Expenses', then Time's. A
// credit note that returns a line in full has the same holders take that
// line's stamps back, at the same place (releases.go).

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
	codeKidLengthExceeded       = "kid_length_exceeded"
	storageUnavailableTitle     = "Document storage is unavailable"
	cannotIssueTitle            = "The document cannot be issued"
)

// issueAfterAllocation is called inside the issue's transaction right after
// the number is allocated, with the transaction itself, so a test can make
// the transaction fail there and prove the number rolls back with it, or read
// pg_current_xact_id() through tx and prove a holder ran on the same one. nil
// in production.
var issueAfterAllocation func(ctx context.Context, tx pgx.Tx, invoiceID int64) error

// issueBeforeLock is called after every read the issue makes before its
// transaction and before the transaction begins, so a test can slip a save in
// between (invoice_changed). nil in production.
var issueBeforeLock func(ctx context.Context, invoiceID int64)

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
	// original is a credit note's original as locked, and releases the work
	// of it the credit note releases (invoices work design D8).
	original *store.InvoicesInvoice
	releases []release
	// settlement is an invoice that deducts an earlier one (invoices work
	// design D7): its gross must be positive. correction is one with an
	// energy correction line (energy consumption billing design D7), which
	// may be negative: its gross must be positive too.
	settlement, correction bool
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
		// The customers module stores a legal identity's country lowercase
		// ("no"); the snapshot prints it the ISO way. The customers +
		// invoices integration test found this comparison case-sensitive.
		country := strings.ToUpper(p.LegalCountry)
		if country == "NO" {
			b.BuyerOrganisationNumber = &p.LegalID
			// § 5-1-2: a VAT-registered buyer's organisation number is
			// followed by MVA, on the PDF and in the EHF's PartyTaxScheme.
			b.BuyerVatRegistered = p.VATRegistered
		} else if country != "" {
			b.BuyerForeignID = ptr(country + p.LegalID)
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
// the VAT codes as they stand on the issue date — but for a settlement's
// deduction lines, taxed at their a-kontos' snapshots and capped by what the
// a-kontos have left, read here, after the counter, and never locked
// (invoices work design D7, issueDeductions).
func invoiceIssueChecks(ctx context.Context, txq *store.Queries, profile *contracts.CustomerBillingProfile,
	settings store.InvoicesSetting, issueDate time.Time, locked store.InvoicesInvoice, lines []store.InvoicesLine,
) (issuePlan, *gen.InvoicesConflictProblem, error) {
	if refusal := customerGate(profile); refusal != nil {
		return issuePlan{}, refusal, nil
	}
	plan := issuePlan{buyer: buyerSnapshot(profile)}
	if !buyerComplete(plan.buyer) {
		return issuePlan{}, cannotIssue(codeBuyerIncomplete,
			"The buyer has neither a complete address nor an organisation number (§ 5-1-2)."), nil
	}
	// § 5-1-2 as amended (FOR-2026-09-29-1933): from 2028 an obligated buyer's
	// organisation number, whatever its address (buyer_rule.go).
	if refusal, _ := buyerOrgNumberRule(profile, plan.buyer.BuyerOrganisationNumber, issueDate); refusal != nil {
		return issuePlan{}, refusal, nil
	}
	codes, err := vatCodesOn(ctx, txq, pgDate(issueDate))
	if err != nil {
		return issuePlan{}, nil, err
	}
	deductions, refusal, err := issueDeductions(ctx, txq, locked, lines)
	if err != nil || refusal != nil {
		return issuePlan{}, refusal, err
	}
	plan.settlement = len(deductions) > 0
	for _, l := range lines {
		plan.correction = plan.correction || l.Correction
		if snap, ok := deductions[l.ID]; ok {
			net, err := ratFromNumeric(l.LineNet)
			if err != nil {
				return issuePlan{}, nil, err
			}
			snap.Net = net
			plan.lines = append(plan.lines, issuedLine{id: l.ID, position: l.Position, taxed: snap})
			continue
		}
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
			Net: net, Category: code.category, Rate: code.rate, SAFT: code.safT, Reason: code.reason,
		}})
	}
	for _, l := range plan.lines {
		switch {
		case !settings.VatRegistered && l.taxed.Category != "O":
			return issuePlan{}, cannotIssue(codeVatNotRegistered, fmt.Sprintf(
				"The seller is not VAT-registered, so every line is outside the VAT act (category O); line %d is not.", l.position)), nil
		case settings.VatRegistered && l.taxed.Category == "O":
			return issuePlan{}, cannotIssue(codeCategoryONotAllowed, fmt.Sprintf(
				"The seller is VAT-registered, so no line may be outside the VAT act (category O); line %d is.", l.position)), nil
		case l.taxed.Category == "AE" && plan.buyer.BuyerOrganisationNumber == nil:
			return issuePlan{}, cannotIssue(codeReverseChargeNeedsOrgNr, fmt.Sprintf(
				"Line %d is reverse charge, which needs the buyer's organisation number (§ 5-1-1).", l.position)), nil
		}
	}
	return plan, nil, nil
}

// PostInvoicesByIdIssue Issue a draft
// (POST /api/v1/invoices/{id}/issue)
func (s *server) PostInvoicesByIdIssue(ctx context.Context, req gen.PostInvoicesByIdIssueRequestObject) (gen.PostInvoicesByIdIssueResponseObject, error) {
	out, err := s.issueDraft(ctx, req.Id, req.Body.IssueDate)
	switch {
	case err != nil:
		return nil, err
	case out.notFound:
		return gen.PostInvoicesByIdIssue404Response{}, nil
	case out.unavailable != nil:
		return gen.PostInvoicesByIdIssue503ApplicationProblemPlusJSONResponse(*out.unavailable), nil
	case out.refusal != nil:
		return gen.PostInvoicesByIdIssue409ApplicationProblemPlusJSONResponse(*out.refusal), nil
	}
	// useCredit (invoices phase 6 design I11): the customer's credit applied
	// after the issue committed, never inside it, by an issuer who may apply
	// credit (invoices:payments); a failure, or an issuer who may not, is a
	// warning.
	var warnings []string
	if req.Body.UseCredit != nil && *req.Body.UseCredit && out.doc.Kind == kindInvoice {
		if s.has(ctx, "invoices:payments") {
			warnings = s.useCreditAfterIssue(ctx, out.doc)
		} else {
			warnings = []string{warningCreditNotApplied}
		}
	}
	resp, err := s.invoiceResponse(ctx, store.New(s.deps.Pool), out.doc, nil, out.loc)
	if err != nil {
		return nil, err
	}
	if len(warnings) > 0 {
		resp.Warnings = append(resp.Warnings, warnings...)
	}
	s.withIssueWarnings(ctx, out, &resp)
	return gen.PostInvoicesByIdIssue200JSONResponse(resp), nil
}

// issueOutcome is what the issue's core made of a draft: the issued document,
// its PDF stored (or pdfStored saying not), with the business time zone its
// response is built with — or the refusal (a 409), the object store missing
// (a 503, storage_unavailable) or the draft gone (a 404). Exactly one holds.
type issueOutcome struct {
	doc         store.InvoicesInvoice
	loc         *time.Location
	profile     *contracts.CustomerBillingProfile
	refusal     *gen.InvoicesConflictProblem
	unavailable *gen.InvoicesConflictProblem
	notFound    bool
}

// issueDraft is the issue's core — its reads, issueLocked under the
// document's lock and storeAfterIssue after the commit — and the handler's
// and the invoices-billing worker's one code path (billing runs plan reading
// 6). Its actor is the principal on ctx, which callerID reads: the issuer,
// or the run's creator (readings on the record 10). issueDate is the
// request's, nil for today.
func (s *server) issueDraft(ctx context.Context, id int64, issueDate *openapi_types.Date) (issueOutcome, error) {
	q := store.New(s.deps.Pool)
	draft, err := q.GetInvoice(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return issueOutcome{notFound: true}, nil
	}
	if err != nil {
		return issueOutcome{}, fmt.Errorf("invoices: read document %d: %w", id, err)
	}
	if draft.Status != statusDraft {
		return issueOutcome{refusal: ptr(invoiceIssued())}, nil
	}
	if !s.storageConfigured {
		status := int32(503)
		code, title := codeStorageUnavailable, storageUnavailableTitle
		detail := "This installation has no object store, so an issued document's PDF could never be stored. Nothing was issued."
		return issueOutcome{unavailable: &gen.InvoicesConflictProblem{
			Code: &code, Title: &title, Detail: &detail, Status: &status,
		}}, nil
	}
	var profile *contracts.CustomerBillingProfile
	var work issueWork
	var refusal *gen.InvoicesConflictProblem
	switch draft.Kind {
	case kindInvoice:
		if profile, err = s.customerProfile(ctx, draft.CustomerID); err != nil {
			return issueOutcome{}, err
		}
		// The work the invoice bills: its projects and the issuer's name are
		// read now, never under the lock (invoices work design D1).
		if work, refusal, err = s.readIssueWork(ctx, q, draft); err != nil {
			return issueOutcome{}, err
		}
		if refusal != nil {
			return issueOutcome{refusal: refusal}, nil
		}
	case kindCreditNote:
		// The work the credit note may release: the holders and the
		// issuer's name, read now when its original bills any (D8).
		if work, err = s.readReleaseWork(ctx, q, draft); err != nil {
			return issueOutcome{}, err
		}
		// Whether its buyer is bookkeeping-obligated, for the 2028 rule
		// (buyer_rule.go): read for nothing else, and only when the buyer
		// snapshot it carries has no organisation number — the rule cannot
		// refuse one that has.
		if draft.BuyerOrganisationNumber == nil || *draft.BuyerOrganisationNumber == "" {
			if profile, err = s.customerProfile(ctx, draft.CustomerID); err != nil {
				return issueOutcome{}, err
			}
		}
	}
	// The business time zone the issue and due dates are taken in, read
	// before the lock as every call out of the module is.
	loc, err := s.businessZone(ctx)
	if err != nil {
		return issueOutcome{}, err
	}
	// The logo's object, asked of the store before the lock: one the store
	// does not hold is not snapshotted (goneLogo).
	work.goneLogo = s.goneLogo(ctx, q)
	if issueBeforeLock != nil {
		issueBeforeLock(ctx, draft.ID)
	}

	var issued store.InvoicesInvoice
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		// 1. The document, FOR UPDATE: two issues of one draft queue here.
		locked, err := txq.LockInvoice(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDocumentGone
		}
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", id, err)
		}
		if locked.Status != statusDraft {
			refusal = ptr(invoiceIssued())
			return errRefused
		}
		issued, refusal, err = s.issueLocked(ctx, tx, txq, locked, draft, profile, work, issueDate, false, loc)
		if err != nil {
			return err
		}
		if refusal != nil {
			return errRefused
		}
		return nil
	})
	if refusal != nil {
		return issueOutcome{refusal: refusal}, nil
	}
	if errors.Is(err, errDocumentGone) {
		return issueOutcome{notFound: true}, nil
	}
	if err != nil {
		return issueOutcome{}, err
	}
	// After the commit, the PDF is stored once (D7). A failure there is
	// logged and never fails the issue; pdfStored says so. A credit note's
	// surplus then moves to the customer's credit in a transaction of its
	// own (invoices phase 6 design I10), under the credit's lock order — the
	// issue's own is untouched — and a failure there is logged too.
	doc := s.storeAfterIssue(ctx, q, issued)
	s.moveAfterIssue(ctx, doc)
	return issueOutcome{doc: doc, loc: loc, profile: profile}, nil
}

// issueLocked runs the issue's steps 2–6 on a document the transaction tx
// already holds — locked by LockInvoice, or inserted by the quick invoice on
// the same transaction, so the order stays the document, the settings row,
// the counter: the merge check, the settings FOR SHARE, the number, the
// checks, the work's write-back and the writes. draft is the document as it
// was read before the transaction, with the billing profile and the work read
// then. It answers the issued document, or the refusal that must roll the
// transaction back, or an error. issueAfterAllocation runs here. Last, an
// invoice gets its pay link when pay links are available (D15) — when
// handedOver, the quick invoice's, only when on-site payment is too (I5).
// loc is the business time zone, read before the transaction: the issue's
// today is the day its clock read falls on there.
func (s *server) issueLocked(ctx context.Context, tx pgx.Tx, txq *store.Queries, locked store.InvoicesInvoice,
	draft store.InvoicesInvoice, profile *contracts.CustomerBillingProfile, work issueWork, issueDateReq *openapi_types.Date, handedOver bool,
	loc *time.Location,
) (store.InvoicesInvoice, *gen.InvoicesConflictProblem, error) {
	var refusal *gen.InvoicesConflictProblem
	refused := func() (store.InvoicesInvoice, *gen.InvoicesConflictProblem, error) {
		return store.InvoicesInvoice{}, refusal, nil
	}
	failed := func(err error) (store.InvoicesInvoice, *gen.InvoicesConflictProblem, error) {
		return store.InvoicesInvoice{}, nil, err
	}
	// 2. A merge may have re-pointed the draft since the profile was read.
	if locked.Kind == kindInvoice && locked.CustomerID != draft.CustomerID {
		return store.InvoicesInvoice{}, ptr(conflict(codeInvoiceChanged, cannotIssueTitle, "The invoice changed; try again.")), nil
	}
	// 3. The settings row FOR SHARE: the seller snapshot and the start.
	settings, err := txq.ShareSettings(ctx)
	if err != nil {
		return failed(fmt.Errorf("invoices: share the settings: %w", err))
	}
	// 4. The number.
	number, err := txq.AllocateNumber(ctx, settings.SeriesStart)
	if err != nil {
		return failed(fmt.Errorf("invoices: allocate a number: %w", err))
	}
	if issueAfterAllocation != nil {
		if err := issueAfterAllocation(ctx, tx, locked.ID); err != nil {
			return failed(err)
		}
	}
	// "Today" is read only now, after the counter: an issue that waited
	// on it across midnight is dated the day it is issued, the day
	// its issued_at falls on. The issue's clock is read here once: today,
	// every stamp's time and the document's issued_at, so the issue date
	// and issued_at never straddle midnight.
	now := s.deps.Clock()
	today := holidays.DayIn(now, loc)
	issueDate := today
	if issueDateReq != nil {
		issueDate = utcDay(issueDateReq.Time)
	}
	// 5. Only now, the checks.
	lines, err := txq.Lines(ctx, locked.ID)
	if err != nil {
		return failed(fmt.Errorf("invoices: read document %d's lines: %w", locked.ID, err))
	}
	deliveryEnd := deliveryEndOf(locked)
	latest, err := txq.LatestIssueDate(ctx)
	if err != nil {
		return failed(fmt.Errorf("invoices: read the latest issue date: %w", err))
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
		return refused()
	}
	var plan issuePlan
	switch locked.Kind {
	case kindInvoice:
		plan, refusal, err = invoiceIssueChecks(ctx, txq, profile, settings, issueDate, locked, lines)
	case kindCreditNote:
		plan, refusal, err = creditIssueChecks(ctx, txq, locked, lines)
		// A credit note is a sales document too: the 2028 buyer rule on its
		// buyer snapshot, the original's (buyer_rule.go).
		if err == nil && refusal == nil {
			refusal, _ = buyerOrgNumberRule(profile, locked.BuyerOrganisationNumber, issueDate)
		}
	default:
		return failed(fmt.Errorf("invoices: document %d is a %s, which this module cannot issue", locked.ID, locked.Kind))
	}
	if err != nil {
		return failed(err)
	}
	if refusal != nil {
		return refused()
	}
	// The rate (invoices phase 6 design I19, I22): an invoice in currency
	// needs the IBAN and BIC it is paid to, and its automatic rate is
	// resolved again for the issue date — refused without one within 7 days
	// — where an agreed one stands; a credit note keeps its original's.
	rate, refusal, err := issueRate(ctx, txq, locked, settings, issueDate)
	if err != nil {
		return failed(err)
	}
	if refusal != nil {
		return refused()
	}
	exchangeRate := rate.Rate
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
		return refused()
	}
	// A settlement whose gross is not positive could never be credited,
	// nor the a-kontos it deducted (invoices work design D7).
	if plan.settlement && totals.Gross.Sign() <= 0 {
		refusal = cannotIssue(codeInvoiceTotalNotPositive, fmt.Sprintf(
			"This settlement's total is %s: a settlement deducts less than it bills. A fixed price billed in full on account ends with its last a-konto.",
			totals.Gross.FloatString(2)))
		return refused()
	}
	// Nor may an invoice whose energy corrections lower it below what it
	// bills (invoices from consumption plan reading 3): a credit is a
	// credit note's.
	if plan.correction && totals.Gross.Sign() <= 0 {
		refusal = cannotIssue(codeInvoiceTotalNotPositive, fmt.Sprintf(
			"This invoice's total is %s: its corrections lower it below zero. Bill the corrections together with consumption, or credit the earlier invoice.",
			totals.Gross.FloatString(2)))
		return refused()
	}
	// An invoice under the KID agreement gets its KID from the number
	// just allocated (EHF and KID design D3); a number the agreement no
	// longer fits refuses the issue, and the number rolls back with it.
	var documentKid, kidAlgorithm *string
	if locked.Kind == kindInvoice && settings.KidLength != nil && settings.KidAlgorithm != nil {
		// Once AvtaleGiro has been on, the customer KIDs own the first
		// digit for good (phase 6 reading 12): the number is capped one
		// digit lower, AvtaleGiro on or off, so no invoice KID ever meets a
		// customer KID handed out.
		compute := kid.Compute
		if settings.KidSpaceReserved {
			compute = kid.ComputeReserved
		}
		k, err := compute(number, int(*settings.KidLength), *settings.KidAlgorithm)
		if errors.Is(err, kid.ErrLengthExceeded) {
			refusal = cannotIssue(codeKidLengthExceeded, fmt.Sprintf(
				"Number %d does not fit the KID agreement's %d digits; the agreement was shortened. Change it in the settings.",
				number, *settings.KidLength))
			return refused()
		}
		if err != nil {
			return failed(fmt.Errorf("invoices: document %d's KID: %w", locked.ID, err))
		}
		documentKid, kidAlgorithm = &k, settings.KidAlgorithm
	}

	// The work, last of the checks and first of the writes: the holders
	// stamp an invoice's invoiced on this transaction (D1, rule 10), and
	// take back the stamps of what a credit note releases (D8) — before
	// anything of the document is written.
	switch locked.Kind {
	case kindInvoice:
		ref := contracts.InvoiceRef{
			ID: locked.ID, Number: number, IssueDate: issueDate, IssuedAt: now,
			IssuedBy: callerID(ctx), IssuedByDisplay: work.display,
		}
		if refusal, err = s.markIssueWork(ctx, tx, txq, work, ref); err != nil {
			return failed(err)
		}
		if refusal != nil {
			return refused()
		}
	case kindCreditNote:
		// The stamp taken back is the original's: its id, number and
		// date; who took it back, and when, is this issue's.
		if len(plan.releases) > 0 {
			o := plan.original
			if o.Number == nil {
				return failed(fmt.Errorf("invoices: credit note %d's original %d has no number", locked.ID, o.ID))
			}
			ref := contracts.InvoiceRef{
				ID: o.ID, Number: *o.Number, IssueDate: o.IssueDate.Time, IssuedAt: now,
				IssuedBy: callerID(ctx), IssuedByDisplay: work.display,
			}
			if err := s.releaseCreditWork(ctx, tx, txq, locked.ID, work, plan.releases, ref); err != nil {
				return failed(err)
			}
		}
	}

	// 6. The writes: the lines' snapshots, the summaries, and last the
	// row itself — the trigger refuses line writes under an issued one. Each
	// line's net in NOK carries its row's remainder (I21).
	netsNOK := netsNOKOf(taxed, rows, exchangeRate)
	for i, l := range plan.lines {
		if a := l.amounts; a != nil {
			p := store.SquareCreditLineParams{ID: l.id}
			for _, c := range []struct {
				dst *pgtype.Numeric
				v   *big.Rat
			}{{&p.LineGross, a.Gross}, {&p.LineAllowance, a.Allowance}, {&p.LineNet, a.Net}} {
				if *c.dst, err = numericFromRat(c.v, 2); err != nil {
					return failed(err)
				}
			}
			if err := txq.SquareCreditLine(ctx, p); err != nil {
				return failed(fmt.Errorf("invoices: square line %d: %w", l.id, err))
			}
		}
		vatRate, err := numericFromRat(l.taxed.Rate, 2)
		if err != nil {
			return failed(err)
		}
		netNOK, err := numericFromRat(netsNOK[i], 2)
		if err != nil {
			return failed(err)
		}
		if err := txq.SnapshotLine(ctx, store.SnapshotLineParams{
			ID: l.id, VatRatePercent: vatRate, VatCategory: &l.taxed.Category, SafTCode: &l.taxed.SAFT,
			ExemptionReason: l.taxed.Reason, LineNetNok: netNOK,
		}); err != nil {
			return failed(fmt.Errorf("invoices: snapshot line %d: %w", l.id, err))
		}
	}
	for _, r := range rows {
		p := store.InsertVatSummaryParams{InvoiceID: locked.ID, VatCategory: r.Category, SafTCode: r.SAFT, ExemptionReason: r.Reason}
		for _, c := range []struct {
			dst *pgtype.Numeric
			v   *big.Rat
		}{{&p.RatePercent, r.Rate}, {&p.TaxableAmount, r.Taxable}, {&p.VatAmount, r.VAT}, {&p.VatAmountNok, r.VATNOK}, {&p.TaxableAmountNok, r.BaseNOK}} {
			if *c.dst, err = numericFromRat(c.v, 2); err != nil {
				return failed(err)
			}
		}
		if err := txq.InsertVatSummary(ctx, p); err != nil {
			return failed(fmt.Errorf("invoices: write document %d's VAT: %w", locked.ID, err))
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
	if work.goneLogo != "" && params.SellerLogoObjectKey != nil && *params.SellerLogoObjectKey == work.goneLogo {
		params.SellerLogoObjectKey, params.SellerLogoSha256 = nil, nil
	}
	params.Kid, params.KidAlgorithm = documentKid, kidAlgorithm
	if params.ExchangeRate, err = numericFromRat(rate.Rate, ratesMaxDecimals); err != nil {
		return failed(err)
	}
	params.ExchangeRateDate, params.ExchangeRateSource = pgDate(rate.Day), rate.Source
	// A person's invoice while AvtaleGiro is on prints the customer KID the
	// payer creates the mandate with (phase 6 reading 11), frozen here.
	if documentKid != nil && settings.AvtalegiroEnabled && s.efakturaSwitchedOn() && params.BuyerType != nil && *params.BuyerType == "person" &&
		params.BuyerCustomerNumber != nil {
		if ck, err := kid.CustomerKID(*params.BuyerCustomerNumber, int(*settings.KidLength), *settings.KidAlgorithm); err == nil {
			params.BuyerAvtalegiroKid = &ck
		}
	}
	if params.NetTotal, params.VatTotal, params.GrossTotal, params.VatTotalNok, err = numerics(totals); err != nil {
		return failed(err)
	}
	params.Now, params.IssuedByUserID = now, ptr(callerID(ctx))
	if plan.buyer == nil {
		// A credit note keeps the buyer snapshot its draft copied.
		keepBuyer(&params, locked)
	}
	issued, err := txq.IssueDocument(ctx, params)
	if err != nil {
		return failed(fmt.Errorf("invoices: issue document %d: %w", locked.ID, err))
	}
	// A credit note that brings its foreign original to zero open in the
	// currency leaves the NOK rest of its rounding (review M1): written as
	// an exchange difference, never bent into the credit note's own VAT.
	if issued.Kind == kindCreditNote && issued.Currency != "NOK" && issued.CreditsInvoiceID != nil {
		if err := closingDifference(ctx, txq, *issued.CreditsInvoiceID, issued.ID, issueDate, now); err != nil {
			return failed(err)
		}
	}
	// The document's event, on the issue's transaction and under its lock
	// (accounting design A12): it commits with the document or not at all.
	// contracts.SalesJournal is what a reader posts from; this only wakes it.
	gross, err := ratFromNumeric(issued.GrossTotal)
	if err != nil {
		return failed(err)
	}
	if err := events.Publish(ctx, tx, "invoices", contracts.DocumentIssued{
		DocumentID: issued.ID, Number: number, Kind: issued.Kind, CustomerID: issued.CustomerID,
		IssueDate: issueDate.Format(time.DateOnly), GrossTotal: gross.FloatString(2), Currency: issued.Currency,
	}, now); err != nil {
		return failed(err)
	}
	// 7. The pay link, on the document just issued (D15).
	if err := s.payLinkAtIssue(ctx, tx, txq, issued, handedOver); err != nil {
		return failed(err)
	}
	// A credit note asks its invoice's live AvtaleGiro claim to be
	// cancelled (phase 6 reading 15), on this transaction; the
	// invoices-efaktura worker carries it to the provider. A paid claim is
	// left as it is.
	if issued.Kind == kindCreditNote && issued.CreditsInvoiceID != nil {
		asked, err := txq.RequestClaimCancel(ctx, store.RequestClaimCancelParams{Now: now, InvoiceID: *issued.CreditsInvoiceID})
		if err != nil {
			return failed(fmt.Errorf("invoices: ask credit note %d's claim to cancel: %w", issued.ID, err))
		}
		for _, c := range asked {
			if err := publishClaimChanged(ctx, tx, c, c.Status, now); err != nil {
				return failed(err)
			}
		}
	}

	return issued, nil, nil
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
// (D4), the phone and the logo's key and hash with it (invoices phase 6
// design I2, I4). An optional field that is not set is stored as NULL.
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
	p.SellerPhone = optional(s.Phone)
	p.SellerLogoObjectKey, p.SellerLogoSha256 = s.LogoObjectKey, s.LogoSha256
}

// keepBuyer carries a document's own buyer snapshot through the issue.
func keepBuyer(p *store.IssueDocumentParams, inv store.InvoicesInvoice) {
	p.BuyerCustomerNumber, p.BuyerType, p.BuyerName = inv.BuyerCustomerNumber, inv.BuyerType, inv.BuyerName
	p.BuyerOrganisationNumber, p.BuyerForeignID = inv.BuyerOrganisationNumber, inv.BuyerForeignID
	p.BuyerVatRegistered = inv.BuyerVatRegistered
	p.BuyerAddressLine1, p.BuyerAddressLine2 = inv.BuyerAddressLine1, inv.BuyerAddressLine2
	p.BuyerPostalCode, p.BuyerCity, p.BuyerRegion, p.BuyerCountry = inv.BuyerPostalCode, inv.BuyerCity, inv.BuyerRegion, inv.BuyerCountry
	p.BuyerPeppolID, p.BuyerGln, p.BuyerLanguage = inv.BuyerPeppolID, inv.BuyerGln, inv.BuyerLanguage
}

// issueRate is the rate a document is issued at (invoices phase 6 design
// I19, I22; plan C5), read under the issue's lock: NOK's 1 of the issue
// date; a credit note's original's, which its draft copied; an invoice's
// agreed rate as it stands, or Norges Bank's resolved again for the issue
// date. An invoice in currency is refused iban_required without the
// seller's IBAN and BIC, and exchange_rate_missing without an automatic rate
// within 7 days. Turning a currency off leaves its drafts issuable (§11).
func issueRate(ctx context.Context, txq *store.Queries, doc store.InvoicesInvoice, settings store.InvoicesSetting, issueDate time.Time) (resolvedRate, *gen.InvoicesConflictProblem, error) {
	stored, err := ratFromNumeric(doc.ExchangeRate)
	if err != nil {
		return resolvedRate{}, nil, err
	}
	day := issueDate
	if doc.ExchangeRateDate.Valid {
		day = doc.ExchangeRateDate.Time
	}
	switch {
	case doc.Kind == kindCreditNote:
		return resolvedRate{Rate: stored, Day: day, Source: doc.ExchangeRateSource}, nil, nil
	case doc.Currency == "NOK":
		return resolvedRate{Rate: big.NewRat(1, 1), Day: issueDate, Source: rateSourceNOK}, nil, nil
	case settings.Iban == "" || settings.Bic == "":
		return resolvedRate{}, ptr(conflict(codeIbanRequired, cannotIssueTitle,
			"An invoice in another currency than NOK is paid to the IBAN and BIC: fill in both in the settings first.")), nil
	case doc.ExchangeRateSource == rateSourceManual:
		return resolvedRate{Rate: stored, Day: day, Source: rateSourceManual}, nil, nil
	}
	r, ok, err := resolveRate(ctx, txq, doc.Currency, issueDate)
	if err != nil {
		return resolvedRate{}, nil, err
	}
	if !ok {
		refusal := conflict(codeExchangeRateMissing, cannotIssueTitle, fmt.Sprintf(
			"No rate of %s on %s or the 7 days before it: fetch the rates, or enter one in the settings or on the draft.",
			doc.Currency, issueDate.Format(time.DateOnly)))
		refusal.Currency = ptr(doc.Currency)
		return resolvedRate{}, &refusal, nil
	}
	// The source the day's row has: manual when a manager's rate replaced
	// Norges Bank's for it, so the PDF says avtalt kurs and the export manual.
	return resolvedRate{Rate: r.Rate, Day: r.Day, Source: r.Source}, nil, nil
}
