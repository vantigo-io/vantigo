package invoices

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// This file is send all (invoices phase 6 design I36; plan E-1, E3, E4): a
// batch of send jobs, one per document asked for — chosen on the list or made
// by a billing run — written in one transaction, and the print ZIP and Mark
// as posted for the ones left for paper. The invoices-send worker carries the
// jobs (send_worker.go). Neither table holds personal data: the recipient
// stays in invoices.deliveries.

// The states of a send job.
const (
	jobQueued        = "queued"
	jobSending       = "sending"
	jobSent          = "sent"
	jobHandedOver    = "handed_over"
	jobAwaitingPrint = "awaiting_print"
	jobPrinted       = "printed"
	jobSkipped       = "skipped"
	jobFailed        = "failed"
	jobUncertain     = "uncertain"

	sourceSelection  = "selection"
	sourceBillingRun = "billing_run"

	// maxSendBatch is the most documents one batch takes.
	maxSendBatch = 500

	cannotSendAllTitle = "The documents cannot be sent"

	// codeSendBatchRateLimited is a Send all past sendAllPolicy.
	codeSendBatchRateLimited = "send_batch_rate_limited"

	// uncertainElsewhereDetail is the sentence of a document another send
	// job may have mailed.
	uncertainElsewhereDetail = "An earlier Send all may have e-mailed this document without a delivery to show for it. Check with the customer, then send it from the document itself."
)

// sendAllPolicy is send all's rate limit, counted in documents per principal:
// 500 documents per 10 minutes, one full batch.
var sendAllPolicy = ratelimit.Policy{Name: "invoices-send-all", Limit: maxSendBatch, Window: 10 * time.Minute}

// sendBatchRefused is a 400 of send all, in the conflict problem's shape.
func sendBatchRefused(code, detail string) gen.InvoicesConflictProblem {
	c := conflict(code, cannotSendAllTitle, detail)
	c.Status = ptr(int32(http.StatusBadRequest))
	return c
}

// PostInvoicesSendBatches Send documents in one go
// (POST /api/v1/invoices/send-batches)
func (s *server) PostInvoicesSendBatches(ctx context.Context, req gen.PostInvoicesSendBatchesRequestObject) (gen.PostInvoicesSendBatchesResponseObject, error) {
	body := req.Body
	if (body.InvoiceIds == nil) == (body.BillingRunId == nil) {
		return gen.PostInvoicesSendBatches400ApplicationProblemPlusJSONResponse(sendBatchRefused(codeSendBatchEmpty,
			"Name the documents to send, or a billing run, and not both.")), nil
	}
	q := store.New(s.deps.Pool)
	var ids []int64
	source := sourceSelection
	if body.InvoiceIds != nil {
		for _, id := range *body.InvoiceIds {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	} else {
		source = sourceBillingRun
		run, err := q.GetBillingRun(ctx, *body.BillingRunId)
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.PostInvoicesSendBatches404Response{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("invoices: read billing run %d: %w", *body.BillingRunId, err)
		}
		if run.Status != "done" && run.Status != "cancelled" {
			return gen.PostInvoicesSendBatches409ApplicationProblemPlusJSONResponse(conflict(codeBillingRunNotFinished, cannotSendAllTitle,
				"The billing run is still making its documents. Send them once it is done.")), nil
		}
		if ids, err = q.BillingRunInvoiceIDs(ctx, run.ID); err != nil {
			return nil, fmt.Errorf("invoices: read billing run %d's documents: %w", run.ID, err)
		}
	}
	switch {
	case len(ids) == 0:
		return gen.PostInvoicesSendBatches400ApplicationProblemPlusJSONResponse(sendBatchRefused(codeSendBatchEmpty,
			"There is no document to send.")), nil
	case len(ids) > maxSendBatch:
		return gen.PostInvoicesSendBatches400ApplicationProblemPlusJSONResponse(sendBatchRefused(codeSendBatchTooLarge,
			fmt.Sprintf("At most %d documents are sent in one go.", maxSendBatch))), nil
	}

	// The rate limit counts documents, not calls, per principal — a person
	// or an API key's integration user (the review's finding 6): send all is
	// the single send's relay many times over.
	if s.deps.Limiter != nil {
		d, err := s.deps.Limiter.HitN(ctx, sendAllPolicy, callerID(ctx).String(), len(ids))
		if err != nil {
			return nil, fmt.Errorf("invoices: count send all's documents: %w", err)
		}
		if !d.Allowed {
			c := conflict(codeSendBatchRateLimited, cannotSendAllTitle, fmt.Sprintf(
				"At most %d documents are sent in %d minutes. Try again in %d minutes.",
				sendAllPolicy.Limit, int(sendAllPolicy.Window.Minutes()), int(d.RetryAfter.Minutes())+1))
			c.Status = ptr(int32(http.StatusTooManyRequests))
			return gen.PostInvoicesSendBatches429ApplicationProblemPlusJSONResponse(c), nil
		}
	}

	var batch store.InvoicesSendBatch
	err := s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		now := s.deps.Clock()
		docs, err := txq.SendDocuments(ctx, ids)
		if err != nil {
			return fmt.Errorf("invoices: read the documents to send: %w", err)
		}
		status := make(map[int64]string, len(docs))
		for _, d := range docs {
			status[d.ID] = d.Status
		}
		delivered, err := txq.DeliveredDocuments(ctx, ids)
		if err != nil {
			return fmt.Errorf("invoices: read which documents are delivered: %w", err)
		}
		uncertain, err := txq.UncertainDocuments(ctx, ids)
		if err != nil {
			return fmt.Errorf("invoices: read which documents may have been mailed: %w", err)
		}
		batch, err = txq.InsertSendBatch(ctx, store.InsertSendBatchParams{
			Source: source, BillingRunID: body.BillingRunId, RequestedBy: callerID(ctx), CreatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("invoices: record a send batch: %w", err)
		}
		skip := func(id int64, code, detail string) error {
			if err := txq.InsertSkippedSendJob(ctx, store.InsertSkippedSendJobParams{
				BatchID: batch.ID, InvoiceID: id, Code: &code, Detail: detail, CreatedAt: now,
			}); err != nil {
				return fmt.Errorf("invoices: record document %d skipped in send batch %d: %w", id, batch.ID, err)
			}
			return nil
		}
		for _, id := range ids {
			var err error
			switch st, ok := status[id]; {
			case !ok:
				err = skip(id, codeSendDocumentGone, "The document no longer exists.")
			case st != statusIssued:
				err = skip(id, codeInvoiceDraft, "A draft is not a sales document: issue it before sending it.")
			case slices.Contains(delivered, id):
				err = skip(id, codeAlreadyDelivered, "The document is delivered already. Send it another way from the document itself.")
			case slices.Contains(uncertain, id):
				err = skip(id, codeSendUncertainElsewhere, uncertainElsewhereDetail)
			default:
				_, err = txq.InsertQueuedSendJob(ctx, store.InsertQueuedSendJobParams{BatchID: batch.ID, InvoiceID: id, CreatedAt: now})
				if errors.Is(err, pgx.ErrNoRows) {
					err = skip(id, codeSendInProgress, "Another Send all is sending this document now.")
				} else if err != nil {
					err = fmt.Errorf("invoices: queue document %d in send batch %d: %w", id, batch.ID, err)
				}
			}
			if err != nil {
				return err
			}
		}
		// A batch where nothing was queued is finished at once.
		if _, err := txq.FinishSendBatch(ctx, store.FinishSendBatchParams{Now: now, ID: batch.ID}); err != nil {
			return fmt.Errorf("invoices: finish send batch %d: %w", batch.ID, err)
		}
		return notifySendBatch(ctx, tx, batch.ID)
	})
	if err != nil {
		return nil, err
	}
	detail, err := s.sendBatchDetail(ctx, q, batch.ID)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesSendBatches202JSONResponse(detail), nil
}

// GetInvoicesSendBatchesById Get a send batch
// (GET /api/v1/invoices/send-batches/{id})
func (s *server) GetInvoicesSendBatchesById(ctx context.Context, req gen.GetInvoicesSendBatchesByIdRequestObject) (gen.GetInvoicesSendBatchesByIdResponseObject, error) {
	detail, err := s.sendBatchDetail(ctx, store.New(s.deps.Pool), req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetInvoicesSendBatchesById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesSendBatchesById200JSONResponse(detail), nil
}

// GetInvoicesSendBatches List a billing run's send batches
// (GET /api/v1/invoices/send-batches)
func (s *server) GetInvoicesSendBatches(ctx context.Context, req gen.GetInvoicesSendBatchesRequestObject) (gen.GetInvoicesSendBatchesResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.SendBatchesOfRun(ctx, &req.Params.BillingRunId)
	if err != nil {
		return nil, fmt.Errorf("invoices: read billing run %d's send batches: %w", req.Params.BillingRunId, err)
	}
	wire, err := sendBatchesWire(ctx, q, rows)
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesSendBatches200JSONResponse(gen.InvoicesSendBatchList{Data: wire}), nil
}

// sendBatchesWire is batches on the wire, their jobs counted by state.
func sendBatchesWire(ctx context.Context, q *store.Queries, rows []store.InvoicesSendBatch) ([]gen.InvoicesSendBatch, error) {
	ids := make([]int64, 0, len(rows))
	for _, b := range rows {
		ids = append(ids, b.ID)
	}
	counted, err := q.SendBatchCounts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: count the send batches' jobs: %w", err)
	}
	counts := map[int64]*gen.InvoicesSendBatchCounts{}
	for _, b := range rows {
		counts[b.ID] = &gen.InvoicesSendBatchCounts{}
	}
	for _, c := range counted {
		n := counts[c.BatchID]
		if n == nil {
			continue
		}
		switch c.State {
		case jobQueued:
			n.Queued = c.Jobs
		case jobSending:
			n.Sending = c.Jobs
		case jobSent:
			n.Sent = c.Jobs
		case jobHandedOver:
			n.HandedOver = c.Jobs
		case jobAwaitingPrint:
			n.AwaitingPrint = c.Jobs
		case jobPrinted:
			n.Printed = c.Jobs
		case jobSkipped:
			n.Skipped = c.Jobs
		case jobFailed:
			n.Failed = c.Jobs
		case jobUncertain:
			n.Uncertain = c.Jobs
		}
	}
	out := make([]gen.InvoicesSendBatch, 0, len(rows))
	for _, b := range rows {
		out = append(out, gen.InvoicesSendBatch{
			Id: b.ID, Source: gen.InvoicesSendBatchSource(b.Source), BillingRunId: b.BillingRunID,
			RequestedBy: b.RequestedBy, CreatedAt: b.CreatedAt, FinishedAt: b.FinishedAt, Counts: *counts[b.ID],
		})
	}
	return out, nil
}

// sendBatchDetail is a batch with every one of its jobs, each named by its
// document's number and its customer's name from the directory — best
// effort: a directory that fails leaves the names out and says so at warn.
// No row is pgx.ErrNoRows.
func (s *server) sendBatchDetail(ctx context.Context, q *store.Queries, id int64) (gen.InvoicesSendBatchDetail, error) {
	batch, err := q.GetSendBatch(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.InvoicesSendBatchDetail{}, err
		}
		return gen.InvoicesSendBatchDetail{}, fmt.Errorf("invoices: read send batch %d: %w", id, err)
	}
	wire, err := sendBatchesWire(ctx, q, []store.InvoicesSendBatch{batch})
	if err != nil {
		return gen.InvoicesSendBatchDetail{}, err
	}
	rows, err := q.SendJobsOfBatch(ctx, id)
	if err != nil {
		return gen.InvoicesSendBatchDetail{}, fmt.Errorf("invoices: read send batch %d's jobs: %w", id, err)
	}
	var customerIDs []int32
	for _, r := range rows {
		if r.CustomerID != nil && !slices.Contains(customerIDs, *r.CustomerID) {
			customerIDs = append(customerIDs, *r.CustomerID)
		}
	}
	names := map[int32]string{}
	if s.deps.Directory != nil && len(customerIDs) > 0 {
		entries, err := s.customerEntries(ctx, customerIDs)
		if err != nil {
			s.deps.Logger.WarnContext(ctx, "invoices: a send batch's customers could not be named", "batch_id", id, "error", err.Error())
		}
		for _, e := range entries {
			names[e.ID] = e.Name
		}
	}
	jobs := make([]gen.InvoicesSendJob, 0, len(rows))
	for _, r := range rows {
		j := gen.InvoicesSendJob{
			Id: r.ID, InvoiceId: r.InvoiceID, State: gen.InvoicesSendJobState(r.State), Code: r.Code, Detail: r.Detail,
			Attempts: r.Attempts, FinishedAt: r.FinishedAt, TransmissionId: r.TransmissionID, DeliveryId: r.DeliveryID,
			Number: r.InvoiceNumber, Kind: r.InvoiceKind, CustomerId: r.CustomerID,
		}
		if r.Channel != nil {
			j.Channel = ptr(gen.InvoicesSendJobChannel(*r.Channel))
		}
		if r.CustomerID != nil {
			if name, ok := names[*r.CustomerID]; ok {
				j.CustomerName = &name
			}
		}
		jobs = append(jobs, j)
	}
	return gen.InvoicesSendBatchDetail{Batch: wire[0], Jobs: jobs}, nil
}

// sendBatchZip streams the ZIP straight onto the response (the review's
// finding 7): each stored PDF is read, verified against its hash and written
// in turn, so at most one document is held in memory. Its headers are never
// cached, never sniffed, and named. A failure once the first byte is written
// can no longer change the status: it is logged, and the ZIP is cut short,
// which a client's unzip refuses.
type sendBatchZip struct {
	s           *server
	ctx         context.Context
	batchID     int64
	docs        []store.InvoicesInvoice
	disposition string
}

func (r sendBatchZip) VisitGetInvoicesSendBatchesByIdPrintZipResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	w.Header().Set("Content-Type", "application/zip")
	w.WriteHeader(http.StatusOK)
	q := store.New(r.s.deps.Pool)
	zw := zip.NewWriter(w)
	for _, inv := range r.docs {
		pdf, problem := r.s.loadStoredPDF(r.ctx, q, inv)
		if problem != nil {
			r.s.deps.Logger.ErrorContext(r.ctx, "invoices: a send batch's ZIP was cut short", "batch_id", r.batchID, "invoice_id", inv.ID)
			return fmt.Errorf("invoices: send batch %d's ZIP: document %d could not be read", r.batchID, inv.ID)
		}
		f, err := zw.CreateHeader(&zip.FileHeader{Name: strconv.FormatInt(*inv.Number, 10) + ".pdf", Method: zip.Store, Modified: r.s.deps.Clock()})
		if err != nil {
			return fmt.Errorf("invoices: write send batch %d's ZIP: %w", r.batchID, err)
		}
		if _, err := f.Write(pdf.body); err != nil {
			return fmt.Errorf("invoices: write send batch %d's ZIP: %w", r.batchID, err)
		}
	}
	return zw.Close()
}

// GetInvoicesSendBatchesByIdPrintZip Download a send batch's documents for
// printing (GET /api/v1/invoices/send-batches/{id}/print.zip)
//
// Plan E4 and reading E13: the stored PDFs of the batch's jobs awaiting print,
// each verified against its hash and never rendered again, named
// {number}.pdf. Before the first byte, every document is made sure of: one
// never stored (a job written by hand) is stored once, as the download does,
// and a stored object that is gone is the 500 and a store that cannot be read
// the 503, so the common failures still answer a status; then the ZIP is
// streamed.
func (s *server) GetInvoicesSendBatchesByIdPrintZip(ctx context.Context, req gen.GetInvoicesSendBatchesByIdPrintZipRequestObject) (gen.GetInvoicesSendBatchesByIdPrintZipResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetSendBatch(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.GetInvoicesSendBatchesByIdPrintZip404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("invoices: read send batch %d: %w", req.Id, err)
	}
	jobs, err := q.AwaitingPrintJobs(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("invoices: read send batch %d's documents for printing: %w", req.Id, err)
	}
	if len(jobs) == 0 {
		return gen.GetInvoicesSendBatchesByIdPrintZip404Response{}, nil
	}
	if !s.storageConfigured {
		return gen.GetInvoicesSendBatchesByIdPrintZip503ApplicationProblemPlusJSONResponse(storageUnavailable("This installation has no object store.")), nil
	}
	docs := make([]store.InvoicesInvoice, 0, len(jobs))
	for _, j := range jobs {
		inv, err := q.GetInvoice(ctx, j.InvoiceID)
		if err != nil {
			return nil, fmt.Errorf("invoices: read document %d: %w", j.InvoiceID, err)
		}
		if inv.Number == nil {
			return nil, fmt.Errorf("invoices: document %d left for printing has no number", inv.ID)
		}
		if inv.PdfSha256 == nil || inv.PdfObjectKey == nil {
			stored, problem := s.loadStoredPDF(ctx, q, inv)
			switch {
			case problem == nil:
			case problem.unavailable != nil:
				return gen.GetInvoicesSendBatchesByIdPrintZip503ApplicationProblemPlusJSONResponse(*problem.unavailable), nil
			case problem.broken:
				return gen.GetInvoicesSendBatchesByIdPrintZip500ApplicationProblemPlusJSONResponse(storedDocumentBroken()), nil
			default:
				return nil, problem.err
			}
			inv.PdfObjectKey, inv.PdfSha256 = &stored.key, &stored.sha256
		} else {
			exists, err := s.objectExists(ctx, *inv.PdfObjectKey)
			switch {
			case err != nil:
				s.deps.Logger.WarnContext(ctx, "invoices: the document store could not be read", "invoice_id", inv.ID, "error", err.Error())
				return gen.GetInvoicesSendBatchesByIdPrintZip503ApplicationProblemPlusJSONResponse(storageUnavailable("The document store could not be read. Try again.")), nil
			case !exists:
				s.deps.Logger.ErrorContext(ctx, "invoices: an issued document's stored PDF is gone", "invoice_id", inv.ID, "key", *inv.PdfObjectKey)
				return gen.GetInvoicesSendBatchesByIdPrintZip500ApplicationProblemPlusJSONResponse(storedDocumentBroken()), nil
			}
		}
		docs = append(docs, inv)
	}
	return sendBatchZip{
		s: s, ctx: ctx, batchID: req.Id, docs: docs,
		disposition: fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("send-batch-%d-print.zip", req.Id)),
	}, nil
}

// PostInvoicesSendBatchesByIdPosted Confirm a send batch's printed documents
// posted (POST /api/v1/invoices/send-batches/{id}/posted)
//
// Plan E4: for each job awaiting print, under its document's lock and then
// the job's, an invoice gets a manual delivery of kind posted, as
// PostInvoicesByIdManualDeliveries records one, and the job becomes printed;
// a credit note, which records none, only becomes printed.
func (s *server) PostInvoicesSendBatchesByIdPosted(ctx context.Context, req gen.PostInvoicesSendBatchesByIdPostedRequestObject) (gen.PostInvoicesSendBatchesByIdPostedResponseObject, error) {
	q := store.New(s.deps.Pool)
	batch, err := q.GetSendBatch(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesSendBatchesByIdPosted404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read send batch %d: %w", req.Id, err)
	}
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	on, made, today := utcDay(req.Body.PostedOn.Time), holidays.DayIn(batch.CreatedAt, loc), holidays.DayIn(s.deps.Clock(), loc)
	switch {
	case req.Body.PostedOn.IsZero():
		return gen.PostInvoicesSendBatchesByIdPosted400ApplicationProblemPlusJSONResponse(invalid(invalidDeliveryTitle,
			fieldError("postedOn", "A posting needs the day it was made"))), nil
	case on.Before(made):
		return gen.PostInvoicesSendBatchesByIdPosted400ApplicationProblemPlusJSONResponse(invalid(invalidDeliveryTitle,
			fieldError("postedOn", "The documents are posted on or after the batch's day, "+made.Format(time.DateOnly)))), nil
	case on.After(today):
		return gen.PostInvoicesSendBatchesByIdPosted400ApplicationProblemPlusJSONResponse(invalid(invalidDeliveryTitle,
			fieldError("postedOn", "A posting is recorded today at the latest, "+today.Format(time.DateOnly)))), nil
	}
	jobs, err := q.AwaitingPrintJobs(ctx, batch.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read send batch %d's documents for printing: %w", batch.ID, err)
	}
	for _, j := range jobs {
		err := s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
			locked, err := lockInvoice(ctx, txq, j.InvoiceID)
			if err != nil {
				return fmt.Errorf("invoices: lock document %d: %w", j.InvoiceID, err)
			}
			job, err := txq.LockSendJob(ctx, j.ID)
			if err != nil {
				return fmt.Errorf("invoices: lock send job %d: %w", j.ID, err)
			}
			if job.State != jobAwaitingPrint {
				return nil
			}
			now := s.deps.Clock()
			if locked.Kind == kindInvoice {
				if _, err := txq.InsertManualDelivery(ctx, store.InsertManualDeliveryParams{
					InvoiceID: locked.ID, Kind: "posted", DeliveredOn: pgDate(on), RecordedByUserID: callerID(ctx), RecordedAt: now,
				}); err != nil {
					return fmt.Errorf("invoices: record document %d posted: %w", locked.ID, err)
				}
			}
			if _, err := txq.MarkSendJobPrinted(ctx, store.MarkSendJobPrintedParams{Now: now, ID: job.ID}); err != nil {
				return fmt.Errorf("invoices: mark send job %d printed: %w", job.ID, err)
			}
			return notifySendBatch(ctx, tx, batch.ID)
		})
		if err != nil {
			return nil, err
		}
	}
	detail, err := s.sendBatchDetail(ctx, q, batch.ID)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesSendBatchesByIdPosted200JSONResponse(detail), nil
}
