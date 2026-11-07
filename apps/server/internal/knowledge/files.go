package knowledge

// Files in articles (knowledge design K19): uploadKnowledgeFile and
// getKnowledgeFile. The bytes are in the object store under the module's
// scope, everything else about a file in knowledge.article_files. The two
// cannot be written in one transaction, so the order is product images': the
// object is written before the row's transaction and removed again when it
// does not commit; a row deleted later — by the retention worker, or with its
// article — queues its object in knowledge.file_object_deletions (a trigger),
// which the retention worker empties after the commit. The store is never
// called inside a transaction.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/imagemeta"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/doc"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/gen"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

const (
	// storageScope is the module's namespace in the object store.
	storageScope = "knowledge"
	// maxFileBytes is the largest file uploaded into an article (K19).
	maxFileBytes = 25 << 20
	// multipartMargin is the room the router's cap leaves for a form's
	// framing around its file.
	multipartMargin = 64 << 10
	// fileNameMax is a file name's length, in characters.
	fileNameMax = 255
	// fileCacheControl is what a served file carries: it is named by a fresh
	// random id and never replaced, but its article's access can change, so
	// only the browser's own cache, and briefly.
	fileCacheControl = "private, max-age=300"
	// fileContentSecurityPolicy is the policy a served file carries itself.
	fileContentSecurityPolicy = "default-src 'none'; sandbox"
	// objectCleanupTimeout bounds removing objects after a transaction, on a
	// context detached from the request's.
	objectCleanupTimeout = 30 * time.Second

	codeFileTooLarge       = "file_too_large"
	codeStorageUnavailable = "storage_unavailable"
)

// objects is the module's object store, or the error it could not be built
// with, which every call answers: a process that never touches a file still
// starts.
type objects struct {
	store storage.ObjectStore
	err   error
}

// newObjects is d.ObjectStore when a harness set one, otherwise the module's
// own scope over internal/storage.New.
func newObjects(d module.Deps) objects {
	if d.ObjectStore != nil {
		return objects{store: d.ObjectStore}
	}
	cfg := d.Config
	if cfg == nil {
		cfg = &config.Config{}
	}
	base, err := storage.New(cfg)
	if err != nil {
		return objects{err: fmt.Errorf("knowledge: build the object store: %w", err)}
	}
	scoped, err := storage.NewScope(base, storageScope)
	if err != nil {
		return objects{err: fmt.Errorf("knowledge: scope the object store: %w", err)}
	}
	return objects{store: scoped}
}

func (o objects) ready() error {
	if o.err != nil {
		return o.err
	}
	if o.store == nil {
		return storage.ErrNotConfigured
	}
	return nil
}

func (o objects) get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := o.ready(); err != nil {
		return nil, err
	}
	return o.store.Get(ctx, key)
}

func (o objects) put(ctx context.Context, key string, data []byte, contentType string) error {
	if err := o.ready(); err != nil {
		return err
	}
	return o.store.Put(ctx, key, bytes.NewReader(data), contentType)
}

func (o objects) delete(ctx context.Context, key string) error {
	if err := o.ready(); err != nil {
		return err
	}
	return o.store.Delete(ctx, key)
}

// removeObjects makes one attempt at removing each key, on a context detached
// from the request's, and logs a failure naming the article, never the key.
func removeObjects(ctx context.Context, d module.Deps, o objects, article uuid.UUID, keys ...string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), objectCleanupTimeout)
	defer cancel()
	for _, key := range keys {
		if err := o.delete(cleanup, key); err != nil && !errors.Is(err, storage.ErrNotExist) {
			d.Logger.ErrorContext(cleanup, "knowledge: a file object could not be removed from the store",
				"article_id", article.String(), "error", err.Error())
		}
	}
}

// fileObjectKey is where a new file's bytes go: its article and a fresh
// random name, never derived from the file's name.
func fileObjectKey(article uuid.UUID) string {
	return article.String() + "/" + uuid.New().String()
}

// fileURL is where a file is served; an image node's src is exactly it.
func fileURL(id uuid.UUID) string { return doc.FilePathPrefix + id.String() }

// upload is a file as stored: its bytes — an image's without its metadata —
// what it is called and what it is.
type upload struct {
	data        []byte
	filename    string
	contentType string
	isImage     bool
}

// classify judges a file's bytes (K19): a JPEG or a PNG, by its own bytes and
// stripped of its metadata, is an image; anything else, SVG included, is an
// attachment, its type kept for display only and never served.
func classify(data []byte, rawName, declared string) upload {
	u := upload{data: data, filename: cleanFileName(rawName, "file")}
	sniffed, _, _ := strings.Cut(http.DetectContentType(data), ";")
	sniffed = strings.TrimSpace(sniffed)
	if sniffed == "image/jpeg" || sniffed == "image/png" {
		if stripped, err := imagemeta.Strip(sniffed, data); err == nil {
			u.data, u.contentType, u.isImage = stripped, sniffed, true
			return u
		}
	}
	u.contentType = "application/octet-stream"
	if mediaType, _, err := mime.ParseMediaType(declared); err == nil && mediaType != "" && len(mediaType) <= 255 &&
		mediaType != "image/jpeg" && mediaType != "image/png" {
		u.contentType = mediaType
	} else if sniffed != "" && sniffed != "image/jpeg" && sniffed != "image/png" {
		u.contentType = sniffed
	}
	return u
}

// cleanFileName is a client's file name without any path it arrived with or
// control characters, at most 255 characters, fallback when nothing is left.
func cleanFileName(raw, fallback string) string {
	name := strings.TrimSpace(raw)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name))
	if name == "" || name == "." || name == ".." {
		return fallback
	}
	if runes := []rune(name); len(runes) > fileNameMax {
		name = string(runes[:fileNameMax])
	}
	return name
}

// fileForm is a multipart upload as read: the one file, its name and type,
// how many files it carried, and the form's other fields.
type fileForm struct {
	data       []byte
	filename   string
	declared   string
	files      int
	fields     map[string]string
	tooLarge   bool
	unreadable bool
}

// readFileForm reads the part file of a form, at most limit+1 bytes of it,
// and the form's short text fields. A read the router's own cap stops is too
// large as well.
func readFileForm(mr *multipart.Reader, limit int64) fileForm {
	f := fileForm{fields: map[string]string{}}
	if mr == nil {
		f.unreadable = true
		return f
	}
	fail := func(err error) fileForm {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			f.tooLarge = true
		} else {
			f.unreadable = true
		}
		return f
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return f
		}
		if err != nil {
			return fail(err)
		}
		name := part.FormName()
		if name != "file" {
			value, err := io.ReadAll(io.LimitReader(part, 1024))
			_ = part.Close()
			if err != nil {
				return fail(err)
			}
			f.fields[name] = strings.TrimSpace(string(value))
			continue
		}
		f.files++
		f.filename, f.declared = part.FileName(), part.Header.Get("Content-Type")
		data, err := io.ReadAll(io.LimitReader(part, limit+1))
		_ = part.Close()
		if err != nil {
			return fail(err)
		}
		if int64(len(data)) > limit {
			f.tooLarge = true
			return f
		}
		f.data = data
	}
}

// fileOut is a file row on the wire.
func fileOut(id uuid.UUID, filename, contentType string, size int64, isImage bool) gen.KnowledgeFile {
	return gen.KnowledgeFile{Id: id, Url: fileURL(id), Filename: filename, ContentType: contentType, Size: size, IsImage: isImage}
}

func storageUnavailable() gen.KnowledgeProblem {
	return problem(http.StatusServiceUnavailable, codeStorageUnavailable, "File storage is unavailable",
		"The file store could not be reached. Nothing was changed; try again.")
}

func fileTooLarge(limit string) gen.KnowledgeProblem {
	return problem(http.StatusRequestEntityTooLarge, codeFileTooLarge, "The file is too large", "A file can be at most "+limit+".")
}

// UploadKnowledgeFile uploads a file into an article the caller may write
// (K19). The article is judged first (404, 403, 409 locked), then the form;
// the object is written before the row's transaction, which judges the
// article again under its row lock.
func (s *server) UploadKnowledgeFile(ctx context.Context, req gen.UploadKnowledgeFileRequestObject) (gen.UploadKnowledgeFileResponseObject, error) {
	fail := func(err error) (gen.UploadKnowledgeFileResponseObject, error) {
		return answer(ctx, s, err, answers[gen.UploadKnowledgeFileResponseObject]{
			badRequest: func(p gen.KnowledgeProblem) gen.UploadKnowledgeFileResponseObject {
				return gen.UploadKnowledgeFile400ApplicationProblemPlusJSONResponse(p)
			},
			forbidden: func(p gen.KnowledgeProblem) gen.UploadKnowledgeFileResponseObject {
				return gen.UploadKnowledgeFile403ApplicationProblemPlusJSONResponse(p)
			},
			notFound: func() gen.UploadKnowledgeFileResponseObject { return gen.UploadKnowledgeFile404Response{} },
			conflict: func(p gen.KnowledgeProblem) gen.UploadKnowledgeFileResponseObject {
				return gen.UploadKnowledgeFile409ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	user := callerID(ctx)
	writable := func(q *store.Queries) error {
		m, err := writableMeta(ctx, q, req.Id, user)
		if err != nil {
			return err
		}
		if m.Locked {
			return locked()
		}
		return nil
	}
	if err := s.withReadTx(ctx, writable); err != nil {
		return fail(err)
	}
	form := readFileForm(req.Body, maxFileBytes)
	switch {
	case form.tooLarge:
		return gen.UploadKnowledgeFile413ApplicationProblemPlusJSONResponse(fileTooLarge("25 MiB")), nil
	case form.unreadable:
		return fail(invalid("file", "", "The request's body could not be read as a multipart form."))
	case form.files > 1:
		return fail(invalid("file", "", "A request uploads one file; this one carried more than one."))
	case form.files == 0 || len(form.data) == 0:
		return fail(invalid("file", "", "A file is required."))
	}
	u := classify(form.data, form.filename, form.declared)
	key := fileObjectKey(req.Id)
	if err := s.objects.put(ctx, key, u.data, u.contentType); err != nil {
		s.deps.Logger.ErrorContext(ctx, "knowledge: a file could not be written to the store", "article_id", req.Id.String(), "error", err.Error())
		return gen.UploadKnowledgeFile503ApplicationProblemPlusJSONResponse(storageUnavailable()), nil
	}
	id := uuid.New()
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if err := q.LockArticle(ctx, req.Id); err != nil {
			return fmt.Errorf("knowledge: lock article %s: %w", req.Id, err)
		}
		if err := writable(q); err != nil {
			return err
		}
		if err := q.InsertFile(ctx, store.InsertFileParams{
			ID: id, ArticleID: req.Id, ObjectKey: key, Filename: u.filename, ContentType: u.contentType,
			Size: int64(len(u.data)), IsImage: u.isImage, CreatedBy: &user, CreatedAt: s.deps.Clock(),
		}); err != nil {
			return fmt.Errorf("knowledge: record a file of %s: %w", req.Id, err)
		}
		return nil
	})
	if err != nil {
		removeObjects(ctx, s.deps, s.objects, req.Id, key)
		return fail(err)
	}
	return gen.UploadKnowledgeFile201JSONResponse(fileOut(id, u.filename, u.contentType, int64(len(u.data)), u.isImage)), nil
}

// fileDownload sets the headers a served file carries before the generated
// response writes its type, its length and the bytes.
type fileDownload struct {
	body        gen.GetKnowledgeFileResponseObject
	disposition string
}

func (r fileDownload) VisitGetKnowledgeFileResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", fileCacheControl)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", fileContentSecurityPolicy)
	if r.disposition != "" {
		w.Header().Set("Content-Disposition", r.disposition)
	}
	return r.body.VisitGetKnowledgeFileResponse(w)
}

// GetKnowledgeFile streams a file of an article the caller can read (K19): an
// image inline with its type, anything else as a download of
// application/octet-stream. A file of an article the caller cannot read is
// the bare 404 an unknown id is.
func (s *server) GetKnowledgeFile(ctx context.Context, req gen.GetKnowledgeFileRequestObject) (gen.GetKnowledgeFileResponseObject, error) {
	user := callerID(ctx)
	var f store.GetFileRow
	err := s.withReadTx(ctx, func(q *store.Queries) error {
		var err error
		f, err = q.GetFile(ctx, req.FileId)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("knowledge: read file %s: %w", req.FileId, err)
		}
		_, err = readableMeta(ctx, q, f.ArticleID, user)
		return err
	})
	if isNotFound(err) {
		return fileDownload{body: gen.GetKnowledgeFile404Response{}}, nil
	}
	if err != nil {
		return nil, err
	}
	body, err := s.objects.get(ctx, f.ObjectKey)
	if errors.Is(err, storage.ErrNotExist) {
		return fileDownload{body: gen.GetKnowledgeFile404Response{}}, nil
	}
	if err != nil {
		s.deps.Logger.ErrorContext(ctx, "knowledge: a file could not be read from the store", "article_id", f.ArticleID.String(), "error", err.Error())
		return fileDownload{body: gen.GetKnowledgeFile503ApplicationProblemPlusJSONResponse(storageUnavailable())}, nil
	}
	if f.IsImage {
		return fileDownload{
			body:        gen.GetKnowledgeFile200AsteriskResponse{Body: body, ContentType: f.ContentType, ContentLength: f.Size},
			disposition: "inline; filename*=UTF-8''" + rfc5987(f.Filename),
		}, nil
	}
	return fileDownload{
		body:        gen.GetKnowledgeFile200AsteriskResponse{Body: body, ContentType: "application/octet-stream", ContentLength: f.Size},
		disposition: "attachment; filename*=UTF-8''" + rfc5987(f.Filename),
	}, nil
}

// rfc5987 percent-encodes every byte that is not an RFC 5987 attr-char.
func rfc5987(value string) string {
	const attrChars = "!#$&+-.^_`|~"
	var b strings.Builder
	for _, c := range []byte(value) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte(attrChars, c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// checkFiles refuses (400 invalid_document, with the path of the first node
// at fault) a document naming a file that is not one of the article's own
// (K19). The files it finds are read FOR SHARE, so the retention worker
// cannot delete one while the save that names it commits.
func checkFiles(ctx context.Context, q *store.Queries, article uuid.UUID, refs doc.Refs) error {
	if len(refs.Files) == 0 {
		return nil
	}
	const reason = "an image or a file names something that is not a file of this article"
	ids := make([]uuid.UUID, 0, len(refs.Files))
	for _, f := range refs.Files {
		id, err := uuid.Parse(f)
		if err != nil {
			return invalidDocument(refs.FilePaths[f], reason)
		}
		ids = append(ids, id)
	}
	found, err := q.ArticleFileIDs(ctx, store.ArticleFileIDsParams{ArticleID: article, Ids: ids})
	if err != nil {
		return fmt.Errorf("knowledge: check the files of %s: %w", article, err)
	}
	have := map[uuid.UUID]bool{}
	for _, id := range found {
		have[id] = true
	}
	for i, id := range ids {
		if !have[id] {
			return invalidDocument(refs.FilePaths[refs.Files[i]], reason)
		}
	}
	return nil
}

// invalidDocument is the 400 invalid_document of a rule the allowlist cannot
// see alone.
func invalidDocument(path, reason string) error {
	if path == "" {
		path = "$"
	}
	f := fieldErrors{code: codeInvalidDocument}
	f.add("content", reason)
	p := problem(http.StatusBadRequest, codeInvalidDocument, "Invalid document", "")
	p.Errors = &f.errs
	p.Path = &path
	return &refusal{status: http.StatusBadRequest, problem: p}
}

// CopyKnowledgeFile copies a file of an article the caller can read into an
// article the caller may write (K19): a paste from one article into another.
// The copy is a new file with its own object, so each article's files stay
// its own. The target is judged first (404, 403, 409 locked), then the
// source (404 when the caller cannot read its article); the bytes are copied
// before the row's transaction, which judges the target again under its row
// lock.
func (s *server) CopyKnowledgeFile(ctx context.Context, req gen.CopyKnowledgeFileRequestObject) (gen.CopyKnowledgeFileResponseObject, error) {
	fail := func(err error) (gen.CopyKnowledgeFileResponseObject, error) {
		return answer(ctx, s, err, answers[gen.CopyKnowledgeFileResponseObject]{
			badRequest: func(p gen.KnowledgeProblem) gen.CopyKnowledgeFileResponseObject {
				return gen.CopyKnowledgeFile400ApplicationProblemPlusJSONResponse(p)
			},
			forbidden: func(p gen.KnowledgeProblem) gen.CopyKnowledgeFileResponseObject {
				return gen.CopyKnowledgeFile403ApplicationProblemPlusJSONResponse(p)
			},
			notFound: func() gen.CopyKnowledgeFileResponseObject { return gen.CopyKnowledgeFile404Response{} },
			conflict: func(p gen.KnowledgeProblem) gen.CopyKnowledgeFileResponseObject {
				return gen.CopyKnowledgeFile409ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	if req.Body == nil {
		return fail(invalid("fileId", "", "A file is required."))
	}
	user := callerID(ctx)
	writable := func(q *store.Queries) error {
		m, err := writableMeta(ctx, q, req.Id, user)
		if err != nil {
			return err
		}
		if m.Locked {
			return locked()
		}
		return nil
	}
	var src store.GetFileRow
	err := s.withReadTx(ctx, func(q *store.Queries) error {
		if err := writable(q); err != nil {
			return err
		}
		var err error
		src, err = q.GetFile(ctx, req.Body.FileId)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("knowledge: read file %s: %w", req.Body.FileId, err)
		}
		_, err = readableMeta(ctx, q, src.ArticleID, user)
		return err
	})
	if err != nil {
		return fail(err)
	}
	body, err := s.objects.get(ctx, src.ObjectKey)
	if errors.Is(err, storage.ErrNotExist) {
		return gen.CopyKnowledgeFile404Response{}, nil
	}
	if err != nil {
		s.deps.Logger.ErrorContext(ctx, "knowledge: a file could not be read for a copy", "article_id", src.ArticleID.String(), "error", err.Error())
		return gen.CopyKnowledgeFile503ApplicationProblemPlusJSONResponse(storageUnavailable()), nil
	}
	data, err := io.ReadAll(io.LimitReader(body, maxFileBytes+1))
	_ = body.Close()
	if err != nil {
		s.deps.Logger.ErrorContext(ctx, "knowledge: a file could not be read for a copy", "article_id", src.ArticleID.String(), "error", err.Error())
		return gen.CopyKnowledgeFile503ApplicationProblemPlusJSONResponse(storageUnavailable()), nil
	}
	key := fileObjectKey(req.Id)
	if err := s.objects.put(ctx, key, data, src.ContentType); err != nil {
		s.deps.Logger.ErrorContext(ctx, "knowledge: a copied file could not be written to the store", "article_id", req.Id.String(), "error", err.Error())
		return gen.CopyKnowledgeFile503ApplicationProblemPlusJSONResponse(storageUnavailable()), nil
	}
	id := uuid.New()
	err = s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if err := q.LockArticle(ctx, req.Id); err != nil {
			return fmt.Errorf("knowledge: lock article %s: %w", req.Id, err)
		}
		if err := writable(q); err != nil {
			return err
		}
		return q.InsertFile(ctx, store.InsertFileParams{
			ID: id, ArticleID: req.Id, ObjectKey: key, Filename: src.Filename, ContentType: src.ContentType,
			Size: int64(len(data)), IsImage: src.IsImage, CreatedBy: &user, CreatedAt: s.deps.Clock(),
		})
	})
	if err != nil {
		removeObjects(ctx, s.deps, s.objects, req.Id, key)
		return fail(err)
	}
	return gen.CopyKnowledgeFile201JSONResponse(fileOut(id, src.Filename, src.ContentType, int64(len(data)), src.IsImage)), nil
}
