package invoices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is a letter's own operations (invoices payments and reminders
// design D10, plan reading 12): the list, the PDF of a printed or sent
// letter, a person's withdrawal and the retry of a failed letter. The two
// writes lock the letter alone (D18): the worker re-reads its status after
// its own locks, so a letter withdrawn before its dispatch takes it is never
// sent, and one being sent — queued, its facts written, under a live lease
// (plan reading 45) — is never withdrawn.

// The letters' refusals.
const (
	codeReminderNotWithdrawable = "reminder_not_withdrawable"
	codeReminderNotFailed       = "reminder_not_failed"
	codeReminderNotSent         = "reminder_not_sent"

	cannotWithdrawLetterTitle = "The letter cannot be withdrawn"
	cannotRetryLetterTitle    = "The letter cannot be retried"
	noLetterPDFTitle          = "The letter has no PDF"
	invalidWithdrawLetter     = "Invalid withdrawal"
)

// beingSent is plan reading 45's predicate on a letter as read at now:
// queued, its facts written, under a lease still live.
func beingSent(r store.InvoicesReminder, now time.Time) bool {
	return r.Status == reminderrules.StatusQueued && r.SentOn.Valid && r.LeaseUntil != nil && r.LeaseUntil.After(now)
}

// GetInvoicesReminders List the reminder letters
// (GET /api/v1/invoices/reminders)
func (s *server) GetInvoicesReminders(ctx context.Context, req gen.GetInvoicesRemindersRequestObject) (gen.GetInvoicesRemindersResponseObject, error) {
	p := req.Params
	errs := validatePageParams(p.Page, p.PageSize)
	if p.Status != nil && !p.Status.Valid() {
		errs = append(errs, "status is queued, awaiting_print, printed, sent, withdrawn or failed.")
	}
	if p.Channel != nil && !p.Channel.Valid() {
		errs = append(errs, "channel is email or paper.")
	}
	if len(errs) > 0 {
		return gen.GetInvoicesReminders400ApplicationProblemPlusJSONResponse(apicommon.Problem(invalidQueryTitle, strings.Join(errs, " "))), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)
	filter := store.CountRemindersParams{InvoiceID: p.InvoiceId, RunID: p.RunId}
	if p.Status != nil {
		filter.Status = ptr(string(*p.Status))
	}
	if p.Channel != nil {
		filter.Channel = ptr(string(*p.Channel))
	}
	q := store.New(s.deps.Pool)
	total, err := q.CountReminders(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("invoices: count the letters: %w", err)
	}
	rows, err := q.ListReminders(ctx, store.ListRemindersParams{
		Status: filter.Status, Channel: filter.Channel, InvoiceID: filter.InvoiceID, RunID: filter.RunID,
		PageOffset: (page - 1) * pageSize, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: list the letters: %w", err)
	}
	showRecipient := s.has(ctx, "invoices:payments")
	data := make([]gen.InvoicesReminder, 0, len(rows))
	for _, r := range rows {
		w, err := reminderWire(r, showRecipient)
		if err != nil {
			return nil, err
		}
		data = append(data, w)
	}
	return gen.GetInvoicesReminders200JSONResponse(gen.PaginatedResponseOfInvoicesReminder{
		Data: data, Pagination: apicommon.Pagination(page, pageSize, total),
	}), nil
}

// letterPDF is a letter's PDF with the headers a download answers with.
type letterPDF struct {
	body        gen.GetInvoicesRemindersByIdPdf200ApplicationpdfResponse
	disposition string
}

func (r letterPDF) VisitGetInvoicesRemindersByIdPdfResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	return r.body.VisitGetInvoicesRemindersByIdPdfResponse(w)
}

// GetInvoicesRemindersByIdPdf Download a reminder letter's PDF
// (GET /api/v1/invoices/reminders/{id}/pdf)
//
// The stored object a printed or sent letter went with, read whole and
// verified against its hash: never rendered again, so what is downloaded is
// what was mailed or printed — a retry that stored a second object leaves
// the row naming the one it mailed.
func (s *server) GetInvoicesRemindersByIdPdf(ctx context.Context, req gen.GetInvoicesRemindersByIdPdfRequestObject) (gen.GetInvoicesRemindersByIdPdfResponseObject, error) {
	q := store.New(s.deps.Pool)
	r, err := q.GetReminder(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetInvoicesRemindersByIdPdf404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read letter %d: %w", req.Id, err)
	}
	if r.Status != reminderrules.StatusPrinted && r.Status != reminderrules.StatusSent {
		return gen.GetInvoicesRemindersByIdPdf409ApplicationProblemPlusJSONResponse(conflict(codeReminderNotSent, noLetterPDFTitle,
			"The letter is neither printed nor sent, so it has no PDF yet.")), nil
	}
	if !s.storageConfigured {
		return gen.GetInvoicesRemindersByIdPdf503ApplicationProblemPlusJSONResponse(storageUnavailable("This installation has no object store.")), nil
	}
	if r.PdfObjectKey == nil || r.PdfSha256 == nil {
		s.deps.Logger.ErrorContext(ctx, "invoices: a printed or sent letter has no stored PDF", "reminder_id", r.ID)
		return gen.GetInvoicesRemindersByIdPdf500ApplicationProblemPlusJSONResponse(storedDocumentBroken()), nil
	}
	rc, err := s.objectGet(ctx, *r.PdfObjectKey)
	switch {
	case errors.Is(err, storage.ErrNotExist):
		s.deps.Logger.ErrorContext(ctx, "invoices: a letter's stored PDF is gone", "reminder_id", r.ID, "key", *r.PdfObjectKey)
		return gen.GetInvoicesRemindersByIdPdf500ApplicationProblemPlusJSONResponse(storedDocumentBroken()), nil
	case err != nil:
		s.deps.Logger.WarnContext(ctx, "invoices: the document store could not be read", "reminder_id", r.ID, "error", err.Error())
		return gen.GetInvoicesRemindersByIdPdf503ApplicationProblemPlusJSONResponse(storageUnavailable("The document store could not be read. Try again.")), nil
	}
	body, err := io.ReadAll(io.LimitReader(rc, maxStoredPDF+1))
	_ = rc.Close()
	if err != nil {
		return gen.GetInvoicesRemindersByIdPdf503ApplicationProblemPlusJSONResponse(storageUnavailable("The document store could not be read. Try again.")), nil
	}
	sum := sha256.Sum256(body)
	if len(body) > maxStoredPDF || hex.EncodeToString(sum[:]) != *r.PdfSha256 {
		s.deps.Logger.ErrorContext(ctx, "invoices: a letter's stored PDF does not match its hash", "reminder_id", r.ID, "key", *r.PdfObjectKey)
		return gen.GetInvoicesRemindersByIdPdf500ApplicationProblemPlusJSONResponse(storedDocumentBroken()), nil
	}
	inv, err := q.GetInvoice(ctx, r.InvoiceID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read letter %d's document: %w", r.ID, err)
	}
	return letterPDF{
		body:        gen.GetInvoicesRemindersByIdPdf200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		disposition: fmt.Sprintf("attachment; filename=%q", letterFileName(r, inv)),
	}, nil
}

// letterFileName is a letter's PDF's name in its language: purring-,
// inkassovarsel-, reminder- or collection-notice-, the invoice's number and
// the letter's sequence.
func letterFileName(r store.InvoicesReminder, inv store.InvoicesInvoice) string {
	words := reminderText(strings.TrimSpace(r.Language), reminderrules.Level(r.Level), r.AnnouncesCollection)
	number := int64(0)
	if inv.Number != nil {
		number = *inv.Number
	}
	return fmt.Sprintf("%s-%d-%d.pdf", words.fileStem, number, r.Sequence)
}

// PostInvoicesRemindersByIdWithdraw Withdraw a reminder letter
// (POST /api/v1/invoices/reminders/{id}/withdraw)
func (s *server) PostInvoicesRemindersByIdWithdraw(ctx context.Context, req gen.PostInvoicesRemindersByIdWithdrawRequestObject) (gen.PostInvoicesRemindersByIdWithdrawResponseObject, error) {
	reason := strings.TrimSpace(req.Body.Reason)
	switch {
	case reason == "":
		return gen.PostInvoicesRemindersByIdWithdraw400ApplicationProblemPlusJSONResponse(invalid(invalidWithdrawLetter,
			fieldError("reason", "A withdrawal needs a reason"))), nil
	case maxLength("The reason", reason, 200) != "":
		return gen.PostInvoicesRemindersByIdWithdraw400ApplicationProblemPlusJSONResponse(invalid(invalidWithdrawLetter,
			fieldError("reason", maxLength("The reason", reason, 200)))), nil
	}
	now := s.deps.Clock()
	by := callerID(ctx)
	var withdrawn store.InvoicesReminder
	var refusal *gen.InvoicesConflictProblem
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		letter, err := lockReminder(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		switch {
		case beingSent(letter, now):
			refusal = ptr(conflict(codeReminderNotWithdrawable, cannotWithdrawLetterTitle,
				"The letter is being sent by e-mail right now. It will be sent, or retried and withdrawable again; read it again in a minute."))
			return errRefused
		case letter.Status == reminderrules.StatusSent, letter.Status == reminderrules.StatusWithdrawn:
			refusal = ptr(conflict(codeReminderNotWithdrawable, cannotWithdrawLetterTitle,
				fmt.Sprintf("The letter is %s already, and stays so.", letter.Status)))
			return errRefused
		}
		withdrawn, err = txq.WithdrawReminder(ctx, store.WithdrawReminderParams{
			WithdrawnAt: now, WithdrawnByUserID: &by, WithdrawalReason: reason, ID: letter.ID,
		})
		if err != nil {
			return fmt.Errorf("invoices: withdraw letter %d: %w", letter.ID, err)
		}
		return nil
	})
	switch {
	case refusal != nil:
		return gen.PostInvoicesRemindersByIdWithdraw409ApplicationProblemPlusJSONResponse(*refusal), nil
	case errors.Is(err, pgx.ErrNoRows):
		return gen.PostInvoicesRemindersByIdWithdraw404Response{}, nil
	case err != nil:
		return nil, err
	}
	w, err := reminderWire(withdrawn, true)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesRemindersByIdWithdraw200JSONResponse(w), nil
}

// PostInvoicesRemindersByIdRetry Retry a failed reminder letter
// (POST /api/v1/invoices/reminders/{id}/retry)
func (s *server) PostInvoicesRemindersByIdRetry(ctx context.Context, req gen.PostInvoicesRemindersByIdRetryRequestObject) (gen.PostInvoicesRemindersByIdRetryResponseObject, error) {
	now := s.deps.Clock()
	var retried store.InvoicesReminder
	var refusal *gen.InvoicesConflictProblem
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		letter, err := lockReminder(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if letter.Status != reminderrules.StatusFailed {
			refusal = ptr(conflict(codeReminderNotFailed, cannotRetryLetterTitle,
				fmt.Sprintf("Only a failed letter is retried; this one is %s.", letter.Status)))
			return errRefused
		}
		if retried, err = txq.RetryReminder(ctx, store.RetryReminderParams{Now: now, ID: letter.ID}); err != nil {
			return fmt.Errorf("invoices: retry letter %d: %w", letter.ID, err)
		}
		return nil
	})
	switch {
	case refusal != nil:
		return gen.PostInvoicesRemindersByIdRetry409ApplicationProblemPlusJSONResponse(*refusal), nil
	case errors.Is(err, pgx.ErrNoRows):
		return gen.PostInvoicesRemindersByIdRetry404Response{}, nil
	case err != nil:
		return nil, err
	}
	w, err := reminderWire(retried, true)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesRemindersByIdRetry200JSONResponse(w), nil
}
