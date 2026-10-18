package staff

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is the object store, the module's third call out beside the user
// directory and the mail (design S14, §4 Locking; plan reading 1). A
// document's bytes live in the store under the module's own scope and its
// metadata in staff.documents, and the two cannot be written in one
// transaction, so the store is reached only through the three accessors
// below — reported to the contract-call hook and refused inside any of the
// module's transactions, exactly as contractscalls.go's are: a slow store
// under a person's lock starves the pool as a slow directory would. Hence the
// orders documents.go keeps: the object is written before the row's
// transaction and removed after a refusal; a download reads the row in a
// snapshot, then the object outside it; a delete removes the row and queues
// the object in one transaction, then removes the object once it committed.
//
// The rest is Expenses' receipt pattern (internal/expenses/attachments.go),
// ported because no module imports another: the bounds, the sniffing, the
// file name and the rate limit.

// storageScope is this module's namespace in the object store. Every key the
// module writes is relative to it, and the scoped store refuses to be handed
// its own prefix, so no key here can reach another module's objects.
const storageScope = "staff"

// The bounds of design S14. maxDocumentRequestBytes is the router's cap for
// the upload (module.RouterOptions.BodyLimits): the file plus room for the
// multipart framing and the three small fields beside it, Expenses' margin.
const (
	maxDocumentBytes        = 10 * 1024 * 1024
	maxDocumentRequestBytes = maxDocumentBytes + 64*1024
	maxDocumentsPerPerson   = 200
	documentNameMaxLength   = 255
)

// objectCleanupTimeout bounds an object delete that runs on a context
// detached from the request's own: a compensation after a refusal, or the
// one attempt after a delete has committed. Detached so a client that closes
// the tab does not leave the bytes behind; bounded so a store that has
// stopped answering does not hold the request for ever.
const objectCleanupTimeout = 30 * time.Second

// documentContentSecurityPolicy is the policy a served document carries
// itself: nothing may be fetched, and sandbox puts it in an opaque origin —
// the last bridge between a PDF's scripting and this app's origin.
const documentContentSecurityPolicy = "default-src 'none'; sandbox"

// documentFormField is the multipart part the contract names for the file;
// another name is not guessed at.
const documentFormField = "file"

// documentFallbackName is a document's name when the client sent no usable
// one: the column is NOT NULL, and a download needs something to offer.
const documentFallbackName = "document"

// bodyLimits raises the router's request-body cap for the document upload
// and the register's import (people_import.go); every other operation keeps
// the platform default (1 MiB).
var bodyLimits = map[string]int64{
	"postStaffPeopleByIdDocuments": maxDocumentRequestBytes,
	"postStaffPeopleImport":        maxImportRequestBytes,
}

// documentUploadsPerHour is how many uploads one client address may make. Two
// hundred documents bound a person, and nothing bounds how many people HR may
// create, so the upload carries the platform's rate limit — keyed by client
// address, as every policy is, which is why it is Expenses' six hundred: an
// office behind one address shares it.
const documentUploadsPerHour = 600

// policyDocumentUpload is that bound as the platform states one.
var policyDocumentUpload = ratelimit.Policy{
	Name:    "StaffDocumentUpload",
	Limit:   documentUploadsPerHour,
	Window:  time.Hour,
	Message: "Too many document uploads. Please wait a little before adding more.",
}

// limits maps each rate-limited operationId to its policy: the upload, the
// one operation that writes bytes outside the database, and the account runs,
// which call identity and may mail (accounts_run.go).
var limits = map[string]ratelimit.Policy{
	"postStaffPeopleByIdDocuments":      policyDocumentUpload,
	"postStaffAccountsRun":              policyAccountsRun,
	"postStaffPeopleByIdAccountRun":     policyAccountAction,
	"postStaffPeopleByIdAccountDisable": policyAccountAction,
}

// newServer builds the module's operations over d. It never fails: an object
// store that cannot be built is kept as its error, which every object call
// answers (objectStore), so a process — the API, or a worker that never
// touches a document — starts without storage and fails only what needs it.
func newServer(d module.Deps) *server {
	objects, err := moduleObjectStore(d)
	return &server{deps: d, objects: objects, objectsErr: err, payroll: map[string]PayrollExporter{payrollFormatCSV: csvExporter{}}}
}

// moduleObjectStore is this module's object store: d.ObjectStore when a
// harness set one, otherwise the module's own scoped wrapper over
// internal/storage.New — Expenses' construction. A nil d.Config is the
// white-box tests' bare Deps, which an empty config stands in for: the
// unconfigured, fail-closed store.
func moduleObjectStore(d module.Deps) (storage.ObjectStore, error) {
	if d.ObjectStore != nil {
		return d.ObjectStore, nil
	}
	cfg := d.Config
	if cfg == nil {
		cfg = &config.Config{}
	}
	base, err := storage.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("staff: build the object store: %w", err)
	}
	scoped, err := storage.NewScope(base, storageScope)
	if err != nil {
		return nil, fmt.Errorf("staff: scope the object store: %w", err)
	}
	return scoped, nil
}

// objectStore is the store an accessor calls, or the error it was built with.
func (s *server) objectStore() (storage.ObjectStore, error) {
	if s.objectsErr != nil {
		return nil, s.objectsErr
	}
	if s.objects == nil {
		return nil, storage.ErrNotConfigured
	}
	return s.objects, nil
}

// objectPut, objectGet and objectDelete are the object store, the only way
// this module may reach it: each reports its call and refuses one made inside
// a transaction (noteContractCall).
func (s *server) objectPut(ctx context.Context, key string, r io.Reader, contentType string) error {
	if err := noteContractCall(ctx, "ObjectStore.Put"); err != nil {
		return err
	}
	store, err := s.objectStore()
	if err != nil {
		return err
	}
	return store.Put(ctx, key, r, contentType)
}

func (s *server) objectGet(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := noteContractCall(ctx, "ObjectStore.Get"); err != nil {
		return nil, err
	}
	store, err := s.objectStore()
	if err != nil {
		return nil, err
	}
	return store.Get(ctx, key)
}

func (s *server) objectDelete(ctx context.Context, key string) error {
	if err := noteContractCall(ctx, "ObjectStore.Delete"); err != nil {
		return err
	}
	store, err := s.objectStore()
	if err != nil {
		return err
	}
	return store.Delete(ctx, key)
}

// removeObject makes one attempt at removing an object, on a context
// detached from the request's and bounded by objectCleanupTimeout, and logs a
// failure naming the document's person rather than the key — a key in a
// server log helps nobody. It answers whether the store confirmed.
func (s *server) removeObject(ctx context.Context, personID int64, key string) bool {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), objectCleanupTimeout)
	defer cancel()
	if err := s.objectDelete(cleanup, key); err != nil {
		s.deps.Logger.ErrorContext(cleanup, "staff: a document object could not be removed from the store",
			"person_id", personID, "error", err.Error())
		return false
	}
	return true
}

// documentObjectKey is where a document's bytes go: the person and a fresh
// random name, never derived from the file's name, so no caller chooses a key
// or reads anything out of one. It is relative to the module's scope.
func documentObjectKey(personID int64) string {
	return fmt.Sprintf("people/%d/%s", personID, uuid.New().String())
}

// documentType is one of design S14's four formats: what it is stored and
// served as, the content types a client may declare it with, its extensions,
// and whether a browser is asked to show it in place.
type documentType struct {
	ContentType string
	Declared    []string
	Extensions  []string
	// Inline is false for HEIC: only Safari renders one, so everywhere else
	// an inline HEIC is a blank frame, while a download opens in the viewer
	// the operating system already has.
	Inline bool
}

var documentTypes = []documentType{
	{ContentType: "application/pdf", Declared: []string{"application/pdf", "application/x-pdf"}, Extensions: []string{".pdf"}, Inline: true},
	{ContentType: "image/jpeg", Declared: []string{"image/jpeg", "image/jpg", "image/pjpeg"}, Extensions: []string{".jpg", ".jpeg", ".jpe"}, Inline: true},
	{ContentType: "image/png", Declared: []string{"image/png"}, Extensions: []string{".png"}, Inline: true},
	{
		ContentType: "image/heic",
		Declared:    []string{"image/heic", "image/heif", "image/heic-sequence", "image/heif-sequence"},
		Extensions:  []string{".heic", ".heif"},
	},
}

// heicBrands are the ISO base media major brands of a HEIF still image.
// net/http's sniffer does not know HEIC, so the brand is read here; only the
// major brand decides, since an ordinary video lists mif1 among its
// compatible ones.
var heicBrands = []string{"heic", "heix", "heim", "heis", "hevc", "hevx", "hevm", "hevs", "mif1", "msf1"}

func isHEIC(data []byte) bool {
	return len(data) >= 12 && string(data[4:8]) == "ftyp" && slices.Contains(heicBrands, string(data[8:12]))
}

// sniffDocumentType is the type data really is, from its own leading bytes.
func sniffDocumentType(data []byte) (documentType, bool) {
	sniffed := "image/heic"
	if !isHEIC(data) {
		sniffed, _, _ = strings.Cut(http.DetectContentType(data), ";")
		sniffed = strings.TrimSpace(sniffed)
	}
	for _, t := range documentTypes {
		if strings.EqualFold(t.ContentType, sniffed) {
			return t, true
		}
	}
	return documentType{}, false
}

func familyOfDeclared(mediaType string) (documentType, bool) {
	for _, t := range documentTypes {
		if slices.ContainsFunc(t.Declared, func(d string) bool { return strings.EqualFold(d, mediaType) }) {
			return t, true
		}
	}
	return documentType{}, false
}

func familyOfExtension(ext string) (documentType, bool) {
	for _, t := range documentTypes {
		if slices.Contains(t.Extensions, ext) {
			return t, true
		}
	}
	return documentType{}, false
}

// documentTypeOf is the type a document is stored under: the one its bytes
// are, which must be one of the four and must not be contradicted by what
// the client called it — a PNG named .pdf is refused, and so is a PDF sent as
// image/png. A declared type or an extension the four do not claim (a phone's
// application/octet-stream, no type at all, a name without an extension) is
// not evidence either way.
func documentTypeOf(declared, fileName string, data []byte) (documentType, bool) {
	t, ok := sniffDocumentType(data)
	if !ok {
		return documentType{}, false
	}
	if mediaType, _, err := mime.ParseMediaType(declared); err == nil && mediaType != "" {
		if other, known := familyOfDeclared(mediaType); known && other.ContentType != t.ContentType {
			return documentType{}, false
		}
	}
	if other, known := familyOfExtension(strings.ToLower(path.Ext(fileName))); known && other.ContentType != t.ContentType {
		return documentType{}, false
	}
	return t, true
}

// storedDocumentType is the type a stored document is served as; a row that
// somehow names another is served as a file to save, the safe way round.
func storedDocumentType(contentType string) documentType {
	for _, t := range documentTypes {
		if t.ContentType == contentType {
			return t
		}
	}
	return documentType{ContentType: contentType}
}

// documentFileName is the name a document keeps: the client's own without any
// path it arrived with — either separator — and without control characters,
// capped at the column's 255 characters, and never part of the object key.
func documentFileName(raw string) string {
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
		return documentFallbackName
	}
	if runes := []rune(name); len(runes) > documentNameMaxLength {
		name = string(runes[:documentNameMaxLength])
	}
	return name
}

// contentDisposition names a document for the browser — in place for the
// types every browser renders, a file to save for HEIC — RFC 5987 encoded, so
// a Norwegian name arrives intact and a quote in it cannot break the header.
func contentDisposition(t documentType, fileName string) string {
	kind := "attachment"
	if t.Inline {
		kind = "inline"
	}
	return fmt.Sprintf("%s; filename*=UTF-8''%s", kind, rfc5987Encode(fileName))
}

// rfc5987Encode percent-encodes every byte that is not an RFC 5987
// attr-char.
func rfc5987Encode(value string) string {
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
