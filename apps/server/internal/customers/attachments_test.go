package customers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is GET/POST /customers/{id}/attachments and
// GET/DELETE /customers/{id}/attachments/{attachmentId} (customer attachments
// design D1-D6): the upload and the download, the limits and the refusals,
// the two events, the ledger, the merge moving the rows, the export and the
// anonymisation. The object store is a fake in memory (fakeObjectStore, set
// with modtest.WithObjectStore), so no test depends on a filesystem and each
// of its operations can be made to fail on demand — Staff's own harness
// shape.

// fakeObjectStore is storage.ObjectStore in memory. Put, Get and Delete can
// each be made to fail on demand: the only way to drive a store failure — a
// write that fails before the row, a read of an object that is gone, a
// delete the store refuses — deterministically.
type fakeObjectStore struct {
	mu        sync.Mutex
	objects   map[string][]byte
	putErr    error
	getErr    error
	deleteErr error
	deletes   []string
}

var _ storage.ObjectStore = (*fakeObjectStore)(nil)

func newFakeObjectStore() *fakeObjectStore {
	return &fakeObjectStore{objects: map[string][]byte{}}
}

func (s *fakeObjectStore) Put(_ context.Context, key string, r io.Reader, _ string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.putErr != nil {
		return s.putErr
	}
	s.objects[key] = data
	return nil
}

func (s *fakeObjectStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return nil, s.getErr
	}
	data, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("customers test store: %q: %w", key, storage.ErrNotExist)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *fakeObjectStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok, nil
}

func (s *fakeObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes = append(s.deletes, key)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.objects, key)
	return nil
}

func (s *fakeObjectStore) failPut(err error)    { s.mu.Lock(); defer s.mu.Unlock(); s.putErr = err }
func (s *fakeObjectStore) failGet(err error)    { s.mu.Lock(); defer s.mu.Unlock(); s.getErr = err }
func (s *fakeObjectStore) failDelete(err error) { s.mu.Lock(); defer s.mu.Unlock(); s.deleteErr = err }

// keys is every key the store holds, and deletesSoFar every key it was asked
// to delete, refused or not.
func (s *fakeObjectStore) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.objects))
	for k := range s.objects {
		out = append(out, k)
	}
	return out
}

func (s *fakeObjectStore) deletesSoFar() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deletes)
}

// newAttachmentHarness is a customers installation with a fake object store.
func newAttachmentHarness(t *testing.T, opts ...modtest.Option) (*modtest.Harness, *fakeObjectStore) {
	t.Helper()
	objects := newFakeObjectStore()
	return newHarness(t, append([]modtest.Option{modtest.WithObjectStore(objects)}, opts...)...), objects
}

// The attachment bytes every test uploads. No binary fixture is committed:
// the images come from the standard library's own encoders, and the PDF and
// the HEIC file are the magic bytes each format is sniffed by (Staff's own).
func testPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return buf.Bytes()
}

func testJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	return buf.Bytes()
}

// testPDF is the smallest thing http.DetectContentType calls a PDF, padded.
func testPDF(padding int) []byte {
	return append([]byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"), bytes.Repeat([]byte("x"), padding)...)
}

// testHEIC is an ISO base media file whose major brand is "heic".
func testHEIC() []byte {
	box := []byte{0, 0, 0, 24}
	box = append(box, []byte("ftypheic")...)
	box = append(box, 0, 0, 0, 0)
	box = append(box, []byte("mif1")...)
	return append(box, []byte("meta and the picture itself")...)
}

type attachmentEntryJSON struct {
	Id         int32  `json:"id"`
	EventType  string `json:"eventType"`
	OccurredOn string `json:"occurredOn"`
	Summary    string `json:"summary"`
	State      string `json:"state"`
}

type attachmentJSON struct {
	Id            int32                `json:"id"`
	CustomerId    int32                `json:"customerId"`
	FileName      string               `json:"fileName"`
	ContentType   string               `json:"contentType"`
	SizeBytes     int64                `json:"sizeBytes"`
	UploadedAt    time.Time            `json:"uploadedAt"`
	TimelineEntry *attachmentEntryJSON `json:"timelineEntry"`
}

type attachmentListJSON struct {
	Data []attachmentJSON `json:"data"`
}

// attachmentPayloadJSON decodes customer.attachment_added's and _removed's payload.
type attachmentPayloadJSON struct {
	CustomerId      int32  `json:"customerId"`
	AttachmentId    int32  `json:"attachmentId"`
	FileName        string `json:"fileName"`
	ContentType     string `json:"contentType"`
	SizeBytes       int64  `json:"sizeBytes"`
	TimelineEntryId *int32 `json:"timelineEntryId"`
}

// upload is one attachment upload's form: the file part and the entry field
// beside it, each sent only when set.
type upload struct {
	fileName, contentType string
	data                  []byte
	entryID               string
	noFile                bool
	twoFiles              bool
}

// form is u as multipart/form-data: its content type and its body.
func (u upload) form(t *testing.T) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if u.entryID != "" {
		if err := w.WriteField("timelineEntryId", u.entryID); err != nil {
			t.Fatalf("write the entry field: %v", err)
		}
	}
	files := 1
	if u.twoFiles {
		files = 2
	}
	if u.noFile {
		files = 0
	}
	for range files {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, u.fileName))
		if u.contentType != "" {
			header.Set("Content-Type", u.contentType)
		}
		part, err := w.CreatePart(header)
		if err != nil {
			t.Fatalf("create the file part: %v", err)
		}
		if _, err := part.Write(u.data); err != nil {
			t.Fatalf("write the file part: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the multipart writer: %v", err)
	}
	return w.FormDataContentType(), buf.Bytes()
}

func pdfUpload(name string) upload {
	return upload{fileName: name, contentType: "application/pdf", data: testPDF(32)}
}

func attachmentsPath(customerID int32) string {
	return fmt.Sprintf("/api/v1/customers/%d/attachments", customerID)
}

func attachmentPath(customerID, id int32) string {
	return fmt.Sprintf("/api/v1/customers/%d/attachments/%d", customerID, id)
}

// postAttachment sends one upload and answers the whole response.
func postAttachment(t *testing.T, c *modtest.Client, customerID int32, u upload) *modtest.Response {
	t.Helper()
	ct, body := u.form(t)
	return c.Do(http.MethodPost, attachmentsPath(customerID), nil, modtest.RawBody(ct, body))
}

// uploadAttachment attaches one file and fails the test unless it was created.
func uploadAttachment(t *testing.T, c *modtest.Client, customerID int32, u upload) attachmentJSON {
	t.Helper()
	r := postAttachment(t, c, customerID, u)
	if r.Status != http.StatusCreated {
		t.Fatalf("upload %s for customer %d: status %d body %s, want 201", u.fileName, customerID, r.Status, r.Body)
	}
	var out attachmentJSON
	r.JSON(&out)
	return out
}

func listAttachments(t *testing.T, c *modtest.Client, customerID int32) []attachmentJSON {
	t.Helper()
	r := c.Do(http.MethodGet, attachmentsPath(customerID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list customer %d's attachments: status %d body %s, want 200", customerID, r.Status, r.Body)
	}
	var list attachmentListJSON
	r.JSON(&list)
	return list.Data
}

func fieldErrors(t *testing.T, r *modtest.Response) map[string][]string {
	t.Helper()
	var problem validationProblemJSON
	r.JSON(&problem)
	return problem.Errors
}

// objectKeyOf is the key an attachment's row points at, which the response
// never carries.
func objectKeyOf(t *testing.T, h *modtest.Harness, id int32) string {
	t.Helper()
	return modtest.One[string](t, h, `SELECT object_key FROM customers.customer_attachments WHERE id = $1`, id)
}

// scopedKey is the physical key the fake store holds for a relative one.
func scopedKey(relative string) string { return "customers/" + relative }

// TestCustomerAttachments_UploadListDownloadDelete is the happy path of
// design D1-D4: each of the four types is taken, stored under
// attachments/<uuid> in the customers scope, listed newest first with its
// entry, streamed back with its headers, and removed with its object and its
// ledger row — each step recorded on the timeline with the caller as actor,
// and no revision of the entry a file went on.
func TestCustomerAttachments_UploadListDownloadDelete(t *testing.T) {
	t.Parallel()
	h, objects := newAttachmentHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	customer := createCustomer(t, c, "Acme AS")
	entry := createManual(t, c, customer.Id, "2026-07-27", "Signed the frame agreement")

	contract := uploadAttachment(t, c, customer.Id, upload{fileName: "Rammeavtale – Acme.pdf", contentType: "application/pdf", data: testPDF(64)})
	if contract.CustomerId != customer.Id || contract.FileName != "Rammeavtale – Acme.pdf" || contract.ContentType != "application/pdf" ||
		contract.SizeBytes != int64(len(testPDF(64))) || contract.TimelineEntry != nil || !contract.UploadedAt.Equal(h.Now()) {
		t.Errorf("created = %+v, want the file's own fields and no entry", contract)
	}
	key := objectKeyOf(t, h, contract.Id)
	if !strings.HasPrefix(key, "attachments/") || strings.Contains(key, "Acme") {
		t.Errorf("object key = %q, want attachments/<uuid>, naming neither the file nor the customer", key)
	}
	if _, err := uuid.Parse(strings.TrimPrefix(key, "attachments/")); err != nil {
		t.Errorf("object key %q is not attachments/<uuid>: %v", key, err)
	}
	if got, err := objects.Get(context.Background(), scopedKey(key)); err != nil {
		t.Errorf("the object is not in the customers scope: %v", err)
	} else {
		_ = got.Close()
	}

	h.Advance(time.Minute)
	jpegData := testJPEG(t)
	photo := uploadAttachment(t, c, customer.Id, upload{fileName: "signering.jpg", contentType: "image/jpeg", data: jpegData, entryID: strconv.Itoa(int(entry.Id))})
	if photo.TimelineEntry == nil || photo.TimelineEntry.Id != entry.Id || photo.TimelineEntry.EventType != "note" ||
		photo.TimelineEntry.OccurredOn != "2026-07-27" || photo.TimelineEntry.State != "active" || photo.TimelineEntry.Summary != "Signed the frame agreement" {
		t.Errorf("created on an entry = %+v, want the entry named", photo.TimelineEntry)
	}
	h.Advance(time.Minute)
	uploadAttachment(t, c, customer.Id, upload{fileName: "skjerm.png", contentType: "image/png", data: testPNG(t)})
	h.Advance(time.Minute)
	heic := uploadAttachment(t, c, customer.Id, upload{fileName: "bilde.heic", contentType: "image/heic", data: testHEIC()})

	list := listAttachments(t, c, customer.Id)
	if len(list) != 4 || list[0].Id != heic.Id || list[1].FileName != "skjerm.png" || list[2].Id != photo.Id || list[3].Id != contract.Id {
		t.Errorf("list = %+v, want the four newest first", list)
	}
	if list[2].TimelineEntry == nil || list[2].TimelineEntry.Id != entry.Id {
		t.Errorf("the listed photo names no entry: %+v", list[2])
	}

	// The download: the bytes, the stored type, the disposition and the
	// headers — on the 404 as on the bytes.
	r := c.Do(http.MethodGet, attachmentPath(customer.Id, photo.Id), nil)
	if r.Status != http.StatusOK || !bytes.Equal(r.Body, jpegData) {
		t.Fatalf("download: status %d, %d bytes, want 200 with the JPEG", r.Status, len(r.Body))
	}
	if r.Header("Content-Type") != "image/jpeg" || r.Header("Content-Disposition") != "inline; filename*=UTF-8''signering.jpg" ||
		r.Header("Cache-Control") != "private, no-store" || r.Header("X-Content-Type-Options") != "nosniff" ||
		r.Header("Content-Security-Policy") != "default-src 'none'; sandbox" {
		t.Errorf("download headers = type %q disposition %q cache %q sniff %q csp %q", r.Header("Content-Type"),
			r.Header("Content-Disposition"), r.Header("Cache-Control"), r.Header("X-Content-Type-Options"), r.Header("Content-Security-Policy"))
	}
	if r := c.Do(http.MethodGet, attachmentPath(customer.Id, contract.Id), nil); r.Header("Content-Disposition") != "inline; filename*=UTF-8''Rammeavtale%20%E2%80%93%20Acme.pdf" {
		t.Errorf("a Norwegian name's disposition = %q, want it RFC 5987 encoded", r.Header("Content-Disposition"))
	}
	if r := c.Do(http.MethodGet, attachmentPath(customer.Id, heic.Id), nil); r.Header("Content-Disposition") != "attachment; filename*=UTF-8''bilde.heic" {
		t.Errorf("a HEIC's disposition = %q, want a file to save", r.Header("Content-Disposition"))
	}
	other := createCustomer(t, c, "Other AS")
	if r := c.Do(http.MethodGet, attachmentPath(other.Id, photo.Id), nil); r.Status != http.StatusNotFound || r.Header("Cache-Control") != "private, no-store" {
		t.Errorf("another customer's id: status %d cache %q, want the bare 404 with the headers", r.Status, r.Header("Cache-Control"))
	}

	// The events: added with the actor and the payload, and the entry's
	// revision unchanged by the file put on it.
	added := entriesOfType(timelineOf(t, c, customer.Id), "customer.attachment_added")
	if len(added) != 4 {
		t.Fatalf("attachment_added events = %d, want 4", len(added))
	}
	var payload attachmentPayloadJSON
	photoEvent := added[2]
	if err := json.Unmarshal(photoEvent.Payload, &payload); err != nil || payload.AttachmentId != photo.Id || payload.FileName != "signering.jpg" ||
		payload.ContentType != "image/jpeg" || payload.SizeBytes != int64(len(jpegData)) || payload.TimelineEntryId == nil || *payload.TimelineEntryId != entry.Id ||
		payload.CustomerId != customer.Id {
		t.Errorf("attachment_added payload = %s (%v), want the photo on the entry", photoEvent.Payload, err)
	}
	if str(photoEvent.Summary) != "Attachment added: signering.jpg" || photoEvent.ActorKind != "user" || str(photoEvent.ActorDisplay) != userDisplayName(t, h, userID) ||
		photoEvent.Provenance != "generated" || photoEvent.Producer != "customers.api" {
		t.Errorf("attachment_added = %+v, want the summary and the caller as actor", photoEvent)
	}
	if entryNow := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customer.Id, entry.Id), nil); entryNow.Status != http.StatusOK ||
		!strings.Contains(string(entryNow.Body), `"currentRevision":1`) {
		t.Errorf("the entry after an attachment: status %d body %s, want revision 1 still", entryNow.Status, entryNow.Body)
	}

	// The delete: the row, the object and the ledger row all gone, and the
	// event with the file's name.
	if r := c.Do(http.MethodDelete, attachmentPath(customer.Id, photo.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s, want 204", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_attachments WHERE id = $1`, photo.Id); n != 0 {
		t.Errorf("after the delete: %d rows, want none", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.object_deletions`); n != 0 {
		t.Errorf("after the delete: %d ledger rows, want none — the store confirmed", n)
	}
	if n := len(objects.keys()); n != 3 || objects.deletesSoFar() != 1 {
		t.Errorf("after the delete the store holds %d objects after %d deletes, want 3 after 1", n, objects.deletesSoFar())
	}
	removed := entriesOfType(timelineOf(t, c, customer.Id), "customer.attachment_removed")
	if len(removed) != 1 || str(removed[0].Summary) != "Attachment removed: signering.jpg" || removed[0].ActorKind != "user" {
		t.Errorf("attachment_removed = %+v, want one with the file's name by the caller", removed)
	}
	if err := json.Unmarshal(removed[0].Payload, &payload); err != nil || payload.AttachmentId != photo.Id || payload.TimelineEntryId == nil {
		t.Errorf("attachment_removed payload = %s, want the photo on the entry", removed[0].Payload)
	}
	if list := listAttachments(t, c, customer.Id); len(list) != 3 {
		t.Errorf("after the delete the list has %d, want 3", len(list))
	}
	if r := c.Do(http.MethodDelete, attachmentPath(customer.Id, photo.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("delete again: status %d, want 404", r.Status)
	}
	if r := c.Do(http.MethodGet, attachmentsPath(987654), nil); r.Status != http.StatusNotFound {
		t.Errorf("list of an unknown customer: status %d, want 404", r.Status)
	}
}

// TestCustomerAttachments_RefusesWhatTheFileIsNot is design D2's file rule:
// the bytes decide, a name or a declared type may only disagree, and the
// bound is ten mebibytes exactly. Every refusal is a 400 on file under one
// title, before any object is written.
func TestCustomerAttachments_RefusesWhatTheFileIsNot(t *testing.T) {
	t.Parallel()
	h, objects := newAttachmentHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Acme AS")

	cases := []struct {
		name string
		u    upload
		want string
	}{
		{"a PNG named .pdf", upload{fileName: "avtale.pdf", contentType: "image/png", data: testPNG(t)}, "must match the type and file name"},
		{"a PDF declared image/png", upload{fileName: "avtale.pdf", contentType: "image/png", data: testPDF(8)}, "must match the type and file name"},
		{"a text file", upload{fileName: "notat.txt", contentType: "text/plain", data: []byte("hei")}, "must be a PDF, JPEG, PNG or HEIC"},
		{"an empty file", upload{fileName: "tom.pdf", contentType: "application/pdf"}, "file is required"},
		{"no file part", upload{noFile: true}, "file is required"},
		{"two files", upload{fileName: "a.pdf", contentType: "application/pdf", data: testPDF(8), twoFiles: true}, "one file"},
		{"one byte over", upload{fileName: "stor.pdf", contentType: "application/pdf", data: testPDF(10*1024*1024 - len(testPDF(0)) + 1)}, "at most 10 MiB"},
	}
	for _, tc := range cases {
		r := postAttachment(t, c, customer.Id, tc.u)
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d body %s, want 400", tc.name, r.Status, r.Body)
			continue
		}
		errs := fieldErrors(t, r)
		if msgs := errs["file"]; len(msgs) != 1 || !strings.Contains(msgs[0], tc.want) {
			t.Errorf("%s: errors = %v, want one on file saying %q", tc.name, errs, tc.want)
		}
	}
	if n := len(objects.keys()); n != 0 {
		t.Errorf("after the refusals the store holds %d objects, want none", n)
	}

	// Exactly the bound, and a phone's octet-stream and a name without an
	// extension, are taken.
	exact := testPDF(10*1024*1024 - len(testPDF(0)))
	if got := uploadAttachment(t, c, customer.Id, upload{fileName: "stor.pdf", contentType: "application/pdf", data: exact}); got.SizeBytes != 10*1024*1024 {
		t.Errorf("a ten-mebibyte file: size %d, want 10485760", got.SizeBytes)
	}
	if got := uploadAttachment(t, c, customer.Id, upload{fileName: "IMG_0001", contentType: "application/octet-stream", data: testHEIC()}); got.ContentType != "image/heic" {
		t.Errorf("an octet-stream HEIC without an extension: type %q, want image/heic", got.ContentType)
	}
	if got := uploadAttachment(t, c, customer.Id, upload{fileName: `C:\Users\kari\..\Faktura nr. 12345`, contentType: "", data: testPDF(8)}); got.FileName != "Faktura nr. 12345" {
		t.Errorf("a name with a path: %q, want the last segment", got.FileName)
	}
	if got := uploadAttachment(t, c, customer.Id, upload{fileName: "   ", contentType: "application/pdf", data: testPDF(8)}); got.FileName != "attachment" {
		t.Errorf("a blank name: %q, want the fallback", got.FileName)
	}
	if r := postAttachment(t, c, customer.Id, upload{fileName: "a.pdf", contentType: "application/pdf", data: testPDF(8), entryID: "abc"}); r.Status != http.StatusBadRequest ||
		len(fieldErrors(t, r)["timelineEntryId"]) != 1 {
		t.Errorf("a non-numeric entry id: status %d body %s, want 400 on timelineEntryId", r.Status, r.Body)
	}
	if r := postAttachment(t, c, 987654, pdfUpload("a.pdf")); r.Status != http.StatusNotFound {
		t.Errorf("an unknown customer: status %d, want 404", r.Status)
	}
}

// TestCustomerAttachments_OnlyAManualActiveEntryTakesOne is design D2's link
// rule, judged under the lock: a generated entry, a deleted entry, another
// customer's entry and an unknown id are each a field error on
// timelineEntryId, and the object written before the transaction is removed
// again.
func TestCustomerAttachments_OnlyAManualActiveEntryTakesOne(t *testing.T) {
	t.Parallel()
	h, objects := newAttachmentHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Acme AS")
	other := createCustomer(t, c, "Other AS")
	generated := entriesOfType(timelineOf(t, c, customer.Id), "customer.created")[0]
	deleted := createManual(t, c, customer.Id, "2026-07-27", "Gone")
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/timeline/%d?expectedRevision=1", customer.Id, deleted.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete the entry: status %d body %s", r.Status, r.Body)
	}
	theirs := createManual(t, c, other.Id, "2026-07-27", "Theirs")

	for name, id := range map[string]int32{"a generated entry": generated.Id, "a deleted entry": deleted.Id, "another customer's entry": theirs.Id, "an unknown entry": 987654} {
		r := postAttachment(t, c, customer.Id, upload{fileName: "a.pdf", contentType: "application/pdf", data: testPDF(8), entryID: strconv.Itoa(int(id))})
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d body %s, want 400", name, r.Status, r.Body)
			continue
		}
		if msgs := fieldErrors(t, r)["timelineEntryId"]; len(msgs) != 1 || !strings.Contains(msgs[0], "manual, active timeline entry") {
			t.Errorf("%s: errors = %v, want one on timelineEntryId", name, fieldErrors(t, r))
		}
	}
	if n, d := len(objects.keys()), objects.deletesSoFar(); n != 0 || d != 4 {
		t.Errorf("after the refusals the store holds %d objects after %d deletes, want none after 4 compensations", n, d)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_attachments`); n != 0 {
		t.Errorf("rows after the refusals = %d, want none", n)
	}

	// A deleted entry keeps the attachments it had.
	entry := createManual(t, c, customer.Id, "2026-07-28", "Kept")
	kept := uploadAttachment(t, c, customer.Id, upload{fileName: "a.pdf", contentType: "application/pdf", data: testPDF(8), entryID: strconv.Itoa(int(entry.Id))})
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/timeline/%d?expectedRevision=1", customer.Id, entry.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete the entry: status %d body %s", r.Status, r.Body)
	}
	list := listAttachments(t, c, customer.Id)
	if len(list) != 1 || list[0].Id != kept.Id || list[0].TimelineEntry == nil || list[0].TimelineEntry.State != "deleted" {
		t.Errorf("after the entry's delete the list = %+v, want the attachment still naming the deleted entry", list)
	}
}

// TestCustomerAttachments_TheCap is design D2's two hundred, counted on the
// customer whatever entry the files are on.
func TestCustomerAttachments_TheCap(t *testing.T) {
	t.Parallel()
	h, objects := newAttachmentHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Acme AS")
	entry := createManual(t, c, customer.Id, "2026-07-27", "Many")
	h.Exec(t, `INSERT INTO customers.customer_attachments (customer_id, timeline_entry_id, object_key, file_name, content_type, size_bytes, created_at)
	           SELECT $1, CASE WHEN n % 2 = 0 THEN $2::int END, 'attachments/' || gen_random_uuid(), 'f' || n || '.pdf', 'application/pdf', 8, $3
	           FROM generate_series(1, 199) n`, customer.Id, entry.Id, h.Now())
	last := uploadAttachment(t, c, customer.Id, pdfUpload("200.pdf"))
	r := postAttachment(t, c, customer.Id, pdfUpload("201.pdf"))
	refusedWith(t, r, "attachment_count_exceeded")
	if n := len(objects.keys()); n != 1 {
		t.Errorf("after the refusal the store holds %d objects, want the 200th alone", n)
	}
	if r := c.Do(http.MethodDelete, attachmentPath(customer.Id, last.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s", r.Status, r.Body)
	}
	uploadAttachment(t, c, customer.Id, pdfUpload("201.pdf"))
}

// TestCustomerAttachments_AStoreThatFails is the 503s: a Put that fails
// records nothing, and a Get that fails is a fault to be seen, not a 404.
func TestCustomerAttachments_AStoreThatFails(t *testing.T) {
	t.Parallel()
	h, objects := newAttachmentHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Acme AS")
	one := uploadAttachment(t, c, customer.Id, pdfUpload("one.pdf"))

	objects.failPut(errors.New("disk full"))
	r := postAttachment(t, c, customer.Id, pdfUpload("two.pdf"))
	if r.Status != http.StatusServiceUnavailable || !strings.Contains(string(r.Body), "Attachment storage is unavailable") {
		t.Errorf("a failing Put: status %d body %s, want 503", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_attachments WHERE customer_id = $1`, customer.Id); n != 1 {
		t.Errorf("rows after a failed Put = %d, want the one from before", n)
	}
	if added := entriesOfType(timelineOf(t, c, customer.Id), "customer.attachment_added"); len(added) != 1 {
		t.Errorf("attachment_added events after a failed Put = %d, want the one from before", len(added))
	}
	objects.failPut(nil)

	objects.failGet(errors.New("bucket gone"))
	r = c.Do(http.MethodGet, attachmentPath(customer.Id, one.Id), nil)
	if r.Status != http.StatusServiceUnavailable || r.Header("Cache-Control") != "private, no-store" || r.Header("X-Content-Type-Options") != "nosniff" {
		t.Errorf("a failing Get: status %d cache %q sniff %q, want 503 with the headers", r.Status, r.Header("Cache-Control"), r.Header("X-Content-Type-Options"))
	}
}

// TestCustomerAttachments_ADeleteTheStoreRefusesLeavesTheLedgerRow is
// design D6's ledger: the row goes whatever the store says, the refused
// object stays on the ledger with its attempt recorded and the next due
// after the backoff, and the worker's pass finishes it once the store
// answers again.
func TestCustomerAttachments_ADeleteTheStoreRefusesLeavesTheLedgerRow(t *testing.T) {
	t.Parallel()
	h, objects := newAttachmentHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Acme AS")
	a := uploadAttachment(t, c, customer.Id, pdfUpload("a.pdf"))
	key := objectKeyOf(t, h, a.Id)

	objects.failDelete(errors.New("store down"))
	if r := c.Do(http.MethodDelete, attachmentPath(customer.Id, a.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s, want 204 — the row is gone whatever the store says", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_attachments WHERE id = $1`, a.Id); n != 0 {
		t.Errorf("rows after the delete = %d, want none", n)
	}
	if n := len(objects.keys()); n != 1 {
		t.Errorf("the store holds %d objects, want the refused one still", n)
	}
	var attempts int32
	var lastError *string
	var next time.Time
	if err := h.Pool().QueryRow(context.Background(), `SELECT attempts, last_error, next_attempt_at FROM customers.object_deletions WHERE object_key = $1`, key).
		Scan(&attempts, &lastError, &next); err != nil {
		t.Fatalf("read the ledger row: %v", err)
	}
	if attempts != 1 || lastError == nil || *lastError != "the object store refused the delete" || !next.Equal(h.Now().Add(2*time.Second)) {
		t.Errorf("ledger row = attempts %d, last_error %v, next %s, want 1, the reason, now + 2s", attempts, str(lastError), next)
	}

	// Not due yet: the pass claims nothing. Then due, and the store answers.
	worker := customers.NewAnonymisationWorker(h.Deps())
	if attempted, _, err := worker.RunLedger(context.Background()); err != nil || attempted != 0 {
		t.Errorf("a pass before the backoff: attempted %d, %v; want 0", attempted, err)
	}
	objects.failDelete(nil)
	h.Advance(3 * time.Second)
	if attempted, succeeded, err := worker.RunLedger(context.Background()); err != nil || attempted != 1 || succeeded != 1 {
		t.Errorf("a pass after the backoff: attempted %d, succeeded %d, %v; want 1, 1", attempted, succeeded, err)
	}
	if n := len(objects.keys()); n != 0 {
		t.Errorf("after the pass the store holds %d objects, want none", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.object_deletions`); n != 0 {
		t.Errorf("after the pass %d ledger rows, want none", n)
	}
}

// TestCustomerAttachments_AReadOnlyCustomerKeepsItsFiles: a merged-away and
// an anonymised customer each refuse the upload and the delete as every
// write, and still list and serve what they have.
func TestCustomerAttachments_AReadOnlyCustomerKeepsItsFiles(t *testing.T) {
	t.Parallel()
	h, _ := newAttachmentHarness(t)
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS")
	duplicate := createCustomer(t, c, "Acme Norge AS")
	mergeOK(t, c, survivor.Id, duplicate.Id)
	anonymised := createCustomerOfType(t, c, "Kari Nordmann", "person")
	kept := uploadAttachment(t, c, anonymised.Id, pdfUpload("avtale.pdf"))
	markAnonymised(t, h, anonymised.Id)

	refusedWith(t, postAttachment(t, c, duplicate.Id, pdfUpload("a.pdf")), "customer_merged")
	refusedWith(t, c.Do(http.MethodDelete, attachmentPath(duplicate.Id, 987654), nil), "customer_merged")
	if list := listAttachments(t, c, duplicate.Id); len(list) != 0 {
		t.Errorf("a merged-away customer lists %d, want none — they are the survivor's", len(list))
	}
	refusedWith(t, postAttachment(t, c, anonymised.Id, pdfUpload("a.pdf")), "customer_anonymised")
	refusedWith(t, c.Do(http.MethodDelete, attachmentPath(anonymised.Id, kept.Id), nil), "customer_anonymised")
	if list := listAttachments(t, c, anonymised.Id); len(list) != 1 || list[0].Id != kept.Id {
		t.Errorf("an anonymised customer lists %+v, want what it has", list)
	}
	if r := c.Do(http.MethodGet, attachmentPath(anonymised.Id, kept.Id), nil); r.Status != http.StatusOK {
		t.Errorf("download on an anonymised customer: status %d, want 200", r.Status)
	}
}

// TestCustomerAttachments_RideOnTheTimelineKeys is design D3's gate: the two
// writes want customers:timeline-manage with customers:timeline-view, the two
// reads customers:timeline-view, and customers:view alone opens nothing.
func TestCustomerAttachments_RideOnTheTimelineKeys(t *testing.T) {
	t.Parallel()
	h, _ := newAttachmentHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Acme AS")
	a := uploadAttachment(t, c, customer.Id, pdfUpload("avtale.pdf"))

	reader := h.SignIn(t, "customers:view", "customers:timeline-view")
	if r := postAttachment(t, reader, customer.Id, pdfUpload("b.pdf")); r.Status != http.StatusForbidden {
		t.Errorf("upload without timeline-manage: status %d, want 403", r.Status)
	}
	if r := reader.Do(http.MethodDelete, attachmentPath(customer.Id, a.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("delete without timeline-manage: status %d, want 403", r.Status)
	}
	if r := reader.Do(http.MethodGet, attachmentsPath(customer.Id), nil); r.Status != http.StatusOK {
		t.Errorf("list with timeline-view: status %d, want 200", r.Status)
	}
	if r := reader.Do(http.MethodGet, attachmentPath(customer.Id, a.Id), nil); r.Status != http.StatusOK {
		t.Errorf("download with timeline-view: status %d, want 200", r.Status)
	}
	viewer := h.SignIn(t, "customers:view", "customers:update", "customers:timeline-manage")
	if r := viewer.Do(http.MethodGet, attachmentsPath(customer.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("list with customers:view alone: status %d, want 403", r.Status)
	}
	if r := viewer.Do(http.MethodGet, attachmentPath(customer.Id, a.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("download with customers:view alone: status %d, want 403", r.Status)
	}
	if r := postAttachment(t, viewer, customer.Id, pdfUpload("b.pdf")); r.Status != http.StatusForbidden {
		t.Errorf("upload with timeline-manage but not timeline-view: status %d, want 403", r.Status)
	}
}

// TestCustomerAttachments_UploadsAreRateLimited: two hundred attachments
// bound a customer and nothing bounds how many customers there are, so the
// upload carries the platform's rate limit, keyed by client address.
func TestCustomerAttachments_UploadsAreRateLimited(t *testing.T) {
	t.Parallel()
	h, _ := newAttachmentHarness(t)
	one := authenticatedClient(t, h)
	other := authenticatedClient(t, h)

	// Refused uploads count too — the limit is in front of the handler — so
	// this costs the suite no storage and no rows.
	const unknown = int32(987654)
	for i := range customers.AttachmentUploadsPerHour {
		if r := postAttachment(t, one, unknown, pdfUpload("a.pdf")); r.Status != http.StatusNotFound {
			t.Fatalf("upload %d: status %d body %s, want the unknown customer's 404", i+1, r.Status, r.Body)
		}
	}
	over := postAttachment(t, one, unknown, pdfUpload("a.pdf"))
	if over.Status != http.StatusTooManyRequests || over.Code() != "rate_limited" || over.Header("Retry-After") == "" {
		t.Errorf("upload %d: status %d code %q Retry-After %q, want 429 rate_limited with a Retry-After",
			customers.AttachmentUploadsPerHour+1, over.Status, over.Code(), over.Header("Retry-After"))
	}
	if r := postAttachment(t, other, unknown, pdfUpload("a.pdf")); r.Status != http.StatusNotFound {
		t.Errorf("another client after the first was limited: status %d, want 404", r.Status)
	}
}

// TestCustomerAttachments_MoveWithAMerge is design D5: every attachment of
// the absorbed customer is the survivor's afterwards, one on an entry still
// naming its entry, counted as customers.attachments in the answer and the
// event, and nothing in the store was touched.
func TestCustomerAttachments_MoveWithAMerge(t *testing.T) {
	t.Parallel()
	h, objects := newAttachmentHarness(t)
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS")
	duplicate := createCustomer(t, c, "Acme Norge AS")
	own := uploadAttachment(t, c, survivor.Id, pdfUpload("own.pdf"))
	entry := createManual(t, c, duplicate.Id, "2026-07-27", "Theirs")
	loose := uploadAttachment(t, c, duplicate.Id, pdfUpload("loose.pdf"))
	onEntry := uploadAttachment(t, c, duplicate.Id, upload{fileName: "on-entry.pdf", contentType: "application/pdf", data: testPDF(8), entryID: strconv.Itoa(int(entry.Id))})

	result := mergeOK(t, c, survivor.Id, duplicate.Id)
	if got := movedCount(result, "customers.attachments"); got != 2 {
		t.Errorf("moved customers.attachments = %d, want 2", got)
	}
	list := listAttachments(t, c, survivor.Id)
	ids := map[int32]*attachmentEntryJSON{}
	for _, a := range list {
		ids[a.Id] = a.TimelineEntry
	}
	if len(list) != 3 || ids[own.Id] != nil || ids[loose.Id] != nil || ids[onEntry.Id] == nil || ids[onEntry.Id].Id != entry.Id {
		t.Errorf("the survivor's list = %+v, want its own and both of the duplicate's, the one on the entry still naming it", list)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE id = $1 AND customer_id = $2`, entry.Id, survivor.Id); n != 1 {
		t.Errorf("the entry did not move with its attachment")
	}
	if objects.deletesSoFar() != 0 || len(objects.keys()) != 3 {
		t.Errorf("the merge touched the store: %d deletes, %d objects, want 0 and 3", objects.deletesSoFar(), len(objects.keys()))
	}
	merged := entriesOfType(timelineOf(t, c, survivor.Id), "customer.merged")
	if len(merged) != 1 || !strings.Contains(str(merged[0].Summary), "2 attachments") {
		t.Errorf("customer.merged summary = %q, want it to count the attachments", str(merged[0].Summary))
	}
	if r := c.Do(http.MethodGet, attachmentPath(survivor.Id, loose.Id), nil); r.Status != http.StatusOK {
		t.Errorf("download of a moved attachment: status %d, want 200", r.Status)
	}
}

// TestCustomerAttachments_ExportedAndErased is design D6: the export names
// the attachments; the anonymisation deletes every one, queues their objects,
// counts them in customer.anonymised, takes the file names out of the two
// events, and the worker's ledger pass removes the objects.
func TestCustomerAttachments_ExportedAndErased(t *testing.T) {
	t.Parallel()
	h, objects := newAttachmentHarness(t)
	c := mergeClient(t, h)
	pd := personalDataClient(t, h)
	person := createCustomerOfType(t, c, "Kari Nordmann", "person")
	entry := createManual(t, c, person.Id, "2026-07-27", "Kari sendte kontrakten")
	contract := uploadAttachment(t, c, person.Id, pdfUpload("Kari Nordmann – kontrakt.pdf"))
	mail := uploadAttachment(t, c, person.Id, upload{fileName: "epost.pdf", contentType: "application/pdf", data: testPDF(8), entryID: strconv.Itoa(int(entry.Id))})
	if r := c.Do(http.MethodDelete, attachmentPath(person.Id, mail.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s", r.Status, r.Body)
	}
	mail = uploadAttachment(t, c, person.Id, upload{fileName: "epost.pdf", contentType: "application/pdf", data: testPDF(8), entryID: strconv.Itoa(int(entry.Id))})

	r := getPersonalData(t, pd, person.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("export: status %d body %s", r.Status, r.Body)
	}
	var file struct {
		Customer struct {
			Attachments []attachmentJSON `json:"attachments"`
		} `json:"customer"`
	}
	r.JSON(&file)
	if got := file.Customer.Attachments; len(got) != 2 || got[0].Id != contract.Id || got[1].Id != mail.Id || got[1].TimelineEntry == nil || got[1].TimelineEntry.Id != entry.Id {
		t.Errorf("exported attachments = %+v, want both, oldest first, the second on its entry", got)
	}

	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", person.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s", r.Status, r.Body)
	}
	scheduleOn(t, h, person.Id, day(h, 0))
	runAnonymisation(t, h)

	if n := h.Count(t, `SELECT count(*) FROM customers.customer_attachments WHERE customer_id = $1`, person.Id); n != 0 {
		t.Errorf("attachments after the anonymisation = %d, want none", n)
	}
	entries := timelineOf(t, c, person.Id)
	var payload struct {
		Erased []struct {
			Kind  string `json:"kind"`
			Count int64  `json:"count"`
		} `json:"erased"`
	}
	last := entriesOfType(entries, "customer.anonymised")[0]
	if err := json.Unmarshal(last.Payload, &payload); err != nil {
		t.Fatalf("decode customer.anonymised: %v", err)
	}
	var attachmentsErased int64 = -1
	for _, e := range payload.Erased {
		if e.Kind == "customers.attachments" {
			attachmentsErased = e.Count
		}
	}
	if attachmentsErased != 2 {
		t.Errorf("customer.anonymised erased customers.attachments = %d, want 2 (payload %s)", attachmentsErased, last.Payload)
	}
	for _, kind := range []string{"customer.attachment_added", "customer.attachment_removed"} {
		for _, e := range entriesOfType(entries, kind) {
			var p attachmentPayloadJSON
			if err := json.Unmarshal(e.Payload, &p); err != nil || p.FileName != "[anonymised]" || str(e.Summary) != "[anonymised]" || p.AttachmentId == 0 {
				t.Errorf("%s after the anonymisation: summary %q payload %s, want the file name taken out and the id kept", kind, str(e.Summary), e.Payload)
			}
		}
	}
	// The worker's pass finishes the objects: RunCycle is the cycle alone,
	// and the ledger is Run's second step, asked for here as Run would.
	if n := h.Count(t, `SELECT count(*) FROM customers.object_deletions`); n != 2 {
		t.Errorf("ledger rows after the anonymisation = %d, want 2", n)
	}
	if _, succeeded, err := customers.NewAnonymisationWorker(h.Deps()).RunLedger(context.Background()); err != nil || succeeded != 2 {
		t.Errorf("the ledger pass: succeeded %d, %v; want 2", succeeded, err)
	}
	if n := len(objects.keys()); n != 0 {
		t.Errorf("after the pass the store holds %d objects, want none", n)
	}
	if list := listAttachments(t, c, person.Id); len(list) != 0 {
		t.Errorf("an anonymised customer lists %d attachments, want none", len(list))
	}
}
