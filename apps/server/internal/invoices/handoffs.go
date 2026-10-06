package invoices

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the hand-off to collection (invoices payments and reminders
// design D11): recorded after it was made outside Vantigo, so no letter is
// made while it is live, and withdrawn when the claim comes back. A payment
// is still registered while it is live — the claim is still the creditor's
// (inkassoloven § 2) — and must be reported to the agency.

// The hand-off's titles.
const (
	cannotHandOffTitle      = "The invoice cannot be handed off"
	cannotWithdrawTitle     = "The hand-off cannot be withdrawn"
	invalidHandoffTitle     = "Invalid hand-off"
	invalidHandoffWithdraw  = "Invalid hand-off withdrawal"
	handedOnField           = "handedOn"
	withdrawnOnField        = "withdrawnOn"
	collectionAgencyMaxRune = 200
)

// handoffFields is a hand-off's body as judged before the invoice is read.
type handoffFields struct {
	handedOn                time.Time
	agency, reference, note string
	acknowledgeNotDelivered bool
}

// parseHandoff runs D11's field rules that need no invoice, every failure
// collected: handedOn present and not after today, the agency 1-200
// characters, the reference at most 100, the note at most 500.
func parseHandoff(body gen.InvoicesHandoffRequest, today time.Time) (handoffFields, map[string][]string) {
	var errs map[string][]string
	f := handoffFields{
		handedOn: utcDay(body.HandedOn.Time), agency: strings.TrimSpace(body.Agency),
		reference: optionalText(body.AgencyReference), note: optionalText(body.Note),
		acknowledgeNotDelivered: body.AcknowledgeNotDelivered != nil && *body.AcknowledgeNotDelivered,
	}
	switch {
	case body.HandedOn.IsZero():
		errs = withFieldError(errs, handedOnField, "A hand-off needs the day it was made")
	case f.handedOn.After(today):
		errs = withFieldError(errs, handedOnField, "A hand-off is recorded today at the latest, "+today.Format(time.DateOnly))
	}
	if f.agency == "" {
		errs = withFieldError(errs, "agency", "A hand-off names the collection agency")
	} else if msg := maxLength("The agency", f.agency, collectionAgencyMaxRune); msg != "" {
		errs = withFieldError(errs, "agency", msg)
	}
	if msg := maxLength("The agency's reference", f.reference, 100); msg != "" {
		errs = withFieldError(errs, "agencyReference", msg)
	}
	if msg := maxLength("The note", f.note, 500); msg != "" {
		errs = withFieldError(errs, "note", msg)
	}
	return f, errs
}

// PostInvoicesByIdCollection Record the hand-off to collection
// (POST /api/v1/invoices/{id}/collection)
func (s *server) PostInvoicesByIdCollection(ctx context.Context, req gen.PostInvoicesByIdCollectionRequestObject) (gen.PostInvoicesByIdCollectionResponseObject, error) {
	now := s.deps.Clock()
	today := businessDay(now)
	f, errs := parseHandoff(*req.Body, today)
	if errs != nil {
		return gen.PostInvoicesByIdCollection400ApplicationProblemPlusJSONResponse(invalid(invalidHandoffTitle, errs)), nil
	}
	inv, refusal, err := issuedInvoiceFor(ctx, store.New(s.deps.Pool), req.Id, codeCreditNoteNoReminders, cannotHandOffTitle,
		"A credit note is never reminded of, so it is never handed off.",
		"A draft is not a sales document: it has no claim to hand off.")
	switch {
	case err != nil:
		return nil, err
	case refusal != nil:
		return gen.PostInvoicesByIdCollection409ApplicationProblemPlusJSONResponse(*refusal), nil
	case inv == nil:
		return gen.PostInvoicesByIdCollection404Response{}, nil
	}
	if issued := utcDay(inv.IssueDate.Time); f.handedOn.Before(issued) {
		return gen.PostInvoicesByIdCollection400ApplicationProblemPlusJSONResponse(invalid(invalidHandoffTitle, fieldError(handedOnField,
			"An invoice is handed off on or after its issue date, "+issued.Format(time.DateOnly)))), nil
	}
	var locked store.InvoicesInvoice
	var left []store.InvoicesReminder
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		if locked, err = lockIssuedInvoice(ctx, txq, inv.ID); err != nil {
			return err
		}
		open, err := openOf(ctx, txq, locked)
		if err != nil {
			return err
		}
		if open.Sign() <= 0 {
			refusal = ptr(conflict(codeInvoiceSettled, cannotHandOffTitle,
				"Nothing of this invoice is open: it is paid or credited, so there is no claim to hand off."))
			return errRefused
		}
		live, err := txq.LiveHandoffs(ctx, []int64{locked.ID})
		if err != nil {
			return fmt.Errorf("invoices: read document %d's hand-off: %w", locked.ID, err)
		}
		if len(live) > 0 {
			refusal = ptr(conflict(codeInvoiceHandedOff, cannotHandOffTitle, fmt.Sprintf(
				"This invoice was handed off to %s on %s. Withdraw that hand-off before recording another.",
				live[0].Agency, live[0].HandedOn.Time.Format(time.DateOnly))))
			return errRefused
		}
		if !f.acknowledgeNotDelivered {
			days, err := deliveriesOf(ctx, txq, locked.ID)
			if err != nil {
				return err
			}
			due := utcDay(locked.DueDate.Time)
			if len(days) == 0 || days[0].After(due) {
				refusal = ptr(conflict(codeInvoiceNotDelivered, cannotHandOffTitle, fmt.Sprintf(
					"No delivery of this invoice is recorded on or before its due date, %s, so it may not have fallen due. "+
						"If it was in fact delivered, record a manual delivery first; otherwise confirm the hand-off.", due.Format(time.DateOnly))))
				return errRefused
			}
		}
		if _, err := txq.InsertHandoff(ctx, store.InsertHandoffParams{
			InvoiceID: locked.ID, HandedOn: pgDate(f.handedOn), Agency: f.agency, AgencyReference: f.reference,
			Note: f.note, CreatedAt: now, CreatedByUserID: callerID(ctx),
		}); err != nil {
			return fmt.Errorf("invoices: hand off document %d: %w", locked.ID, err)
		}
		_, left, err = withdrawInFlight(ctx, txq, locked.ID, withdrawnHandedOff, now)
		return err
	})
	switch {
	case refusal != nil:
		return gen.PostInvoicesByIdCollection409ApplicationProblemPlusJSONResponse(*refusal), nil
	case errors.Is(err, errDocumentGone):
		return gen.PostInvoicesByIdCollection404Response{}, nil
	case err != nil:
		return nil, err
	}
	result, err := s.holdOrHandoffResult(ctx, locked, left)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdCollection200JSONResponse(result), nil
}

// errWithdrawnBeforeHandedOn is a withdrawal dated before its hand-off,
// found under the lock: a 400 on withdrawnOn.
var errWithdrawnBeforeHandedOn = errors.New("invoices: a hand-off withdrawn before it was made")

// PostInvoicesByIdCollectionWithdraw Withdraw the hand-off
// (POST /api/v1/invoices/{id}/collection/withdraw)
func (s *server) PostInvoicesByIdCollectionWithdraw(ctx context.Context, req gen.PostInvoicesByIdCollectionWithdrawRequestObject) (gen.PostInvoicesByIdCollectionWithdrawResponseObject, error) {
	now := s.deps.Clock()
	today := businessDay(now)
	on := utcDay(req.Body.WithdrawnOn.Time)
	var errs map[string][]string
	switch {
	case req.Body.WithdrawnOn.IsZero():
		errs = withFieldError(errs, withdrawnOnField, "A withdrawal needs the day the claim came back")
	case on.After(today):
		errs = withFieldError(errs, withdrawnOnField, "A withdrawal is recorded today at the latest, "+today.Format(time.DateOnly))
	}
	reason := strings.TrimSpace(req.Body.Reason)
	if reason == "" {
		errs = withFieldError(errs, "reason", "A withdrawal needs a reason")
	} else if msg := maxLength("A reason", reason, 200); msg != "" {
		errs = withFieldError(errs, "reason", msg)
	}
	if errs != nil {
		return gen.PostInvoicesByIdCollectionWithdraw400ApplicationProblemPlusJSONResponse(invalid(invalidHandoffWithdraw, errs)), nil
	}
	var refusal *gen.InvoicesConflictProblem
	var handedOn time.Time
	var locked store.InvoicesInvoice
	var left []store.InvoicesReminder
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		if locked, err = lockIssuedInvoice(ctx, txq, req.Id); err != nil {
			return err
		}
		live, err := txq.LiveHandoffs(ctx, []int64{locked.ID})
		if err != nil {
			return fmt.Errorf("invoices: read document %d's hand-off: %w", locked.ID, err)
		}
		if len(live) == 0 {
			refusal = ptr(conflict(codeInvoiceNotHandedOff, cannotWithdrawTitle, "This invoice is not handed off: there is no hand-off to withdraw."))
			return errRefused
		}
		if handedOn = utcDay(live[0].HandedOn.Time); on.Before(handedOn) {
			return errWithdrawnBeforeHandedOn
		}
		if _, err := txq.WithdrawHandoff(ctx, store.WithdrawHandoffParams{
			InvoiceID: locked.ID, WithdrawnOn: pgDate(on), WithdrawnByUserID: callerID(ctx), WithdrawalReason: reason,
		}); err != nil {
			return fmt.Errorf("invoices: withdraw document %d's hand-off: %w", locked.ID, err)
		}
		left, err = txq.PrintedLettersOf(ctx, store.PrintedLettersOfParams{InvoiceID: locked.ID, Now: now})
		if err != nil {
			return fmt.Errorf("invoices: read document %d's letters left: %w", locked.ID, err)
		}
		return nil
	})
	switch {
	case refusal != nil:
		return gen.PostInvoicesByIdCollectionWithdraw409ApplicationProblemPlusJSONResponse(*refusal), nil
	case errors.Is(err, errDocumentGone):
		return gen.PostInvoicesByIdCollectionWithdraw404Response{}, nil
	case errors.Is(err, errWithdrawnBeforeHandedOn):
		return gen.PostInvoicesByIdCollectionWithdraw400ApplicationProblemPlusJSONResponse(invalid(invalidHandoffWithdraw, fieldError(withdrawnOnField,
			"A hand-off is withdrawn on or after the day it was made, "+handedOn.Format(time.DateOnly)))), nil
	case err != nil:
		return nil, err
	}
	result, err := s.holdOrHandoffResult(ctx, locked, left)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdCollectionWithdraw200JSONResponse(result), nil
}
