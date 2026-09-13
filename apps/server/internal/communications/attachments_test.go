package communications_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is the attachment-staging and download area's tests
// (EP/ConversationEndpoints.cs:103-236, communications inventory §5, §12.3;
// task 6's brief and dispatch — the dispatch corrects and extends the
// brief, and wins where they disagree — are the authority for what must be
// pinned here) for postCommunicationsConversationsByIdAttachments,
// getCommunicationsConversationsByConversationIdAttachmentsByAttachmentId
// and getCommunicationsAttachmentsByIdDownload.

// attachmentUploadJSON is AttachmentUploadResponse.
type attachmentUploadJSON struct {
	ContentType string    `json:"contentType"`
	ExpiresAt   time.Time `json:"expiresAt"`
	FileName    string    `json:"fileName"`
	Id          string    `json:"id"`
	IsInline    bool      `json:"isInline"`
	Ready       bool      `json:"ready"`
	ScanStatus  string    `json:"scanStatus"`
	SizeBytes   int64     `json:"sizeBytes"`
}

// multipartBuilder assembles a multipart/form-data body one part at a time,
// in whatever order the test calls field/file — StageAttachment's own
// IFormCollection has no documented ordering requirement, and
// readAttachmentForm reads every part to EOF regardless of order.
type multipartBuilder struct {
	t   *testing.T
	buf bytes.Buffer
	w   *multipart.Writer
}

func newMultipartBuilder(t *testing.T) *multipartBuilder {
	t.Helper()
	b := &multipartBuilder{t: t}
	b.w = multipart.NewWriter(&b.buf)
	return b
}

func (b *multipartBuilder) field(name, value string) *multipartBuilder {
	b.t.Helper()
	fw, err := b.w.CreateFormField(name)
	if err != nil {
		b.t.Fatalf("create field %s: %v", name, err)
	}
	if _, err := fw.Write([]byte(value)); err != nil {
		b.t.Fatalf("write field %s: %v", name, err)
	}
	return b
}

// file adds a part carrying a filename, the same shape a browser's
// <input type="file"> produces — the test carries the field name of its
// choice so TestStageAttachment_FileRequiredWhenTwoFileParts can name both
// "file", exactly as a naive multi-file form post would.
func (b *multipartBuilder) file(fieldName, fileName, contentType string, data []byte) *multipartBuilder {
	b.t.Helper()
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, fieldName, fileName))
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	part, err := b.w.CreatePart(header)
	if err != nil {
		b.t.Fatalf("create file part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		b.t.Fatalf("write file part: %v", err)
	}
	return b
}

func (b *multipartBuilder) build() (contentType string, body []byte) {
	b.t.Helper()
	if err := b.w.Close(); err != nil {
		b.t.Fatalf("close multipart writer: %v", err)
	}
	return b.w.FormDataContentType(), b.buf.Bytes()
}

// stageAttachmentRaw posts a pre-built multipart body to conversationID's
// attachments endpoint with idempotencyKey.
func stageAttachmentRaw(c *modtest.Client, conversationID, idempotencyKey, contentType string, body []byte) *modtest.Response {
	return c.Do(http.MethodPost, "/api/v1/communications/conversations/"+conversationID+"/attachments", nil,
		modtest.RawBody(contentType, body), modtest.Header("Idempotency-Key", idempotencyKey))
}

// stageAttachment posts one "file" part, no extra fields — the common case
// most tests need.
func stageAttachment(t *testing.T, c *modtest.Client, conversationID, idempotencyKey, fileName, contentType string, data []byte) *modtest.Response {
	t.Helper()
	ct, body := newMultipartBuilder(t).file("file", fileName, contentType, data).build()
	return stageAttachmentRaw(c, conversationID, idempotencyKey, ct, body)
}

// mustStageAttachment stages data under a fresh conversation and a fresh
// idempotency key and fails t unless the response is 201, returning the
// decoded upload.
func mustStageAttachment(t *testing.T, c *modtest.Client, conversationID string, data []byte) attachmentUploadJSON {
	t.Helper()
	r := stageAttachment(t, c, conversationID, uuid.NewString(), "upload.bin", "application/octet-stream", data)
	if r.Status != http.StatusCreated {
		t.Fatalf("stage attachment: status %d body %s, want 201", r.Status, r.Body)
	}
	var upload attachmentUploadJSON
	r.JSON(&upload)
	return upload
}

// stagingConversation returns a fresh conversation id, created through a
// channel the fixture also provisions.
func stagingConversation(t *testing.T, h *modtest.Harness, c *modtest.Client) string {
	t.Helper()
	setupChannel(t, h)
	return createConversation(t, c, newConversationBody("someone@example.test")).ConversationId
}

// promoteStagedAttachmentToMessage copies a staged upload's storage_key and
// metadata onto a new message_attachments row bound to messageID — the same
// carry-over Reply's own promotion will do once a later task implements it
// (inventory §5.5: "the object itself is never moved or re-keyed"), built
// directly against the schema here because Reply is out of this task's
// scope and download must still be testable against a real, previously-Put
// object.
func promoteStagedAttachmentToMessage(t *testing.T, h *modtest.Harness, uploadID, messageID string) string {
	t.Helper()
	fileName := modtest.One[string](t, h, `SELECT file_name FROM communications.attachment_uploads WHERE id = $1`, uploadID)
	contentType := modtest.One[string](t, h, `SELECT content_type FROM communications.attachment_uploads WHERE id = $1`, uploadID)
	sizeBytes := modtest.One[int64](t, h, `SELECT size_bytes FROM communications.attachment_uploads WHERE id = $1`, uploadID)
	contentHash := modtest.One[string](t, h, `SELECT content_hash FROM communications.attachment_uploads WHERE id = $1`, uploadID)
	storageKey := modtest.One[string](t, h, `SELECT storage_key FROM communications.attachment_uploads WHERE id = $1`, uploadID)
	id := uuid.NewString()
	h.Exec(t, `INSERT INTO communications.message_attachments
		(id, message_id, file_name, content_type, size_bytes, content_hash, content_id, storage_key, scan_status, is_inline, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULL, $7, 'clean', false, $8)`,
		id, messageID, fileName, contentType, sizeBytes, contentHash, storageKey, h.Now())
	return id
}

// putFailsStore is a storage.ObjectStore whose Put always errors — for
// pinning StageAttachment's 503 attachment_storage_unavailable boundary
// deterministically, without depending on filesystem permission behaviour
// (unreliable when a test happens to run as root). Its other methods are
// never exercised by the test that uses it.
type putFailsStore struct{}

func (putFailsStore) Put(context.Context, string, io.Reader, string) error {
	return errors.New("modtest: object store put is unavailable")
}
func (putFailsStore) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("modtest: object store get is unavailable")
}
func (putFailsStore) Exists(context.Context, string) (bool, error) {
	return false, errors.New("modtest: object store exists is unavailable")
}
func (putFailsStore) Delete(context.Context, string) error {
	return errors.New("modtest: object store delete is unavailable")
}

// getFailsStore is a storage.ObjectStore whose Get always errors with
// something other than storage.ErrNotExist — pinning download's "a storage
// outage is 503, a missing object is 404" split (inventory §19.2 item 23).
type getFailsStore struct{}

func (getFailsStore) Put(context.Context, string, io.Reader, string) error { return nil }
func (getFailsStore) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("modtest: object store get is unavailable")
}
func (getFailsStore) Exists(context.Context, string) (bool, error) { return false, nil }
func (getFailsStore) Delete(context.Context, string) error         { return nil }

var _ storage.ObjectStore = putFailsStore{}
var _ storage.ObjectStore = getFailsStore{}

// ---- PostCommunicationsConversationsByIdAttachments (Stage) ----

// TestStageAttachment_ReplyAndViewBothRequired pins the permission pairing
// the dispatch's item 5 and the contract's own x-vantigo-access agree on
// for staging: conversations-reply alone is forbidden, conversations-view
// alone is forbidden, and only both together succeed.
func TestStageAttachment_ReplyAndViewBothRequired(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	convID := createConversation(t, h.SignIn(t, "communications:conversations-reply", "communications:conversations-view"), newConversationBody("x@example.test")).ConversationId

	if r := stageAttachment(t, h.SignIn(t, "communications:conversations-reply"), convID, uuid.NewString(), "a.txt", "text/plain", []byte("hi")); r.Status != http.StatusForbidden {
		t.Errorf("reply alone: status %d, want 403", r.Status)
	}
	if r := stageAttachment(t, h.SignIn(t, "communications:conversations-view"), convID, uuid.NewString(), "a.txt", "text/plain", []byte("hi")); r.Status != http.StatusForbidden {
		t.Errorf("view alone: status %d, want 403", r.Status)
	}
	both := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	if r := stageAttachment(t, both, convID, uuid.NewString(), "a.txt", "text/plain", []byte("hi")); r.Status != http.StatusCreated {
		t.Errorf("reply+view: status %d body %s, want 201", r.Status, r.Body)
	}
}

// TestStageAttachment_IdempotencyKeyTooLongIs400 mirrors
// TestCreateConversation_IdempotencyKeyTooLongIs400's own reasoning: an
// entirely absent Idempotency-Key header never reaches this handler (the
// contract marks it required, refused earlier by the generated decoder), so
// this pins the module's *own* idempotency_key_required with a
// present-but-invalid key, exact code and exact message both asserted — a
// mutation that swapped either would slip past a status-code-only check.
func TestStageAttachment_IdempotencyKeyTooLongIs400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)

	r := stageAttachment(t, c, convID, strings.Repeat("k", 201), "a.txt", "text/plain", []byte("hi"))
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	if body.Error.Code != "idempotency_key_required" {
		t.Errorf("code = %q, want idempotency_key_required", body.Error.Code)
	}
	if body.Error.Message != "A valid Idempotency-Key header is required." {
		t.Errorf("message = %q, want the exact idempotency_key_required text", body.Error.Message)
	}
}

// TestStageAttachment_ConversationNotFoundIsBare404 pins the plain
// not-found path: a real, valid Idempotency-Key with no prior upload for it
// and an unknown conversation id answers a bare 404 (no body) — this is the
// non-replay branch of dispatch item 2's ordering.
func TestStageAttachment_ConversationNotFoundIsBare404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")

	r := stageAttachment(t, c, uuid.NewString(), uuid.NewString(), "a.txt", "text/plain", []byte("hi"))
	if r.Status != http.StatusNotFound {
		t.Fatalf("status %d body %s, want 404", r.Status, r.Body)
	}
	if len(r.Body) != 0 {
		t.Errorf("body = %q, want empty (bare 404)", r.Body)
	}
}

// TestStageAttachment_ReplaySameFingerprintIs200 pins the dispatch's
// correction to the brief: a replay of the same (uploaderUserId,
// idempotencyKey) whose file content matches the original answers 200 with
// the original upload — not a second row.
func TestStageAttachment_ReplaySameFingerprintIs200(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)
	key := uuid.NewString()
	data := []byte("identical bytes")

	first := stageAttachment(t, c, convID, key, "a.txt", "text/plain", data)
	if first.Status != http.StatusCreated {
		t.Fatalf("first: status %d body %s, want 201", first.Status, first.Body)
	}
	var firstUpload attachmentUploadJSON
	first.JSON(&firstUpload)

	second := stageAttachment(t, c, convID, key, "a.txt", "text/plain", data)
	if second.Status != http.StatusOK {
		t.Fatalf("second: status %d body %s, want 200 (replay)", second.Status, second.Body)
	}
	var secondUpload attachmentUploadJSON
	second.JSON(&secondUpload)
	if secondUpload.Id != firstUpload.Id {
		t.Errorf("replay id = %s, want %s", secondUpload.Id, firstUpload.Id)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.attachment_uploads WHERE conversation_id = $1`, convID); n != 1 {
		t.Errorf("attachment_uploads rows = %d, want 1", n)
	}
}

// TestStageAttachment_ReplayDifferentPayloadIs409 pins the fork's other
// half: a replay under the same key whose file content differs answers 409
// idempotency_key_reused, byte for byte the same message text
// idempotency_key_reused carries for CreateConversation and Reply — and
// never creates a second row or silently overwrites the first, unlike
// .NET's own StageAttachment (inventory §5.1).
func TestStageAttachment_ReplayDifferentPayloadIs409(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)
	key := uuid.NewString()

	first := stageAttachment(t, c, convID, key, "a.txt", "text/plain", []byte("original bytes"))
	if first.Status != http.StatusCreated {
		t.Fatalf("first: status %d body %s, want 201", first.Status, first.Body)
	}
	var firstUpload attachmentUploadJSON
	first.JSON(&firstUpload)

	second := stageAttachment(t, c, convID, key, "a.txt", "text/plain", []byte("a completely different file"))
	if second.Status != http.StatusConflict {
		t.Fatalf("second: status %d body %s, want 409", second.Status, second.Body)
	}
	var body commErrorJSON
	second.JSON(&body)
	if body.Error.Code != "idempotency_key_reused" {
		t.Errorf("code = %q, want idempotency_key_reused", body.Error.Code)
	}
	if body.Error.Message != "The Idempotency-Key was already used with a different payload." {
		t.Errorf("message = %q, want the exact idempotency_key_reused text", body.Error.Message)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.attachment_uploads WHERE conversation_id = $1`, convID); n != 1 {
		t.Errorf("attachment_uploads rows = %d, want 1 (the mismatched replay must not create a second)", n)
	}
	if got := h.Count(t, `SELECT count(*) FROM communications.attachment_uploads WHERE id = $1 AND file_name = 'a.txt'`, firstUpload.Id); got != 1 {
		t.Errorf("the original row must survive unchanged")
	}
}

// TestStageAttachment_ReplayNeverChecksConversationExistence pins dispatch
// item 2's headline correction directly: the replay decision (whichever way
// it forks) never touches req.Id's conversation lookup at all. A replay
// against a URL naming a conversation id that never existed still answers
// 200 for a matching payload — if the handler ever moved the replay lookup
// after the conversation-existence check, this would regress to 404.
func TestStageAttachment_ReplayNeverChecksConversationExistence(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)
	key := uuid.NewString()
	data := []byte("same bytes both times")

	first := stageAttachment(t, c, convID, key, "a.txt", "text/plain", data)
	if first.Status != http.StatusCreated {
		t.Fatalf("first: status %d body %s, want 201", first.Status, first.Body)
	}

	neverExisted := uuid.NewString()
	replay := stageAttachment(t, c, neverExisted, key, "a.txt", "text/plain", data)
	if replay.Status != http.StatusOK {
		t.Fatalf("replay against an unknown conversation id: status %d body %s, want 200 (the replay must never reach the conversation lookup)", replay.Status, replay.Body)
	}
}

// TestStageAttachment_FileRequiredWhenNoFilePart pins dispatch item 2's
// "Files.Count != 1 -> 400 file_required" boundary on the low side: a
// well-formed multipart body with no file part at all.
func TestStageAttachment_FileRequiredWhenNoFilePart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)

	ct, body := newMultipartBuilder(t).field("contentId", "x").build()
	r := stageAttachmentRaw(c, convID, uuid.NewString(), ct, body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var e commErrorJSON
	r.JSON(&e)
	if e.Error.Code != "file_required" {
		t.Errorf("code = %q, want file_required", e.Error.Code)
	}
	if e.Error.Message != "Exactly one file is required." {
		t.Errorf("message = %q, want the exact file_required text", e.Error.Message)
	}
}

// TestStageAttachment_FileRequiredWhenTwoFileParts is the same boundary
// from the high side: exactly one file is required, not "at least one".
func TestStageAttachment_FileRequiredWhenTwoFileParts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)

	ct, body := newMultipartBuilder(t).
		file("file", "a.txt", "text/plain", []byte("a")).
		file("file", "b.txt", "text/plain", []byte("b")).
		build()
	r := stageAttachmentRaw(c, convID, uuid.NewString(), ct, body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var e commErrorJSON
	r.JSON(&e)
	if e.Error.Code != "file_required" {
		t.Errorf("code = %q, want file_required", e.Error.Code)
	}
}

// TestStageAttachment_ZeroByteFileIs413 pins the size lower bound: "length
// in (0, maxBytes]" — an empty file is rejected, not silently accepted as a
// valid zero-length attachment.
func TestStageAttachment_ZeroByteFileIs413(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)

	r := stageAttachment(t, c, convID, uuid.NewString(), "empty.txt", "text/plain", []byte{})
	if r.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d body %s, want 413", r.Status, r.Body)
	}
	var e commErrorJSON
	r.JSON(&e)
	if e.Error.Code != "attachment_too_large" {
		t.Errorf("code = %q, want attachment_too_large", e.Error.Code)
	}
	if e.Error.Message != "The attachment exceeds the configured limit." {
		t.Errorf("message = %q, want the exact attachment_too_large text", e.Error.Message)
	}
}

// TestStageAttachment_SizeAtConfiguredLimitSucceedsOneByteOverFails pins the
// upper size boundary precisely, with a small configured limit so the test
// need not push megabytes over the wire: exactly maxBytes succeeds, and
// maxBytes+1 answers 413 — the mutation this catches is "> maxBytes"
// silently becoming ">= maxBytes" or vice versa.
func TestStageAttachment_SizeAtConfiguredLimitSucceedsOneByteOverFails(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithEnv("COMMUNICATIONS_ATTACHMENT_MAX_BYTES", "16"))
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)

	atLimit := bytes.Repeat([]byte{'a'}, 16)
	r := stageAttachment(t, c, convID, uuid.NewString(), "at-limit.bin", "application/octet-stream", atLimit)
	if r.Status != http.StatusCreated {
		t.Fatalf("at limit (16 bytes): status %d body %s, want 201", r.Status, r.Body)
	}

	overLimit := bytes.Repeat([]byte{'a'}, 17)
	r2 := stageAttachment(t, c, convID, uuid.NewString(), "over-limit.bin", "application/octet-stream", overLimit)
	if r2.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("one byte over (17 bytes): status %d body %s, want 413", r2.Status, r2.Body)
	}
}

// TestStageAttachment_OversizedFormBodyIs413 pins dispatch item 2's "form
// read -> 413 on a malformed/oversized form" boundary: a request whose
// *total* body (a large non-file field plus a small, otherwise-acceptable
// file) crosses the router's own per-operation body cap
// (module.go's bodyLimits, configured from CommunicationsAttachmentMaxBytes)
// before the handler ever gets to inspect the file part on its own —
// mirroring .NET's InvalidDataException from ReadFormAsync's own
// MultipartBodyLengthLimit, the same "filler field" technique
// identity/avatar_test.go's avatarUploadWithFiller uses for its own body
// cap.
func TestStageAttachment_OversizedFormBodyIs413(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithEnv("COMMUNICATIONS_ATTACHMENT_MAX_BYTES", "1024"))
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)

	ct, body := newMultipartBuilder(t).
		field("filler", strings.Repeat("x", 200*1024)).
		file("file", "small.txt", "text/plain", []byte("small enough on its own")).
		build()
	r := stageAttachmentRaw(c, convID, uuid.NewString(), ct, body)
	if r.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d body %s, want 413 (the whole request exceeds the operation's body cap)", r.Status, r.Body)
	}
	var e commErrorJSON
	r.JSON(&e)
	if e.Error.Code != "attachment_too_large" {
		t.Errorf("code = %q, want attachment_too_large", e.Error.Code)
	}
}

// TestStageAttachment_AttachmentLimitBoundary pins the "twenty or more live
// uploads" boundary both ways: the 20th upload for a (conversation, user)
// succeeds, the 21st answers 409 attachment_limit with the exact message —
// the mutation this catches is ">= 20" silently becoming "> 20".
func TestStageAttachment_AttachmentLimitBoundary(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)

	for i := range 20 {
		r := stageAttachment(t, c, convID, uuid.NewString(), fmt.Sprintf("f%d.txt", i), "text/plain", []byte(fmt.Sprintf("content-%d", i)))
		if r.Status != http.StatusCreated {
			t.Fatalf("upload %d: status %d body %s, want 201", i, r.Status, r.Body)
		}
	}
	r := stageAttachment(t, c, convID, uuid.NewString(), "f20.txt", "text/plain", []byte("content-20"))
	if r.Status != http.StatusConflict {
		t.Fatalf("upload 21: status %d body %s, want 409", r.Status, r.Body)
	}
	var e commErrorJSON
	r.JSON(&e)
	if e.Error.Code != "attachment_limit" {
		t.Errorf("code = %q, want attachment_limit", e.Error.Code)
	}
	if e.Error.Message != "The attachment limit for this conversation has been reached." {
		t.Errorf("message = %q, want the exact attachment_limit text", e.Error.Message)
	}
}

// TestStageAttachment_StorageFailureIs503 pins the object-store write's own
// boundary: any Put failure answers 503 attachment_storage_unavailable, and
// — because the reservation (attachment_cleanup_records) is written and
// committed *before* the Put attempt (inventory §5.4 step 8) — no
// attachment_uploads row is ever created for the failed attempt, and the
// cleanup record is left "staged" rather than "owned". A mutation that
// reordered the reservation after the Put, or that marked the record
// "owned" regardless of the Put's outcome, would pass a status-code-only
// check but fail the two assertions below.
func TestStageAttachment_StorageFailureIs503(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithObjectStore(putFailsStore{}))
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)

	r := stageAttachment(t, c, convID, uuid.NewString(), "a.txt", "text/plain", []byte("hi"))
	if r.Status != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s, want 503", r.Status, r.Body)
	}
	var e commErrorJSON
	r.JSON(&e)
	if e.Error.Code != "attachment_storage_unavailable" {
		t.Errorf("code = %q, want attachment_storage_unavailable", e.Error.Code)
	}
	if e.Error.Message != "Attachment storage is unavailable." {
		t.Errorf("message = %q, want the exact attachment_storage_unavailable text", e.Error.Message)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.attachment_uploads WHERE conversation_id = $1`, convID); n != 0 {
		t.Errorf("attachment_uploads rows = %d, want 0 (the Put failed before the metadata insert)", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE status = 'staged'`); n != 1 {
		t.Errorf(`attachment_cleanup_records rows with status='staged' = %d, want 1 (the reservation, committed before the failed Put, is left for a future sweep)`, n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE status = 'owned'`); n != 0 {
		t.Errorf("attachment_cleanup_records rows with status='owned' = %d, want 0", n)
	}
}

// TestStageAttachment_ReservationTransitionsToOwnedOnSuccess is the success
// mirror of TestStageAttachment_StorageFailureIs503: once the Put and the
// metadata insert both succeed, the same cleanup-ledger row MarkOwnedAsync
// transitions in the *same commit* as the attachment_uploads insert
// (inventory §5.4 step 11) is 'owned', not left 'staged' — a mutation that
// dropped the MarkCleanupRecordOwned call, or that ran it outside the
// transaction, would leave the reservation looking exactly like a crashed
// upload a future sweep should reclaim, even though the upload succeeded.
func TestStageAttachment_ReservationTransitionsToOwnedOnSuccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)

	mustStageAttachment(t, c, convID, []byte("hi"))

	if n := h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE status = 'owned'`); n != 1 {
		t.Errorf("attachment_cleanup_records rows with status='owned' = %d, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE status = 'staged'`); n != 0 {
		t.Errorf("attachment_cleanup_records rows with status='staged' = %d, want 0 (none left behind on success)", n)
	}
}

// TestStageAttachment_ResponseNeverExposesTheStorageKey is the contract
// rule the dispatch pins explicitly: "No endpoint may expose a storage
// key." Greps the raw response body — not a typed field access, which
// would trivially pass simply because gen.AttachmentUploadResponse has no
// such field — for the exact key this upload was given.
func TestStageAttachment_ResponseNeverExposesTheStorageKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)

	r := stageAttachment(t, c, convID, uuid.NewString(), "a.txt", "text/plain", []byte("hi"))
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var upload attachmentUploadJSON
	r.JSON(&upload)
	storageKey := modtest.One[string](t, h, `SELECT storage_key FROM communications.attachment_uploads WHERE id = $1`, upload.Id)
	if storageKey == "" {
		t.Fatal("fixture error: storage_key is empty")
	}
	if bytes.Contains(r.Body, []byte(storageKey)) {
		t.Errorf("response body contains the storage key %q: %s", storageKey, r.Body)
	}
	if bytes.Contains(r.Body, []byte("staged-attachments")) {
		t.Errorf("response body leaks the storage key scheme: %s", r.Body)
	}
}

// TestStageAttachment_ReadyMirrorsScanStatusClean pins D2: every staged
// upload is born scan_status="clean" (there is nothing left to scan), and
// ready is a derived duplicate of that, never an independent flag.
func TestStageAttachment_ReadyMirrorsScanStatusClean(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)

	upload := mustStageAttachment(t, c, convID, []byte("hi"))
	if upload.ScanStatus != "clean" {
		t.Errorf("scanStatus = %q, want clean", upload.ScanStatus)
	}
	if !upload.Ready {
		t.Error("ready = false, want true (ready == scanStatus == clean)")
	}
}

// TestStageAttachment_IsInlineRequiresAValidContentId pins AttachmentSafety's
// rule (inventory §5.3): isInline is only honoured when a valid contentId
// accompanies it — "true" alone, with no contentId, must not set it.
func TestStageAttachment_IsInlineRequiresAValidContentId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)

	ct, body := newMultipartBuilder(t).
		field("isInline", "true").
		file("file", "inline.png", "image/png", []byte("fake-png-bytes")).
		build()
	r := stageAttachmentRaw(c, convID, uuid.NewString(), ct, body)
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var upload attachmentUploadJSON
	r.JSON(&upload)
	if upload.IsInline {
		t.Error("isInline = true, want false (isInline=true was sent with no contentId)")
	}

	ct2, body2 := newMultipartBuilder(t).
		field("isInline", "true").
		field("contentId", "cid:logo").
		file("file", "inline.png", "image/png", []byte("fake-png-bytes-2")).
		build()
	r2 := stageAttachmentRaw(c, convID, uuid.NewString(), ct2, body2)
	if r2.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r2.Status, r2.Body)
	}
	var upload2 attachmentUploadJSON
	r2.JSON(&upload2)
	if !upload2.IsInline {
		t.Error("isInline = false, want true (isInline=true was sent with a valid contentId)")
	}
}

// TestStageAttachment_SafeFileNameStripsPathAndControlCharacters pins
// AttachmentSafety.SafeFileName (inventory §5.3): an untrusted client
// filename carrying path segments is reduced to its last segment.
func TestStageAttachment_SafeFileNameStripsPathAndControlCharacters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)

	r := stageAttachment(t, c, convID, uuid.NewString(), "../../etc/evil.txt", "text/plain", []byte("hi"))
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var upload attachmentUploadJSON
	r.JSON(&upload)
	if upload.FileName != "evil.txt" {
		t.Errorf("fileName = %q, want %q (the last path segment only)", upload.FileName, "evil.txt")
	}
}

// ---- GetCommunicationsConversationsByConversationIdAttachmentsByAttachmentId (status) ----

// TestGetAttachmentUploadStatus_RequiresView pins the *actual* contract
// rule for this operation — openapi/communications.yaml's own
// x-vantigo-access on
// getCommunicationsConversationsByConversationIdAttachmentsByAttachmentId is
// "permission:communications:conversations-view" alone, not reply+view. The
// dispatch's prose (item 5) says this operation needs both; the committed,
// router-enforced contract disagrees. module.Router derives every
// permission check from the contract, not from this handler, so this test
// pins the contract as it is actually enforced and flags the discrepancy
// for review rather than silently contradicting either source.
//
// It cannot isolate "view alone is sufficient" from "and the row must
// belong to you" as cleanly as the equivalent tests for other operations
// do: this endpoint's own lookup is scoped by uploaderUserId (inventory
// §5.2), and modtest's SignIn mints a brand-new user on every call with no
// way to plant a session for a chosen, pre-existing id — so a second
// "view-only" signed-in client can never be the same principal who staged
// the fixture upload; it can only ever be told apart from a genuine owner
// by getting 404, not 403, which TestGetAttachmentUploadStatus_ScopedByConversationAndUploader
// already covers from the other end. What this test can and does prove:
// conversations-reply alone (no view) is forbidden, so view really is the
// permission this operation demands, not a leftover checked-but-unused
// requirement; the uploader's own reply+view client succeeds.
func TestGetAttachmentUploadStatus_RequiresView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	uploader := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, uploader)
	upload := mustStageAttachment(t, uploader, convID, []byte("hi"))

	path := "/api/v1/communications/conversations/" + convID + "/attachments/" + upload.Id
	if r := h.SignIn(t).Do(http.MethodGet, path, nil); r.Status != http.StatusForbidden {
		t.Errorf("no permission: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t, "communications:conversations-reply").Do(http.MethodGet, path, nil); r.Status != http.StatusForbidden {
		t.Errorf("reply alone (no view): status %d, want 403 (view is the permission this operation actually demands)", r.Status)
	}
	if r := uploader.Do(http.MethodGet, path, nil); r.Status != http.StatusOK {
		t.Errorf("the uploader's own reply+view client: status %d body %s, want 200", r.Status, r.Body)
	}
}

// TestGetAttachmentUploadStatus_ScopedByConversationAndUploader pins
// inventory §5.2: the lookup is scoped by (attachmentId, conversationId,
// uploaderUserId) together, so a valid upload id cannot be used to probe
// another conversation. Even a caller who holds conversations-view (and can
// therefore reach the endpoint at all) gets 404 for the wrong conversation
// id, because the scoping is by row ownership, not by permission alone.
func TestGetAttachmentUploadStatus_ScopedByConversationAndUploader(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	uploader := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, uploader)
	upload := mustStageAttachment(t, uploader, convID, []byte("hi"))

	otherConvID := stagingConversation(t, h, uploader)
	wrongConvPath := "/api/v1/communications/conversations/" + otherConvID + "/attachments/" + upload.Id
	if r := uploader.Do(http.MethodGet, wrongConvPath, nil); r.Status != http.StatusNotFound {
		t.Errorf("wrong conversation id: status %d, want 404", r.Status)
	}

	rightPath := "/api/v1/communications/conversations/" + convID + "/attachments/" + upload.Id
	if r := uploader.Do(http.MethodGet, rightPath, nil); r.Status != http.StatusOK {
		t.Errorf("right conversation id: status %d body %s, want 200", r.Status, r.Body)
	}
}

// TestGetAttachmentUploadStatus_UnknownIdIsBare404 pins the plain
// not-found shape.
func TestGetAttachmentUploadStatus_UnknownIdIsBare404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)

	r := c.Do(http.MethodGet, "/api/v1/communications/conversations/"+convID+"/attachments/"+uuid.NewString(), nil)
	if r.Status != http.StatusNotFound {
		t.Fatalf("status %d, want 404", r.Status)
	}
	if len(r.Body) != 0 {
		t.Errorf("body = %q, want empty (bare 404)", r.Body)
	}
}

// TestGetAttachmentUploadStatus_NoCacheHeaders pins inventory §5.2's
// explicit header set, asserted on both the 200 and the 404 path since
// .NET sets them unconditionally before its own lookup runs.
func TestGetAttachmentUploadStatus_NoCacheHeaders(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)
	upload := mustStageAttachment(t, c, convID, []byte("hi"))

	checkHeaders := func(t *testing.T, r *modtest.Response) {
		t.Helper()
		if got := r.Header("Cache-Control"); got != "no-store, no-cache, private" {
			t.Errorf("Cache-Control = %q, want %q", got, "no-store, no-cache, private")
		}
		if got := r.Header("Pragma"); got != "no-cache" {
			t.Errorf("Pragma = %q, want no-cache", got)
		}
		if got := r.Header("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
		}
	}

	found := c.Do(http.MethodGet, "/api/v1/communications/conversations/"+convID+"/attachments/"+upload.Id, nil)
	checkHeaders(t, found)

	notFound := c.Do(http.MethodGet, "/api/v1/communications/conversations/"+convID+"/attachments/"+uuid.NewString(), nil)
	checkHeaders(t, notFound)
}

// ---- GetCommunicationsAttachmentsByIdDownload ----

// TestDownloadAttachment_ViewAloneSuffices pins the dispatch's permission
// claim for download, which does match the committed contract: view alone.
func TestDownloadAttachment_ViewAloneSuffices(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	uploader := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, uploader)
	m := createConversation(t, uploader, newConversationBody("x@example.test"))
	upload := mustStageAttachment(t, uploader, convID, []byte("file bytes"))
	attID := promoteStagedAttachmentToMessage(t, h, upload.Id, *m.MessageId)

	path := "/api/v1/communications/attachments/" + attID + "/download"
	if r := h.SignIn(t).Do(http.MethodGet, path, nil); r.Status != http.StatusForbidden {
		t.Errorf("no permission: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t, "communications:conversations-view").Do(http.MethodGet, path, nil); r.Status != http.StatusOK {
		t.Errorf("conversations-view alone: status %d, want 200", r.Status)
	}
}

// TestDownloadAttachment_ServesTheStoredBytesAndContentType pins the basic
// success path: the exact bytes this upload was staged with, and the
// content type sanitizeAttachmentContentType resolved for it.
func TestDownloadAttachment_ServesTheStoredBytesAndContentType(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)
	m := createConversation(t, c, newConversationBody("x@example.test"))
	data := []byte("the exact bytes this attachment carries")
	r := stageAttachment(t, c, convID, uuid.NewString(), "report.txt", "text/plain", data)
	if r.Status != http.StatusCreated {
		t.Fatalf("stage: status %d body %s, want 201", r.Status, r.Body)
	}
	var upload attachmentUploadJSON
	r.JSON(&upload)
	attID := promoteStagedAttachmentToMessage(t, h, upload.Id, *m.MessageId)

	dl := c.Do(http.MethodGet, "/api/v1/communications/attachments/"+attID+"/download", nil)
	if dl.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", dl.Status, dl.Body)
	}
	if !bytes.Equal(dl.Body, data) {
		t.Errorf("body = %q, want %q", dl.Body, data)
	}
	if got := dl.Header("Content-Type"); got != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", got)
	}
}

// TestDownloadAttachment_ServesMessageBoundAttachmentsOnly pins the
// dispatch's item 4: a staged upload — never promoted to a message
// attachment — is not downloadable through this endpoint even though its id
// is a real, live upload id. Download queries message_attachments, never
// attachment_uploads.
func TestDownloadAttachment_ServesMessageBoundAttachmentsOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, c)
	upload := mustStageAttachment(t, c, convID, []byte("staged, never sent"))

	r := c.Do(http.MethodGet, "/api/v1/communications/attachments/"+upload.Id+"/download", nil)
	if r.Status != http.StatusNotFound {
		t.Fatalf("status %d body %s, want 404 (a staged upload is not a message attachment)", r.Status, r.Body)
	}
}

// TestDownloadAttachment_NoPerConversationScoping pins inventory §19.2 item
// 22 as a deliberate, preserved quirk: any conversations-view holder may
// download any clean attachment by id, whether or not they staged it, are
// assigned to its conversation, or have ever interacted with it at all.
func TestDownloadAttachment_NoPerConversationScoping(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := stagingConversation(t, h, owner)
	m := createConversation(t, owner, newConversationBody("x@example.test"))
	upload := mustStageAttachment(t, owner, convID, []byte("someone else's file"))
	attID := promoteStagedAttachmentToMessage(t, h, upload.Id, *m.MessageId)

	stranger := h.SignIn(t, "communications:conversations-view") // never touched convID
	r := stranger.Do(http.MethodGet, "/api/v1/communications/attachments/"+attID+"/download", nil)
	if r.Status != http.StatusOK {
		t.Errorf("an unrelated conversations-view holder: status %d body %s, want 200", r.Status, r.Body)
	}
}

// TestDownloadAttachment_NonCleanScanStatusIs404 pins the scanStatus gate
// (inventory §5.5): only scan_status='clean' message attachments are
// downloadable. Every attachment this port's own handlers ever write is
// 'clean' (D2), so this fixture writes a non-clean row directly to prove
// the gate is enforced, not merely vacuously true.
func TestDownloadAttachment_NonCleanScanStatusIs404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	setupChannel(t, h)
	m := createConversation(t, c, newConversationBody("x@example.test"))
	id := uuid.NewString()
	h.Exec(t, `INSERT INTO communications.message_attachments
		(id, message_id, file_name, content_type, size_bytes, content_hash, content_id, storage_key, scan_status, is_inline, created_at)
		VALUES ($1, $2, 'quarantined.bin', 'application/octet-stream', 3, 'deadbeef', NULL, 'staged-attachments/does/not/matter', 'quarantined', false, $3)`,
		id, *m.MessageId, h.Now())

	r := c.Do(http.MethodGet, "/api/v1/communications/attachments/"+id+"/download", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404 (scan_status is not clean)", r.Status, r.Body)
	}
}

// TestDownloadAttachment_MissingObjectInStoreIs404 pins the row-exists-but-
// object-missing case: a message_attachments row whose storage_key was
// never actually Put answers the same bare 404 a missing row does.
func TestDownloadAttachment_MissingObjectInStoreIs404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	setupChannel(t, h)
	m := createConversation(t, c, newConversationBody("x@example.test"))
	id := uuid.NewString()
	h.Exec(t, `INSERT INTO communications.message_attachments
		(id, message_id, file_name, content_type, size_bytes, content_hash, content_id, storage_key, scan_status, is_inline, created_at)
		VALUES ($1, $2, 'ghost.bin', 'application/octet-stream', 3, 'deadbeef', NULL, 'staged-attachments/never/put/anything', 'clean', false, $3)`,
		id, *m.MessageId, h.Now())

	r := c.Do(http.MethodGet, "/api/v1/communications/attachments/"+id+"/download", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404 (the row exists but the object was never written)", r.Status, r.Body)
	}
}

// TestDownloadAttachment_StorageOutageIs503 pins inventory §19.2 item 23:
// deliberately separated from the missing-object 404 — any storage failure
// other than "the object does not exist" answers 503, never 404, so an
// outage is never mistaken for the attachment being genuinely gone.
func TestDownloadAttachment_StorageOutageIs503(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithObjectStore(getFailsStore{}))
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	setupChannel(t, h)
	m := createConversation(t, c, newConversationBody("x@example.test"))
	id := uuid.NewString()
	h.Exec(t, `INSERT INTO communications.message_attachments
		(id, message_id, file_name, content_type, size_bytes, content_hash, content_id, storage_key, scan_status, is_inline, created_at)
		VALUES ($1, $2, 'a.bin', 'application/octet-stream', 3, 'deadbeef', NULL, 'staged-attachments/whatever/key', 'clean', false, $3)`,
		id, *m.MessageId, h.Now())

	r := c.Do(http.MethodGet, "/api/v1/communications/attachments/"+id+"/download", nil)
	if r.Status != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s, want 503", r.Status, r.Body)
	}
	var e commErrorJSON
	r.JSON(&e)
	if e.Error.Code != "attachment_storage_unavailable" {
		t.Errorf("code = %q, want attachment_storage_unavailable", e.Error.Code)
	}
	if e.Error.Message != "Attachment storage is unavailable." {
		t.Errorf("message = %q, want the exact attachment_storage_unavailable text", e.Error.Message)
	}
}

// TestDownloadAttachment_UnknownIdIsBare404 pins the plain not-found shape.
func TestDownloadAttachment_UnknownIdIsBare404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view")

	r := c.Do(http.MethodGet, "/api/v1/communications/attachments/"+uuid.NewString()+"/download", nil)
	if r.Status != http.StatusNotFound {
		t.Fatalf("status %d, want 404", r.Status)
	}
	if len(r.Body) != 0 {
		t.Errorf("body = %q, want empty (bare 404)", r.Body)
	}
}
