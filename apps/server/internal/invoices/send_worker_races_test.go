package invoices_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/consumerinvoice/mps/mpstest"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The invoices-send worker under contention (plan E7): two workers on one
// batch, two batches at once for one document, a lease that runs out
// mid-e-mail — and the whole cycle on a pool of one connection.

// Two workers on one batch: every document is mailed once.
func TestSendWorker_TwoWorkersSendEachDocumentOnce(t *testing.T) {
	t.Parallel()
	h, mails := sendReady(t)
	var ids []int64
	for range 3 {
		ids = append(ids, anIssued(t, h, customerPerson).ID, anIssued(t, h, customerAcme).ID)
	}
	b := sendAll(t, h, ids...)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := invoices.NewSendWorker(h.Deps())
			for {
				ok, err := w.ProcessOne(context.Background())
				if err != nil {
					errs <- err
					return
				}
				if !ok {
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("ProcessOne: %v", err)
	}
	got := readBatch(t, h, b.Batch.ID)
	if got.Batch.Counts["sent"] != int32(len(ids)) || got.Batch.FinishedAt == nil {
		t.Errorf("the batch: %+v, want %d sent and finished", got.Batch, len(ids))
	}
	if n := len(mails.mails()); n != len(ids) {
		t.Errorf("%d mails, want %d — one per document", n, len(ids))
	}
}

// Two batches enqueued at once for one document: one job, one skip.
func TestSendWorker_TwoBatchesAtOnceQueueOneJob(t *testing.T) {
	t.Parallel()
	h, mails := sendReady(t)
	inv := anIssued(t, h, customerPerson)
	c1, c2 := issuer(t, h), issuer(t, h)
	var wg sync.WaitGroup
	results := make([]sendBatchJSON, 2)
	for i, c := range []*modtest.Client{c1, c2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := enqueue(c, map[string]any{"invoiceIds": []int64{inv.ID}})
			if res.Status == http.StatusAccepted {
				res.JSON(&results[i])
			}
		}()
	}
	wg.Wait()
	states := map[string]int{}
	for _, r := range results {
		if len(r.Jobs) != 1 {
			t.Fatalf("a batch with %d jobs: %+v", len(r.Jobs), r)
		}
		states[r.Jobs[0].State+" "+orEmpty(r.Jobs[0].Code)]++
	}
	if states["queued "] != 1 || states["skipped send_in_progress"] != 1 {
		t.Errorf("the two batches' jobs: %v, want one queued and one send_in_progress", states)
	}
	drainSends(t, h)
	if n := len(mails.mails()); n != 1 {
		t.Errorf("%d mails, want 1", n)
	}
}

// deadClaim makes batch's job look claimed by a worker that died while it
// e-mailed: sending by e-mail, its lease run out a minute ago.
func deadClaim(t *testing.T, h *harness, batch int64) {
	t.Helper()
	now := h.Now()
	h.Exec(t, `UPDATE invoices.send_jobs SET state = 'sending', channel = 'email', attempts = 1, started_at = $2,
		lease_id = 'gone', lease_until = $3 WHERE batch_id = $1`, batch, now, now.Add(-time.Minute))
}

// A lease that runs out mid-e-mail (reading E4): with no delivery logged the
// job is uncertain and never mailed again; with one logged since the claim
// it is sent.
func TestSendWorker_AReclaimedEmailIsNeverSentTwice(t *testing.T) {
	t.Parallel()
	h, mails := sendReady(t)
	lost := anIssued(t, h, customerPerson)
	b := sendAll(t, h, lost.ID)
	deadClaim(t, h, b.Batch.ID)
	drainSends(t, h)
	j := jobOf(t, readBatch(t, h, b.Batch.ID), lost.ID)
	wantJob(t, "a reclaimed e-mail with no delivery", j, "uncertain", "send_uncertain", "email")
	if n := len(mails.mails()); n != 0 {
		t.Errorf("%d mails, want none: an uncertain job is never mailed by itself", n)
	}
	items := ofType(attentionOf(t, issuer(t, h)), "sendBatchNotSent")
	if len(items) != 1 {
		t.Errorf("attention = %+v, want the uncertain job's batch", items)
	}

	logged := anIssued(t, h, customerPerson)
	b = sendAll(t, h, logged.ID)
	deadClaim(t, h, b.Batch.ID)
	sent(t, h, logged.ID, nil) // the lost attempt's mail, logged
	drainSends(t, h)
	j = jobOf(t, readBatch(t, h, b.Batch.ID), logged.ID)
	wantJob(t, "a reclaimed e-mail with its delivery", j, "sent", "", "email")
	if j.DeliveryID == nil {
		t.Errorf("no delivery named: %+v", j)
	}
	if n := len(mails.mails()); n != 1 {
		t.Errorf("%d mails, want the one logged", n)
	}
}

// The worker on a pool of one connection: a batch of one of each channel —
// eFaktura, EHF, e-mail and print — reaches its end states; no step holds a
// connection while it waits for another.
func TestSendWorker_RunsTheCycleOnAPoolOfOne(t *testing.T) {
	t.Parallel()
	h, mails, _ := sendAllReady(t)
	withKidAgreement(t, h, 10, kid.Mod10)
	h.mps.SetClock(h.Now)
	if res := manager(t, h).Do(http.MethodPut, consumerProviderPath, map[string]any{
		"provider": "mps", "efakturaAgreement": "active", "avtalegiroAgreement": "active",
		"creditorAccount": "86011117947", "brandName": "Selger AS", "abbreviatedName": "SELGER",
	}); res.Status != http.StatusOK {
		t.Fatalf("PUT the consumer provider = %d %s", res.Status, res.Body)
	}
	h.customers.edit(customerPerson, func(p *contracts.CustomerBillingProfile) { p.Phone, p.InvoiceEmail = "912 34 567", personEmail })
	h.mps.AddPerson(mpstest.Person{Identifier: "123456789", Phone: personPhone, Email: personEmail,
		FirstName: "Kari", LastName: "Nordmann", AutoAccept: true})
	h.customers.edit(customerNoTerms, func(p *contracts.CustomerBillingProfile) { p.InvoiceDelivery = "paper" })
	person, acme, foreign := anIssued(t, h, customerPerson), anIssued(t, h, customerAcme), anIssued(t, h, customerForeign)
	paper := anIssued(t, h, customerNoTerms)
	b := sendAll(t, h, person.ID, acme.ID, foreign.ID, paper.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	one, err := db.Open(ctx, h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	d := h.Deps()
	d.Pool = one
	w := invoices.NewSendWorker(d)
	if err := w.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle on a pool of one: %v", err)
	}
	got := readBatch(t, h, b.Batch.ID)
	wantJob(t, "the person", jobOf(t, got, person.ID), "handed_over", "", "efaktura")
	wantJob(t, "Acme", jobOf(t, got, acme.ID), "handed_over", "", "ehf")
	wantJob(t, "the foreign business", jobOf(t, got, foreign.ID), "sent", "", "email")
	wantJob(t, "the paper one", jobOf(t, got, paper.ID), "awaiting_print", "", "print")
	if got.Batch.FinishedAt == nil || len(mails.mails()) != 1 {
		t.Errorf("the batch: %+v with %d mails, want finished with one", got.Batch, len(mails.mails()))
	}
}
