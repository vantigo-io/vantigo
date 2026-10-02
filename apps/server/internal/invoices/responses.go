package invoices

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file renders a document for the wire (D4): every column in camelCase,
// its lines, its VAT summaries and its warnings. An issued document is
// rendered from its own rows and snapshots only; a draft's summaries and
// totals are computed afresh — an invoice draft's with the rates in force
// today, a credit-note draft's at its original lines' snapshot rates. Every
// document answers its derived state (D3); an issued invoice also its money —
// paid, open, a refund due — and its payments; every issued document its
// deliveries (payments and delivery design D4).

// The warnings a document carries (D4, D6, D8). They are never refusals.
const (
	warningCustomerCurrencyDiffers = "customer_currency_differs"
	warningIssuedLate              = "issued_late"
	// warningVatCodeNotValid is a draft line whose code has no rate period
	// covering today: the draft totals it at 0 %, and the issue would refuse
	// it (vat_code_not_valid), so the editor is told before the dialog is.
	warningVatCodeNotValid = "vat_code_not_valid"
)

// wireDateOf is a date column onto the wire, nil for NULL.
func wireDateOf(d pgtype.Date) *openapi_types.Date {
	if !d.Valid {
		return nil
	}
	return ptr(wireDate(d.Time))
}

// deliveryEndOf is when a document's delivery ended — its day, or its
// period's last day — and the zero time when it has none.
func deliveryEndOf(inv store.InvoicesInvoice) time.Time {
	switch {
	case inv.DeliveryDate.Valid:
		return inv.DeliveryDate.Time
	case inv.DeliveryTo.Valid:
		return inv.DeliveryTo.Time
	}
	return time.Time{}
}

// lineResponse renders one line.
func lineResponse(l store.InvoicesLine) (gen.InvoicesLine, error) {
	out := gen.InvoicesLine{
		Id: l.ID, Position: l.Position, Description: l.Description, Unit: l.Unit, VatCodeId: l.VatCodeID,
		CreditsLineId: l.CreditsLineID, VatCategory: l.VatCategory, SafTCode: l.SafTCode, ExemptionReason: l.ExemptionReason,
	}
	var err error
	for _, c := range []struct {
		dst *float64
		n   pgtype.Numeric
	}{
		{&out.Quantity, l.Quantity}, {&out.UnitPrice, l.UnitPrice}, {&out.DiscountPercent, l.DiscountPercent},
		{&out.LineGross, l.LineGross}, {&out.LineAllowance, l.LineAllowance}, {&out.LineNet, l.LineNet},
	} {
		if *c.dst, err = floatFromNumeric(c.n); err != nil {
			return gen.InvoicesLine{}, err
		}
	}
	if l.VatRatePercent.Valid {
		rate, err := floatFromNumeric(l.VatRatePercent)
		if err != nil {
			return gen.InvoicesLine{}, err
		}
		out.VatRatePercent = &rate
	}
	return out, nil
}

// summaryResponse renders one computed VAT summary row.
func summaryResponse(r vatSummary) gen.InvoicesVatSummary {
	return gen.InvoicesVatSummary{
		VatCategory: r.category, RatePercent: floatFromRat(r.rate, 2), SafTCode: r.safT, ExemptionReason: r.reason,
		TaxableAmount: floatFromRat(r.taxable, 2), VatAmount: floatFromRat(r.vat, 2), VatAmountNok: floatFromRat(r.vatNOK, 2),
	}
}

// storedSummaryResponse renders one issued document's stored summary row.
func storedSummaryResponse(r store.InvoicesVatSummary) (gen.InvoicesVatSummary, error) {
	out := gen.InvoicesVatSummary{VatCategory: r.VatCategory, SafTCode: r.SafTCode, ExemptionReason: r.ExemptionReason}
	var err error
	for _, c := range []struct {
		dst *float64
		n   pgtype.Numeric
	}{
		{&out.RatePercent, r.RatePercent}, {&out.TaxableAmount, r.TaxableAmount},
		{&out.VatAmount, r.VatAmount}, {&out.VatAmountNok, r.VatAmountNok},
	} {
		if *c.dst, err = floatFromNumeric(c.n); err != nil {
			return gen.InvoicesVatSummary{}, err
		}
	}
	return out, nil
}

// storedDraftLines are a draft's stored lines as the arithmetic reads them.
func storedDraftLines(lines []store.InvoicesLine) ([]draftLine, error) {
	out := make([]draftLine, 0, len(lines))
	for _, l := range lines {
		net, err := ratFromNumeric(l.LineNet)
		if err != nil {
			return nil, err
		}
		out = append(out, draftLine{vatCodeID: l.VatCodeID, amounts: lineAmounts{net: net}})
	}
	return out, nil
}

// buyerResponse is a document's buyer snapshot, nil when it has none yet (an
// invoice draft).
func buyerResponse(inv store.InvoicesInvoice) *gen.InvoicesBuyer {
	if inv.BuyerName == nil {
		return nil
	}
	b := &gen.InvoicesBuyer{
		Name: *inv.BuyerName, OrganisationNumber: inv.BuyerOrganisationNumber, ForeignId: inv.BuyerForeignID,
		AddressLine1: inv.BuyerAddressLine1, AddressLine2: inv.BuyerAddressLine2, PostalCode: inv.BuyerPostalCode,
		City: inv.BuyerCity, Region: inv.BuyerRegion, Country: inv.BuyerCountry, PeppolId: inv.BuyerPeppolID, Gln: inv.BuyerGln,
	}
	if inv.BuyerCustomerNumber != nil {
		b.CustomerNumber = *inv.BuyerCustomerNumber
	}
	if inv.BuyerType != nil {
		b.Type = *inv.BuyerType
	}
	if inv.BuyerLanguage != nil {
		b.Language = *inv.BuyerLanguage
	}
	return b
}

// sellerResponse is an issued document's seller snapshot, nil on a draft.
func sellerResponse(inv store.InvoicesInvoice) *gen.InvoicesSeller {
	if inv.SellerLegalName == nil {
		return nil
	}
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	flag := func(b *bool) bool { return b != nil && *b }
	return &gen.InvoicesSeller{
		LegalName: *inv.SellerLegalName, OrganisationNumber: deref(inv.SellerOrganisationNumber),
		VatRegistered: flag(inv.SellerVatRegistered), InForetaksregisteret: flag(inv.SellerInForetaksregisteret),
		AddressLine1: deref(inv.SellerAddressLine1), AddressLine2: deref(inv.SellerAddressLine2),
		PostalCode: deref(inv.SellerPostalCode), City: deref(inv.SellerCity), Country: deref(inv.SellerCountry),
		BankAccount: deref(inv.SellerBankAccount), Iban: deref(inv.SellerIban), Bic: deref(inv.SellerBic),
		Email: deref(inv.SellerEmail), FooterText: deref(inv.SellerFooterText),
	}
}

// settle answers an issued invoice's money on resp (D3): what its issued
// credit notes credit and what is left uncredited, what its live payments
// paid, what is open (gross − credited − paid, which a credit note after a
// payment takes below zero), the refund due only then, its state through the
// Go mirror with today the Oslo business day, and every payment, removed ones
// included with their removal (D2). Every figure is exact until the wire.
func settle(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, today time.Time, resp *gen.InvoicesInvoiceResponse) error {
	credited, left, err := uncredited(ctx, q, inv)
	if err != nil {
		return err
	}
	sum, err := q.LivePaymentsSum(ctx, inv.ID)
	if err != nil {
		return fmt.Errorf("invoices: read what document %d is paid: %w", inv.ID, err)
	}
	paid, err := ratFromNumeric(sum)
	if err != nil {
		return err
	}
	gross, err := ratFromNumeric(inv.GrossTotal)
	if err != nil {
		return err
	}
	var due *time.Time
	if inv.DueDate.Valid {
		due = &inv.DueDate.Time
	}
	open := new(big.Rat).Sub(left, paid)
	resp.State = documentState(inv.Kind, inv.Status, gross, credited, paid, due, today)
	resp.CreditedAmount, resp.UncreditedAmount = ptr(floatFromRat(credited, 2)), ptr(floatFromRat(left, 2))
	resp.PaidAmount, resp.OpenAmount = ptr(floatFromRat(paid, 2)), ptr(floatFromRat(open, 2))
	if open.Sign() < 0 {
		resp.RefundDue = ptr(floatFromRat(new(big.Rat).Neg(open), 2))
	}
	rows, err := q.PaymentsOf(ctx, inv.ID)
	if err != nil {
		return fmt.Errorf("invoices: read document %d's payments: %w", inv.ID, err)
	}
	payments := make([]gen.InvoicesPayment, 0, len(rows))
	for _, p := range rows {
		amount, err := ratFromNumeric(p.Amount)
		if err != nil {
			return err
		}
		payments = append(payments, gen.InvoicesPayment{
			Id: p.ID, PaidOn: wireDate(p.PaidOn.Time), Amount: floatFromRat(amount, 2), Currency: p.Currency,
			Reference: p.Reference, Note: p.Note, RegisteredAt: p.RegisteredAt, RegisteredByUserId: p.RegisteredByUserID,
			RemovedAt: p.RemovedAt, RemovedByUserId: p.RemovedByUserID, RemovalReason: p.RemovalReason,
		})
	}
	resp.Payments = &payments
	return nil
}

// invoiceResponse renders one document. profile is the billing profile the
// caller already read for an invoice draft (for its current name and the
// currency warning), nil otherwise; this function reads no directory itself.
func (s *server) invoiceResponse(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, profile *contracts.CustomerBillingProfile) (gen.InvoicesInvoiceResponse, error) {
	return s.renderInvoice(ctx, q, inv, profile, nil)
}

// creditDraftResponse renders a credit-note draft with the credit book its
// caller has read already, so a save reads its original's once.
func (s *server) creditDraftResponse(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, book *creditBook) (gen.InvoicesInvoiceResponse, error) {
	return s.renderInvoice(ctx, q, inv, nil, book)
}

// renderInvoice is invoiceResponse, with book the credit book of a credit
// note's original when the caller has it, nil otherwise.
func (s *server) renderInvoice(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, profile *contracts.CustomerBillingProfile, book *creditBook) (gen.InvoicesInvoiceResponse, error) {
	stored, err := q.Lines(ctx, inv.ID)
	if err != nil {
		return gen.InvoicesInvoiceResponse{}, fmt.Errorf("invoices: read document %d's lines: %w", inv.ID, err)
	}
	resp := gen.InvoicesInvoiceResponse{
		Id: inv.ID, Kind: inv.Kind, Status: inv.Status, Number: inv.Number, CustomerId: inv.CustomerID,
		IssueDate: wireDateOf(inv.IssueDate), DeliveryDate: wireDateOf(inv.DeliveryDate),
		DeliveryFrom: wireDateOf(inv.DeliveryFrom), DeliveryTo: wireDateOf(inv.DeliveryTo),
		PaymentTermsDays: inv.PaymentTermsDays, DueDate: wireDateOf(inv.DueDate), Currency: inv.Currency,
		ExchangeRateDate: wireDateOf(inv.ExchangeRateDate),
		YourReference:    inv.YourReference, OurReference: inv.OurReference, OrderReference: inv.OrderReference,
		Note: inv.Note, InternalNote: inv.InternalNote, Buyer: buyerResponse(inv), Seller: sellerResponse(inv),
		IssuedAt: inv.IssuedAt, IssuedByUserId: inv.IssuedByUserID,
		CreatedAt: inv.CreatedAt, UpdatedAt: inv.UpdatedAt, Revision: inv.Revision,
		Lines: make([]gen.InvoicesLine, 0, len(stored)), VatSummaries: []gen.InvoicesVatSummary{}, Warnings: []string{},
	}
	if inv.DeliveryAddressLine1 != nil {
		resp.DeliveryAddress = &gen.InvoicesDeliveryAddress{
			Line1: *inv.DeliveryAddressLine1, Line2: inv.DeliveryAddressLine2, PostalCode: inv.DeliveryPostalCode,
		}
		if inv.DeliveryCity != nil {
			resp.DeliveryAddress.City = *inv.DeliveryCity
		}
		if inv.DeliveryCountry != nil {
			resp.DeliveryAddress.Country = *inv.DeliveryCountry
		}
	}
	if resp.ExchangeRate, err = floatFromNumeric(inv.ExchangeRate); err != nil {
		return gen.InvoicesInvoiceResponse{}, err
	}
	for _, l := range stored {
		line, err := lineResponse(l)
		if err != nil {
			return gen.InvoicesInvoiceResponse{}, err
		}
		resp.Lines = append(resp.Lines, line)
	}
	switch {
	case inv.BuyerName != nil:
		resp.CustomerName = inv.BuyerName
	case profile != nil:
		resp.CustomerName = &profile.Name
	}

	today := businessDay(s.deps.Clock())
	// A draft's and a credit note's state reads no money; an issued
	// invoice's is settle's.
	if inv.Kind != kindInvoice || inv.Status != statusIssued {
		resp.State = documentState(inv.Kind, inv.Status, nil, nil, nil, nil, today)
	}
	if inv.Status == statusIssued {
		rows, err := q.VatSummaries(ctx, inv.ID)
		if err != nil {
			return gen.InvoicesInvoiceResponse{}, fmt.Errorf("invoices: read document %d's VAT: %w", inv.ID, err)
		}
		for _, r := range rows {
			row, err := storedSummaryResponse(r)
			if err != nil {
				return gen.InvoicesInvoiceResponse{}, err
			}
			resp.VatSummaries = append(resp.VatSummaries, row)
		}
		for _, c := range []struct {
			dst *float64
			n   pgtype.Numeric
		}{
			{&resp.NetTotal, inv.NetTotal}, {&resp.VatTotal, inv.VatTotal},
			{&resp.GrossTotal, inv.GrossTotal}, {&resp.VatTotalNok, inv.VatTotalNok},
		} {
			if *c.dst, err = floatFromNumeric(c.n); err != nil {
				return gen.InvoicesInvoiceResponse{}, err
			}
		}
		resp.PdfStored = ptr(inv.PdfSha256 != nil && inv.PdfObjectKey != nil)
		sends, err := q.DeliveriesOf(ctx, inv.ID)
		if err != nil {
			return gen.InvoicesInvoiceResponse{}, fmt.Errorf("invoices: read document %d's deliveries: %w", inv.ID, err)
		}
		deliveries := make([]gen.InvoicesDelivery, 0, len(sends))
		for _, d := range sends {
			deliveries = append(deliveries, gen.InvoicesDelivery{
				Id: d.ID, Recipient: d.Recipient, Subject: d.Subject, SentAt: d.SentAt, SentByUserId: d.SentByUserID,
			})
		}
		resp.Deliveries = &deliveries
		// § 5-2-2 is about when the document was issued, not the date it
		// carries: one issued on the 14th dated the last of the previous
		// month (§ 5-1-3) is judged on the 14th.
		issuedOn := inv.IssueDate.Time
		if inv.IssuedAt != nil {
			issuedOn = businessDay(*inv.IssuedAt)
		}
		if inv.Kind == kindInvoice {
			if issuedLate(issuedOn, deliveryEndOf(inv)) {
				resp.Warnings = append(resp.Warnings, warningIssuedLate)
			}
			if err := settle(ctx, q, inv, today, &resp); err != nil {
				return gen.InvoicesInvoiceResponse{}, err
			}
		}
		return resp, creditLinks(ctx, q, inv, &resp, nil)
	}

	lines, err := storedDraftLines(stored)
	if err != nil {
		return gen.InvoicesInvoiceResponse{}, err
	}
	var rows []vatSummary
	var totals documentTotals
	var credit *creditDraft
	if inv.Kind == kindCreditNote {
		// Totalled as its issue will total it (creditBook.total): a final
		// note's lines show what their original lines have left.
		cd, err := readCreditDraft(ctx, q, inv, stored, book)
		if err != nil {
			return gen.InvoicesInvoiceResponse{}, err
		}
		credit, rows, totals = &cd, cd.totals.rows, cd.totals.totals
		for i, a := range cd.totals.amounts {
			resp.Lines[i].LineGross, resp.Lines[i].LineAllowance = floatFromRat(a.gross, 2), floatFromRat(a.allowance, 2)
			resp.Lines[i].LineNet = floatFromRat(a.net, 2)
		}
	} else {
		codes, err := vatCodesOn(ctx, q, pgDate(today))
		if err != nil {
			return gen.InvoicesInvoiceResponse{}, err
		}
		// VAT in NOK at the draft's own exchange rate, as its issue writes it.
		exchangeRate, err := ratFromNumeric(inv.ExchangeRate)
		if err != nil {
			return gen.InvoicesInvoiceResponse{}, err
		}
		rows, totals, _ = summarize(taxedLines(lines, codes), exchangeRate)
		for _, l := range lines {
			if c, ok := codes[l.vatCodeID]; ok && c.rate == nil {
				resp.Warnings = append(resp.Warnings, warningVatCodeNotValid)
				break
			}
		}
	}
	for _, r := range rows {
		resp.VatSummaries = append(resp.VatSummaries, summaryResponse(r))
	}
	resp.NetTotal, resp.VatTotal = floatFromRat(totals.net, 2), floatFromRat(totals.vat, 2)
	resp.GrossTotal, resp.VatTotalNok = floatFromRat(totals.gross, 2), floatFromRat(totals.vatNOK, 2)
	if profile != nil && profile.Currency != "" && profile.Currency != inv.Currency {
		resp.Warnings = append(resp.Warnings, warningCustomerCurrencyDiffers)
	}
	// A credit note keeps its original's delivery and is issued after it by
	// nature: issued_late would always hold and say nothing (D8).
	if inv.Kind == kindInvoice && issuedLate(today, deliveryEndOf(inv)) {
		resp.Warnings = append(resp.Warnings, warningIssuedLate)
	}
	latest, err := q.LatestIssueDate(ctx)
	if err != nil {
		return gen.InvoicesInvoiceResponse{}, fmt.Errorf("invoices: read the latest issue date: %w", err)
	}
	allowed := []openapi_types.Date{}
	for _, d := range allowedIssueDates(today, deliveryEndOf(inv), latest.Time) {
		allowed = append(allowed, wireDate(d))
	}
	resp.AllowedIssueDates = &allowed
	return resp, creditLinks(ctx, q, inv, &resp, credit)
}
