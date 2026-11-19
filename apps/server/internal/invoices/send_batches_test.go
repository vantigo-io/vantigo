package invoices_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
)

// Send all (invoices phase 6 design I36; plan E-1): the batch's refusals,
// its skips, a billing run's batch and the tables' triggers, then the
// invoices-send worker driven by ProcessOne on the fixed clock.

const sendBatchesPath = "/api/v1/invoices/send-batches"

func sendBatchPath(id int64) string { return fmt.Sprintf("%s/%d", sendBatchesPath, id) }

// sendJobJSON is one job as a client reads it.
type sendJobJSON struct {
	ID             int64   `json:"id"`
	InvoiceID      int64   `json:"invoiceId"`
	Number         *int64  `json:"number"`
	CustomerName   *string `json:"customerName"`
	Channel        *string `json:"channel"`
	State          string  `json:"state"`
	Code           *string `json:"code"`
	Detail         string  `json:"detail"`
	Attempts       int32   `json:"attempts"`
	TransmissionID *int64  `json:"transmissionId"`
	DeliveryID     *int64  `json:"deliveryId"`
}

// sendBatchJSON is a batch and its jobs as a client reads them.
type sendBatchJSON struct {
	Batch struct {
		ID           int64            `json:"id"`
		Source       string           `json:"source"`
		BillingRunID *int64           `json:"billingRunId"`
		FinishedAt   *string          `json:"finishedAt"`
		Counts       map[string]int32 `json:"counts"`
	} `json:"batch"`
	Jobs []sendJobJSON `json:"jobs"`
}

// enqueue posts a batch as c.
func enqueue(c *modtest.Client, body map[string]any) *modtest.Response {
	return c.Do(http.MethodPost, sendBatchesPath, body)
}

// sendAll enqueues ids as an issuer and answers the batch, failing unless
// it was accepted.
func sendAll(t *testing.T, h *harness, ids ...int64) sendBatchJSON {
	t.Helper()
	res := enqueue(issuer(t, h), map[string]any{"invoiceIds": ids})
	if res.Status != http.StatusAccepted {
		t.Fatalf("POST /send-batches = %d %s, want 202", res.Status, res.Body)
	}
	var b sendBatchJSON
	res.JSON(&b)
	return b
}

// readBatch reads batch id as an issuer.
func readBatch(t *testing.T, h *harness, id int64) sendBatchJSON {
	t.Helper()
	res := issuer(t, h).Do(http.MethodGet, sendBatchPath(id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /send-batches/%d = %d %s", id, res.Status, res.Body)
	}
	var b sendBatchJSON
	res.JSON(&b)
	return b
}

// jobOf is the job of document id in b.
func jobOf(t *testing.T, b sendBatchJSON, id int64) sendJobJSON {
	t.Helper()
	for _, j := range b.Jobs {
		if j.InvoiceID == id {
			return j
		}
	}
	t.Fatalf("no job for document %d in %+v", id, b.Jobs)
	return sendJobJSON{}
}

// wantJob asserts a job's state, its code ("" for none) and its channel (""
// for none).
func wantJob(t *testing.T, what string, j sendJobJSON, state, code, channel string) {
	t.Helper()
	if j.State != state || orEmpty(j.Code) != code || orEmpty(j.Channel) != channel {
		t.Errorf("%s: job %+v (detail %q), want %s %q by %q", what, j, j.Detail, state, code, channel)
	}
}

// sendWorker is the invoices-send worker as worker mode builds it.
func sendWorker(t *testing.T, h *harness) *invoices.SendWorker {
	t.Helper()
	for _, w := range h.Workers(h.Deps()) {
		if s, ok := w.(*invoices.SendWorker); ok {
			return s
		}
	}
	t.Fatal("no invoices-send worker is registered")
	return nil
}

// drainSends carries every due job.
func drainSends(t *testing.T, h *harness) {
	t.Helper()
	w := sendWorker(t, h)
	for range 100 {
		ok, err := w.ProcessOne(context.Background())
		if err != nil {
			t.Fatalf("ProcessOne: %v", err)
		}
		if !ok {
			return
		}
	}
	t.Fatal("the send worker never ran out of jobs")
}

// anInvoice issues one line of 1 000 for customer.
func anIssued(t *testing.T, h *harness, customer int32) invoiceJSON {
	t.Helper()
	vat := int32(vat25)
	if customer == customerForeign {
		vat = vatExport
	}
	return issued(t, h, createDraft(t, h, draftBody(customer, line("Rådgivning", 1, 1000, vat))).ID)
}

// problemCode is a refusal's code.
func problemCodeOf(t *testing.T, res *modtest.Response) string {
	t.Helper()
	var p struct {
		Code string `json:"code"`
	}
	res.JSON(&p)
	return p.Code
}

func TestSendBatches_Refusals(t *testing.T) {
	t.Parallel()
	h, _ := sendReady(t)
	c := issuer(t, h)
	inv := anIssued(t, h, customerPerson)
	tooMany := make([]int64, 501)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	for _, tc := range []struct {
		name   string
		body   map[string]any
		status int
		code   string
	}{
		{"neither", map[string]any{}, http.StatusBadRequest, "send_batch_empty"},
		{"both", map[string]any{"invoiceIds": []int64{inv.ID}, "billingRunId": 1}, http.StatusBadRequest, "send_batch_empty"},
		{"none", map[string]any{"invoiceIds": []int64{}}, http.StatusBadRequest, "send_batch_empty"},
		{"too many", map[string]any{"invoiceIds": tooMany}, http.StatusBadRequest, "send_batch_too_large"},
		{"an unknown run", map[string]any{"billingRunId": 999999}, http.StatusNotFound, ""},
	} {
		res := enqueue(c, tc.body)
		if res.Status != tc.status || (tc.code != "" && problemCodeOf(t, res) != tc.code) {
			t.Errorf("%s = %d %s, want %d %s", tc.name, res.Status, res.Body, tc.status, tc.code)
		}
	}

	// A run still making its documents.
	var runID int64
	if err := h.Pool().QueryRow(context.Background(), `INSERT INTO invoices.billing_runs (through, issue, customers, created_by_user_id, created_at)
		VALUES (DATE '2026-09-12', true, 1, gen_random_uuid(), now()) RETURNING id`).Scan(&runID); err != nil {
		t.Fatalf("plant a run: %v", err)
	}
	if res := enqueue(c, map[string]any{"billingRunId": runID}); res.Status != http.StatusConflict || problemCodeOf(t, res) != "billing_run_not_finished" {
		t.Errorf("a queued run = %d %s, want 409 billing_run_not_finished", res.Status, res.Body)
	}

	// invoices:issue is the operation's rule.
	if res := enqueue(h.SignIn(t, "invoices:access"), map[string]any{"invoiceIds": []int64{inv.ID}}); res.Status != http.StatusForbidden {
		t.Errorf("without invoices:issue = %d, want 403", res.Status)
	}
}

// Every document asked for is listed: a draft, a document gone, one
// delivered already on any channel and one another batch is sending are
// skipped at once, the rest queued.
func TestSendBatches_Skips(t *testing.T) {
	t.Parallel()
	h, mails := sendReady(t)
	draft := createDraft(t, h, draftBody(customerPerson, line("A", 1, 100, vat25)))
	mailed := anIssued(t, h, customerPerson)
	sent(t, h, mailed.ID, nil)
	posted := anIssued(t, h, customerPerson)
	if res := issuer(t, h).Do(http.MethodPost, manualDeliveriesPath(posted.ID),
		map[string]any{"kind": "posted", "deliveredOn": "2026-09-12"}); res.Status != http.StatusOK {
		t.Fatalf("record a manual delivery = %d %s", res.Status, res.Body)
	}
	queued := anIssued(t, h, customerPerson)
	first := sendAll(t, h, queued.ID, queued.ID)
	if len(first.Jobs) != 1 {
		t.Fatalf("a document named twice: %d jobs, want 1", len(first.Jobs))
	}
	wantJob(t, "the first batch's", first.Jobs[0], "queued", "", "")

	b := sendAll(t, h, draft.ID, 987654, mailed.ID, posted.ID, queued.ID)
	wantJob(t, "a draft", jobOf(t, b, draft.ID), "skipped", "invoice_draft", "")
	wantJob(t, "a document gone", jobOf(t, b, 987654), "skipped", "invoice_not_found", "")
	wantJob(t, "e-mailed by hand", jobOf(t, b, mailed.ID), "skipped", "already_delivered", "")
	wantJob(t, "posted by hand", jobOf(t, b, posted.ID), "skipped", "already_delivered", "")
	wantJob(t, "queued by another batch", jobOf(t, b, queued.ID), "skipped", "send_in_progress", "")
	if b.Batch.FinishedAt == nil || b.Batch.Counts["skipped"] != 5 || b.Batch.Source != "selection" {
		t.Errorf("a batch with nothing queued: %+v, want finished, five skipped, from a selection", b.Batch)
	}
	if got := len(mails.mails()); got != 1 {
		t.Errorf("%d mails, want only the send by hand", got)
	}
}

// A billing run's batch takes every document its items made, once the run
// is done; the run lists it.
func TestSendBatches_OfABillingRun(t *testing.T) {
	t.Parallel()
	h, _ := sendReady(t)
	a, b := anIssued(t, h, customerPerson), anIssued(t, h, customerAcme)
	var runID int64
	if err := h.Pool().QueryRow(context.Background(), `INSERT INTO invoices.billing_runs (through, issue, status, customers, created_by_user_id, created_at, finished_at)
		VALUES (DATE '2026-09-12', true, 'done', 2, gen_random_uuid(), now(), now()) RETURNING id`).Scan(&runID); err != nil {
		t.Fatalf("plant a run: %v", err)
	}
	h.Exec(t, `INSERT INTO invoices.billing_run_items (run_id, customer_id, status, invoice_ids, next_attempt_at, finished_at)
		VALUES ($1, $2, 'issued', ARRAY[$3::bigint], now(), now()), ($1, $4, 'issued', ARRAY[$5::bigint], now(), now())`,
		runID, customerPerson, a.ID, customerAcme, b.ID)
	res := enqueue(issuer(t, h), map[string]any{"billingRunId": runID})
	if res.Status != http.StatusAccepted {
		t.Fatalf("POST a run's batch = %d %s", res.Status, res.Body)
	}
	var batch sendBatchJSON
	res.JSON(&batch)
	if batch.Batch.Source != "billing_run" || deref64(batch.Batch.BillingRunID) != runID || len(batch.Jobs) != 2 || batch.Batch.Counts["queued"] != 2 {
		t.Errorf("the run's batch = %+v", batch)
	}
	list := issuer(t, h).Do(http.MethodGet, fmt.Sprintf("%s?billingRunId=%d", sendBatchesPath, runID), nil)
	var listed struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	list.JSON(&listed)
	if list.Status != http.StatusOK || len(listed.Data) != 1 || listed.Data[0].ID != batch.Batch.ID {
		t.Errorf("the run's batches = %d %s", list.Status, list.Body)
	}
}

// The triggers: a job moves only as the worker moves it and is never
// deleted, and a batch is never deleted.
func TestSendBatches_Triggers(t *testing.T) {
	t.Parallel()
	h, _ := sendReady(t)
	b := sendAll(t, h, anIssued(t, h, customerPerson).ID)
	ctx := context.Background()
	for _, bad := range []string{
		fmt.Sprintf(`UPDATE invoices.send_jobs SET state = 'sent', finished_at = now() WHERE batch_id = %d`, b.Batch.ID),
		fmt.Sprintf(`UPDATE invoices.send_jobs SET invoice_id = invoice_id + 1 WHERE batch_id = %d`, b.Batch.ID),
		fmt.Sprintf(`DELETE FROM invoices.send_jobs WHERE batch_id = %d`, b.Batch.ID),
		fmt.Sprintf(`DELETE FROM invoices.send_batches WHERE id = %d`, b.Batch.ID),
		fmt.Sprintf(`UPDATE invoices.send_batches SET requested_by = gen_random_uuid() WHERE id = %d`, b.Batch.ID),
	} {
		if _, err := h.Pool().Exec(ctx, bad); err == nil || !strings.Contains(err.Error(), "invoices: a send") {
			t.Errorf("%s: %v, want the trigger's refusal", bad, err)
		}
	}
	drainSends(t, h)
	if _, err := h.Pool().Exec(ctx, fmt.Sprintf(`UPDATE invoices.send_jobs SET state = 'queued', finished_at = NULL WHERE batch_id = %d`, b.Batch.ID)); err == nil {
		t.Error("a sent job moved back to queued")
	}
}

// orEmpty is a nullable string's value, "" for none.
func orEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func deref64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// The worker: e-mail sent, logged once under the requester's name.
func TestSendWorker_EmailsAPerson(t *testing.T) {
	t.Parallel()
	h, mails := sendReady(t)
	inv := anIssued(t, h, customerPerson)
	b := sendAll(t, h, inv.ID)
	drainSends(t, h)
	got := readBatch(t, h, b.Batch.ID)
	j := jobOf(t, got, inv.ID)
	wantJob(t, "a person with an invoice e-mail", j, "sent", "", "email")
	if j.DeliveryID == nil || len(mails.mails()) != 1 || mails.mails()[0].out.To[0] != "kari@example.org" {
		t.Errorf("the send: %+v, %d mails", j, len(mails.mails()))
	}
	if got.Batch.FinishedAt == nil || got.Batch.Counts["sent"] != 1 {
		t.Errorf("the batch: %+v, want finished with one sent", got.Batch)
	}
	if again := readAs(t, issuer(t, h), inv.ID); len(again.Deliveries) != 1 {
		t.Errorf("deliveries = %+v, want one", again.Deliveries)
	}
}

// sendAllReady can send every way but eFaktura: mail, and EHF with the
// Peppol lookup faked.
func sendAllReady(t *testing.T) (*harness, *fakeSMTP, *fakeLookup) {
	t.Helper()
	mails, lookup := &fakeSMTP{}, &fakeLookup{result: receivesBoth}
	h := newHarness(t, withChannel(mails.send), modtest.WithPeppolLookup(lookup.lookup))
	chooseChannel(t, h)
	saveSeller(t, h, completeSeller(1))
	plantAccessPointCredentials(t, h)
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.InvoiceEmail = "faktura@acme.example" })
	h.customers.edit(customerForeign, func(p *contracts.CustomerBillingProfile) { p.InvoiceEmail = "faktura@svenska.example" })
	h.customers.edit(customerPerson, func(p *contracts.CustomerBillingProfile) { p.InvoiceEmail = "kari@example.org" })
	return h, mails, lookup
}

// A business goes as EHF; one with no Peppol id falls through to e-mail; a
// preference for paper is left for printing, which the ZIP serves byte for
// byte from the store and Mark as posted records.
func TestSendWorker_EhfEmailAndPrint(t *testing.T) {
	t.Parallel()
	h, mails, _ := sendAllReady(t)
	h.customers.edit(customerPerson, func(p *contracts.CustomerBillingProfile) { p.InvoiceDelivery = "paper" })
	acme, foreign, person := anIssued(t, h, customerAcme), anIssued(t, h, customerForeign), anIssued(t, h, customerPerson)
	b := sendAll(t, h, acme.ID, foreign.ID, person.ID)
	drainSends(t, h)
	got := readBatch(t, h, b.Batch.ID)
	ehfJob := jobOf(t, got, acme.ID)
	wantJob(t, "a Norwegian business", ehfJob, "handed_over", "", "ehf")
	if ehfJob.TransmissionID == nil {
		t.Errorf("the EHF job names no transmission: %+v", ehfJob)
	}
	wantJob(t, "a business without a Peppol id", jobOf(t, got, foreign.ID), "sent", "", "email")
	wantJob(t, "a person preferring paper", jobOf(t, got, person.ID), "awaiting_print", "", "print")
	if n := len(mails.mails()); n != 1 {
		t.Errorf("%d mails, want 1", n)
	}

	zipRes := issuer(t, h).Do(http.MethodGet, sendBatchPath(b.Batch.ID)+"/print.zip", nil)
	if zipRes.Status != http.StatusOK || zipRes.Header("Cache-Control") != "private, no-store" {
		t.Fatalf("print.zip = %d %s", zipRes.Status, zipRes.Body)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipRes.Body), int64(len(zipRes.Body)))
	if err != nil || len(zr.File) != 1 || zr.File[0].Name != fmt.Sprintf("%d.pdf", *person.Number) {
		t.Fatalf("the ZIP: %v %+v", err, zr)
	}
	rc, _ := zr.File[0].Open()
	inZip, _ := io.ReadAll(rc)
	_ = rc.Close()
	pdf := issuer(t, h).Do(http.MethodGet, invoicePath(person.ID)+"/pdf", nil)
	if !bytes.Equal(inZip, pdf.Body) {
		t.Error("the ZIP's PDF is not the stored one")
	}

	if res := issuer(t, h).Do(http.MethodPost, sendBatchPath(b.Batch.ID)+"/posted", map[string]any{"postedOn": "2026-09-13"}); res.Status != http.StatusBadRequest {
		t.Errorf("posted tomorrow = %d, want 400", res.Status)
	}
	res := issuer(t, h).Do(http.MethodPost, sendBatchPath(b.Batch.ID)+"/posted", map[string]any{"postedOn": "2026-09-12"})
	if res.Status != http.StatusOK {
		t.Fatalf("posted = %d %s", res.Status, res.Body)
	}
	var posted sendBatchJSON
	res.JSON(&posted)
	wantJob(t, "posted", jobOf(t, posted, person.ID), "printed", "", "print")
	var manual int
	if err := h.Pool().QueryRow(context.Background(), `SELECT count(*) FROM invoices.manual_deliveries WHERE invoice_id = $1 AND kind = 'posted'`, person.ID).Scan(&manual); err != nil || manual != 1 {
		t.Errorf("manual deliveries = %d %v, want one posted", manual, err)
	}
	if res := issuer(t, h).Do(http.MethodGet, sendBatchPath(b.Batch.ID)+"/print.zip", nil); res.Status != http.StatusNotFound {
		t.Errorf("print.zip once posted = %d, want 404", res.Status)
	}
}

// A preferred channel that refuses fails the job with its code: no
// fallback to e-mail.
func TestSendWorker_APreferenceThatRefusesFails(t *testing.T) {
	t.Parallel()
	h, mails, lookup := sendAllReady(t)
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.InvoiceDelivery = "ehf" })
	lookup.answer(peppol.Result{}, nil)
	inv := anIssued(t, h, customerAcme)
	b := sendAll(t, h, inv.ID)
	drainSends(t, h)
	wantJob(t, "EHF preferred, not receivable", jobOf(t, readBatch(t, h, b.Batch.ID), inv.ID), "failed", "peppol_not_receivable", "ehf")
	if n := len(mails.mails()); n != 0 {
		t.Errorf("%d mails, want none", n)
	}
	// The failure is the sender's attention item.
	items := attentionOf(t, issuer(t, h))
	if got := ofType(items, "sendBatchNotSent"); len(got) != 1 || got[0].EntityID != fmt.Sprint(b.Batch.ID) || deref32(got[0].Count) != int32(1) {
		t.Errorf("attention = %+v, want one sendBatchNotSent of 1", got)
	}
}

// From 2027 a Norwegian business is never e-mailed: without EHF the job is
// skipped buyer_norwegian_business_required. A foreign one still is.
func TestSendWorker_TheB2BDutySkips(t *testing.T) {
	t.Parallel()
	h, mails := sendReady(t)
	h.customers.edit(customerForeign, func(p *contracts.CustomerBillingProfile) { p.InvoiceEmail = "faktura@svenska.example" })
	acme, foreign := anIssued(t, h, customerAcme), anIssued(t, h, customerForeign)
	h.Advance(120 * 24 * time.Hour) // into 2027
	b := sendAll(t, h, acme.ID, foreign.ID)
	drainSends(t, h)
	got := readBatch(t, h, b.Batch.ID)
	wantJob(t, "a Norwegian business in 2027", jobOf(t, got, acme.ID), "skipped", "buyer_norwegian_business_required", "")
	wantJob(t, "a foreign business in 2027", jobOf(t, got, foreign.ID), "sent", "", "email")
	if n := len(mails.mails()); n != 1 {
		t.Errorf("%d mails, want the foreign one", n)
	}
}

// A credit note is never eFaktura: a person preferring it gets the credit
// note by e-mail.
func TestSendWorker_ACreditNoteFallsThrough(t *testing.T) {
	t.Parallel()
	h, mails := sendReady(t)
	h.customers.edit(customerPerson, func(p *contracts.CustomerBillingProfile) { p.InvoiceDelivery = "efaktura" })
	original := anIssued(t, h, customerPerson)
	credit := issued(t, h, creditDraft(t, h, original.ID).ID)
	b := sendAll(t, h, credit.ID)
	drainSends(t, h)
	wantJob(t, "a credit note preferring eFaktura", jobOf(t, readBatch(t, h, b.Batch.ID), credit.ID), "sent", "", "email")
	if n := len(mails.mails()); n != 1 {
		t.Errorf("%d mails, want 1", n)
	}
}

// eFaktura: a person found is handed over; one not found falls through to
// the next channel — print, on an installation without mail.
func TestSendWorker_Efaktura(t *testing.T) {
	t.Parallel()
	h := efakturaReady(t)
	found := efakturaInvoice(t, h, customerPerson)
	b := sendAll(t, h, found.ID)
	drainSends(t, h)
	j := jobOf(t, readBatch(t, h, b.Batch.ID), found.ID)
	wantJob(t, "a person found", j, "handed_over", "", "efaktura")
	if j.TransmissionID == nil {
		t.Errorf("no transmission: %+v", j)
	}

	h.customers.edit(customerPerson, func(p *contracts.CustomerBillingProfile) { p.Phone, p.InvoiceEmail = "999 99 999", "ukjent@example.no" })
	h.Exec(t, `DELETE FROM invoices.consumer_lookups`)
	missing := efakturaInvoice(t, h, customerPerson)
	b = sendAll(t, h, missing.ID)
	drainSends(t, h)
	wantJob(t, "a person not found", jobOf(t, readBatch(t, h, b.Batch.ID), missing.ID), "awaiting_print", "", "print")
}
