package invoices

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is GET /meta: the one read every Invoices page makes before it
// draws anything (D2). It answers what this installation can do (an object
// store or not, a channel chosen to send from or not, EHF or not), what the seller
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
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	today := holidays.DayIn(s.deps.Clock(), loc)
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
	mail := s.mailAvailable(ctx)
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
			CanManageBankFeed:    s.has(ctx, "invoices:manage"),
			CanQuickInvoice:      canIssue && s.has(ctx, "invoices:create"),
			CanRegisterPayments:  s.has(ctx, "invoices:payments"),
			CanRunReminders:      s.has(ctx, "invoices:payments"),
			CanSend:              canIssue && mail,
			CanSendEhf:           canIssue && ehf,
			CanTakeOnSitePayment: canIssue && pay.onSite,
		},
	}), nil
}

// sendingChannel is the channel documents and reminder letters are sent from
// now (integrations design T6), and whether it can send: the one Invoice
// settings chose, with Communications answering that it exists and can send.
// Disabled - nothing chosen, or Communications off - and a channel that is
// gone or cannot send alike answer false; an error reading either is logged
// and answers false, since a document must not be sent on a guess. No network
// call; never read under a lock.
func (s *server) sendingChannel(ctx context.Context) (uuid.UUID, bool) {
	chosen, err := store.New(s.deps.Pool).GetSendChannel(ctx)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: the sending channel could not be read", "error", err.Error())
		return uuid.UUID{}, false
	}
	if chosen == nil {
		return uuid.UUID{}, false
	}
	ch, err := s.outboundChannel(ctx, *chosen)
	switch {
	case errors.Is(err, contracts.ErrChannelUnknown):
		return uuid.UUID{}, false
	case err != nil:
		s.deps.Logger.WarnContext(ctx, "invoices: the sending channel could not be read", "error", err.Error())
		return uuid.UUID{}, false
	}
	return ch.ID, ch.Available
}

// mailAvailable is whether a document can be sent by e-mail now: Invoice
// settings choose a channel, and it can send. Otherwise sending is disabled -
// a document is refused before anything is read (the send's 503), and a
// letter goes on paper.
func (s *server) mailAvailable(ctx context.Context) bool {
	_, ok := s.sendingChannel(ctx)
	return ok
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
