package invoices

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is GET /meta: the one read every Invoices page makes before it
// draws anything (D2). It answers what this installation can do (an object
// store or not, a mail driver that sends or not, EHF or not), what the seller
// still lacks, whether the series has started, the VAT codes a new line may
// take today, and what the caller may do — so no client re-derives a rule
// this module owns — and which work it can invoice.

// GetInvoicesMeta Get the Invoices metadata
// (GET /api/v1/invoices/meta)
func (s *server) GetInvoicesMeta(ctx context.Context, _ gen.GetInvoicesMetaRequestObject) (gen.GetInvoicesMetaResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	issued, err := anythingIssued(ctx, q)
	if err != nil {
		return nil, err
	}
	today := businessDay(s.deps.Clock())
	codes, err := q.VatCodesInForce(ctx, pgDate(today))
	if err != nil {
		return nil, fmt.Errorf("invoices: read the VAT codes in force: %w", err)
	}
	inForce := make([]gen.InvoicesVatCodeInForce, 0, len(codes))
	for _, c := range codes {
		rate, err := floatFromNumeric(c.RatePercent)
		if err != nil {
			return nil, err
		}
		inForce = append(inForce, gen.InvoicesVatCodeInForce{
			Id: c.ID, Code: c.Code, Name: c.Name, SafTCode: c.SafTCode, EhfCategory: c.EhfCategory,
			ExemptionReason: c.ExemptionReason, RatePercent: rate,
		})
	}
	missing := sellerMissingFields(row)
	mail := s.mailAvailable()
	ehf, rejected, err := s.ehfAvailable(ctx, q, row)
	if err != nil {
		return nil, err
	}
	canIssue := s.has(ctx, "invoices:issue")
	// Whether reminders are offered at all (invoices payments and reminders
	// design D1, D7).
	reminders, _, err := s.reminderSettings(ctx, q)
	if err != nil {
		return nil, err
	}
	pay, err := s.paymentsAvailable(ctx, q)
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesMeta200JSONResponse(gen.InvoicesMetaResponse{
		Currency:                       row.DefaultCurrency,
		DefaultPaymentTermsDays:        row.DefaultPaymentTermsDays,
		SellerComplete:                 len(missing) == 0,
		MissingSellerFields:            missing,
		AnythingIssued:                 issued,
		SeriesStart:                    row.SeriesStart,
		StorageAvailable:               s.storageConfigured,
		MailAvailable:                  mail,
		EhfAvailable:                   ehf,
		AccessPointCredentialsRejected: rejected,
		Today:                          wireDate(today),
		VatCodes:                       inForce,
		// What work this installation can invoice (invoices work design D3):
		// the billable reads composed, each optional.
		WorkAvailable:    s.workAvailable(),
		RemindersEnabled: reminders.Enabled,
		// Vipps (invoices payments and reminders design D1, D14).
		PayLinksAvailable:        pay.payLinks,
		OnSitePaymentsAvailable:  pay.onSite,
		VippsCredentialsRejected: pay.rejected,
		Work: gen.InvoicesMetaWork{
			Hours: s.deps.BillableHours != nil, Expenses: s.deps.BillableExpenses != nil, Milestones: s.deps.BillableMilestones != nil,
		},
		Capabilities: gen.InvoicesMetaCapabilities{
			CanCreate:            s.has(ctx, "invoices:create"),
			CanImportBankFiles:   s.has(ctx, "invoices:payments"),
			CanIssue:             canIssue,
			CanManage:            s.has(ctx, "invoices:manage"),
			CanRegisterPayments:  s.has(ctx, "invoices:payments"),
			CanRunReminders:      s.has(ctx, "invoices:payments"),
			CanSend:              canIssue && mail,
			CanSendEhf:           canIssue && ehf,
			CanTakeOnSitePayment: canIssue && pay.onSite,
		},
	}), nil
}

// mailAvailable is whether this installation can send a document at all: its
// mail driver is smtp (payments and delivery design D4). The log driver
// delivers nothing, so a send there is refused before anything is read.
func (s *server) mailAvailable() bool {
	return s.deps.Config != nil && s.deps.Config.Mail.Driver == "smtp"
}

// ehfAvailable is whether this installation can send a document as EHF
// (EHF and KID design D1): the INVOICES_EHF_ENABLED switch, the Peppol lookup
// enabled (a send that cannot re-check its receiver does not send), an
// access-point credentials row and the seller's Peppol id, all four. rejected
// is whether the provider refused the stored key — reported beside, never
// folded in, so the settings page and the send dialog can say why a send
// fails rather than hide the feature. Nothing here touches the network.
func (s *server) ehfAvailable(ctx context.Context, q *store.Queries, settings store.InvoicesSetting) (available, rejected bool, err error) {
	creds, err := q.GetAccessPointCredentials(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, false, nil
	case err != nil:
		return false, false, fmt.Errorf("invoices: read the access point credentials: %w", err)
	}
	rejected = creds.RejectedAt != nil
	available = s.deps.Config != nil && s.deps.Config.InvoicesEhfEnabled &&
		s.peppolLookup != nil && settings.PeppolID != nil && *settings.PeppolID != ""
	return available, rejected, nil
}

// paymentAvailability is meta's three payment fields.
type paymentAvailability struct {
	payLinks, onSite, rejected bool
}

// paymentsAvailable is what Vipps offers this installation now (D1, D14):
// pay links with INVOICES_VIPPS_ENABLED on, PUBLIC_BASE_URL set, credentials
// stored and not rejected and the settings' switch on; on-site payment the
// same without PUBLIC_BASE_URL. rejected is reported beside, never folded
// into the rest of the answer, so a page can say why. Nothing here touches
// the network.
func (s *server) paymentsAvailable(ctx context.Context, q *store.Queries) (paymentAvailability, error) {
	creds, err := q.GetPaymentCredentials(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return paymentAvailability{}, nil
	case err != nil:
		return paymentAvailability{}, fmt.Errorf("invoices: read the Vipps credentials: %w", err)
	}
	settings, err := q.GetPaymentSettings(ctx)
	if err != nil {
		return paymentAvailability{}, fmt.Errorf("invoices: read the payment settings: %w", err)
	}
	out := paymentAvailability{rejected: creds.RejectedAt != nil}
	usable := s.deps.Config != nil && s.deps.Config.InvoicesVippsEnabled && !out.rejected
	out.payLinks = usable && s.deps.Config.PublicBaseURL != "" && settings.PayLinksEnabled
	out.onSite = usable && settings.OnSitePaymentsEnabled
	return out, nil
}

// anythingIssued is "the counter row exists" (D2), never a count of documents.
func anythingIssued(ctx context.Context, q *store.Queries) (bool, error) {
	_, err := q.CounterNextValue(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("invoices: read the document counter: %w", err)
	}
	return true, nil
}
