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
	// The clock is read here once, for the draft's defaults (its delivery
	// day and created_at), and once more by issueLocked after the counter,
	// as the issue does: a quick invoice waiting there across Oslo midnight
	// is issued, and handed over, the day it is issued, after the day its
	// draft says it was delivered — never before it.
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
	// After the commit, a merge that ran in the window follows the document
	// (quickFollowMerge); then the PDF is stored once (D7), as the issue does.
	issued = s.quickFollowMerge(ctx, issued)
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

// maxMergeHops bounds how far quickFollowMerge follows MergedInto: a
// survivor merged away in turn is followed, a cycle is not.
const maxMergeHops = 10

// quickFollowMerge moves a committed quick invoice to the customer its own
// was merged into while its transaction was open (D17, after review). The
// merge's RepointCustomer moves every document of the customer merged away,
// but it cannot see one inserted by a transaction that has not committed, and
// the quick invoice read the billing profile before its own transaction. So
// the profile is read again after the commit, outside any lock, MergedInto
// followed to the survivor, and the document — under its own lock, in a
// transaction of its own — gets the survivor's id: exactly the re-point's
// write for an issued document, the buyer snapshot unchanged. A merge that
// still has not committed by the time of this read is not seen: its
// re-point ran before the quick invoice committed, and the document stays
// on the customer merged away, as an ordinary issue racing the same merge
// leaves its document. A failure here is logged: the invoice is issued, and
// a replay of the request (requestId) looks again.
func (s *server) quickFollowMerge(ctx context.Context, inv store.InvoicesInvoice) store.InvoicesInvoice {
	into := inv.CustomerID
	for range maxMergeHops {
		p, err := s.customerProfile(ctx, into)
		if err != nil {
			s.deps.Logger.ErrorContext(ctx, "invoices: a quick invoice's customer could not be read again after its issue",
				"invoice_id", inv.ID, "customer_id", into, "error", err.Error())
			return inv
		}
		if p == nil || p.MergedInto == nil || *p.MergedInto == into {
			break
		}
		into = *p.MergedInto
	}
	if into == inv.CustomerID {
		return inv
	}
	moved := inv
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		locked, err := lockInvoice(ctx, txq, inv.ID)
		if err != nil {
			return fmt.Errorf("invoices: lock quick invoice %d: %w", inv.ID, err)
		}
		if locked.CustomerID != inv.CustomerID {
			// A merge that committed after the read moved it already.
			moved = locked
			return nil
		}
		moved, err = txq.RepointQuickInvoice(ctx, store.RepointQuickInvoiceParams{
			IntoCustomerID: into, ID: inv.ID, FromCustomerID: inv.CustomerID,
		})
		if err != nil {
			return fmt.Errorf("invoices: re-point quick invoice %d to customer %d: %w", inv.ID, into, err)
		}
		return nil
	})
	if err != nil {
		s.deps.Logger.ErrorContext(ctx, "invoices: a quick invoice issued to a customer merged away was not moved to the survivor",
			"invoice_id", inv.ID, "customer_id", inv.CustomerID, "survivor_id", into, "error", err.Error())
		return inv
	}
	return moved
}
