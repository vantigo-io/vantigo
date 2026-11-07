package knowledge_test

// Delivery K-B (knowledge design K18–K23): files, links, mentions, Markdown
// export and import.

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/knowledge"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// filesHarness is one installation with a real file object store on a
// temporary root, answered with the root.
func filesHarness(t *testing.T) (*modtest.Harness, string) {
	t.Helper()
	root := t.TempDir()
	h := newHarness(t,
		modtest.WithEnv("STORAGE_PROVIDER", "fs"),
		modtest.WithEnv("STORAGE_FS_ROOT", root),
		modtest.WithEnv("STORAGE_FS_ALLOW_INSECURE_ROOT", "1"),
	)
	return h, root
}

// pngWithText is a small PNG carrying a tEXt chunk saying secret.
func pngWithText(t *testing.T, secret string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range 16 {
		img.Set(i%4, i/4, color.RGBA{200, 10, 10, 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	chunk := []byte("Comment\x00" + secret)
	var c []byte
	c = binary.BigEndian.AppendUint32(c, uint32(len(chunk)))
	c = append(c, "tEXt"...)
	c = append(c, chunk...)
	c = binary.BigEndian.AppendUint32(c, crc32.ChecksumIEEE(append([]byte("tEXt"), chunk...)))
	out := append([]byte{}, data[:33]...)
	out = append(out, c...)
	return append(out, data[33:]...)
}

// form is a multipart form of one file and some fields.
func form(t *testing.T, filename, contentType string, data []byte, fields map[string]string) modtest.RequestOption {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", contentType)
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return modtest.RawBody(w.FormDataContentType(), buf.Bytes())
}

type fileJSON struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	IsImage     bool   `json:"isImage"`
}

func uploadFile(t *testing.T, p person, article, filename, contentType string, data []byte) fileJSON {
	t.Helper()
	r := p.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+article+"/files", nil, form(t, filename, contentType, data, nil))
	expectStatus(t, "upload "+filename, r, http.StatusCreated, "")
	var f fileJSON
	r.JSON(&f)
	return f
}

// saveDoc is an autosave of a whole document.
func saveDoc(p person, id, title string, content map[string]any, version int32) *modtest.Response {
	return p.c.Do(http.MethodPut, "/api/v1/knowledge/articles/"+id+"/content", map[string]any{
		"title": title, "content": content, "expectedVersion": version,
	})
}

func docOf(blocks ...any) map[string]any { return map[string]any{"type": "doc", "content": blocks} }

func para(inline ...any) map[string]any {
	return map[string]any{"type": "paragraph", "content": inline}
}

func txt(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func imageNode(src, alt string) map[string]any {
	return map[string]any{"type": "image", "attrs": map[string]any{"src": src, "alt": alt}}
}

func articleLink(id, label string) map[string]any {
	return map[string]any{"type": "articleLink", "attrs": map[string]any{"id": id, "label": label}}
}

func mention(id uuid.UUID, label string) map[string]any {
	return map[string]any{"type": "mention", "attrs": map[string]any{"id": id.String(), "label": label}}
}

// Files: an image is stripped and shown inline, anything else is a download;
// both behind the article's read access (K19).
func TestFiles_UploadServeAndAccess(t *testing.T) {
	t.Parallel()
	h, root := filesHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)
	c := signIn(t, h)
	art := create(t, a, "", "private", "Office")
	expectStatus(t, "share with B as a reader", setAccess(a, art.ID, "none", map[uuid.UUID]string{a.id: "write", b.id: "read"}, true), http.StatusOK, "")

	img := uploadFile(t, a, art.ID, "C:\\photos\\office.png", "image/png", pngWithText(t, "Storgata 1, Oslo"))
	if !img.IsImage || img.ContentType != "image/png" || img.Filename != "office.png" || img.URL != "/api/v1/knowledge/files/"+img.ID {
		t.Fatalf("image = %+v", img)
	}
	r := b.c.Do(http.MethodGet, img.URL, nil)
	expectStatus(t, "B reads the image", r, http.StatusOK, "")
	if r.Header("Content-Type") != "image/png" || !strings.HasPrefix(r.Header("Content-Disposition"), "inline;") ||
		r.Header("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(r.Header("Cache-Control"), "private") {
		t.Errorf("image headers: %q %q %q %q", r.Header("Content-Type"), r.Header("Content-Disposition"), r.Header("X-Content-Type-Options"), r.Header("Cache-Control"))
	}
	if bytes.Contains(r.Body, []byte("Storgata")) || int64(len(r.Body)) != img.Size {
		t.Error("the image's metadata was served")
	}

	svg := uploadFile(t, a, art.ID, "logo.svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	if svg.IsImage {
		t.Fatalf("an SVG is an image: %+v", svg)
	}
	r = b.c.Do(http.MethodGet, svg.URL, nil)
	expectStatus(t, "B downloads the SVG", r, http.StatusOK, "")
	if r.Header("Content-Type") != "application/octet-stream" || r.Header("Content-Disposition") != "attachment; filename*=UTF-8''logo.svg" {
		t.Errorf("an attachment served as %q, %q", r.Header("Content-Type"), r.Header("Content-Disposition"))
	}

	// C cannot read the article: its files are the bare 404 an unknown id is.
	expectStatus(t, "C reads the image", c.c.Do(http.MethodGet, img.URL, nil), http.StatusNotFound, "")
	expectStatus(t, "an unknown file", a.c.Do(http.MethodGet, "/api/v1/knowledge/files/"+uuid.NewString(), nil), http.StatusNotFound, "")
	expectStatus(t, "C uploads", c.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+art.ID+"/files", nil, form(t, "x.txt", "text/plain", []byte("x"), nil)), http.StatusNotFound, "")
	expectStatus(t, "B uploads", b.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+art.ID+"/files", nil, form(t, "x.txt", "text/plain", []byte("x"), nil)), http.StatusForbidden, "article_read_only")
	expectStatus(t, "an empty file", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+art.ID+"/files", nil, form(t, "x.txt", "text/plain", nil, nil)), http.StatusBadRequest, "")
	expectStatus(t, "over 25 MiB", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+art.ID+"/files", nil,
		form(t, "big.bin", "application/octet-stream", make([]byte, 25<<20+1), nil)), http.StatusRequestEntityTooLarge, "file_too_large")

	expectStatus(t, "lock", a.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+art.ID, map[string]any{"locked": true}), http.StatusOK, "")
	expectStatus(t, "upload to a locked article", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+art.ID+"/files", nil,
		form(t, "x.txt", "text/plain", []byte("x"), nil)), http.StatusConflict, "article_locked")
	expectStatus(t, "unlock", a.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+art.ID, map[string]any{"locked": false}), http.StatusOK, "")

	// A document may only name its own article's files (K19).
	other := create(t, a, "", "workspace", "Other")
	foreign := uploadFile(t, a, other.ID, "x.png", "image/png", pngWithText(t, "x"))
	_, now := get(t, a, art.ID)
	r = saveDoc(a, art.ID, "Office", docOf(para(txt("x")), imageNode(foreign.URL, "x")), now.Version)
	expectStatus(t, "an image of another article's file", r, http.StatusBadRequest, "invalid_document")
	if p := problemOf(t, r).Path; p != "$.content[1]" {
		t.Errorf("invalid_document path = %q, want the image's", p)
	}

	// A paste from another article copies the file into this one (review 2).
	copyFile := func(p person, into, file string) *modtest.Response {
		return p.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+into+"/files/copy", map[string]any{"fileId": file})
	}
	r = copyFile(a, art.ID, foreign.ID)
	expectStatus(t, "copy Other's image into Office", r, http.StatusCreated, "")
	var copied fileJSON
	r.JSON(&copied)
	if copied.ID == foreign.ID || copied.Filename != foreign.Filename || copied.Size != foreign.Size || !copied.IsImage {
		t.Errorf("copy = %+v of %+v", copied, foreign)
	}
	if a1, a2 := a.c.Do(http.MethodGet, copied.URL, nil), a.c.Do(http.MethodGet, foreign.URL, nil); !bytes.Equal(a1.Body, a2.Body) {
		t.Error("the copy's bytes differ")
	}
	expectStatus(t, "B copies into an article B only reads", copyFile(b, art.ID, foreign.ID), http.StatusForbidden, "article_read_only")
	expectStatus(t, "C copies into an article C cannot read", copyFile(c, art.ID, foreign.ID), http.StatusNotFound, "")
	cPrivate := create(t, c, "", "private", "C's")
	cFile := uploadFile(t, c, cPrivate.ID, "c.txt", "text/plain", []byte("c"))
	expectStatus(t, "A copies a file of an article A cannot read", copyFile(a, other.ID, cFile.ID), http.StatusNotFound, "")
	expectStatus(t, "A copies an unknown file", copyFile(a, other.ID, uuid.NewString()), http.StatusNotFound, "")
	r = saveDoc(a, art.ID, "Office", docOf(map[string]any{"type": "fileAttachment", "attrs": map[string]any{"fileId": uuid.NewString()}}), now.Version)
	expectStatus(t, "an unknown file", r, http.StatusBadRequest, "invalid_document")
	r = saveDoc(a, art.ID, "Office", docOf(imageNode(strings.ToUpper(img.URL[len("/api/v1/knowledge/files/"):]), "x")), now.Version)
	expectStatus(t, "an upper-case src is not a path of the module", r, http.StatusBadRequest, "invalid_document")
	upperSrc := "/api/v1/knowledge/files/" + strings.ToUpper(img.ID)
	r = saveDoc(a, art.ID, "Office", docOf(imageNode(upperSrc, "The office"), imageNode(copied.URL, "copied"),
		map[string]any{"type": "fileAttachment", "attrs": map[string]any{"fileId": strings.ToUpper(svg.ID), "filename": "logo.svg", "size": svg.Size, "contentType": svg.ContentType}}), now.Version)
	expectStatus(t, "its own files, ids in upper case", r, http.StatusOK, "")
	if _, saved := get(t, a, art.ID); func() bool {
		raw, _ := json.Marshal(saved.Content)
		return strings.Contains(string(raw), strings.ToUpper(img.ID))
	}() {
		t.Error("an upper-case id was stored as sent")
	}
	// Stored content from before the stored form was lower-cased still
	// keeps its files (review 6).
	h.Exec(t, `UPDATE knowledge.articles SET content = replace(content::text, $2, upper($2))::jsonb WHERE id = $1`, art.ID, img.ID)
	h.Exec(t, `UPDATE knowledge.article_versions SET content = replace(content::text, $2, upper($2))::jsonb WHERE article_id = $1`, art.ID, img.ID)

	// The retention worker removes a file nothing names once its grace is
	// over, its object with it; a named one stays.
	stray := uploadFile(t, a, art.ID, "stray.txt", "text/plain", []byte("stray"))
	objects := func() int {
		n := 0
		_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				n++
			}
			return nil
		})
		return n
	}
	before := objects()
	h.Advance(8 * 24 * time.Hour)
	res, err := retentionCycle(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesPruned != 3 || res.ObjectsRemoved != 3 { // the stray, the foreign one Other never named, and C's
		t.Errorf("retention = %+v, want three files and their objects", res)
	}
	if objects() != before-3 {
		t.Errorf("%d objects, want %d", objects(), before-3)
	}
	// (The sessions have expired by now: the rows tell.)
	if h.Count(t, `SELECT count(*) FROM knowledge.article_files WHERE id = $1`, stray.ID) != 0 ||
		h.Count(t, `SELECT count(*) FROM knowledge.article_files WHERE id = $1`, img.ID) != 1 {
		t.Error("the stray stayed, or the named image went")
	}

	// Purging an article queues its files' objects too.
	h.Exec(t, `UPDATE knowledge.articles SET trashed_at = $2, trash_root = id WHERE id = $1`, art.ID, h.Now().Add(-31*24*time.Hour))
	if res, err = retentionCycle(t, h); err != nil || res.ObjectsRemoved != 3 {
		t.Errorf("after the purge: %+v, %v; want the three objects removed", res, err)
	}
}

type resolvedJSON struct {
	ID    string  `json:"id"`
	Title *string `json:"title"`
	Icon  *string `json:"icon"`
	State string  `json:"state"`
}

// Links: rewritten on every save, backlinks filtered by access, and the
// chips resolved without naming what the reader may not read (K21).
func TestLinks_BacklinksAndResolve(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)
	target := create(t, a, "", "workspace", "Target")
	secret := create(t, a, "", "private", "Salaries 2026")
	from := create(t, a, "", "workspace", "From")
	hidden := create(t, a, "", "private", "Private notes")
	gone := uuid.NewString()

	expectStatus(t, "From links", saveDoc(a, from.ID, "From", docOf(para(txt("See "), articleLink(target.ID, "Target"),
		txt(" and "), articleLink(secret.ID, "Salaries 2026"), txt(" and "), articleLink(gone, "Old"))), 1), http.StatusOK, "")
	expectStatus(t, "Private notes links", saveDoc(a, hidden.ID, "Private notes", docOf(para(articleLink(target.ID, "Target"))), 1), http.StatusOK, "")
	if n := h.Count(t, `SELECT count(*) FROM knowledge.article_links`); n != 3 {
		t.Errorf("%d links, want 3 (no row for a target that does not exist)", n)
	}
	// A link's label is never stored, so never answered (review 1).
	for _, p := range []person{a, b} {
		_, got := get(t, p, from.ID)
		raw, _ := json.Marshal(got.Content)
		if strings.Contains(string(raw), "Salaries") || strings.Contains(string(raw), `"label":"`) {
			t.Errorf("the article answers a link's label: %s", raw)
		}
	}
	r := b.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+from.ID+"/export", nil)
	expectStatus(t, "B exports From", r, http.StatusOK, "")
	if got := string(r.Body); strings.Contains(got, "Salaries") || got != "See [[Target]] and No access and No access\n" {
		t.Errorf("B's export = %q", got)
	}

	backlinks := func(p person, id string) []string {
		t.Helper()
		r := p.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+id+"/backlinks", nil)
		expectStatus(t, "backlinks", r, http.StatusOK, "")
		var out []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		r.JSON(&out)
		var titles []string
		for _, o := range out {
			titles = append(titles, o.Title)
		}
		return titles
	}
	if got := backlinks(a, target.ID); !slices.Equal(got, []string{"From", "Private notes"}) {
		t.Errorf("A's backlinks = %v", got)
	}
	if got := backlinks(b, target.ID); !slices.Equal(got, []string{"From"}) {
		t.Errorf("B's backlinks = %v, want only what B can read", got)
	}
	expectStatus(t, "B's backlinks of a private article", b.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+secret.ID+"/backlinks", nil), http.StatusNotFound, "")

	resolve := func(p person, ids ...string) []resolvedJSON {
		t.Helper()
		r := p.c.Do(http.MethodPost, "/api/v1/knowledge/links/resolve", map[string]any{"ids": ids})
		expectStatus(t, "resolve", r, http.StatusOK, "")
		var out []resolvedJSON
		r.JSON(&out)
		return out
	}
	got := resolve(b, target.ID, secret.ID, gone)
	if len(got) != 3 || got[0].State != "ok" || got[0].Title == nil || *got[0].Title != "Target" ||
		got[1].State != "no_access" || got[1].Title != nil || got[2].State != "deleted" || got[2].Title != nil {
		t.Errorf("B resolves %+v", got)
	}
	r = b.c.Do(http.MethodPost, "/api/v1/knowledge/links/resolve", map[string]any{"ids": []string{secret.ID}})
	if strings.Contains(string(r.Body), "Salaries") {
		t.Errorf("resolve named a title B may not read: %s", r.Body)
	}
	expectStatus(t, "trash the target", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+target.ID+"/trash", nil), http.StatusNoContent, "")
	if got := resolve(a, target.ID); got[0].State != "no_access" || got[0].Title != nil {
		t.Errorf("a trashed target resolves %+v", got)
	}
	many := make([]string, 201)
	for i := range many {
		many[i] = uuid.NewString()
	}
	expectStatus(t, "201 ids", a.c.Do(http.MethodPost, "/api/v1/knowledge/links/resolve", map[string]any{"ids": many}), http.StatusBadRequest, "")

	// A link's label never reaches the text search and the Assistant read.
	var text struct{ Text string }
	r = b.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+from.ID+"/text", nil)
	r.JSON(&text)
	if strings.Contains(text.Text, "Salaries") {
		t.Errorf("the text holds a link's label: %q", text.Text)
	}

	// A save without the links removes them; restoring the version with
	// them brings them back.
	_, now := get(t, a, from.ID)
	h.Advance(11 * time.Minute) // a version of its own, not coalesced
	expectStatus(t, "From unlinks", saveDoc(a, from.ID, "From", docOf(para(txt("nothing"))), now.Version), http.StatusOK, "")
	if n := h.Count(t, `SELECT count(*) FROM knowledge.article_links WHERE from_id = $1`, from.ID); n != 0 {
		t.Errorf("%d links left after the save", n)
	}
	r = a.c.Do(http.MethodPost, fmt.Sprintf("/api/v1/knowledge/articles/%s/versions/%d/restore", from.ID, now.Version), map[string]any{"expectedVersion": now.Version + 1})
	expectStatus(t, "restore the linking version", r, http.StatusOK, "")
	if n := h.Count(t, `SELECT count(*) FROM knowledge.article_links WHERE from_id = $1`, from.ID); n != 2 {
		t.Errorf("%d links after the restore, want 2", n)
	}
}

// Mentions: a notice to each newly mentioned person who may read the
// article, once per person per article per hour (K20).
func TestMentions_ThrottleAndAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)
	c := signIn(t, h)
	art := create(t, a, "", "private", "Plan")
	expectStatus(t, "share with B", setAccess(a, art.ID, "none", map[uuid.UUID]string{a.id: "write", b.id: "read"}, true), http.StatusOK, "")

	var notices func(uuid.UUID) int
	notices = func(user uuid.UUID) int {
		n := 0
		for _, x := range h.Notices() {
			if x.UserID == user {
				n++
			}
		}
		return n
	}
	version := int32(1)
	saveMentions := func(people ...person) {
		t.Helper()
		inline := []any{txt("Hi ")}
		for _, p := range people {
			inline = append(inline, mention(p.id, "someone"))
		}
		r := saveDoc(a, art.ID, "Plan", docOf(para(inline...)), version)
		expectStatus(t, "save", r, http.StatusOK, "")
		var saved struct{ Version int32 }
		r.JSON(&saved)
		version = saved.Version
	}
	// A new article's mentions are told too (review 10).
	r := a.c.Do(http.MethodPost, "/api/v1/knowledge/articles", map[string]any{"title": "Kickoff", "content": docOf(para(mention(b.id, "B")))})
	expectStatus(t, "create with a mention", r, http.StatusCreated, "")
	if notices(b.id) != 1 {
		t.Fatalf("B told %d times of the new article, want once", notices(b.id))
	}
	h.Exec(t, `DELETE FROM knowledge.mention_notices`)
	base := len(h.Notices())
	notices = func(user uuid.UUID) int {
		n := 0
		for _, x := range h.Notices()[base:] {
			if x.UserID == user {
				n++
			}
		}
		return n
	}
	saveMentions(a, b, c)
	if notices(b.id) != 1 || notices(c.id) != 0 || notices(a.id) != 0 {
		t.Fatalf("notices: B %d, C %d, A %d; want B told once, C (no access) and A (the author) never", notices(b.id), notices(c.id), notices(a.id))
	}
	for _, x := range h.Notices()[base:] {
		if x.UserID == b.id && !strings.Contains(x.Notice.Texts["en"].Subject, "Plan") {
			t.Errorf("notice = %+v", x.Notice)
		}
	}
	saveMentions(a, b, c) // still mentioned, not newly
	saveMentions()        // removed
	saveMentions(b)       // added again within the hour
	if notices(b.id) != 1 {
		t.Errorf("B told %d times within the hour, want once", notices(b.id))
	}
	h.Advance(61 * time.Minute)
	saveMentions()
	saveMentions(b)
	if notices(b.id) != 2 {
		t.Errorf("B told %d times after the hour, want twice", notices(b.id))
	}
}

// zipOf is a zip of the named entries.
func zipOf(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	names := make([]string, 0, len(entries))
	for k := range entries {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, name := range names {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(entries[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type importJSON struct {
	Created []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"created"`
	Warnings []string `json:"warnings"`
}

func importFile(p person, t *testing.T, filename string, data []byte, fields map[string]string) *modtest.Response {
	t.Helper()
	return p.c.Do(http.MethodPost, "/api/v1/knowledge/import", nil, form(t, filename, "application/octet-stream", data, fields))
}

// blockTypes is a document's block types, nested blocks in brackets.
func blockTypes(content any) string {
	list, _ := content.([]any)
	var parts []string
	for _, c := range list {
		m, _ := c.(map[string]any)
		typ, _ := m["type"].(string)
		if typ == "text" || typ == "hardBreak" || typ == "mention" || typ == "articleLink" || typ == "recordLink" {
			continue
		}
		if inner := blockTypes(m["content"]); inner != "" {
			typ += "[" + inner + "]"
		}
		parts = append(parts, typ)
	}
	return strings.Join(parts, " ")
}

// Export a subtree, import it back, and the structure, the links and the
// images survive (K23).
func TestExportImport_RoundTrip(t *testing.T) {
	t.Parallel()
	h, _ := filesHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)
	live := listenLive(t, h)

	root := create(t, a, "", "workspace", "Handbook")
	child := create(t, a, root.ID, "", "Onboarding")
	private := create(t, a, root.ID, "", "Salaries")
	expectStatus(t, "make Salaries private", setAccess(a, private.ID, "none", map[uuid.UUID]string{a.id: "write"}, true), http.StatusOK, "")
	img := uploadFile(t, a, root.ID, "office.png", "image/png", pngWithText(t, "x"))
	content := docOf(
		map[string]any{"type": "heading", "attrs": map[string]any{"level": 2}, "content": []any{txt("Welcome")}},
		map[string]any{"type": "callout", "attrs": map[string]any{"kind": "tip", "emoji": nil}, "content": []any{para(txt("Read this first"))}},
		map[string]any{"type": "details", "attrs": map[string]any{"open": false}, "content": []any{
			map[string]any{"type": "detailsSummary", "content": []any{txt("Why")}},
			map[string]any{"type": "detailsContent", "content": []any{para(txt("Because"))}},
		}},
		map[string]any{"type": "table", "content": []any{
			map[string]any{"type": "tableRow", "content": []any{
				map[string]any{"type": "tableHeader", "content": []any{para(txt("Who"))}},
				map[string]any{"type": "tableHeader", "content": []any{para(txt("What"))}}}},
			map[string]any{"type": "tableRow", "content": []any{
				map[string]any{"type": "tableCell", "content": []any{para(txt("Kari"))}},
				map[string]any{"type": "tableCell", "content": []any{para(txt("Lead"))}}}},
		}},
		map[string]any{"type": "taskList", "content": []any{
			map[string]any{"type": "taskItem", "attrs": map[string]any{"checked": true}, "content": []any{para(txt("Sign"))}}}},
		map[string]any{"type": "codeBlock", "attrs": map[string]any{"language": "bash"}, "content": []any{txt("make up")}},
		imageNode(img.URL, "The office"),
		para(txt("Start with "), articleLink(child.ID, "Onboarding"), txt(", not "), articleLink(private.ID, "Salaries")),
	)
	expectStatus(t, "save the handbook", saveDoc(a, root.ID, "Handbook", content, 1), http.StatusOK, "")
	expectStatus(t, "save onboarding", saveDoc(a, child.ID, "Onboarding", docOf(para(txt("Day one"))), 1), http.StatusOK, "")

	// One article without files is one .md.
	r := a.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+child.ID+"/export", nil)
	expectStatus(t, "export one", r, http.StatusOK, "")
	if !strings.HasPrefix(r.Header("Content-Type"), "text/markdown") || string(r.Body) != "Day one\n" ||
		r.Header("Content-Disposition") != "attachment; filename*=UTF-8''Onboarding.md" {
		t.Errorf("export one: %q %q %q", r.Header("Content-Type"), r.Header("Content-Disposition"), r.Body)
	}

	// B exports the subtree: Salaries, which B cannot read, is neither a
	// file nor a title.
	r = b.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+root.ID+"/export?subtree=true", nil)
	expectStatus(t, "export the subtree", r, http.StatusOK, "")
	if r.Header("Content-Type") != "application/zip" {
		t.Fatalf("export type %q", r.Header("Content-Type"))
	}
	zr, err := zip.NewReader(bytes.NewReader(r.Body), int64(len(r.Body)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	files := map[string]string{}
	for _, f := range zr.File {
		names = append(names, f.Name)
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		files[f.Name] = string(data)
	}
	slices.Sort(names)
	want := []string{"Handbook.md", "Handbook/Onboarding.md", "assets/" + img.ID + "-office.png"}
	if !slices.Equal(names, want) {
		t.Fatalf("zip = %v, want %v", names, want)
	}
	md := files["Handbook.md"]
	for _, w := range []string{"## Welcome", "> [!tip]", "> [!note]- Why", "| Who | What |", "- [x] Sign", "```bash", "![The office](assets/" + img.ID + "-office.png)", "Start with [[Onboarding]], not No access"} {
		if !strings.Contains(md, w) {
			t.Errorf("Handbook.md lacks %q:\n%s", w, md)
		}
	}

	// Import it back under a new parent.
	parent := create(t, a, "", "workspace", "Imported")
	live.drain(200 * time.Millisecond)
	r = importFile(a, t, "Handbook.zip", r.Body, map[string]string{"parentId": parent.ID})
	expectStatus(t, "import", r, http.StatusCreated, "")
	var res importJSON
	r.JSON(&res)
	if len(res.Created) != 2 || res.Created[0].Title != "Handbook" || res.Created[1].Title != "Onboarding" {
		t.Fatalf("created %+v", res.Created)
	}
	live.expect(t, treeEvent)
	_, imported := get(t, a, res.Created[0].ID)
	if imported.ParentID == nil || *imported.ParentID != parent.ID {
		t.Errorf("imported under %v, want %s", imported.ParentID, parent.ID)
	}
	_, original := get(t, a, root.ID)
	if got, want := blockTypes(imported.Content["content"]), blockTypes(original.Content["content"]); got != want {
		t.Errorf("imported structure\n%s\nwant\n%s", got, want)
	}
	raw, _ := json.Marshal(imported.Content)
	if !strings.Contains(string(raw), `"id":"`+res.Created[1].ID+`"`) {
		t.Errorf("the link to Onboarding does not name the imported Onboarding: %s", raw)
	}
	var importedImage string
	for _, blk := range imported.Content["content"].([]any) {
		if m := blk.(map[string]any); m["type"] == "image" {
			importedImage = m["attrs"].(map[string]any)["src"].(string)
		}
	}
	if importedImage == "" || importedImage == img.URL {
		t.Fatalf("the imported image is %q", importedImage)
	}
	expectStatus(t, "the imported image", a.c.Do(http.MethodGet, importedImage, nil), http.StatusOK, "")
	if got := len(func() []string {
		r := a.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+res.Created[1].ID+"/backlinks", nil)
		var out []map[string]any
		r.JSON(&out)
		titles := []string{}
		for _, o := range out {
			titles = append(titles, o["title"].(string))
		}
		return titles
	}()); got != 1 {
		t.Errorf("the imported Onboarding has %d backlinks, want 1", got)
	}
}

// Import: one .md, Obsidian's layout, and every limit (K23).
func TestImport_FilesAndLimits(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)

	r := importFile(a, t, "Ruter 0123456789abcdef0123456789abcdef.md", []byte("# Ruter\n\nHello [[Nowhere]]"), map[string]string{"section": "private"})
	expectStatus(t, "one .md", r, http.StatusCreated, "")
	var one importJSON
	r.JSON(&one)
	if len(one.Created) != 1 || one.Created[0].Title != "Ruter" {
		t.Fatalf("created %+v", one.Created)
	}
	_, art := get(t, a, one.Created[0].ID)
	if art.Section != "private" || blockTypes(art.Content["content"]) != "paragraph" {
		t.Errorf("the imported .md: section %s, blocks %s", art.Section, blockTypes(art.Content["content"]))
	}
	expectStatus(t, "B cannot read A's private import", func() *modtest.Response { r, _ := get(t, b, art.ID); return r }(), http.StatusNotFound, "")

	vault := zipOf(t, map[string][]byte{
		"Vault/.obsidian/app.json":   []byte("{}"),
		"Vault/Guides.md":            []byte("> [!warning] Careful\n> See [[Setup|the setup]]."),
		"Vault/Guides/Setup.md":      []byte("Steps"),
		"Vault/Guides/Deep/Note.md":  []byte("Deep"),
		"Vault/Unlinked/Orphan.md":   []byte("Alone"),
		"Vault/attachments/note.txt": []byte("not imported"),
	})
	r = importFile(a, t, "vault.zip", vault, nil)
	expectStatus(t, "a vault", r, http.StatusCreated, "")
	var v importJSON
	r.JSON(&v)
	var titles []string
	for _, c := range v.Created {
		titles = append(titles, c.Title)
	}
	if !slices.Equal(titles, []string{"Guides", "Deep", "Note", "Setup", "Unlinked", "Orphan"}) {
		t.Errorf("created %v", titles)
	}
	_, guides := get(t, a, v.Created[0].ID)
	if blockTypes(guides.Content["content"]) != "callout[paragraph paragraph]" || guides.Section != "workspace" {
		t.Errorf("Guides: %s in %s", blockTypes(guides.Content["content"]), guides.Section)
	}
	raw, _ := json.Marshal(guides.Content)
	if !strings.Contains(string(raw), v.Created[3].ID) {
		t.Errorf("the wiki link does not name Setup: %s", raw)
	}

	bad := func(name string, data []byte, fields map[string]string, status int, code string) {
		t.Helper()
		expectStatus(t, name, importFile(a, t, name, data, fields), status, code)
	}
	bad("evil.zip", zipOf(t, map[string][]byte{"../evil.md": []byte("x")}), nil, http.StatusBadRequest, "")
	bad("abs.zip", zipOf(t, map[string][]byte{"/etc/evil.md": []byte("x")}), nil, http.StatusBadRequest, "")
	bad("dots.zip", zipOf(t, map[string][]byte{"a/../../evil.md": []byte("x")}), nil, http.StatusBadRequest, "")
	bad("back.zip", zipOf(t, map[string][]byte{"a\\..\\evil.md": []byte("x")}), nil, http.StatusBadRequest, "")
	many := map[string][]byte{}
	for i := range 501 {
		many[fmt.Sprintf("n%03d.md", i)] = []byte("x")
	}
	bad("many.zip", zipOf(t, many), nil, http.StatusBadRequest, "")
	bad("deep.zip", zipOf(t, map[string][]byte{strings.Repeat("d/", 11) + "x.md": []byte("x")}), nil, http.StatusBadRequest, "")
	bad("tall.zip", zipOf(t, map[string][]byte{strings.Repeat("d/", 10) + "x.md": []byte("x"), "y.md": []byte("y")}), nil, http.StatusBadRequest, "too_deep")
	bad("none.zip", zipOf(t, map[string][]byte{"a.txt": []byte("x")}), nil, http.StatusBadRequest, "")
	bad("broken.zip", []byte("PK\x03\x04 not really"), nil, http.StatusBadRequest, "")
	bad("notes.docx.pdf", []byte("%PDF-1.4"), nil, http.StatusBadRequest, "")
	bad("x.md", []byte("x"), map[string]string{"parentId": uuid.NewString()}, http.StatusBadRequest, "parent_invalid")
	bad("x.md", []byte("x"), map[string]string{"section": "shared"}, http.StatusBadRequest, "")
	readOnly := create(t, b, "", "workspace", "B's")
	expectStatus(t, "make B's read-only for others", setAccess(b, readOnly.ID, "read", map[uuid.UUID]string{b.id: "write"}, true), http.StatusOK, "")
	bad("x.md", []byte("x"), map[string]string{"parentId": readOnly.ID}, http.StatusForbidden, "article_read_only")

	// A zip bomb: 51 files of 4 MiB each, far smaller compressed, is refused
	// on what it holds uncompressed, counted as read.
	big := bytes.Repeat([]byte("a"), 4<<20)
	bomb := map[string][]byte{}
	for i := range 51 {
		bomb[fmt.Sprintf("b%02d.md", i)] = big
	}
	data := zipOf(t, bomb)
	if len(data) > 2<<20 {
		t.Fatalf("the bomb is %d bytes compressed", len(data))
	}
	bad("bomb.zip", data, nil, http.StatusBadRequest, "")
	if n := h.Count(t, `SELECT count(*) FROM knowledge.articles WHERE title LIKE 'b%' AND title ~ '^b[0-9]{2}$'`); n != 0 {
		t.Errorf("the bomb created %d articles", n)
	}
}

// An export over its bounds is refused before anything is written (review
// 3). Not parallel: it lowers the module's bounds.
func TestExport_TooLarge(t *testing.T) {
	h := newHarness(t)
	a := signIn(t, h)
	root := create(t, a, "", "workspace", "Root")
	create(t, a, root.ID, "", "Child")
	restore := knowledge.SetExportLimits(1, 1<<30)
	defer restore()
	r := a.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+root.ID+"/export?subtree=true", nil)
	expectStatus(t, "two entries over a bound of one", r, http.StatusRequestEntityTooLarge, "export_too_large")
	knowledge.SetExportLimits(10, 10)
	expectStatus(t, "over the size bound", a.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+root.ID+"/export?subtree=true", nil),
		http.StatusRequestEntityTooLarge, "export_too_large")
}

// A third import at once is told to come back (review 4). Not parallel: it
// holds the module's import slots.
func TestImport_Busy(t *testing.T) {
	h := newHarness(t)
	a := signIn(t, h)
	release := knowledge.HoldImportSlots()
	r := importFile(a, t, "x.md", []byte("x"), nil)
	release()
	expectStatus(t, "an import while two run", r, http.StatusTooManyRequests, "import_busy")
	if r.Header("Retry-After") == "" {
		t.Error("no Retry-After")
	}
	expectStatus(t, "an import once they are done", importFile(a, t, "x.md", []byte("x"), nil), http.StatusCreated, "")
}

// An image that lands in a heading or a table cell is its alt text and is
// never uploaded (review 8).
func TestImport_ImagesOnlyWhereTheyShow(t *testing.T) {
	t.Parallel()
	h, _ := filesHarness(t)
	a := signIn(t, h)
	pic := pngWithText(t, "x")
	r := importFile(a, t, "v.zip", zipOf(t, map[string][]byte{
		"Page.md":    []byte("# ![in a heading](a.png)\n\n| ![in a cell](b.png) |\n|---|\n| x |\n\n![shown](c.png)\n"),
		"a.png":      pic,
		"b.png":      pic,
		"c.png":      pic,
		"Other.md":   []byte("![[c.png]]"),
		"Nothing.md": []byte("no images"),
	}), nil)
	expectStatus(t, "import", r, http.StatusCreated, "")
	var res importJSON
	r.JSON(&res)
	if n := h.Count(t, `SELECT count(*) FROM knowledge.article_files`); n != 2 {
		t.Errorf("%d files, want 2: c.png in Page and in Other, never a.png or b.png", n)
	}
}
