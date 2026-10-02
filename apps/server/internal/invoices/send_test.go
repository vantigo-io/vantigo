package invoices_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func sendPath(id int64) string { return fmt.Sprintf("%s/%d/send", invoicesPath, id) }

// deliveryJSON is one send on a document, as the response carries it.
type deliveryJSON struct {
	ID           int64  `json:"id"`
	Recipient    string `json:"recipient"`
	Subject      string `json:"subject"`
	SentAt       string `json:"sentAt"`
	SentByUserID string `json:"sentByUserId"`
}

// sendDefaultsJSON is what the Send dialog opens with.
type sendDefaultsJSON struct {
	Recipient  *string  `json:"recipient"`
	Preference *string  `json:"preference"`
	Warnings   []string `json:"warnings"`
}

// fakeSMTP stands in for mail.SendOutbound: it records every envelope it is
// handed, with the mail configuration it came with, fails on demand, and can
// hold a send while a test does something.
type fakeSMTP struct {
	mu   sync.Mutex
	sent []sentMail
	err  error
	hold func(ctx context.Context)
}

type sentMail struct {
	cfg config.MailConfig
	out mail.Outbound
}

func (f *fakeSMTP) send(ctx context.Context, cfg config.MailConfig, out mail.Outbound) error {
	f.mu.Lock()
	hold := f.hold
	f.mu.Unlock()
	if hold != nil {
		hold(ctx)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentMail{cfg: cfg, out: out})
	return nil
}

// fail makes every send fail with err, nil to stop.
func (f *fakeSMTP) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// holdWith runs fn inside every send before it answers.
func (f *fakeSMTP) holdWith(fn func(ctx context.Context)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hold = fn
}

// mails is every envelope sent so far.
func (f *fakeSMTP) mails() []sentMail {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent)
}

// last is the one envelope a test expects to have been sent since it had n.
func (f *fakeSMTP) last(t *testing.T, n int) sentMail {
	t.Helper()
	sent := f.mails()
	if len(sent) != n+1 {
		t.Fatalf("%d mails sent, want %d", len(sent), n+1)
	}
	return sent[n]
}

// sendReady is an installation that can send: the smtp driver configured,
// the SMTP seam faked, a complete seller saved, and Acme and Kari Nordmann
// with an invoice e-mail in the directory.
func sendReady(t *testing.T) (*harness, *fakeSMTP) {
	t.Helper()
	fake := &fakeSMTP{}
	h := newHarness(t, modtest.WithSMTPSend(fake.send), modtest.WithEnv("MAIL_DRIVER", "smtp"),
		modtest.WithEnv("SMTP_HOST", "smtp.example.invalid"), modtest.WithEnv("SMTP_FROM", "faktura@example.invalid"))
	saveSeller(t, h, completeSeller(1))
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.InvoiceEmail = "faktura@acme.example" })
	h.customers.edit(customerPerson, func(p *contracts.CustomerBillingProfile) { p.InvoiceEmail = "kari@example.org" })
	return h, fake
}

// sender is a caller who may send: invoices:issue.
func sender(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "invoices:access", "invoices:issue")
}

// sendAs posts a send of id with body as c.
func sendAs(c *modtest.Client, id int64, body map[string]any) *modtest.Response {
	if body == nil {
		body = map[string]any{}
	}
	return c.Do(http.MethodPost, sendPath(id), body)
}

// sent sends id as a sender and answers the document, failing unless it went.
func sent(t *testing.T, h *harness, id int64, body map[string]any) invoiceJSON {
	t.Helper()
	res := sendAs(sender(t, h), id, body)
	if res.Status != http.StatusOK {
		t.Fatalf("send %d = %d %s, want 200", id, res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	return inv
}

// sendRefused asserts res is a status-coded problem with code.
func sendRefused(t *testing.T, what string, res *modtest.Response, status int, code string) {
	t.Helper()
	if res.Status != status {
		t.Errorf("%s = %d %s, want %d %s", what, res.Status, res.Body, status, code)
		return
	}
	if p := problemOf(t, res); p.Code != code {
		t.Errorf("%s = %s (%s), want %s", what, p.Code, p.Detail, code)
	}
}

// deliveryRows is how many delivery rows document id has.
func deliveryRows(t *testing.T, h *harness, id int64) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM invoices.deliveries WHERE invoice_id = $1`, id)
}

// storedPDF is the bytes the object store holds for document id.
func storedPDF(t *testing.T, h *harness, id int64) []byte {
	t.Helper()
	key := modtest.One[string](t, h.Harness, `SELECT pdf_object_key FROM invoices.invoices WHERE id = $1`, id)
	return h.objects.object(key)
}

// An installation that cannot send says so before anything else (D4 step 1):
// the log driver answers mail_unavailable for an issued document, a draft and
// an id that does not exist alike, and nothing is read or sent.
func TestSend_MailUnavailableIsJudgedFirst(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := issuedAcme(t, h)
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 1, 100, vat25)))
	c, userID := h.SignInUser(t, "invoices:access", "invoices:issue")
	for _, id := range []int64{inv.ID, draft.ID, 999999} {
		res := sendAs(c, id, nil)
		sendRefused(t, fmt.Sprintf("send %d on the log driver", id), res, http.StatusServiceUnavailable, "mail_unavailable")
	}
	if calls := contractCalls.by(userID); len(calls) != 0 {
		t.Errorf("calls out of the module = %+v, want none", calls)
	}
	if n := deliveryRows(t, h, inv.ID); n != 0 {
		t.Errorf("%d delivery rows, want none", n)
	}
}

// A draft is no document to send (invoice_draft), an unknown id is a bare
// 404, and a customer this module has anonymised is never written to again
// (customer_anonymised, D4 step 3) — override or not. Nothing is sent.
func TestSend_RefusesADraftAndAnAnonymisedCustomer(t *testing.T) {
	t.Parallel()
	h, fake := sendReady(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 1, 100, vat25)))
	c := sender(t, h)
	sendRefused(t, "send a draft", sendAs(c, draft.ID, nil), http.StatusConflict, "invoice_draft")
	if res := sendAs(c, 999999, nil); res.Status != http.StatusNotFound {
		t.Errorf("send an unknown id = %d %s, want 404", res.Status, res.Body)
	}

	inv := issuedAcme(t, h)
	h.Exec(t, `INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now())`, customerAcme)
	sendRefused(t, "send to an anonymised customer", sendAs(c, inv.ID, nil), http.StatusConflict, "customer_anonymised")
	sendRefused(t, "send to an anonymised customer with an override", sendAs(c, inv.ID, map[string]any{"recipient": "someone@example.org"}),
		http.StatusConflict, "customer_anonymised")
	if len(fake.mails()) != 0 || deliveryRows(t, h, inv.ID) != 0 {
		t.Errorf("%d mails, %d rows; want nothing sent and nothing logged", len(fake.mails()), deliveryRows(t, h, inv.ID))
	}
}

// Without an override, the recipient is the customer's current invoice
// e-mail; a profile without one is no_invoice_email (D4 step 4) — and once
// the customer has one, the same document goes.
func TestSend_NoInvoiceEmail(t *testing.T) {
	t.Parallel()
	h, fake := sendReady(t)
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.InvoiceEmail = "" })
	inv := issuedAcme(t, h)
	sendRefused(t, "send without an invoice e-mail", sendAs(sender(t, h), inv.ID, nil), http.StatusConflict, "no_invoice_email")
	if len(fake.mails()) != 0 {
		t.Fatalf("%d mails sent, want none", len(fake.mails()))
	}

	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.InvoiceEmail = "regnskap@acme.example" })
	sent(t, h, inv.ID, nil)
	if got := fake.last(t, 0).out.To; !slices.Equal(got, []string{"regnskap@acme.example"}) {
		t.Errorf("To = %v, want the profile's current invoice e-mail", got)
	}
}

// An override wins over the profile's address and is held to the settings'
// rule (D4): a bare address that parses to itself, at most 254 characters —
// so "Name <a@b>" is refused, and so is an empty one. A refusal names
// recipient and sends nothing.
func TestSend_TheOverrideWinsAndIsValidated(t *testing.T) {
	t.Parallel()
	h, fake := sendReady(t)
	inv := issuedAcme(t, h)
	c := sender(t, h)
	for _, bad := range []string{
		"Kari Nordmann <kari@example.org>", "not an address", "", "   ", strings.Repeat("a", 245) + "@example.no",
	} {
		res := sendAs(c, inv.ID, map[string]any{"recipient": bad})
		if res.Status != http.StatusBadRequest {
			t.Errorf("recipient %q = %d %s, want 400", bad, res.Status, res.Body)
			continue
		}
		if p := problemOf(t, res); len(p.Errors["recipient"]) == 0 {
			t.Errorf("recipient %q = %+v, want an error on recipient", bad, p.Errors)
		}
	}
	if len(fake.mails()) != 0 {
		t.Fatalf("%d mails sent, want none", len(fake.mails()))
	}

	sent(t, h, inv.ID, map[string]any{"recipient": " bokholder@example.org "})
	if got := fake.last(t, 0).out.To; !slices.Equal(got, []string{"bokholder@example.org"}) {
		t.Errorf("To = %v, want the override, trimmed", got)
	}
	// The override needs no address in the directory at all.
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.InvoiceEmail = "" })
	sent(t, h, inv.ID, map[string]any{"recipient": "bokholder@example.org"})
	fake.last(t, 1)
}

// The envelope (D4 step 6): from the installation's address under the
// seller snapshot's name, Reply-To the current settings' e-mail and none
// when the settings have none, to the recipient, the Norwegian subject and
// body in full, the stored PDF's very bytes attached under the download's
// name as application/pdf, and a fresh bare Message-ID on the module's
// domain.
func TestSend_TheEnvelope(t *testing.T) {
	t.Parallel()
	h, fake := sendReady(t)
	inv := issuedAcme(t, h)
	sent(t, h, inv.ID, nil)
	m := fake.last(t, 0)

	if m.cfg.Driver != "smtp" || m.cfg.Host != "smtp.example.invalid" || m.cfg.From != "faktura@example.invalid" {
		t.Errorf("mail config = %+v, want the installation's smtp driver, host and from", m.cfg)
	}
	out := m.out
	if out.DisplayName != "Kraft-Verket AS" || out.ReplyTo != "faktura@kraft-verket.no" ||
		!slices.Equal(out.To, []string{"faktura@acme.example"}) || len(out.Cc)+len(out.Bcc) != 0 {
		t.Errorf("envelope = name %q reply-to %q to %v cc %v bcc %v; want Kraft-Verket AS, faktura@kraft-verket.no, the invoice e-mail, nobody else",
			out.DisplayName, out.ReplyTo, out.To, out.Cc, out.Bcc)
	}
	if out.Subject != "Faktura 1 fra Kraft-Verket AS" {
		t.Errorf("subject = %q", out.Subject)
	}
	wantBody := "Hei,\n\n" +
		"Vedlagt følger faktura 1 fra Kraft-Verket AS på NOK 2 500,00, med forfall 12.10.2026.\n" +
		"Beløpet betales til kontonummer 86011117947. Merk betalingen med fakturanummer 1.\n\n" +
		"Med vennlig hilsen\nKraft-Verket AS\n"
	if out.TextBody != wantBody || out.HTMLBody != "" {
		t.Errorf("body =\n%q (html %q)\nwant\n%q, no HTML", out.TextBody, out.HTMLBody, wantBody)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}@vantigo\.invalid$`).MatchString(out.MessageID) {
		t.Errorf("Message-ID = %q, want a bare <uuid>@vantigo.invalid", out.MessageID)
	}
	if len(out.Attachments) != 1 {
		t.Fatalf("attachments = %d, want the PDF", len(out.Attachments))
	}
	a := out.Attachments[0]
	if a.FileName != "faktura-1.pdf" || a.ContentType != "application/pdf" || a.Inline || a.ContentID != "" ||
		string(a.Content) != string(storedPDF(t, h, inv.ID)) {
		t.Errorf("attachment = %q %q inline %v, %d bytes; want faktura-1.pdf, application/pdf, the stored object's bytes",
			a.FileName, a.ContentType, a.Inline, len(a.Content))
	}

	// Replies reach today's mailbox: the settings now, not the snapshot.
	settings := completeSeller(2)
	settings["email"] = ""
	saveSeller(t, h, settings)
	sent(t, h, inv.ID, nil)
	again := fake.last(t, 1).out
	if again.ReplyTo != "" || again.DisplayName != "Kraft-Verket AS" {
		t.Errorf("without a settings e-mail = reply-to %q name %q, want none and still the snapshot's name", again.ReplyTo, again.DisplayName)
	}
	if again.MessageID == out.MessageID {
		t.Errorf("the second send reused Message-ID %q", out.MessageID)
	}
}

// An English buyer is asked to pay the IBAN — with the BIC when the snapshot
// has one, without it when not — and the domestic account only when the
// snapshot has no IBAN (D4); the subject, the dates and the money are
// English, and so is the file's name.
func TestSend_TheEnglishBodyAndTheIBANForm(t *testing.T) {
	t.Parallel()
	h, fake := sendReady(t)
	head := func(number int) string {
		return fmt.Sprintf("Hello,\n\nPlease find attached invoice %d from Kraft-Verket AS for NOK 2,500.00, due 2026-09-26.\n", number)
	}
	const tail = "\n\nKind regards\nKraft-Verket AS\n"
	settings := completeSeller(2)
	for i, c := range []struct {
		iban, bic, payment string
	}{
		{"NO93 8601 1117 947", "dnbanokkxxx", "Please pay to IBAN NO9386011117947 (BIC DNBANOKKXXX), quoting invoice number 1."},
		{"NO93 8601 1117 947", "", "Please pay to IBAN NO9386011117947, quoting invoice number 2."},
		{"", "", "Please pay to account 86011117947, quoting invoice number 3."},
	} {
		if i > 0 {
			settings["iban"], settings["bic"] = c.iban, c.bic
			settings["revision"] = saveSeller(t, h, settings).Revision
		}
		inv := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Consulting", 2, 1000, vat25))).ID)
		sent(t, h, inv.ID, nil)
		out := fake.last(t, i).out
		if want := head(i+1) + c.payment + tail; out.TextBody != want {
			t.Errorf("iban %q bic %q: body =\n%q\nwant\n%q", c.iban, c.bic, out.TextBody, want)
		}
		if want := fmt.Sprintf("Invoice %d from Kraft-Verket AS", i+1); out.Subject != want {
			t.Errorf("subject = %q, want %q", out.Subject, want)
		}
		if want := fmt.Sprintf("invoice-%d.pdf", i+1); len(out.Attachments) != 1 || out.Attachments[0].FileName != want {
			t.Errorf("attachments = %+v, want %s", out.Attachments, want)
		}
	}
}

// The payment paragraph follows what is open at the send (D4): the whole
// amount asked for while nothing is paid, the open part after a partial
// payment, and nothing once it is paid — so a re-send never asks for money
// that is not owed.
func TestSend_ThePaymentParagraphFollowsTheOpenAmount(t *testing.T) {
	t.Parallel()
	h, fake := sendReady(t)
	inv := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 10, 80, vat25))).ID)
	for i, c := range []struct {
		payBefore float64
		paragraph string
	}{
		{0, "Beløpet betales til kontonummer 86011117947. Merk betalingen med fakturanummer 1."},
		{300, "Utestående beløp er NOK 700,00, som betales til kontonummer 86011117947. Merk betalingen med fakturanummer 1."},
		{700, "Fakturaen er gjort opp. Det er ingenting å betale."},
	} {
		if c.payBefore > 0 {
			registered(t, h, inv.ID, pay(c.payBefore, "2026-09-12"))
		}
		sent(t, h, inv.ID, nil)
		body := fake.last(t, i).out.TextBody
		lines := strings.Split(body, "\n")
		if len(lines) < 4 || lines[2] != "Vedlagt følger faktura 1 fra Kraft-Verket AS på NOK 1 000,00, med forfall 12.10.2026." || lines[3] != c.paragraph {
			t.Errorf("after %v paid: body =\n%q\nwant the payment line %q", c.payBefore, body, c.paragraph)
		}
	}
}

// A credit note goes to the same buyer's current address with its own text,
// naming the invoice it credits, and under its own file name (D4).
func TestSend_ACreditNote(t *testing.T) {
	t.Parallel()
	h, fake := sendReady(t)
	inv := issuedAcme(t, h)
	credit := issued(t, h, creditDraft(t, h, inv.ID).ID)
	if credit.Number == nil || *credit.Number != 2 {
		t.Fatalf("the credit note = %+v, want number 2", credit.Number)
	}
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.InvoiceEmail = "ny@acme.example" })
	after := sent(t, h, credit.ID, nil)
	out := fake.last(t, 0).out
	want := "Hei,\n\n" +
		"Vedlagt følger kreditnota 2 fra Kraft-Verket AS på NOK 2 500,00, som krediterer faktura 1.\n\n" +
		"Med vennlig hilsen\nKraft-Verket AS\n"
	if out.Subject != "Kreditnota 2 fra Kraft-Verket AS" || out.TextBody != want {
		t.Errorf("credit note = %q\n%q\nwant Kreditnota 2 fra Kraft-Verket AS\n%q", out.Subject, out.TextBody, want)
	}
	if !slices.Equal(out.To, []string{"ny@acme.example"}) || len(out.Attachments) != 1 || out.Attachments[0].FileName != "kreditnota-2.pdf" ||
		string(out.Attachments[0].Content) != string(storedPDF(t, h, credit.ID)) {
		t.Errorf("to %v, attachments %d; want today's address and kreditnota-2.pdf with the stored bytes", out.To, len(out.Attachments))
	}
	if len(after.Deliveries) != 1 || after.Deliveries[0].Subject != out.Subject {
		t.Errorf("deliveries = %+v, want the one send", after.Deliveries)
	}
}

// A document whose PDF was never stored — the store failed after the issue —
// is stored first, and the row's hash is that of the bytes attached (D4 step
// 5): a send never attaches what the store does not hold.
func TestSend_StoresAnUnstoredPDFFirst(t *testing.T) {
	t.Parallel()
	h, fake := sendReady(t)
	h.objects.failPuts(errors.New("the store is down"))
	inv := issuedAcme(t, h)
	if inv.PdfStored == nil || *inv.PdfStored {
		t.Fatalf("pdfStored = %v, want false after a failed store", inv.PdfStored)
	}
	h.objects.failPuts(nil)

	after := sent(t, h, inv.ID, nil)
	if after.PdfStored == nil || !*after.PdfStored {
		t.Errorf("pdfStored after the send = %v, want true", after.PdfStored)
	}
	attached := fake.last(t, 0).out.Attachments[0].Content
	rowHash := modtest.One[string](t, h.Harness, `SELECT pdf_sha256 FROM invoices.deliveries WHERE invoice_id = $1`, inv.ID)
	docHash := modtest.One[string](t, h.Harness, `SELECT pdf_sha256 FROM invoices.invoices WHERE id = $1`, inv.ID)
	if rowHash != sha(attached) || docHash != rowHash || string(storedPDF(t, h, inv.ID)) != string(attached) {
		t.Errorf("row hash %s, document hash %s, attached %s; want all three the stored object's", rowHash, docHash, sha(attached))
	}
}

// The store answers a send as it answers a download (D4 step 5): an object
// store that cannot be read or written is storage_unavailable, a stored
// object that is gone a 500 — and in every case nothing is sent or logged.
func TestSend_StorageUnavailable(t *testing.T) {
	t.Parallel()
	h, fake := sendReady(t)
	stored := issuedAcme(t, h)
	c := sender(t, h)

	h.objects.failGets(errors.New("the store is down"))
	sendRefused(t, "send while the store cannot be read", sendAs(c, stored.ID, nil), http.StatusServiceUnavailable, "storage_unavailable")
	h.objects.failGets(nil)

	h.objects.failPuts(errors.New("the store is down"))
	unstored := issuedAcme(t, h)
	sendRefused(t, "send while the store cannot be written", sendAs(c, unstored.ID, nil), http.StatusServiceUnavailable, "storage_unavailable")
	h.objects.failPuts(nil)

	h.objects.lose(modtest.One[string](t, h.Harness, `SELECT pdf_object_key FROM invoices.invoices WHERE id = $1`, stored.ID))
	if res := sendAs(c, stored.ID, nil); res.Status != http.StatusInternalServerError {
		t.Errorf("send of a lost object = %d %s, want 500", res.Status, res.Body)
	}
	if len(fake.mails()) != 0 || deliveryRows(t, h, stored.ID)+deliveryRows(t, h, unstored.ID) != 0 {
		t.Errorf("%d mails sent; want nothing sent and nothing logged", len(fake.mails()))
	}
}

// A send that fails is mail_failed, a 502, and records nothing — the ruling
// (D4 step 7). The SMTP error is logged at warn with the document, never put
// on the wire.
func TestSend_AFailedSendRecordsNothing(t *testing.T) {
	t.Parallel()
	h, fake := sendReady(t)
	inv := issuedAcme(t, h)
	fake.fail(errors.New("554 relay access denied for the secret relay"))
	res := sendAs(sender(t, h), inv.ID, nil)
	sendRefused(t, "a failed send", res, http.StatusBadGateway, "mail_failed")
	if strings.Contains(string(res.Body), "relay") {
		t.Errorf("the 502 = %s, want the SMTP error kept off the wire", res.Body)
	}
	if n := deliveryRows(t, h, inv.ID); n != 0 {
		t.Errorf("%d delivery rows after a failed send, want none", n)
	}
	logged := false
	for _, l := range strings.Split(h.Logs(), "\n") {
		logged = logged || (strings.Contains(l, `"level":"WARN"`) && strings.Contains(l, "relay access denied") &&
			strings.Contains(l, fmt.Sprintf(`"invoice_id":%d`, inv.ID)))
	}
	if !logged {
		t.Errorf("logs =\n%s\nwant a warning naming the document and the SMTP error", h.Logs())
	}
}

// A browser that goes away mid-send does not abort it (D4 step 7): the
// request is cancelled while the SMTP send is under way, the send runs on to
// the end on a context the cancellation never reached, and its row is
// written.
func TestSend_ACancelledRequestStillSendsAndLogs(t *testing.T) {
	t.Parallel()
	h, fake := sendReady(t)
	inv := issuedAcme(t, h)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := make(chan error, 1)
	fake.holdWith(func(sendCtx context.Context) {
		cancel()
		r, ok := contracts.RequestFrom(sendCtx)
		if !ok {
			seen <- errors.New("the send's context carries no request")
			return
		}
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
			seen <- errors.New("the server never saw the request cancelled")
			return
		}
		if err := sendCtx.Err(); err != nil {
			seen <- fmt.Errorf("the send's own context ended with the request: %w", err)
			return
		}
		seen <- nil
	})

	c := sender(t, h)
	answered := make(chan *modtest.Response, 1)
	go func() { answered <- c.Do(http.MethodPost, sendPath(inv.ID), map[string]any{}, modtest.Context(ctx)) }()
	select {
	case err := <-seen:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the send was never made")
	}
	if res := <-answered; res.Status != 0 {
		t.Errorf("the cancelled request was answered %d %s, want no answer", res.Status, res.Body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for deliveryRows(t, h, inv.ID) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no delivery row was written after the request was cancelled")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := len(fake.mails()); n != 1 {
		t.Errorf("%d mails sent, want 1", n)
	}
}

// Every send is logged once and shown on the document (D4 steps 8-9): a
// re-send is a second row; each row names the recipient, the subject, the
// time and the sender; the row keeps the Message-ID and the attached bytes'
// hash; a reader sees the log too; a draft has none. The send went through
// the contract-call seam (SMTPSend), and never under a lock.
func TestSend_IsLogged(t *testing.T) {
	t.Parallel()
	h, fake := sendReady(t)
	inv := issuedAcme(t, h)
	if fresh := getInvoice(t, h, inv.ID); fresh.Deliveries == nil || len(fresh.Deliveries) != 0 {
		t.Errorf("deliveries before any send = %v, want an empty list", fresh.Deliveries)
	}
	c, userID := h.SignInUser(t, "invoices:access", "invoices:issue")
	for _, body := range []map[string]any{nil, {"recipient": "kopi@example.org"}} {
		if res := sendAs(c, inv.ID, body); res.Status != http.StatusOK {
			t.Fatalf("send %v = %d %s", body, res.Status, res.Body)
		}
	}
	got := getInvoice(t, h, inv.ID)
	if len(got.Deliveries) != 2 {
		t.Fatalf("deliveries = %+v, want two", got.Deliveries)
	}
	for i, want := range []string{"faktura@acme.example", "kopi@example.org"} {
		d := got.Deliveries[i]
		at, err := time.Parse(time.RFC3339Nano, d.SentAt)
		if d.Recipient != want || d.Subject != "Faktura 1 fra Kraft-Verket AS" || d.SentByUserID != userID.String() || err != nil || !at.Equal(h.Now()) {
			t.Errorf("delivery %d = %+v, want to %s, the subject, by %s at %s", i, d, want, userID, h.Now())
		}
	}
	out := fake.last(t, 1).out
	row := modtest.One[string](t, h.Harness, `SELECT message_id || ' ' || pdf_sha256 FROM invoices.deliveries WHERE id = $1`, got.Deliveries[1].ID)
	if row != out.MessageID+" "+sha(out.Attachments[0].Content) {
		t.Errorf("row = %s, want the Message-ID and the attached bytes' hash", row)
	}
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 1, 100, vat25)))
	if d := getInvoice(t, h, draft.ID); d.Deliveries != nil {
		t.Errorf("a draft's deliveries = %v, want none", d.Deliveries)
	}

	var smtp []contractCall
	for _, call := range contractCalls.by(userID) {
		if call.method == "SMTPSend" {
			smtp = append(smtp, call)
		}
	}
	if len(smtp) != 2 || smtp[0].locked || smtp[1].locked {
		t.Errorf("SMTPSend calls = %+v, want two, neither under a lock", smtp)
	}
}

// sendDefaults (D4): on an issued document read by a caller who may send —
// invoices:issue on an installation that can send — the customer's current
// invoice e-mail, the delivery preference and the send warnings; never for a
// reader, a creator or a payment's response, never on a draft or on an
// installation that cannot send, and left out with a warning in the log when
// the directory fails. The preference warning is delivery_preference_ehf for
// ehf and delivery_preference_other for efaktura and paper; a buyer with a
// Norwegian organisation number is buyer_norwegian_business, before
// 2027-01-01 and after it alike — the red alert from that day is the app's.
func TestSend_SendDefaultsAndWarnings(t *testing.T) {
	t.Parallel()
	h, _ := sendReady(t)
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.InvoiceDelivery = "ehf" })
	inv := issuedAcme(t, h)
	read := func(c *modtest.Client, id int64) invoiceJSON {
		t.Helper()
		res := c.Do(http.MethodGet, invoicePath(id), nil)
		if res.Status != http.StatusOK {
			t.Fatalf("GET %d = %d %s", id, res.Status, res.Body)
		}
		var got invoiceJSON
		res.JSON(&got)
		return got
	}
	// opt is an optional field as the assertion reads it: absent is "", and
	// present but empty — which the contract never answers — is told apart.
	opt := func(s *string) string {
		switch {
		case s == nil:
			return ""
		case *s == "":
			return "<empty>"
		}
		return *s
	}
	is := func(what string, d *sendDefaultsJSON, recipient, preference string, warnings ...string) {
		t.Helper()
		if warnings == nil {
			warnings = []string{}
		}
		switch {
		case d == nil:
			t.Errorf("%s: sendDefaults absent", what)
		case opt(d.Recipient) != recipient || opt(d.Preference) != preference || d.Warnings == nil || !slices.Equal(d.Warnings, warnings):
			t.Errorf("%s: sendDefaults = %q %q %v, want %q %q %v", what, opt(d.Recipient), opt(d.Preference), d.Warnings, recipient, preference, warnings)
		}
	}
	c := sender(t, h)
	is("ehf, a Norwegian business", read(c, inv.ID).SendDefaults, "faktura@acme.example", "ehf",
		"delivery_preference_ehf", "buyer_norwegian_business")

	for _, other := range []*modtest.Client{
		h.SignIn(t, "invoices:access"), h.SignIn(t, "invoices:access", "invoices:create", "invoices:payments"),
	} {
		if d := read(other, inv.ID).SendDefaults; d != nil {
			t.Errorf("a caller without invoices:issue sees sendDefaults %+v", d)
		}
	}
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 1, 100, vat25)))
	if d := read(c, draft.ID).SendDefaults; d != nil {
		t.Errorf("a draft's sendDefaults = %+v, want none", d)
	}
	res := h.SignIn(t, "invoices:access", "invoices:issue", "invoices:payments").Do(http.MethodPost, paymentsPath(inv.ID), pay(100, "2026-09-12"))
	var paid invoiceJSON
	res.JSON(&paid)
	if res.Status != http.StatusOK || paid.SendDefaults != nil {
		t.Errorf("a payment's response = %d, sendDefaults %+v; want 200 and none", res.Status, paid.SendDefaults)
	}
	sendRes := sendAs(c, inv.ID, map[string]any{"recipient": "kopi@example.org"})
	var afterSend invoiceJSON
	sendRes.JSON(&afterSend)
	is("the send's response", afterSend.SendDefaults, "faktura@acme.example", "ehf", "delivery_preference_ehf", "buyer_norwegian_business")

	for _, c2 := range []struct{ preference, warning string }{
		{"efaktura", "delivery_preference_other"}, {"paper", "delivery_preference_other"}, {"email", ""}, {"", ""},
	} {
		h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.InvoiceDelivery = c2.preference })
		want := []string{"buyer_norwegian_business"}
		if c2.warning != "" {
			want = []string{c2.warning, "buyer_norwegian_business"}
		}
		is("preference "+c2.preference, read(c, inv.ID).SendDefaults, "faktura@acme.example", c2.preference, want...)
	}

	person := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Consulting", 1, 100, vat25))).ID)
	is("a private person", read(c, person.ID).SendDefaults, "kari@example.org", "")
	h.customers.edit(customerPerson, func(p *contracts.CustomerBillingProfile) { p.InvoiceEmail = "" })
	is("no invoice e-mail", read(c, person.ID).SendDefaults, "", "")

	h.Advance(time.Date(2027, 1, 2, 9, 0, 0, 0, time.UTC).Sub(h.Now()))
	c = sender(t, h) // the earlier session has expired by then
	is("from 2027", read(c, inv.ID).SendDefaults, "faktura@acme.example", "", "buyer_norwegian_business")

	h.customers.failProfiles(errors.New("the directory is down"))
	if d := read(c, inv.ID).SendDefaults; d != nil {
		t.Errorf("with the directory down, sendDefaults = %+v, want none", d)
	}
	if !strings.Contains(h.Logs(), "the directory is down") {
		t.Errorf("logs =\n%s\nwant a warning naming the directory's error", h.Logs())
	}

	// An installation that cannot send offers nothing to send with.
	quiet := readyToIssue(t)
	other := issuedAcme(t, quiet)
	if d := read(sender(t, quiet), other.ID).SendDefaults; d != nil {
		t.Errorf("on the log driver, sendDefaults = %+v, want none", d)
	}
}

// Sending is under invoices:issue (D1): invoices:access alone, or with
// create and payments, is a 403, and nothing is sent.
func TestSend_NeedsIssue(t *testing.T) {
	t.Parallel()
	h, fake := sendReady(t)
	inv := issuedAcme(t, h)
	for _, perms := range [][]string{{"invoices:access"}, {"invoices:access", "invoices:create", "invoices:payments", "invoices:manage"}} {
		if res := sendAs(h.SignIn(t, perms...), inv.ID, nil); res.Status != http.StatusForbidden {
			t.Errorf("send as %v = %d %s, want 403", perms, res.Status, res.Body)
		}
	}
	if len(fake.mails()) != 0 {
		t.Errorf("%d mails sent, want none", len(fake.mails()))
	}
}

// The send is rate limited per client: 60 in ten minutes, the 61st a 429 in
// the limiter's own body with a Retry-After. The limiter runs before access
// and before the document is looked up, so an id that does not exist counts.
func TestSend_IsRateLimited(t *testing.T) {
	t.Parallel()
	h, _ := sendReady(t)
	c := sender(t, h)
	for i := range 60 {
		if res := sendAs(c, 999999, nil); res.Status != http.StatusNotFound {
			t.Fatalf("send %d = %d %s, want 404", i+1, res.Status, res.Body)
		}
	}
	res := sendAs(c, 999999, nil)
	if res.Status != http.StatusTooManyRequests || res.Header("Retry-After") == "" {
		t.Fatalf("the 61st send = %d (Retry-After %q) %s, want 429 with a Retry-After", res.Status, res.Header("Retry-After"), res.Body)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	res.JSON(&body)
	if body.Error.Code != "rate_limited" {
		t.Errorf("the 429 = %s, want error.code rate_limited", res.Body)
	}
	// Another client is a different budget.
	if res := sendAs(sender(t, h), 999999, nil); res.Status != http.StatusNotFound {
		t.Errorf("another client's send = %d, want 404", res.Status)
	}
}
