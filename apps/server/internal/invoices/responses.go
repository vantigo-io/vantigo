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
// totals are computed afresh with the rates in force today.

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

// invoiceResponse renders one document. profile is the billing profile the
// caller already read for an invoice draft (for its current name and the
// currency warning), nil otherwise; this function reads no directory itself.
func (s *server) invoiceResponse(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, profile *contracts.CustomerBillingProfile) (gen.InvoicesInvoiceResponse, error) {
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
		resp.PdfStored = ptr(inv.PdfSha256 != nil)
		if inv.Kind == kindInvoice && issuedLate(inv.IssueDate.Time, deliveryEndOf(inv)) {
			resp.Warnings = append(resp.Warnings, warningIssuedLate)
		}
		return resp, creditLinks(ctx, q, inv, &resp)
	}

	lines, err := storedDraftLines(stored)
	if err != nil {
		return gen.InvoicesInvoiceResponse{}, err
	}
	var taxed []taxedLine
	if inv.Kind == kindCreditNote {
		if taxed, err = creditDraftTaxedLines(ctx, q, *inv.CreditsInvoiceID, stored, lines); err != nil {
			return gen.InvoicesInvoiceResponse{}, err
		}
	} else {
		codes, err := vatCodesOn(ctx, q, pgDate(today))
		if err != nil {
			return gen.InvoicesInvoiceResponse{}, err
		}
		taxed = taxedLines(lines, codes)
		for _, l := range lines {
			if c, ok := codes[l.vatCodeID]; ok && c.rate == nil {
				resp.Warnings = append(resp.Warnings, warningVatCodeNotValid)
				break
			}
		}
	}
	rows, totals, _ := summarize(taxed, big.NewRat(1, 1))
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
	return resp, creditLinks(ctx, q, inv, &resp)
}
