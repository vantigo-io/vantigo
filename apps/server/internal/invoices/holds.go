package invoices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the hold (invoices payments and reminders design D11): an
// issued invoice marked disputed, so no letter is made for it while the hold
// is live, and its lift, which answers whether the objection was obviously
// groundless — when it was not, every fee and compensation claimed is waived
// and charges stay barred on the invoice for good (inkassoloven § 17 second
// paragraph; the new act's § 18). Late interest is not a cost and keeps
// running; payments and bank imports are still taken.
//
// A hold, a lift, a hand-off and its withdrawal take the invoice FOR UPDATE
// (lockInvoice), then touch its letters — withdrawn — and, on a barring lift,
// insert the waivers (D18). One clock read per request.

// The codes and titles of the holds' and the hand-off's refusals.
const (
	codeInvoiceOnHold       = "invoice_on_hold"
	codeInvoiceNotOnHold    = "invoice_not_on_hold"
	codeInvoiceHandedOff    = "invoice_handed_off"
	codeInvoiceNotHandedOff = "invoice_not_handed_off"
	codeInvoiceNotDelivered = "invoice_not_delivered"

	cannotHoldTitle  = "The invoice cannot be held"
	cannotLiftTitle  = "The hold cannot be lifted"
	invalidHoldTitle = "Invalid hold"
	invalidLiftTitle = "Invalid lift"
)

// The reasons the module writes on the letters it withdraws (plan reading
// 44), with no user: a person's withdrawal writes their own words.
const (
	withdrawnOnHold    = "on_hold"
	withdrawnHandedOff = "handed_off"
)

// withdrawInFlight withdraws invoice invoiceID's letters still in flight —
// queued, awaiting print or failed — with reason and no user, at at; the
// caller holds the invoice FOR UPDATE (lockInvoice), so no run or dispatch
// is between its reads and this write. A printed letter is left for the
// posting's re-judge (plan reading 9), and a letter being sent — queued, its
// facts written, under a lease live at at — is left to become sent (plan
// reading 45). It answers the letters it withdrew, by id, each reported to
// the lock-order seam as the UPDATE locked it, and the letters it left, by
// sequence. A hold, a hand-off and the erase (customer_anonymised) call it.
func withdrawInFlight(ctx context.Context, txq *store.Queries, invoiceID int64, reason string, at time.Time) (withdrawn []int64, left []store.InvoicesReminder, err error) {
	withdrawn, err = txq.WithdrawLettersInFlight(ctx, store.WithdrawLettersInFlightParams{
		InvoiceID: invoiceID, WithdrawnAt: at, WithdrawalReason: reason, Now: at,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("invoices: withdraw document %d's letters in flight: %w", invoiceID, err)
	}
	slices.Sort(withdrawn)
	for _, id := range withdrawn {
		noteLock(ctx, "reminder", strconv.FormatInt(id, 10))
	}
	if left, err = txq.PrintedLettersOf(ctx, store.PrintedLettersOfParams{InvoiceID: invoiceID, Now: at}); err != nil {
		return nil, nil, fmt.Errorf("invoices: read document %d's letters left: %w", invoiceID, err)
	}
	return withdrawn, left, nil
}

// lettersLeftWire is the letters a withdrawal left, on the wire.
func lettersLeftWire(left []store.InvoicesReminder) []gen.InvoicesLetterLeft {
	out := make([]gen.InvoicesLetterLeft, 0, len(left))
	for _, l := range left {
		out = append(out, gen.InvoicesLetterLeft{
			ReminderId: l.ID, Status: gen.InvoicesLetterLeftStatus(l.Status), PrintBatchId: l.PrintBatchID,
		})
	}
	return out
}

// holdOrHandoffResult is what the four writes answer, rendered after their
// transaction: the document and the letters left.
func (s *server) holdOrHandoffResult(ctx context.Context, inv store.InvoicesInvoice, left []store.InvoicesReminder) (gen.InvoicesHoldOrHandoffResult, error) {
	resp, err := s.invoiceResponse(ctx, store.New(s.deps.Pool), inv, nil)
	if err != nil {
		return gen.InvoicesHoldOrHandoffResult{}, err
	}
	return gen.InvoicesHoldOrHandoffResult{Invoice: resp, LettersLeft: lettersLeftWire(left)}, nil
}

// withHoldAndHandoff answers an issued invoice's latest hold and hand-off,
// live or ended, on resp (D11); each absent when there was none.
func withHoldAndHandoff(ctx context.Context, q *store.Queries, invoiceID int64, resp *gen.InvoicesInvoiceResponse) error {
	holds, err := q.LatestHoldOf(ctx, invoiceID)
	if err != nil {
		return fmt.Errorf("invoices: read document %d's hold: %w", invoiceID, err)
	}
	if len(holds) > 0 {
		resp.Hold = ptr(holdWire(holds[0]))
	}
	handoffs, err := q.LatestHandoffOf(ctx, invoiceID)
	if err != nil {
		return fmt.Errorf("invoices: read document %d's hand-off: %w", invoiceID, err)
	}
	if len(handoffs) > 0 {
		resp.Handoff = ptr(handoffWire(handoffs[0]))
	}
	return nil
}

// lockIssuedInvoice is the four writes' first statement: the invoice FOR
// UPDATE, errDocumentGone for none.
func lockIssuedInvoice(ctx context.Context, txq *store.Queries, id int64) (store.InvoicesInvoice, error) {
	inv, err := lockInvoice(ctx, txq, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return inv, errDocumentGone
	}
	if err != nil {
		return inv, fmt.Errorf("invoices: lock document %d: %w", id, err)
	}
	return inv, nil
}

// PostInvoicesByIdHold Hold a disputed invoice
// (POST /api/v1/invoices/{id}/hold)
func (s *server) PostInvoicesByIdHold(ctx context.Context, req gen.PostInvoicesByIdHoldRequestObject) (gen.PostInvoicesByIdHoldResponseObject, error) {
	note := strings.TrimSpace(req.Body.Note)
	switch {
	case note == "":
		return gen.PostInvoicesByIdHold400ApplicationProblemPlusJSONResponse(invalid(invalidHoldTitle,
			fieldError("note", "A hold needs a note saying what the customer disputes"))), nil
	case maxLength("The note", note, 500) != "":
		return gen.PostInvoicesByIdHold400ApplicationProblemPlusJSONResponse(invalid(invalidHoldTitle,
			fieldError("note", maxLength("The note", note, 500)))), nil
	}
	inv, refusal, err := issuedInvoiceFor(ctx, store.New(s.deps.Pool), req.Id, codeCreditNoteNoReminders, cannotHoldTitle,
		"A credit note is never reminded of, so it is never held.",
		"A draft is not a sales document: it has no claim to dispute.")
	switch {
	case err != nil:
		return nil, err
	case refusal != nil:
		return gen.PostInvoicesByIdHold409ApplicationProblemPlusJSONResponse(*refusal), nil
	case inv == nil:
		return gen.PostInvoicesByIdHold404Response{}, nil
	}
	now := s.deps.Clock()
	var locked store.InvoicesInvoice
	var left []store.InvoicesReminder
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		if locked, err = lockIssuedInvoice(ctx, txq, inv.ID); err != nil {
			return err
		}
		live, err := txq.LiveHolds(ctx, []int64{locked.ID})
		if err != nil {
			return fmt.Errorf("invoices: read document %d's hold: %w", locked.ID, err)
		}
		if len(live) > 0 {
			refusal = ptr(conflict(codeInvoiceOnHold, cannotHoldTitle, fmt.Sprintf(
				"This invoice has been on hold since %s. Lift that hold before placing another.", businessDay(live[0].PlacedAt).Format(time.DateOnly))))
			return errRefused
		}
		if _, err := txq.InsertHold(ctx, store.InsertHoldParams{
			InvoiceID: locked.ID, Note: note, PlacedAt: now, PlacedByUserID: callerID(ctx),
		}); err != nil {
			return fmt.Errorf("invoices: hold document %d: %w", locked.ID, err)
		}
		_, left, err = withdrawInFlight(ctx, txq, locked.ID, withdrawnOnHold, now)
		return err
	})
	switch {
	case refusal != nil:
		return gen.PostInvoicesByIdHold409ApplicationProblemPlusJSONResponse(*refusal), nil
	case errors.Is(err, errDocumentGone):
		return gen.PostInvoicesByIdHold404Response{}, nil
	case err != nil:
		return nil, err
	}
	result, err := s.holdOrHandoffResult(ctx, locked, left)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdHold200JSONResponse(result), nil
}

// parseLift judges a lift's body: chargesAllowed present, true or false —
// never read as false when absent, since false waives charges — and a note
// within its column.
func parseLift(body gen.InvoicesHoldLiftRequest) (allowed bool, note string, errs map[string][]string) {
	raw := bytes.TrimSpace(body.ChargesAllowed)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &allowed) != nil {
		errs = withFieldError(errs, "chargesAllowed", "Say whether the objection was obviously groundless: true or false")
	}
	note = optionalText(body.Note)
	if msg := maxLength("The note", note, 500); msg != "" {
		errs = withFieldError(errs, "note", msg)
	}
	return allowed, note, errs
}

// PostInvoicesByIdHoldLift Lift a hold
// (POST /api/v1/invoices/{id}/hold/lift)
func (s *server) PostInvoicesByIdHoldLift(ctx context.Context, req gen.PostInvoicesByIdHoldLiftRequestObject) (gen.PostInvoicesByIdHoldLiftResponseObject, error) {
	allowed, note, errs := parseLift(*req.Body)
	if errs != nil {
		return gen.PostInvoicesByIdHoldLift400ApplicationProblemPlusJSONResponse(invalid(invalidLiftTitle, errs)), nil
	}
	now := s.deps.Clock()
	by := callerID(ctx)
	var refusal *gen.InvoicesConflictProblem
	var locked store.InvoicesInvoice
	var left []store.InvoicesReminder
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		if locked, err = lockIssuedInvoice(ctx, txq, req.Id); err != nil {
			return err
		}
		lifted, err := txq.LiftHold(ctx, store.LiftHoldParams{
			InvoiceID: locked.ID, LiftedAt: now, LiftedByUserID: by, LiftNote: note, ChargesAllowed: allowed,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			refusal = ptr(conflict(codeInvoiceNotOnHold, cannotLiftTitle, "This invoice is not on hold: there is no hold to lift."))
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: lift document %d's hold: %w", locked.ID, err)
		}
		if !allowed {
			// The objection had reasonable grounds: every fee and
			// compensation claimed is waived, and the engine bars charges
			// on the invoice for good from this lifted hold (RuleHolds).
			if problem, err := waiveClaimedCharges(ctx, txq, locked.ID, deref(lifted.LiftNote), by, now); err != nil {
				return err
			} else if problem != nil {
				// waiveCharges judges what ChargesClaimedOf just read under
				// the same lock: a refusal here is the server's own error.
				return fmt.Errorf("invoices: the barring lift of %d was refused: %s", locked.ID, deref(problem.Detail))
			}
		}
		left, err = txq.PrintedLettersOf(ctx, store.PrintedLettersOfParams{InvoiceID: locked.ID, Now: now})
		if err != nil {
			return fmt.Errorf("invoices: read document %d's letters left: %w", locked.ID, err)
		}
		return nil
	})
	switch {
	case refusal != nil:
		return gen.PostInvoicesByIdHoldLift409ApplicationProblemPlusJSONResponse(*refusal), nil
	case errors.Is(err, errDocumentGone):
		return gen.PostInvoicesByIdHoldLift404Response{}, nil
	case err != nil:
		return nil, err
	}
	result, err := s.holdOrHandoffResult(ctx, locked, left)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdHoldLift200JSONResponse(result), nil
}

// waiveClaimedCharges waives every fee and compensation invoiceID's sent
// letters claimed and no waiver released yet, objection_upheld (D11, B3),
// through waiveCharges — which sees only sent letters and refuses a charge
// waived already, so those are skipped here. Interest is never waived by it.
// The caller holds the invoice FOR UPDATE.
func waiveClaimedCharges(ctx context.Context, txq *store.Queries, invoiceID int64, note string, by uuid.UUID, at time.Time) (*gen.InvoicesConflictProblem, error) {
	claimed, err := txq.ChargesClaimedOf(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's charges claimed: %w", invoiceID, err)
	}
	if len(claimed) == 0 {
		return nil, nil
	}
	ws := make([]waiverRequest, 0, len(claimed))
	for _, c := range claimed {
		kind := reminderrules.WaiverFee
		if c.Kind == reminderrules.WaiverCompensation {
			kind = reminderrules.WaiverCompensation
		}
		ws = append(ws, waiverRequest{reminderID: c.ReminderID, kind: kind})
	}
	return waiveCharges(ctx, txq, invoiceID, ws, waiverObjectionUpheld, note, by, at)
}

// waiverObjectionUpheld is the reason a barring lift's waivers carry.
const waiverObjectionUpheld = "objection_upheld"
