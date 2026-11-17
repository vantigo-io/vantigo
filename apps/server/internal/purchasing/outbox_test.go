package purchasing_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// historyOf is an order's history.
func historyOf(t *testing.T, c *modtest.Client, id int64) gen.PurchasingOrderHistory {
	t.Helper()
	res := c.Do(http.MethodGet, orderPath(id, "history"), nil)
	mustStatus(t, res, http.StatusOK, "history")
	var hist gen.PurchasingOrderHistory
	res.JSON(&hist)
	return hist
}

// A Send whose PDF cannot be stored is still never acknowledged without its
// mail: the mailing was queued in the send's transaction, a retry under the
// same clientRequestId answers it as it stands, and the worker stores the
// PDF and sends it.
func TestSend_StoreFailureLeavesTheMailingQueued(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	chooseChannel(t, h)
	c := h.SignIn(t, buyer...)
	o := createOrder(t, c, supNorsk, variantLine(varBolt, "2"))
	h.objects.failNextPuts(1)
	body := gen.PurchasingSendInput{ClientRequestId: uuid.New(), ExpectedUpdatedAt: o.UpdatedAt}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "send"), body), http.StatusServiceUnavailable, "mail_failed", "a send whose PDF is not stored")
	if n := h.Count(t, `SELECT count(*) FROM purchasing.mailings WHERE order_id = $1 AND status = 'queued' AND attempts = 1`, o.Id); n != 1 {
		t.Fatalf("queued mailings after the failed store = %d, want 1", n)
	}
	res := c.Do(http.MethodPost, orderPath(o.Id, "send"), body)
	mustStatus(t, res, http.StatusOK, "the retry under the same id")
	r := sendResultOf(t, res)
	if r.Order.Status != gen.PurchasingOrderStatusSent || r.Mailing == nil || r.Mailing.Status != gen.PurchasingMailingStatusQueued {
		t.Fatalf("the retry = %+v, mailing %+v", r.Order, r.Mailing)
	}
	if len(h.mail.Sent()) != 0 {
		t.Fatal("mailed without a stored PDF")
	}
	h.Advance(5 * time.Minute)
	if n := workerNamed(t, h, "purchasing.mail")(); n != 1 {
		t.Fatalf("the worker claimed %d, want 1", n)
	}
	if sent := h.mail.Sent(); len(sent) != 1 || len(sent[0].Out.Attachments) != 1 {
		t.Fatalf("the worker's mail = %+v", sent)
	}
	hist := historyOf(t, c, o.Id)
	if len(hist.Documents) != 1 || len(hist.Mailings) != 1 || hist.Mailings[0].Status != gen.PurchasingMailingStatusSent || hist.Mailings[0].Attempts != 2 {
		t.Fatalf("history = %+v", hist)
	}
}

// A request that dies between its commit and its mail leaves the mailing
// queued under its hold, with no PDF named; once the hold runs out the worker
// stores the PDF and sends it.
func TestMailWorker_RecoversACrashAfterTheCommit(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	chooseChannel(t, h)
	c := h.SignIn(t, buyer...)
	o := markSent(t, c, createOrder(t, c, supNorsk, variantLine(varBolt, "2")))
	// The state a crashed Send leaves: the order sent by mail, its mailing
	// queued and held, no PDF stored.
	h.Exec(t, `DELETE FROM purchasing.documents WHERE order_id = $1`, o.Id)
	h.Exec(t, `UPDATE purchasing.orders SET send_method = 'mail' WHERE id = $1`, o.Id)
	h.Exec(t, `INSERT INTO purchasing.mailings (order_id, kind, version, recipient, subject, channel_id, status, next_attempt_at, created_at)
VALUES ($1, 'order', 1, 'ordre@stal.example', '', $2, 'queued', $3, $4)`, o.Id, h.channel, h.Now().Add(2*time.Minute), h.Now())
	run := workerNamed(t, h, "purchasing.mail")
	if n := run(); n != 0 {
		t.Fatalf("the worker took a held mailing: %d", n)
	}
	h.Advance(3 * time.Minute)
	if n := run(); n != 1 {
		t.Fatalf("the worker claimed %d, want 1", n)
	}
	if sent := h.mail.Sent(); len(sent) != 1 || sent[0].Out.Subject != "Innkjøpsordre 1 fra Vantigo Demo AS" {
		t.Fatalf("the recovered mail = %+v", sent)
	}
	if n := h.Count(t, `SELECT count(*) FROM purchasing.mailings WHERE order_id = $1 AND status = 'sent' AND document_id IS NOT NULL`, o.Id); n != 1 {
		t.Errorf("sent mailings naming their PDF = %d", n)
	}
}

// Send again fails the earlier versions' still-queued mailings for good, and
// the worker refuses a mailing of a version below the order's.
func TestSendAgain_SupersedesQueuedMailings(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	chooseChannel(t, h)
	c := h.SignIn(t, buyer...)
	o := createOrder(t, c, supNorsk, variantLine(varBolt, "2"))
	h.mail.FailWith("Send", errors.New("421 try later"))
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "send"), gen.PurchasingSendInput{ClientRequestId: uuid.New(), ExpectedUpdatedAt: o.UpdatedAt}),
		http.StatusServiceUnavailable, "mail_failed", "a refused mail")
	h.mail.FailWith("Send", nil)
	o = getOrder(t, c, o.Id)
	res := c.Do(http.MethodPost, orderPath(o.Id, "send-again"), gen.PurchasingSendAgainInput{ExpectedUpdatedAt: o.UpdatedAt, Mail: new(bool)})
	mustStatus(t, res, http.StatusOK, "send again, stored only")
	hist := historyOf(t, c, o.Id)
	if len(hist.Mailings) != 1 || hist.Mailings[0].Status != gen.PurchasingMailingStatusFailed || !strings.HasPrefix(deref(hist.Mailings[0].LastError), "Superseded") {
		t.Fatalf("the version 1 mailing = %+v", hist.Mailings)
	}
	// A stale row the transaction did not see is refused by the worker.
	h.Exec(t, `INSERT INTO purchasing.mailings (order_id, kind, version, recipient, channel_id, status, next_attempt_at, created_at)
VALUES ($1, 'order', 1, 'ordre@stal.example', $2, 'queued', $3, $3)`, o.Id, h.channel, h.Now())
	h.Advance(time.Hour)
	if n := workerNamed(t, h, "purchasing.mail")(); n != 1 {
		t.Fatalf("the worker claimed %d, want 1", n)
	}
	if len(h.mail.Sent()) != 0 {
		t.Fatalf("a superseded version was mailed: %+v", h.mail.Sent())
	}
	if n := h.Count(t, `SELECT count(*) FROM purchasing.mailings WHERE order_id = $1 AND status = 'failed' AND last_error LIKE 'Superseded%'`, o.Id); n != 2 {
		t.Errorf("superseded mailings = %d, want 2", n)
	}
}

// Both workers, built as module.Workers builds them, finish their cycles on
// a pool of one connection: neither holds a connection while it takes
// another or calls out of the module.
func TestWorkers_OnAPoolOfOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	chooseChannel(t, h)
	c := h.SignIn(t, buyer...)
	o := createOrder(t, c, supNorsk, variantLine(varBolt, "2"))
	h.objects.failNextPuts(1)
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "send"), gen.PurchasingSendInput{ClientRequestId: uuid.New(), ExpectedUpdatedAt: o.UpdatedAt}),
		http.StatusServiceUnavailable, "mail_failed", "a send whose PDF is not stored")
	h.Exec(t, `INSERT INTO purchasing.deletions (storage_key, requested_at) VALUES ('order/0/gone.pdf', $1)`, h.Now())
	h.Advance(5 * time.Minute)

	cfg := h.Pool().Config().Copy()
	cfg.MaxConns, cfg.MinConns = 1, 0
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("pool of one: %v", err)
	}
	t.Cleanup(pool.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	ran := map[string]int{}
	for _, w := range h.Workers(h.DepsOn(pool)) {
		if !strings.HasPrefix(w.Name(), "purchasing.") {
			continue
		}
		n, err := purchasing.RunCycle(ctx, w)
		if err != nil {
			t.Fatal(err)
		}
		ran[w.Name()] = n
	}
	if ctx.Err() != nil || ran["purchasing.mail"] != 1 || ran["purchasing.deletions"] != 1 {
		t.Fatalf("on a pool of one: %v (%v)", ran, ctx.Err())
	}
	if len(h.mail.Sent()) != 1 {
		t.Errorf("mails = %d", len(h.mail.Sent()))
	}
}
