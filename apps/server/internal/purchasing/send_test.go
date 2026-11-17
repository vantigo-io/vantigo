package purchasing_test

import (
	"bytes"
	"errors"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// chooseChannel saves the settings with the harness's channel.
func chooseChannel(t *testing.T, h *harness) {
	t.Helper()
	a := h.SignIn(t, admin...)
	res := a.Do(http.MethodPut, "/api/v1/purchasing/settings", gen.PurchasingSettingsInput{
		OrderSeriesStart: 1, OrderFooter: "Leveringsbetingelser: DAP", PriceTolerancePercent: "2", SendChannelId: &h.channel,
		DeliveryAddresses: []gen.PurchasingSiteAddress{},
	})
	mustStatus(t, res, http.StatusOK, "save the settings")
}

func sendResultOf(t *testing.T, res *modtest.Response) gen.PurchasingSendResult {
	t.Helper()
	var r gen.PurchasingSendResult
	res.JSON(&r)
	return r
}

// Send allocates the number, snapshots, stores the PDF, mails it and
// publishes the event; a replay answers the first send and another body
// under the same id is refused.
func TestSend_MailsAndIsIdempotent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	c := h.SignIn(t, buyer...)
	o := createOrder(t, c, supNorsk, variantLine(varBolt, "2"))
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "send"), gen.PurchasingSendInput{ClientRequestId: uuid.New(), ExpectedUpdatedAt: o.UpdatedAt}),
		http.StatusServiceUnavailable, "sending_disabled", "send with no channel chosen")
	chooseChannel(t, h)

	id := uuid.New()
	body := gen.PurchasingSendInput{ClientRequestId: id, ExpectedUpdatedAt: o.UpdatedAt, Message: str("Takk!")}
	res := c.Do(http.MethodPost, orderPath(o.Id, "send"), body)
	mustStatus(t, res, http.StatusOK, "send")
	r := sendResultOf(t, res)
	if r.Order.Status != gen.PurchasingOrderStatusSent || r.Order.Number == nil || *r.Order.Number != 1 || r.Order.Version != 1 ||
		r.Mailing == nil || r.Mailing.Status != gen.PurchasingMailingStatusSent || r.Mailing.Recipient != "ordre@stal.example" {
		t.Fatalf("sent = %+v, mailing %+v", r.Order, r.Mailing)
	}
	if r.Order.DeliveryAddress.Name == nil || *r.Order.DeliveryAddress.Name != "Vantigo Demo AS, Hovedlager" {
		t.Errorf("delivery snapshot = %+v", r.Order.DeliveryAddress)
	}
	sent := h.mail.Sent()
	if len(sent) != 1 || sent[0].Out.Subject != "Innkjøpsordre 1 fra Vantigo Demo AS" || len(sent[0].Out.Attachments) != 1 ||
		!bytes.HasPrefix(sent[0].Out.Attachments[0].Content, []byte("%PDF")) {
		t.Fatalf("mail = %+v", sent)
	}
	if n := eventsNamed(t, h, "purchasing.order.sent", o.Id); n != 1 {
		t.Errorf("sent events = %d", n)
	}

	res = c.Do(http.MethodPost, orderPath(o.Id, "send"), body)
	mustStatus(t, res, http.StatusOK, "the replay")
	if again := sendResultOf(t, res); *again.Order.Number != 1 || again.Mailing == nil || again.Mailing.Id != r.Mailing.Id {
		t.Errorf("the replay = %+v", again)
	}
	if len(h.mail.Sent()) != 1 {
		t.Errorf("the replay mailed again")
	}
	body.Message = str("Annet")
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "send"), body), http.StatusConflict, "client_request_reused", "another body")
	other := createOrder(t, c, supNorsk, variantLine(varBolt, "1"))
	expectRefusal(t, c.Do(http.MethodPost, orderPath(other.Id, "send"), gen.PurchasingSendInput{ClientRequestId: id, ExpectedUpdatedAt: other.UpdatedAt}),
		http.StatusConflict, "client_request_reused", "the id for another order")

	// A supplier with no e-mail needs a recipient.
	person := createOrder(t, c, supPerson, variantLine(varBolt, "1"))
	expectRefusal(t, c.Do(http.MethodPost, orderPath(person.Id, "send"), gen.PurchasingSendInput{ClientRequestId: uuid.New(), ExpectedUpdatedAt: person.UpdatedAt}),
		http.StatusBadRequest, "recipient_missing", "no recipient")

	// The PDF and the history.
	res = c.Do(http.MethodGet, orderPath(o.Id, "pdf"), nil)
	mustStatus(t, res, http.StatusOK, "pdf")
	res = c.Do(http.MethodGet, orderPath(o.Id, "history"), nil)
	var hist gen.PurchasingOrderHistory
	res.JSON(&hist)
	if len(hist.Documents) != 1 || len(hist.Mailings) != 1 {
		t.Fatalf("history = %+v", hist)
	}
	mustStatus(t, c.Do(http.MethodGet, orderPath(o.Id, "documents", itoa(hist.Documents[0].Id)), nil), http.StatusOK, "the stored PDF")
	expectRefusal(t, c.Do(http.MethodGet, orderPath(o.Id, "documents", "99999"), nil), http.StatusNotFound, "not_found", "an unknown PDF")

	// Send again: version 2, revised, mailed; with mail false only stored.
	o = getOrder(t, c, o.Id)
	res = c.Do(http.MethodPost, orderPath(o.Id, "send-again"), gen.PurchasingSendAgainInput{ExpectedUpdatedAt: o.UpdatedAt})
	mustStatus(t, res, http.StatusOK, "send again")
	if r := sendResultOf(t, res); r.Order.Version != 2 || r.Mailing == nil || r.Mailing.Version != 2 {
		t.Errorf("sent again = %+v", r)
	}
	if got := h.mail.Sent(); len(got) != 2 || got[1].Out.Subject != "Revidert innkjøpsordre 1 fra Vantigo Demo AS" {
		t.Errorf("revised mail = %+v", got[len(got)-1].Out.Subject)
	}
	if _, ok := pdfModels.find("Revidert innkjøpsordre", "Ordre 1 – versjon 2"); !ok {
		t.Error("no revised PDF")
	}
	o = getOrder(t, c, o.Id)
	res = c.Do(http.MethodPost, orderPath(o.Id, "send-again"), gen.PurchasingSendAgainInput{ExpectedUpdatedAt: o.UpdatedAt, Mail: new(bool)})
	mustStatus(t, res, http.StatusOK, "store again")
	if len(h.mail.Sent()) != 2 {
		t.Error("mail false mailed")
	}

	// Cancel a mailed order: the supplier is sent its cancellation.
	o = getOrder(t, c, o.Id)
	res = c.Do(http.MethodPost, orderPath(o.Id, "cancel"), gen.PurchasingCancelInput{ExpectedUpdatedAt: o.UpdatedAt})
	mustStatus(t, res, http.StatusOK, "cancel")
	if r := sendResultOf(t, res); r.Order.Status != gen.PurchasingOrderStatusCancelled || r.Mailing == nil || r.Mailing.Kind != gen.PurchasingMailingKindCancellation {
		t.Errorf("cancelled = %+v", r)
	}
	if got := h.mail.Sent(); len(got) != 3 || got[2].Out.Subject != "Kansellert innkjøpsordre 1 fra Vantigo Demo AS" {
		t.Errorf("cancellation mail = %+v", got[len(got)-1].Out.Subject)
	}
	if m, ok := pdfModels.find("Kansellert innkjøpsordre", "Ordre 1"); !ok || m.Watermark != "KANSELLERT" {
		t.Errorf("cancellation PDF = %+v", m)
	}
}

// A mail the provider refuses leaves the order sent and its mailing queued;
// the purchasing.mail worker, built as worker mode builds it, sends it.
func TestSend_MailFailureQueuesForTheWorker(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	chooseChannel(t, h)
	c := h.SignIn(t, buyer...)
	o := createOrder(t, c, supGerman, variantLine(varBolt, "2"))
	h.mail.FailWith("Send", errors.New("421 try later"))
	res := c.Do(http.MethodPost, orderPath(o.Id, "send"), gen.PurchasingSendInput{ClientRequestId: uuid.New(), ExpectedUpdatedAt: o.UpdatedAt})
	expectRefusal(t, res, http.StatusServiceUnavailable, "mail_failed", "a refused mail")
	o = getOrder(t, c, o.Id)
	if o.Status != gen.PurchasingOrderStatusSent || o.LastMailingStatus == nil || *o.LastMailingStatus != gen.PurchasingOrderLastMailingStatusQueued {
		t.Fatalf("after a refused mail: %s, %v", o.Status, o.LastMailingStatus)
	}
	run := workerNamed(t, h, "purchasing.mail")
	if n := run(); n != 0 {
		t.Errorf("the worker claimed %d before the backoff ran out", n)
	}
	h.mail.FailWith("Send", nil)
	h.Advance(5 * time.Minute)
	if n := run(); n != 1 {
		t.Fatalf("the worker claimed %d, want 1", n)
	}
	sent := h.mail.Sent()
	if len(sent) != 1 || sent[0].Out.Subject != "Purchase order 1 from Vantigo Demo AS" {
		t.Fatalf("the retried mail = %+v", sent)
	}
	if o = getOrder(t, c, o.Id); *o.LastMailingStatus != gen.PurchasingOrderLastMailingStatusSent {
		t.Errorf("the mailing = %v", *o.LastMailingStatus)
	}
	if _, ok := pdfModels.find("Purchase order", "Order 1"); !ok {
		t.Error("no English PDF")
	}
	_ = workerNamed(t, h, "purchasing.deletions")
}

// The PDF in both languages names our company, the supplier, the delivery
// address, the lines, the total net of VAT and the number to quote; a
// draft's is marked Draft.
func TestPDF_BothLanguages(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, buyer...)
	nb := createOrder(t, c, supNorsk, variantLine(varBolt, "2"), textLine("Frakt", "1", "1250"))
	mustStatus(t, c.Do(http.MethodGet, orderPath(nb.Id, "pdf"), nil), http.StatusOK, "a draft's PDF")
	if m, ok := pdfModels.find("Innkjøpsordre", ""); !ok || m.Watermark != "UTKAST" {
		t.Errorf("draft PDF = %+v", m)
	}
	nb = markSent(t, c, nb)
	mustStatus(t, c.Do(http.MethodGet, orderPath(nb.Id, "pdf"), nil), http.StatusOK, "nb PDF")
	m, ok := pdfModels.find("Innkjøpsordre", "Ordre "+itoa(*nb.Number), func(m purchasing.PDFModel) bool { return m.Totals[0][1] == "NOK 1 250,00" })
	if !ok {
		t.Fatal("no nb model")
	}
	if m.Seller[0] != "Vantigo Demo AS" || m.Supplier[0] != "Norsk Stål AS" || m.Totals[0][1] != "NOK 1 250,00" ||
		m.Notes[0] != "Beløpene er uten mva." || m.Notes[1] != "Oppgi vårt ordrenummer "+itoa(*nb.Number)+" på fakturaen." {
		t.Errorf("nb model = %+v", m)
	}
	en := markSent(t, c, createOrder(t, c, supGerman, textLine("Freight", "2", "1000.5")))
	mustStatus(t, c.Do(http.MethodGet, orderPath(en.Id, "pdf"), nil), http.StatusOK, "en PDF")
	m, ok = pdfModels.find("Purchase order", "Order "+itoa(*en.Number), func(m purchasing.PDFModel) bool { return m.Totals[0][1] == "EUR 2,001.00" })
	if !ok || m.Totals[0][1] != "EUR 2,001.00" || m.Notes[0] != "Amounts exclude VAT." {
		t.Errorf("en model = %+v", m)
	}
}

// Concurrent sends take one gap-free number each.
func TestSend_SeriesUnderConcurrentSends(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, buyer...)
	a := h.SignIn(t, admin...)
	res := a.Do(http.MethodPut, "/api/v1/purchasing/settings", gen.PurchasingSettingsInput{OrderSeriesStart: 5000, PriceTolerancePercent: "2", DeliveryAddresses: []gen.PurchasingSiteAddress{}})
	mustStatus(t, res, http.StatusOK, "series start")
	var orders []gen.PurchasingOrder
	for range 8 {
		orders = append(orders, createOrder(t, c, supNorsk, variantLine(varBolt, "1")))
	}
	var wg sync.WaitGroup
	numbers := make([]int64, len(orders))
	for i, o := range orders {
		wg.Go(func() {
			r := c.Do(http.MethodPost, orderPath(o.Id, "mark-sent"), gen.PurchasingMarkSentInput{ClientRequestId: uuid.New(), ExpectedUpdatedAt: o.UpdatedAt})
			if r.Status == http.StatusOK {
				numbers[i] = *sendResultOf(t, r).Order.Number
			}
		})
	}
	wg.Wait()
	sort.Slice(numbers, func(i, j int) bool { return numbers[i] < numbers[j] })
	for i, n := range numbers {
		if n != int64(5000+i) {
			t.Fatalf("numbers = %v, want 5000..5007", numbers)
		}
	}
	var st gen.PurchasingSettings
	res = a.Do(http.MethodGet, "/api/v1/purchasing/settings", nil)
	res.JSON(&st)
	if st.NextOrderNumber != 5008 {
		t.Errorf("next = %d", st.NextOrderNumber)
	}
	expectRefusal(t, a.Do(http.MethodPut, "/api/v1/purchasing/settings", gen.PurchasingSettingsInput{OrderSeriesStart: 10, Revision: st.Revision,
		PriceTolerancePercent: "2", DeliveryAddresses: []gen.PurchasingSiteAddress{}}), http.StatusBadRequest, "series_start_too_low", "lower the start")
	expectRefusal(t, a.Do(http.MethodPut, "/api/v1/purchasing/settings", gen.PurchasingSettingsInput{OrderSeriesStart: 9000, Revision: st.Revision - 1,
		PriceTolerancePercent: "2", DeliveryAddresses: []gen.PurchasingSiteAddress{}}), http.StatusConflict, "version_conflict", "a stale revision")
	expectRefusal(t, a.Do(http.MethodPut, "/api/v1/purchasing/settings", gen.PurchasingSettingsInput{OrderSeriesStart: 9000, Revision: st.Revision,
		PriceTolerancePercent: "2", DeliveryAddresses: []gen.PurchasingSiteAddress{{SiteId: 999}}}), http.StatusBadRequest, "site_not_found", "an unknown site")
	// The settings' delivery address goes on the next order.
	res = a.Do(http.MethodPut, "/api/v1/purchasing/settings", gen.PurchasingSettingsInput{OrderSeriesStart: 9000, Revision: st.Revision,
		PriceTolerancePercent: "2.5", DefaultSiteId: i64(siteSecond),
		DeliveryAddresses: []gen.PurchasingSiteAddress{{SiteId: siteSecond, Address: gen.PurchasingAddress{Name: str("Vestlager"), Line1: str("Kaia 3"), City: str("Bergen")}}}})
	mustStatus(t, res, http.StatusOK, "save")
	res = c.Do(http.MethodPost, ordersPath, gen.PurchasingOrderInput{SupplierId: supNorsk, Lines: &[]gen.PurchasingLineInput{}})
	mustStatus(t, res, http.StatusCreated, "an order at the default site")
	if o := orderOf(t, res); o.SiteId != siteSecond || deref(o.DeliveryAddress.Line1) != "Kaia 3" {
		t.Errorf("default site order = %d %+v", o.SiteId, o.DeliveryAddress)
	}
}
