package invoices

import (
	"context"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the quick invoice (invoices payments and reminders design
// D17): for a job finished on site, an invoice created and issued in one
// transaction and recorded as handed over. Before the transaction, as a
// draft's create and the issue do: the body by the draft's rules, the object
// store, the billing profile and the customer gates, the VAT codes — no lock
// is held across a directory call. In the transaction: the draft and its
// lines inserted — the row the transaction now holds, so the order stays the
// document, the settings row, the counter — then issueLocked, the issue's own
// body, then a manual delivery handed_over on the issue date. Any refusal
// rolls the whole transaction back: no draft is left and no number is taken.
// Paid later it is a credit sale like any invoice; paid on site with Vipps it
// is kontantsalg, offered only as a kontantfaktura (D17's hard rules), which
// payOnSite asks for.

// maxQuickLines is how many lines a quick invoice carries (D17).
const maxQuickLines = 10

// deliveryHandedOver is the manual delivery a quick invoice records (D8).
const deliveryHandedOver = "handed_over"

// quickAfterInsert is called inside the quick invoice's transaction right
// after its draft and lines are inserted, before the issue, with the new
// document's id, so a test can park it there or show the draft is not yet
// visible outside it. nil in production.
var quickAfterInsert func(ctx context.Context, invoiceID int64) error

// quickDraft is the quick invoice's body as a draft's request, deliveryDate
// defaulting to today.
func quickDraft(body gen.InvoicesQuickInvoiceRequest, today openapi_types.Date) gen.InvoicesInvoiceRequest {
	lines := make([]gen.InvoicesLineRequest, 0, len(body.Lines))
	for _, l := range body.Lines {
		lines = append(lines, gen.InvoicesLineRequest{
			Description: l.Description, Quantity: l.Quantity, Unit: l.Unit, UnitPrice: l.UnitPrice,
			DiscountPercent: l.DiscountPercent, VatCodeId: l.VatCodeId,
		})
	}
	delivery := body.DeliveryDate
	if delivery == nil {
		delivery = &today
	}
	return gen.InvoicesInvoiceRequest{
		CustomerId: body.CustomerId, Lines: lines, DeliveryDate: delivery,
		PaymentTermsDays: body.PaymentTermsDays, YourReference: body.YourReference, Note: body.Note,
	}
}

// PostInvoicesQuick Create and issue a quick invoice
// (POST /api/v1/invoices/quick)
func (s *server) PostInvoicesQuick(ctx context.Context, req gen.PostInvoicesQuickRequestObject) (gen.PostInvoicesQuickResponseObject, error) {
	q := store.New(s.deps.Pool)
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	now := s.deps.Clock()
	in, errs := parseDraft(quickDraft(*req.Body, wireDate(businessDay(now))), settings.DefaultCurrency)
	switch n := len(req.Body.Lines); {
	case n == 0:
		errs = withFieldError(errs, "lines", "A quick invoice needs at least one line")
	case n > maxQuickLines:
		errs = withFieldError(errs, "lines", fmt.Sprintf("A quick invoice has at most %d lines", maxQuickLines))
	}
	if len(errs) > 0 {
		return gen.PostInvoicesQuick400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle, errs)), nil
	}
	if !s.storageConfigured {
		status := int32(503)
		code, title := codeStorageUnavailable, storageUnavailableTitle
		detail := "This installation has no object store, so an issued document's PDF could never be stored. Nothing was created or issued."
		return gen.PostInvoicesQuick503ApplicationProblemPlusJSONResponse(gen.InvoicesConflictProblem{
			Code: &code, Title: &title, Detail: &detail, Status: &status,
		}), nil
	}
	profile, err := s.customerProfile(ctx, in.customerID)
	if err != nil {
		return nil, err
	}
	if refusal := customerGate(profile); refusal != nil {
		return gen.PostInvoicesQuick409ApplicationProblemPlusJSONResponse(*refusal), nil
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
	codes, err := vatCodesOn(ctx, q, in.deliveryDate)
	if err != nil {
		return nil, err
	}
	errs = checkInvoiceLines(in.lines, codes, errs)
	// No line names a deduction, so this is the draft's quantity rule alone.
	errs, _, err = deductionRules(ctx, q, in.customerID, settings.DefaultCurrency, in.lines, errs)
	if err != nil {
		return nil, err
	}
	_, totals, _ := summarize(taxedLines(in.lines, codes), big.NewRat(1, 1))
	errs = checkTotal(totals, errs)
	if len(errs) > 0 {
		return gen.PostInvoicesQuick400ApplicationProblemPlusJSONResponse(invalid(invalidInvoiceTitle, errs)), nil
	}
	net, vat, gross, vatNOK, err := numerics(totals)
	if err != nil {
		return nil, err
	}
	line1, line2, postal, city, country := addressColumns(in.address)

	var issued store.InvoicesInvoice
	var refusal *gen.InvoicesConflictProblem
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		// 1. The draft and its lines, as a create writes them: the row this
		// transaction now holds. A quick invoice bills no work, so it carries
		// no timesheet.
		draft, err := txq.InsertInvoiceDraft(ctx, store.InsertInvoiceDraftParams{
			CustomerID: in.customerID, DeliveryDate: in.deliveryDate, DeliveryFrom: in.deliveryFrom, DeliveryTo: in.deliveryTo,
			DeliveryAddressLine1: line1, DeliveryAddressLine2: line2, DeliveryPostalCode: postal, DeliveryCity: city, DeliveryCountry: country,
			PaymentTermsDays: in.paymentTermsDays, Currency: settings.DefaultCurrency,
			YourReference: *in.yourReference, OurReference: in.ourReference, OrderReference: in.orderReference,
			Note: in.note, InternalNote: in.internalNote,
			NetTotal: net, VatTotal: vat, GrossTotal: gross, VatTotalNok: vatNOK,
			CreatedByUserID: callerID(ctx), Now: now,
		})
		if err != nil {
			return fmt.Errorf("invoices: create a quick invoice's draft: %w", err)
		}
		if _, err := writeLines(ctx, txq, draft.ID, in.lines); err != nil {
			return err
		}
		if quickAfterInsert != nil {
			if err := quickAfterInsert(ctx, draft.ID); err != nil {
				return err
			}
		}
		// 2. The issue's own body on the document held: the settings row,
		// the number, the checks, the writes.
		issued, refusal, err = s.issueLocked(ctx, tx, txq, draft, draft, profile, issueWork{}, nil)
		if err != nil {
			return err
		}
		if refusal != nil {
			return errRefused
		}
		// 3. Handed over on site, on the issue date (D8, D17).
		if _, err := txq.InsertManualDelivery(ctx, store.InsertManualDeliveryParams{
			InvoiceID: issued.ID, Kind: deliveryHandedOver, DeliveredOn: issued.IssueDate,
			RecordedByUserID: callerID(ctx), RecordedAt: *issued.IssuedAt,
		}); err != nil {
			return fmt.Errorf("invoices: record quick invoice %d handed over: %w", issued.ID, err)
		}
		return nil
	})
	if refusal != nil {
		return gen.PostInvoicesQuick409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if err != nil {
		return nil, err
	}
	// After the commit, the PDF is stored once (D7), as the issue does.
	issued = s.storeAfterIssue(ctx, q, issued)
	resp, err := s.invoiceResponse(ctx, q, issued, nil)
	if err != nil {
		return nil, err
	}
	out := gen.InvoicesQuickInvoiceResponse{Invoice: resp}
	if req.Body.PayOnSite != nil && *req.Body.PayOnSite {
		// On-site payment is not offered yet: the invoice stands issued, and
		// the answer says why no payment request was made.
		out.PaymentRequestError = ptr(conflict(codeOnSitePaymentsUnavailable, "The payment cannot be requested",
			"On-site payment is not turned on. The invoice is issued, and is paid later like any other."))
	}
	return gen.PostInvoicesQuick201JSONResponse(out), nil
}
