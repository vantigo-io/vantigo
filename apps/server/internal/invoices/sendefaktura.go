package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/consumerinvoice"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the send as eFaktura (invoices phase 6 design I31, I33, plan
// D6): an issued invoice in NOK with a KID and a due date three days to a
// year ahead, sent to a person found at the provider, queued as one
// invoices.transmissions row on the eFaktura channel — and, when AvtaleGiro is
// on and the person has an active mandate that covers the amount and the due
// date leaves the bank its time, its AvtaleGiro claim beside it. Nothing here
// submits: the invoices-efaktura worker does. The lookup and the coverage
// check are made outside any transaction; the transaction takes the document
// FOR UPDATE and the consumer provider settings FOR SHARE, the send as EHF's
// shape.

const (
	codeEfakturaCreditNote  = "efaktura_credit_note"
	codeEfakturaDueTooSoon  = "efaktura_due_too_soon"
	codeEfakturaDueTooFar   = "efaktura_due_too_far"
	codeEfakturaNotFound    = "efaktura_not_found"
	codeEfakturaAlreadySent = "efaktura_already_sent"
	codeClaimExists         = "avtalegiro_claim_exists"
	// uxClaimsLive is the partial unique index that allows one live claim
	// per invoice.
	uxClaimsLive             = "ux_avtalegiro_claims_live"
	codeKidMissing           = "kid_missing"
	cannotSendEfakturaTitle  = "The invoice cannot be sent as eFaktura"
	efakturaNotSent          = "not_sent"
	reasonNoAgreement        = "no_agreement"
	avtaleGiroOff            = "avtalegiro_off"
	avtaleGiroNoMandate      = "avtalegiro_no_mandate"
	avtaleGiroDueTooSoon     = "avtalegiro_due_too_soon"
	avtaleGiroLimit          = "avtalegiro_limit"
	avtaleGiroCheckFailed    = "avtalegiro_check_failed"
	paymentTypeEfaktura      = "efaktura"
	paymentTypeBoth          = "both"
	avtaleGiroClaimDeadlineH = 14

	// warningEfakturaPreferred replaces delivery_preference_other on the
	// e-mail send when the invoice can go as eFaktura (plan D6).
	warningEfakturaPreferred = "efaktura_preferred"
)

// osloZone is the zone the network's deadlines are judged in, whatever the
// workspace's zone: MPS's 14:00 is Oslo's.
var osloZone = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		return time.FixedZone("CET", 3600)
	}
	return loc
}()

// avtaleGiroEarliestDue is the first due date a claim may carry when sent
// at now (DL): the fourth calendar day after the next working day that is
// still before 14:00 in Oslo.
func avtaleGiroEarliestDue(now time.Time) time.Time {
	local := now.In(osloZone)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	base := today
	if !holidays.BusinessDay(today) || local.Hour() >= avtaleGiroClaimDeadlineH {
		base = holidays.NextBusinessDay(today.AddDate(0, 0, 1))
	}
	return base.AddDate(0, 0, 4)
}

// avtaleGiroCancelDeadline is the last moment a claim due on due may be
// cancelled (DL): 14:00 Oslo time on the day before.
func avtaleGiroCancelDeadline(due time.Time) time.Time {
	return time.Date(due.Year(), due.Month(), due.Day()-1, avtaleGiroClaimDeadlineH, 0, 0, 0, osloZone)
}

func efakturaRefused(code, detail string) gen.InvoicesConflictProblem {
	return conflict(code, cannotSendEfakturaTitle, detail)
}

// efakturaBlockedBy is the first refusal a send would meet without the
// network: steps 1 to 4 and 6.
func (s *server) efakturaBlockedBy(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, rows []store.InvoicesTransmission, today time.Time) (string, error) {
	available, _, _, err := s.efakturaAvailable(ctx, q)
	if err != nil {
		return "", err
	}
	if !available {
		return codeEfakturaUnavailable, nil
	}
	if inv.Kind == kindCreditNote {
		return codeEfakturaCreditNote, nil
	}
	erased, err := q.CustomerErased(ctx, inv.CustomerID)
	if err != nil {
		return "", fmt.Errorf("invoices: read whether customer %d was erased: %w", inv.CustomerID, err)
	}
	switch {
	case erased:
		return codeCustomerAnonymised, nil
	case inv.Currency != "NOK":
		return codeCurrencyNotNOK, nil
	case inv.Kid == nil:
		return codeKidMissing, nil
	}
	if code := dueWindowRefusal(inv, today); code != "" {
		return code, nil
	}
	for _, t := range rows {
		if activeTransmission[t.Status] {
			return codeEfakturaAlreadySent, nil
		}
	}
	return "", nil
}

// dueWindowRefusal is the due date judged against today (I31).
func dueWindowRefusal(inv store.InvoicesInvoice, today time.Time) string {
	if !inv.DueDate.Valid {
		return codeEfakturaDueTooSoon
	}
	earliest, latest := efakturaDayWindow(today)
	due := utcDay(inv.DueDate.Time)
	switch {
	case due.Before(earliest):
		return codeEfakturaDueTooSoon
	case due.After(latest):
		return codeEfakturaDueTooFar
	}
	return ""
}

// efakturaTransmissionResponse is one row on the wire.
func efakturaTransmissionResponse(t store.InvoicesTransmission, issuerFields bool) gen.InvoicesEfakturaTransmission {
	out := gen.InvoicesEfakturaTransmission{
		Id: t.ID, Status: t.Status, PaymentType: deref(t.PaymentType), QueuedAt: t.QueuedAt,
		SubmittedAt: t.SubmittedAt, DeliveredAt: t.DeliveredAt, FailedAt: t.FailedAt, CancelledAt: t.CancelledAt,
		ProviderStatus: t.ProviderStatus,
	}
	if issuerFields && t.LastError != nil && strings.TrimSpace(*t.LastError) != "" {
		out.Reason = ptr(redactReason(*t.LastError))
	}
	return out
}

// claimResponse is one claim on the wire.
func claimResponse(c store.InvoicesAvtalegiroClaim) (gen.InvoicesAvtaleGiroClaim, error) {
	amount, err := floatFromNumeric(c.Amount)
	if err != nil {
		return gen.InvoicesAvtaleGiroClaim{}, err
	}
	out := gen.InvoicesAvtaleGiroClaim{
		Id: c.ID, Status: c.Status, Amount: amount, Kid: c.Kid, Attention: c.Attention,
		AcceptedAt: c.AcceptedAt, RejectedAt: c.RejectedAt, CancelRequestedAt: c.CancelRequestedAt,
		CancelRefusedAt: c.CancelRefusedAt, CancelledAt: c.CancelledAt, PaidAt: c.PaidAt, UnpaidAt: c.UnpaidAt,
		RejectionCode: c.RejectionCode,
	}
	out.DueDate.Time = c.DueDate.Time
	if c.RejectionText != nil {
		out.RejectionText = ptr(redactReason(*c.RejectionText))
	}
	return out, nil
}

// efakturaState is an issued invoice's eFaktura block.
func (s *server) efakturaState(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, canIssue *bool, loc *time.Location) (gen.InvoicesEfakturaState, error) {
	rows, err := q.EfakturaTransmissionsOf(ctx, inv.ID)
	if err != nil {
		return gen.InvoicesEfakturaState{}, fmt.Errorf("invoices: read document %d's eFaktura: %w", inv.ID, err)
	}
	issuerFields := false
	if canIssue != nil {
		issuerFields = *canIssue
	} else if len(rows) > 0 {
		issuerFields = s.has(ctx, "invoices:issue")
	}
	out := gen.InvoicesEfakturaState{Status: efakturaNotSent, Transmissions: make([]gen.InvoicesEfakturaTransmission, 0, len(rows))}
	for _, t := range rows {
		out.Transmissions = append(out.Transmissions, efakturaTransmissionResponse(t, issuerFields))
	}
	if len(rows) > 0 {
		out.Status = rows[0].Status
		claim, err := q.ClaimOfTransmission(ctx, rows[0].ID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return gen.InvoicesEfakturaState{}, fmt.Errorf("invoices: read document %d's claim: %w", inv.ID, err)
		default:
			c, err := claimResponse(claim)
			if err != nil {
				return gen.InvoicesEfakturaState{}, err
			}
			out.Claim = &c
		}
	}
	blocked, err := s.efakturaBlockedBy(ctx, q, inv, rows, holidays.DayIn(s.deps.Clock(), loc))
	if err != nil {
		return gen.InvoicesEfakturaState{}, err
	}
	out.CanSend = blocked == ""
	if blocked != "" {
		out.BlockedBy = &blocked
	}
	return out, nil
}

// openAmountOf is what is left to pay on an issued invoice: gross less its
// credit notes, its live payments and its standing write-off.
func openAmountOf(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) (*big.Rat, error) {
	_, left, err := uncredited(ctx, q, inv)
	if err != nil {
		return nil, err
	}
	sum, err := q.LivePaymentsSum(ctx, inv.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read what document %d is paid: %w", inv.ID, err)
	}
	paid, err := ratFromNumeric(sum.Paid)
	if err != nil {
		return nil, err
	}
	writtenOff, err := ratFromNumeric(sum.WrittenOff)
	if err != nil {
		return nil, err
	}
	return new(big.Rat).Sub(left, new(big.Rat).Add(paid, writtenOff)), nil
}

// claimPlan is what step 7 decided: a claim on the mandate for amount, or
// none and why.
type claimPlan struct {
	mandate *store.InvoicesAvtalegiroMandate
	amount  *big.Rat
	reason  string
}

// planClaim is step 7 (I33): AvtaleGiro on, an active mandate, the due date
// past the bank's deadline, and the mandate covering the amount — asked of
// the provider here, outside any transaction.
func (s *server) planClaim(ctx context.Context, q *store.Queries, p consumerinvoice.Provider, settings store.InvoicesSetting, inv store.InvoicesInvoice) (claimPlan, error) {
	if !settings.AvtalegiroEnabled {
		return claimPlan{reason: avtaleGiroOff}, nil
	}
	mandate, err := q.ActiveMandateOfCustomer(ctx, &inv.CustomerID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return claimPlan{reason: avtaleGiroNoMandate}, nil
	case err != nil:
		return claimPlan{}, fmt.Errorf("invoices: read customer %d's mandate: %w", inv.CustomerID, err)
	}
	if utcDay(inv.DueDate.Time).Before(avtaleGiroEarliestDue(s.deps.Clock())) {
		return claimPlan{reason: avtaleGiroDueTooSoon}, nil
	}
	open, err := openAmountOf(ctx, q, inv)
	if err != nil {
		return claimPlan{}, err
	}
	if open.Sign() <= 0 {
		return claimPlan{reason: avtaleGiroLimit}, nil
	}
	minor, err := minorOf(open)
	if err != nil {
		return claimPlan{}, err
	}
	covered, err := p.Covers(ctx, mandate.Kid, consumerinvoice.NOK(minor))
	switch {
	case err != nil:
		s.deps.Logger.WarnContext(ctx, "invoices: the mandate's coverage could not be asked; the eFaktura goes alone",
			"invoice_id", inv.ID, "error", redactReason(err.Error()))
		return claimPlan{reason: avtaleGiroCheckFailed}, nil
	case !covered:
		return claimPlan{reason: avtaleGiroLimit}, nil
	}
	return claimPlan{mandate: &mandate, amount: open}, nil
}

// publishTransmissionChanged is the transmission's event, on tx.
func publishTransmissionChanged(ctx context.Context, tx pgx.Tx, t store.InvoicesTransmission, status string, now time.Time) error {
	return events.Publish(ctx, tx, "invoices", contracts.EfakturaTransmissionChanged{
		TransmissionID: t.ID, InvoiceID: t.InvoiceID, Status: status, PaymentType: deref(t.PaymentType),
	}, now)
}

// publishClaimChanged is the claim's event, on tx.
func publishClaimChanged(ctx context.Context, tx pgx.Tx, c store.InvoicesAvtalegiroClaim, status string, now time.Time) error {
	amount, err := ratFromNumeric(c.Amount)
	if err != nil {
		return err
	}
	return events.Publish(ctx, tx, "invoices", contracts.AvtaleGiroClaimChanged{
		ClaimID: c.ID, InvoiceID: c.InvoiceID, Status: status, Amount: amount.FloatString(2),
		DueDate: c.DueDate.Time.Format(time.DateOnly),
	}, now)
}

// lookupForSend is step 5: the stored lookup, refreshed outside any
// transaction when older than seven days or never made.
func (s *server) lookupForSend(ctx context.Context, q *store.Queries, p consumerinvoice.Provider, customerID int32) (*store.InvoicesConsumerLookup, error) {
	row, err := q.GetConsumerLookup(ctx, customerID)
	switch {
	case err == nil && s.deps.Clock().Sub(row.CheckedAt) <= lookupMaxAge:
		return &row, nil
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("invoices: read customer %d's lookup: %w", customerID, err)
	}
	rows, err := s.refreshLookups(ctx, p, []int32{customerID})
	if err != nil {
		return nil, err
	}
	fresh, ok := rows[customerID]
	if !ok {
		return nil, nil
	}
	return &fresh, nil
}

// notFoundReason is the reason an efaktura_not_found carries.
func notFoundReason(l *store.InvoicesConsumerLookup) string {
	switch {
	case l == nil:
		return lookupNotFound
	case l.Status == lookupFound:
		return reasonNoAgreement
	default:
		return l.Status
	}
}

// PostInvoicesByIdSendEfaktura Send an issued invoice as eFaktura
// (POST /api/v1/invoices/{id}/send-efaktura)
func (s *server) PostInvoicesByIdSendEfaktura(ctx context.Context, req gen.PostInvoicesByIdSendEfakturaRequestObject) (gen.PostInvoicesByIdSendEfakturaResponseObject, error) {
	refused := func(c gen.InvoicesConflictProblem) (gen.PostInvoicesByIdSendEfakturaResponseObject, error) {
		return gen.PostInvoicesByIdSendEfaktura409ApplicationProblemPlusJSONResponse(c), nil
	}
	unavailable := func() (gen.PostInvoicesByIdSendEfakturaResponseObject, error) {
		return gen.PostInvoicesByIdSendEfaktura503ApplicationProblemPlusJSONResponse(efakturaUnavailable()), nil
	}

	// 1. An installation that cannot send eFaktura reads no document.
	q := store.New(s.deps.Pool)
	available, _, _, err := s.efakturaAvailable(ctx, q)
	if err != nil {
		return nil, err
	}
	if !available {
		return unavailable()
	}

	// 2. The document, issued, an invoice, its customer not anonymised.
	inv, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesByIdSendEfaktura404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	if inv.Status != statusIssued {
		return refused(conflict(codeInvoiceDraft, cannotSendEfakturaTitle, "A draft is not a sales document: issue it before sending it."))
	}
	if inv.Kind == kindCreditNote {
		return refused(efakturaRefused(codeEfakturaCreditNote,
			"Credit notes cannot be sent as eFaktura: the network carries none. Send it by e-mail or post."))
	}
	erased, err := q.CustomerErased(ctx, inv.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read whether customer %d was erased: %w", inv.CustomerID, err)
	}
	if erased {
		return refused(efakturaRefused(codeCustomerAnonymised, "This invoice's customer has been anonymised, and is not sent to again."))
	}

	// 3-4. NOK, a KID, a due date in the window, judged in the business
	// time zone.
	if inv.Currency != "NOK" {
		return refused(efakturaRefused(codeCurrencyNotNOK, "eFaktura carries invoices in NOK only."))
	}
	if inv.Kid == nil {
		return refused(efakturaRefused(codeKidMissing, "eFaktura needs the invoice's KID, and this invoice was issued without one. Agree a KID with the bank."))
	}
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	switch dueWindowRefusal(inv, holidays.DayIn(s.deps.Clock(), loc)) {
	case codeEfakturaDueTooSoon:
		return refused(efakturaRefused(codeEfakturaDueTooSoon, "The due date must be at least three days ahead for eFaktura."))
	case codeEfakturaDueTooFar:
		return refused(efakturaRefused(codeEfakturaDueTooFar, "The due date may be at most twelve months ahead for eFaktura."))
	}

	// 5. The person, found and wanting it: the lookup refreshed outside any
	// transaction when old.
	p, cpSettings, err := s.consumerProvider(ctx)
	switch {
	case errors.Is(err, errNoConsumerProvider), errors.Is(err, errEfakturaOff), errors.Is(err, errSubmitterUnreadable):
		return unavailable()
	case err != nil:
		return nil, err
	}
	lookup, err := s.lookupForSend(ctx, q, p, inv.CustomerID)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: an eFaktura lookup failed", "invoice_id", inv.ID, "error", redactReason(err.Error()))
		c := conflict(codeEfakturaLookupFailed, "The eFaktura network could not be asked",
			"The provider could not say whether the person receives eFaktura. Nothing was queued; try again.")
		c.Status = ptr(int32(http.StatusBadGateway))
		return gen.PostInvoicesByIdSendEfaktura502ApplicationProblemPlusJSONResponse(c), nil
	}
	if lookup == nil || !sendableLookup(*lookup) {
		c := efakturaRefused(codeEfakturaNotFound,
			"The person was not found for eFaktura, blocks this issuer, or has no agreement with it. Send the invoice by e-mail or post.")
		c.EfakturaReason = ptr(notFoundReason(lookup))
		return refused(c)
	}

	// 6. No live eFaktura.
	active, err := q.ActiveEfakturaTransmissionExists(ctx, inv.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's eFaktura: %w", inv.ID, err)
	}
	if active {
		return refused(efakturaRefused(codeEfakturaAlreadySent, "This invoice is already queued, submitted, delivered or awaiting confirmation as eFaktura."))
	}

	// 7. The claim, judged outside any transaction.
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	plan, err := s.planClaim(ctx, q, p, settings, inv)
	if err != nil {
		return nil, err
	}

	// The stored PDF the shipment carries.
	pdf, problem := s.loadStoredPDF(ctx, q, inv)
	switch {
	case problem == nil:
	case problem.unavailable != nil:
		return gen.PostInvoicesByIdSendEfaktura503ApplicationProblemPlusJSONResponse(*problem.unavailable), nil
	case problem.broken:
		return gen.PostInvoicesByIdSendEfaktura500ApplicationProblemPlusJSONResponse(storedDocumentBroken()), nil
	default:
		return nil, problem.err
	}
	_ = cpSettings

	// 8. Under the document's lock and the settings' share: the
	// transmission, its claim and their events.
	var refusal *gen.InvoicesConflictProblem
	var noSettings bool
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		locked, err := txq.LockInvoice(ctx, inv.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDocumentGone
		}
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", inv.ID, err)
		}
		erased, err := txq.CustomerErased(ctx, locked.CustomerID)
		if err != nil {
			return fmt.Errorf("invoices: read whether customer %d was erased: %w", locked.CustomerID, err)
		}
		if erased {
			refusal = ptr(efakturaRefused(codeCustomerAnonymised, "This invoice's customer has been anonymised, and is not sent to again."))
			return errRefused
		}
		if _, err := txq.LockConsumerProviderCredentialsForShare(ctx); errors.Is(err, pgx.ErrNoRows) {
			noSettings = true
			return errRefused
		} else if err != nil {
			return fmt.Errorf("invoices: read the consumer provider settings: %w", err)
		}
		active, err := txq.ActiveEfakturaTransmissionExists(ctx, locked.ID)
		if err != nil {
			return fmt.Errorf("invoices: read document %d's eFaktura: %w", locked.ID, err)
		}
		if active {
			refusal = ptr(efakturaRefused(codeEfakturaAlreadySent, "This invoice is already queued, submitted, delivered or awaiting confirmation as eFaktura."))
			return errRefused
		}
		now := s.deps.Clock()
		paymentType := paymentTypeEfaktura
		if plan.mandate != nil {
			paymentType = paymentTypeBoth
		}
		t, err := txq.InsertEfakturaTransmission(ctx, store.InsertEfakturaTransmissionParams{
			InvoiceID: locked.ID, IdempotencyKey: uuid.New(), PdfSha256: pdf.sha256, PdfObjectKey: pdf.key,
			EfakturaIdentifier: *lookup.EfakturaIdentifier, ShipmentID: uuid.New(), PaymentType: paymentType,
			Now: now, CreatedByUserID: callerID(ctx),
		})
		switch {
		case db.IsUniqueViolation(err, uxTransmissionsActive):
			refusal = ptr(efakturaRefused(codeEfakturaAlreadySent, "This invoice is already queued, submitted, delivered or awaiting confirmation as eFaktura."))
			return errRefused
		case isAnonymisedRefusal(err):
			refusal = ptr(efakturaRefused(codeCustomerAnonymised, "This invoice's customer has been anonymised, and is not sent to again."))
			return errRefused
		case err != nil:
			return fmt.Errorf("invoices: queue document %d's eFaktura: %w", locked.ID, err)
		}
		if err := publishTransmissionChanged(ctx, tx, t, t.Status, now); err != nil {
			return err
		}
		if plan.mandate == nil {
			return nil
		}
		amount, err := numericFromRat(plan.amount, 2)
		if err != nil {
			return err
		}
		claim, err := txq.InsertClaim(ctx, store.InsertClaimParams{
			InvoiceID: locked.ID, TransmissionID: t.ID, MandateID: plan.mandate.ID, Amount: amount,
			DueDate: locked.DueDate, Kid: plan.mandate.Kid, Now: now,
		})
		if db.IsUniqueViolation(err, uxClaimsLive) {
			refusal = ptr(efakturaRefused(codeClaimExists,
				"The invoice already has a live AvtaleGiro claim. Wait until it is settled, or credit the invoice."))
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: record document %d's AvtaleGiro claim: %w", locked.ID, err)
		}
		return publishClaimChanged(ctx, tx, claim, claim.Status, now)
	})
	switch {
	case refusal != nil:
		return refused(*refusal)
	case noSettings:
		return unavailable()
	case errors.Is(err, errDocumentGone):
		return gen.PostInvoicesByIdSendEfaktura404Response{}, nil
	case err != nil:
		return nil, err
	}
	s.deps.Logger.InfoContext(ctx, "invoices: an invoice is queued as eFaktura", "invoice_id", inv.ID, "avtalegiro", plan.mandate != nil)

	// 9. The document with its eFaktura state.
	if inv, err = q.GetInvoice(ctx, inv.ID); err != nil {
		return nil, fmt.Errorf("invoices: re-read document %d: %w", req.Id, err)
	}
	canIssue := true // the operation's own rule
	resp, err := s.renderInvoice(ctx, q, inv, nil, nil, &canIssue, loc)
	if err != nil {
		return nil, err
	}
	out := gen.InvoicesSendEfakturaResponse{Invoice: resp, Avtalegiro: gen.InvoicesSendEfakturaAvtaleGiro{Included: plan.mandate != nil}}
	if plan.reason != "" {
		out.Avtalegiro.Reason = ptr(plan.reason)
	}
	return gen.PostInvoicesByIdSendEfaktura200JSONResponse(out), nil
}
