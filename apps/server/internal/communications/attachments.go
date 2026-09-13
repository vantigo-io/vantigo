package communications

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is the attachment-staging and download area
// (EP/ConversationEndpoints.cs:103-236, communications inventory §5, §12.3;
// task 6's brief and dispatch — the dispatch corrects and extends the
// brief, and wins where they disagree): postCommunicationsConversationsByIdAttachments,
// getCommunicationsConversationsByConversationIdAttachmentsByAttachmentId
// and getCommunicationsAttachmentsByIdDownload.
//
// D2 (design doc): every upload this port stages is created scan_status =
// "clean" — there is nothing left to scan — so "ready" (a derived duplicate
// of scan_status, never an independent flag) is permanently true for every
// staged upload and the scanning state machine §5.6 catalogues never
// actually turns through any of its other states here.

// maxLiveAttachmentUploads is StageAttachment's per-(conversation, user) cap
// (`:128`, inventory §5.3): "20 attachments per (conversation, user)".
const maxLiveAttachmentUploads = 20

// reservationLifetime is ObjectOwnershipLifecycle.ReservationLifetime
// (`SV/ObjectOwnershipLifecycle.cs:22`, inventory §12.3): a staged
// reservation's bounded expiry, so a process crash between the object-store
// write and the metadata commit cannot strand the object forever — a future
// cleanup sweep (out of this task's scope, design doc D3) reclaims it once
// this window passes.
const reservationLifetime = 10 * time.Minute

// errStorageKeyAlreadyOwned is ReserveAsync's "already owned"/"already
// deleting" throw (inventory §12.3): reserving a storage key that some
// other, still-live reservation already claims. Unreachable through this
// API's own flow — the deterministic (uploaderUserId, idempotencyKey) ->
// storage key mapping means a genuine replay is always caught by
// FindAttachmentUploadByUploaderAndKey before a reservation is ever
// attempted again for the same key — so, like errContactNotFound in
// conversations_create.go, this is left as an unwrapped 500 rather than
// invented a 4xx code no test in the inventory names.
var errStorageKeyAlreadyOwned = errors.New("communications: storage key already reserved by a live, uncompleted upload")

// attachmentUploadRow is the attachment_uploads-table fields every one of
// FindAttachmentUploadByUploaderAndKey, InsertAttachmentUpload and
// GetAttachmentUploadForCaller returns, in whatever sqlc-generated shape
// that particular query happens to produce — one seam so
// attachmentUploadResponseOf only has to know one shape (the same pattern
// channels.go's channelRow follows for the four channel queries).
type attachmentUploadRow struct {
	ID          uuid.UUID
	FileName    string
	ContentType string
	SizeBytes   int64
	ScanStatus  string
	IsInline    bool
	ExpiresAt   time.Time
}

// FindAttachmentUploadByUploaderAndKey, InsertAttachmentUpload and
// GetAttachmentUploadForCaller each select every column of
// attachment_uploads, so sqlc gives all three the same generated shape,
// store.CommunicationsAttachmentUpload, rather than three distinct Row
// types — one converter below covers all three call sites.
func attachmentUploadRowOf(r store.CommunicationsAttachmentUpload) attachmentUploadRow {
	return attachmentUploadRow{
		ID: r.ID, FileName: r.FileName, ContentType: r.ContentType, SizeBytes: r.SizeBytes,
		ScanStatus: r.ScanStatus, IsInline: r.IsInline, ExpiresAt: r.ExpiresAt,
	}
}

// attachmentUploadResponseOf is ToUploadResponse
// (EP/ConversationEndpoints.cs:444, inventory §5.2): Ready is a derived
// duplicate of ScanStatus == "clean", never an independent flag, and
// StorageKey is never exposed — there is no field on this response type to
// put it in (gen.AttachmentUploadResponse has none), so leaking it would
// take a deliberate change to the contract, not an accidental one.
func attachmentUploadResponseOf(row attachmentUploadRow) gen.AttachmentUploadResponse {
	return gen.AttachmentUploadResponse{
		ContentType: row.ContentType, ExpiresAt: row.ExpiresAt, FileName: row.FileName, Id: row.ID,
		IsInline: row.IsInline, Ready: row.ScanStatus == "clean", ScanStatus: row.ScanStatus, SizeBytes: row.SizeBytes,
	}
}

// containsWhitespace reports whether v contains any Unicode whitespace
// character (AttachmentSafety.ContentId's `value.Any(char.IsWhiteSpace)`).
func containsWhitespace(v string) bool {
	for _, r := range v {
		if unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

// lastPathSegment is Path.GetFileName's role in AttachmentSafety.SafeFileName
// (`:9`): the text after the last "/" or "\" — both separators, since the
// input is an untrusted client-supplied file name that .NET's Path.GetFileName
// (platform-aware) treats as a path to strip down to its leaf, unlike Go's
// filepath.Base, which only recognises "/" on a non-Windows GOOS.
func lastPathSegment(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.LastIndexAny(v, `/\`); i >= 0 {
		v = v[i+1:]
	}
	return v
}

// safeAttachmentFileName is AttachmentSafety.SafeFileName (`:7-12`,
// inventory §5.3): the last path segment of value (or "attachment" for a
// blank value), stripped of control characters and any remaining slash,
// trimmed, capped at 200 UTF-16 code units, defaulting to "attachment" when
// nothing survives.
func safeAttachmentFileName(value string) string {
	name := value
	if strings.TrimSpace(name) == "" {
		name = "attachment"
	}
	name = strings.TrimSpace(lastPathSegment(name))

	var b strings.Builder
	for _, r := range name {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			continue
		}
		b.WriteRune(r)
	}
	filtered := strings.TrimSpace(b.String())
	if filtered == "" {
		return "attachment"
	}
	units := []rune(filtered)
	// utf16Length-style truncation, done in code points here rather than
	// via unicode/utf16 round-tripping (preview's approach): SafeFileName's
	// own 200-unit cap is a defensive length bound on an untrusted string,
	// not a value ever compared byte-for-byte against a stored .NET
	// fixture, so a rune-count truncation serves the identical purpose
	// (bound the length) without needing surrogate-pair fidelity.
	if len(units) > 200 {
		filtered = string(units[:200])
	}
	return filtered
}

// sanitizedAttachmentContentType is AttachmentSafety.ContentType (`:14-19`,
// inventory §5.3): a blank, over-length or control-bearing value falls back
// to "application/octet-stream"; otherwise the declared media type
// (parameters such as charset stripped), falling back the same way when it
// does not parse.
func sanitizedAttachmentContentType(value string) string {
	const fallback = "application/octet-stream"
	if strings.TrimSpace(value) == "" || utf16Length(value) > 200 || containsControl(value) {
		return fallback
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil || strings.TrimSpace(mediaType) == "" {
		return fallback
	}
	return mediaType
}

// sanitizedAttachmentContentID is AttachmentSafety.ContentId (`:21-26`,
// inventory §5.3): present is false for an entirely absent form field
// (distinct from an empty one, matching form["contentId"].FirstOrDefault()
// being null vs ""). A blank, over-length or control-bearing value, or one
// that is empty or contains whitespace after trimming and stripping
// surrounding angle brackets, yields nil.
func sanitizedAttachmentContentID(value string, present bool) *string {
	if !present || strings.TrimSpace(value) == "" || utf16Length(value) > 500 || containsControl(value) {
		return nil
	}
	normalized := strings.Trim(strings.TrimSpace(value), "<>")
	if normalized == "" || containsWhitespace(normalized) {
		return nil
	}
	return &normalized
}

// attachmentFormResult is what readAttachmentForm collects from one
// multipart body: exactly the fields StageAttachment reads from
// IFormCollection (`:121-127`), plus fileCount and malformed so the caller
// can reproduce .NET's ordering (form read -> Files.Count != 1 -> size
// bounds) without a second pass over the body, which an already-consumed
// multipart.Reader cannot support anyway.
type attachmentFormResult struct {
	fileCount           int
	fileData            []byte
	fileName            string
	declaredContentType string
	contentIDRaw        string
	contentIDPresent    bool
	isInlineRaw         string
	malformed           bool
}

// readAttachmentForm reads every part of mr to EOF, capping the file part
// at maxBytes+1 bytes (enough to detect "too large" without buffering an
// arbitrarily larger body). malformed is set, and reading stops immediately,
// for any read error along the way — a bad multipart boundary, a part
// exceeding this module's own field-length caps below, or (most commonly)
// the router's own http.MaxBytesReader for this operation
// (module.go's bodyLimits) firing mid-read — mirroring .NET's single
// ReadFormAsync call, which reads the whole form eagerly and turns any of
// these into one InvalidDataException (`:119-120`) before the handler
// examines Files.Count or file.Length at all.
//
// A "file" is any part carrying a filename (multipart.Part.FileName() !=
// ""), the same test ASP.NET Core's own IFormCollection.Files population
// applies — not a part literally named "file"; the contract names that
// field "file" as the documented convention, but StageAttachment's own
// Files.Count check never filters by field name either.
func readAttachmentForm(mr *multipart.Reader, maxBytes int64) attachmentFormResult {
	var res attachmentFormResult
	if mr == nil {
		res.malformed = true
		return res
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return res
		}
		if err != nil {
			res.malformed = true
			return res
		}

		if part.FileName() != "" {
			res.fileCount++
			data, rerr := io.ReadAll(io.LimitReader(part, maxBytes+1))
			_ = part.Close()
			if rerr != nil {
				res.malformed = true
				return res
			}
			if res.fileCount == 1 {
				res.fileData = data
				res.fileName = part.FileName()
				res.declaredContentType = part.Header.Get("Content-Type")
			}
			continue
		}

		switch part.FormName() {
		case "contentId":
			data, rerr := io.ReadAll(io.LimitReader(part, 4096))
			_ = part.Close()
			if rerr != nil {
				res.malformed = true
				return res
			}
			res.contentIDRaw = string(data)
			res.contentIDPresent = true
		case "isInline":
			data, rerr := io.ReadAll(io.LimitReader(part, 64))
			_ = part.Close()
			if rerr != nil {
				res.malformed = true
				return res
			}
			res.isInlineRaw = string(data)
		default:
			_ = part.Close()
		}
	}
}

// deterministicUploadID is ObjectOwnershipLifecycle.DeterministicGuid's role
// for the staged-upload id (`:32-36`, `EP/ConversationEndpoints.cs:132`,
// inventory §12.3, §16.2): the same (uploaderUserId, idempotencyKey) always
// maps to the same id, and therefore the same storage key below, so a
// crash-retry with the same Idempotency-Key overwrites the exact object a
// prior, uncommitted attempt left behind instead of leaking a second one.
// This Go port does not reproduce .NET's exact byte order (a Guid built
// from a SHA-256 prefix interpreted little-endian in its first 8 bytes) —
// nothing here ever compares this id against a value a .NET process
// produced, so only self-consistency (same inputs -> same id, every time,
// in this process and any other Go process reading the same database)
// matters, the same "same conclusion, not same bytes" latitude
// fingerprintOf already takes in conversations_create.go.
func deterministicUploadID(uploaderUserID uuid.UUID, idempotencyKey string) uuid.UUID {
	sum := sha256.Sum256([]byte(uploaderUserID.String() + ":staged-upload:" + idempotencyKey))
	var id uuid.UUID
	copy(id[:], sum[:16])
	return id
}

// hexN is a uuid rendered as 32 lowercase hex characters with no dashes —
// .NET's ":N" format specifier, used throughout this module's storage keys
// (inventory §16.2: "`:N` = 32 lowercase hex, no dashes, throughout").
func hexN(id uuid.UUID) string { return strings.ReplaceAll(id.String(), "-", "") }

// stagedAttachmentStorageKey is StageAttachment's own key shape
// (`EP/ConversationEndpoints.cs:133`, inventory §16.2):
// "staged-attachments/{conversationId:N}/{uploaderUserId:N}/{uploadId:N}".
func stagedAttachmentStorageKey(conversationID, uploaderUserID, uploadID uuid.UUID) string {
	return fmt.Sprintf("staged-attachments/%s/%s/%s", hexN(conversationID), hexN(uploaderUserID), hexN(uploadID))
}

// reserveStorageKey is ObjectOwnershipLifecycle.ReserveAsync + the
// SaveChanges that follows it in the same statement (`SV/ObjectOwnershipLifecycle.cs:38-83`,
// `EP/ConversationEndpoints.cs:138-140`, inventory §5.4 step 8, §12.3): the
// cleanup ledger's reservation is written and durably committed, on its own,
// before the object-store write it is about to guard even begins — so a
// crash between this call and the write leaves a "staged" row a future
// cleanup sweep can find and reclaim by its own 10-minute expiry (design doc
// D3; no sweeper is built by this task). Deliberately runs directly against
// s.deps.Pool, outside any transaction shared with the later
// attachment_uploads insert: those two writes must land in two separate
// commits, not one, for the durability guarantee to mean anything.
//
// uploadID identifies the reservation in a wrapped error instead of key
// itself (fix round 2, item 2): key is the physical storage key, and
// wrapping it into an error string that reaches httpx.WriteError's generic
// "request failed" log line — the only unhandled-error path this function's
// caller can take — would put it in server logs, which never gets back to
// a caller (the contract rule holds either way) but is still an exposure no
// endpoint should manufacture. uploadID is already the identifier the
// caller, the database, and anyone reading that log line's other fields can
// correlate the reservation to.
func (s *server) reserveStorageKey(ctx context.Context, uploadID uuid.UUID, key string, now time.Time) error {
	q := store.New(s.deps.Pool)
	existing, err := q.FindLatestCleanupRecordByStorageKey(ctx, key)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return q.InsertCleanupRecord(ctx, store.InsertCleanupRecordParams{
			ID: uuid.New(), StorageKey: key, NextAttemptAt: now,
			ReservationExpiresAt: ptr(now.Add(reservationLifetime)), CreatedAt: now,
		})
	case err != nil:
		return fmt.Errorf("communications: find cleanup record for upload %s: %w", uploadID, err)
	case existing.Status == "owned" || existing.Status == "deleting":
		return errStorageKeyAlreadyOwned
	default:
		return q.ResetCleanupRecordToStaged(ctx, store.ResetCleanupRecordToStagedParams{
			ID: existing.ID, NextAttemptAt: now, ReservationExpiresAt: ptr(now.Add(reservationLifetime)),
		})
	}
}

// attachmentFingerprint is the SHA-256 hex of an uploaded file's bytes,
// stored on every upload as content_hash (StageAttachment `:134`, `:147-151`,
// `:167`). It is a content fingerprint, not a replay decision: fix round 1
// removed the fork that once compared it against a replay's own bytes (see
// this function's git history / task-6-report.md's fix-round-1 entry) —
// StageAttachment's replay lookup has no fingerprint at all
// (`:112-113`, inventory §5.1: "There is no payload fingerprint — a
// different file under the same key silently returns the *first* upload"),
// and this port now matches that exactly.
func attachmentFingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// PostCommunicationsConversationsByIdAttachments Stage an attachment
// (POST /api/v1/communications/conversations/{id}/attachments)
//
// StageAttachment (inventory §2, §5.4 — the dispatch's corrected order,
// which wins over the brief): (1) Idempotency-Key validity -> 400
// idempotency_key_required; (2) replay lookup by (uploaderUserId,
// idempotencyKey) -> 200 with the existing upload, unconditionally and
// *before* the conversation is known to exist — a different file under the
// same key silently returns the *first* upload, exactly as .NET does
// (`:112-113`, inventory §5.1; fix round 1 reverted an earlier fingerprint
// fork here that the task dispatch had mistakenly asked for, conflating
// this endpoint's replay with CreateConversation's and Reply's, which do
// carry a fingerprint); (3) conversation lookup -> 404 bare; (4) form read
// -> 413 on a malformed/oversized form; (5) exactly one file required -> 400
// file_required; (6) size in (0, maxBytes] -> 413; (7) twenty or more live
// uploads for this (conversation, user) -> 409 attachment_limit; (8) the
// reservation, durable before the object-store write; (9) the object-store
// write itself -> 503 attachment_storage_unavailable on any failure,
// leaving the reservation "staged" for a future sweep; (10) the
// attachment_uploads insert and MarkOwnedAsync in one commit, a unique
// violation on that insert (a concurrent identical replay) answering 200
// with the row the other request won — the same unconditional 200 as step 2.
func (s *server) PostCommunicationsConversationsByIdAttachments(ctx context.Context, req gen.PostCommunicationsConversationsByIdAttachmentsRequestObject) (gen.PostCommunicationsConversationsByIdAttachmentsResponseObject, error) {
	caller, err := callerUserID(ctx)
	if err != nil {
		return nil, err
	}

	key := req.Params.IdempotencyKey
	if !validIdempotencyKey(key) {
		return gen.PostCommunicationsConversationsByIdAttachments400JSONResponse(flatErrorBody(
			"idempotency_key_required", "A valid Idempotency-Key header is required.")), nil
	}

	maxBytes := s.deps.Config.CommunicationsAttachmentMaxBytes
	q := store.New(s.deps.Pool)
	existing, err := q.FindAttachmentUploadByUploaderAndKey(ctx, store.FindAttachmentUploadByUploaderAndKeyParams{
		UploadedByUserID: caller, IdempotencyKey: key,
	})
	if err == nil {
		return gen.PostCommunicationsConversationsByIdAttachments200JSONResponse(
			attachmentUploadResponseOf(attachmentUploadRowOf(existing))), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("communications: get attachment upload: %w", err)
	}

	if _, err := q.GetConversationByID(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCommunicationsConversationsByIdAttachments404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get conversation: %w", err)
	}

	form := readAttachmentForm(req.Body, maxBytes)
	if form.malformed {
		return gen.PostCommunicationsConversationsByIdAttachments413JSONResponse(flatErrorBody(
			"attachment_too_large", "The attachment exceeds the configured limit.")), nil
	}
	if form.fileCount != 1 {
		return gen.PostCommunicationsConversationsByIdAttachments400JSONResponse(flatErrorBody(
			"file_required", "Exactly one file is required.")), nil
	}
	size := int64(len(form.fileData))
	if size <= 0 || size > maxBytes {
		return gen.PostCommunicationsConversationsByIdAttachments413JSONResponse(flatErrorBody(
			"attachment_too_large", "The attachment exceeds the configured limit.")), nil
	}

	fileName := safeAttachmentFileName(form.fileName)
	contentType := sanitizedAttachmentContentType(form.declaredContentType)
	contentID := sanitizedAttachmentContentID(form.contentIDRaw, form.contentIDPresent)
	isInline := strings.EqualFold(form.isInlineRaw, "true") && contentID != nil

	liveCount, err := q.CountLiveAttachmentUploads(ctx, store.CountLiveAttachmentUploadsParams{
		ConversationID: req.Id, UploadedByUserID: caller,
	})
	if err != nil {
		return nil, fmt.Errorf("communications: count attachment uploads: %w", err)
	}
	if liveCount >= maxLiveAttachmentUploads {
		return gen.PostCommunicationsConversationsByIdAttachments409JSONResponse(flatErrorBody(
			"attachment_limit", "The attachment limit for this conversation has been reached.")), nil
	}

	uploadID := deterministicUploadID(caller, key)
	storageKey := stagedAttachmentStorageKey(req.Id, caller, uploadID)
	now := s.deps.Clock()

	if err := s.reserveStorageKey(ctx, uploadID, storageKey, now); err != nil {
		return nil, fmt.Errorf("communications: reserve storage key for upload %s: %w", uploadID, err)
	}

	if err := s.store.Put(ctx, storageKey, bytes.NewReader(form.fileData), contentType); err != nil {
		return gen.PostCommunicationsConversationsByIdAttachments503JSONResponse(flatErrorBody(
			"attachment_storage_unavailable", "Attachment storage is unavailable.")), nil
	}

	contentHash := attachmentFingerprint(form.fileData)

	var resp gen.AttachmentUploadResponse
	txErr := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		inserted, err := txq.InsertAttachmentUpload(ctx, store.InsertAttachmentUploadParams{
			ID: uploadID, ConversationID: req.Id, UploadedByUserID: caller, FileName: fileName,
			ContentType: contentType, SizeBytes: size, ContentHash: contentHash, ContentID: contentID,
			StorageKey: storageKey, IsInline: isInline, IdempotencyKey: key,
			ExpiresAt: now.Add(s.deps.Config.CommunicationsUploadExpiry), CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if err := txq.MarkCleanupRecordOwned(ctx, storageKey); err != nil {
			return err
		}
		resp = attachmentUploadResponseOf(attachmentUploadRowOf(inserted))
		return nil
	})
	if txErr != nil {
		// A concurrent request for the same (uploaderUserId, idempotencyKey)
		// can win the insert race on either of two constraints, not just the
		// natural key: uploadID is deterministicUploadID(caller, key) (this
		// file's own comment on that function explains why), so two
		// concurrent requests carrying the identical (uploaderUserId,
		// idempotencyKey) pair also compute the identical row id and collide
		// on attachment_uploads_pkey — which Postgres's index evaluation
		// order can report *instead of*
		// ux_attachment_uploads_uploaded_by_user_id_idempotency_key, not
		// alongside it. Checking only the named unique index here left a
		// genuine concurrent replay falling through to the unhandled 500
		// below (surfaced as a generic 23505 fallback 409, the wrong
		// vocabulary for this module — the same class of gap
		// channels.go's own two-constraint check guards against for
		// (type, address) vs (type, is_default)); fix round 1's gated
		// concurrency test caught this live.
		if db.IsUniqueViolation(txErr, "ux_attachment_uploads_uploaded_by_user_id_idempotency_key", "attachment_uploads_pkey") {
			// A concurrent request for the same (uploaderUserId,
			// idempotencyKey) won the insert first (`:183-187`) — the same
			// unconditional 200 the upfront replay lookup above answers,
			// whatever either request's own file bytes were.
			duplicate, derr := q.FindAttachmentUploadByUploaderAndKey(ctx, store.FindAttachmentUploadByUploaderAndKeyParams{
				UploadedByUserID: caller, IdempotencyKey: key,
			})
			if derr != nil {
				return nil, fmt.Errorf("communications: get attachment upload after race: %w", derr)
			}
			return gen.PostCommunicationsConversationsByIdAttachments200JSONResponse(
				attachmentUploadResponseOf(attachmentUploadRowOf(duplicate))), nil
		}
		return nil, fmt.Errorf("communications: stage attachment: %w", txErr)
	}
	return gen.PostCommunicationsConversationsByIdAttachments201JSONResponse(resp), nil
}

// attachmentStatusHeaders sets the no-cache/no-sniff headers
// GetAttachmentUploadStatus sets unconditionally, before its own lookup even
// runs (`EP/ConversationEndpoints.cs:196-198`, inventory §5.2), on whatever
// underlying response body results — the 200 and the bare 404 alike, the
// same shape identity's avatarResponseHeaders wraps its own avatar reads
// with.
type attachmentStatusHeaders struct {
	body gen.GetCommunicationsConversationsByConversationIdAttachmentsByAttachmentIdResponseObject
}

func (r attachmentStatusHeaders) VisitGetCommunicationsConversationsByConversationIdAttachmentsByAttachmentIdResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "no-store, no-cache, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	return r.body.VisitGetCommunicationsConversationsByConversationIdAttachmentsByAttachmentIdResponse(w)
}

// GetCommunicationsConversationsByConversationIdAttachmentsByAttachmentId
// Get a staged attachment's status
// (GET /api/v1/communications/conversations/{conversationId}/attachments/{attachmentId})
//
// GetAttachmentUploadStatus (inventory §5.2): scoped by (attachmentId,
// conversationId, uploaderUserId, scanStatus != "expired") together, so a
// valid upload id cannot be used to probe another conversation or another
// uploader's row — a mismatch on any one of those answers the same bare
// 404 as a genuinely unknown id.
func (s *server) GetCommunicationsConversationsByConversationIdAttachmentsByAttachmentId(ctx context.Context, req gen.GetCommunicationsConversationsByConversationIdAttachmentsByAttachmentIdRequestObject) (gen.GetCommunicationsConversationsByConversationIdAttachmentsByAttachmentIdResponseObject, error) {
	caller, err := callerUserID(ctx)
	if err != nil {
		return nil, err
	}
	q := store.New(s.deps.Pool)
	row, err := q.GetAttachmentUploadForCaller(ctx, store.GetAttachmentUploadForCallerParams{
		ID: req.AttachmentId, ConversationID: req.ConversationId, UploadedByUserID: caller,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return attachmentStatusHeaders{body: gen.GetCommunicationsConversationsByConversationIdAttachmentsByAttachmentId404Response{}}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get attachment upload: %w", err)
	}
	body := gen.GetCommunicationsConversationsByConversationIdAttachmentsByAttachmentId200JSONResponse(
		attachmentUploadResponseOf(attachmentUploadRowOf(row)))
	return attachmentStatusHeaders{body: body}, nil
}

// GetCommunicationsAttachmentsByIdDownload Download a message attachment
// (GET /api/v1/communications/attachments/{id}/download)
//
// DownloadAttachment (inventory §2, §5.4, §5.5, §19.2 items 22-23): scoped
// only by scan_status = "clean" — message-bound attachments alone, never a
// staged upload, and deliberately with no per-conversation authorisation
// (any conversations-view holder may fetch any clean attachment by id, a
// documented .NET quirk this port reproduces rather than "fixes"). A
// missing row, or a storage read that reports the object itself does not
// exist, both answer the same bare 404; any other storage failure answers
// 503 attachment_storage_unavailable rather than mapping to 404 and hiding
// a real outage. The response never carries the object's storage key —
// there is no field on GetCommunicationsAttachmentsByIdDownload200AsteriskResponse
// to put it in.
func (s *server) GetCommunicationsAttachmentsByIdDownload(ctx context.Context, req gen.GetCommunicationsAttachmentsByIdDownloadRequestObject) (gen.GetCommunicationsAttachmentsByIdDownloadResponseObject, error) {
	q := store.New(s.deps.Pool)
	attachment, err := q.GetCleanMessageAttachmentByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCommunicationsAttachmentsByIdDownload404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get message attachment: %w", err)
	}

	content, err := s.store.Get(ctx, attachment.StorageKey)
	if errors.Is(err, storage.ErrNotExist) {
		return gen.GetCommunicationsAttachmentsByIdDownload404Response{}, nil
	} else if err != nil {
		return gen.GetCommunicationsAttachmentsByIdDownload503JSONResponse(flatErrorBody(
			"attachment_storage_unavailable", "Attachment storage is unavailable.")), nil
	}

	return gen.GetCommunicationsAttachmentsByIdDownload200AsteriskResponse{
		Body: content, ContentType: sanitizedAttachmentContentType(attachment.ContentType), ContentLength: attachment.SizeBytes,
	}, nil
}
