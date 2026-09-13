package communications

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// internalRecorder is this file's own contract recorder — package
// communications_test's main_test.go already has one (and the
// pendingOperations coverage gate it drives), but that var is unexported in
// a different package. A white-box test still needs *a* recorder to
// validate every modtest exchange against communications.yaml; it just does
// not need to be the *same* instance the coverage gate reads, since this
// file's black-box-style HTTP calls (the step 6 / step 5-vs-6 tests below)
// are not the only tests exercising postCommunicationsConversationsByIdReply
// — conversations_reply_test.go's tests already do, against the real
// recorder, satisfying RequireCoverage on their own.
var internalRecorder = contracttest.New(mustLoadCommunicationsContract())

func mustLoadCommunicationsContract() *openapi3.T {
	doc, err := openapi.Load(context.Background(), "communications")
	if err != nil {
		panic(err)
	}
	return doc
}

// This file is Reply's white-box tests: package communications, not
// communications_test, because steps 8, 9 and 10 of QueueOutboundAsync
// (conversations_reply.go's own top-of-file comment has the full ten-step
// order) are unreachable through the real HTTP handler — step 7
// (recipients_missing) always fires first, since this port can never write
// a row with conversation_messages.direction = 'inbound' (see
// GetLatestInboundParticipantAddress's comment in queries/conversations.sql).
//
// Fix round 1 corrected how steps 8-10 are exercised here. The original
// shape called queueReply directly with hand-supplied caller/channelType/
// fingerprint/now — bypassing PostCommunicationsConversationsByIdReply
// entirely, so the *production wiring* of those five values (the handler's
// own call into queueReply) was never under test: deleting that call left
// the whole package green. Every test below now drives the real handler,
// doInternalReply, past step 7 by overriding only srv.resolveReplyRecipientsFunc
// (server.go's own comment on that field has the full reasoning) — the same
// "override one seam, drive the real thing" shape task 4's verifyChannel
// established via channels_internal_test.go, moved one step earlier so the
// handler's own wiring is genuinely exercised, not assumed.
//
// Step 6 (idempotency replay) is reachable through the real HTTP handler —
// nothing about it depends on a resolved recipient — but exercising the
// "replay of an already-accepted request against a since-deactivated
// channel" ordering (steps 5 vs 6) needs a fixture idempotency_records row
// whose payload_fingerprint matches what the handler will independently
// recompute, which needs replyFingerprint itself. Those tests live here too.

// newInternalHarness is one communications installation for a white-box
// test: the same modtest composition harness_test.go's newHarness builds
// (package communications_test), duplicated here because that helper is
// unexported in a different package. srv is a *server built directly over
// the harness's own module.Deps (h.Deps()), so its handler methods can be
// called without going through HTTP, and its resolveReplyRecipientsFunc
// field can be overridden per test — the seam this file exists to use.
func newInternalHarness(t *testing.T) (*modtest.Harness, *server) {
	t.Helper()
	h := modtest.New(t, modtest.WithRecorder(internalRecorder), modtest.WithModule(Module()),
		modtest.WithEnv("STORAGE_PROVIDER", "fs"),
		modtest.WithEnv("STORAGE_FS_ROOT", t.TempDir()),
		modtest.WithEnv("STORAGE_FS_ALLOW_INSECURE_ROOT", "1"),
	)
	srv, err := newServer(h.Deps())
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	return h, srv
}

// withFixedRecipient overrides srv's recipient-resolution seam to answer
// address unconditionally — standing in for the inbound participant this
// port's own database can never produce (see resolveReplyRecipients' own
// comment). Every other step of PostCommunicationsConversationsByIdReply
// still runs for real: the conversation/channel lookup, the channel-active
// check, the fingerprint computation, the idempotency replay lookup, and
// queueReply's own steps 8-10, all against the real database through the
// real handler.
func withFixedRecipient(srv *server, address string) {
	srv.resolveReplyRecipientsFunc = func(context.Context, *store.Queries, uuid.UUID) (string, bool, error) {
		return address, true, nil
	}
}

// doInternalReply calls the real PostCommunicationsConversationsByIdReply
// directly (no HTTP round trip, but the exact same production method the
// mounted handler calls), with caller attached to ctx the same way
// module.Router's Access.Check attaches a real Principal for every mounted
// request.
func doInternalReply(ctx context.Context, srv *server, caller, conversationID uuid.UUID, key string, body gen.ReplyRequest) (gen.PostCommunicationsConversationsByIdReplyResponseObject, error) {
	ctx = contracts.WithPrincipal(ctx, contracts.Principal{UserID: caller})
	req := gen.PostCommunicationsConversationsByIdReplyRequestObject{
		Id:     conversationID,
		Params: gen.PostCommunicationsConversationsByIdReplyParams{IdempotencyKey: key},
		Body:   &body,
	}
	return srv.PostCommunicationsConversationsByIdReply(ctx, req)
}

// internalSetupChannel creates one active email channel through the real
// HTTP API and returns its id.
func internalSetupChannel(t *testing.T, h *modtest.Harness) uuid.UUID {
	t.Helper()
	admin := h.SignIn(t, "communications:channels-manage")
	r := admin.Do(http.MethodPost, "/api/v1/communications/channels", map[string]any{
		"type": "email", "address": "reply-internal-" + uuid.NewString() + "@example.test",
		"smtp": map[string]any{"host": "smtp.example.test", "port": 587},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("create channel: status %d body %s, want 201", r.Status, r.Body)
	}
	var ch struct {
		Id uuid.UUID `json:"id"`
	}
	r.JSON(&ch)
	return ch.Id
}

// internalCreateConversation creates a conversation through the real HTTP
// API (the only way to get a legitimately-owned channel_id/subject pair)
// and returns its id.
func internalCreateConversation(t *testing.T, c *modtest.Client) uuid.UUID {
	t.Helper()
	body := map[string]any{
		"subject": "Subject " + uuid.NewString(), "textBody": "Original body",
		"to": []map[string]any{{"email": "seed@example.test"}},
	}
	r := c.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", uuid.NewString()))
	if r.Status != http.StatusCreated {
		t.Fatalf("create conversation: status %d body %s, want 201", r.Status, r.Body)
	}
	var m struct {
		ConversationId uuid.UUID `json:"conversationId"`
	}
	r.JSON(&m)
	return m.ConversationId
}

// insertStagedAttachment inserts an attachment_uploads row directly
// (bypassing the staging endpoint, which this file's package cannot reach
// without duplicating its multipart-building helpers) plus a matching
// 'staged' attachment_cleanup_records row, so a successful claim's
// MarkCleanupRecordOwned transition is observable. Returns the upload id
// and its storage key.
func insertStagedAttachment(t *testing.T, h *modtest.Harness, conversationID, uploaderID uuid.UUID, scanStatus string, expiresAt time.Time) (uuid.UUID, string) {
	t.Helper()
	id := uuid.New()
	storageKey := "staged-attachments/test/" + id.String()
	h.Exec(t, `INSERT INTO communications.attachment_uploads
		(id, conversation_id, uploaded_by_user_id, file_name, content_type, size_bytes, content_hash, content_id, storage_key, scan_status, is_inline, idempotency_key, expires_at, created_at)
		VALUES ($1, $2, $3, 'report.pdf', 'application/pdf', 12, 'deadbeefcafe', NULL, $4, $5, false, $6, $7, $8)`,
		id, conversationID, uploaderID, storageKey, scanStatus, uuid.NewString(), expiresAt, h.Now())
	h.Exec(t, `INSERT INTO communications.attachment_cleanup_records
		(id, message_id, storage_key, status, attempts, next_attempt_at, created_at)
		VALUES ($1, NULL, $2, 'staged', 0, $3, $4)`, uuid.New(), storageKey, h.Now(), h.Now())
	return id, storageKey
}

func replyBodyFor(text string) gen.ReplyRequest {
	return gen.ReplyRequest{TextBody: ptr(text)}
}

// ---- Step 8: the staged-attachment preflight ----

// TestQueueReply_AttachmentsNotReady_UnknownId pins the preflight for an
// attachmentIds entry that matches no staged upload at all.
func TestQueueReply_AttachmentsNotReady_UnknownId(t *testing.T) {
	t.Parallel()
	h, srv := newInternalHarness(t)
	internalSetupChannel(t, h)
	owner := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := internalCreateConversation(t, owner)
	caller := uuid.New()
	withFixedRecipient(srv, "unknown-id@example.test")

	body := replyBodyFor("hi")
	body.AttachmentIds = &[]uuid.UUID{uuid.New()}
	resp, err := doInternalReply(context.Background(), srv, caller, convID, uuid.NewString(), body)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	assertReplyErrorStatus(t, resp, http.StatusConflict, "attachments_not_ready", "One or more attachments are still being scanned or are unavailable.")
}

// TestQueueReply_AttachmentsNotReady_Expired pins the preflight's
// expires_at > now half of the gate: an otherwise-clean upload past its
// expiry is treated exactly like a missing one.
func TestQueueReply_AttachmentsNotReady_Expired(t *testing.T) {
	t.Parallel()
	h, srv := newInternalHarness(t)
	internalSetupChannel(t, h)
	owner := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := internalCreateConversation(t, owner)
	caller := uuid.New()
	uploadID, _ := insertStagedAttachment(t, h, convID, caller, "clean", h.Now().Add(-time.Minute))
	withFixedRecipient(srv, "expired@example.test")

	body := replyBodyFor("hi")
	body.AttachmentIds = &[]uuid.UUID{uploadID}
	resp, err := doInternalReply(context.Background(), srv, caller, convID, uuid.NewString(), body)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	assertReplyErrorStatus(t, resp, http.StatusConflict, "attachments_not_ready", "One or more attachments are still being scanned or are unavailable.")
}

// TestQueueReply_AttachmentsNotReady_WrongUploader pins the preflight's own
// uploader scoping: a staged upload that exists, is clean and unexpired,
// but belongs to a different caller, is invisible to this reply. caller
// here flows into the real handler through doInternalReply's context
// principal, exercising the actual callerUserID(ctx) -> queueReply wiring,
// not a value the test hands queueReply directly.
func TestQueueReply_AttachmentsNotReady_WrongUploader(t *testing.T) {
	t.Parallel()
	h, srv := newInternalHarness(t)
	internalSetupChannel(t, h)
	owner := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := internalCreateConversation(t, owner)
	uploader := uuid.New()
	caller := uuid.New() // deliberately not uploader
	uploadID, _ := insertStagedAttachment(t, h, convID, uploader, "clean", h.Now().Add(time.Hour))
	withFixedRecipient(srv, "wrong-uploader@example.test")

	body := replyBodyFor("hi")
	body.AttachmentIds = &[]uuid.UUID{uploadID}
	resp, err := doInternalReply(context.Background(), srv, caller, convID, uuid.NewString(), body)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	assertReplyErrorStatus(t, resp, http.StatusConflict, "attachments_not_ready", "One or more attachments are still being scanned or are unavailable.")
}

// TestQueueReply_AttachmentsPrecedeSuppression is the mutation-order test
// for steps 8 vs 9: an unready attachment AND a suppressed recipient in the
// same request must answer 409 attachments_not_ready, never 422
// recipient_suppressed — proving the preflight runs first. Swapping these
// two steps would flip this test's expectation.
func TestQueueReply_AttachmentsPrecedeSuppression(t *testing.T) {
	t.Parallel()
	h, srv := newInternalHarness(t)
	internalSetupChannel(t, h)
	owner := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := internalCreateConversation(t, owner)
	caller := uuid.New()
	address := "both-broken-" + uuid.NewString() + "@example.test"
	h.Exec(t, `INSERT INTO communications.suppressions (id, normalized_email_address, reason, created_at) VALUES ($1, $2, NULL, $3)`,
		uuid.New(), normalizeEmail(address), h.Now())
	withFixedRecipient(srv, address)

	body := replyBodyFor("hi")
	body.AttachmentIds = &[]uuid.UUID{uuid.New()} // unknown -> preflight fails
	resp, err := doInternalReply(context.Background(), srv, caller, convID, uuid.NewString(), body)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	assertReplyErrorStatus(t, resp, http.StatusConflict, "attachments_not_ready", "One or more attachments are still being scanned or are unavailable.")
}

// ---- Step 9: suppression ----

// TestQueueReply_RecipientSuppressed pins step 9's shape exactly: 422
// recipient_suppressed, fields.recipients carrying the suppressed
// address(es) in their stored, uppercased form (D7) — the fixed recipient
// address is deliberately lowercase, proving the echoed value comes from
// normalisation, not from echoing the input verbatim.
func TestQueueReply_RecipientSuppressed(t *testing.T) {
	t.Parallel()
	h, srv := newInternalHarness(t)
	internalSetupChannel(t, h)
	owner := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := internalCreateConversation(t, owner)
	caller := uuid.New()
	address := "suppressed-" + uuid.NewString() + "@example.test"
	h.Exec(t, `INSERT INTO communications.suppressions (id, normalized_email_address, reason, created_at) VALUES ($1, $2, NULL, $3)`,
		uuid.New(), normalizeEmail(address), h.Now())
	withFixedRecipient(srv, address)

	resp, err := doInternalReply(context.Background(), srv, caller, convID, uuid.NewString(), replyBodyFor("hi"))
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	r422, ok := resp.(gen.PostCommunicationsConversationsByIdReply422JSONResponse)
	if !ok {
		t.Fatalf("response = %T, want 422JSONResponse", resp)
	}
	if r422.Error.Code != "recipient_suppressed" {
		t.Errorf("code = %q, want recipient_suppressed", r422.Error.Code)
	}
	if r422.Error.Message != "One or more recipients are suppressed." {
		t.Errorf("message = %q, want the exact recipient_suppressed text", r422.Error.Message)
	}
	if r422.Error.Fields == nil {
		t.Fatal("fields is nil, want fields.recipients")
	}
	got := (*r422.Error.Fields)["recipients"]
	if len(got) != 1 || got[0] != normalizeEmail(address) {
		t.Errorf("fields[recipients] = %v, want [%q] (uppercased)", got, normalizeEmail(address))
	}
}

// TestQueueReply_SuppressionPrecedesAttachmentClaim is the mutation-order
// test for steps 9 vs 10: a suppressed recipient with an otherwise-claimable
// attachment must never reach the transaction at all — the staged upload's
// scan_status must still read 'clean' afterward, proving step 10's claim
// never ran. Swapping steps 9 and 10 would let the claim fire before the
// suppression check and this assertion would fail.
func TestQueueReply_SuppressionPrecedesAttachmentClaim(t *testing.T) {
	t.Parallel()
	h, srv := newInternalHarness(t)
	internalSetupChannel(t, h)
	owner := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := internalCreateConversation(t, owner)
	caller := uuid.New()
	address := "suppressed-with-attachment-" + uuid.NewString() + "@example.test"
	h.Exec(t, `INSERT INTO communications.suppressions (id, normalized_email_address, reason, created_at) VALUES ($1, $2, NULL, $3)`,
		uuid.New(), normalizeEmail(address), h.Now())
	uploadID, _ := insertStagedAttachment(t, h, convID, caller, "clean", h.Now().Add(time.Hour))
	withFixedRecipient(srv, address)

	body := replyBodyFor("hi")
	body.AttachmentIds = &[]uuid.UUID{uploadID}
	resp, err := doInternalReply(context.Background(), srv, caller, convID, uuid.NewString(), body)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	assertReplyErrorStatus(t, resp, http.StatusUnprocessableEntity, "recipient_suppressed", "One or more recipients are suppressed.")

	status := modtest.One[string](t, h, `SELECT scan_status FROM communications.attachment_uploads WHERE id = $1`, uploadID)
	if status != "clean" {
		t.Errorf("attachment scan_status = %q after a suppressed reply, want unchanged clean (the transaction must never have run)", status)
	}
}

// ---- Step 10: the transactional claim, success, and its promotion ----

// TestQueueReply_Success_PromotesAttachmentVerbatim is the happy path with
// one staged attachment: 201, a message_attachments row carrying the
// staged upload's scan_status and content_hash verbatim (not recomputed —
// the coordinator's addendum on task 7 named content_hash specifically),
// the attachment_uploads row deleted, its cleanup record transitioned to
// 'owned', an outbox job and idempotency record written, and the
// conversation's activity fields advanced (LastActivityAt, PreviewText, and
// Subject filled once since this conversation already has one) —
// everything the real handler computes and wires into queueReply, not
// values this test supplies.
func TestQueueReply_Success_PromotesAttachmentVerbatim(t *testing.T) {
	t.Parallel()
	h, srv := newInternalHarness(t)
	internalSetupChannel(t, h)
	owner := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := internalCreateConversation(t, owner)
	caller := uuid.New()
	uploadID, storageKey := insertStagedAttachment(t, h, convID, caller, "clean", h.Now().Add(time.Hour))
	wantHash := modtest.One[string](t, h, `SELECT content_hash FROM communications.attachment_uploads WHERE id = $1`, uploadID)
	withFixedRecipient(srv, "recipient@example.test")

	body := replyBodyFor("A reply with an attachment")
	body.AttachmentIds = &[]uuid.UUID{uploadID}
	key := uuid.NewString()
	resp, err := doInternalReply(context.Background(), srv, caller, convID, key, body)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	r201, ok := resp.(gen.PostCommunicationsConversationsByIdReply201JSONResponse)
	if !ok {
		t.Fatalf("response = %T, want 201JSONResponse", resp)
	}
	if r201.Status != "queued" {
		t.Errorf("status = %q, want queued", r201.Status)
	}
	if r201.MessageId == nil {
		t.Fatal("messageId is nil")
	}
	messageID := *r201.MessageId

	gotHash := modtest.One[string](t, h, `SELECT content_hash FROM communications.message_attachments WHERE message_id = $1`, messageID)
	if gotHash != wantHash {
		t.Errorf("message_attachments.content_hash = %q, want %q (carried over, not recomputed)", gotHash, wantHash)
	}
	gotScanStatus := modtest.One[string](t, h, `SELECT scan_status FROM communications.message_attachments WHERE message_id = $1`, messageID)
	if gotScanStatus != "clean" {
		t.Errorf("message_attachments.scan_status = %q, want clean", gotScanStatus)
	}
	gotStorageKey := modtest.One[string](t, h, `SELECT storage_key FROM communications.message_attachments WHERE message_id = $1`, messageID)
	if gotStorageKey != storageKey {
		t.Errorf("message_attachments.storage_key = %q, want %q (never re-keyed)", gotStorageKey, storageKey)
	}

	if n := h.Count(t, `SELECT count(*) FROM communications.attachment_uploads WHERE id = $1`, uploadID); n != 0 {
		t.Errorf("attachment_uploads rows for %s = %d, want 0 (deleted after promotion)", uploadID, n)
	}
	cleanupStatus := modtest.One[string](t, h, `SELECT status FROM communications.attachment_cleanup_records WHERE storage_key = $1`, storageKey)
	if cleanupStatus != "owned" {
		t.Errorf("cleanup record status = %q, want owned", cleanupStatus)
	}

	if n := h.Count(t, `SELECT count(*) FROM communications.outbox_jobs WHERE message_id = $1`, messageID); n != 1 {
		t.Errorf("outbox_jobs rows = %d, want 1", n)
	}
	idempKey := modtest.One[string](t, h, `SELECT key FROM communications.idempotency_records WHERE message_id = $1`, messageID)
	if idempKey != key {
		t.Errorf("idempotency_records.key = %q, want %q", idempKey, key)
	}

	subject := modtest.One[*string](t, h, `SELECT subject FROM communications.conversations WHERE id = $1`, convID)
	if subject == nil {
		t.Fatal("conversation subject is nil, want the one CreateConversation set (fill-once must not clear it)")
	}
}

// assertReplyErrorStatus checks resp is a PostCommunicationsConversationsByIdReplyResponseObject
// carrying the module's flat CommunicationErrorResponse shape with the
// given status/code/message — a small shared assertion across this file's
// preflight/claim tests, all of which answer the same 409 shape.
func assertReplyErrorStatus(t *testing.T, resp gen.PostCommunicationsConversationsByIdReplyResponseObject, wantStatus int, wantCode, wantMessage string) {
	t.Helper()
	switch wantStatus {
	case http.StatusConflict:
		r, ok := resp.(gen.PostCommunicationsConversationsByIdReply409JSONResponse)
		if !ok {
			t.Fatalf("response = %T, want 409JSONResponse", resp)
		}
		if r.Error.Code != wantCode {
			t.Errorf("code = %q, want %q", r.Error.Code, wantCode)
		}
		if r.Error.Message != wantMessage {
			t.Errorf("message = %q, want %q", r.Error.Message, wantMessage)
		}
	case http.StatusUnprocessableEntity:
		r, ok := resp.(gen.PostCommunicationsConversationsByIdReply422JSONResponse)
		if !ok {
			t.Fatalf("response = %T, want 422JSONResponse", resp)
		}
		if r.Error.Code != wantCode {
			t.Errorf("code = %q, want %q", r.Error.Code, wantCode)
		}
		if r.Error.Message != wantMessage {
			t.Errorf("message = %q, want %q", r.Error.Message, wantMessage)
		}
	default:
		t.Fatalf("unsupported wantStatus %d", wantStatus)
	}
}

// ---- Step 10, race: the TOCTOU window a preflight-only implementation
// would leave open ----

// awaitReplyLockWaiters waits until n backends on h's database are waiting
// on a lock, failing t if finished closes first or ten seconds pass —
// duplicated from channels_concurrency_test.go's awaitLockWaiters (package
// communications_test, unexported, so not importable from here).
func awaitReplyLockWaiters(t *testing.T, h *modtest.Harness, n int, finished <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for h.Count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) < n {
		select {
		case <-finished:
			t.Fatalf("the requests answered without waiting on the gate's lock")
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("fewer than %d requests ever waited on the gate's lock", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestQueueReply_ConcurrentAttachmentClaimRace is the teeth check for why
// the transactional claim (step 10) exists at all, not just the preflight
// (step 8): two replies — through the real handler, each with its own
// idempotency key — race to claim the same staged attachment. Both
// preflights (plain SELECTs) can pass — nothing locks between them — but
// only one transaction's conditional clean -> claimed UPDATE can actually
// flip the row; the loser's claimed count comes back short and it answers
// the same 409 attachments_not_ready the preflight itself would, rather
// than double-sending the attachment or corrupting either message. The gate
// (LOCK TABLE ... IN EXCLUSIVE MODE, released only once both requests are
// confirmed waiting) is duplicated from channels_concurrency_test.go's
// pattern, per task 7's dispatch.
func TestQueueReply_ConcurrentAttachmentClaimRace(t *testing.T) {
	h, srv := newInternalHarness(t)
	internalSetupChannel(t, h)
	owner := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := internalCreateConversation(t, owner)
	caller := uuid.New()
	uploadID, _ := insertStagedAttachment(t, h, convID, caller, "clean", h.Now().Add(time.Hour))
	withFixedRecipient(srv, "race@example.test")

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.attachment_uploads IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock attachment_uploads: %v", err)
	}

	run := func(key string) (gen.PostCommunicationsConversationsByIdReplyResponseObject, error) {
		body := replyBodyFor("racing reply")
		body.AttachmentIds = &[]uuid.UUID{uploadID}
		return doInternalReply(ctx, srv, caller, convID, key, body)
	}

	var responses [2]gen.PostCommunicationsConversationsByIdReplyResponseObject
	var errs [2]error
	var wg sync.WaitGroup
	wg.Add(2)
	finished := make(chan struct{})
	go func() { defer wg.Done(); responses[0], errs[0] = run(uuid.NewString()) }()
	go func() { defer wg.Done(); responses[1], errs[1] = run(uuid.NewString()) }()
	go func() { wg.Wait(); close(finished) }()

	awaitReplyLockWaiters(t, h, 2, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	<-finished

	var created, conflicted int
	for i, resp := range responses {
		if errs[i] != nil {
			t.Fatalf("reply[%d]: %v", i, errs[i])
		}
		switch r := resp.(type) {
		case gen.PostCommunicationsConversationsByIdReply201JSONResponse:
			created++
		case gen.PostCommunicationsConversationsByIdReply409JSONResponse:
			conflicted++
			if r.Error.Code != "attachments_not_ready" {
				t.Errorf("loser code = %q, want attachments_not_ready", r.Error.Code)
			}
		default:
			t.Errorf("response[%d] = %T, want 201 or 409", i, resp)
		}
	}
	if created != 1 {
		t.Errorf("created = %d, want exactly 1", created)
	}
	if conflicted != 1 {
		t.Errorf("conflicted = %d, want exactly 1", conflicted)
	}
}

// ---- Step 6: idempotency replay, and step 5 preceding it ----

// insertIdempotencyFixture writes an idempotency_records row whose
// payload_fingerprint is replyFingerprint's own real output for
// (conversationID, body) — computed the same way the handler itself will,
// so a replay against it exercises genuine fingerprint equality rather than
// a hand-picked stand-in string.
func insertIdempotencyFixture(t *testing.T, h *modtest.Harness, conversationID, messageID uuid.UUID, key string, body gen.ReplyRequest) {
	t.Helper()
	fingerprint, err := replyFingerprint(conversationID, body)
	if err != nil {
		t.Fatalf("replyFingerprint: %v", err)
	}
	h.Exec(t, `INSERT INTO communications.idempotency_records (id, key, payload_fingerprint, conversation_id, message_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, uuid.New(), key, fingerprint, conversationID, messageID, h.Now())
}

// TestReply_IdempotencyReplaySameFingerprintIs200 pins step 6's 200 half:
// a replay carrying the identical payload as an existing idempotency record
// returns that record's conversationId/messageId with 200, without ever
// reaching step 7 (this conversation genuinely has no inbound participant,
// so if the replay check did not short-circuit first, this would 422
// instead).
func TestReply_IdempotencyReplaySameFingerprintIs200(t *testing.T) {
	t.Parallel()
	h, _ := newInternalHarness(t)
	internalSetupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := internalCreateConversation(t, c)

	key := uuid.NewString()
	body := replyBodyFor("replayed body")
	fakeMessageID := uuid.New()
	insertIdempotencyFixture(t, h, convID, fakeMessageID, key, body)

	r := c.Do(http.MethodPost, "/api/v1/communications/conversations/"+convID.String()+"/reply",
		map[string]any{"textBody": "replayed body"}, modtest.Header("Idempotency-Key", key))
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200 (replay)", r.Status, r.Body)
	}
	var m struct {
		ConversationId uuid.UUID `json:"conversationId"`
		MessageId      uuid.UUID `json:"messageId"`
	}
	r.JSON(&m)
	if m.ConversationId != convID || m.MessageId != fakeMessageID {
		t.Errorf("replay = %+v, want conversationId=%s messageId=%s", m, convID, fakeMessageID)
	}
}

// TestReply_IdempotencyReplayDifferentPayloadIs409 pins step 6's 409 half.
func TestReply_IdempotencyReplayDifferentPayloadIs409(t *testing.T) {
	t.Parallel()
	h, _ := newInternalHarness(t)
	internalSetupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := internalCreateConversation(t, c)

	key := uuid.NewString()
	insertIdempotencyFixture(t, h, convID, uuid.New(), key, replyBodyFor("original body"))

	r := c.Do(http.MethodPost, "/api/v1/communications/conversations/"+convID.String()+"/reply",
		map[string]any{"textBody": "a different body"}, modtest.Header("Idempotency-Key", key))
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	r.JSON(&body)
	if body.Error.Code != "idempotency_key_reused" {
		t.Errorf("code = %q, want idempotency_key_reused", body.Error.Code)
	}
}

// TestReply_ChannelInactivePrecedesIdempotencyReplay is the mutation-order
// test for steps 5 vs 6 — task 7's own named hazard: a replay of an
// already-accepted request against a since-deactivated channel answers 422
// channel_inactive, not the cached 200. Swapping steps 5 and 6 would make
// this test observe 200 instead.
func TestReply_ChannelInactivePrecedesIdempotencyReplay(t *testing.T) {
	t.Parallel()
	h, _ := newInternalHarness(t)
	chID := internalSetupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID := internalCreateConversation(t, c)

	key := uuid.NewString()
	body := replyBodyFor("accepted, then the channel dies")
	insertIdempotencyFixture(t, h, convID, uuid.New(), key, body)

	admin := h.SignIn(t, "communications:channels-manage")
	if r := admin.Do(http.MethodPut, "/api/v1/communications/channels/"+chID.String(), map[string]any{"isActive": false}); r.Status != http.StatusOK {
		t.Fatalf("deactivate channel: status %d body %s, want 200", r.Status, r.Body)
	}

	r := c.Do(http.MethodPost, "/api/v1/communications/conversations/"+convID.String()+"/reply",
		map[string]any{"textBody": "accepted, then the channel dies"}, modtest.Header("Idempotency-Key", key))
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422 (channel_inactive precedes the cached replay)", r.Status, r.Body)
	}
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	r.JSON(&errBody)
	if errBody.Error.Code != "channel_inactive" {
		t.Errorf("code = %q, want channel_inactive, not the replay's cached 200", errBody.Error.Code)
	}
}
