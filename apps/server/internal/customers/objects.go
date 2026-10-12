package customers

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

// This file is the object store (customer attachments design D1, D2), the
// module's second call out beside the user directory. An attachment's bytes
// live in the store under the module's own scope and its metadata in
// customers.customer_attachments, and the two cannot be written in one
// transaction, so the store is reached only through the three accessors
// below, and none of them is ever called from inside a db.WithTx of this
// module: a slow store under the customer's lock would starve the pool as a
// slow directory would (actor.go's rule). Hence the orders attachments.go
// keeps: the object is written before the row's transaction and removed
// after a refusal; a download reads the row on the pool, then the object; a
// delete removes the row and queues the object in one transaction, then
// removes the object once it committed (object_deletions.go).
//
// The rest is Staff's port of Expenses' receipt pattern
// (internal/staff/objects.go, internal/expenses/attachments.go), copied
// because no module imports another: the bounds, the sniffing, the file
// name, the disposition and the rate limit.

// storageScope is this module's namespace in the object store. Every key the
// module writes is relative to it, and the scoped store refuses to be handed
// its own prefix, so no key here can reach another module's objects.
const storageScope = "customers"

// The bounds of design D2, Staff's values. maxAttachmentRequestBytes is the
// router's cap for the upload (module.RouterOptions.BodyLimits): the file
// plus room for the multipart framing and the one small field beside it,
// Expenses' margin.
const (
	maxAttachmentBytes        = 10 * 1024 * 1024
	maxAttachmentRequestBytes = maxAttachmentBytes + 64*1024
	maxAttachmentsPerCustomer = 200
	attachmentNameMaxLength   = 255
)

// objectCleanupTimeout bounds an object delete that runs on a context
// detached from the request's own: a compensation after a refusal, or the
// one attempt after a delete has committed. Detached so a client that closes
// the tab does not leave the bytes behind; bounded so a store that has
// stopped answering does not hold the request for ever.
const objectCleanupTimeout = 30 * time.Second

// attachmentContentSecurityPolicy is the policy a served attachment carries
// itself: nothing may be fetched, and sandbox puts it in an opaque origin —
// the last bridge between a PDF's scripting and this app's origin.
const attachmentContentSecurityPolicy = "default-src 'none'; sandbox"

// attachmentFormField is the multipart part the contract names for the file,
// and attachmentEntryField the one small field beside it; another name is
// not guessed at.
const (
	attachmentFormField  = "file"
	attachmentEntryField = "timelineEntryId"
)

// attachmentFallbackName is an attachment's name when the client sent no
// usable one: the column is NOT NULL, and a download needs something to
// offer.
const attachmentFallbackName = "attachment"

// bodyLimits raises the router's request-body cap for the attachment upload
// and the CSV import (import.go); every other operation keeps the platform
// default (1 MiB).
var bodyLimits = map[string]int64{
	"postCustomersByIdAttachments": maxAttachmentRequestBytes,
	"postCustomersImport":          maxImportRequestBytes,
}

// attachmentUploadsPerHour is how many uploads one client address may make.
// Two hundred attachments bound a customer, and nothing bounds how many
// customers there are, so the upload carries the platform's rate limit —
// keyed by client address, as every policy is, which is why it is Expenses'
// and Staff's six hundred: an office behind one address shares it.
const attachmentUploadsPerHour = 600

// policyAttachmentUpload is that bound as the platform states one.
var policyAttachmentUpload = ratelimit.Policy{
	Name:    "CustomerAttachmentUpload",
	Limit:   attachmentUploadsPerHour,
	Window:  time.Hour,
	Message: "Too many attachment uploads. Please wait a little before adding more.",
}

// moduleObjectStore is this module's object store: d.ObjectStore when a
// harness set one, otherwise the one Config selects (internal/storage.New),
// either way wrapped in the module's own scope. A store that cannot be built
// is an error the caller keeps: a process that never touches an attachment
// starts without storage and fails only what needs it.
func moduleObjectStore(d module.Deps) (storage.ObjectStore, error) {
	if d.ObjectStore != nil {
		scoped, err := storage.NewScope(d.ObjectStore, storageScope)
		if err != nil {
			return nil, fmt.Errorf("customers: scope the object store: %w", err)
		}
		return scoped, nil
	}
	cfg := d.Config
	if cfg == nil {
		cfg = &config.Config{}
	}
	base, err := storage.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("customers: build the object store: %w", err)
	}
	scoped, err := storage.NewScope(base, storageScope)
	if err != nil {
		return nil, fmt.Errorf("customers: scope the object store: %w", err)
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
// this module may reach it. None is called inside a transaction, by
// construction: every caller is in attachments.go or object_deletions.go,
// outside db.WithTx.
func (s *server) objectPut(ctx context.Context, key string, r io.Reader, contentType string) error {
	store, err := s.objectStore()
	if err != nil {
		return err
	}
	return store.Put(ctx, key, r, contentType)
}

func (s *server) objectGet(ctx context.Context, key string) (io.ReadCloser, error) {
	store, err := s.objectStore()
	if err != nil {
		return nil, err
	}
	return store.Get(ctx, key)
}

func (s *server) objectDelete(ctx context.Context, key string) error {
	store, err := s.objectStore()
	if err != nil {
		return err
	}
	return store.Delete(ctx, key)
}

// removeObject makes one attempt at removing an object nothing points at —
// a compensation after a refused upload — on a context detached from the
// request's and bounded by objectCleanupTimeout, and logs a failure naming
// the customer rather than the key: a key in a server log helps nobody.
func (s *server) removeObject(ctx context.Context, customerID int32, key string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), objectCleanupTimeout)
	defer cancel()
	if err := s.objectDelete(cleanup, key); err != nil {
		s.logger().ErrorContext(cleanup, "customers: an attachment object could not be removed from the store",
			"customer_id", customerID, "error", err.Error())
	}
}

// attachmentObjectKey is where an attachment's bytes go: a fresh random name
// under attachments/, relative to the module's scope, naming no customer
// (design D1) — a merge moves the row, and the store has no rename — and
// never derived from the file's name, so no caller chooses a key or reads
// anything out of one.
func attachmentObjectKey() string {
	return "attachments/" + uuid.New().String()
}

// attachmentType is one of design D2's four formats: what it is stored and
// served as, the content types a client may declare it with, its extensions,
// and whether a browser is asked to show it in place.
type attachmentType struct {
	ContentType string
	Declared    []string
	Extensions  []string
	// Inline is false for HEIC: only Safari renders one, so everywhere else
	// an inline HEIC is a blank frame, while a download opens in the viewer
	// the operating system already has.
	Inline bool
}

var attachmentTypes = []attachmentType{
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

// sniffAttachmentType is the type data really is, from its own leading bytes.
func sniffAttachmentType(data []byte) (attachmentType, bool) {
	sniffed := "image/heic"
	if !isHEIC(data) {
		sniffed, _, _ = strings.Cut(http.DetectContentType(data), ";")
		sniffed = strings.TrimSpace(sniffed)
	}
	for _, t := range attachmentTypes {
		if strings.EqualFold(t.ContentType, sniffed) {
			return t, true
		}
	}
	return attachmentType{}, false
}

func familyOfDeclared(mediaType string) (attachmentType, bool) {
	for _, t := range attachmentTypes {
		if slices.ContainsFunc(t.Declared, func(d string) bool { return strings.EqualFold(d, mediaType) }) {
			return t, true
		}
	}
	return attachmentType{}, false
}

func familyOfExtension(ext string) (attachmentType, bool) {
	for _, t := range attachmentTypes {
		if slices.Contains(t.Extensions, ext) {
			return t, true
		}
	}
	return attachmentType{}, false
}

// attachmentTypeOf is the type an attachment is stored under: the one its
// bytes are, which must be one of the four and must not be contradicted by
// what the client called it — a PNG named .pdf is refused, and so is a PDF
// sent as image/png. A declared type or an extension the four do not claim
// (a phone's application/octet-stream, no type at all, a name without an
// extension) is not evidence either way.
func attachmentTypeOf(declared, fileName string, data []byte) (attachmentType, bool) {
	t, ok := sniffAttachmentType(data)
	if !ok {
		return attachmentType{}, false
	}
	if mediaType, _, err := mime.ParseMediaType(declared); err == nil && mediaType != "" {
		if other, known := familyOfDeclared(mediaType); known && other.ContentType != t.ContentType {
			return attachmentType{}, false
		}
	}
	if other, known := familyOfExtension(strings.ToLower(path.Ext(fileName))); known && other.ContentType != t.ContentType {
		return attachmentType{}, false
	}
	return t, true
}

// storedAttachmentType is the type a stored attachment is served as; a row
// that somehow names another is served as a file to save, the safe way round.
func storedAttachmentType(contentType string) attachmentType {
	for _, t := range attachmentTypes {
		if t.ContentType == contentType {
			return t
		}
	}
	return attachmentType{ContentType: contentType}
}

// attachmentFileName is the name an attachment keeps: the client's own
// without any path it arrived with — either separator — and without control
// characters, capped at the column's 255 characters, and never part of the
// object key.
func attachmentFileName(raw string) string {
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
		return attachmentFallbackName
	}
	if runes := []rune(name); len(runes) > attachmentNameMaxLength {
		name = string(runes[:attachmentNameMaxLength])
	}
	return name
}

// contentDisposition names an attachment for the browser — in place for the
// types every browser renders, a file to save for HEIC — RFC 5987 encoded,
// so a Norwegian name arrives intact and a quote in it cannot break the
// header.
func contentDisposition(t attachmentType, fileName string) string {
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
