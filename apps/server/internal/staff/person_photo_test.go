package staff_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

func photoPath(id int64) string { return personPath(id) + "/photo" }

// putPhoto sends data as the photo of personID under fileName and the
// declared contentType, and answers the whole response.
func putPhoto(t *testing.T, c *modtest.Client, personID int64, fileName, contentType string, data []byte) *modtest.Response {
	t.Helper()
	ct, body := upload{fileName: fileName, contentType: contentType, data: data}.form(t)
	return c.Do(http.MethodPut, photoPath(personID), nil, modtest.RawBody(ct, body))
}

// setPhoto sets data, a PNG, as the photo of personID and fails the test
// unless it was answered 200 with the person.
func setPhoto(t *testing.T, c *modtest.Client, personID int64, data []byte) gen.StaffPerson {
	t.Helper()
	r := putPhoto(t, c, personID, "me.png", "image/png", data)
	if r.Status != http.StatusOK {
		t.Fatalf("set person %d's photo: status %d body %s, want 200", personID, r.Status, r.Body)
	}
	var out gen.StaffPerson
	r.JSON(&out)
	return out
}

// photoKey is the object key and the type person id's row names, "" when
// there is no photo.
func photoKey(t *testing.T, h *harness, id int64) (key, contentType string) {
	t.Helper()
	key = modtest.One[string](t, h.Harness, `SELECT coalesce(photo_object_key, '') FROM staff.people WHERE id = $1`, id)
	contentType = modtest.One[string](t, h.Harness, `SELECT coalesce(photo_content_type, '') FROM staff.people WHERE id = $1`, id)
	return key, contentType
}

// queued reports whether key waits in the deletion ledger.
func queued(t *testing.T, h *harness, key string) bool {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM staff.object_deletions WHERE object_key = $1`, key) == 1
}

// sizedPNG is a w×h PNG.
func sizedPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return buf.Bytes()
}

func testGIF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gif.Encode(&buf, image.NewPaletted(image.Rect(0, 0, 8, 8), color.Palette{color.Black, color.White}), nil); err != nil {
		t.Fatalf("encode GIF: %v", err)
	}
	return buf.Bytes()
}

// staff:manage and staff:personal-data set the photo (photo sync design S2:
// HR alone): 200 with the person, photoUpdatedAt set and canSetPhoto true for
// them, the object stored under the person's photo prefix. Each set replaces
// the last, whose object waits in the ledger — never removed inline. The
// person themselves, the manager and a colleague see the person and are the
// uniform 403 — the person's canSetPhoto false, their own picture being their
// Vantigo avatar — a caller who does not see them the bare 404; an erased
// person is person_erased, the object written for it removed again.
func TestPersonPhoto_WhoMaySetIt(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h

	var previous string
	for _, tc := range []struct {
		name string
		c    *modtest.Client
	}{{"HR", w.hr}, {"personal-data", w.pd}} {
		got := setPhoto(t, tc.c, w.kari.Id, testPNG(t))
		if got.PhotoUpdatedAt == nil || !got.Capabilities.CanSetPhoto {
			t.Errorf("%s: photoUpdatedAt %v canSetPhoto %v, want set and true", tc.name, got.PhotoUpdatedAt, got.Capabilities.CanSetPhoto)
		}
		key, contentType := photoKey(t, h, w.kari.Id)
		if !strings.HasPrefix(key, "people/"+strconv.FormatInt(w.kari.Id, 10)+"/photo/") || contentType != "image/png" || !h.objects.has(key) {
			t.Errorf("%s: stored %q as %q (in the store: %v), want the person's photo prefix, image/png, stored", tc.name, key, contentType, h.objects.has(key))
		}
		if previous != "" && (key == previous || !queued(t, h, previous) || !h.objects.has(previous)) {
			t.Errorf("%s: the replaced object %q: queued %v, still stored %v; want a new key, queued, not removed inline",
				tc.name, previous, queued(t, h, previous), h.objects.has(previous))
		}
		previous = key
	}

	for name, c := range map[string]*modtest.Client{"the manager": w.bossC, "the person": w.kariC} {
		var got gen.StaffPerson
		do(t, c, http.MethodGet, personPath(w.kari.Id), nil, http.StatusOK, &got)
		if got.Capabilities.CanSetPhoto || got.PhotoUpdatedAt == nil {
			t.Errorf("%s: canSetPhoto %v photoUpdatedAt %v, want false and set", name, got.Capabilities.CanSetPhoto, got.PhotoUpdatedAt)
		}
	}
	var mine gen.StaffPerson
	do(t, w.kariC, http.MethodGet, mePath, nil, http.StatusOK, &mine)
	if mine.Capabilities.CanSetPhoto {
		t.Error("the person's own record: canSetPhoto true, want false (their picture is their Vantigo avatar)")
	}

	stored := len(h.objects.keys())
	for name, c := range map[string]*modtest.Client{"the person": w.kariC, "the manager": w.bossC, "a colleague": w.viewer} {
		if r := putPhoto(t, c, w.kari.Id, "me.png", "image/png", testPNG(t)); r.Status != http.StatusForbidden || r.Code() != "forbidden" {
			t.Errorf("%s: status %d body %s, want the uniform 403", name, r.Status, r.Body)
		}
	}
	for name, id := range map[string]int64{"a stranger": w.kari.Id, "an unknown id": 9_999_999} {
		c := w.nobody
		if id != w.kari.Id {
			c = w.hr
		}
		if r := putPhoto(t, c, id, "me.png", "image/png", testPNG(t)); r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("%s: status %d body %q, want the bare 404", name, r.Status, r.Body)
		}
	}
	if n := len(h.objects.keys()); n != stored {
		t.Errorf("refused sets left %d objects, want %d", n, stored)
	}

	h.Exec(t, `UPDATE staff.people SET erased_at = now() WHERE id = $1`, w.kari.Id)
	for name, c := range map[string]*modtest.Client{"the person": w.kariC, "HR": w.hr, "personal-data": w.pd} {
		var erased gen.StaffPerson
		do(t, c, http.MethodGet, personPath(w.kari.Id), nil, http.StatusOK, &erased)
		if erased.Capabilities.CanSetPhoto {
			t.Errorf("%s: canSetPhoto on an erased person, whose photo is person_erased", name)
		}
	}
	refused(t, putPhoto(t, w.hr, w.kari.Id, "me.png", "image/png", testPNG(t)), http.StatusConflict, "person_erased")
	if n := len(h.objects.keys()); n != stored {
		t.Errorf("the erased person's set left %d objects, want %d: the object is removed after a refusal", n, stored)
	}
}

// A photo is a JPEG or PNG by its own bytes, at most 5 MiB, at most 4096
// pixels a side; anything else is photo_invalid on file, nothing stored. A
// JPEG sent as a .png is a JPEG — the bytes decide — and is served as one.
func TestPersonPhoto_TheFileIsJudgedByItsBytes(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h

	big := append(sizedPNG(t, 8, 8), bytes.Repeat([]byte{0}, 5*1024*1024)...)
	big = big[:5*1024*1024+1]
	for _, tc := range []struct {
		name, fileName, contentType string
		data                        []byte
		message                     string
	}{
		{"a PDF", "me.pdf", "application/pdf", testPDF(32), "JPEG or PNG"},
		{"a PDF called a PNG", "me.png", "image/png", testPDF(32), "JPEG or PNG"},
		{"a GIF", "me.gif", "image/gif", testGIF(t), "JPEG or PNG"},
		{"a HEIC", "me.heic", "image/heic", testHEIC(), "JPEG or PNG"},
		{"a PNG 4097 pixels tall", "tall.png", "image/png", sizedPNG(t, 1, 4097), "4096 pixels"},
		{"5 MiB and a byte", "big.png", "image/png", big, "5 MiB"},
		// Its header is a PNG's, but its chunks stop before IEND: the
		// metadata strip cannot walk it.
		{"a PNG cut short", "cut.png", "image/png", func() []byte { p := testPNG(t); return p[:len(p)-12] }(), "JPEG or PNG"},
	} {
		p := refused(t, putPhoto(t, w.hr, w.kari.Id, tc.fileName, tc.contentType, tc.data), http.StatusBadRequest, "photo_invalid")
		if msgs := p.Errors["file"]; len(msgs) != 1 || !strings.Contains(msgs[0], tc.message) {
			t.Errorf("%s: errors %v, want one on file naming %q", tc.name, p.Errors, tc.message)
		}
	}
	ct, body := upload{noFile: true}.form(t)
	if p := refused(t, w.hr.Do(http.MethodPut, photoPath(w.kari.Id), nil, modtest.RawBody(ct, body)), http.StatusBadRequest, ""); len(p.Errors["file"]) != 1 {
		t.Errorf("no file: errors %v, want one on file", p.Errors)
	}
	if keys := h.objects.keys(); len(keys) != 0 {
		t.Errorf("refused photos stored %v, want nothing", keys)
	}

	square := sizedPNG(t, 4096, 4096)
	setPhoto(t, w.hr, w.kari.Id, square)

	r := putPhoto(t, w.hr, w.kari.Id, "me.png", "image/png", testJPEG(t))
	if r.Status != http.StatusOK {
		t.Fatalf("a JPEG named .png: status %d body %s, want 200", r.Status, r.Body)
	}
	if _, contentType := photoKey(t, h, w.kari.Id); contentType != "image/jpeg" {
		t.Errorf("a JPEG named .png is stored as %q, want image/jpeg", contentType)
	}
	got := w.viewer.Do(http.MethodGet, photoPath(w.kari.Id), nil)
	if got.Status != http.StatusOK || got.Header("Content-Type") != "image/jpeg" || !bytes.Equal(got.Body, testJPEG(t)) {
		t.Errorf("served: status %d type %q, want 200 image/jpeg with the bytes", got.Status, got.Header("Content-Type"))
	}
}

// GET /people/{id}/photo streams the bytes to everyone who reads the Work
// group — a colleague included — with its type and length, cached privately
// for five minutes, never sniffed, with no disposition; v is ignored. A
// caller who does not see the person, and a person without a photo, is the
// bare 404; an object the store cannot read is 503.
func TestPersonPhoto_GetStreamsItWithItsHeaders(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h

	if r := w.viewer.Do(http.MethodGet, photoPath(w.kari.Id), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("no photo: status %d body %q, want the bare 404", r.Status, r.Body)
	}
	data := testPNG(t)
	person := setPhoto(t, w.hr, w.kari.Id, data)

	for name, c := range map[string]*modtest.Client{"a colleague": w.viewer, "the manager": w.bossC, "the person": w.kariC} {
		r := c.Do(http.MethodGet, photoPath(w.kari.Id)+"?v="+person.PhotoUpdatedAt.Format("20060102150405.000000"), nil)
		if r.Status != http.StatusOK || !bytes.Equal(r.Body, data) {
			t.Fatalf("%s: status %d, want 200 with the bytes", name, r.Status)
		}
		for header, want := range map[string]string{
			"Content-Type":           "image/png",
			"Content-Length":         strconv.Itoa(len(data)),
			"Cache-Control":          "private, max-age=300",
			"X-Content-Type-Options": "nosniff",
			"Content-Disposition":    "",
		} {
			if got := r.Header(header); got != want {
				t.Errorf("%s: %s = %q, want %q", name, header, got, want)
			}
		}
	}
	for name, id := range map[string]int64{"a stranger": w.kari.Id, "an unknown id": 9_999_999} {
		c := w.nobody
		if id != w.kari.Id {
			c = w.hr
		}
		if r := c.Do(http.MethodGet, photoPath(id), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("%s: status %d body %q, want the bare 404", name, r.Status, r.Body)
		}
	}

	// An object removed from the store between the row's snapshot and the
	// read is the bare 404, as no photo is.
	key, _ := photoKey(t, h, w.kari.Id)
	if err := h.objects.Delete(context.Background(), key); err != nil {
		t.Fatalf("remove the object: %v", err)
	}
	if r := w.viewer.Do(http.MethodGet, photoPath(w.kari.Id), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("an object gone from the store: status %d body %q, want the bare 404", r.Status, r.Body)
	}

	h.objects.failGet(errors.New("disk gone"))
	refused(t, w.viewer.Do(http.MethodGet, photoPath(w.kari.Id), nil), http.StatusServiceUnavailable, "storage_unavailable")
}

// DELETE /people/{id}/photo is the setters' too: the row cleared, the object
// queued in the ledger, 204; then GET is 404. A person without a photo is
// 404, the manager and the person themselves the uniform 403, a stranger the
// 404.
func TestPersonPhoto_RemoveIt(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h

	if r := w.hr.Do(http.MethodDelete, photoPath(w.kari.Id), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("no photo: status %d body %q, want the bare 404", r.Status, r.Body)
	}
	setPhoto(t, w.hr, w.kari.Id, testPNG(t))
	key, _ := photoKey(t, h, w.kari.Id)

	for name, c := range map[string]*modtest.Client{"the manager": w.bossC, "the person": w.kariC} {
		if r := c.Do(http.MethodDelete, photoPath(w.kari.Id), nil); r.Status != http.StatusForbidden || r.Code() != "forbidden" {
			t.Errorf("%s: status %d body %s, want the uniform 403", name, r.Status, r.Body)
		}
	}
	if r := w.nobody.Do(http.MethodDelete, photoPath(w.kari.Id), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("a stranger: status %d body %q, want the bare 404", r.Status, r.Body)
	}

	do(t, w.pd, http.MethodDelete, photoPath(w.kari.Id), nil, http.StatusNoContent, nil)
	if now, _ := photoKey(t, h, w.kari.Id); now != "" || !queued(t, h, key) || !h.objects.has(key) {
		t.Errorf("after the delete: row names %q, queued %v, stored %v; want none, queued, left to the ledger", now, queued(t, h, key), h.objects.has(key))
	}
	if r := w.viewer.Do(http.MethodGet, photoPath(w.kari.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("GET after the delete = %d, want 404", r.Status)
	}
	var person gen.StaffPerson
	do(t, w.hr, http.MethodGet, personPath(w.kari.Id), nil, http.StatusOK, &person)
	if person.PhotoUpdatedAt != nil {
		t.Errorf("photoUpdatedAt = %v after the delete, want absent", person.PhotoUpdatedAt)
	}

	runLedger(t, h)
	if h.objects.has(key) || queued(t, h, key) {
		t.Error("the ledger did not remove the photo's object")
	}
}

// photo_set and photo_removed name the field photo with the content type and
// nothing else (reading 8), read by everyone who reads the history — the
// field is the Work group's.
func TestPersonPhoto_TheEventsCarryTheTypeOnly(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)

	setPhoto(t, w.hr, w.kari.Id, testPNG(t))
	do(t, w.pd, http.MethodPut, photoPath(w.kari.Id), nil, http.StatusOK, nil, modtest.RawBody(upload{fileName: "me.jpg", data: testJPEG(t)}.form(t)))
	do(t, w.hr, http.MethodDelete, photoPath(w.kari.Id), nil, http.StatusNoContent, nil)

	for name, c := range map[string]*modtest.Client{"the manager": w.bossC, "HR": w.hr, "the person": w.kariC} {
		var sets []map[string]any
		var removed map[string]any
		for _, e := range listEvents(t, c, w.kari.Id) {
			switch e.Kind {
			case "photo_set":
				sets = append(sets, fieldsOf(e))
			case "photo_removed":
				removed = fieldsOf(e)
			}
		}
		if len(sets) != 2 || removed == nil {
			t.Fatalf("%s: %d photo_set and removed %v, want two and one", name, len(sets), removed)
		}
		// Newest first: the JPEG replacing the PNG, then the first PNG.
		for i, want := range []map[string]any{{"from": "image/png", "to": "image/jpeg"}, {"to": "image/png"}} {
			if len(sets[i]) != 1 || !sameFields(sets[i]["photo"], want) {
				t.Errorf("%s: photo_set %d = %v, want photo %v and nothing else", name, i, sets[i], want)
			}
		}
		if len(removed) != 1 || !sameFields(removed["photo"], map[string]any{"from": "image/jpeg"}) {
			t.Errorf("%s: photo_removed = %v, want photo from image/jpeg and nothing else", name, removed)
		}
	}
}

// sameFields reports whether got, a decoded change, is want.
func sameFields(got any, want map[string]any) bool {
	m, ok := got.(map[string]any)
	if !ok || len(m) != len(want) {
		return false
	}
	for k, v := range want {
		if m[k] != v {
			return false
		}
	}
	return true
}

// A store that cannot write is 503 storage_unavailable with nothing recorded.
func TestPersonPhoto_AStoreThatCannotWriteIs503(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	w.h.objects.failPut(errors.New("disk full"))
	refused(t, putPhoto(t, w.hr, w.kari.Id, "me.png", "image/png", testPNG(t)), http.StatusServiceUnavailable, "storage_unavailable")
	if key, _ := photoKey(t, w.h, w.kari.Id); key != "" {
		t.Errorf("the row names %q after the store refused, want nothing", key)
	}
	if n := w.h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'photo_set'`, w.kari.Id); n != 0 {
		t.Errorf("photo_set events = %d, want none", n)
	}
}

// A photo's metadata is stripped before it is stored (review M2): a JPEG's
// Exif segment never reaches the store nor anyone who reads the photo, the
// served length is the stripped file's, and the pixels are the same.
func TestPersonPhoto_TheMetadataIsStripped(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	clean := testJPEG(t)
	payload := []byte("Exif\x00\x00GPS 59.91N 10.75E")
	exif := append([]byte{0xFF, 0xE1, 0x00, byte(len(payload) + 2)}, payload...)
	tagged := append(append(append([]byte{}, clean[:2]...), exif...), clean[2:]...)

	setPhoto(t, w.hr, w.kari.Id, tagged)
	key, contentType := photoKey(t, w.h, w.kari.Id)
	got := w.viewer.Do(http.MethodGet, photoPath(w.kari.Id), nil)
	if contentType != "image/jpeg" || got.Status != http.StatusOK || !bytes.Equal(got.Body, clean) ||
		got.Header("Content-Length") != strconv.Itoa(len(clean)) {
		t.Errorf("served %s (%d bytes, Content-Length %s), want the %d bytes without the Exif segment",
			contentType, len(got.Body), got.Header("Content-Length"), len(clean))
	}
	stored, err := w.h.objects.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("read the object: %v", err)
	}
	defer func() { _ = stored.Close() }()
	if data, _ := io.ReadAll(stored); bytes.Contains(data, []byte("Exif")) || bytes.Contains(data, []byte("GPS")) {
		t.Error("the stored photo still carries its Exif segment")
	}
}

// The set carries the uploads' rate limit, 600 an hour per client address,
// counted with the document and evidence uploads: it writes bytes outside
// the database.
func TestPersonPhoto_SetsAreRateLimited(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	other, _ := signIn(t, h, "staff:manage")
	ct, body := upload{fileName: "me.png", contentType: "image/png", data: testPNG(t)}.form(t)
	for i := range staff.DocumentUploadsPerHour {
		if r := hr.Do(http.MethodPut, photoPath(9_999_999), nil, modtest.RawBody(ct, body)); r.Status != http.StatusNotFound {
			t.Fatalf("set %d: status %d body %s, want the unknown person's 404", i+1, r.Status, r.Body)
		}
	}
	over := hr.Do(http.MethodPut, photoPath(9_999_999), nil, modtest.RawBody(ct, body))
	if over.Status != http.StatusTooManyRequests || over.Code() != "rate_limited" || over.Header("Retry-After") == "" {
		t.Errorf("set %d: status %d code %q, want 429 rate_limited with a Retry-After", staff.DocumentUploadsPerHour+1, over.Status, over.Code())
	}
	if r := other.Do(http.MethodPut, photoPath(9_999_999), nil, modtest.RawBody(ct, body)); r.Status != http.StatusNotFound {
		t.Errorf("another client: status %d, want 404", r.Status)
	}
}
