package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the draft (D4): created, replaced whole and deleted while it is
// a draft, and never again once issued. A draft is not a salgsdokument — it
// has no number, and its totals are computed with today's rates only so the
// person editing it sees them; the issue computes them again for the issue
// date. A credit-note draft is the exception: it is totalled at its original
// lines' snapshot rates (credits.go).

// The document's kinds and statuses, as the columns hold them.
const (
	kindInvoice    = "invoice"
	kindCreditNote = "credit_note"
	statusDraft    = "draft"
	statusIssued   = "issued"
)

// The codes and titles of the drafts' 409s.
const (
	codeInvoiceIssued    = "invoice_issued"
	codeCustomerMerged   = "customer_merged"
	codeCustomerArchived = "customer_archived"
	codeCustomerBlocked  = "customer_blocked"
	codeCustomerMissing  = "customer_missing"

	invoiceIssuedTitle  = "The document is issued"
	customerGateTitle   = "The customer cannot be invoiced"
	invalidInvoiceTitle = "Invalid invoice"
)

// maxLines is how many lines one document may carry (D5).
const maxLines = 500

// invoiceIssued is the answer to every edit and delete of an issued document.
func invoiceIssued() gen.InvoicesConflictProblem {
	return conflict(codeInvoiceIssued, invoiceIssuedTitle,
		"An issued document is never changed or deleted. Correct it with a credit note.")
}

// customerGate is D4's customer gates on creating and saving an invoice draft,
// in their order, from the billing profile: merged away (carrying where to),
// archived (an anonymised person is archived), disabled — "blocked for
// invoicing" — and, what cannot happen since customers are never deleted, no
// profile at all. nil when the customer may be invoiced. A credit note never
// passes through here (D8).
func customerGate(p *contracts.CustomerBillingProfile) *gen.InvoicesConflictProblem {
	switch {
	case p == nil:
		return ptr(conflict(codeCustomerMissing, customerGateTitle, "No customer has this id."))
	case p.MergedInto != nil:
		c := conflict(codeCustomerMerged, customerGateTitle, fmt.Sprintf(
			"This customer was merged into customer %d; invoice that one instead.", *p.MergedInto))
		c.MergedInto = p.MergedInto
		return &c
	case p.Status == "archived":
		return ptr(conflict(codeCustomerArchived, customerGateTitle,
			"This customer is archived, and an archived customer is not invoiced."))
	case p.Status == "disabled":
		return ptr(conflict(codeCustomerBlocked, customerGateTitle,
			"This customer is blocked for invoicing."))
	}
	return nil
}

// draftLine is one validated line of a request.
type draftLine struct {
	description, unit             string
	quantity, unitPrice, discount *big.Rat
	vatCodeID                     int32
	creditsLineID                 *int64
	amounts                       lineAmounts
	// sources is the work the line bills, by identity (invoices work design
	// D2); sourcesGiven tells [] (none) from a request that left the field
	// out.
	sources      []sourceRef
	sourcesGiven bool
	// deductsInvoiceID makes the line a settlement's deduction of an earlier
	// invoice (invoices work design D7), and snapshot is that invoice's line's
	// VAT treatment at the line's code, which taxedLines taxes it at. On a
	// credit note's line both are the original line's.
	deductsInvoiceID *int64
	snapshot         *taxedLine
}

// draftInput is one validated draft body.
type draftInput struct {
	customerID                             int32
	paymentTermsDays                       *int32
	deliveryDate, deliveryFrom, deliveryTo pgtype.Date
	address                                *gen.InvoicesDeliveryAddress
	yourReference                          *string
	ourReference, orderReference           string
	note, internalNote                     string
	lines                                  []draftLine
	// refresh is what refreshSources read before the save's transaction, nil
	// without it.
	refresh *sourcesRefresh
	// projectCodes is what the save's derived project may need from the
	// project directory, read before its transaction (saveProjectCodes).
	projectCodes map[int32]string
}

// amount is one number of a line: finite, at least minimum (above it when
// strict), at most places decimals, and within the column. It answers the
// exact decimal and the message to report, "" when it holds.
func amount(label string, v float64, places int, minimum *big.Rat, strict bool, maximum *big.Rat) (*big.Rat, string) {
	if !finite(v) {
		return nil, label + " is a number"
	}
	if decimalPlaces(v) > places {
		return nil, fmt.Sprintf("%s has at most %d decimals", label, places)
	}
	r := ratFromFloat(v)
	switch c := r.Cmp(minimum); {
	case strict && c <= 0:
		return nil, fmt.Sprintf("%s is greater than %s", label, minimum.FloatString(0))
	case c < 0:
		return nil, fmt.Sprintf("%s is %s or more", label, minimum.FloatString(0))
	}
	if r.Cmp(maximum) > 0 {
		return nil, fmt.Sprintf("%s is at most %s", label, maximum.FloatString(places))
	}
	return r, ""
}

// The columns' own ceilings: quantity numeric(12,3), unit_price
// numeric(14,4), discount_percent 0-100.
var (
	maxQuantity  = mustRat("999999999.999")
	maxUnitPrice = mustRat("9999999999.9999")
	zero         = new(big.Rat)
)

// optionalText is a trimmed optional field, "" when absent.
func optionalText(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

// parseDraft runs D4's and D5's field rules over a draft body, every failure
// collected. currency is the one this installation invoices in (NOK in phase
// 1). The VAT codes and the bounds on computed amounts are checked afterwards,
// against the codes (checkLines). A negative quantity is accepted here
// provisionally: whether a line may carry one — a deduction line, or a credit
// note's line crediting one (invoices work design D7) — is decided once the
// line's kind is known (deductionRules, putCreditDraft).
func parseDraft(body gen.InvoicesInvoiceRequest, currency string) (draftInput, map[string][]string) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}
	in := draftInput{
		customerID:       body.CustomerId,
		paymentTermsDays: body.PaymentTermsDays,
		ourReference:     optionalText(body.OurReference),
		orderReference:   optionalText(body.OrderReference),
		note:             optionalText(body.Note),
		internalNote:     optionalText(body.InternalNote),
	}
	if body.YourReference != nil {
		in.yourReference = ptr(strings.TrimSpace(*body.YourReference))
		add("yourReference", maxLength("A reference", *in.yourReference, 100))
	}
	if body.CustomerId <= 0 {
		add("customerId", "A document needs a customer")
	}
	if body.Currency != nil && strings.ToUpper(strings.TrimSpace(*body.Currency)) != currency {
		add("currency", onlyNOK)
	}
	if t := body.PaymentTermsDays; t != nil && (*t < 0 || *t > 365) {
		add("paymentTermsDays", "Payment terms are between 0 and 365 days")
	}
	add("ourReference", maxLength("A reference", in.ourReference, 100))
	add("orderReference", maxLength("A reference", in.orderReference, 100))
	add("note", maxLength("The note", in.note, 1000))
	add("internalNote", maxLength("The internal note", in.internalNote, 1000))

	// Delivery (§ 5-1-1 nr. 4): a day, a period with from on or before to,
	// or — on a draft only — nothing.
	date := func(d *openapi_types.Date) pgtype.Date {
		if d == nil {
			return pgtype.Date{}
		}
		return pgDate(utcDay(d.Time))
	}
	in.deliveryDate, in.deliveryFrom, in.deliveryTo = date(body.DeliveryDate), date(body.DeliveryFrom), date(body.DeliveryTo)
	switch {
	case in.deliveryDate.Valid && (in.deliveryFrom.Valid || in.deliveryTo.Valid):
		add("deliveryDate", "A delivery is a day or a period, not both")
	case in.deliveryFrom.Valid != in.deliveryTo.Valid:
		add("deliveryTo", "A delivery period has both a first and a last day")
	case in.deliveryFrom.Valid && in.deliveryFrom.Time.After(in.deliveryTo.Time):
		add("deliveryTo", "A delivery period ends on or after the day it starts")
	}
	if a := body.DeliveryAddress; a != nil {
		addr := gen.InvoicesDeliveryAddress{
			Line1: strings.TrimSpace(a.Line1), City: strings.TrimSpace(a.City),
			Country: strings.ToUpper(strings.TrimSpace(a.Country)),
		}
		if v := optionalText(a.Line2); v != "" {
			addr.Line2 = &v
		}
		if v := optionalText(a.PostalCode); v != "" {
			addr.PostalCode = &v
		}
		if addr.Line1 == "" {
			add("deliveryAddress.line1", "A place of delivery needs an address line")
		}
		add("deliveryAddress.line1", maxLength("An address line", addr.Line1, 200))
		add("deliveryAddress.line2", maxLength("An address line", optionalText(addr.Line2), 200))
		add("deliveryAddress.postalCode", maxLength("A postal code", optionalText(addr.PostalCode), 20))
		if addr.City == "" {
			add("deliveryAddress.city", "A place of delivery needs a city")
		}
		add("deliveryAddress.city", maxLength("A city", addr.City, 100))
		if !validCountry(addr.Country) {
			add("deliveryAddress.country", "A country is a two-letter ISO 3166-1 code, such as NO")
		}
		in.address = &addr
	}

	if len(body.Lines) > maxLines {
		add("lines", fmt.Sprintf("At most %d lines", maxLines))
		return in, errs
	}
	// A save can add no work, so its bound is its body's (D2): at most as
	// many identities as one billable read answers, before any read.
	named := 0
	for _, l := range body.Lines {
		if l.Sources != nil {
			named += len(*l.Sources)
		}
	}
	if named > contracts.MaxBillableRows {
		add("lines", fmt.Sprintf("At most %d sources on one document", contracts.MaxBillableRows))
		return in, errs
	}
	for i, l := range body.Lines {
		field := func(name string) string { return fmt.Sprintf("lines[%d].%s", i, name) }
		line := draftLine{
			description: strings.TrimSpace(l.Description), unit: optionalText(l.Unit),
			vatCodeID: l.VatCodeId, creditsLineID: l.CreditsLineId, sourcesGiven: l.Sources != nil,
			deductsInvoiceID: l.DeductsInvoiceId,
		}
		var sourceErrs map[string][]string
		line.sources, sourceErrs = parseLineSources(i, l)
		for field, msgs := range sourceErrs {
			for _, msg := range msgs {
				add(field, msg)
			}
		}
		if line.description == "" {
			add(field("description"), "A line needs a description")
		}
		add(field("description"), maxLength("A description", line.description, 500))
		add(field("unit"), maxLength("A unit", line.unit, 20))
		var msg string
		if l.Quantity < 0 {
			line.quantity, msg = amount("A quantity", -l.Quantity, 3, zero, true, maxQuantity)
			if line.quantity != nil {
				line.quantity.Neg(line.quantity)
			}
		} else {
			line.quantity, msg = amount("A quantity", l.Quantity, 3, zero, true, maxQuantity)
		}
		add(field("quantity"), msg)
		line.unitPrice, msg = amount("A unit price", l.UnitPrice, 4, zero, false, maxUnitPrice)
		add(field("unitPrice"), msg)
		discount := 0.0
		if l.DiscountPercent != nil {
			discount = *l.DiscountPercent
		}
		line.discount, msg = amount("A discount", discount, 2, zero, false, big.NewRat(100, 1))
		add(field("discountPercent"), msg)
		if line.quantity != nil && line.unitPrice != nil && line.discount != nil {
			line.amounts = computeLine(line.quantity, line.unitPrice, line.discount)
			if new(big.Rat).Abs(line.amounts.gross).Cmp(maxLineGross) > 0 {
				add(field("unitPrice"), "The line amount is too large")
			}
		}
		in.lines = append(in.lines, line)
	}
	return in, errs
}

// vatCodeOnDay is one code as a draft line is judged against it.
type vatCodeOnDay struct {
	active         bool
	code           string
	category, safT string
	reason         *string
	rate           *big.Rat // nil when no period covers the day
}

// vatCodesOn is every code with the rate of the period covering day.
func vatCodesOn(ctx context.Context, q *store.Queries, day pgtype.Date) (map[int32]vatCodeOnDay, error) {
	rows, err := q.VatCodesOnDay(ctx, day)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the VAT codes: %w", err)
	}
	out := make(map[int32]vatCodeOnDay, len(rows))
	for _, r := range rows {
		c := vatCodeOnDay{active: r.Active, code: r.Code, category: r.EhfCategory, safT: r.SafTCode, reason: r.ExemptionReason}
		if r.RatePercent.Valid {
			if c.rate, err = ratFromNumeric(r.RatePercent); err != nil {
				return nil, err
			}
		}
		out[r.ID] = c
	}
	return out, nil
}

// taxedLines are the lines as today's rates would tax them: a draft's preview
// (D4). A code with no period covering today counts at 0; the issue refuses it
// (vat_code_not_valid). A deduction line is taxed at its a-konto line's
// snapshot instead, never at today's rate of its code (invoices work design
// D7).
func taxedLines(lines []draftLine, codes map[int32]vatCodeOnDay) []taxedLine {
	out := make([]taxedLine, 0, len(lines))
	for _, l := range lines {
		if s := l.snapshot; s != nil {
			out = append(out, taxedLine{net: l.amounts.net, category: s.category, rate: s.rate, safT: s.safT, reason: s.reason})
			continue
		}
		c := codes[l.vatCodeID]
		rate := c.rate
		if rate == nil {
			rate = new(big.Rat)
		}
		out = append(out, taxedLine{net: l.amounts.net, category: c.category, rate: rate, safT: c.safT, reason: c.reason})
	}
	return out
}

// checkInvoiceLines is an invoice draft's VAT codes (known and active, D3) and
// its total bound (D5), added to errs. A deduction line's code need not be
// active: it is taxed at its a-konto line's snapshot (invoices work design D7).
func checkInvoiceLines(lines []draftLine, codes map[int32]vatCodeOnDay, errs map[string][]string) map[string][]string {
	for i, l := range lines {
		switch c, ok := codes[l.vatCodeID]; {
		case !ok:
			errs = withFieldError(errs, fmt.Sprintf("lines[%d].vatCodeId", i), "No VAT code has this id")
		case !c.active && l.deductsInvoiceID == nil:
			errs = withFieldError(errs, fmt.Sprintf("lines[%d].vatCodeId", i), "This VAT code is no longer offered for new lines")
		}
		if l.creditsLineID != nil {
			errs = withFieldError(errs, fmt.Sprintf("lines[%d].creditsLineId", i), "Only a credit note's line credits another line")
		}
	}
	return errs
}

// checkTotal is D5's document bound, on the totals the lines make.
func checkTotal(totals documentTotals, errs map[string][]string) map[string][]string {
	if totals.gross.Cmp(maxGrossTotal) > 0 {
		errs = withFieldError(errs, "lines", "The document total is too large")
	}
	return errs
}

// numerics is a document's four totals as the columns take them.
func numerics(t documentTotals) (net, vat, gross, vatNOK pgtype.Numeric, err error) {
	if net, err = numericFromRat(t.net, 2); err != nil {
		return
	}
	if vat, err = numericFromRat(t.vat, 2); err != nil {
		return
	}
	if gross, err = numericFromRat(t.gross, 2); err != nil {
		return
	}
	vatNOK, err = numericFromRat(t.vatNOK, 2)
	return
}

// addressColumns is the delivery address as its five columns.
func addressColumns(a *gen.InvoicesDeliveryAddress) (line1, line2, postal, city, country *string) {
	if a == nil {
		return nil, nil, nil, nil, nil
	}
	return &a.Line1, a.Line2, a.PostalCode, &a.City, &a.Country
}

// writeLines replaces a draft's lines with lines, on txq, and answers the new
// lines' ids by position. The delete takes the lines' held sources with it
// (the cascade); a save carries them across (carrySources, insertSources).
func writeLines(ctx context.Context, txq *store.Queries, invoiceID int64, lines []draftLine) ([]int64, error) {
	if err := txq.DeleteLines(ctx, invoiceID); err != nil {
		return nil, fmt.Errorf("invoices: clear draft %d's lines: %w", invoiceID, err)
	}
	ids := make([]int64, 0, len(lines))
	for i, l := range lines {
		p := store.InsertLineParams{
			InvoiceID: invoiceID, Position: int32(i + 1), Description: l.description, Unit: l.unit,
			VatCodeID: l.vatCodeID, CreditsLineID: l.creditsLineID, DeductsInvoiceID: l.deductsInvoiceID,
		}
		var err error
		for _, c := range []struct {
			dst    *pgtype.Numeric
			v      *big.Rat
			places int
		}{
			{&p.Quantity, l.quantity, 3}, {&p.UnitPrice, l.unitPrice, 4}, {&p.DiscountPercent, l.discount, 2},
			{&p.LineGross, l.amounts.gross, 2}, {&p.LineAllowance, l.amounts.allowance, 2}, {&p.LineNet, l.amounts.net, 2},
		} {
			if *c.dst, err = numericFromRat(c.v, c.places); err != nil {
				return nil, err
			}
		}
		id, err := txq.InsertLine(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("invoices: write draft %d's line %d: %w", invoiceID, i+1, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// PostInvoices Create an invoice draft
// (POST /api/v1/invoices)
//
// The billing profile is read before anything is written — no lock is held
// across a directory call — for the prefills and the gates: yourReference is
// the profile's buyer reference and paymentTermsDays its terms, else the
// settings' default, whenever the request leaves them out.
func (s *server) PostInvoices(ctx context.Context, req gen.PostInvoicesRequestObject) (gen.PostInvoicesResponseObject, error) {
	q := store.New(s.deps.Pool)
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	in, errs := parseDraft(*req.Body, settings.DefaultCurrency)
	// Work arrives on a draft only through the uninvoiced view (D2, D3): a
	// create holds none, and has none to refresh.
	for i, l := range in.lines {
		if len(l.sources) > 0 {
			errs = withSourceError(errs, i, msgSourcesOnCreate)
		}
	}
	if req.Body.RefreshSources != nil && *req.Body.RefreshSources {
		errs = withFieldError(errs, "refreshSources", "A new draft holds no work to refresh")
	}
	if len(errs) > 0 {
		return gen.PostInvoices400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle, errs)), nil
	}
	profile, err := s.customerProfile(ctx, in.customerID)
	if err != nil {
		return nil, err
	}
	if refusal := customerGate(profile); refusal != nil {
		return gen.PostInvoices409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if in.yourReference == nil {
		in.yourReference = ptr(profile.BuyerReference)
	}
	if in.paymentTermsDays == nil {
		in.paymentTermsDays = profile.PaymentTermsDays
	}
	if in.paymentTermsDays == nil {
		in.paymentTermsDays = &settings.DefaultPaymentTermsDays
	}

	codes, err := vatCodesOn(ctx, q, pgDate(businessDay(s.deps.Clock())))
	if err != nil {
		return nil, err
	}
	errs = checkInvoiceLines(in.lines, codes, errs)
	errs, duplicated, err := deductionRules(ctx, q, in.customerID, 0, in.lines, errs)
	if err != nil {
		return nil, err
	}
	_, totals, _ := summarize(taxedLines(in.lines, codes), big.NewRat(1, 1))
	errs = checkTotal(totals, errs)
	if len(errs) > 0 {
		return gen.PostInvoices400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle, errs)), nil
	}
	if duplicated != nil {
		return gen.PostInvoices409ApplicationProblemPlusJSONResponse(*duplicated), nil
	}
	net, vat, gross, vatNOK, err := numerics(totals)
	if err != nil {
		return nil, err
	}
	line1, line2, postal, city, country := addressColumns(in.address)
	var created store.InvoicesInvoice
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		created, err = txq.InsertInvoiceDraft(ctx, store.InsertInvoiceDraftParams{
			CustomerID: in.customerID, DeliveryDate: in.deliveryDate, DeliveryFrom: in.deliveryFrom, DeliveryTo: in.deliveryTo,
			DeliveryAddressLine1: line1, DeliveryAddressLine2: line2, DeliveryPostalCode: postal, DeliveryCity: city, DeliveryCountry: country,
			PaymentTermsDays: in.paymentTermsDays, Currency: settings.DefaultCurrency,
			YourReference: *in.yourReference, OurReference: in.ourReference, OrderReference: in.orderReference,
			Note: in.note, InternalNote: in.internalNote,
			NetTotal: net, VatTotal: vat, GrossTotal: gross, VatTotalNok: vatNOK,
			CreatedByUserID: callerID(ctx), Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("invoices: create a draft: %w", err)
		}
		_, err = writeLines(ctx, txq, created.ID, in.lines)
		return err
	})
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, q, created, profile)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoices201JSONResponse(resp), nil
}

// PutInvoicesById Replace a draft
// (PUT /api/v1/invoices/{id})
//
// A full replace with revision. The billing profile is read before the
// transaction, and the gates run on every save: a customer merged away,
// archived or disabled since the draft was made refuses the save too.
func (s *server) PutInvoicesById(ctx context.Context, req gen.PutInvoicesByIdRequestObject) (gen.PutInvoicesByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	current, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutInvoicesById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	if current.Status != statusDraft {
		return gen.PutInvoicesById409ApplicationProblemPlusJSONResponse(invoiceIssued()), nil
	}
	if req.Body.Revision == nil {
		return gen.PutInvoicesById400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle,
			fieldError("revision", "A replace carries the revision it was read at"))), nil
	}
	if current.Kind == kindCreditNote {
		return s.putCreditDraft(ctx, q, current, *req.Body)
	}
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	in, errs := parseDraft(*req.Body, settings.DefaultCurrency)
	if in.paymentTermsDays == nil {
		errs = withFieldError(errs, "paymentTermsDays", "An invoice needs payment terms")
	}
	if len(errs) > 0 {
		return gen.PutInvoicesById400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle, errs)), nil
	}
	profile, err := s.customerProfile(ctx, in.customerID)
	if err != nil {
		return nil, err
	}
	if refusal := customerGate(profile); refusal != nil {
		return gen.PutInvoicesById409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	codes, err := vatCodesOn(ctx, q, pgDate(businessDay(s.deps.Clock())))
	if err != nil {
		return nil, err
	}
	errs = checkInvoiceLines(in.lines, codes, errs)
	errs, duplicated, err := deductionRules(ctx, q, in.customerID, current.ID, in.lines, errs)
	if err != nil {
		return nil, err
	}
	exchangeRate, err := ratFromNumeric(current.ExchangeRate)
	if err != nil {
		return nil, err
	}
	_, totals, _ := summarize(taxedLines(in.lines, codes), exchangeRate)
	errs = checkTotal(totals, errs)
	if len(errs) > 0 {
		return gen.PutInvoicesById400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle, errs)), nil
	}
	if duplicated != nil {
		return gen.PutInvoicesById409ApplicationProblemPlusJSONResponse(*duplicated), nil
	}
	// The held work, read on the pool before the save's transaction: what
	// refreshSources asks its modules about now, and what the derived
	// project is judged on — no contract call is made under the lock (D2).
	held, err := sourcesOf(ctx, q, req.Id)
	if err != nil {
		return nil, err
	}
	if req.Body.RefreshSources != nil && *req.Body.RefreshSources {
		now, kinds, err := s.billableNow(ctx, held)
		if err != nil {
			return nil, err
		}
		in.refresh = &sourcesRefresh{read: held, now: now, kind: kinds}
	}
	// The derived project's code, should the save need one (D9): read here,
	// before the transaction, like everything the directory answers.
	if in.projectCodes, err = s.saveProjectCodes(ctx, current, in, held); err != nil {
		return nil, err
	}
	saved, err := s.saveDraft(ctx, req.Id, *req.Body.Revision, in, totals)
	if errors.Is(err, errDocumentGone) {
		return gen.PutInvoicesById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	if saved.invalid != nil {
		return gen.PutInvoicesById400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle, saved.invalid)), nil
	}
	if saved.refusal != nil {
		return gen.PutInvoicesById409ApplicationProblemPlusJSONResponse(*saved.refusal), nil
	}
	resp, err := s.invoiceResponse(ctx, q, saved.doc, profile)
	if err != nil {
		return nil, err
	}
	withReleased(saved.released, &resp)
	return gen.PutInvoicesById200JSONResponse(resp), nil
}

// errDocumentGone is a document deleted between a handler's first read and
// its lock — two users, one deleting and one saving or issuing. The handler
// answers it with its operation's 404, as if the first read had missed.
var errDocumentGone = errors.New("invoices: the document was deleted")

// draftSaved is a save's outcome: the saved row and the work the save
// dropped (D2), or why it was refused — a 409, or a 400 on the fields only
// the lock can judge (a line's sources against the work the draft holds).
type draftSaved struct {
	doc      store.InvoicesInvoice
	released []sourceRef
	refusal  *gen.InvoicesConflictProblem
	invalid  map[string][]string
}

// saveDraft is the transaction every draft replace runs: the row taken FOR
// UPDATE, still a draft and at the revision the caller read; then the row and
// its lines replaced, the lines' work carried across (D2). The held sources
// are read under the document's lock, before DeleteLines takes them with the
// old lines: each named again on a new line is re-inserted under it with the
// snapshot the draft took, in one statement; what no line names, and every
// hold when the customer changed (plan reading 33), is dropped; an invoice
// draft's project is then derived from the work it carries (D9,
// setDocumentProject). A row deleted since the caller read it is
// errDocumentGone.
func (s *server) saveDraft(ctx context.Context, id int64, revision int32, in draftInput, totals documentTotals) (draftSaved, error) {
	net, vat, gross, vatNOK, err := numerics(totals)
	if err != nil {
		return draftSaved{}, err
	}
	line1, line2, postal, city, country := addressColumns(in.address)
	var out draftSaved
	var carried []heldSource
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		locked, err := txq.LockInvoice(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDocumentGone
		}
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", id, err)
		}
		if locked.Status != statusDraft {
			out.refusal = ptr(invoiceIssued())
			return errRefused
		}
		held, err := sourcesOf(ctx, txq, id)
		if err != nil {
			return err
		}
		// A refresh judged the work it read before this lock; a save that
		// slipped in between and changed that work makes its judgment moot
		// (plan reading 32).
		if in.refresh != nil && !sameHeldSet(in.refresh.read, held) {
			out.refusal = ptr(conflict(codeInvoiceChanged, "The draft changed",
				"The work this draft holds changed while it was being refreshed; read the draft again and retry."))
			return errRefused
		}
		if locked.Revision != revision {
			out.refusal = ptr(revisionConflict("Invoice", locked.Revision, revision))
			return errRefused
		}
		var released []sourceRef
		if in.customerID != locked.CustomerID && len(held) > 0 {
			dropped, err := txq.DeleteHeldSourcesOf(ctx, id)
			if err != nil {
				return fmt.Errorf("invoices: release draft %d's sources: %w", id, err)
			}
			for _, d := range dropped {
				released = append(released, sourceRef{kind: contracts.WorkSourceKind(d.SourceKind), id: d.SourceID})
			}
			held = nil
		}
		rows, dropped, invalidSources := carrySources(held, in.lines)
		if invalidSources != nil {
			out.invalid = invalidSources
			return errRefused
		}
		released = append(released, dropped...)
		if in.refresh != nil {
			rows, dropped = in.refresh.apply(rows)
			released = append(released, dropped...)
		}
		slices.SortFunc(released, compareRefs)
		yourReference := ""
		if in.yourReference != nil {
			yourReference = *in.yourReference
		}
		out.doc, err = txq.UpdateDraft(ctx, store.UpdateDraftParams{
			ID: id, CustomerID: in.customerID, DeliveryDate: in.deliveryDate, DeliveryFrom: in.deliveryFrom, DeliveryTo: in.deliveryTo,
			DeliveryAddressLine1: line1, DeliveryAddressLine2: line2, DeliveryPostalCode: postal, DeliveryCity: city, DeliveryCountry: country,
			PaymentTermsDays: in.paymentTermsDays, YourReference: yourReference, OurReference: in.ourReference,
			OrderReference: in.orderReference, Note: in.note, InternalNote: in.internalNote,
			NetTotal: net, VatTotal: vat, GrossTotal: gross, VatTotalNok: vatNOK, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("invoices: replace draft %d: %w", id, err)
		}
		lineIDs, err := writeLines(ctx, txq, id, in.lines)
		if err != nil {
			return err
		}
		carried, out.released = rows, released
		if err := insertSources(ctx, txq, id, lineIDs, rows); err != nil {
			return err
		}
		// A credit note holds no work: it keeps the project it copied from
		// its original (D9).
		if locked.Kind != kindInvoice {
			return nil
		}
		out.doc, err = setDocumentProject(ctx, txq, out.doc, rows, in.projectCodes)
		return err
	})
	if errors.Is(err, errSourceHeldElsewhere) {
		refusal, err := heldElsewhere(ctx, store.New(s.deps.Pool), id, carried)
		if err != nil {
			return draftSaved{}, err
		}
		return draftSaved{refusal: &refusal}, nil
	}
	if out.refusal != nil || out.invalid != nil {
		return draftSaved{refusal: out.refusal, invalid: out.invalid}, nil
	}
	return out, err
}

// DeleteInvoicesById Delete a draft
// (DELETE /api/v1/invoices/{id})
func (s *server) DeleteInvoicesById(ctx context.Context, req gen.DeleteInvoicesByIdRequestObject) (gen.DeleteInvoicesByIdResponseObject, error) {
	var notFound, issued bool
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		locked, err := txq.LockInvoice(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			notFound = true
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", req.Id, err)
		}
		if locked.Status != statusDraft {
			issued = true
			return errRefused
		}
		_, err = txq.DeleteDraft(ctx, req.Id)
		return err
	})
	switch {
	case notFound:
		return gen.DeleteInvoicesById404Response{}, nil
	case issued:
		return gen.DeleteInvoicesById409ApplicationProblemPlusJSONResponse(invoiceIssued()), nil
	case err != nil:
		return nil, fmt.Errorf("invoices: delete draft %d: %w", req.Id, err)
	}
	return gen.DeleteInvoicesById204Response{}, nil
}

// GetInvoicesById Get an invoice or a credit note
// (GET /api/v1/invoices/{id})
func (s *server) GetInvoicesById(ctx context.Context, req gen.GetInvoicesByIdRequestObject) (gen.GetInvoicesByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	inv, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetInvoicesById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	var profile *contracts.CustomerBillingProfile
	if inv.Status == statusDraft && inv.Kind == kindInvoice {
		// A draft invoice names its customer as the directory knows it today,
		// and warns when the profile invoices in another currency.
		if profile, err = s.customerProfile(ctx, inv.CustomerID); err != nil {
			return nil, err
		}
	}
	// An issued document's deliveries and sendDefaults both turn on
	// invoices:issue: asked once, for both.
	var canIssue *bool
	if inv.Status == statusIssued {
		canIssue = ptr(s.has(ctx, "invoices:issue"))
	}
	resp, err := s.renderInvoice(ctx, q, inv, profile, nil, canIssue)
	if err != nil {
		return nil, err
	}
	if inv.Status == statusDraft && inv.Kind == kindInvoice && s.has(ctx, "invoices:create") {
		s.withDraftFreshness(ctx, q, inv.ID, &resp)
	}
	// The one read besides the send that answers what a send would open with
	// (payments and delivery design D4) — by e-mail and as EHF (EHF and KID
	// design D10): no write adds a directory call.
	if canIssue != nil {
		if err := s.withSendDefaults(ctx, q, inv, nil, *canIssue, true, &resp); err != nil {
			return nil, err
		}
	}
	return gen.GetInvoicesById200JSONResponse(resp), nil
}

// withDraftFreshness warns on an invoice draft's response which of its held
// sources changed or are no longer invoiceable (D2) — read by id through the
// billable reads, on the pool, for a caller who builds invoices
// (invoices:create), the one who can act on it. A read that fails is logged
// and leaves the warnings out: a read of the draft is never refused for a
// neighbour, and the issue judges the work again under its lock.
func (s *server) withDraftFreshness(ctx context.Context, q *store.Queries, invoiceID int64, resp *gen.InvoicesInvoiceResponse) {
	held, err := sourcesOf(ctx, q, invoiceID)
	if err == nil && len(held) > 0 {
		var verdicts map[sourceRef]string
		if verdicts, err = s.freshness(ctx, held); err == nil {
			withFreshness(held, verdicts, resp)
		}
	}
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: a draft's work could not be judged fresh", "invoiceId", invoiceID, "error", err)
	}
}
