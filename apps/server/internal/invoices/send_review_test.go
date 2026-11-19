package invoices_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
)

// The review of delivery E-1: a document goes once across batches, the
// cores' re-check under the lock, the rate limit and the requester, and the
// worker's failure paths.

// plantFinishedRun is a billing run done, whose items made ids.
func plantFinishedRun(t *testing.T, h *harness, ids ...int64) int64 {
	t.Helper()
	var runID int64
	if err := h.Pool().QueryRow(context.Background(), `INSERT INTO invoices.billing_runs (through, issue, status, customers, created_by_user_id, created_at, finished_at)
		VALUES (DATE '2026-09-12', true, 'done', 1, gen_random_uuid(), now(), now()) RETURNING id`).Scan(&runID); err != nil {
		t.Fatalf("plant a run: %v", err)
	}
	h.Exec(t, `INSERT INTO invoices.billing_run_items (run_id, customer_id, status, invoice_ids, next_attempt_at, finished_at)
		VALUES ($1, $2, 'issued', $3::bigint[], now(), now())`, runID, customerPerson, ids)
	return runID
}

// sendRun enqueues a run's documents as an issuer.
func sendRun(t *testing.T, h *harness, runID int64) sendBatchJSON {
	t.Helper()
	res := enqueue(issuer(t, h), map[string]any{"billingRunId": runID})
	if res.Status != http.StatusAccepted {
		t.Fatalf("POST a run's batch = %d %s", res.Status, res.Body)
	}
	var b sendBatchJSON
	res.JSON(&b)
	return b
}

// A run sent again after its documents were left for print, posted, made
// uncertain and failed mail_failed sends none of them twice.
func TestSendBatches_ARunSentAgainSendsNothingTwice(t *testing.T) {
	t.Parallel()
	h, mails := sendReady(t)
	h.customers.edit(customerForeign, func(p *contracts.CustomerBillingProfile) { p.InvoiceDelivery = "paper" })
	paper, lost, refused := anIssued(t, h, customerForeign), anIssued(t, h, customerPerson), anIssued(t, h, customerAcme)
	runID := plantFinishedRun(t, h, paper.ID, lost.ID, refused.ID)

	first := sendRun(t, h, runID)
	now := h.Now()
	h.Exec(t, `UPDATE invoices.send_jobs SET state = 'sending', channel = 'email', attempts = 1, started_at = $3,
		lease_id = 'gone', lease_until = $4 WHERE batch_id = $1 AND invoice_id = $2`, first.Batch.ID, lost.ID, now, now.Add(-time.Minute))
	mails.fail(errors.New("421 try later"))
	drainSends(t, h)
	mails.fail(nil)
	got := readBatch(t, h, first.Batch.ID)
	wantJob(t, "paper", jobOf(t, got, paper.ID), "awaiting_print", "", "print")
	wantJob(t, "a lost e-mail", jobOf(t, got, lost.ID), "uncertain", "send_uncertain", "email")
	wantJob(t, "a refused e-mail", jobOf(t, got, refused.ID), "failed", "mail_failed", "email")

	again := sendRun(t, h, runID)
	wantJob(t, "left for print", jobOf(t, again, paper.ID), "skipped", "already_delivered", "")
	wantJob(t, "uncertain", jobOf(t, again, lost.ID), "skipped", "send_uncertain_elsewhere", "")
	wantJob(t, "mail_failed", jobOf(t, again, refused.ID), "skipped", "send_uncertain_elsewhere", "")

	if res := issuer(t, h).Do(http.MethodPost, sendBatchPath(first.Batch.ID)+"/posted", map[string]any{"postedOn": "2026-09-12"}); res.Status != http.StatusOK {
		t.Fatalf("posted = %d %s", res.Status, res.Body)
	}
	third := sendRun(t, h, runID)
	wantJob(t, "printed", jobOf(t, third, paper.ID), "skipped", "already_delivered", "")
	drainSends(t, h)
	if n := len(mails.mails()); n != 0 {
		t.Errorf("%d mails, want none: nothing is sent twice", n)
	}
}

// A delivery made between the worker's reads and the core's lock is seen
// under the lock: the EHF is not queued, and the job is skipped.
func TestSendWorker_ADeliveryMeanwhileIsSeenUnderTheLock(t *testing.T) {
	t.Parallel()
	h, mails, lookup := sendAllReady(t)
	inv := anIssued(t, h, customerAcme)
	b := sendAll(t, h, inv.ID)
	lookup.whenCalled(func(string) {
		// The seam: the EHF core asks the network outside any lock, and a
		// person records the invoice handed over by then.
		h.Exec(t, `INSERT INTO invoices.manual_deliveries (invoice_id, kind, delivered_on, note, recorded_by_user_id, recorded_at)
			VALUES ($1, 'handed_over', DATE '2026-09-12', '', gen_random_uuid(), now())`, inv.ID)
	})
	drainSends(t, h)
	wantJob(t, "delivered meanwhile", jobOf(t, readBatch(t, h, b.Batch.ID), inv.ID), "skipped", "already_delivered", "ehf")
	if n := transmissionRows(t, h, inv.ID); n != 0 {
		t.Errorf("%d transmissions, want none", n)
	}
	if n := len(mails.mails()); n != 0 {
		t.Errorf("%d mails, want none", n)
	}
}

// The rate limit counts documents per principal: 500 in ten minutes, then
// 429 — a document gone counts as one asked for.
func TestSendBatches_TheRateLimitCountsDocuments(t *testing.T) {
	t.Parallel()
	h, _ := sendReady(t)
	c := issuer(t, h)
	ids := make([]int64, 500)
	for i := range ids {
		ids[i] = int64(900000 + i)
	}
	if res := enqueue(c, map[string]any{"invoiceIds": ids}); res.Status != http.StatusAccepted {
		t.Fatalf("500 documents = %d %s", res.Status, res.Body)
	}
	res := enqueue(c, map[string]any{"invoiceIds": []int64{1}})
	if res.Status != http.StatusTooManyRequests || problemCodeOf(t, res) != "send_batch_rate_limited" {
		t.Fatalf("the 501st = %d %s, want 429 send_batch_rate_limited", res.Status, res.Body)
	}
	h.Advance(11 * time.Minute)
	if res := enqueue(issuer(t, h), map[string]any{"invoiceIds": []int64{1}}); res.Status != http.StatusAccepted {
		t.Errorf("after the window = %d %s", res.Status, res.Body)
	}
}

// A requester no longer active or allowed sends nothing: the job is
// skipped requester_not_allowed when it runs.
func TestSendWorker_ARequesterNoLongerAllowedSendsNothing(t *testing.T) {
	t.Parallel()
	h, mails := sendReady(t)
	inv := anIssued(t, h, customerPerson)
	var batchID int64
	if err := h.Pool().QueryRow(context.Background(), `INSERT INTO invoices.send_batches (source, requested_by, created_at)
		VALUES ('selection', gen_random_uuid(), now()) RETURNING id`).Scan(&batchID); err != nil {
		t.Fatalf("plant a batch: %v", err)
	}
	h.Exec(t, `INSERT INTO invoices.send_jobs (batch_id, invoice_id, created_at) VALUES ($1, $2, now())`, batchID, inv.ID)
	drainSends(t, h)
	wantJob(t, "an unknown requester", jobOf(t, readBatch(t, h, batchID), inv.ID), "skipped", "requester_not_allowed", "")
	if n := len(mails.mails()); n != 0 {
		t.Errorf("%d mails, want none", n)
	}
}

// A mail the server took whose delivery row cannot be written is uncertain,
// never mailed again.
func TestSendWorker_MailedButNotLoggedIsUncertain(t *testing.T) {
	t.Parallel()
	h, mails := sendReady(t)
	inv := anIssued(t, h, customerPerson)
	h.Exec(t, `CREATE FUNCTION invoices.refuse_test_delivery() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'test: no delivery row'; END; $$`)
	h.Exec(t, `CREATE TRIGGER tr_test_no_delivery BEFORE INSERT ON invoices.deliveries
		FOR EACH ROW EXECUTE FUNCTION invoices.refuse_test_delivery()`)
	b := sendAll(t, h, inv.ID)
	drainSends(t, h)
	wantJob(t, "mailed, not logged", jobOf(t, readBatch(t, h, b.Batch.ID), inv.ID), "uncertain", "send_uncertain", "email")
	h.Advance(time.Hour)
	drainSends(t, h)
	if n := len(mails.mails()); n != 1 {
		t.Errorf("%d mails, want the one", n)
	}
}

// An unavailability is tried again with backoff, five attempts in all, then
// fails with its code.
func TestSendWorker_BacksOffThenFailsAfterFiveAttempts(t *testing.T) {
	t.Parallel()
	h, _, lookup := sendAllReady(t)
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.InvoiceDelivery = "ehf" })
	lookup.answer(peppol.Result{}, errors.New("dns timeout"))
	inv := anIssued(t, h, customerAcme)
	b := sendAll(t, h, inv.ID)
	for attempt := 1; attempt <= 5; attempt++ {
		drainSends(t, h)
		j := jobOf(t, readBatch(t, h, b.Batch.ID), inv.ID)
		if j.Attempts != int32(attempt) {
			t.Fatalf("after attempt %d: %+v", attempt, j)
		}
		if attempt < 5 {
			wantJob(t, "waiting", j, "queued", "peppol_lookup_failed", "")
			drainSends(t, h) // not due yet: nothing is claimed
			if again := jobOf(t, readBatch(t, h, b.Batch.ID), inv.ID); again.Attempts != int32(attempt) {
				t.Fatalf("claimed before its backoff: %+v", again)
			}
			h.Advance(time.Duration(attempt)*time.Minute + time.Second)
		} else {
			wantJob(t, "the last attempt", j, "failed", "peppol_lookup_failed", "ehf")
		}
	}
}

// mail_failed is final: the mail server may have taken it.
func TestSendWorker_MailFailedIsFinal(t *testing.T) {
	t.Parallel()
	h, mails := sendReady(t)
	inv := anIssued(t, h, customerPerson)
	mails.fail(errors.New("421 try later"))
	b := sendAll(t, h, inv.ID)
	drainSends(t, h)
	h.Advance(time.Hour)
	drainSends(t, h)
	j := jobOf(t, readBatch(t, h, b.Batch.ID), inv.ID)
	wantJob(t, "a refused mail", j, "failed", "mail_failed", "email")
	if j.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", j.Attempts)
	}
}

// The print ZIP answers 503 when the store cannot be read and 500 when a
// stored PDF is gone, before the first byte.
func TestSendBatches_ThePrintZipsFailures(t *testing.T) {
	t.Parallel()
	h, _, _ := sendAllReady(t)
	h.customers.edit(customerPerson, func(p *contracts.CustomerBillingProfile) { p.InvoiceDelivery = "paper" })
	inv := anIssued(t, h, customerPerson)
	b := sendAll(t, h, inv.ID)
	drainSends(t, h)
	zipPath := sendBatchPath(b.Batch.ID) + "/print.zip"
	h.objects.failExists(errors.New("store down"))
	if res := issuer(t, h).Do(http.MethodGet, zipPath, nil); res.Status != http.StatusServiceUnavailable {
		t.Errorf("a store down = %d, want 503", res.Status)
	}
	h.objects.failExists(nil)
	stored := getInvoice(t, h, inv.ID)
	if stored.PdfStored == nil || !*stored.PdfStored {
		t.Fatalf("the PDF is not stored: %+v", stored)
	}
	var key string
	if err := h.Pool().QueryRow(context.Background(), `SELECT pdf_object_key FROM invoices.invoices WHERE id = $1`, inv.ID).Scan(&key); err != nil {
		t.Fatalf("read the key: %v", err)
	}
	h.objects.lose(key)
	if res := issuer(t, h).Do(http.MethodGet, zipPath, nil); res.Status != http.StatusInternalServerError {
		t.Errorf("a PDF gone = %d, want 500", res.Status)
	}
}
