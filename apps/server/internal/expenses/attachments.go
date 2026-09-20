package expenses

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is the receipts (decision X11). Three things about them are worth
// saying once, because every handler below follows from them:
//
//   - The bytes live in the object store, the metadata in expenses.attachments,
//     and the two cannot be written in one transaction. So the object is
//     written FIRST and the row second, inside the transaction that holds the
//     expense's row lock; if that transaction refuses or fails, the object is
//     removed again. A delete goes the other way: the row first, the object
//     once the delete has committed. Either way a failure leaves an object
//     nothing points at, never a row pointing at nothing.
//   - The object store is platform infrastructure rather than another module,
//     so the locked-transaction rule (server.go) does not formally cover it —
//     but a slow store under a row lock starves the pool exactly as a slow
//     neighbour would, so no store call is ever made inside withLockedTx.
//   - A receipt has no visibility of its own: it is read by whoever may see
//     its expense, and changed by whoever may change it. There is no third
//     rule here, only authorize.go's.

// The bounds of design §3.2. maxReceiptRequestBytes is the router's cap for
// the upload operation (module.RouterOptions.BodyLimits): the file itself plus
// room for the multipart framing around it, the same margin identity's avatar
// upload leaves. The router puts its http.MaxBytesReader in place before the
// generated strict server's multipart decoder ever reads the body, so this is
// the one cap in front of an upload, and a read past it fails the part the
// same way an oversized part does.
const (
	maxReceiptBytes        = 10 * 1024 * 1024
	maxReceiptRequestBytes = maxReceiptBytes + 64*1024
	maxReceiptsPerEntry    = 10
	receiptNameMaxLength   = 255
)

// receiptCleanupTimeout bounds a compensating or post-commit object delete,
// which runs on a context detached from the request's own (removeReceiptObject).
// Detached is not unbounded: a store that has stopped answering must not hold
// a connection open indefinitely.
const receiptCleanupTimeout = 30 * time.Second

// receiptContentSecurityPolicy is the policy a served receipt carries itself.
const receiptContentSecurityPolicy = "default-src 'none'; sandbox"

// receiptFormField is the multipart field the contract documents. Unlike
// .NET's convention of taking whatever single file a form carries, the part
// has to be this one: a client that sends another name has misread the
// contract, and guessing for it would make the contract a suggestion.
const receiptFormField = "file"

// receiptFallbackName is what a receipt is called when the client sent no
// usable name at all — the column is NOT NULL and a nameless download would
// have nothing to offer the browser.
const receiptFallbackName = "receipt"

// receiptBodyLimits raises the router's request-body cap for the upload to
// maxReceiptRequestBytes. Every other operation of this module keeps the
// platform default (module.DefaultMaxBodyBytes, 1 MiB), which is far more than
// any JSON body here.
var receiptBodyLimits = map[string]int64{
	"postExpensesEntriesByIdAttachments": maxReceiptRequestBytes,
}

// invalidReceiptFileMessage answers every way the uploaded file itself is
// refused: no "file" part, an empty one, one past the size bound, a type that
// is none of the four, and a file whose bytes disagree with what it was called
// — a PNG named .pdf is refused rather than stored under a type it is not. One
// message for all of them, so the refusal never tells a caller which probe
// worked.
const invalidReceiptFileMessage = "A receipt must be a JPEG, PNG, HEIC or PDF file of at most 10 MB, and its contents must match the type and file name it is sent under"

// receiptType is one of design §3.2's four formats: what this module stores
// and serves it as, the content types a client may declare it with, the file
// extensions that go with it, and whether a browser can be asked to show it in
// place.
type receiptType struct {
	ContentType string
	Declared    []string
	Extensions  []string
	// Inline is whether the download is offered in place rather than as a file
	// to save. HEIC is not: only Safari renders one, so everywhere else an
	// inline HEIC is a blank frame where a receipt should be, while a download
	// opens in the picture viewer the operating system already has.
	Inline bool
}

var receiptTypes = []receiptType{
	{
		ContentType: "image/jpeg",
		Declared:    []string{"image/jpeg", "image/jpg", "image/pjpeg"},
		Extensions:  []string{".jpg", ".jpeg", ".jpe"},
		Inline:      true,
	},
	{
		ContentType: "image/png",
		Declared:    []string{"image/png"},
		Extensions:  []string{".png"},
		Inline:      true,
	},
	{
		ContentType: "image/heic",
		Declared:    []string{"image/heic", "image/heif", "image/heic-sequence", "image/heif-sequence"},
		Extensions:  []string{".heic", ".heif"},
		Inline:      false,
	},
	{
		ContentType: "application/pdf",
		Declared:    []string{"application/pdf", "application/x-pdf"},
		Extensions:  []string{".pdf"},
		Inline:      true,
	},
}

// heicBrands are the ISO base media major brands that mean "this is a HEIF
// still image" — the format an iPhone photographs a receipt in.
// net/http's sniffer knows mp4 and nothing else in this family, so a HEIC
// reaches DetectContentType as application/octet-stream; this module reads the
// brand itself instead. Only the MAJOR brand decides: an ordinary video also
// lists mif1 among its compatible brands, and scanning those would make every
// mp4 a receipt.
var heicBrands = []string{"heic", "heix", "heim", "heis", "hevc", "hevx", "hevm", "hevs", "mif1", "msf1"}

// isHEIC reports whether data is an ISO base media file whose major brand is
// one of the still-image ones.
func isHEIC(data []byte) bool {
	return len(data) >= 12 && string(data[4:8]) == "ftyp" && slices.Contains(heicBrands, string(data[8:12]))
}

// sniffReceiptType is the type data really is, from its own leading bytes and
// nothing the client said.
func sniffReceiptType(data []byte) (receiptType, bool) {
	sniffed := "image/heic"
	if !isHEIC(data) {
		sniffed, _, _ = strings.Cut(http.DetectContentType(data), ";")
		sniffed = strings.TrimSpace(sniffed)
	}
	for _, t := range receiptTypes {
		if strings.EqualFold(t.ContentType, sniffed) {
			return t, true
		}
	}
	return receiptType{}, false
}

// familyOfDeclared and familyOfExtension are the allowed type a client's
// declared content type, or a file name's extension, belongs to — if either
// belongs to one of the four at all.
func familyOfDeclared(mediaType string) (receiptType, bool) {
	for _, t := range receiptTypes {
		if slices.ContainsFunc(t.Declared, func(d string) bool { return strings.EqualFold(d, mediaType) }) {
			return t, true
		}
	}
	return receiptType{}, false
}

func familyOfExtension(ext string) (receiptType, bool) {
	for _, t := range receiptTypes {
		if slices.Contains(t.Extensions, ext) {
			return t, true
		}
	}
	return receiptType{}, false
}

// receiptTypeOf is the type a receipt is stored under: the type sniffed from
// its own bytes, which must be one of the four and must not be contradicted by
// what the client called it. The bytes decide; the declared type and the file
// extension can only ever *disagree*, and they disagree when they name one of
// the other three formats — a PNG called .pdf is refused, and so is a PDF sent
// as image/png.
//
// Anything the four formats do not claim is simply not evidence. A phone that
// sends application/octet-stream for a HEIC, a browser that sends no type at
// all, and an invoice a person saved as "Faktura nr. 12345" (whose last dot
// begins no extension) are all ordinary, and refusing them taught the caller
// nothing: the message names the type and the name together, so a refusal
// there would send them hunting for the wrong problem.
func receiptTypeOf(declared, fileName string, data []byte) (receiptType, bool) {
	t, ok := sniffReceiptType(data)
	if !ok {
		return receiptType{}, false
	}
	if mediaType, _, err := mime.ParseMediaType(declared); err == nil && mediaType != "" {
		if other, known := familyOfDeclared(mediaType); known && other.ContentType != t.ContentType {
			return receiptType{}, false
		}
	}
	if other, known := familyOfExtension(strings.ToLower(path.Ext(fileName))); known && other.ContentType != t.ContentType {
		return receiptType{}, false
	}
	return t, true
}

// receiptFileName is the name a receipt keeps: the client's own, stripped of
// any path it arrived with — both separators, because the name is untrusted
// text from whatever system produced it — and of control characters, capped at
// the column's 255 characters, and never, ever part of the object key.
func receiptFileName(raw string) string {
	name := strings.TrimSpace(raw)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return receiptFallbackName
	}
	if runes := []rune(name); len(runes) > receiptNameMaxLength {
		name = string(runes[:receiptNameMaxLength])
	}
	return name
}

// receiptObjectKey is where a receipt's bytes go: the expense it belongs to
// and a fresh random name. It is never derived from the file's name, so no
// caller can choose a key, collide with another's, or read anything out of one.
// The key is relative to the module's own storage scope, which the production
// store prefixes (storage.NewScope) and refuses to be handed twice.
func receiptObjectKey(entryID int64) string {
	return fmt.Sprintf("receipts/%d/%s", entryID, uuid.New().String())
}

// receiptPart reads the multipart part named "file". data is capped at
// maxReceiptBytes+1 bytes, which is enough to tell "too large" from "just
// within" without ever buffering an unbounded body; a read failure — a
// malformed multipart body, or the router's own request cap firing while a
// preceding part is drained — reports ok=false, the same refusal an empty or
// oversized part gets.
func receiptPart(mr *multipart.Reader) (data []byte, fileName, declared string, ok bool) {
	if mr == nil {
		return nil, "", "", false
	}
	found := false
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", "", false
		}
		if part.FormName() != receiptFormField {
			_ = part.Close()
			continue
		}
		if found {
			// A second file in one request: refused rather than dropped. The
			// contract documents one, and quietly keeping whichever came first
			// would lose a person's receipt without telling anybody.
			_ = part.Close()
			return nil, "", "", false
		}
		found = true
		fileName, declared = part.FileName(), part.Header.Get("Content-Type")
		data, err = io.ReadAll(io.LimitReader(part, maxReceiptBytes+1))
		_ = part.Close()
		if err != nil {
			return nil, "", "", false
		}
	}
	return data, fileName, declared, found && len(data) > 0 && len(data) <= maxReceiptBytes
}

// entryTakesReceipts is why an expense's receipts cannot be changed right now,
// "" when they can. A mileage line never carries one; beyond that the reasons
// are the expense's own state, which entryStateRefusal owns for the whole
// module (a closed period, or an expense that has moved past being editable).
//
// Whether the caller may change the expense at all is a separate question,
// answered before this one with a 403 — the module's one rule for the two
// codes: who is asking is a 403 (or the bare 404 of something they cannot
// see), what the expense is right now is a 400 naming the reason. Both the
// upload and the delete report it on entryId, because the expense is the thing
// that refuses and the thing the caller can do something about.
func entryTakesReceipts(c *caller, entry store.ExpensesEntry, unit entryUnit) string {
	if entry.Kind != kindOutlay {
		return "Only an outlay carries a receipt; a mileage line has none"
	}
	_, msg := entryStateRefusal(c, unit)
	return msg
}

// receiptsStranded is the message a save carries when it would change the kind
// of an expense that still holds receipts. It is the other side of
// entryTakesReceipts: a receipt belongs to an outlay and to nothing else, so a
// line that stopped being one would leave its receipts where no door of this
// module reaches them — the upload and the delete both refuse a mileage line,
// and the owner's only way out would be deleting the whole expense. Removing
// them here instead would put an object-store call in the update path, which
// this module deliberately keeps out of it (see the file header), so the save
// is what gives way.
const receiptsStranded = "Remove this expense's receipts before making it a mileage line"

// changeStrandsReceipts reports whether replacing current with a body of this
// kind would leave receipts on a line that cannot carry them.
//
// It is asked twice, on the two queries a save already makes: in prepare, so
// the caller hears about it before anything is locked, and again under the
// expense's own row lock, where an upload that committed in between is counted
// too — the upload takes that same lock, so the two cannot interleave.
func changeStrandsReceipts(ctx context.Context, q *store.Queries, current store.ExpensesEntry, kind string) (bool, error) {
	if current.Kind != kindOutlay || kind == kindOutlay {
		return false, nil
	}
	count, err := q.CountAttachmentsForEntry(ctx, current.ID)
	if err != nil {
		return false, fmt.Errorf("expenses: count an expense's receipts: %w", err)
	}
	return count > 0, nil
}

// attachmentResponse renders one receipt row. The object key is not on it, and
// there is no field on the generated type to put it in, so exposing it would
// take a deliberate change to the contract rather than an accidental one.
func attachmentResponse(row store.ExpensesAttachment) gen.ExpensesAttachmentResponse {
	return gen.ExpensesAttachmentResponse{
		Id:          row.ID,
		FileName:    row.FileName,
		ContentType: row.ContentType,
		SizeBytes:   row.SizeBytes,
	}
}

// visibleAttachment loads one receipt, the expense it is on and the caller's
// access to that expense, answering found=false for an unknown receipt id and
// for one on an expense the caller may not see alike — the two are the same
// bare 404, and so is an unknown expense id.
func (s *server) visibleAttachment(ctx context.Context, q *store.Queries, c *caller, id int64) (
	store.ExpensesAttachment, store.ExpensesEntry, entryUnit, entryAccess, bool, error,
) {
	attachment, err := q.GetAttachment(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ExpensesAttachment{}, store.ExpensesEntry{}, entryUnit{}, entryAccess{}, false, nil
	}
	if err != nil {
		return store.ExpensesAttachment{}, store.ExpensesEntry{}, entryUnit{}, entryAccess{}, false,
			fmt.Errorf("expenses: get a receipt: %w", err)
	}
	entry, unit, a, found, err := s.visibleEntry(ctx, q, c, attachment.EntryID)
	if err != nil || !found {
		return store.ExpensesAttachment{}, store.ExpensesEntry{}, entryUnit{}, entryAccess{}, false, err
	}
	return attachment, entry, unit, a, true, nil
}

// removeReceiptObject removes an object the database no longer points at (or
// never came to). It is best effort by design: the row is what makes a receipt,
// so an object that outlives it is a stray to be swept, not a reason to fail a
// request that otherwise succeeded. The key is deliberately not in the log
// line — it would reach server logs for no one's benefit — so the expense is
// what a reader correlates on.
func (s *server) removeReceiptObject(ctx context.Context, entryID int64, key string) {
	// Detached from the request's own cancellation, and bounded by a deadline
	// of its own. Every call of this is a compensation for a decision already
	// made — the row is gone, or was never written — and internal/storage's fs
	// driver checks ctx.Err() before it touches anything, so on the request's
	// context a client that closed the tab would leave the bytes behind for
	// good: nothing in this installation ever sweeps them up (docs, M3).
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), receiptCleanupTimeout)
	defer cancel()
	if err := s.objects.Delete(cleanup, key); err != nil {
		s.deps.Logger.ErrorContext(cleanup, "expenses: a receipt object could not be removed from the store",
			"entry_id", entryID, "error", err.Error())
	}
}

// PostExpensesEntriesByIdAttachments Attach a receipt to an expense
// (POST /api/v1/expenses/entries/{id}/attachments)
//
// The order of refusals is the order that tells a caller least: an expense they
// may not see is the unknown id's bare 404, one they may see but not change is
// the access layer's 403, and only then is the expense's own state judged (400
// on entryId) and the file read (400 on file). The object is written before the
// transaction, and removed again if the transaction refuses — the count bound
// is decided under the expense's row lock, so two uploads racing for the tenth
// slot cannot both take it.
func (s *server) PostExpensesEntriesByIdAttachments(ctx context.Context, req gen.PostExpensesEntriesByIdAttachmentsRequestObject) (gen.PostExpensesEntriesByIdAttachmentsResponseObject, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	entry, unit, a, found, err := s.visibleEntry(ctx, q, c, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.PostExpensesEntriesByIdAttachments404Response{}, nil
	}
	if !a.IsWriter {
		return gen.PostExpensesEntriesByIdAttachments403JSONResponse(forbidden()), nil
	}
	if msg := entryTakesReceipts(c, entry, unit); msg != "" {
		return gen.PostExpensesEntriesByIdAttachments400ApplicationProblemPlusJSONResponse(
			invalidReceipt(fieldError("entryId", msg))), nil
	}

	data, rawName, declared, ok := receiptPart(req.Body)
	if !ok {
		return gen.PostExpensesEntriesByIdAttachments400ApplicationProblemPlusJSONResponse(
			invalidReceipt(fieldError("file", invalidReceiptFileMessage))), nil
	}
	receipt, ok := receiptTypeOf(declared, rawName, data)
	if !ok {
		return gen.PostExpensesEntriesByIdAttachments400ApplicationProblemPlusJSONResponse(
			invalidReceipt(fieldError("file", invalidReceiptFileMessage))), nil
	}

	key := receiptObjectKey(req.Id)
	if err := s.objects.Put(ctx, key, bytes.NewReader(data), receipt.ContentType); err != nil {
		s.deps.Logger.ErrorContext(ctx, "expenses: a receipt could not be written to the store",
			"entry_id", req.Id, "error", err.Error())
		return gen.PostExpensesEntriesByIdAttachments503ApplicationProblemPlusJSONResponse(receiptStoreUnavailable()), nil
	}

	var (
		created store.ExpensesAttachment
		refusal string
		gone    bool
	)
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		locked, lockedUnit, found, err := lockEntryUnit(ctx, txq, req.Id, entry.ClaimID)
		if err != nil {
			return err
		}
		if !found {
			gone = true
			return nil
		}
		// Judged again on the row as it is under the lock: a submit that
		// committed since must win over the read this request began with.
		if refusal = entryTakesReceipts(c, locked, lockedUnit); refusal != "" {
			return nil
		}
		count, err := txq.CountAttachmentsForEntry(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("expenses: count an expense's receipts: %w", err)
		}
		if count >= maxReceiptsPerEntry {
			refusal = fmt.Sprintf("An expense carries at most %d receipts", maxReceiptsPerEntry)
			return nil
		}
		created, err = txq.InsertAttachment(ctx, store.InsertAttachmentParams{
			EntryID:          req.Id,
			ObjectKey:        key,
			FileName:         receiptFileName(rawName),
			ContentType:      receipt.ContentType,
			SizeBytes:        int64(len(data)),
			UploadedByUserID: c.UserID,
			Now:              s.deps.Clock(),
		})
		return err
	})
	switch {
	case err != nil:
		s.removeReceiptObject(ctx, req.Id, key)
		return nil, fmt.Errorf("expenses: attach a receipt: %w", err)
	case gone:
		s.removeReceiptObject(ctx, req.Id, key)
		return gen.PostExpensesEntriesByIdAttachments404Response{}, nil
	case refusal != "":
		s.removeReceiptObject(ctx, req.Id, key)
		return gen.PostExpensesEntriesByIdAttachments400ApplicationProblemPlusJSONResponse(
			invalidReceipt(fieldError("entryId", refusal))), nil
	}
	return gen.PostExpensesEntriesByIdAttachments201JSONResponse(attachmentResponse(created)), nil
}

// receiptDownload sets the headers every receipt read answers with, before the
// generated response's own Visit writes the content type and the status: never
// cached, never sniffed, and named as it was uploaded. A read of somebody's
// receipt is as private as the expense it belongs to, so the headers are set on
// the 404 and the 503 as well as on the bytes.
type receiptDownload struct {
	body gen.GetExpensesAttachmentsByIdResponseObject
	// disposition is the whole Content-Disposition header, "" when there is
	// nothing to name.
	disposition string
}

func (r receiptDownload) VisitGetExpensesAttachmentsByIdResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Defence in depth over the platform's own policy (internal/security): a
	// receipt is a document nobody should be able to make fetch anything, and
	// sandbox puts the one served here in an opaque origin, which is the last
	// bridge between a PDF's own scripting engine and this app's origin. It
	// does not affect using the same URL as an <img> source: the policy governs
	// the document, and an image subresource is not one.
	w.Header().Set("Content-Security-Policy", receiptContentSecurityPolicy)
	if r.disposition != "" {
		w.Header().Set("Content-Disposition", r.disposition)
	}
	return r.body.VisitGetExpensesAttachmentsByIdResponse(w)
}

// contentDisposition names a receipt for the browser: shown in place for the
// types every browser renders, handed over as a file for the one that does not
// (HEIC). The name is RFC 5987 encoded, so a Norwegian file name arrives
// intact rather than mangled or, worse, breaking the header with a quote of its
// own.
func contentDisposition(receipt receiptType, fileName string) string {
	kind := "attachment"
	if receipt.Inline {
		kind = "inline"
	}
	return fmt.Sprintf("%s; filename*=UTF-8''%s", kind, rfc5987Encode(fileName))
}

// rfc5987Encode percent-encodes every byte of value that is not an RFC 5987
// attr-char, which is what a filename* parameter may carry unescaped.
func rfc5987Encode(value string) string {
	const attrChars = "!#$&+-.^_`|~"
	var b strings.Builder
	for _, c := range []byte(value) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			strings.IndexByte(attrChars, c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// GetExpensesAttachmentsById Download a receipt
// (GET /api/v1/expenses/attachments/{id})
//
// Streamed with the type it was stored under — which is the type its own bytes
// were sniffed as on upload, so serving it in place is safe — and with the
// expense's own visibility: anyone who may not see the expense gets the same
// bare 404 an unknown receipt id gets. A store that cannot be read answers 503
// rather than 404: a row with no object behind it is a fault to be seen, not a
// receipt to be denied.
func (s *server) GetExpensesAttachmentsById(ctx context.Context, req gen.GetExpensesAttachmentsByIdRequestObject) (gen.GetExpensesAttachmentsByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	attachment, _, _, _, found, err := s.visibleAttachment(ctx, q, c, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return receiptDownload{body: gen.GetExpensesAttachmentsById404Response{}}, nil
	}

	body, err := s.objects.Get(ctx, attachment.ObjectKey)
	if err != nil {
		s.deps.Logger.ErrorContext(ctx, "expenses: a receipt could not be read from the store",
			"attachment_id", attachment.ID, "error", err.Error())
		return receiptDownload{body: gen.GetExpensesAttachmentsById503ApplicationProblemPlusJSONResponse(
			receiptStoreUnavailable())}, nil
	}

	// The stored type is one of the four; a row that somehow carries another
	// is served as a file rather than shown, which is the safe way round.
	var receipt receiptType
	for _, t := range receiptTypes {
		if t.ContentType == attachment.ContentType {
			receipt = t
		}
	}
	return receiptDownload{
		body: gen.GetExpensesAttachmentsById200AsteriskResponse{
			Body:          body,
			ContentType:   attachment.ContentType,
			ContentLength: attachment.SizeBytes,
		},
		disposition: contentDisposition(receipt, attachment.FileName),
	}, nil
}

// DeleteExpensesAttachmentsById Delete a receipt
// (DELETE /api/v1/expenses/attachments/{id})
//
// Removing a receipt is changing the expense, so it is exactly
// capabilities.canEdit: its owner or expenses:manage, while the expense is a
// draft or rejected, and not before the period lock. The two halves answer
// differently, as they do on the expense's own PUT and DELETE: a caller the
// expense does not belong to gets the access layer's 403 (and one who may not
// see it at all, the unknown id's 404), while an expense that has been
// submitted or is dated inside a closed period is a 400 on entryId naming the
// reason. The row goes in the transaction; the object goes after it has
// committed.
func (s *server) DeleteExpensesAttachmentsById(ctx context.Context, req gen.DeleteExpensesAttachmentsByIdRequestObject) (gen.DeleteExpensesAttachmentsByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	attachment, entry, unit, a, found, err := s.visibleAttachment(ctx, q, c, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.DeleteExpensesAttachmentsById404Response{}, nil
	}
	if !a.IsWriter {
		return gen.DeleteExpensesAttachmentsById403JSONResponse(forbidden()), nil
	}
	if msg := entryTakesReceipts(c, entry, unit); msg != "" {
		return gen.DeleteExpensesAttachmentsById400ApplicationProblemPlusJSONResponse(
			invalidReceipt(fieldError("entryId", msg))), nil
	}

	var refusal string
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		locked, lockedUnit, found, err := lockEntryUnit(ctx, txq, entry.ID, entry.ClaimID)
		if err != nil {
			return err
		}
		if !found {
			// The expense itself went in the meantime, and the row with it
			// (the foreign key cascades). The receipt is gone, which is what
			// was asked; its object is removed below all the same.
			return nil
		}
		// Judged again on the row as it is under the lock, by the very rule
		// that judged it before the transaction rather than by a copy of half
		// of it: a submit that committed since is the refusal above, arrived a
		// moment later.
		if refusal = entryTakesReceipts(c, locked, lockedUnit); refusal != "" {
			return nil
		}
		if _, err := txq.DeleteAttachment(ctx, req.Id); err != nil {
			return fmt.Errorf("expenses: delete a receipt: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if refusal != "" {
		return gen.DeleteExpensesAttachmentsById400ApplicationProblemPlusJSONResponse(
			invalidReceipt(fieldError("entryId", refusal))), nil
	}
	s.removeReceiptObject(ctx, entry.ID, attachment.ObjectKey)
	return gen.DeleteExpensesAttachmentsById204Response{}, nil
}
