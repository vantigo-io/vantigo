package expenses_test

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the receipts (decision X11): what may be uploaded onto an
// expense, who may read one back, and when either stops being allowed. The
// bytes never touch the database — they go to the object store under a random
// key — and they are only ever served back through this API, with the
// expense's own visibility.

// TestExpensesReceipts_EveryAllowedTypeRoundTrips: the four formats design
// §3.2 names go up, are stored under the type sniffed from their own bytes,
// and come back byte for byte. HEIC is the interesting one: net/http's
// sniffer does not know it, so the module reads the ISO base media brand
// itself.
func TestExpensesReceipts_EveryAllowedTypeRoundTrips(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	for _, tc := range []struct {
		name        string
		fileName    string
		declared    string
		data        []byte
		contentType string
	}{
		{"a photograph of a till receipt", "kvittering.jpg", "image/jpeg", testJPEG(t, 8, 8), "image/jpeg"},
		{"a screenshot", "skjermbilde.png", "image/png", testPNG(t, 8, 8), "image/png"},
		{"an invoice", "faktura.pdf", "application/pdf", testPDF(64), "application/pdf"},
		{"an iPhone photograph", "IMG_0042.HEIC", "image/heic", testHEIC("heic"), "image/heic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := createEntry(t, owner, outlayBody(nil))
			got := uploadReceipt(t, owner, entry.Id, tc.fileName, tc.declared, tc.data)

			if got.FileName != tc.fileName || got.ContentType != tc.contentType || got.SizeBytes != int64(len(tc.data)) {
				t.Errorf("uploaded = %+v, want %s as %s of %d bytes", got, tc.fileName, tc.contentType, len(tc.data))
			}
			if got.Id == 0 {
				t.Error("the receipt has no id")
			}

			after := getEntry(t, owner, entry.Id)
			if after.AttachmentCount != 1 || len(after.Attachments) != 1 || after.Attachments[0] != got {
				t.Errorf("entry attachments = %d %+v, want the one just uploaded", after.AttachmentCount, after.Attachments)
			}

			read := downloadReceipt(t, owner, got.Id)
			if !bytes.Equal(read.Body, tc.data) {
				t.Errorf("downloaded %d bytes, want the %d uploaded", len(read.Body), len(tc.data))
			}
			if ct := read.Header("Content-Type"); ct != tc.contentType {
				t.Errorf("Content-Type = %q, want the stored %q", ct, tc.contentType)
			}
		})
	}
}

// TestExpensesReceipts_AreServedPrivatelyAndNamedAsTheyWereUploaded: a
// receipt is a private document, so it is never cached and never sniffed, and
// it carries the name its owner gave it — RFC 5987 encoded, so a Norwegian
// file name survives. An image or a PDF is shown in place; HEIC is handed over
// as a file, because no browser but Safari renders one.
func TestExpensesReceipts_AreServedPrivatelyAndNamedAsTheyWereUploaded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	entry := createEntry(t, owner, outlayBody(nil))

	pdf := uploadReceipt(t, owner, entry.Id, "Kvittering på tømmer.pdf", "application/pdf", testPDF(16))
	read := downloadReceipt(t, owner, pdf.Id)
	if cd := read.Header("Content-Disposition"); cd != `inline; filename*=UTF-8''Kvittering%20p%C3%A5%20t%C3%B8mmer.pdf` {
		t.Errorf("Content-Disposition = %q, want the inline RFC 5987 name", cd)
	}
	if cc := read.Header("Cache-Control"); cc != "private, no-store" {
		t.Errorf("Cache-Control = %q, want private, no-store", cc)
	}
	if xcto := read.Header("X-Content-Type-Options"); xcto != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", xcto)
	}

	heic := uploadReceipt(t, owner, entry.Id, "IMG_1.heic", "image/heic", testHEIC("heix"))
	if cd := downloadReceipt(t, owner, heic.Id).Header("Content-Disposition"); !strings.HasPrefix(cd, "attachment; ") {
		t.Errorf("Content-Disposition of a HEIC = %q, want it served as a file", cd)
	}
}

// TestExpensesReceipts_AreNamedByTheirOwnerButNeverByTheirPath: the client's
// name is kept, stripped of any directory it arrived with and capped at the
// column's 255 characters, and it is never what the object is stored under.
func TestExpensesReceipts_AreNamedByTheirOwnerButNeverByTheirPath(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	entry := createEntry(t, owner, outlayBody(nil))

	got := uploadReceipt(t, owner, entry.Id, `../../etc/passwd/kvittering.pdf`, "application/pdf", testPDF(8))
	if got.FileName != "kvittering.pdf" {
		t.Errorf("fileName = %q, want the base name alone", got.FileName)
	}

	long := uploadReceipt(t, owner, entry.Id, strings.Repeat("æ", 400)+".pdf", "application/pdf", testPDF(8))
	if runes := []rune(long.FileName); len(runes) != 255 {
		t.Errorf("fileName is %d characters, want it capped at 255", len(runes))
	}

	for _, key := range h.objects.keys() {
		if strings.Contains(key, "kvittering") || strings.Contains(key, "passwd") || strings.Contains(key, "æ") {
			t.Errorf("object key %q is derived from the file name, want a random one", key)
		}
		if !strings.HasPrefix(key, fmt.Sprintf("receipts/%d/", entry.Id)) {
			t.Errorf("object key %q, want it under the entry's own prefix", key)
		}
	}
}

// TestExpensesReceipts_RefuseWhatIsNotOneOfTheFourTypes: the type is decided
// by the bytes, and the bytes have to agree with what the client called them —
// a PNG named .pdf is somebody's mistake at best, so it is refused rather than
// stored under a type it is not.
func TestExpensesReceipts_RefuseWhatIsNotOneOfTheFourTypes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	entry := createEntry(t, owner, outlayBody(nil))

	for _, tc := range []struct {
		name     string
		fileName string
		declared string
		data     []byte
	}{
		{"a text file", "notat.txt", "text/plain", []byte("dette er ingen kvittering")},
		{"a GIF", "animasjon.gif", "image/gif", []byte("GIF89a" + strings.Repeat("\x00", 32))},
		{"a PNG called a PDF", "kvittering.pdf", "application/pdf", testPNG(t, 4, 4)},
		{"a PDF called a PNG", "kvittering.png", "image/png", testPDF(32)},
		{"a PDF named .png, declared as a PDF", "kvittering.png", "application/pdf", testPDF(32)},
		{"an empty file", "tom.pdf", "application/pdf", nil},
		{"an ISO media file that is not HEIC", "film.mp4", "video/mp4", testHEIC("mp42")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := refusedReceipt(t, owner, entry.Id, tc.fileName, tc.declared, tc.data)
			if len(errs["file"]) == 0 {
				t.Errorf("errors = %v, want the refusal on the file field", errs)
			}
		})
	}

	if got := getEntry(t, owner, entry.Id); got.AttachmentCount != 0 || len(got.Attachments) != 0 {
		t.Errorf("entry carries %d receipts, want none of the refused ones stored", got.AttachmentCount)
	}
	if n := h.objects.count(); n != 0 {
		t.Errorf("the object store holds %d objects, want nothing written for a refused upload", n)
	}
}

// TestExpensesReceipts_NeedAPartCalledFile: the contract documents one field,
// and the part has to be it. A body that is not multipart at all is the same
// refusal.
func TestExpensesReceipts_NeedAPartCalledFile(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	entry := createEntry(t, owner, outlayBody(nil))

	ct, body := receiptForm(t, "receipt", "kvittering.pdf", "application/pdf", testPDF(8))
	wrongName := owner.Do(http.MethodPost, entryAttachmentsPath(entry.Id), nil, modtest.RawBody(ct, body))
	if wrongName.Status != http.StatusBadRequest {
		t.Errorf("a part named 'receipt': status %d body %s, want 400", wrongName.Status, wrongName.Body)
	}

	notMultipart := owner.Do(http.MethodPost, entryAttachmentsPath(entry.Id), nil,
		modtest.RawBody("application/json", []byte(`{"file":"kvittering.pdf"}`)))
	if notMultipart.Status != http.StatusBadRequest {
		t.Errorf("a JSON body: status %d body %s, want 400", notMultipart.Status, notMultipart.Body)
	}
}

// TestExpensesReceipts_AreCappedAtTenMegabytes: design §3.2's size bound,
// judged on the file itself. The router's own cap for this operation is that
// plus the multipart framing, so a legitimate ten-megabyte receipt is not
// refused by the platform before the handler ever sees it.
func TestExpensesReceipts_AreCappedAtTenMegabytes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	entry := createEntry(t, owner, outlayBody(nil))

	const maxBytes = 10 * 1024 * 1024
	big := testPDF(maxBytes)
	if errs := refusedReceipt(t, owner, entry.Id, "for-stor.pdf", "application/pdf", big); len(errs["file"]) == 0 {
		t.Errorf("errors = %v, want the refusal on the file field", errs)
	}
	if n := h.objects.count(); n != 0 {
		t.Errorf("the object store holds %d objects, want nothing written for an oversized upload", n)
	}

	just := testPDF(maxBytes - 1024)
	got := uploadReceipt(t, owner, entry.Id, "stor.pdf", "application/pdf", just)
	if got.SizeBytes != int64(len(just)) {
		t.Errorf("sizeBytes = %d, want %d", got.SizeBytes, len(just))
	}
}

// TestExpensesReceipts_AreCappedAtTenPerExpense: design §3.2's count bound.
// The eleventh is refused, and the object it had already been written under is
// removed again — a refusal leaves nothing behind.
func TestExpensesReceipts_AreCappedAtTenPerExpense(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	entry := createEntry(t, owner, outlayBody(nil))

	for i := range 10 {
		uploadReceipt(t, owner, entry.Id, fmt.Sprintf("kvittering-%d.pdf", i), "application/pdf", testPDF(8))
	}
	errs := refusedReceipt(t, owner, entry.Id, "kvittering-11.pdf", "application/pdf", testPDF(8))
	if len(errs["entryId"]) == 0 {
		t.Errorf("errors = %v, want the refusal on the expense rather than the file", errs)
	}
	if got := getEntry(t, owner, entry.Id); got.AttachmentCount != 10 || len(got.Attachments) != 10 {
		t.Errorf("entry carries %d receipts, want exactly ten", got.AttachmentCount)
	}
	if n := h.objects.count(); n != 10 {
		t.Errorf("the object store holds %d objects, want the ten that are rows", n)
	}
}

// TestExpensesReceipts_AreForOutlaysOnly: a kilometre has no receipt, so a
// mileage line takes none (design §3.1's split between the two kinds).
func TestExpensesReceipts_AreForOutlaysOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	mileage := createEntry(t, owner, mileageBody(nil))

	errs := refusedReceipt(t, owner, mileage.Id, "kvittering.pdf", "application/pdf", testPDF(8))
	if len(errs["entryId"]) == 0 {
		t.Errorf("errors = %v, want the refusal on the expense", errs)
	}
	if n := h.objects.count(); n != 0 {
		t.Errorf("the object store holds %d objects, want none", n)
	}
}

// TestExpensesReceipts_OnlyWhileTheExpenseIsEditable: spec §8 — an upload to
// an expense that has been submitted or approved is refused, and so is
// removing one. A rejected line is a draft again and takes both.
func TestExpensesReceipts_OnlyWhileTheExpenseIsEditable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	for _, status := range []string{"submitted", "approved"} {
		t.Run(status, func(t *testing.T) {
			entry := createEntry(t, owner, outlayBody(nil))
			receipt := uploadReceipt(t, owner, entry.Id, "kvittering.pdf", "application/pdf", testPDF(8))
			h.Exec(t, `UPDATE expenses.entries SET status = $1 WHERE id = $2`, status, entry.Id)

			errs := refusedReceipt(t, owner, entry.Id, "en-til.pdf", "application/pdf", testPDF(8))
			if len(errs["entryId"]) == 0 {
				t.Errorf("errors = %v, want the refusal on the expense", errs)
			}
			// What the expense *is* refuses the delete, not who is asking, so
			// it is a 400 naming the reason — the same rule the expense's own
			// PUT and DELETE follow.
			errs = refusedReceiptDelete(t, owner, receipt.Id)
			if len(errs["entryId"]) == 0 {
				t.Errorf("delete a %s expense's receipt: errors = %v, want one on entryId", status, errs)
			}
			// It is still readable: an approver has to be able to see what
			// they approved.
			downloadReceipt(t, owner, receipt.Id)
		})
	}

	rejected := createEntry(t, owner, outlayBody(nil))
	h.Exec(t, `UPDATE expenses.entries SET status = 'rejected' WHERE id = $1`, rejected.Id)
	receipt := uploadReceipt(t, owner, rejected.Id, "kvittering.pdf", "application/pdf", testPDF(8))
	if r := owner.Do(http.MethodDelete, attachmentPath(receipt.Id), nil); r.Status != http.StatusNoContent {
		t.Errorf("delete a rejected expense's receipt: status %d body %s, want 204", r.Status, r.Body)
	}
}

// TestExpensesReceipts_ThePeriodLockFreezesThemToo: design §4's lock is on
// every mutating path, receipts included, and expenses:manage is the one
// exemption.
func TestExpensesReceipts_ThePeriodLockFreezesThemToo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	admin, _ := signIn(t, h, "expenses:manage")

	entry := createEntry(t, owner, outlayBody(nil))
	receipt := uploadReceipt(t, owner, entry.Id, "kvittering.pdf", "application/pdf", testPDF(8))
	putSettings(t, admin, settingsBody(map[string]any{"lockedBefore": "2026-04-01"}))

	errs := refusedReceipt(t, owner, entry.Id, "en-til.pdf", "application/pdf", testPDF(8))
	if len(errs["entryId"]) == 0 {
		t.Errorf("errors = %v, want the refusal on the expense", errs)
	}
	if errs := refusedReceiptDelete(t, owner, receipt.Id); len(errs["entryId"]) == 0 {
		t.Errorf("delete behind the lock: errors = %v, want one on entryId naming the lock", errs)
	}

	// The administrator works past it, for their colleague's expense.
	added := uploadReceipt(t, admin, entry.Id, "admin.pdf", "application/pdf", testPDF(8))
	if r := admin.Do(http.MethodDelete, attachmentPath(added.Id), nil); r.Status != http.StatusNoContent {
		t.Errorf("delete past the lock: status %d body %s, want 204", r.Status, r.Body)
	}
	if got := getEntry(t, owner, entry.Id); got.Owner.UserId != ownerID || got.AttachmentCount != 1 {
		t.Errorf("entry = %+v, want the owner's, carrying the one receipt", got)
	}
}

// TestExpensesReceipts_AreReadableByWhoeverMaySeeTheExpense: visibility is
// the expense's own (design §5), and everybody else gets the bare 404 an
// unknown id gets — the same answer for a receipt on an expense they may not
// see, for an id no receipt has, and for an expense that does not exist.
func TestExpensesReceipts_AreReadableByWhoeverMaySeeTheExpense(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	approver, _ := signIn(t, h, "expenses:approve")
	viewer, _ := signIn(t, h, "expenses:view-all")
	outsider, _ := signIn(t, h)

	entry := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	receipt := uploadReceipt(t, owner, entry.Id, "kvittering.pdf", "application/pdf", testPDF(8))

	for _, tc := range []struct {
		name   string
		client *modtest.Client
	}{
		{"its owner", owner},
		{"a manager of its project", manager},
		{"an approver", approver},
		{"view-all", viewer},
	} {
		if r := tc.client.Do(http.MethodGet, attachmentPath(receipt.Id), nil); r.Status != http.StatusOK {
			t.Errorf("%s downloading: status %d body %s, want 200", tc.name, r.Status, r.Body)
		}
	}

	for _, tc := range []struct {
		name string
		path string
	}{
		{"a receipt on an expense they may not see", attachmentPath(receipt.Id)},
		{"an id no receipt has", attachmentPath(receipt.Id + 9999)},
	} {
		r := outsider.Do(http.MethodGet, tc.path, nil)
		if r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("an outsider reading %s: status %d body %q, want a bare 404", tc.name, r.Status, r.Body)
		}
		if d := outsider.Do(http.MethodDelete, tc.path, nil); d.Status != http.StatusNotFound || len(d.Body) != 0 {
			t.Errorf("an outsider deleting %s: status %d body %q, want a bare 404", tc.name, d.Status, d.Body)
		}
	}

	// Uploading answers the same way, whether the expense is invisible or
	// simply not there.
	for _, tc := range []struct {
		name    string
		entryID int64
	}{
		{"an expense they may not see", entry.Id},
		{"an expense that does not exist", entry.Id + 9999},
	} {
		r := postReceipt(t, outsider, tc.entryID, "kvittering.pdf", "application/pdf", testPDF(8))
		if r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("an outsider uploading to %s: status %d body %q, want a bare 404", tc.name, r.Status, r.Body)
		}
	}
	if n := h.objects.count(); n != 1 {
		t.Errorf("the object store holds %d objects, want only the one receipt", n)
	}
}

// TestExpensesReceipts_AreDeletedByWhoeverMayChangeTheExpense: seeing an
// expense is not changing it. A colleague with view-all may read the receipt
// and is refused the delete; the owner's delete takes the row and the object
// with it.
func TestExpensesReceipts_AreDeletedByWhoeverMayChangeTheExpense(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	viewer, _ := signIn(t, h, "expenses:view-all")

	entry := createEntry(t, owner, outlayBody(nil))
	receipt := uploadReceipt(t, owner, entry.Id, "kvittering.pdf", "application/pdf", testPDF(8))
	keys := h.objects.keys()
	if len(keys) != 1 {
		t.Fatalf("the object store holds %v, want one object", keys)
	}

	if r := viewer.Do(http.MethodDelete, attachmentPath(receipt.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("view-all deleting: status %d body %s, want 403", r.Status, r.Body)
	}

	if r := owner.Do(http.MethodDelete, attachmentPath(receipt.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s, want 204", r.Status, r.Body)
	}
	if n := h.objects.count(); n != 0 {
		t.Errorf("the object store still holds %v, want the object gone with the row", h.objects.keys())
	}
	if got := getEntry(t, owner, entry.Id); got.AttachmentCount != 0 || len(got.Attachments) != 0 {
		t.Errorf("entry carries %d receipts after the delete, want none", got.AttachmentCount)
	}
	if r := owner.Do(http.MethodGet, attachmentPath(receipt.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("reading a deleted receipt: status %d, want 404", r.Status)
	}
	if r := owner.Do(http.MethodDelete, attachmentPath(receipt.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("deleting it twice: status %d, want the unknown id's 404", r.Status)
	}
}

// TestExpensesReceipts_GoWithTheExpenseWhenItIsDeleted: the rows go with the
// entry (the foreign key cascades), and the objects they named go with them —
// after the delete has committed, so nothing is removed for a delete that did
// not happen.
func TestExpensesReceipts_GoWithTheExpenseWhenItIsDeleted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	kept := createEntry(t, owner, outlayBody(nil))
	uploadReceipt(t, owner, kept.Id, "beholdes.pdf", "application/pdf", testPDF(8))

	entry := createEntry(t, owner, outlayBody(nil))
	first := uploadReceipt(t, owner, entry.Id, "en.pdf", "application/pdf", testPDF(8))
	second := uploadReceipt(t, owner, entry.Id, "to.png", "image/png", testPNG(t, 4, 4))
	if n := h.objects.count(); n != 3 {
		t.Fatalf("the object store holds %d objects, want three", n)
	}

	if r := owner.Do(http.MethodDelete, entryPath(entry.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete the expense: status %d body %s, want 204", r.Status, r.Body)
	}
	if n := h.objects.count(); n != 1 {
		t.Errorf("the object store holds %v, want only the other expense's receipt", h.objects.keys())
	}
	for _, id := range []int64{first.Id, second.Id} {
		if r := owner.Do(http.MethodGet, attachmentPath(id), nil); r.Status != http.StatusNotFound {
			t.Errorf("receipt %d after the expense was deleted: status %d, want 404", id, r.Status)
		}
	}
	if h.Count(t, `SELECT count(*) FROM expenses.attachments WHERE entry_id = $1`, entry.Id) != 0 {
		t.Error("the attachment rows outlived the expense")
	}
}

// TestExpensesReceipts_AStorageFailureLeavesNothingBehind: the object is
// written before the row and the row is what makes it a receipt, so a store
// that will not write answers a 503 and leaves the expense as it was. A store
// that will not read answers the same, rather than pretending the receipt is
// not there.
func TestExpensesReceipts_AStorageFailureLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	entry := createEntry(t, owner, outlayBody(nil))

	h.objects.failPut(errors.New("the receipt store is down"))
	r := postReceipt(t, owner, entry.Id, "kvittering.pdf", "application/pdf", testPDF(8))
	if r.Status != http.StatusServiceUnavailable {
		t.Errorf("upload while the store is down: status %d body %s, want 503", r.Status, r.Body)
	}
	if got := getEntry(t, owner, entry.Id); got.AttachmentCount != 0 {
		t.Errorf("entry carries %d receipts, want none written", got.AttachmentCount)
	}

	h.objects.failPut(nil)
	receipt := uploadReceipt(t, owner, entry.Id, "kvittering.pdf", "application/pdf", testPDF(8))

	h.objects.failGet(errors.New("the receipt store is down"))
	if read := owner.Do(http.MethodGet, attachmentPath(receipt.Id), nil); read.Status != http.StatusServiceUnavailable {
		t.Errorf("download while the store is down: status %d body %s, want 503", read.Status, read.Body)
	}

	// A delete the store refuses is still a delete: the row is gone, and the
	// object that could not be removed is somebody else's problem to sweep.
	h.objects.failGet(nil)
	h.objects.failDelete(errors.New("the receipt store is down"))
	if del := owner.Do(http.MethodDelete, attachmentPath(receipt.Id), nil); del.Status != http.StatusNoContent {
		t.Errorf("delete while the store is down: status %d body %s, want 204", del.Status, del.Body)
	}
	if got := getEntry(t, owner, entry.Id); got.AttachmentCount != 0 {
		t.Errorf("entry carries %d receipts after the delete, want none", got.AttachmentCount)
	}
	if !strings.Contains(h.Logs(), "receipt") {
		t.Error("the object that could not be removed was not logged")
	}
}

// TestExpensesReceipts_AreOnEveryExpenseAndEveryPage: the list carries what a
// single read carries, so a client never has to fetch an expense again to know
// what is attached to it. An expense with none carries an empty array, never a
// missing key.
func TestExpensesReceipts_AreOnEveryExpenseAndEveryPage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	bare := createEntry(t, owner, outlayBody(nil))
	if bare.Attachments == nil || len(bare.Attachments) != 0 || bare.AttachmentCount != 0 {
		t.Errorf("a new expense carries %v, want an empty array", bare.Attachments)
	}
	raw := rawEntry(t, owner, bare.Id)
	if _, ok := raw["attachments"]; !ok {
		t.Error("attachments is absent from an expense with none, want an empty array")
	}

	entry := createEntry(t, owner, outlayBody(nil))
	one := uploadReceipt(t, owner, entry.Id, "en.pdf", "application/pdf", testPDF(8))
	two := uploadReceipt(t, owner, entry.Id, "to.pdf", "application/pdf", testPDF(16))

	page := listEntries(t, owner, "")
	for _, got := range page.Data {
		switch got.Id {
		case entry.Id:
			if got.AttachmentCount != 2 || len(got.Attachments) != 2 ||
				got.Attachments[0] != one || got.Attachments[1] != two {
				t.Errorf("listed entry carries %+v, want both receipts in the order they were uploaded", got.Attachments)
			}
		case bare.Id:
			if len(got.Attachments) != 0 {
				t.Errorf("listed entry carries %+v, want none", got.Attachments)
			}
		}
	}
}
