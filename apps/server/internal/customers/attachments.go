package customers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is GET/POST /customers/{id}/attachments and
// GET/DELETE /customers/{id}/attachments/{attachmentId} (customer attachments
// design D2-D4): a customer's documents — a contract, an NDA, the
// correspondence behind a manual timeline entry — read with
// customers:timeline-view and written with customers:timeline-manage, the
// timeline's own pair, because an attachment is a record of the
// relationship's history and every one added or removed is a timeline event.
// The bytes are in the object store, reached only through objects.go's
// accessors and never inside a transaction: the upload writes the object
// first and removes it again on a refusal, the download reads the row on the
// pool and then the object, and the delete removes the row and queues the
// object in one transaction, then removes it (object_deletions.go). Every
// write takes the customer's lock (lockWritableCustomer), so a merged-away or
// anonymised customer refuses it as it refuses every write.

// invalidAttachmentTitle is the 400's title, every field error under it.
const invalidAttachmentTitle = "Invalid attachment"

// The file's refusals. The type's message is one for every way the bytes
// and what they were called disagree, so the refusal never tells a caller
// which probe worked.
const (
	attachmentTypeMessage = "An attachment must be a PDF, JPEG, PNG or HEIC file, and its contents must match the type and file name it is sent under"
	attachmentSizeMessage = "An attachment is at most 10 MiB"
)

// attachmentEntryMessage is the one message for every way timelineEntryId
// names no entry to attach to: unknown, another customer's, generated or
// deleted (design D2).
const attachmentEntryMessage = "timelineEntryId must name a manual, active timeline entry of this customer"

// maxAttachmentFieldBytes bounds the one text field of the upload so it is
// never buffered past what an id can hold.
const maxAttachmentFieldBytes = 64

// errAttachmentCountExceeded aborts the upload's transaction on the cap; the
// 409 is built by the handler.
var errAttachmentCountExceeded = errors.New("customers: attachment count exceeded")

// errAttachmentEntryInvalid aborts the upload's transaction when the entry
// it names is not one to attach to.
var errAttachmentEntryInvalid = errors.New("customers: attachment entry invalid")

// attachmentCountExceeded is the 409 the 201st attachment answers, on
// CustomerConflictProblem with its code, as the merge's and the GDPR
// delivery's refusals are.
func attachmentCountExceeded() gen.CustomerConflictProblem {
	return *mergeConflict("Too many attachments", "attachment_count_exceeded", fmt.Sprintf(
		"A customer carries at most %d attachments, on the customer and on its timeline entries together. Remove one first.",
		maxAttachmentsPerCustomer))
}

// attachmentStoreUnavailable is the 503 an upload or a download answers when
// the object store fails: the request was right and the installation could
// not serve it — worth retrying and worth an operator seeing, rather than a
// 404 that would hide a fault as a missing file.
func attachmentStoreUnavailable() apicommon.ProblemDetails {
	return apicommon.ProblemStatus("Attachment storage is unavailable",
		"The attachment store could not be reached. Nothing was changed; try again.",
		http.StatusServiceUnavailable)
}

// attachmentForm is one upload's body as read: the file's bytes, the name
// and type it was sent under, and the entry field. tooLarge and unreadable
// say why the file could not be had.
type attachmentForm struct {
	data               []byte
	fileName, declared string
	files              int
	tooLarge           bool
	unreadable         bool
	entry              *string
}

// readAttachmentForm reads every part of mr. The file is read up to
// maxAttachmentBytes+1 bytes — enough to tell "too large" from "just within"
// without buffering an unbounded body; a read the router's own request cap
// stops (http.MaxBytesError) is too large as well, and any other failure an
// unreadable body.
func readAttachmentForm(mr *multipart.Reader) attachmentForm {
	var f attachmentForm
	if mr == nil {
		f.unreadable = true
		return f
	}
	fail := func(err error) attachmentForm {
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
		switch part.FormName() {
		case attachmentFormField:
			f.files++
			f.fileName, f.declared = part.FileName(), part.Header.Get("Content-Type")
			data, err := io.ReadAll(io.LimitReader(part, maxAttachmentBytes+1))
			_ = part.Close()
			if err != nil {
				return fail(err)
			}
			if len(data) > maxAttachmentBytes {
				f.tooLarge = true
				return f
			}
			f.data = data
		case attachmentEntryField:
			value, err := io.ReadAll(io.LimitReader(part, maxAttachmentFieldBytes+1))
			_ = part.Close()
			if err != nil {
				return fail(err)
			}
			s := string(value)
			f.entry = &s
		default:
			_ = part.Close()
		}
	}
}

// parsedAttachment is one upload judged: the type its bytes are and the
// entry it names.
type parsedAttachment struct {
	kind    attachmentType
	entryID *int32
}

// parseAttachmentForm judges f: the file is there once, within the bound,
// one of the four types and not contradicted by its name or declared type;
// the entry, when sent, is an id. Every failure is collected — but a file too
// large is answered alone, since the field after it was never read.
func parseAttachmentForm(f attachmentForm) (parsedAttachment, map[string][]string) {
	var out parsedAttachment
	errs := map[string][]string{}
	if f.tooLarge {
		errs[attachmentFormField] = []string{attachmentSizeMessage}
		return out, errs
	}
	switch {
	case f.unreadable:
		errs[attachmentFormField] = []string{"The request's body could not be read as a multipart form"}
	case f.files > 1:
		errs[attachmentFormField] = []string{"A request attaches one file; this one carried more than one"}
	case f.files == 0 || len(f.data) == 0:
		errs[attachmentFormField] = []string{"An attachment file is required"}
	default:
		kind, ok := attachmentTypeOf(f.declared, f.fileName, f.data)
		if !ok {
			errs[attachmentFormField] = []string{attachmentTypeMessage}
		}
		out.kind = kind
	}
	if f.entry != nil && strings.TrimSpace(*f.entry) != "" {
		id, err := strconv.ParseInt(strings.TrimSpace(*f.entry), 10, 32)
		if err != nil || id < 1 {
			errs[attachmentEntryField] = []string{fmt.Sprintf("timelineEntryId is a timeline entry's id, but was '%s'", strings.TrimSpace(*f.entry))}
		} else {
			entryID := int32(id)
			out.entryID = &entryID
		}
	}
	if len(errs) == 0 {
		return out, nil
	}
	return out, errs
}

// attachmentEntryRef is the entry an attachment belongs to as the response
// names it, from the list's or the get's left join; nil when it belongs to
// none.
func attachmentEntryRef(id *int32, eventType *string, occurredOn pgtype.Date, summary, state *string) *gen.CustomerAttachmentTimelineEntry {
	if id == nil || eventType == nil {
		return nil
	}
	return &gen.CustomerAttachmentTimelineEntry{
		Id: *id, EventType: *eventType, OccurredOn: openapi_types.Date{Time: occurredOn.Time},
		Summary: deref(summary), State: deref(state),
	}
}

// attachmentResponse is one attachment with its entry. The object key is not
// on it, and there is no field on the generated type to put it in.
func attachmentResponse(a store.CustomersCustomerAttachment, entry *gen.CustomerAttachmentTimelineEntry) gen.CustomerAttachment {
	return gen.CustomerAttachment{
		Id: a.ID, CustomerId: a.CustomerID, FileName: a.FileName, ContentType: a.ContentType,
		SizeBytes: a.SizeBytes, UploadedAt: a.CreatedAt, TimelineEntry: entry,
	}
}

// listRowAttachment and getRowAttachment are the joined rows' attachment
// halves, so one response builder serves both.
func listRowAttachment(r store.ListCustomerAttachmentsRow) (store.CustomersCustomerAttachment, *gen.CustomerAttachmentTimelineEntry) {
	return store.CustomersCustomerAttachment{
			ID: r.ID, CustomerID: r.CustomerID, TimelineEntryID: r.TimelineEntryID, ObjectKey: r.ObjectKey, FileName: r.FileName,
			ContentType: r.ContentType, SizeBytes: r.SizeBytes, UploadedByUserID: r.UploadedByUserID, CreatedAt: r.CreatedAt,
		},
		attachmentEntryRef(r.TimelineEntryID, r.EntryEventType, r.EntryOccurredOn, r.EntrySummary, r.EntryState)
}

func getRowAttachment(r store.GetCustomerAttachmentRow) (store.CustomersCustomerAttachment, *gen.CustomerAttachmentTimelineEntry) {
	return listRowAttachment(store.ListCustomerAttachmentsRow(r))
}

// attachmentEventPayload is what the two events carry (design D4): the file
// as a person would describe it, and the entry it went on. fileName is
// among personalPayloadKeys, so an anonymisation takes it out.
func attachmentEventPayload(a store.CustomersCustomerAttachment) map[string]any {
	payload := map[string]any{
		"customerId": a.CustomerID, "attachmentId": a.ID, "fileName": a.FileName,
		"contentType": a.ContentType, "sizeBytes": a.SizeBytes,
	}
	if a.TimelineEntryID != nil {
		payload["timelineEntryId"] = *a.TimelineEntryID
	}
	return payload
}

// recordAttachmentAdded and recordAttachmentRemoved are the upload's and the
// delete's generated events (design D4), shaped like the address events: a
// fixed verb and the file name, truncated to the summary column.
func recordAttachmentAdded(ctx context.Context, q *store.Queries, now time.Time, a store.CustomersCustomerAttachment, act actor) error {
	return recordGeneratedEvent(ctx, q, a.CustomerID, now, "customer.attachment_added",
		truncateUTF16("Attachment added: "+a.FileName, 500), attachmentEventPayload(a), 1, act.Kind, act.Display, act.UserID)
}

func recordAttachmentRemoved(ctx context.Context, q *store.Queries, now time.Time, a store.CustomersCustomerAttachment, act actor) error {
	return recordGeneratedEvent(ctx, q, a.CustomerID, now, "customer.attachment_removed",
		truncateUTF16("Attachment removed: "+a.FileName, 500), attachmentEventPayload(a), 1, act.Kind, act.Display, act.UserID)
}

// GetCustomersByIdAttachments List a customer's attachments
// (GET /api/v1/customers/{id}/attachments)
//
// Newest first, each with its entry. A merged-away or anonymised customer's
// are listed too: they are its history.
func (s *server) GetCustomersByIdAttachments(ctx context.Context, req gen.GetCustomersByIdAttachmentsRequestObject) (gen.GetCustomersByIdAttachmentsResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetCustomer(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersByIdAttachments404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}
	rows, err := q.ListCustomerAttachments(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: list customer %d's attachments: %w", req.Id, err)
	}
	data := make([]gen.CustomerAttachment, 0, len(rows))
	for _, r := range rows {
		data = append(data, attachmentResponse(listRowAttachment(r)))
	}
	return gen.GetCustomersByIdAttachments200JSONResponse{Data: data}, nil
}

// PostCustomersByIdAttachments Attach a file to a customer
// (POST /api/v1/customers/{id}/attachments)
//
// The refusals come in the order that tells a caller least and costs the
// server least: what was sent (400, before any database access — a 10 MiB
// body is not read twice), whether the customer exists (404), the read-only
// refusal read on the pool (409 customer_merged or customer_anonymised —
// before a 10 MiB object is written for a customer that takes no writes,
// refuseReadOnlyCustomer's purpose), then under the customer's lock the same
// refusal again as the race decider, the entry (400 on timelineEntryId) and
// the count (409 attachment_count_exceeded). The object is written before the
// transaction — a store that cannot write is 503 with nothing recorded — and
// whatever refuses or fails after the write removes it. The actor is
// resolved before the transaction opens (actor.go's rule).
func (s *server) PostCustomersByIdAttachments(ctx context.Context, req gen.PostCustomersByIdAttachmentsRequestObject) (gen.PostCustomersByIdAttachmentsResponseObject, error) {
	form := readAttachmentForm(req.Body)
	parsed, errs := parseAttachmentForm(form)
	if errs != nil {
		return gen.PostCustomersByIdAttachments400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(invalidAttachmentTitle, errs)), nil
	}

	q := store.New(s.deps.Pool)
	if _, err := q.GetCustomer(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCustomersByIdAttachments404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}
	if merr := refuseReadOnlyCustomer(ctx, q, req.Id); isReadOnlyCustomer(merr) {
		return gen.PostCustomersByIdAttachments409ApplicationProblemPlusJSONResponse(readOnlyProblem(merr)), nil
	} else if merr != nil {
		return nil, merr
	}
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	key := attachmentObjectKey()
	if err := s.objectPut(ctx, key, bytes.NewReader(form.data), parsed.kind.ContentType); err != nil {
		s.logger().ErrorContext(ctx, "customers: an attachment could not be written to the store", "customer_id", req.Id, "error", err.Error())
		return gen.PostCustomersByIdAttachments503ApplicationProblemPlusJSONResponse(attachmentStoreUnavailable()), nil
	}

	now := s.deps.Clock()
	var (
		created store.CustomersCustomerAttachment
		entry   *gen.CustomerAttachmentTimelineEntry
	)
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := lockWritableCustomer(ctx, txq, req.Id); err != nil {
			return err
		}
		if parsed.entryID != nil {
			if _, err := txq.AttachableTimelineEntry(ctx, store.AttachableTimelineEntryParams{ID: *parsed.entryID, CustomerID: req.Id}); errors.Is(err, pgx.ErrNoRows) {
				return errAttachmentEntryInvalid
			} else if err != nil {
				return fmt.Errorf("read timeline entry %d: %w", *parsed.entryID, err)
			}
		}
		count, err := txq.CountCustomerAttachments(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("count the attachments: %w", err)
		}
		if count >= maxAttachmentsPerCustomer {
			return errAttachmentCountExceeded
		}
		var uploadedBy *uuid.UUID
		if act.UserID != nil {
			id := *act.UserID
			uploadedBy = &id
		}
		created, err = txq.InsertCustomerAttachment(ctx, store.InsertCustomerAttachmentParams{
			CustomerID: req.Id, TimelineEntryID: parsed.entryID, ObjectKey: key,
			FileName: attachmentFileName(form.fileName), ContentType: parsed.kind.ContentType, SizeBytes: int64(len(form.data)),
			UploadedByUserID: uploadedBy, Now: now,
		})
		if err != nil {
			return fmt.Errorf("record the attachment: %w", err)
		}
		if created.TimelineEntryID != nil {
			// The entry as the response names it, read here rather than on
			// the pool afterwards: a delete that won the race between the
			// commit and a second read would turn a committed upload into a
			// 500.
			row, err := txq.GetCustomerAttachment(ctx, store.GetCustomerAttachmentParams{ID: created.ID, CustomerID: req.Id})
			if err != nil {
				return fmt.Errorf("read attachment %d's entry: %w", created.ID, err)
			}
			_, entry = getRowAttachment(row)
		}
		return recordAttachmentAdded(ctx, txq, now, created, act)
	})
	if err != nil {
		s.removeObject(ctx, req.Id, key)
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.PostCustomersByIdAttachments404Response{}, nil
	case isReadOnlyCustomer(err):
		return gen.PostCustomersByIdAttachments409ApplicationProblemPlusJSONResponse(readOnlyProblem(err)), nil
	case errors.Is(err, errAttachmentEntryInvalid):
		return gen.PostCustomersByIdAttachments400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(invalidAttachmentTitle,
			map[string][]string{attachmentEntryField: {attachmentEntryMessage}})), nil
	case errors.Is(err, errAttachmentCountExceeded):
		return gen.PostCustomersByIdAttachments409ApplicationProblemPlusJSONResponse(attachmentCountExceeded()), nil
	case err != nil:
		return nil, fmt.Errorf("customers: attach a file to customer %d: %w", req.Id, err)
	}

	return gen.PostCustomersByIdAttachments201JSONResponse(attachmentResponse(created, entry)), nil
}

// attachmentDownload sets the headers every attachment read answers with,
// the 404 and the 503 included: never cached, never sniffed, a policy of its
// own, and the name it was uploaded under.
type attachmentDownload struct {
	body        gen.GetCustomersByIdAttachmentsByAttachmentIdResponseObject
	disposition string
}

func (r attachmentDownload) VisitGetCustomersByIdAttachmentsByAttachmentIdResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", attachmentContentSecurityPolicy)
	if r.disposition != "" {
		w.Header().Set("Content-Disposition", r.disposition)
	}
	return r.body.VisitGetCustomersByIdAttachmentsByAttachmentIdResponse(w)
}

// GetCustomersByIdAttachmentsByAttachmentId Download an attachment
// (GET /api/v1/customers/{id}/attachments/{attachmentId})
//
// The row is read on the pool and the object after it, outside any
// transaction. An id on another customer is the 404 an unknown id is. A row
// whose object cannot be read is 503: a fault to be seen, not a file to be
// denied.
func (s *server) GetCustomersByIdAttachmentsByAttachmentId(ctx context.Context, req gen.GetCustomersByIdAttachmentsByAttachmentIdRequestObject) (gen.GetCustomersByIdAttachmentsByAttachmentIdResponseObject, error) {
	row, err := store.New(s.deps.Pool).GetCustomerAttachment(ctx, store.GetCustomerAttachmentParams{ID: req.AttachmentId, CustomerID: req.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return attachmentDownload{body: gen.GetCustomersByIdAttachmentsByAttachmentId404Response{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: read attachment %d: %w", req.AttachmentId, err)
	}
	a, _ := getRowAttachment(row)
	body, err := s.objectGet(ctx, a.ObjectKey)
	if err != nil {
		s.logger().ErrorContext(ctx, "customers: an attachment could not be read from the store", "attachment_id", a.ID, "error", err.Error())
		return attachmentDownload{body: gen.GetCustomersByIdAttachmentsByAttachmentId503ApplicationProblemPlusJSONResponse(attachmentStoreUnavailable())}, nil
	}
	return attachmentDownload{
		body:        gen.GetCustomersByIdAttachmentsByAttachmentId200AsteriskResponse{Body: body, ContentType: a.ContentType, ContentLength: a.SizeBytes},
		disposition: contentDisposition(storedAttachmentType(a.ContentType), a.FileName),
	}, nil
}

// DeleteCustomersByIdAttachmentsByAttachmentId Remove an attachment from a customer
// (DELETE /api/v1/customers/{id}/attachments/{attachmentId})
//
// The row is read on the pool for its 404, then under the customer's lock —
// the read-only refusal first — read again as it is now, deleted, its object
// queued on the ledger and the event recorded, in one transaction. Once that
// has committed, one attempt removes the object and, when the store confirms,
// clears the ledger row; a store that refuses leaves the row for the
// anonymisation worker's ledger pass. A row that went in the meantime is the
// 404 it would have been a moment earlier.
func (s *server) DeleteCustomersByIdAttachmentsByAttachmentId(ctx context.Context, req gen.DeleteCustomersByIdAttachmentsByAttachmentIdRequestObject) (gen.DeleteCustomersByIdAttachmentsByAttachmentIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetCustomerAttachment(ctx, store.GetCustomerAttachmentParams{ID: req.AttachmentId, CustomerID: req.Id}); errors.Is(err, pgx.ErrNoRows) {
		if merr := refuseReadOnlyCustomer(ctx, q, req.Id); isReadOnlyCustomer(merr) {
			return gen.DeleteCustomersByIdAttachmentsByAttachmentId409ApplicationProblemPlusJSONResponse(readOnlyProblem(merr)), nil
		} else if merr != nil {
			return nil, merr
		}
		return gen.DeleteCustomersByIdAttachmentsByAttachmentId404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: read attachment %d: %w", req.AttachmentId, err)
	}
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	now := s.deps.Clock()
	var removed store.CustomersCustomerAttachment
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := lockWritableCustomer(ctx, txq, req.Id); err != nil {
			return err
		}
		var err error
		removed, err = txq.LockCustomerAttachment(ctx, store.LockCustomerAttachmentParams{ID: req.AttachmentId, CustomerID: req.Id})
		if err != nil {
			return err
		}
		if err := txq.DeleteCustomerAttachment(ctx, removed.ID); err != nil {
			return fmt.Errorf("delete attachment %d: %w", removed.ID, err)
		}
		if err := txq.InsertObjectDeletion(ctx, store.InsertObjectDeletionParams{ObjectKey: removed.ObjectKey, Now: now}); err != nil {
			return fmt.Errorf("queue attachment %d's object: %w", removed.ID, err)
		}
		return recordAttachmentRemoved(ctx, txq, now, removed, act)
	})
	switch {
	case isReadOnlyCustomer(err):
		return gen.DeleteCustomersByIdAttachmentsByAttachmentId409ApplicationProblemPlusJSONResponse(readOnlyProblem(err)), nil
	case errors.Is(err, pgx.ErrNoRows):
		return gen.DeleteCustomersByIdAttachmentsByAttachmentId404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("customers: remove attachment %d: %w", req.AttachmentId, err)
	}
	s.finishObjectDeletion(ctx, removed.ObjectKey, 0, "customer_id", req.Id)
	return gen.DeleteCustomersByIdAttachmentsByAttachmentId204Response{}, nil
}
