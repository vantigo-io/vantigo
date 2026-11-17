package invoices_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/gif"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file tests invoices phase 6 delivery A against Postgres: the logo's
// three operations (I1), its snapshot and its print (I2, I3), the phone
// (I4), the HTML mail with the logo inline (I5) and the personal message
// (I6), with the two triggers that guard it.

const logoPath = settingsPath + "/logo"

// pngLogo is a w × h PNG.
func pngLogo(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// putLogo uploads data as the logo's file part as c.
func putLogo(t *testing.T, c *modtest.Client, data ...[]byte) *modtest.Response {
	t.Helper()
	contentType, body := fileBody(t, "file", data...)
	return c.Do(http.MethodPut, logoPath, nil, modtest.RawBody(contentType, body))
}

// withLogo sets data as the logo and answers the settings.
func withLogo(t *testing.T, h *harness, data []byte) settingsJSON {
	t.Helper()
	res := putLogo(t, manager(t, h), data)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT /settings/logo = %d %s, want 200", res.Status, res.Body)
	}
	var s settingsJSON
	res.JSON(&s)
	return s
}

// The logo is set, served and removed (I1): stored under its content's key,
// named on the settings, served with its hash as the ETag, and removed by
// clearing the settings alone — the object stays, since a document may name
// it.
func TestLogo_SetServedAndRemoved(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	logo := pngLogo(t, 240, 80)

	saved := withLogo(t, h, logo)
	// The stored file is the normalised one, named by its own hash; setting
	// the logo leaves the settings' revision as it was (2).
	if saved.Logo == nil || saved.Logo.ContentType != "image/png" || saved.Logo.Missing != nil || saved.Revision != 2 {
		t.Fatalf("settings after PUT = logo %+v revision %d, want a PNG and the revision unmoved", saved.Logo, saved.Revision)
	}
	key := "branding/logo-" + saved.Logo.Sha256 + ".png"
	stored := h.objects.object(key)
	if stored == nil || sha(stored) != saved.Logo.Sha256 {
		t.Fatalf("the store holds %d bytes at %s, want the normalised logo under its own hash", len(stored), key)
	}

	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, logoPath, nil)
	if res.Status != http.StatusOK || !bytes.Equal(res.Body, stored) {
		t.Fatalf("GET /settings/logo = %d, %d bytes; want 200 and the logo", res.Status, len(res.Body))
	}
	if res.Header("Content-Type") != "image/png" || res.Header("ETag") != `"`+saved.Logo.Sha256+`"` ||
		res.Header("Cache-Control") != "private, max-age=0, must-revalidate" || res.Header("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers = type %q etag %q cache %q", res.Header("Content-Type"), res.Header("ETag"), res.Header("Cache-Control"))
	}
	// A client holding the current logo is answered 304, with no body.
	again := h.SignIn(t, "invoices:access").Do(http.MethodGet, logoPath, nil, modtest.Header("If-None-Match", `"`+saved.Logo.Sha256+`"`))
	if again.Status != http.StatusNotModified || len(again.Body) != 0 || again.Header("ETag") != `"`+saved.Logo.Sha256+`"` {
		t.Errorf("GET with If-None-Match = %d, %d bytes", again.Status, len(again.Body))
	}
	if other := h.SignIn(t, "invoices:access").Do(http.MethodGet, logoPath, nil, modtest.Header("If-None-Match", `"0000"`)); other.Status != http.StatusOK {
		t.Errorf("GET with another ETag = %d, want 200", other.Status)
	}

	if res := manager(t, h).Do(http.MethodDelete, logoPath, nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE /settings/logo = %d %s, want 204", res.Status, res.Body)
	}
	var read settingsJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&read)
	if read.Logo != nil || read.Revision != 2 {
		t.Errorf("settings after DELETE = logo %+v revision %d, want none and the revision unmoved", read.Logo, read.Revision)
	}
	if h.objects.object(key) == nil {
		t.Error("the removed logo's object was deleted; it is kept for the documents that name it")
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, logoPath, nil); res.Status != http.StatusNotFound {
		t.Errorf("GET without a logo = %d, want 404", res.Status)
	}
	if res := manager(t, h).Do(http.MethodDelete, logoPath, nil); res.Status != http.StatusNotFound {
		t.Errorf("DELETE without a logo = %d, want 404", res.Status)
	}
}

// A logo that breaks a rule is refused with its code and nothing is stored;
// a store that cannot write is 503 with nothing recorded; setting and
// removing need invoices:manage.
func TestLogo_TheRefusals(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	c := manager(t, h)
	var gifBytes bytes.Buffer
	if err := gif.Encode(&gifBytes, image.NewGray(image.Rect(0, 0, 50, 50)), nil); err != nil {
		t.Fatal(err)
	}
	big := append(pngLogo(t, 100, 100), make([]byte, 512<<10)...)
	for _, r := range []struct {
		name string
		res  *modtest.Response
		code string
	}{
		{"a GIF", putLogo(t, c, gifBytes.Bytes()), "logo_unsupported_type"},
		{"past 512 KiB", putLogo(t, c, big), "logo_too_large"},
		{"2,400 pixels wide", putLogo(t, c, pngLogo(t, 2400, 100)), "logo_too_wide"},
		{"10 pixels high", putLogo(t, c, pngLogo(t, 300, 10)), "logo_too_small"},
		{"two files", putLogo(t, c, pngLogo(t, 100, 100), pngLogo(t, 100, 100)), "logo_invalid"},
		{"an empty file", putLogo(t, c, []byte{}), "logo_invalid"},
	} {
		sendRefused(t, r.name, r.res, http.StatusBadRequest, r.code)
	}
	if keys, puts, _ := h.objects.stored(); puts != 0 {
		t.Errorf("a refused logo stored %v", keys)
	}

	h.objects.failPuts(errors.New("disk full"))
	sendRefused(t, "a store that cannot write", putLogo(t, c, pngLogo(t, 100, 100)), http.StatusServiceUnavailable, "storage_unavailable")
	var read settingsJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&read)
	if read.Logo != nil {
		t.Errorf("a logo the store refused is named on the settings: %+v", read.Logo)
	}

	reader := h.SignIn(t, "invoices:access")
	if res := putLogo(t, reader, pngLogo(t, 100, 100)); res.Status != http.StatusForbidden {
		t.Errorf("PUT without invoices:manage = %d, want 403", res.Status)
	}
	if res := reader.Do(http.MethodDelete, logoPath, nil); res.Status != http.StatusForbidden {
		t.Errorf("DELETE without invoices:manage = %d, want 403", res.Status)
	}
}

// The phone (I4) is validated by Customers' rule, kept when a request leaves
// it out, and cleared by an empty one.
func TestSettings_ThePhone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := completeSeller(1)
	body["phone"] = "  +47 22 33 44 55 "
	if saved := saveSeller(t, h, body); saved.Phone != "+47 22 33 44 55" {
		t.Errorf("phone = %q, want it trimmed and as typed", saved.Phone)
	}
	if saved := saveSeller(t, h, completeSeller(2)); saved.Phone != "+47 22 33 44 55" {
		t.Errorf("a body without phone = %q, want the stored phone kept", saved.Phone)
	}
	for _, bad := range []string{"ring meg", "+-() .", strings.Repeat("1", 31)} {
		b := completeSeller(3)
		b["phone"] = bad
		res := manager(t, h).Do(http.MethodPut, settingsPath, b)
		if p := problemOf(t, res); res.Status != http.StatusBadRequest || len(p.Errors["phone"]) == 0 {
			t.Errorf("phone %q = %d %v, want a 400 on phone", bad, res.Status, p.Errors)
		}
	}
	b := completeSeller(3)
	b["phone"] = ""
	if saved := saveSeller(t, h, b); saved.Phone != "" {
		t.Errorf("an empty phone = %q, want it cleared", saved.Phone)
	}
}

// A document takes the phone and the logo at issue (I2), and keeps them when
// the settings change: the stored PDF is rendered with the logo, the logo
// removed from the settings afterwards changes nothing issued, and the next
// document carries none.
func TestIssue_SnapshotsThePhoneAndTheLogo(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	body := completeSeller(2)
	body["phone"] = "22 33 44 55"
	saveSeller(t, h, body)
	logo := pngLogo(t, 240, 80)
	sum := withLogo(t, h, logo).Logo.Sha256

	first := issuedAcme(t, h)
	snapshot := `SELECT coalesce(seller_phone, '-') || '|' || coalesce(seller_logo_object_key, '-') || '|' || coalesce(seller_logo_sha256, '-')
		FROM invoices.invoices WHERE id = $1`
	want := "22 33 44 55|branding/logo-" + sum + ".png|" + sum
	if got := modtest.One[string](t, h.Harness, snapshot, first.ID); got != want {
		t.Errorf("snapshot = %s, want %s", got, want)
	}
	if first.PdfStored == nil || !*first.PdfStored {
		t.Errorf("the PDF of a document with a logo was not stored")
	}

	if res := manager(t, h).Do(http.MethodDelete, logoPath, nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE /settings/logo = %d", res.Status)
	}
	if got := modtest.One[string](t, h.Harness, snapshot, first.ID); got != want {
		t.Errorf("after the logo's removal the issued snapshot = %s, want %s", got, want)
	}
	second := issuedAcme(t, h)
	if got := modtest.One[string](t, h.Harness, snapshot, second.ID); got != "22 33 44 55|-|-" {
		t.Errorf("the next document's snapshot = %s, want the phone and no logo", got)
	}
	if res := download(t, h, second.ID); res.Status != http.StatusOK {
		t.Errorf("download without a logo = %d", res.Status)
	}
}

// A logo whose stored bytes no longer hash to the snapshot is never printed:
// the document's PDF is not stored, and its download is a 500 — never the
// document with another logo. A draft's preview prints the settings' logo.
func TestPDF_ALogoThatDoesNotVerifyIsNotPrinted(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	logo := pngLogo(t, 240, 80)
	sum := withLogo(t, h, logo).Logo.Sha256
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 2, 1000, vat25)))
	if res := creator(t, h).Do(http.MethodGet, previewPath(draft.ID), nil); res.Status != http.StatusOK {
		t.Fatalf("preview with a logo = %d %s", res.Status, res.Body)
	}

	h.objects.replace("branding/logo-"+sum+".png", pngLogo(t, 240, 81))
	inv := issued(t, h, draft.ID)
	if inv.PdfStored != nil && *inv.PdfStored {
		t.Fatal("a document whose logo does not verify had its PDF stored")
	}
	if res := download(t, h, inv.ID); res.Status != http.StatusInternalServerError {
		t.Errorf("download = %d, want 500", res.Status)
	}
}

// A send carries the text and the HTML, the logo inline beside the PDF, and
// the personal message above the standard text in both (I5, I6). The
// delivery keeps the message, answered to an invoices:issue holder alone; a
// message past 2,000 characters is a 400 on message with nothing sent.
func TestSend_TheHTMLTheLogoAndTheMessage(t *testing.T) {
	t.Parallel()
	h, fake := sendReady(t)
	logo := pngLogo(t, 240, 80)
	sum := withLogo(t, h, logo).Logo.Sha256
	inv := issuedAcme(t, h)

	message := "Hei Acme,\r\nher er fakturaen <b>som avtalt</b>.  "
	got := sent(t, h, inv.ID, map[string]any{"message": message})
	out := fake.last(t, 0).out
	if !strings.HasPrefix(out.TextBody, "Hei,\n\nHei Acme,\nher er fakturaen <b>som avtalt</b>.\n\nVedlagt følger faktura") {
		t.Errorf("text body = %q, want the trimmed message after the greeting", out.TextBody)
	}
	for _, want := range []string{`<img src="cid:logo" alt="Kraft-Verket AS"`, "Hei Acme,<br>her er fakturaen &lt;b&gt;som avtalt&lt;/b&gt;."} {
		if !strings.Contains(out.HTMLBody, want) {
			t.Errorf("HTML body lacks %q:\n%s", want, out.HTMLBody)
		}
	}
	if len(out.Attachments) != 2 {
		t.Fatalf("attachments = %d, want the PDF and the logo", len(out.Attachments))
	}
	if a := out.Attachments[1]; !a.Inline || a.ContentID != "logo" || a.ContentType != "image/png" || sha(a.Content) != sum {
		t.Errorf("the logo part = %q %q inline %v id %q", a.FileName, a.ContentType, a.Inline, a.ContentID)
	}
	stored := "Hei Acme,\nher er fakturaen <b>som avtalt</b>."
	if d := got.Deliveries[0]; d.Message == nil || *d.Message != stored {
		t.Errorf("delivery message = %v, want %q", d.Message, stored)
	}
	if read := readAs(t, h.SignIn(t, "invoices:access"), inv.ID); read.Deliveries[0].Message != nil {
		t.Errorf("a reader without invoices:issue is answered the message %q", *read.Deliveries[0].Message)
	}

	res := sendAs(sender(t, h), inv.ID, map[string]any{"message": strings.Repeat("æ", 2001)})
	if p := problemOf(t, res); res.Status != http.StatusBadRequest || len(p.Errors["message"]) == 0 {
		t.Errorf("a message of 2,001 characters = %d %v, want a 400 on message", res.Status, p.Errors)
	}
	if n := len(fake.mails()); n != 1 {
		t.Errorf("%d mails sent, want the one", n)
	}
	sent(t, h, inv.ID, map[string]any{"message": strings.Repeat("æ", 2000)})
}

// A message is refused, before anything is sent, when it carries a control
// character but a line break or a tab; \r\n and a lone \r become \n.
func TestSend_TheMessagesCharacters(t *testing.T) {
	t.Parallel()
	h, fake := sendReady(t)
	inv := issuedAcme(t, h)
	for _, bad := range []string{"Hei\x00der", "Hei\x1b[31m", "Hei\x07"} {
		res := sendAs(sender(t, h), inv.ID, map[string]any{"message": bad})
		if p := problemOf(t, res); res.Status != http.StatusBadRequest || len(p.Errors["message"]) == 0 {
			t.Errorf("message %q = %d %v, want a 400 on message", bad, res.Status, p.Errors)
		}
	}
	if n := len(fake.mails()); n != 0 {
		t.Fatalf("%d mails sent for refused messages", n)
	}
	got := sent(t, h, inv.ID, map[string]any{"message": "En\r\nto\rtre\tfire"})
	if d := got.Deliveries[0]; d.Message == nil || *d.Message != "En\nto\ntre\tfire" {
		t.Errorf("stored message = %v, want the line breaks as \\n and the tab kept", d.Message)
	}
}

// A logo the settings name whose object is gone (review of delivery A): the
// settings say missing; an issue snapshots no logo and is not refused; a
// document snapshotted before the object went, its PDF not stored yet, is a
// 503 to retry, and uploading the same file again restores it.
func TestLogo_AnObjectGoneFromTheStore(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	logo := pngLogo(t, 240, 80)
	sum := withLogo(t, h, logo).Logo.Sha256
	key := "branding/logo-" + sum + ".png"

	// A document snapshotted with the logo whose PDF could not be stored.
	h.objects.failPuts(errors.New("disk full"))
	pending := issuedAcme(t, h)
	h.objects.failPuts(nil)
	if pending.PdfStored != nil && *pending.PdfStored {
		t.Fatal("the PDF was stored though every put failed")
	}

	h.objects.lose(key)
	var read settingsJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&read)
	if read.Logo == nil || read.Logo.Missing == nil || !*read.Logo.Missing {
		t.Errorf("settings with the object gone = %+v, want logo.missing", read.Logo)
	}
	if res := download(t, h, pending.ID); res.Status != http.StatusServiceUnavailable {
		t.Errorf("download of a document whose logo is gone = %d, want 503", res.Status)
	}
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 1, 1000, vat25)))
	if res := creator(t, h).Do(http.MethodGet, previewPath(draft.ID), nil); res.Status != http.StatusServiceUnavailable {
		t.Errorf("preview with the settings' logo gone = %d, want 503", res.Status)
	}

	next := issuedAcme(t, h)
	if got := modtest.One[string](t, h.Harness, `SELECT coalesce(seller_logo_object_key, '-') FROM invoices.invoices WHERE id = $1`, next.ID); got != "-" {
		t.Errorf("an issue with the logo's object gone snapshotted %s, want none", got)
	}
	if !strings.Contains(h.Logs(), "the document is issued without it") {
		t.Error("the issue without the logo was not logged")
	}

	if again := withLogo(t, h, logo); again.Logo.Sha256 != sum || again.Logo.Missing != nil {
		t.Errorf("the same file again = %+v, want the same key, no longer missing", again.Logo)
	}
	if res := download(t, h, pending.ID); res.Status != http.StatusOK {
		t.Errorf("download once the logo is back = %d, want 200", res.Status)
	}
}

// A delivery's message is blanked with its recipient when the customer is
// anonymised (I6): the erase blanks both, the update trigger allows exactly
// that and refuses a message changed to anything else, and the insert
// trigger blanks the message of a delivery written after the erase.
func TestDeliveries_TheMessageIsBlankedOnAnonymisation(t *testing.T) {
	t.Parallel()
	h, _ := sendReady(t)
	doc := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Konsultasjon", 1, 1000, vat25))).ID)
	sent(t, h, doc.ID, map[string]any{"message": "Hilsen til Kari"})

	ctx := context.Background()
	if _, err := h.Pool().Exec(ctx, `UPDATE invoices.deliveries SET message = 'endret' WHERE invoice_id = $1`, doc.ID); err == nil {
		t.Error("a delivery's message changed to another text; the trigger allows only ''")
	}
	if _, err := h.Pool().Exec(ctx, `UPDATE invoices.deliveries SET recipient = '' WHERE invoice_id = $1`, doc.ID); err == nil {
		t.Error("a delivery's recipient was blanked with its message kept; the anonymisation blanks both")
	}

	data := invoices.Module().CustomerPersonalData(disabledDeps(h))
	inTx(t, h, true, func(tx pgx.Tx) {
		if _, err := data.EraseCustomerData(ctx, tx, customerPerson); err != nil {
			t.Fatalf("EraseCustomerData: %v", err)
		}
	})
	if n := h.Count(t, `SELECT count(*) FROM invoices.deliveries WHERE invoice_id = $1 AND recipient = '' AND message = ''`, doc.ID); n != 1 {
		t.Errorf("%d deliveries blanked, want the one, its message too", n)
	}

	h.Exec(t, `INSERT INTO invoices.deliveries (invoice_id, recipient, subject, message_id, pdf_sha256, sent_at, sent_by_user_id, message)
		SELECT id, 'kari@example.org', 'Faktura', 'm@vantigo.invalid', pdf_sha256, now(), gen_random_uuid(), 'Etter sletting'
		FROM invoices.invoices WHERE id = $1`, doc.ID)
	if n := h.Count(t, `SELECT count(*) FROM invoices.deliveries WHERE invoice_id = $1 AND message <> ''`, doc.ID); n != 0 {
		t.Errorf("a delivery written after the erase kept its message")
	}
}
