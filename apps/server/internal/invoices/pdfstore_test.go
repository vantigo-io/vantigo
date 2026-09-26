package invoices_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func pdfPath(id int64) string     { return fmt.Sprintf("%s/%d/pdf", invoicesPath, id) }
func previewPath(id int64) string { return fmt.Sprintf("%s/%d/preview.pdf", invoicesPath, id) }

func download(t *testing.T, h *harness, id int64) *modtest.Response {
	t.Helper()
	return h.SignIn(t, "invoices:access").Do(http.MethodGet, pdfPath(id), nil)
}

// issuedAcme issues one invoice to Acme and answers it.
func issuedAcme(t *testing.T, h *harness) invoiceJSON {
	t.Helper()
	return issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 2, 1000, vat25))).ID)
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// The issue stores the PDF once, under the scope-relative key the bytes
// name; the download streams exactly those bytes, as an attachment named in
// the document's language; the module never deletes (D7).
func TestPDF_IssueStoresItOnceAndTheDownloadStreamsIt(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := issuedAcme(t, h)
	if inv.PdfStored == nil || !*inv.PdfStored {
		t.Fatalf("pdfStored = %v, want true", inv.PdfStored)
	}
	keys, puts, _ := h.objects.stored()
	if len(keys) != 1 || puts != 1 || !regexp.MustCompile(fmt.Sprintf(`^documents/%d/1-[0-9a-f]{64}\.pdf$`, inv.ID)).MatchString(keys[0]) {
		t.Fatalf("stored = %v after %d puts, want one scope-relative documents/<id>/<number>-<sha256>.pdf", keys, puts)
	}
	column := modtest.One[string](t, h.Harness, `SELECT pdf_object_key || ' ' || pdf_sha256 FROM invoices.invoices WHERE id = $1`, inv.ID)
	if !strings.HasPrefix(column, keys[0]+" ") || !strings.HasSuffix(keys[0], "-"+strings.Fields(column)[1]+".pdf") {
		t.Errorf("row = %q, want the key and the hash it names", column)
	}

	res := download(t, h, inv.ID)
	if res.Status != http.StatusOK || res.Header("Content-Type") != "application/pdf" ||
		res.Header("Content-Disposition") != `attachment; filename="faktura-1.pdf"` {
		t.Fatalf("download = %d %q %q", res.Status, res.Header("Content-Type"), res.Header("Content-Disposition"))
	}
	if sha(res.Body) != strings.Fields(column)[1] || string(res.Body) != string(h.objects.object(keys[0])) {
		t.Error("the download is not the stored object")
	}
	if _, puts, _ := h.objects.stored(); puts != 1 {
		t.Errorf("puts after the download = %d, want no second store", puts)
	}

	english := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Konsultasjon", 1, 500, vat25))).ID)
	if res := download(t, h, english.ID); res.Header("Content-Disposition") != `attachment; filename="invoice-2.pdf"` {
		t.Errorf("an English document = %q, want invoice-2.pdf", res.Header("Content-Disposition"))
	}
	if _, _, deletes := h.objects.stored(); deletes != 0 {
		t.Errorf("deletes = %d, want none ever", deletes)
	}
}

// A Put failing after the commit does not fail the issue: it answers
// pdfStored false, and the next download stores it once (D6, D7).
func TestPDF_APutFailingAfterTheCommitIsStoredByTheNextDownload(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	h.objects.failPuts(errors.New("disk full"))
	inv := issuedAcme(t, h)
	if inv.Status != "issued" || inv.PdfStored == nil || *inv.PdfStored {
		t.Fatalf("issue with a failing store = %s, pdfStored %v; want issued and false", inv.Status, inv.PdfStored)
	}
	if !strings.Contains(h.Logs(), "could not be stored") {
		t.Error("the failed store was not logged")
	}
	if res := download(t, h, inv.ID); res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "storage_unavailable" {
		t.Errorf("a download while the store still fails = %d %s, want 503", res.Status, res.Body)
	}

	h.objects.failPuts(nil)
	res := download(t, h, inv.ID)
	if res.Status != http.StatusOK {
		t.Fatalf("the next download = %d %s", res.Status, res.Body)
	}
	keys, puts, _ := h.objects.stored()
	if len(keys) != 1 || puts != 1 || sha(res.Body) != sha(h.objects.object(keys[0])) {
		t.Errorf("stored = %v after %d puts, want the one object the download streamed", keys, puts)
	}
	if again := getInvoice(t, h, inv.ID); again.PdfStored == nil || !*again.PdfStored {
		t.Error("the document still says its PDF is not stored")
	}
}

// The key and the hash are one fact (D7): the schema refuses a row with one
// and not the other, so no download ever finds a hash without the object it
// names. Set together, once, they are allowed.
func TestPDF_TheKeyAndTheHashAreSetTogether(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	h.objects.failPuts(errors.New("offline"))
	inv := issuedAcme(t, h)
	ctx := context.Background()
	for _, sql := range []string{
		`UPDATE invoices.invoices SET pdf_sha256 = repeat('a', 64) WHERE id = $1`,
		`UPDATE invoices.invoices SET pdf_object_key = 'documents/x.pdf' WHERE id = $1`,
	} {
		if _, err := h.Pool().Exec(ctx, sql, inv.ID); err == nil || !strings.Contains(err.Error(), "ck_invoices_pdf") {
			t.Errorf("%s: %v, want ck_invoices_pdf to refuse it", sql, err)
		}
	}
	h.Exec(t, `UPDATE invoices.invoices SET pdf_object_key = 'documents/x.pdf', pdf_sha256 = repeat('a', 64) WHERE id = $1`, inv.ID)
}

// Racing downloads of an unstored PDF. A render is reproducible, so plain
// racers would store the same bytes and could not tell a loser that streams
// the winner's object from one that streams its own. Here each racer's render
// differs, and all of them have rendered before any records its hash: one hash wins, every racer streams the
// winner's bytes, and each loser's object stays an orphan, never overwriting
// another (D7). Not parallel: the hook is the package's.
func TestPDF_ARaceLoserStreamsTheWinnersObject(t *testing.T) {
	h := readyToIssue(t)
	h.objects.failPuts(errors.New("offline"))
	inv := issuedAcme(t, h)
	h.objects.failPuts(nil)

	const racers = 4
	var (
		mu       sync.Mutex
		rendered int
		all      = make(chan struct{})
	)
	restore := invoices.SetAfterPDFRender(func(_ context.Context, body []byte) []byte {
		mu.Lock()
		rendered++
		mine := rendered
		if rendered == racers {
			close(all)
		}
		mu.Unlock()
		select {
		case <-all:
		case <-time.After(10 * time.Second):
			t.Errorf("racer %d waited for the others in vain", mine)
		}
		return append(slices.Clone(body), fmt.Sprintf("%%racer %d\n", mine)...)
	})
	defer restore()

	bodies := make([][]byte, racers)
	var wg sync.WaitGroup
	for i := range bodies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := download(t, h, inv.ID)
			if res.Status != http.StatusOK {
				t.Errorf("racing download = %d %s", res.Status, res.Body)
				return
			}
			bodies[i] = res.Body
		}()
	}
	wg.Wait()
	stored := modtest.One[string](t, h.Harness, `SELECT pdf_sha256 FROM invoices.invoices WHERE id = $1`, inv.ID)
	for i, b := range bodies {
		if sha(b) != stored {
			t.Errorf("download %d streamed %s, want the winner's %s", i, sha(b), stored)
		}
	}
	if keys, puts, _ := h.objects.stored(); len(keys) != racers || puts != racers {
		t.Errorf("stored = %v after %d puts, want each racer's own object, none overwritten", keys, puts)
	}
}

// A stored object whose bytes no longer match is a 500 and is never rendered
// again; a missing one is a 500; a Get that fails is a 503; a draft has no
// download (D7).
func TestPDF_TheStoredObjectsFailureModes(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := issuedAcme(t, h)
	keys, _, _ := h.objects.stored()

	h.objects.failGets(errors.New("timeout"))
	if res := download(t, h, inv.ID); res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "storage_unavailable" {
		t.Errorf("a failing Get = %d %s, want 503 storage_unavailable", res.Status, res.Body)
	}
	h.objects.failGets(nil)

	h.objects.replace(keys[0], []byte("%PDF-1.3 something else"))
	if res := download(t, h, inv.ID); res.Status != http.StatusInternalServerError {
		t.Errorf("tampered bytes = %d, want 500", res.Status)
	}
	if _, puts, _ := h.objects.stored(); puts != 1 {
		t.Errorf("puts = %d, want the tampered document never rendered again", puts)
	}
	if !strings.Contains(h.Logs(), "does not match its hash") {
		t.Error("the mismatch was not logged")
	}

	h.objects.lose(keys[0])
	if res := download(t, h, inv.ID); res.Status != http.StatusInternalServerError {
		t.Errorf("a lost object = %d, want 500", res.Status)
	}

	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	if res := download(t, h, draft.ID); res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_draft" {
		t.Errorf("a draft's download = %d %s, want 409 invoice_draft", res.Status, res.Body)
	}
	if res := download(t, h, 424242); res.Status != http.StatusNotFound {
		t.Errorf("an unknown document = %d, want 404", res.Status)
	}
}

// Without a store the download is a 503.
func TestPDF_WithoutAStore(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutStore(t)
	id := plantIssuedDocument(t, h, 1, "2026-09-01")
	if res := download(t, h, id); res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "storage_unavailable" {
		t.Errorf("a download without a store = %d %s, want 503", res.Status, res.Body)
	}
}

// The preview renders a draft on demand and stores nothing; an issued
// document is refused; it needs invoices:create, the download only access.
func TestPDF_ThePreview(t *testing.T) {
	t.Parallel()
	h := newHarness(t) // an incomplete seller previews too
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 2, 1000, vat25)))

	res := creator(t, h).Do(http.MethodGet, previewPath(draft.ID), nil)
	if res.Status != http.StatusOK || res.Header("Content-Type") != "application/pdf" || !strings.HasPrefix(string(res.Body), "%PDF-") ||
		res.Header("Content-Disposition") != fmt.Sprintf(`inline; filename="utkast-%d.pdf"`, draft.ID) {
		t.Fatalf("preview = %d %q %q", res.Status, res.Header("Content-Type"), res.Header("Content-Disposition"))
	}
	if keys, puts, _ := h.objects.stored(); len(keys) != 0 || puts != 0 {
		t.Errorf("the preview stored %v", keys)
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, previewPath(draft.ID), nil); res.Status != http.StatusForbidden {
		t.Errorf("a preview without invoices:create = %d, want 403", res.Status)
	}
	if res := creator(t, h).Do(http.MethodGet, previewPath(424242), nil); res.Status != http.StatusNotFound {
		t.Errorf("an unknown preview = %d, want 404", res.Status)
	}

	saveSeller(t, h, completeSeller(1))
	inv := issued(t, h, draft.ID)
	if res := creator(t, h).Do(http.MethodGet, previewPath(inv.ID), nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_issued" {
		t.Errorf("previewing an issued document = %d %s, want 409 invoice_issued", res.Status, res.Body)
	}
}

// Only the preview carries the watermark, and only the issued document a
// number (D4, D7) — asserted on the model each PDF is laid out from, not on
// the content stream's bytes. Not parallel: the hook is the package's.
func TestPDF_OnlyThePreviewCarriesTheWatermark(t *testing.T) {
	h := readyToIssue(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 2, 1000, vat25)))
	type model struct {
		watermark string
		numbered  bool
	}
	var mu sync.Mutex
	var models []model
	defer invoices.SetPDFModelBuilt(func(id int64, watermark string, numbered bool) {
		if id != draft.ID {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		models = append(models, model{watermark, numbered})
	})()

	if res := creator(t, h).Do(http.MethodGet, previewPath(draft.ID), nil); res.Status != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Status, res.Body)
	}
	issued(t, h, draft.ID) // the issue stores its PDF, so it builds one model
	mu.Lock()
	defer mu.Unlock()
	if want := []model{{"UTKAST — ikke et salgsdokument", false}, {"", true}}; !slices.Equal(models, want) {
		t.Errorf("models = %+v, want the preview watermarked and unnumbered, then the issued document plain and numbered", models)
	}
}

// A download reads at most 20 MB of a stored object (D7): an object of exactly
// 20 MB streams, one byte more is a 500 even when its hash is the recorded one.
func TestPDF_AStoredObjectOverTheCapIsA500(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, tc := range []struct {
		size int
		want int
	}{{20 << 20, http.StatusOK}, {20<<20 + 1, http.StatusInternalServerError}} {
		id := plantIssuedDocument(t, h, int64(tc.size), "2026-09-01")
		body := make([]byte, tc.size)
		key := fmt.Sprintf("documents/%d/%d-%s.pdf", id, tc.size, sha(body))
		h.objects.replace(key, body)
		h.Exec(t, `UPDATE invoices.invoices SET pdf_object_key = $2, pdf_sha256 = $3 WHERE id = $1`, id, key, sha(body))
		if res := download(t, h, id); res.Status != tc.want {
			t.Errorf("a stored object of %d bytes = %d, want %d", tc.size, res.Status, tc.want)
		}
	}
}
