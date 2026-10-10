package invoices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is paper (invoices payments and reminders design D10, reading
// 39): a paper letter is not sent until it is posted. A person prints the
// letters awaiting print for the day they will be posted — a print batch —
// and each letter is judged again on that day, under its invoice's lock and
// its own, and given the facts of that day: sent_on is the posting day, the
// deadline runs from it, the fee is judged then. The batch's PDF is rendered
// from the letters' rows, as often as it is asked for. When the person
// confirms the batch was posted on that very day, its letters are sent —
// judged once more under the batch's locks, and a charge the day no longer
// supports waived claimed_in_error; posted on any other day, the batch is
// reprinted and its letters printed again for the right one.
//
// The lock order (D18): a batch's letter — its invoice FOR UPDATE, then the
// letter, then the collection rates it relies on FOR KEY SHARE; the posting —
// the batch FOR NO KEY UPDATE, its letters' invoices in descending id, then
// the letters; the reprint — the batch, then its printed letters, and no
// invoice (PostInvoicesReminderPrintBatchesByIdReprint says why that is safe).
// Every PDF is rendered and stored outside any transaction.

// The paper letters' refusals.
const (
	codeReminderNotAwaitingPrint = "reminder_not_awaiting_print"
	codeReminderPostedEarly      = "reminder_posted_early"
	codeReminderPostedLate       = "reminder_posted_late"
	codePrintBatchClosed         = "print_batch_closed"

	invalidPrintBatchTitle = "Invalid print batch"
	invalidPostedTitle     = "Invalid posting"
	cannotPrintTitle       = "The letters cannot be printed"
	cannotPostTitle        = "The batch cannot be confirmed posted"
	cannotReprintTitle     = "The batch cannot be reprinted"
)

const (
	// maxPrintBatchLetters is how many letters one batch prints at most.
	maxPrintBatchLetters = 200
	// maxPostOnDays is how far ahead a batch may be printed for.
	maxPostOnDays = 7

	// leftNotAwaitingPrint is a letter a batch left out because it was no
	// longer awaiting print under its lock: withdrawn, or printed by another
	// batch, meanwhile.
	leftNotAwaitingPrint = "not_awaiting_print"
	// waivedChargesBarred is a posted letter whose charges a lift barred
	// since it was printed.
	waivedChargesBarred = "charges_barred"
)

// postedAfterBatchLock is called inside the posting's transaction right
// after it has locked the batch and before it locks the letters' invoices,
// with the batch's id, so a race test can hold the posting there; an error
// rolls the posting back. nil in production.
var postedAfterBatchLock func(ctx context.Context, batchID int64) error

// batchPDFPath is where a batch's combined PDF is downloaded.
func batchPDFPath(id int64) string {
	return fmt.Sprintf("/api/v1/invoices/reminder-print-batches/%d/pdf", id)
}

// GetInvoicesReminderPrintBatches List the print batches
// (GET /api/v1/invoices/reminder-print-batches)
func (s *server) GetInvoicesReminderPrintBatches(ctx context.Context, req gen.GetInvoicesReminderPrintBatchesRequestObject) (gen.GetInvoicesReminderPrintBatchesResponseObject, error) {
	p := req.Params
	if errs := validatePageParams(p.Page, p.PageSize); len(errs) > 0 {
		return gen.GetInvoicesReminderPrintBatches400ApplicationProblemPlusJSONResponse(apicommon.Problem(invalidQueryTitle, strings.Join(errs, " "))), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)
	var total int32
	var batches []store.InvoicesReminderPrintBatch
	var letters []store.InvoicesReminder
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if total, err = q.CountPrintBatches(ctx, p.Posted); err != nil {
			return fmt.Errorf("invoices: count the print batches: %w", err)
		}
		if batches, err = q.ListPrintBatches(ctx, store.ListPrintBatchesParams{
			Posted: p.Posted, PageOffset: (page - 1) * pageSize, PageSize: pageSize,
		}); err != nil {
			return fmt.Errorf("invoices: list the print batches: %w", err)
		}
		ids := make([]int64, 0, len(batches))
		for _, b := range batches {
			ids = append(ids, b.ID)
		}
		if letters, err = q.LettersOfBatches(ctx, ids); err != nil {
			return fmt.Errorf("invoices: read the print batches' letters: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	data := make([]gen.InvoicesPrintBatch, 0, len(batches))
	for _, b := range batches {
		w, err := printBatchWire(b, slices.DeleteFunc(slices.Clone(letters), func(l store.InvoicesReminder) bool {
			return l.PrintBatchID == nil || *l.PrintBatchID != b.ID
		}))
		if err != nil {
			return nil, err
		}
		data = append(data, w)
	}
	return gen.GetInvoicesReminderPrintBatches200JSONResponse(gen.PaginatedResponseOfInvoicesPrintBatch{
		Data: data, Pagination: apicommon.Pagination(page, pageSize, total),
	}), nil
}

// PostInvoicesReminderPrintBatches Print paper letters for a posting day
// (POST /api/v1/invoices/reminder-print-batches)
func (s *server) PostInvoicesReminderPrintBatches(ctx context.Context, req gen.PostInvoicesReminderPrintBatchesRequestObject) (gen.PostInvoicesReminderPrintBatchesResponseObject, error) {
	now := s.deps.Clock() // the batch's one clock read (D18)
	today := businessDay(now)
	ids, postOn := req.Body.ReminderIds, utcDay(req.Body.PostOn.Time)
	var errs map[string][]string
	switch {
	case len(ids) == 0:
		errs = withFieldError(errs, "reminderIds", "A batch prints at least one letter")
	case len(ids) > maxPrintBatchLetters:
		errs = withFieldError(errs, "reminderIds", fmt.Sprintf("A batch prints at most %d letters", maxPrintBatchLetters))
	}
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			errs = withFieldError(errs, "reminderIds", fmt.Sprintf("Letter %d is named twice", id))
		}
		seen[id] = true
	}
	switch {
	case postOn.Before(today):
		errs = withFieldError(errs, "postOn", "A batch is printed for today or a later day")
	case postOn.After(today.AddDate(0, 0, maxPostOnDays)):
		errs = withFieldError(errs, "postOn", fmt.Sprintf("A batch is printed for a day at most %d days on", maxPostOnDays))
	}
	if errs != nil {
		return gen.PostInvoicesReminderPrintBatches400ApplicationProblemPlusJSONResponse(invalid(invalidPrintBatchTitle, errs)), nil
	}
	if !s.storageConfigured {
		return gen.PostInvoicesReminderPrintBatches503ApplicationProblemPlusJSONResponse(
			storageUnavailable("This installation has no object store to keep the letters in.")), nil
	}

	// Every letter awaiting print as the pool reads it, before anything is
	// written; each is judged again under its locks.
	q := store.New(s.deps.Pool)
	letters, err := q.PrintCandidates(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the letters to print: %w", err)
	}
	if len(letters) < len(ids) {
		for _, id := range ids {
			if !slices.ContainsFunc(letters, func(l store.InvoicesReminder) bool { return l.ID == id }) {
				errs = withFieldError(errs, "reminderIds", fmt.Sprintf("No letter has the id %d", id))
			}
		}
		return gen.PostInvoicesReminderPrintBatches400ApplicationProblemPlusJSONResponse(invalid(invalidPrintBatchTitle, errs)), nil
	}
	for _, l := range letters {
		if l.Status != reminderrules.StatusAwaitingPrint {
			return gen.PostInvoicesReminderPrintBatches409ApplicationProblemPlusJSONResponse(conflict(codeReminderNotAwaitingPrint, cannotPrintTitle,
				fmt.Sprintf("Letter %d is %s, not awaiting print; leave it out of the batch.", l.ID, l.Status))), nil
		}
	}

	batch, err := q.InsertPrintBatch(ctx, store.InsertPrintBatchParams{
		PostOn: pgDate(postOn), CreatedAt: now, CreatedByUserID: callerID(ctx),
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: insert the print batch: %w", err)
	}
	leftOut := []gen.InvoicesPrintBatchLeftOut{}
	var printed []int64
	for _, l := range letters {
		left, err := s.printLetter(ctx, batch.ID, l, postOn, now)
		if err != nil {
			return nil, err
		}
		if left != nil {
			leftOut = append(leftOut, *left)
			continue
		}
		printed = append(printed, l.ID)
	}
	for _, id := range printed {
		s.storePrintedLetter(ctx, batch.ID, id)
	}

	inBatch, err := q.LettersOfBatch(ctx, batch.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read print batch %d's letters: %w", batch.ID, err)
	}
	w, err := printBatchWire(batch, inBatch)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesReminderPrintBatches201JSONResponse(gen.InvoicesPrintBatchResult{
		Batch: w, PdfUrl: batchPDFPath(batch.ID), LeftOut: leftOut,
	}), nil
}

// printLetter is one letter's transaction of a print batch (D10): its
// invoice FOR UPDATE, then the letter (D18), then the collection rates in
// force on postOn FOR KEY SHARE — the rows a rate's DELETE judges "used", so
// none of them is deleted under the letter (plan reading 6) — and the
// letter judged again on postOn (judgeAndWriteFacts). A letter no longer
// awaiting print is left out as it is; one held for a rate or the review is
// left out and kept awaiting print (m9); one the engine no longer gives on
// postOn is withdrawn with the reason; every other is printed in the batch.
// It answers the letter left out, or nil when it was printed.
func (s *server) printLetter(ctx context.Context, batchID int64, letter store.InvoicesReminder, postOn, now time.Time) (*gen.InvoicesPrintBatchLeftOut, error) {
	var left *gen.InvoicesPrintBatchLeftOut
	leave := func(reason string) {
		left = &gen.InvoicesPrintBatchLeftOut{
			ReminderId: letter.ID, InvoiceId: letter.InvoiceID, Reason: gen.InvoicesPrintBatchLeftOutReason(reason),
		}
	}
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		inv, err := lockInvoice(ctx, txq, letter.InvoiceID)
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", letter.InvoiceID, err)
		}
		locked, err := lockReminder(ctx, txq, letter.ID)
		if err != nil {
			return fmt.Errorf("invoices: lock letter %d: %w", letter.ID, err)
		}
		if locked.Status != reminderrules.StatusAwaitingPrint {
			leave(leftNotAwaitingPrint)
			return nil
		}
		if err := shareCollectionRates(ctx, txq, postOn); err != nil {
			return err
		}
		j, err := s.judgeAndWriteFacts(ctx, txq, inv, locked, postOn, now)
		if err != nil {
			return err
		}
		switch j.outcome {
		case judgedHeld:
			leave(j.reason)
			if j.outdated != nil {
				left.Outdated = &gen.InvoicesOutdatedRate{Kind: j.outdated.Kind, HalfYear: j.outdated.HalfYear}
			}
			return nil
		case judgedWithdraw:
			if _, err := txq.WithdrawUnprinted(ctx, store.WithdrawUnprintedParams{
				WithdrawnAt: now, WithdrawalReason: j.reason, ID: locked.ID,
			}); err != nil {
				return fmt.Errorf("invoices: withdraw letter %d: %w", locked.ID, err)
			}
			leave(j.reason)
			return nil
		}
		if _, err := txq.MarkPrinted(ctx, store.MarkPrintedParams{PrintBatchID: batchID, ID: locked.ID}); err != nil {
			return fmt.Errorf("invoices: print letter %d: %w", locked.ID, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return left, nil
}

// storePrintedLetter renders a letter the batch printed from its row and
// stores it once under a key carrying its hash (plan reading 35), outside
// any transaction, then records it on the letter while the letter is still
// printed in the batch. A failure is logged and leaves the letter without a
// stored PDF: the batch's combined PDF is rendered from the rows, and the
// letter is printed all the same.
func (s *server) storePrintedLetter(ctx context.Context, batchID, letterID int64) {
	q := store.New(s.deps.Pool)
	warn := func(what string, err error) {
		s.deps.Logger.WarnContext(ctx, "invoices: a printed letter's PDF could not be "+what,
			"print_batch_id", batchID, "reminder_id", letterID, "error", err.Error())
	}
	letter, err := q.GetReminder(ctx, letterID)
	if err != nil {
		warn("read", err)
		return
	}
	inv, err := q.GetInvoice(ctx, letter.InvoiceID)
	if err != nil {
		warn("read", err)
		return
	}
	_, body, err := s.renderLetter(ctx, q, letter, inv)
	if err != nil {
		warn("rendered", err)
		return
	}
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	key := reminderKey(inv.ID, letter.ID, utcDay(letter.SentOn.Time), sha)
	if err := s.storeLetterPDF(ctx, key, body); err != nil {
		warn("stored", err)
		return
	}
	if _, err := q.SetPrintedPDF(ctx, store.SetPrintedPDFParams{
		PdfObjectKey: key, PdfSha256: sha, ID: letter.ID, PrintBatchID: batchID,
	}); err != nil {
		warn("recorded", err)
	}
}

// storeLetterPDF puts body under key unless something is there already: the
// key carries the hash of the bytes, so what is there is these bytes. Both
// calls are bounded together by reminderStoreTimeout.
func (s *server) storeLetterPDF(ctx context.Context, key string, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, reminderStoreTimeout)
	defer cancel()
	exists, err := s.objectExists(ctx, key)
	if err != nil || exists {
		return err
	}
	return s.objectPut(ctx, key, bytes.NewReader(body), "application/pdf")
}

// batchPDF is a batch's combined PDF with the headers a download answers
// with.
type batchPDF struct {
	body        gen.GetInvoicesReminderPrintBatchesByIdPdf200ApplicationpdfResponse
	disposition string
}

func (r batchPDF) VisitGetInvoicesReminderPrintBatchesByIdPdfResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	return r.body.VisitGetInvoicesReminderPrintBatchesByIdPdfResponse(w)
}

// GetInvoicesReminderPrintBatchesByIdPdf Download a print batch's PDF
// (GET /api/v1/invoices/reminder-print-batches/{id}/pdf)
//
// The batch's printed and sent letters — never one withdrawn since printing
// (plan reading 9) — each rendered again from its row, one after the other:
// the models read in one snapshot, the rendering after it.
func (s *server) GetInvoicesReminderPrintBatchesByIdPdf(ctx context.Context, req gen.GetInvoicesReminderPrintBatchesByIdPdfRequestObject) (gen.GetInvoicesReminderPrintBatchesByIdPdfResponseObject, error) {
	var batch store.InvoicesReminderPrintBatch
	var models []reminderModel
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if batch, err = q.GetPrintBatch(ctx, req.Id); err != nil {
			return err
		}
		letters, err := q.LettersOfBatch(ctx, batch.ID)
		if err != nil {
			return fmt.Errorf("invoices: read print batch %d's letters: %w", batch.ID, err)
		}
		invs := map[int64]store.InvoicesInvoice{}
		for _, l := range letters {
			if l.Status != reminderrules.StatusPrinted && l.Status != reminderrules.StatusSent {
				continue
			}
			inv, ok := invs[l.InvoiceID]
			if !ok {
				if inv, err = q.GetInvoice(ctx, l.InvoiceID); err != nil {
					return fmt.Errorf("invoices: read letter %d's document: %w", l.ID, err)
				}
				invs[l.InvoiceID] = inv
			}
			n, err := q.CreditedOn(ctx, store.CreditedOnParams{InvoiceID: inv.ID, Day: l.SentOn})
			if err != nil {
				return fmt.Errorf("invoices: read what was credited of document %d: %w", inv.ID, err)
			}
			credited, err := ratFromNumeric(n)
			if err != nil {
				return err
			}
			m, err := reminderModelOf(l, inv, credited)
			if err != nil {
				return err
			}
			models = append(models, m)
		}
		return nil
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.GetInvoicesReminderPrintBatchesByIdPdf404Response{}, nil
	case err != nil:
		return nil, err
	case len(models) == 0:
		return gen.GetInvoicesReminderPrintBatchesByIdPdf404Response{}, nil
	}
	postOn := utcDay(batch.PostOn.Time)
	body, err := renderLettersPDF(models, postOn, fmt.Sprintf("Reminder letters, print batch %d", batch.ID))
	if err != nil {
		return nil, err
	}
	return batchPDF{
		body: gen.GetInvoicesReminderPrintBatchesByIdPdf200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		disposition: fmt.Sprintf("attachment; filename=%q",
			fmt.Sprintf("paper-letters-%d-%s.pdf", batch.ID, postOn.Format(time.DateOnly))),
	}, nil
}

// PostInvoicesReminderPrintBatchesByIdPosted Confirm a print batch was posted
// (POST /api/v1/invoices/reminder-print-batches/{id}/posted)
//
// Every fact on the batch's letters was judged for its postOn — R7's 14
// days, R10's passed deadline, the six-month reset, the inkassosats, the
// regime and its review, the deadline itself — so the batch is sent only on
// that day: posted earlier, a letter would carry a fee judged for a later
// day; later, its deadline would be short (NB1). On that day the re-judge
// (NI4) takes the batch, its letters' invoices in descending id, then the
// letters, and sends every printed one.
func (s *server) PostInvoicesReminderPrintBatchesByIdPosted(ctx context.Context, req gen.PostInvoicesReminderPrintBatchesByIdPostedRequestObject) (gen.PostInvoicesReminderPrintBatchesByIdPostedResponseObject, error) {
	now := s.deps.Clock() // the confirmation's one clock read (D18)
	today := businessDay(now)
	postedOn := utcDay(req.Body.PostedOn.Time)
	if postedOn.After(today) {
		return gen.PostInvoicesReminderPrintBatchesByIdPosted400ApplicationProblemPlusJSONResponse(invalid(invalidPostedTitle,
			fieldError("postedOn", "A batch is confirmed posted once it was: today or earlier"))), nil
	}
	by := callerID(ctx)
	var posted store.InvoicesReminderPrintBatch
	var letters []store.InvoicesReminder
	skipped, waived := []int64{}, []gen.InvoicesPrintBatchWaiver{}
	var refusal *gen.InvoicesConflictProblem
	notFound := false
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		batch, err := lockPrintBatch(ctx, txq, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			notFound = true
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: lock print batch %d: %w", req.Id, err)
		}
		if hook := postedAfterBatchLock; hook != nil {
			if err := hook(ctx, batch.ID); err != nil {
				return err
			}
		}
		postOn := utcDay(batch.PostOn.Time)
		day := postOn.Format(time.DateOnly)
		switch {
		case batch.PostedOn.Valid || batch.ReprintedAt != nil:
			refusal = ptr(conflict(codePrintBatchClosed, cannotPostTitle, closedDetail(batch)))
		case postedOn.Before(postOn):
			refusal = ptr(conflict(codeReminderPostedEarly, cannotPostTitle, fmt.Sprintf(
				"The batch was printed for %s; every fee and deadline on its letters was judged for that day. Reprint it for the day it is posted.", day)))
		case postedOn.After(postOn):
			refusal = ptr(conflict(codeReminderPostedLate, cannotPostTitle, fmt.Sprintf(
				"The batch was printed for %s; posted later, its letters would give the debtor less time than they say. Reprint it for the day it is posted.", day)))
		}
		if refusal != nil {
			return errRefused
		}
		inBatch, err := txq.LettersOfBatch(ctx, batch.ID)
		if err != nil {
			return fmt.Errorf("invoices: read print batch %d's letters: %w", batch.ID, err)
		}
		var invoiceIDs []int64
		for _, l := range inBatch {
			if !slices.Contains(invoiceIDs, l.InvoiceID) {
				invoiceIDs = append(invoiceIDs, l.InvoiceID)
			}
		}
		invs, err := lockInvoicesDescending(ctx, txq, invoiceIDs)
		if err != nil {
			return err
		}
		for _, l := range inBatch {
			locked, err := lockReminder(ctx, txq, l.ID)
			if err != nil {
				return fmt.Errorf("invoices: lock letter %d: %w", l.ID, err)
			}
			switch locked.Status {
			case reminderrules.StatusWithdrawn:
				skipped = append(skipped, locked.ID)
				continue
			case reminderrules.StatusPrinted:
			default:
				continue
			}
			i := slices.IndexFunc(invs, func(inv store.InvoicesInvoice) bool { return inv.ID == locked.InvoiceID })
			if i < 0 {
				return fmt.Errorf("invoices: letter %d's document %d was not locked", locked.ID, locked.InvoiceID)
			}
			w, err := s.postLetter(ctx, txq, invs[i], locked, postOn, now, by)
			if err != nil {
				return err
			}
			if w != nil {
				waived = append(waived, *w)
			}
		}
		if posted, err = txq.SetBatchPosted(ctx, store.SetBatchPostedParams{
			PostedOn: pgDate(postedOn), PostedAt: now, PostedByUserID: by, ID: batch.ID,
		}); err != nil {
			return fmt.Errorf("invoices: record print batch %d posted: %w", batch.ID, err)
		}
		letters, err = txq.LettersOfBatch(ctx, batch.ID)
		if err != nil {
			return fmt.Errorf("invoices: read print batch %d's letters: %w", batch.ID, err)
		}
		return nil
	})
	switch {
	case notFound:
		return gen.PostInvoicesReminderPrintBatchesByIdPosted404Response{}, nil
	case refusal != nil:
		return gen.PostInvoicesReminderPrintBatchesByIdPosted409ApplicationProblemPlusJSONResponse(*refusal), nil
	case err != nil:
		return nil, err
	}
	w, err := printBatchWire(posted, letters)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesReminderPrintBatchesByIdPosted200JSONResponse(gen.InvoicesPrintBatchPosted{
		Batch: w, Skipped: skipped, Waived: waived,
	}), nil
}

// postLetter sends one printed letter of a batch posted on its postOn, under
// the batch's lock, its invoice's and its own (D18): the engine on postOn,
// the letter left out of the letters in flight; the letter sent at now; and,
// when its invoice was settled, held, handed off or its customer anonymised
// since printing, a lift barred its charges, or the re-judged outcome no
// longer carries the fee or compensation it printed, those waived
// claimed_in_error in the same transaction — after the status change, since
// a waiver names a sent letter — by the person confirming the posting. It
// answers the waiver, or nil.
func (s *server) postLetter(ctx context.Context, txq *store.Queries, inv store.InvoicesInvoice, letter store.InvoicesReminder, postOn, now time.Time, by uuid.UUID) (*gen.InvoicesPrintBatchWaiver, error) {
	erased, err := txq.CustomerErased(ctx, inv.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read whether customer %d is anonymised: %w", inv.CustomerID, err)
	}
	in, err := s.ruleInputLocked(ctx, txq, inv, postOn, letter.ID)
	if err != nil {
		return nil, err
	}
	out := reminderrules.Next(in)
	sent, err := txq.MarkPosted(ctx, store.MarkPostedParams{SentAt: now, ID: letter.ID, PrintBatchID: *letter.PrintBatchID})
	if err != nil {
		return nil, fmt.Errorf("invoices: send letter %d: %w", letter.ID, err)
	}

	reason := postedCause(erased, in, out)
	var ws []waiverRequest
	var kinds []gen.InvoicesPrintBatchWaiverKinds
	for _, c := range []struct {
		kind    string
		printed pgtype.Numeric
		carried func(f *reminderrules.LetterFacts) *big.Rat
	}{
		{reminderrules.WaiverFee, sent.Fee, func(f *reminderrules.LetterFacts) *big.Rat { return f.Fee }},
		{reminderrules.WaiverCompensation, sent.Compensation, func(f *reminderrules.LetterFacts) *big.Rat { return f.Compensation }},
	} {
		v, err := ratOrNil(c.printed)
		if err != nil {
			return nil, err
		}
		if v == nil || v.Sign() <= 0 {
			continue
		}
		if reason == "" {
			// Nothing named changed: the charge stands only while the engine
			// still gives this letter, at its level, with it on postOn.
			f := out.Letter
			if f != nil && string(f.Level) == letter.Level && f.AnnouncesCollection == letter.AnnouncesCollection &&
				c.carried(f) != nil && c.carried(f).Sign() > 0 {
				continue
			}
		}
		ws = append(ws, waiverRequest{reminderID: sent.ID, kind: c.kind})
		kinds = append(kinds, gen.InvoicesPrintBatchWaiverKinds(c.kind))
	}
	if len(ws) == 0 {
		return nil, nil
	}
	if reason == "" {
		reason = withdrawnActionChanged
	}
	problem, err := waiveCharges(ctx, txq, inv.ID, ws, waiverClaimedInError, postedWaiverNote(reason), by, now)
	if err != nil {
		return nil, err
	}
	if problem != nil {
		return nil, fmt.Errorf("invoices: waive letter %d's charges: %s", sent.ID, deref(problem.Detail))
	}
	return &gen.InvoicesPrintBatchWaiver{
		ReminderId: sent.ID, InvoiceId: inv.ID, Kinds: kinds, Reason: gen.InvoicesPrintBatchWaiverReason(reason),
	}, nil
}

// postedCause is what changed since printing that waives every charge a
// posted letter printed — the customer anonymised, the invoice settled,
// handed off or held, the policy none, a lift that barred charges — or ""
// when none of these did.
func postedCause(erased bool, in reminderrules.Input, out reminderrules.Outcome) string {
	switch {
	case erased:
		return withdrawnAnonymised
	case out.Action == reminderrules.ActionNone && slices.Contains(out.Reasons, reminderrules.ReasonHandedOff):
		return withdrawnHandedOff
	case out.Action == reminderrules.ActionNone:
		return withdrawnSettled
	case slices.Contains(out.Reasons, reminderrules.ReasonOnHold):
		return withdrawnOnHold
	case slices.Contains(out.Reasons, reminderrules.ReasonPolicyNone):
		return withdrawnPolicyNone
	case in.ChargesBarred:
		return waivedChargesBarred
	}
	return ""
}

// postedWaiverNote is the note of a posting's claimed_in_error waiver.
func postedWaiverNote(reason string) string {
	switch reason {
	case withdrawnAnonymised:
		return "Posted after the customer was anonymised."
	case withdrawnHandedOff:
		return "Posted after the invoice was handed off to collection."
	case withdrawnSettled:
		return "Posted after the invoice was settled."
	case withdrawnOnHold:
		return "Posted while the invoice was on hold."
	case withdrawnPolicyNone:
		return "Posted after the customer's reminder policy became none."
	case waivedChargesBarred:
		return "Posted after a lift that barred charges on the invoice."
	}
	return "Posted on a day that no longer supports the charge."
}

// closedDetail is why a batch posted or reprinted already is neither again.
func closedDetail(b store.InvoicesReminderPrintBatch) string {
	if b.PostedOn.Valid {
		return fmt.Sprintf("The batch was confirmed posted on %s already.", b.PostedOn.Time.Format(time.DateOnly))
	}
	return "The batch was reprinted already; its letters await print again."
}

// PostInvoicesReminderPrintBatchesByIdReprint Reprint a print batch
// (POST /api/v1/invoices/reminder-print-batches/{id}/reprint)
//
// The batch FOR NO KEY UPDATE, then every printed letter of it — and,
// unlike every other path that writes a letter, not the letters' invoices
// (D18). That cannot cycle: the reprint never waits on an invoice, so a
// cycle would need a path that holds a printed letter of the batch and then
// waits on something the reprint holds, and none does — the dispatch claims
// only queued letters; a hold, a hand-off and the erase hold the invoice
// and write only the letters in flight (queued, awaiting print, failed),
// whose UPDATE skips a printed row without waiting on it; a print batch's
// letter, holding its invoice, waits on the letter and nothing after it; a
// person's withdrawal holds the letter alone; and the posting takes the
// batch first, so it and the reprint queue on the batch row. The letters go
// back to awaiting print with every fact, the batch and the PDF cleared, and
// a later batch prints them again for its own day, under a new key.
func (s *server) PostInvoicesReminderPrintBatchesByIdReprint(ctx context.Context, req gen.PostInvoicesReminderPrintBatchesByIdReprintRequestObject) (gen.PostInvoicesReminderPrintBatchesByIdReprintResponseObject, error) {
	now := s.deps.Clock()
	var reprinted store.InvoicesReminderPrintBatch
	var letters []store.InvoicesReminder
	var refusal *gen.InvoicesConflictProblem
	notFound := false
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		batch, err := lockPrintBatch(ctx, txq, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			notFound = true
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: lock print batch %d: %w", req.Id, err)
		}
		if batch.PostedOn.Valid || batch.ReprintedAt != nil {
			refusal = ptr(conflict(codePrintBatchClosed, cannotReprintTitle, closedDetail(batch)))
			return errRefused
		}
		inBatch, err := txq.LettersOfBatch(ctx, batch.ID)
		if err != nil {
			return fmt.Errorf("invoices: read print batch %d's letters: %w", batch.ID, err)
		}
		for _, l := range inBatch {
			if l.Status != reminderrules.StatusPrinted {
				continue
			}
			if _, err := lockReminder(ctx, txq, l.ID); err != nil {
				return fmt.Errorf("invoices: lock letter %d: %w", l.ID, err)
			}
		}
		if _, err := txq.ReprintBatch(ctx, batch.ID); err != nil {
			return fmt.Errorf("invoices: reprint print batch %d's letters: %w", batch.ID, err)
		}
		if reprinted, err = txq.SetBatchReprinted(ctx, store.SetBatchReprintedParams{ReprintedAt: now, ID: batch.ID}); err != nil {
			return fmt.Errorf("invoices: record print batch %d reprinted: %w", batch.ID, err)
		}
		letters, err = txq.LettersOfBatch(ctx, batch.ID)
		if err != nil {
			return fmt.Errorf("invoices: read print batch %d's letters: %w", batch.ID, err)
		}
		return nil
	})
	switch {
	case notFound:
		return gen.PostInvoicesReminderPrintBatchesByIdReprint404Response{}, nil
	case refusal != nil:
		return gen.PostInvoicesReminderPrintBatchesByIdReprint409ApplicationProblemPlusJSONResponse(*refusal), nil
	case err != nil:
		return nil, err
	}
	w, err := printBatchWire(reprinted, letters)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesReminderPrintBatchesByIdReprint200JSONResponse(w), nil
}

// printBatchWire is a batch and the letters naming it on the wire. Every
// caller holds invoices:payments, so the recipient is answered.
func printBatchWire(b store.InvoicesReminderPrintBatch, letters []store.InvoicesReminder) (gen.InvoicesPrintBatch, error) {
	w := gen.InvoicesPrintBatch{
		Id: b.ID, PostOn: wireDate(utcDay(b.PostOn.Time)), PostedOn: wireDateOf(b.PostedOn), CreatedAt: b.CreatedAt,
		CreatedBy: b.CreatedByUserID, PostedBy: b.PostedByUserID, PostedAt: b.PostedAt, ReprintedAt: b.ReprintedAt,
		Letters: make([]gen.InvoicesReminder, 0, len(letters)),
	}
	for _, l := range letters {
		lw, err := reminderWire(l, true)
		if err != nil {
			return gen.InvoicesPrintBatch{}, err
		}
		w.Letters = append(w.Letters, lw)
	}
	return w, nil
}
