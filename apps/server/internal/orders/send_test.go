package orders_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// chooseChannel saves the settings with the harness's channel to send from.
func chooseChannel(t *testing.T, h *harness) {
	t.Helper()
	admin := h.SignIn(t, settingsKey...)
	ch := h.channel
	res := admin.Do(http.MethodPut, settingsPath, gen.OrdersSettingsInput{
		Revision: 0, QuoteSeriesStart: 1, OrderSeriesStart: 1, QuoteValidityDays: 30, DefaultPaymentTermsDays: 14,
		MailTexts: []gen.OrdersMailText{}, SendChannelId: &ch,
	})
	mustStatus(t, res, http.StatusOK, "choose the channel")
}

// Sending mails the PDF of this revision through the chosen channel, stores
// it, freezes the quote — valid until today plus the validity — logs the
// send and publishes orders.quote.sent.v1 (O20, O24, O25). The snapshot no
// longer follows the customer (O14), and the draft's editor is closed.
func TestSendQuote_MailsStoresFreezesAndPublishes(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	chooseChannel(t, h)
	c := h.SignIn(t, quoter...)
	q := createQuote(t, c, custBusiness, productLine(varBolt, 2))
	res := c.Do(http.MethodPost, quotePath(q.Id, "send"), gen.OrdersSendInput{
		Recipient: "kari@acme.example", Cc: &[]openapi_types.Email{"boss@acme.example"}, Revision: &q.Revision, Message: str("As agreed."),
	})
	mustStatus(t, res, http.StatusOK, "send")
	sent := quoteOf(t, res)
	if sent.Status != gen.OrdersQuoteStatusSent || sent.SentAt == nil || sent.ValidUntil == nil ||
		sent.ValidUntil.Sub(h.Now()) < 29*24*time.Hour || sent.ValidUntil.Sub(h.Now()) > 31*24*time.Hour {
		t.Errorf("sent %+v; want sent, valid 30 days", sent)
	}
	mails := h.mail.Sent()
	if len(mails) != 1 || mails[0].Channel != h.channel || mails[0].Out.To[0] != "kari@acme.example" || len(mails[0].Out.Cc) != 1 ||
		len(mails[0].Out.Attachments) != 1 || !bytes.HasPrefix(mails[0].Out.Attachments[0].Content, []byte("%PDF")) ||
		!strings.Contains(mails[0].Out.Subject, "Tilbud "+strconv.FormatInt(q.Number, 10)) || !strings.Contains(mails[0].Out.TextBody, "As agreed.") {
		t.Fatalf("mails %+v; want one with the PDF, the subject and the message", mails)
	}

	var docs gen.OrdersStoredDocumentList
	res = c.Do(http.MethodGet, quotePath(q.Id, "documents"), nil)
	mustStatus(t, res, http.StatusOK, "documents")
	res.JSON(&docs)
	if len(docs.Items) != 1 || docs.Items[0].Version != 1 || docs.Items[0].Kind != gen.OrdersStoredDocumentKindQuote {
		t.Errorf("documents %+v; want revision 1's", docs)
	}
	stored := c.Do(http.MethodGet, quotePath(q.Id, "pdf"), nil)
	mustStatus(t, stored, http.StatusOK, "stored PDF")
	if !bytes.Equal(stored.Body, mails[0].Out.Attachments[0].Content) {
		t.Error("the stored PDF is not the one mailed")
	}
	var mailings gen.OrdersMailingList
	res = c.Do(http.MethodGet, quotePath(q.Id, "mailings"), nil)
	mustStatus(t, res, http.StatusOK, "mailings")
	res.JSON(&mailings)
	if len(mailings.Items) != 1 || mailings.Items[0].Status != gen.OrdersMailingStatusSent || mailings.Items[0].Recipient != "kari@acme.example" ||
		len(mailings.Items[0].Cc) != 1 || mailings.Items[0].SentBy == nil {
		t.Errorf("mailings %+v; want the one send", mailings)
	}
	if n := eventsNamed(t, h, "orders.quote.sent", q.Id); n != 1 {
		t.Errorf("%d sent events, want 1", n)
	}

	h.dir.update(custBusiness, func(p *contracts.CustomerBillingProfile) { p.Name = "Renamed AS" })
	res = c.Do(http.MethodGet, quotePath(q.Id), nil)
	mustStatus(t, res, http.StatusOK, "get")
	if got := quoteOf(t, res); got.Customer.Name != "Acme AS" {
		t.Errorf("a sent quote's snapshot moved to %q", got.Customer.Name)
	}
	expectRefusal(t, c.Do(http.MethodPut, quotePath(q.Id), gen.OrdersQuoteUpdate{Revision: 1, CustomerId: custBusiness, Lines: []gen.OrdersLineInput{}}),
		http.StatusConflict, "quote_not_draft", "edit a sent quote")
	expectRefusal(t, c.Do(http.MethodPost, quotePath(q.Id, "send"), gen.OrdersSendInput{Recipient: "kari@acme.example", Revision: &q.Revision}),
		http.StatusConflict, "quote_not_draft", "send again")
}

// Without Communications there is no send (sending_disabled), and nothing is
// changed; Mark as sent works all the same (O25).
func TestSendQuote_WithoutCommunicationsOnlyMarkAsSent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	q := createQuote(t, c, custBusiness, productLine(varBolt, 1))
	expectRefusal(t, c.Do(http.MethodPost, quotePath(q.Id, "send"), gen.OrdersSendInput{Recipient: "kari@acme.example", Revision: &q.Revision}),
		http.StatusServiceUnavailable, "sending_disabled", "send without Communications")
	if n := h.Count(t, `SELECT count(*) FROM orders.documents`); n != 0 {
		t.Errorf("%d documents stored by a refused send", n)
	}
	in := h.Now().AddDate(0, 0, 10)
	res := c.Do(http.MethodPost, quotePath(q.Id, "mark-sent"), gen.OrdersMarkSentInput{Revision: 1, ValidUntil: dateOf(time.Date(in.Year(), in.Month(), in.Day(), 0, 0, 0, 0, time.UTC))})
	mustStatus(t, res, http.StatusOK, "mark sent")
	if got := quoteOf(t, res); got.Status != gen.OrdersQuoteStatusSent || got.ValidUntil == nil {
		t.Errorf("marked %+v", got)
	}
	if n := eventsNamed(t, h, "orders.quote.sent", q.Id); n != 1 {
		t.Errorf("%d sent events, want 1", n)
	}
	expectRefusal(t, c.Do(http.MethodPost, quotePath(q.Id, "mark-sent"), gen.OrdersMarkSentInput{Revision: 1}),
		http.StatusConflict, "quote_not_draft", "mark sent again")
}

// A send the provider does not take leaves the quote a draft and lists the
// failed send (O25).
func TestSendQuote_AFailedMailLeavesTheDraft(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	chooseChannel(t, h)
	h.mail.FailWith("Send", errors.New("550 mailbox unavailable for kari@acme.example"))
	c := h.SignIn(t, quoter...)
	q := createQuote(t, c, custBusiness, productLine(varBolt, 1))
	expectRefusal(t, c.Do(http.MethodPost, quotePath(q.Id, "send"), gen.OrdersSendInput{Recipient: "kari@acme.example", Revision: &q.Revision}),
		http.StatusServiceUnavailable, "mail_failed", "send")
	res := c.Do(http.MethodGet, quotePath(q.Id), nil)
	mustStatus(t, res, http.StatusOK, "get")
	if got := quoteOf(t, res); got.Status != gen.OrdersQuoteStatusDraft {
		t.Errorf("status %s after a failed send, want draft", got.Status)
	}
	var mailings gen.OrdersMailingList
	res = c.Do(http.MethodGet, quotePath(q.Id, "mailings"), nil)
	mustStatus(t, res, http.StatusOK, "mailings")
	res.JSON(&mailings)
	if len(mailings.Items) != 1 || mailings.Items[0].Status != gen.OrdersMailingStatusFailed || mailings.Items[0].LastError == nil {
		t.Errorf("mailings %+v; want one failed", mailings)
	}
}

// A quote is sent only when every product line is priced (line_unpriced) and
// has a VAT code (vat_code_unmapped), optional lines included (O16).
func TestMarkQuoteSent_RefusesUnpricedAndUnmappedLines(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	q := createQuote(t, c, custBusiness, productLine(varBolt, 1), productLine(varNoPrice, 1), productLine(varUnmapped, 1))
	p := expectRefusal(t, c.Do(http.MethodPost, quotePath(q.Id, "mark-sent"), gen.OrdersMarkSentInput{Revision: 1}),
		http.StatusBadRequest, "line_unpriced", "mark sent")
	if p.Lines == nil || len(*p.Lines) != 2 || (*p.Lines)[1].Code != "vat_code_unmapped" {
		t.Errorf("lines %+v; want the unpriced and the unmapped", p.Lines)
	}
	past := h.Now().AddDate(0, 0, -3)
	ok := createQuote(t, c, custBusiness, productLine(varBolt, 1))
	expectRefusal(t, c.Do(http.MethodPost, quotePath(ok.Id, "mark-sent"), gen.OrdersMarkSentInput{Revision: 1,
		ValidUntil: dateOf(time.Date(past.Year(), past.Month(), past.Day(), 0, 0, 0, 0, time.UTC))}), http.StatusBadRequest, "", "valid until the past")
}

// Revise moves a sent quote back to a draft at the next revision; each sent
// revision's PDF is kept and listed, and the next prints its revision (O20).
func TestReviseQuote_KeepsEveryRevisionsPDF(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	q := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))
	expectRefusal(t, c.Do(http.MethodPost, quotePath(q.Id, "revise"), gen.OrdersRevisionInput{Revision: 2}), http.StatusConflict, "version_conflict", "stale")
	res := c.Do(http.MethodPost, quotePath(q.Id, "revise"), gen.OrdersRevisionInput{Revision: 1})
	mustStatus(t, res, http.StatusOK, "revise")
	rev := quoteOf(t, res)
	if rev.Status != gen.OrdersQuoteStatusDraft || rev.Revision != 2 || rev.Number != q.Number {
		t.Fatalf("revised %+v; want draft 2 of the same number", rev)
	}
	expectRefusal(t, c.Do(http.MethodPost, quotePath(q.Id, "revise"), gen.OrdersRevisionInput{Revision: 2}), http.StatusConflict, "quote_not_sent", "revise a draft")
	expectRefusal(t, c.Do(http.MethodDelete, quotePath(q.Id), nil), http.StatusConflict, "quote_not_draft", "delete a revised draft")
	markSent(t, c, rev)
	if _, ok := pdfModels.find("Tilbud", "Tilbud "+strconv.FormatInt(q.Number, 10)+" – revisjon 2"); !ok {
		t.Error("no PDF printed revision 2")
	}
	var docs gen.OrdersStoredDocumentList
	res = c.Do(http.MethodGet, quotePath(q.Id, "documents"), nil)
	mustStatus(t, res, http.StatusOK, "documents")
	res.JSON(&docs)
	if len(docs.Items) != 2 || docs.Items[0].Version != 2 || docs.Items[1].Version != 1 {
		t.Errorf("documents %+v; want revisions 2 and 1", docs)
	}
	one := c.Do(http.MethodGet, quotePath(q.Id, "pdf")+"?revision=1", nil)
	two := c.Do(http.MethodGet, quotePath(q.Id, "pdf"), nil)
	mustStatus(t, one, http.StatusOK, "revision 1")
	mustStatus(t, two, http.StatusOK, "revision 2")
	if bytes.Equal(one.Body, two.Body) {
		t.Error("revision 1's PDF is revision 2's")
	}
	mustStatus(t, c.Do(http.MethodGet, quotePath(q.Id, "pdf")+"?revision=3", nil), http.StatusNotFound, "revision 3")
	if keys := h.objects.keys(); len(keys) != 2 || !strings.HasPrefix(keys[0], "orders/quote/") {
		t.Errorf("stored objects %v; want two under orders/quote/", keys)
	}
	pro := c.Do(http.MethodGet, quotePath(q.Id, "proforma"), nil)
	mustStatus(t, pro, http.StatusOK, "pro forma")
	if _, ok := pdfModels.find("Proforma – ikke en faktura", "Tilbud "+strconv.FormatInt(q.Number, 10)+" – revisjon 2"); !ok {
		t.Error("no pro forma printed")
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.documents`); n != 2 {
		t.Errorf("%d documents; the pro forma must not be stored", n)
	}
}

// The discount's visibility (O22): with it, the list price and the discount
// print; without it, the net unit price only.
func TestQuotePDF_TheDiscountsVisibility(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	l := productLine(varBolt, 2)
	l.DiscountPercent = f64(10)
	hidden := false
	res := c.Do(http.MethodPost, quotesPath, gen.OrdersQuoteInput{ClientRequestId: uuid.New(), CustomerId: custBusiness, DiscountVisible: &hidden,
		Lines: &[]gen.OrdersLineInput{l}})
	mustStatus(t, res, http.StatusCreated, "create")
	q := quoteOf(t, res)
	mustStatus(t, c.Do(http.MethodGet, quotePath(q.Id, "pdf"), nil), http.StatusOK, "pdf")
	m, ok := pdfModels.find("Tilbud", "Tilbud "+strconv.FormatInt(q.Number, 10))
	if !ok || len(m.Header) != 5 || m.Rows[0][3] != "90,00" || m.Rows[0][4] != "180,00" {
		t.Errorf("model %+v; want the net unit price 90,00 and 180,00, no discount column", m)
	}
}

// The orders.mail worker sends a queued confirmation with its stored PDF,
// once per triggering event, on a pool of one: every step returns its
// connection before the next is taken, and no call out of the module is made
// while one is held (O25).
func TestMailWorker_SendsAQueuedMailingOnAPoolOfOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	chooseChannel(t, h)
	c := h.SignIn(t, quoter...)
	q := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))
	res := c.Do(http.MethodPost, quotePath(q.Id, "accept"), gen.OrdersAcceptInput{Revision: 1, Into: gen.DraftOrder})
	mustStatus(t, res, http.StatusOK, "accept")
	var a gen.OrdersAccepted
	res.JSON(&a)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := orders.StoreOrderConfirmation(ctx, h.Deps(), a.Order.Id); err != nil {
		t.Fatalf("store the confirmation: %v", err)
	}
	event := uuid.New()
	for i, want := range []bool{true, false} {
		queued, err := orders.QueueOrderMailing(ctx, h.Deps(), a.Order.Id, 1, "kari@acme.example", event)
		if err != nil || queued != want {
			t.Fatalf("queue %d = %v, %v; want %v (once per event)", i, queued, err, want)
		}
	}
	one, err := db.Open(ctx, h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	n, err := orders.RunMailCycle(ctx, h.DepsOn(one))
	if err != nil || n != 1 {
		t.Fatalf("cycle = %d, %v; want one mailing claimed", n, err)
	}
	mails := h.mail.Sent()
	if len(mails) != 1 || !strings.Contains(mails[0].Out.Subject, "Ordrebekreftelse "+strconv.FormatInt(a.Order.Number, 10)) ||
		len(mails[0].Out.Attachments) != 1 {
		t.Fatalf("mails %+v; want the confirmation with its PDF", mails)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.mailings WHERE status = 'sent' AND document_id IS NOT NULL AND sent_at IS NOT NULL`); n != 1 {
		t.Errorf("%d sent mailings, want 1", n)
	}
	if n, _ := orders.RunMailCycle(ctx, h.DepsOn(one)); n != 0 {
		t.Errorf("a second cycle claimed %d, want none", n)
	}
}

// A queued mailing that fails is retried with backoff and failed after the
// last attempt; one with no channel to send from fails at once.
func TestMailWorker_BacksOffAndFails(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	chooseChannel(t, h)
	c := h.SignIn(t, quoter...)
	q := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))
	res := c.Do(http.MethodPost, quotePath(q.Id, "accept"), gen.OrdersAcceptInput{Revision: 1, Into: gen.DraftOrder})
	mustStatus(t, res, http.StatusOK, "accept")
	var a gen.OrdersAccepted
	res.JSON(&a)
	ctx := context.Background()
	if _, err := orders.QueueOrderMailing(ctx, h.Deps(), a.Order.Id, 1, "kari@acme.example", uuid.New()); err != nil {
		t.Fatal(err)
	}
	// No PDF stored yet: retried, not failed.
	if n, _ := orders.RunMailCycle(ctx, h.Deps()); n != 1 {
		t.Fatalf("claimed %d, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.mailings WHERE status = 'queued' AND attempts = 1 AND last_error IS NOT NULL AND next_attempt_at > $1`, h.Now()); n != 1 {
		t.Errorf("%d mailings put back with backoff, want 1", n)
	}
	if orders.MailBackoff(1) != 30*time.Second || orders.MailBackoff(3) != 2*time.Minute || orders.MailBackoff(20) != time.Hour {
		t.Errorf("backoff %v %v %v", orders.MailBackoff(1), orders.MailBackoff(3), orders.MailBackoff(20))
	}
	h.Exec(t, `UPDATE orders.mailings SET attempts = 7, next_attempt_at = $1`, h.Now().Add(-time.Minute))
	if n, _ := orders.RunMailCycle(ctx, h.Deps()); n != 1 {
		t.Fatalf("claimed %d, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.mailings WHERE status = 'failed' AND attempts = 8`); n != 1 {
		t.Errorf("%d failed mailings, want the one at its last attempt", n)
	}
	if len(h.mail.Sent()) != 0 {
		t.Error("a mailing without its PDF was sent")
	}
}
