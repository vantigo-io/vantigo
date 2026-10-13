package invoices_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/boombuler/barcode/qr"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/health"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/server"
	"github.com/vantigo-io/vantigo/server/internal/web"
)

// payLinkPurpose is the secrets box purpose a link's URL is sealed under.
const payLinkPurpose = "invoices/pay-link"

func payLinkPath(id int64) string       { return fmt.Sprintf("%s/%d/pay-link", invoicesPath, id) }
func payLinkRevokePath(id int64) string { return payLinkPath(id) + "/revoke" }

// payLinkJSON is an invoice's payLink as a client reads it.
type payLinkJSON struct {
	ID        int64   `json:"id"`
	CreatedAt string  `json:"createdAt"`
	URL       *string `json:"url"`
	QrSvg     *string `json:"qrSvg"`
}

// payLinkRow is an invoice's pay links as stored, newest first.
type payLinkRow struct {
	id                 int64
	tokenSHA, sealed   string
	createdBy, revoked *string
}

func payLinkRows(t *testing.T, h *harness, invoiceID int64) []payLinkRow {
	t.Helper()
	rows, err := h.Pool().Query(context.Background(), `
		SELECT id, token_sha256, url_ciphertext, created_by_user_id::text, revoked_at::text
		FROM invoices.pay_links WHERE invoice_id = $1 ORDER BY id DESC`, invoiceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []payLinkRow
	for rows.Next() {
		var r payLinkRow
		if err := rows.Scan(&r.id, &r.tokenSHA, &r.sealed, &r.createdBy, &r.revoked); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// liveLinkURL is an invoice's live link opened as the module opens it, ""
// without one.
func liveLinkURL(t *testing.T, h *harness, invoiceID int64) string {
	t.Helper()
	for _, r := range payLinkRows(t, h, invoiceID) {
		if r.revoked == nil {
			url, err := h.Deps().Secrets.OpenString(payLinkPurpose, r.sealed)
			if err != nil {
				t.Fatalf("open pay link %d: %v", r.id, err)
			}
			return url
		}
	}
	return ""
}

// payReady is an installation offering pay links and on-site payment: a
// complete seller, PUBLIC_BASE_URL, vippstest's credentials and both
// switches on, e-mail through a fake SMTP, Acme with an invoice e-mail.
func payReady(t *testing.T, opts ...modtest.Option) (*harness, *fakeSMTP) {
	t.Helper()
	fake := &fakeSMTP{}
	h := newHarness(t, append([]modtest.Option{modtest.WithEnv("PUBLIC_BASE_URL", publicBaseURL),
		modtest.WithSMTPSend(fake.send), modtest.WithEnv("MAIL_DRIVER", "smtp"),
		modtest.WithEnv("SMTP_HOST", "smtp.example.invalid"), modtest.WithEnv("SMTP_FROM", "faktura@example.invalid")}, opts...)...)
	saveSeller(t, h, completeSeller(1))
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.InvoiceEmail = "faktura@acme.example" })
	putVipps(t, h, vippstestKeys())
	enablePayments(t, h)
	return h, fake
}

// onSiteOff turns on-site payment off, pay links left on.
func onSiteOff(t *testing.T, h *harness) {
	t.Helper()
	putPaymentSettings(t, h, paymentSettingsBody(getPaymentSettings(t, h).Revision, map[string]any{
		"payLinksEnabled": true, "termsUrl": termsURL, "privacyUrl": privacyURL,
	}))
}

// payLinksOff turns pay links off, on-site payment left on.
func payLinksOff(t *testing.T, h *harness) {
	t.Helper()
	putPaymentSettings(t, h, paymentSettingsBody(getPaymentSettings(t, h).Revision, map[string]any{
		"termsUrl": termsURL, "privacyUrl": privacyURL, "onSitePaymentsEnabled": true,
	}))
}

// fixedTokens installs the token seam: token n is "test-token-<n>" padded to
// 43 characters of the base64url alphabet. It answers the URL of token n.
func fixedTokens(t *testing.T) func(n int) string {
	t.Helper()
	var mu sync.Mutex
	n := 0
	token := func(i int) string { return fmt.Sprintf("test-token-%02d_%s", i, strings.Repeat("A", 29)) }
	t.Cleanup(invoices.SetPayLinkToken(func() string {
		mu.Lock()
		defer mu.Unlock()
		n++
		return token(n)
	}))
	return func(i int) string { return publicBaseURL + "/pay?token=" + token(i) }
}

// pdfPayLinks collects every PDF model's pay link, by document.
func pdfPayLinks(t *testing.T) func(id int64) ([2]string, bool) {
	t.Helper()
	var mu sync.Mutex
	seen := map[int64][2]string{}
	t.Cleanup(invoices.SetPDFModelBuilt(func(id int64, m invoices.PDFModel) {
		mu.Lock()
		defer mu.Unlock()
		seen[id] = m.PayLink
	}))
	return func(id int64) ([2]string, bool) {
		mu.Lock()
		defer mu.Unlock()
		v, ok := seen[id]
		return v, ok
	}
}

// TestPayLink_AtIssue: an issue makes the link in its own transaction when
// pay links are available — created by nobody, the PDF printing "Betal med
// Vipps", the URL and a QR that reads back as the URL — and none without any
// one of what availability needs, nor for a credit note. Not parallel: the
// token and model seams are the package's.
func TestPayLink_AtIssue(t *testing.T) {
	url := fixedTokens(t)
	models := pdfPayLinks(t)
	h, _ := payReady(t)

	inv := issuedAcme(t, h)
	rows := payLinkRows(t, h, inv.ID)
	if len(rows) != 1 || rows[0].createdBy != nil || rows[0].revoked != nil {
		t.Fatalf("the issue's links = %+v, want one live link made by nobody", rows)
	}
	if got := liveLinkURL(t, h, inv.ID); got != url(1) {
		t.Errorf("the link's URL = %q, want %q", got, url(1))
	}
	if got, _ := models(inv.ID); got != [2]string{"Betal med Vipps", url(1)} {
		t.Errorf("the PDF's pay link = %q, want Betal med Vipps and %q", got, url(1))
	}
	if codes, err := invoices.PDFPayLinkQR(url(1)); err != nil || !slices.Equal(codes, []string{url(1)}) {
		t.Errorf("the PDF's QR codes = %q (%v), want one reading %q", codes, err, url(1))
	}
	if inv.PayLink == nil || inv.PayLink.ID != rows[0].id {
		t.Errorf("the issue's answer carries payLink %+v, want link %d", inv.PayLink, rows[0].id)
	}

	// A credit note is never paid: no link.
	credit := issued(t, h, creditDraft(t, h, inv.ID).ID)
	if rows := payLinkRows(t, h, credit.ID); len(rows) != 0 {
		t.Errorf("a credit note's links = %+v, want none", rows)
	}

	// Each condition of availability missing in turn: no link, no line.
	noLink := func(what string, h *harness) {
		t.Helper()
		inv := issuedAcme(t, h)
		if rows := payLinkRows(t, h, inv.ID); len(rows) != 0 {
			t.Errorf("%s: the issue made %d links, want none", what, len(rows))
		}
		if got, ok := models(inv.ID); !ok || got != ([2]string{}) {
			t.Errorf("%s: the PDF's pay link = %q (laid out %v), want none", what, got, ok)
		}
		if inv.PayLink != nil {
			t.Errorf("%s: payLink = %+v, want absent", what, inv.PayLink)
		}
	}
	payLinksOff(t, h)
	noLink("pay links switched off", h)
	putPaymentSettings(t, h, paymentSettingsBody(getPaymentSettings(t, h).Revision, everythingOn()))
	h.Exec(t, `UPDATE invoices.payment_provider_credentials SET rejected_at = now()`)
	noLink("credentials refused", h)
	h.Exec(t, `UPDATE invoices.payment_provider_credentials SET rejected_at = NULL`)
	if res := vippsManager(t, h).Do(http.MethodDelete, vippsSettingsPath, nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", res.Status, res.Body)
	}
	noLink("no credentials", h)

	noPublic := readyToIssue(t)
	putVipps(t, noPublic, vippstestKeys())
	noPublic.Exec(t, `UPDATE invoices.payment_settings SET pay_links_enabled = true, terms_url = $1, privacy_url = $2,
		on_site_payments_enabled = true, kontantsalg_acknowledged_at = now(), kontantsalg_acknowledged_by_user_id = gen_random_uuid()`,
		termsURL, privacyURL)
	noLink("no PUBLIC_BASE_URL", noPublic)

	switchedOff, _ := payReady(t, modtest.WithEnv("INVOICES_VIPPS_ENABLED", "0"))
	noLink("INVOICES_VIPPS_ENABLED=0", switchedOff)
}

// TestPayLink_OnDemandAndRefusals: POST /pay-link makes a link for an issued
// invoice that has none, by the caller, and refuses, in order, a credit note,
// a draft, an anonymised customer, pay links unavailable and a live link;
// an unknown id is 404 and invoices:issue is needed.
func TestPayLink_OnDemandAndRefusals(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	c, user := h.SignInUser(t, "invoices:access", "invoices:issue")

	inv := issuedAcme(t, h)
	conflictAs(t, "a second link", c.Do(http.MethodPost, payLinkPath(inv.ID), nil), "pay_link_exists")
	if res := c.Do(http.MethodPost, payLinkRevokePath(inv.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", res.Status, res.Body)
	}
	res := c.Do(http.MethodPost, payLinkPath(inv.ID), nil)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST pay-link = %d %s, want 201", res.Status, res.Body)
	}
	var got invoiceJSON
	res.JSON(&got)
	rows := payLinkRows(t, h, inv.ID)
	if len(rows) != 2 || rows[0].revoked != nil || rows[0].createdBy == nil || *rows[0].createdBy != user.String() {
		t.Fatalf("the links = %+v, want a new live one made by %s beside the revoked one", rows, user)
	}
	if got.PayLink == nil || got.PayLink.ID != rows[0].id || got.PayLink.URL == nil || *got.PayLink.URL != liveLinkURL(t, h, inv.ID) {
		t.Errorf("the answer's payLink = %+v, want link %d with its URL", got.PayLink, rows[0].id)
	}

	credit := issued(t, h, creditDraft(t, h, inv.ID).ID)
	conflictAs(t, "a credit note", c.Do(http.MethodPost, payLinkPath(credit.ID), nil), "credit_note_no_payments")
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 1, 500, vat25)))
	conflictAs(t, "a draft", c.Do(http.MethodPost, payLinkPath(draft.ID), nil), "invoice_draft")

	person := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Time", 1, 500, vat25))).ID)
	if res := c.Do(http.MethodPost, payLinkRevokePath(person.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", res.Status, res.Body)
	}
	h.Exec(t, `INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now())`, customerPerson)
	conflictAs(t, "an anonymised customer", c.Do(http.MethodPost, payLinkPath(person.ID), nil), "customer_anonymised")

	other := issuedAcme(t, h)
	if res := c.Do(http.MethodPost, payLinkRevokePath(other.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", res.Status, res.Body)
	}
	payLinksOff(t, h)
	conflictAs(t, "pay links off", c.Do(http.MethodPost, payLinkPath(other.ID), nil), "pay_links_unavailable")
	if n := len(payLinkRows(t, h, other.ID)); n != 1 {
		t.Errorf("after the refusals the invoice has %d links, want its one revoked", n)
	}

	if res := c.Do(http.MethodPost, payLinkPath(999999), nil); res.Status != http.StatusNotFound {
		t.Errorf("an unknown id = %d, want 404 (the lock finds no document)", res.Status)
	}
	reader := h.SignIn(t, "invoices:access", "invoices:payments", "invoices:create")
	for _, path := range []string{payLinkPath(other.ID), payLinkRevokePath(inv.ID)} {
		if res := reader.Do(http.MethodPost, path, nil); res.Status != http.StatusForbidden {
			t.Errorf("POST %s without invoices:issue = %d, want 403", path, res.Status)
		}
	}
}

// TestPayLink_Revoke: the live link is revoked once, by the caller, and the
// invoice answers no payLink after; a second revoke, an invoice without a
// live link and an unknown id are 404.
func TestPayLink_Revoke(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	c, user := h.SignInUser(t, "invoices:access", "invoices:issue")
	inv := issuedAcme(t, h)
	if res := c.Do(http.MethodPost, payLinkRevokePath(inv.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("revoke = %d %s, want 204", res.Status, res.Body)
	}
	by := modtest.One[string](t, h.Harness, `SELECT revoked_by_user_id::text FROM invoices.pay_links WHERE invoice_id = $1`, inv.ID)
	if by != user.String() {
		t.Errorf("revoked by %s, want %s", by, user)
	}
	if got := readAs(t, c, inv.ID); got.PayLink != nil {
		t.Errorf("payLink after the revoke = %+v, want absent", got.PayLink)
	}
	if res := c.Do(http.MethodPost, payLinkRevokePath(inv.ID), nil); res.Status != http.StatusNotFound {
		t.Errorf("a second revoke = %d, want 404", res.Status)
	}
	if res := c.Do(http.MethodPost, payLinkRevokePath(999999), nil); res.Status != http.StatusNotFound {
		t.Errorf("revoke of an unknown id = %d, want 404", res.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.pay_links WHERE invoice_id = $1 AND revoked_at IS NOT NULL`, inv.ID); n != 1 {
		t.Errorf("%d revoked links, want 1", n)
	}
}

// TestPayLink_LockOrder: a link made on demand locks the invoice alone, and
// a revoke the invoice and then the link (D18's 4C rows). Not parallel: the
// lock seam is the package's.
func TestPayLink_LockOrder(t *testing.T) {
	h, _ := payReady(t)
	c := issuer(t, h)
	inv := issuedAcme(t, h)
	link := payLinkRows(t, h, inv.ID)[0].id
	seen := &lockSeen{}
	defer invoices.SetLockTaken(seen.note)()

	if res := c.Do(http.MethodPost, payLinkRevokePath(inv.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", res.Status, res.Body)
	}
	id := strconv.FormatInt(inv.ID, 10)
	if got, want := seen.take(), []string{"invoice " + id, "pay_link " + strconv.FormatInt(link, 10)}; !slices.Equal(got, want) {
		t.Errorf("the revoke's locks = %v, want %v", got, want)
	}
	if res := c.Do(http.MethodPost, payLinkPath(inv.ID), nil); res.Status != http.StatusCreated {
		t.Fatalf("make = %d %s", res.Status, res.Body)
	}
	if got, want := seen.take(), []string{"invoice " + id}; !slices.Equal(got, want) {
		t.Errorf("the make's locks = %v, want %v", got, want)
	}
}

// TestPayLink_TokenStoredHashedOnly: the row holds the token's SHA-256 and
// the sealed URL, never the token, and the URL opened is PUBLIC_BASE_URL's
// /pay with the token in its query. Not parallel: the token seam is the
// package's.
func TestPayLink_TokenStoredHashedOnly(t *testing.T) {
	url := fixedTokens(t)
	h, _ := payReady(t)
	inv := issuedAcme(t, h)
	token := strings.TrimPrefix(url(1), publicBaseURL+"/pay?token=")
	whole := modtest.One[string](t, h.Harness, `SELECT row_to_json(l)::text FROM invoices.pay_links l WHERE invoice_id = $1`, inv.ID)
	if strings.Contains(whole, token) {
		t.Errorf("the row %s holds the token", whole)
	}
	sum := sha256.Sum256([]byte(token))
	if got := payLinkRows(t, h, inv.ID)[0].tokenSHA; got != hex.EncodeToString(sum[:]) {
		t.Errorf("token_sha256 = %s, want the token's SHA-256", got)
	}
	if got := liveLinkURL(t, h, inv.ID); got != url(1) {
		t.Errorf("the URL opened = %q, want %q", got, url(1))
	}
}

// A token drawn for real is 43 characters of the base64url alphabet — 32
// bytes — so the public host never meets an escape.
func TestPayLink_TokenIsCanonical(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	inv := issuedAcme(t, h)
	if got := liveLinkURL(t, h, inv.ID); !regexp.MustCompile(`^` + regexp.QuoteMeta(publicBaseURL) + `/pay\?token=[A-Za-z0-9_-]{43}$`).MatchString(got) {
		t.Errorf("the URL = %q, want PUBLIC_BASE_URL/pay?token= and 43 base64url characters", got)
	}
}

// TestPayLink_UrlOnlyForIssuers (I5): every reader sees that a link exists;
// its URL and QR only an invoices:issue holder while on-site payment is
// available.
func TestPayLink_UrlOnlyForIssuers(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	inv := issuedAcme(t, h)
	url := liveLinkURL(t, h, inv.ID)

	reader := h.SignIn(t, "invoices:access", "invoices:payments", "invoices:manage", "invoices:create")
	got := readAs(t, reader, inv.ID)
	if got.PayLink == nil || got.PayLink.URL != nil || got.PayLink.QrSvg != nil {
		t.Errorf("without invoices:issue payLink = %+v, want its id and no url or qrSvg", got.PayLink)
	}
	got = readAs(t, issuer(t, h), inv.ID)
	if got.PayLink == nil || got.PayLink.URL == nil || *got.PayLink.URL != url || got.PayLink.QrSvg == nil ||
		!strings.HasPrefix(*got.PayLink.QrSvg, "<svg") {
		t.Errorf("with invoices:issue and on-site payment payLink = %+v, want url %q and its QR", got.PayLink, url)
	}
	onSiteOff(t, h)
	got = readAs(t, issuer(t, h), inv.ID)
	if got.PayLink == nil || got.PayLink.URL != nil || got.PayLink.QrSvg != nil {
		t.Errorf("with invoices:issue and on-site payment off payLink = %+v, want no url or qrSvg", got.PayLink)
	}
}

// TestQRSVG_Decodes: the qrSvg answered is the URL's QR code exactly, one
// square per dark module of the code the URL encodes to.
func TestQRSVG_Decodes(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	inv := readAs(t, issuer(t, h), issuedAcme(t, h).ID)
	if inv.PayLink == nil || inv.PayLink.QrSvg == nil {
		t.Fatalf("payLink = %+v, want a QR", inv.PayLink)
	}
	url := liveLinkURL(t, h, inv.ID)
	code, err := qr.Encode(url, qr.M, qr.Auto)
	if err != nil {
		t.Fatal(err)
	}
	b := code.Bounds()
	want := map[string]bool{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if r, g, bl, _ := code.At(x, y).RGBA(); r+g+bl < 3*0x8000 {
				want[fmt.Sprintf("%d %d", x-b.Min.X+4, y-b.Min.Y+4)] = true
			}
		}
	}
	got := map[string]bool{}
	for _, m := range regexp.MustCompile(`M(\d+) (\d+)h1v1h-1z`).FindAllStringSubmatch(*inv.PayLink.QrSvg, -1) {
		got[m[1]+" "+m[2]] = true
	}
	if len(want) == 0 || len(got) != len(want) {
		t.Fatalf("%d dark squares, want the URL's %d dark modules", len(got), len(want))
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("module %s is missing: the QR does not read as %q", k, url)
		}
	}
}

// TestQuickInvoice_NoLinkWithoutOnSite (I5): handed over on site, a quick
// invoice gets no pay link unless on-site payment is available; e-mailed
// later, the send makes one and the cover text carries it. With on-site
// payment it gets one at once.
func TestQuickInvoice_NoLinkWithoutOnSite(t *testing.T) {
	t.Parallel()
	h, mails := payReady(t)
	onSiteOff(t, h)
	quick := quickInvoiced(t, h, quickBody(customerAcme, line("Service", 1, 900, vat25))).Invoice
	if rows := payLinkRows(t, h, quick.ID); len(rows) != 0 || quick.PayLink != nil {
		t.Fatalf("a quick invoice without on-site payment has links %+v, payLink %+v; want none", rows, quick.PayLink)
	}
	sent(t, h, quick.ID, nil)
	url := liveLinkURL(t, h, quick.ID)
	if url == "" {
		t.Fatal("the send made no link")
	}
	if body := mails.last(t, 0).out.TextBody; !strings.Contains(body, "Du kan også betale med Vipps: "+url) {
		t.Errorf("the cover text =\n%s\nwant the pay line with %s", body, url)
	}

	putPaymentSettings(t, h, paymentSettingsBody(getPaymentSettings(t, h).Revision, everythingOn()))
	onSite := quickInvoiced(t, h, quickBody(customerAcme, line("Service", 1, 900, vat25))).Invoice
	if rows := payLinkRows(t, h, onSite.ID); len(rows) != 1 || rows[0].revoked != nil {
		t.Errorf("a quick invoice with on-site payment has links %+v, want one live", rows)
	}
}

// TestSend_MakesALinkOnDemand: an invoice issued before pay links were
// available is e-mailed with a link the send made, by the sender; a second
// send carries the same link and makes none; without availability the send
// makes none and the text has no pay line.
func TestSend_MakesALinkOnDemand(t *testing.T) {
	t.Parallel()
	h, mails := payReady(t)
	payLinksOff(t, h)
	inv := issuedAcme(t, h)
	sent(t, h, inv.ID, nil)
	if rows := payLinkRows(t, h, inv.ID); len(rows) != 0 || strings.Contains(mails.last(t, 0).out.TextBody, "Vipps") {
		t.Fatalf("a send without pay links made %+v and wrote a pay line; want neither", rows)
	}

	putPaymentSettings(t, h, paymentSettingsBody(getPaymentSettings(t, h).Revision, everythingOn()))
	c, user := h.SignInUser(t, "invoices:access", "invoices:issue")
	if res := sendAs(c, inv.ID, nil); res.Status != http.StatusOK {
		t.Fatalf("send = %d %s", res.Status, res.Body)
	}
	rows := payLinkRows(t, h, inv.ID)
	if len(rows) != 1 || rows[0].createdBy == nil || *rows[0].createdBy != user.String() {
		t.Fatalf("the send's links = %+v, want one made by the sender %s", rows, user)
	}
	url := liveLinkURL(t, h, inv.ID)
	if body := mails.last(t, 1).out.TextBody; !strings.Contains(body, "Du kan også betale med Vipps: "+url+"\n") {
		t.Errorf("the cover text =\n%s\nwant the pay line with %s", body, url)
	}
	sent(t, h, inv.ID, nil)
	if n := len(payLinkRows(t, h, inv.ID)); n != 1 || !strings.Contains(mails.last(t, 2).out.TextBody, url) {
		t.Errorf("a second send left %d links and its text lacks the link; want the one link carried again", n)
	}
}

// TestPDF_NoLinkWhenNoneLive (plan reading 13): the PDF prints the link live
// when it is first stored — a link revoked before that store is not printed.
// Not parallel: the model seam is the package's.
func TestPDF_NoLinkWhenNoneLive(t *testing.T) {
	models := pdfPayLinks(t)
	h, _ := payReady(t)
	h.objects.failPuts(fmt.Errorf("the store is down"))
	inv := issuedAcme(t, h)
	if inv.PdfStored == nil || *inv.PdfStored {
		t.Fatalf("pdfStored = %v, want false while the store is down", inv.PdfStored)
	}
	if got, _ := models(inv.ID); got[1] == "" {
		t.Fatalf("the failed store's model printed no link: %q", got)
	}
	if res := issuer(t, h).Do(http.MethodPost, payLinkRevokePath(inv.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", res.Status, res.Body)
	}
	h.objects.failPuts(nil)
	if res := download(t, h, inv.ID); res.Status != http.StatusOK {
		t.Fatalf("download = %d %s", res.Status, res.Body)
	}
	if got, ok := models(inv.ID); !ok || got != ([2]string{}) {
		t.Errorf("the PDF stored after the revoke prints %q, want no pay link", got)
	}

	// Nor is a live link printed for a customer anonymised meanwhile.
	h.objects.failPuts(fmt.Errorf("the store is down"))
	person := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Time", 1, 500, vat25))).ID)
	if len(payLinkRows(t, h, person.ID)) != 1 {
		t.Fatal("the issue made no link")
	}
	h.Exec(t, `INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now())`, customerPerson)
	h.objects.failPuts(nil)
	if res := download(t, h, person.ID); res.Status != http.StatusOK {
		t.Fatalf("download = %d %s", res.Status, res.Body)
	}
	if got, ok := models(person.ID); !ok || got != ([2]string{}) {
		t.Errorf("the PDF of an anonymised customer's invoice prints %q, want no pay link", got)
	}
}

// TestPayLink_NoCallUnderALock: making a link at issue, on demand and on a
// send, and revoking one, make no call out of the module under a lock — the
// secrets box is in-process, the object store is reached after the commit.
func TestPayLink_NoCallUnderALock(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	c, user := h.SignInUser(t, "invoices:access", "invoices:create", "invoices:issue")
	inv := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 2, 1000, vat25)))
	for _, req := range []struct{ method, path string }{
		{http.MethodPost, issuePath(inv.ID)},
		{http.MethodPost, payLinkRevokePath(inv.ID)},
		{http.MethodPost, payLinkPath(inv.ID)},
		{http.MethodPost, payLinkRevokePath(inv.ID)},
		{http.MethodPost, sendPath(inv.ID)},
	} {
		var body any
		if !strings.Contains(req.path, "pay-link") {
			body = map[string]any{}
		}
		if res := c.Do(req.method, req.path, body); res.Status >= 300 {
			t.Fatalf("%s %s = %d %s", req.method, req.path, res.Status, res.Body)
		}
	}
	if liveLinkURL(t, h, inv.ID) == "" {
		t.Fatal("the send made no link")
	}
	for _, call := range contractCalls.by(user) {
		if call.locked {
			t.Errorf("%s was called under a lock", call.method)
		}
	}
}

// TestPayLink_NeverInTheRequestLog: a pay link's URL, requested through
// server.New, is logged by its path alone — never the token in its query. A
// guard of the platform's request log (srv/httpx), kept here beside the token
// it protects; Task 7 retargets its API request to POST
// /api/v1/invoices/pay/read, where the token travels in the body.
func TestPayLink_NeverInTheRequestLog(t *testing.T) {
	t.Parallel()
	const token = "secret-token_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456"
	assets := fstest.MapFS{"index.html": {Data: []byte(`<!doctype html><html><head><title>Vantigo</title></head><body><div id="root"></div></body></html>`)}}
	cfg := &config.Config{Env: config.Production, AppOrigin: "https://vantigo.example.com", AppHostname: "vantigo.example.com",
		Port: 8080, ShutdownTimeout: time.Second, LogLevel: slog.LevelInfo}
	index, err := web.NewIndex(assets, cfg.BasePath, cfg.Branding, cfg.Modules)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := server.New(server.Options{Config: cfg, Logger: logger, Index: index, Assets: assets,
		Health: health.Handler(logger, "test"), API: http.NotFoundHandler()})
	for _, path := range []string{"/pay?token=" + token, "/api/v1/invoices/pay?token=" + token} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "https://vantigo.example.com"+path, nil))
	}
	text := logs.String()
	if strings.Contains(text, token) || strings.Contains(text, "token=") {
		t.Errorf("the request log holds the token:\n%s", text)
	}
	for _, want := range []string{`"path":"/pay"`, `"path":"/api/v1/invoices/pay"`} {
		if !strings.Contains(text, want) {
			t.Errorf("the request log lacks %s:\n%s", want, text)
		}
	}
}

// TestReminderLetter_PayLine (D10, D15, open question 2): a letter of an
// invoice with a live link prints it, in the letter's language, and — since
// the link pays the principal only — that the fees and interest it claims
// are paid by bank, quoting the KID or, without one, the invoice number. The
// link is one of the letter's facts: revoked after the letter was sent, the
// letter renders again byte for byte as stored, link and all; revoked before
// a letter's facts are written, that letter prints none. Not parallel: the
// token and letter seams are the package's.
func TestReminderLetter_PayLine(t *testing.T) {
	fixedTokens(t)
	letters := collectLetters(t)
	for _, c := range []struct {
		file     string
		invoice  letterInvoice
		language string
		has      []string
	}{
		{"paylink-nb.txt", letterInvoice{number: 103, customer: customerAcme, due: "2026-08-03", kid: "kid"}, "nb",
			[]string{"Du kan betale fakturaen med Vipps: " + publicBaseURL + "/pay?token=",
				"Lenken betaler bare fakturaens utestående hovedstol. Gebyrer, kompensasjon og renter betales til kontonummeret over, merket med KID "}},
		{"paylink-en.txt", letterInvoice{number: 105, customer: customerAcme, due: "2026-08-03", language: "en"}, "en",
			[]string{"You can pay the invoice with Vipps: " + publicBaseURL + "/pay?token=",
				"The link pays the invoice's outstanding principal only. Please pay fees, compensation and interest to the account above, quoting invoice number 105."}},
	} {
		t.Run(c.file, func(t *testing.T) {
			h, _ := workerHarness(t, "late_interest = true", modtest.WithEnv("PUBLIC_BASE_URL", publicBaseURL))
			putVipps(t, h, vippstestKeys())
			enablePayments(t, h)
			spec := c.invoice
			if spec.kid != "" {
				spec.kid = kidOf(t, spec.number)
			}
			id := plantLetterInvoice(t, h, spec)
			if res := issuer(t, h).Do(http.MethodPost, payLinkPath(id), nil); res.Status != http.StatusCreated {
				t.Fatalf("POST pay-link = %d %s", res.Status, res.Body)
			}
			url := liveLinkURL(t, h, id)
			letter := queueLetter(t, h, id, 1, "reminder", false, c.language)
			dispatch(t, invoices.NewReminderWorker(h.Deps()))
			row := letterOf(t, h, letter)
			if row.Status != "sent" {
				t.Fatalf("the letter = %s (%s), want sent", row.Status, deref(row.WithdrawalReason))
			}
			text := letters.of(letter)
			for _, s := range append(c.has, url) {
				if !strings.Contains(text, s) {
					t.Errorf("the letter lacks %q", s)
				}
			}
			golden(t, c.file, text)

			if res := issuer(t, h).Do(http.MethodPost, payLinkRevokePath(id), nil); res.Status != http.StatusNoContent {
				t.Fatalf("revoke = %d %s", res.Status, res.Body)
			}
			again, err := invoices.RenderLetterForTest(context.Background(), h.Deps(), letter)
			if err != nil {
				t.Fatal(err)
			}
			if string(again) != string(h.objects.object(deref(row.PdfObjectKey))) || !strings.Contains(letters.of(letter), url) {
				t.Error("the letter rendered after the revoke is not its stored PDF with the link it was sent with")
			}
		})
	}

	// A link revoked before the facts are written is not the letter's.
	h, _ := workerHarness(t, "late_interest = true", modtest.WithEnv("PUBLIC_BASE_URL", publicBaseURL))
	putVipps(t, h, vippstestKeys())
	enablePayments(t, h)
	id := plantLetterInvoice(t, h, letterInvoice{number: 106, customer: customerAcme, due: "2026-08-03"})
	for _, path := range []string{payLinkPath(id), payLinkRevokePath(id)} {
		if res := issuer(t, h).Do(http.MethodPost, path, nil); res.Status >= 300 {
			t.Fatalf("POST %s = %d %s", path, res.Status, res.Body)
		}
	}
	letter := queueLetter(t, h, id, 1, "reminder", false, "nb")
	dispatch(t, invoices.NewReminderWorker(h.Deps()))
	if row := letterOf(t, h, letter); row.Status != "sent" || row.PayLinkID != nil || strings.Contains(letters.of(letter), "Vipps") {
		t.Errorf("a letter written after the revoke = %s with link %v:\n%s\nwant sent with none", row.Status, row.PayLinkID, letters.of(letter))
	}
}

// A letter that claims no charges prints the link and no sentence about
// charges: there is nothing for the bank to be paid besides.
func TestReminderLetter_PayLineWithoutCharges(t *testing.T) {
	letters := collectLetters(t)
	h, _ := workerHarness(t, "person_charge = 'none', business_charge = 'none'", modtest.WithEnv("PUBLIC_BASE_URL", publicBaseURL))
	putVipps(t, h, vippstestKeys())
	enablePayments(t, h)
	id := plantLetterInvoice(t, h, letterInvoice{number: 101, customer: customerAcme, due: "2026-08-03", kid: kidOf(t, 101)})
	if res := issuer(t, h).Do(http.MethodPost, payLinkPath(id), nil); res.Status != http.StatusCreated {
		t.Fatalf("POST pay-link = %d %s", res.Status, res.Body)
	}
	letter := queueLetter(t, h, id, 1, "reminder", false, "nb")
	dispatch(t, invoices.NewReminderWorker(h.Deps()))
	text := letters.of(letter)
	if !strings.Contains(text, "Du kan betale fakturaen med Vipps: "+liveLinkURL(t, h, id)) || strings.Contains(text, "Lenken betaler") {
		t.Errorf("a letter without charges =\n%s\nwant the pay line and no charges sentence", text)
	}
}

// Every token drawn is 43 characters of the base64url alphabet: 32 bytes,
// nothing the public host would have to unescape.
func TestPayLink_TokenAlphabet(t *testing.T) {
	t.Parallel()
	canonical := regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	seen := map[string]bool{}
	for range 1000 {
		token, err := invoices.NewPayLinkToken()
		if err != nil {
			t.Fatal(err)
		}
		if !canonical.MatchString(token) || seen[token] {
			t.Fatalf("token %q: want 43 base64url characters, never drawn twice", token)
		}
		seen[token] = true
	}
}

// TestQuickInvoice_EraseInTheWindowSkipsTheLink: an erase whose marker
// commits while a quick invoice is being issued — its draft invisible to the
// erase — makes the link's trigger refuse; the insert's savepoint is rolled
// back and the invoice is issued and handed over without a link. Not
// parallel: the hook is the package's.
func TestQuickInvoice_EraseInTheWindowSkipsTheLink(t *testing.T) {
	h, _ := payReady(t)
	defer invoices.SetPayLinkAtIssueBeforeInsert(func(context.Context, int64) {
		h.Exec(t, `INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now()) ON CONFLICT DO NOTHING`, customerAcme)
	})()
	quick := quickInvoiced(t, h, quickBody(customerAcme, line("Service", 1, 900, vat25))).Invoice
	if quick.Status != "issued" || quick.PayLink != nil {
		t.Fatalf("the quick invoice = %s with payLink %+v, want issued without one", quick.Status, quick.PayLink)
	}
	if rows := payLinkRows(t, h, quick.ID); len(rows) != 0 {
		t.Errorf("links = %+v, want none", rows)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.manual_deliveries WHERE invoice_id = $1`, quick.ID); n != 1 {
		t.Errorf("%d manual deliveries, want the hand-over", n)
	}
}

// A failed attempt clears a letter's facts, its pay link with them: the
// next attempt judges the link again.
func TestReminderLetter_PayLinkClearedWithTheFacts(t *testing.T) {
	t.Parallel()
	h, mails := workerHarness(t, "late_interest = true", modtest.WithEnv("PUBLIC_BASE_URL", publicBaseURL))
	putVipps(t, h, vippstestKeys())
	enablePayments(t, h)
	id := plantLetterInvoice(t, h, letterInvoice{number: 107, customer: customerAcme, due: "2026-08-03"})
	if res := issuer(t, h).Do(http.MethodPost, payLinkPath(id), nil); res.Status != http.StatusCreated {
		t.Fatalf("POST pay-link = %d %s", res.Status, res.Body)
	}
	letter := queueLetter(t, h, id, 1, "reminder", false, "nb")
	mails.failWith(fmt.Errorf("smtp: 421 try later"))
	dispatch(t, invoices.NewReminderWorker(h.Deps()))
	if row := letterOf(t, h, letter); row.Status != "queued" || row.SentOn.Valid || row.PayLinkID != nil {
		t.Errorf("after a failed attempt the letter = %s, sent_on %v, link %v; want queued with neither", row.Status, row.SentOn.Valid, row.PayLinkID)
	}
}
