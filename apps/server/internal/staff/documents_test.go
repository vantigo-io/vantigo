package staff_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// listDocuments reads a person's archive and fails the test unless it
// answered 200.
func listDocuments(t *testing.T, c *modtest.Client, personID int64) []gen.StaffDocument {
	t.Helper()
	var out []gen.StaffDocument
	do(t, c, http.MethodGet, documentsPath(personID), nil, http.StatusOK, &out)
	return out
}

// categoryKeys is the documents' categories' keys, in the order listed.
func categoryKeys(docs []gen.StaffDocument) []string {
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.Category.Key)
	}
	return out
}

// storedDocument is a document's row as the database holds it.
func storedDocument(t *testing.T, h *harness, id int64) (key string, employmentID *int64) {
	t.Helper()
	if err := h.Pool().QueryRow(context.Background(),
		`SELECT object_key, employment_id FROM staff.documents WHERE id = $1`, id).Scan(&key, &employmentID); err != nil {
		t.Fatalf("read document %d: %v", id, err)
	}
	return key, employmentID
}

// A PDF, a JPEG, a PNG and a HEIC are filed, each stored as its own bytes say
// under people/<personId>/<uuid> — never a key the client chose — with the
// category embedded, and served back byte for byte: in place, but a HEIC as a
// file to save.
func TestDocuments_EachTypeIsFiledAndServedBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	person := seedPerson(t, h, "Kari", "Nordmann")
	other := documentCategoryID(t, h, "other")

	for _, tc := range []struct {
		name, fileName, declared, stored, disposition string
		data                                          []byte
	}{
		{"a scanned contract", "avtale.pdf", "application/pdf", "application/pdf", "inline", testPDF(64)},
		{"a photograph", "attest.jpg", "image/jpeg", "image/jpeg", "inline", testJPEG(t)},
		{"a screenshot", "kurs.png", "image/png", "image/png", "inline", testPNG(t)},
		{"an iPhone photograph", "IMG_0042.HEIC", "image/heic", "image/heic", "attachment", testHEIC()},
		{"one sent with no type at all", "IMG_0043.heic", "", "image/heic", "attachment", testHEIC()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := uploadDocument(t, hr, person, upload{fileName: tc.fileName, contentType: tc.declared, data: tc.data, categoryID: other})
			if got.FileName != tc.fileName || got.ContentType != tc.stored || got.SizeBytes != int64(len(tc.data)) ||
				got.PersonId != person || got.Category.Key != "other" || got.Category.Class != gen.StaffDocumentClassD ||
				got.Category.Restricted || got.Category.Names.En != "Other" || got.Category.Names.Nb != "Annet" || got.Revision != 1 {
				t.Errorf("filed = %+v, want %s as %s of %d bytes under other", got, tc.fileName, tc.stored, len(tc.data))
			}
			key, _ := storedDocument(t, h, got.Id)
			prefix := fmt.Sprintf("people/%d/", person)
			if !strings.HasPrefix(key, prefix) || uuid.Validate(strings.TrimPrefix(key, prefix)) != nil {
				t.Errorf("object key = %q, want %s<uuid>", key, prefix)
			}
			if !h.objects.has(key) {
				t.Errorf("the store has no object at %q", key)
			}

			r := hr.Do(http.MethodGet, documentPath(got.Id), nil)
			if r.Status != http.StatusOK || !bytes.Equal(r.Body, tc.data) {
				t.Fatalf("download: status %d, %d bytes, want 200 and the bytes uploaded", r.Status, len(r.Body))
			}
			for header, want := range map[string]string{
				"Content-Type":            tc.stored,
				"Cache-Control":           "private, no-store",
				"X-Content-Type-Options":  "nosniff",
				"Content-Security-Policy": "default-src 'none'; sandbox",
				"Content-Disposition":     tc.disposition + "; filename*=UTF-8''" + tc.fileName,
			} {
				if got := r.Header(header); got != want {
					t.Errorf("%s = %q, want %q", header, got, want)
				}
			}
		})
	}
}

// The file is judged by its own bytes and must agree with what it is called:
// a PNG named .pdf, a PDF declared as a PNG and a text file are
// document_type_unsupported, a file over 10 MiB document_too_large, a missing
// or empty one a plain field error — and nothing is written to the store.
func TestDocuments_TheFileIsJudgedByItsBytes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	person := seedPerson(t, h, "Kari", "Nordmann")
	other := documentCategoryID(t, h, "other")

	for _, tc := range []struct {
		name string
		u    upload
		code string
	}{
		{"a PNG named .pdf", upload{fileName: "avtale.pdf", contentType: "application/pdf", data: testPNG(t)}, "document_type_unsupported"},
		{"a PDF declared as a PNG", upload{fileName: "avtale", contentType: "image/png", data: testPDF(8)}, "document_type_unsupported"},
		{"a text file", upload{fileName: "notat.txt", contentType: "text/plain", data: []byte("hello, this is not a document")}, "document_type_unsupported"},
		{"a file over 10 MiB", upload{fileName: "stor.pdf", contentType: "application/pdf", data: testPDF(10 * 1024 * 1024)}, "document_too_large"},
		{"no file at all", upload{noFile: true}, ""},
		{"an empty file", upload{fileName: "tom.pdf", contentType: "application/pdf"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.u.categoryID = other
			p := refused(t, postDocument(t, hr, person, tc.u), http.StatusBadRequest, tc.code)
			if len(p.Errors["file"]) == 0 {
				t.Errorf("errors = %v, want the refusal on file", p.Errors)
			}
		})
	}
	if keys := h.objects.keys(); len(keys) != 0 {
		t.Errorf("the store holds %v, want nothing written for a refused file", keys)
	}

	// A file too large is that alone: the fields after it are never read, and
	// their absence is not reported beside it.
	late := upload{fileName: "stor.pdf", contentType: "application/pdf", data: testPDF(10 * 1024 * 1024), categoryID: other, fieldsAfterFile: true}
	p := refused(t, postDocument(t, hr, person, late), http.StatusBadRequest, "document_too_large")
	if len(p.Errors) != 1 || len(p.Errors["file"]) == 0 {
		t.Errorf("errors = %v, want the file alone", p.Errors)
	}

	just := testPDF(10*1024*1024 - 1024)
	if got := uploadDocument(t, hr, person, upload{fileName: "stor.pdf", contentType: "application/pdf", data: just, categoryID: other}); got.SizeBytes != int64(len(just)) {
		t.Errorf("sizeBytes = %d, want %d", got.SizeBytes, len(just))
	}
}

// At most 200 documents per person, counted whatever the caller may read: HR
// without staff:personal-data is refused the 201st although every one of the
// 200 is a health document it cannot see (plan reading 2, accepted and
// documented). The object written before the refusal is removed again.
func TestDocuments_TheTwoHundredAndFirstIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	person := seedPerson(t, h, "Kari", "Nordmann")
	h.Exec(t, `
		INSERT INTO staff.documents (person_id, category_id, object_key, file_name, content_type, size_bytes,
			uploaded_by_user_id, created_at, updated_at)
		SELECT $1::bigint, c.id, 'people/' || $1::bigint || '/seed-' || n, 'helse.pdf', 'application/pdf', 10, $2, $3, $3
		FROM generate_series(1, 200) AS n, staff.document_categories c WHERE c.key = 'health'`, person, uuid.New(), h.Now())

	if docs := listDocuments(t, hr, person); len(docs) != 0 {
		t.Fatalf("HR lists %d documents, want none of the health documents", len(docs))
	}
	refused(t, postDocument(t, hr, person, pdfUpload(documentCategoryID(t, h, "other"))), http.StatusConflict, "document_count_exceeded")
	if keys := h.objects.keys(); len(keys) != 0 {
		t.Errorf("the store holds %v after the refusal, want the object removed", keys)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE person_id = $1`, person); n != 200 {
		t.Errorf("person has %d documents, want 200", n)
	}
}

// Who may file: a non-restricted category is staff:manage's or
// staff:personal-data's, a restricted one staff:personal-data's alone —
// category_restricted to HR without it — and to anyone else, the person
// themselves included, the uniform 403 whatever the category.
func TestDocuments_WhoMayFileWhat(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	person := seedPerson(t, h, "Kari", "Nordmann")
	self, selfID := signIn(t, h)
	link(t, h, person, selfID)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")
	colleague, _ := signIn(t, h, "staff:view")
	outsider, _ := signIn(t, h)
	health, other := documentCategoryID(t, h, "health"), documentCategoryID(t, h, "other")

	for _, tc := range []struct {
		name     string
		c        *modtest.Client
		category int64
		status   int
		code     string
	}{
		{"HR files a non-restricted document", hr, other, http.StatusCreated, ""},
		{"personal-data files a non-restricted document", pd, other, http.StatusCreated, ""},
		{"personal-data files a health document", pd, health, http.StatusCreated, ""},
		{"HR is refused a health document", hr, health, http.StatusForbidden, "category_restricted"},
		{"the person themselves", self, other, http.StatusForbidden, ""},
		{"a colleague", colleague, other, http.StatusForbidden, ""},
		{"an outsider, a health document", outsider, health, http.StatusForbidden, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := postDocument(t, tc.c, person, pdfUpload(tc.category))
			if tc.status == http.StatusCreated {
				if r.Status != http.StatusCreated {
					t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
				}
				return
			}
			refused(t, r, tc.status, tc.code)
		})
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.documents`); n != 3 || len(h.objects.keys()) != 3 {
		t.Errorf("%d rows and %d objects, want the three filed and nothing left of the refusals", n, len(h.objects.keys()))
	}
	do(t, hr, http.MethodPost, documentsPath(9_999_999), nil, http.StatusNotFound, nil,
		modtest.RawBody(pdfUpload(other).form(t)))
}

// The object is written before the row's transaction: a store that cannot
// write is 503 storage_unavailable with no row; a refusal after the write —
// an archived category, an erased person — removes the object again.
func TestDocuments_TheObjectIsWrittenFirstAndRemovedOnARefusal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	person := seedPerson(t, h, "Kari", "Nordmann")

	h.objects.failPut(errors.New("disk full"))
	refused(t, postDocument(t, hr, person, pdfUpload(documentCategoryID(t, h, "other"))), http.StatusServiceUnavailable, "storage_unavailable")
	if n := h.Count(t, `SELECT count(*) FROM staff.documents`); n != 0 {
		t.Errorf("%d rows after the store refused the write, want none", n)
	}
	h.objects.failPut(nil)

	archived := modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.document_categories (key, name_en, name_nb, retention_class, sort, archived_at, created_at, updated_at)
		VALUES ('old', 'Old', 'Gammel', 'D', 7, $1, $1, $1) RETURNING id`, h.Now())
	p := refused(t, postDocument(t, hr, person, pdfUpload(archived)), http.StatusBadRequest, "")
	if len(p.Errors["categoryId"]) == 0 {
		t.Errorf("errors = %v, want the archived category named", p.Errors)
	}

	erased := seedPerson(t, h, "Ola", "Glemt")
	h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, erased, h.Now())
	refused(t, postDocument(t, hr, erased, pdfUpload(documentCategoryID(t, h, "other"))), http.StatusConflict, "person_erased")

	if keys := h.objects.keys(); len(keys) != 0 {
		t.Errorf("the store holds %v, want every object of a refused upload removed", keys)
	}
	if n := len(h.objects.deletes); n != 2 {
		t.Errorf("%d deletes, want one per refusal after the write", n)
	}
}

// A signed contract document sets contract_signed_at on the employment that
// covers its signing day (plan reading 5) — when exactly one does and it has
// none — with the employment's own event; the document belongs to that
// employment. Two covering employments set nothing; neither does another
// category, nor an employment that already has the date.
func TestDocuments_ASignedContractSetsTheEmploymentsSigningDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	contract, other := documentCategoryID(t, h, "contract"), documentCategoryID(t, h, "other")
	signed := func(t *testing.T, employment int64) *time.Time {
		t.Helper()
		return modtest.One[*time.Time](t, h.Harness, `SELECT contract_signed_at::timestamptz FROM staff.employments WHERE id = $1`, employment)
	}

	t.Run("one covering employment", func(t *testing.T) {
		person := seedPerson(t, h, "Kari", "Nordmann")
		employment := seedEmployment(t, h, person, 0)
		u := pdfUpload(contract)
		u.signedAt = "2026-01-05"
		doc := uploadDocument(t, hr, person, u)
		if got := signed(t, employment); got == nil || got.Format(time.DateOnly) != "2026-01-05" {
			t.Errorf("contract_signed_at = %v, want 2026-01-05", got)
		}
		if doc.EmploymentId == nil || *doc.EmploymentId != employment || doc.SignedAt == nil || doc.SignedAt.Format(time.DateOnly) != "2026-01-05" {
			t.Errorf("document = %+v, want employment %d signed 2026-01-05", doc, employment)
		}
		events := listEvents(t, hr, person)
		if k := kinds(events); !slices.Equal(k, []string{"document_added", "employment_updated"}) {
			t.Fatalf("events = %v, want the document after the employment's change", k)
		}
		if f := fieldsOf(events[1]); f["contractSignedAt"] == nil {
			t.Errorf("employment_updated fields = %v, want contractSignedAt", f)
		}

		// A second signed contract changes nothing: the date is there.
		u.signedAt = "2026-02-01"
		uploadDocument(t, hr, person, u)
		if got := signed(t, employment); got == nil || got.Format(time.DateOnly) != "2026-01-05" {
			t.Errorf("after a second contract contract_signed_at = %v, want 2026-01-05 kept", got)
		}
	})

	t.Run("two covering employments", func(t *testing.T) {
		person := seedPerson(t, h, "Ola", "Hansen")
		first := seedEmployment(t, h, person, 0)
		second := seedEmploymentDated(t, h, person, 0, "2026-01-02", nil)
		u := pdfUpload(contract)
		u.signedAt = "2026-01-05"
		doc := uploadDocument(t, hr, person, u)
		if signed(t, first) != nil || signed(t, second) != nil {
			t.Error("a contract covered by two employments set a signing date, want none")
		}
		if doc.EmploymentId != nil {
			t.Errorf("employmentId = %d, want absent with two covering", *doc.EmploymentId)
		}
	})

	t.Run("another category", func(t *testing.T) {
		person := seedPerson(t, h, "Per", "Olsen")
		employment := seedEmployment(t, h, person, 0)
		u := pdfUpload(other)
		u.signedAt = "2026-01-05"
		doc := uploadDocument(t, hr, person, u)
		if signed(t, employment) != nil {
			t.Error("a document of category other set the signing date")
		}
		if doc.EmploymentId == nil || *doc.EmploymentId != employment {
			t.Errorf("employmentId = %v, want %d, the one covering signedAt", doc.EmploymentId, employment)
		}
	})

	t.Run("no signing day: the upload day decides the employment", func(t *testing.T) {
		person := seedPerson(t, h, "Siri", "Berg")
		ended := "2026-03-31"
		seedEmploymentDated(t, h, person, 0, "2026-01-01", &ended)
		current := seedEmploymentDated(t, h, person, 0, "2026-04-01", nil)
		doc := uploadDocument(t, hr, person, pdfUpload(other))
		if doc.EmploymentId == nil || *doc.EmploymentId != current {
			t.Errorf("employmentId = %v, want %d, the one covering today", doc.EmploymentId, current)
		}
	})
}

// The lock order is person → employment → document (design §4): with the
// employment's row held elsewhere, the upload waits on it holding no write
// on staff.documents — the document is inserted after the employment is
// locked, never before.
func TestDocuments_TheEmploymentIsLockedBeforeTheDocument(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	person := seedPerson(t, h, "Kari", "Nordmann")
	employment := seedEmployment(t, h, person, 0)
	ctx := context.Background()

	holder, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, `SELECT 1 FROM staff.employments WHERE id = $1 FOR NO KEY UPDATE`, employment); err != nil {
		t.Fatalf("hold the employment: %v", err)
	}

	u := pdfUpload(documentCategoryID(t, h, "contract"))
	u.signedAt = "2026-01-05"
	ct, body := u.form(t)
	done := make(chan *modtest.Response, 1)
	go func() { done <- hr.Do(http.MethodPost, documentsPath(person), nil, modtest.RawBody(ct, body)) }()

	var waiter int32
	for deadline := time.Now().Add(10 * time.Second); waiter == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the upload never waited on the employment's row")
		}
		_ = h.Pool().QueryRow(ctx, `
			SELECT coalesce(max(pid), 0) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()`).Scan(&waiter)
		time.Sleep(20 * time.Millisecond)
	}
	writes := h.Count(t, `
		SELECT count(*) FROM pg_locks
		WHERE pid = $1 AND relation = 'staff.documents'::regclass AND mode = 'RowExclusiveLock'`, waiter)
	if writes != 0 {
		t.Error("the upload wrote staff.documents before it held the employment, want person → employment → document")
	}
	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("release the employment: %v", err)
	}
	if r := <-done; r.Status != http.StatusCreated {
		t.Errorf("upload after the release: status %d body %s, want 201", r.Status, r.Body)
	}
}

// Who reads a person's archive (design §5): the person all of theirs, health
// included; staff:manage all but health; staff:personal-data all. A manager,
// a colleague and an outsider get the 404 an unknown id gets, and a health
// document is the bare 404 to a reader without the health group.
func TestDocuments_ListAndDownloadPerAudience(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	person := seedPerson(t, h, "Kari", "Nordmann")
	boss := seedPerson(t, h, "Siri", "Sjef")
	seedEmployment(t, h, boss, 0)
	seedEmployment(t, h, person, boss)
	contractDoc := seedDocument(t, h, person, "contract")
	h.Advance(time.Minute)
	healthDoc := seedDocument(t, h, person, "health")

	self, selfID := signIn(t, h)
	link(t, h, person, selfID)
	manager, managerID := signIn(t, h)
	link(t, h, boss, managerID)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")
	colleague, _ := signIn(t, h, "staff:view")
	outsider, _ := signIn(t, h)

	for _, tc := range []struct {
		name     string
		c        *modtest.Client
		list     []string // nil: the 404
		contract int
		health   int
	}{
		{"the person themselves", self, []string{"health", "contract"}, http.StatusOK, http.StatusOK},
		{"HR", hr, []string{"contract"}, http.StatusOK, http.StatusNotFound},
		{"personal-data", pd, []string{"health", "contract"}, http.StatusOK, http.StatusOK},
		{"their manager", manager, nil, http.StatusNotFound, http.StatusNotFound},
		{"a colleague", colleague, nil, http.StatusNotFound, http.StatusNotFound},
		{"an outsider", outsider, nil, http.StatusNotFound, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.list == nil {
				r := tc.c.Do(http.MethodGet, documentsPath(person), nil)
				if r.Status != http.StatusNotFound || len(r.Body) != 0 {
					t.Errorf("list: status %d body %q, want the bare 404", r.Status, r.Body)
				}
			} else if got := categoryKeys(listDocuments(t, tc.c, person)); !slices.Equal(got, tc.list) {
				t.Errorf("list = %v, want %v", got, tc.list)
			}
			for id, want := range map[int64]int{contractDoc: tc.contract, healthDoc: tc.health} {
				r := tc.c.Do(http.MethodGet, documentPath(id), nil)
				if r.Status != want || (want == http.StatusNotFound && len(r.Body) != 0) {
					t.Errorf("download %d: status %d body %q, want %d", id, r.Status, r.Body, want)
				}
			}
		})
	}
	unknown := hr.Do(http.MethodGet, documentPath(9_999_999), nil)
	if unknown.Status != http.StatusNotFound || len(unknown.Body) != 0 {
		t.Errorf("an unknown document: status %d body %q, want the bare 404", unknown.Status, unknown.Body)
	}
	do(t, hr, http.MethodGet, documentsPath(9_999_999), nil, http.StatusNotFound, nil)
}

// A row whose object the store cannot read is 503 storage_unavailable, not a
// 404: a fault to be seen, not a document to be denied.
func TestDocuments_AnUnreadableObjectIs503(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	doc := seedDocument(t, h, seedPerson(t, h, "Kari", "Nordmann"), "other")
	h.objects.failGet(errors.New("disk gone"))
	refused(t, hr.Do(http.MethodGet, documentPath(doc), nil), http.StatusServiceUnavailable, "storage_unavailable")
}

// PUT /documents/{id} replaces the category, the dates and the employment
// under If-Match; moving a document into or out of a restricted category is
// staff:personal-data's (category_restricted), and each change is a
// document_updated event naming what moved.
func TestDocuments_Update(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")
	self, selfID := signIn(t, h)
	person := seedPerson(t, h, "Kari", "Nordmann")
	link(t, h, person, selfID)
	employment := seedEmployment(t, h, person, 0)
	stranger := seedPerson(t, h, "Ola", "Hansen")
	strangers := seedEmployment(t, h, stranger, 0)
	doc := seedDocument(t, h, person, "other")
	certificate, health := documentCategoryID(t, h, "certificate"), documentCategoryID(t, h, "health")

	body := map[string]any{"categoryId": certificate, "validTo": "2027-06-30", "employmentId": employment}
	refused(t, hr.Do(http.MethodPut, documentPath(doc), body, modtest.Header("If-Match", "x")), http.StatusBadRequest, "")
	refused(t, hr.Do(http.MethodPut, documentPath(doc), body, ifMatch(2)), http.StatusConflict, "revision_conflict")
	refused(t, self.Do(http.MethodPut, documentPath(doc), body, ifMatch(1)), http.StatusForbidden, "")
	p := refused(t, hr.Do(http.MethodPut, documentPath(doc), map[string]any{"categoryId": certificate, "employmentId": strangers}, ifMatch(1)), http.StatusBadRequest, "")
	if len(p.Errors["employmentId"]) == 0 {
		t.Errorf("another person's employment: errors = %v, want employmentId named", p.Errors)
	}

	var got gen.StaffDocument
	do(t, hr, http.MethodPut, documentPath(doc), body, http.StatusOK, &got, ifMatch(1))
	if got.Category.Key != "certificate" || got.ValidTo == nil || got.ValidTo.Format(time.DateOnly) != "2027-06-30" ||
		got.EmploymentId == nil || *got.EmploymentId != employment || got.Revision != 2 {
		t.Errorf("updated = %+v, want certificate valid to 2027-06-30 on employment %d, revision 2", got, employment)
	}
	events := listEvents(t, hr, person)
	if len(events) == 0 || events[0].Kind != "document_updated" || events[0].Payload["documentId"] != float64(doc) {
		t.Fatalf("events = %v, want document_updated about document %d first", kinds(events), doc)
	}
	f := fieldsOf(events[0])
	if c, _ := f["category"].(map[string]any); c["from"] != "other" || c["to"] != "certificate" || f["validTo"] == nil || f["fileName"] != nil {
		t.Errorf("document_updated fields = %v, want category other → certificate and validTo", f)
	}

	refused(t, hr.Do(http.MethodPut, documentPath(doc), map[string]any{"categoryId": health}, ifMatch(2)), http.StatusForbidden, "category_restricted")
	got = gen.StaffDocument{}
	do(t, pd, http.MethodPut, documentPath(doc), map[string]any{"categoryId": health}, http.StatusOK, &got, ifMatch(2))
	if got.Category.Key != "health" || got.ValidTo != nil || got.EmploymentId == nil || *got.EmploymentId != employment {
		t.Errorf("moved = %+v, want health, validTo cleared, the employment kept since it was left out", got)
	}
	// An explicit null clears the employment; left out, it stays.
	got = gen.StaffDocument{}
	do(t, pd, http.MethodPut, documentPath(doc), map[string]any{"categoryId": health, "employmentId": nil}, http.StatusOK, &got, ifMatch(3))
	if got.EmploymentId != nil {
		t.Errorf("employmentId null = %+v, want the employment cleared", got)
	}
	// Now a health document, it is the bare 404 to HR — and so is a write.
	if r := hr.Do(http.MethodPut, documentPath(doc), map[string]any{"categoryId": certificate}, ifMatch(4)); r.Status != http.StatusNotFound {
		t.Errorf("HR moving a health document out: status %d, want 404", r.Status)
	}
	do(t, pd, http.MethodPut, documentPath(doc), map[string]any{"categoryId": certificate}, http.StatusOK, nil, ifMatch(4))
	do(t, hr, http.MethodPut, documentPath(9_999_999), body, http.StatusNotFound, nil, ifMatch(1))
}

// DELETE /documents/{id} removes the row and queues its object in one
// transaction, then removes the object and clears the queue; a store that
// refuses leaves the ledger row for the retention worker, and the request
// still succeeds — the document is gone.
func TestDocuments_Delete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	self, selfID := signIn(t, h)
	person := seedPerson(t, h, "Kari", "Nordmann")
	link(t, h, person, selfID)
	ledger := func(key string) int {
		return h.Count(t, `SELECT count(*) FROM staff.object_deletions WHERE object_key = $1`, key)
	}

	doc := seedDocument(t, h, person, "other")
	key, _ := storedDocument(t, h, doc)
	refused(t, self.Do(http.MethodDelete, documentPath(doc), nil), http.StatusForbidden, "")
	do(t, hr, http.MethodDelete, documentPath(doc), nil, http.StatusNoContent, nil)
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, doc); n != 0 || h.objects.has(key) || ledger(key) != 0 {
		t.Errorf("after the delete: %d rows, object %v, %d ledger rows, want all gone", n, h.objects.has(key), ledger(key))
	}
	if events := listEvents(t, hr, person); len(events) == 0 || events[0].Kind != "document_removed" {
		t.Errorf("events = %v, want document_removed", kinds(events))
	}
	do(t, hr, http.MethodDelete, documentPath(doc), nil, http.StatusNotFound, nil)

	kept := seedDocument(t, h, person, "other")
	keptKey, _ := storedDocument(t, h, kept)
	h.objects.failDelete(errors.New("store down"))
	do(t, hr, http.MethodDelete, documentPath(kept), nil, http.StatusNoContent, nil)
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, kept); n != 0 || !h.objects.has(keptKey) || ledger(keptKey) != 1 {
		t.Errorf("after a refused object delete: %d rows, object %v, %d ledger rows, want the row gone and the ledger row kept",
			n, h.objects.has(keptKey), ledger(keptKey))
	}
	// The failed attempt is on the ledger row, so it counts as failing: one
	// attempt, the reason, and the next attempt due after the first backoff.
	var (
		attempts  int32
		lastError *string
		next      time.Time
	)
	if err := h.Pool().QueryRow(context.Background(), `
		SELECT attempts, last_error, next_attempt_at FROM staff.object_deletions WHERE object_key = $1`, keptKey).Scan(&attempts, &lastError, &next); err != nil {
		t.Fatalf("read the ledger row: %v", err)
	}
	if attempts != 1 || lastError == nil || *lastError != "the object store refused the delete" || !next.Equal(h.Now().Add(2*time.Second)) {
		t.Errorf("ledger row = attempts %d, last_error %v, next %s, want 1, the reason, now + 2s", attempts, lastError, next)
	}
	h.objects.failDelete(nil)

	healthDoc := seedDocument(t, h, person, "health")
	do(t, hr, http.MethodDelete, documentPath(healthDoc), nil, http.StatusNotFound, nil)
	pd, _ := signIn(t, h, "staff:personal-data")
	do(t, pd, http.MethodDelete, documentPath(healthDoc), nil, http.StatusNoContent, nil)

	// A person with a document is in use: only a person created in error is
	// deleted, and a document names them.
	other := seedPerson(t, h, "Ola", "Hansen")
	seedDocument(t, h, other, "other")
	r := hr.Do(http.MethodDelete, personPath(other), nil)
	refused(t, r, http.StatusConflict, "person_in_use")
	if !strings.Contains(string(r.Body), "has documents") {
		t.Errorf("person_in_use body = %s, want it to say the person has documents", r.Body)
	}
}

// A holder of staff:manage alone who is the person themselves reads their own
// health document, as the person does — and a change or a removal of it is
// category_restricted (403), not the 404 of a document they cannot see.
func TestDocuments_HROwnHealthDocument(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrID := signIn(t, h, "staff:manage")
	person := seedPerson(t, h, "Hanne", "HR")
	link(t, h, person, hrID)
	doc := seedDocument(t, h, person, "health")

	do(t, hr, http.MethodGet, documentPath(doc), nil, http.StatusOK, nil)
	refused(t, hr.Do(http.MethodPut, documentPath(doc), map[string]any{"categoryId": documentCategoryID(t, h, "health")}, ifMatch(1)),
		http.StatusForbidden, "category_restricted")
	refused(t, hr.Do(http.MethodDelete, documentPath(doc), nil), http.StatusForbidden, "category_restricted")
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, doc); n != 1 {
		t.Errorf("%d rows, want the document kept", n)
	}
}

// The upload carries the platform's rate limit, 600 an hour per client
// address: it is the one operation that writes bytes outside the database.
func TestDocuments_UploadsAreRateLimited(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	other, _ := signIn(t, h, "staff:manage")
	ct, body := upload{fileName: "a.pdf", contentType: "application/pdf", data: testPDF(8), categoryID: 1}.form(t)
	for i := range staff.DocumentUploadsPerHour {
		if r := hr.Do(http.MethodPost, documentsPath(9_999_999), nil, modtest.RawBody(ct, body)); r.Status != http.StatusNotFound {
			t.Fatalf("upload %d: status %d body %s, want the unknown person's 404", i+1, r.Status, r.Body)
		}
	}
	over := hr.Do(http.MethodPost, documentsPath(9_999_999), nil, modtest.RawBody(ct, body))
	if over.Status != http.StatusTooManyRequests || over.Code() != "rate_limited" || over.Header("Retry-After") == "" {
		t.Errorf("upload %d: status %d code %q, want 429 rate_limited with a Retry-After", staff.DocumentUploadsPerHour+1, over.Status, over.Code())
	}
	if r := other.Do(http.MethodPost, documentsPath(9_999_999), nil, modtest.RawBody(ct, body)); r.Status != http.StatusNotFound {
		t.Errorf("another client: status %d, want 404", r.Status)
	}
}

// GET /me/documents is the caller's own archive, health documents included;
// a caller linked to nobody is no_person.
func TestDocuments_MyDocuments(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	person := seedPerson(t, h, "Kari", "Nordmann")
	seedDocument(t, h, person, "health")
	seedDocument(t, h, seedPerson(t, h, "Ola", "Hansen"), "other")
	self, selfID := signIn(t, h)
	link(t, h, person, selfID)

	var mine []gen.StaffDocument
	do(t, self, http.MethodGet, meDocumentsPath, nil, http.StatusOK, &mine)
	if got := categoryKeys(mine); !slices.Equal(got, []string{"health"}) {
		t.Errorf("my documents = %v, want my health document alone", got)
	}
	nobody, _ := signIn(t, h, "staff:manage")
	refused(t, nobody.Do(http.MethodGet, meDocumentsPath, nil), http.StatusNotFound, "no_person")
}

// canSeeHealth (design §5): on GET /meta staff:personal-data's; on a person,
// the person themselves too.
func TestDocuments_CanSeeHealth(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	person := seedPerson(t, h, "Kari", "Nordmann")
	self, selfID := signIn(t, h)
	link(t, h, person, selfID)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")

	for _, tc := range []struct {
		name         string
		c            *modtest.Client
		meta, person bool
	}{
		{"the person themselves", self, false, true},
		{"HR", hr, false, false},
		{"personal-data", pd, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := getMeta(t, tc.c).Capabilities.CanSeeHealth; got != tc.meta {
				t.Errorf("meta canSeeHealth = %v, want %v", got, tc.meta)
			}
			var p gen.StaffPerson
			do(t, tc.c, http.MethodGet, personPath(person), nil, http.StatusOK, &p)
			if p.Capabilities.CanSeeHealth != tc.person {
				t.Errorf("person canSeeHealth = %v, want %v", p.Capabilities.CanSeeHealth, tc.person)
			}
		})
	}
}

// A document's events name the document and its category, file name and
// dates under the documents group (Self, manage, personal-data) — a manager
// reads that one happened and "changed" — or, for a health document, the
// health group (Self, personal-data): left out of the listing altogether for
// anyone else, HR included. Their classes come from the category.
func TestDocuments_EventsPerAudience(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	person := seedPerson(t, h, "Kari", "Nordmann")
	boss := seedPerson(t, h, "Siri", "Sjef")
	seedEmployment(t, h, boss, 0)
	seedEmployment(t, h, person, boss)
	pd, _ := signIn(t, h, "staff:personal-data")
	hr, _ := signIn(t, h, "staff:manage")
	self, selfID := signIn(t, h)
	link(t, h, person, selfID)
	manager, managerID := signIn(t, h)
	link(t, h, boss, managerID)

	u := pdfUpload(documentCategoryID(t, h, "contract"))
	u.validTo = "2027-01-31"
	contractDoc := uploadDocument(t, pd, person, u)
	h.Advance(time.Minute)
	healthDoc := uploadDocument(t, pd, person, pdfUpload(documentCategoryID(t, h, "health")))

	classes := modtest.One[[]string](t, h.Harness, `
		SELECT array_agg(array_to_string(payload_classes, ',') ORDER BY id) FROM staff.events
		WHERE person_id = $1 AND kind = 'document_added'`, person)
	if !slices.Equal(classes, []string{"A", "E"}) {
		t.Errorf("payload_classes = %v, want the contract's A and the health document's E", classes)
	}

	// shown is a field of an event as the reader sees it: the value it was
	// set to, or "changed".
	shown := func(v any) any {
		if c, ok := v.(map[string]any); ok {
			return c["to"]
		}
		return v
	}
	documentEvents := func(t *testing.T, events []gen.StaffEvent) map[float64]map[string]any {
		t.Helper()
		out := map[float64]map[string]any{}
		for _, e := range events {
			if e.Kind == "document_added" {
				id, _ := e.Payload["documentId"].(float64)
				out[id] = fieldsOf(e)
			}
		}
		return out
	}
	var mine gen.PaginatedResponseOfStaffEvent
	do(t, self, http.MethodGet, "/api/v1/staff/me/events", nil, http.StatusOK, &mine)

	for _, tc := range []struct {
		name       string
		events     []gen.StaffEvent
		total      int32
		health     bool
		contractFn string // the contract's fileName as shown
	}{
		{"personal-data", listEvents(t, pd, person), 2, true, "avtale.pdf"},
		{"the person themselves", listEvents(t, self, person), 2, true, "avtale.pdf"},
		{"My page", mine.Data, mine.Pagination.TotalCount, true, "avtale.pdf"},
		{"HR", listEvents(t, hr, person), 1, false, "avtale.pdf"},
		{"their manager", listEvents(t, manager, person), 1, false, "changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := documentEvents(t, tc.events)
			if f, ok := docs[float64(contractDoc.Id)]; !ok || shown(f["fileName"]) != tc.contractFn {
				t.Errorf("the contract's event = %v (present %v), want fileName %q", f, ok, tc.contractFn)
			} else if tc.contractFn != "changed" {
				if shown(f["category"]) != "contract" || shown(f["validTo"]) != "2027-01-31" {
					t.Errorf("the contract's fields = %v, want category contract and validTo", f)
				}
			}
			if _, ok := docs[float64(healthDoc.Id)]; ok != tc.health {
				t.Errorf("the health document's event present = %v, want %v", ok, tc.health)
			}
			if tc.total != 0 && int32(len(tc.events)) != tc.total {
				t.Errorf("%d events, want %d", len(tc.events), tc.total)
			}
		})
	}
	var page gen.PaginatedResponseOfStaffEvent
	do(t, hr, http.MethodGet, eventsPath(person), nil, http.StatusOK, &page)
	if page.Pagination.TotalCount != 1 {
		t.Errorf("HR's total = %d, want the health event left out of the count too", page.Pagination.TotalCount)
	}
}
