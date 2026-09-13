package communications_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is Reply's black-box tests (Reply -> QueueOutboundAsync,
// EP/ConversationEndpoints.cs:96-101, :264-338; communications inventory
// §2's "Reply -> QueueOutboundAsync" bullet, §19.1 item 3; design doc
// §1.1). It covers the steps reachable through the real HTTP handler:
// validation (2), Idempotency-Key validity (3), the 404 (4), channel
// inactive (5) and recipients_missing (7), plus the permission pairing.
// Steps 6, 8, 9 and 10 need a resolved recipient this port's own API can
// never produce (step 7 always fires first — see conversations_reply.go's
// own top-of-file comment) and are covered instead in
// conversations_reply_internal_test.go, which calls queueReply directly.

// replyBody is a valid ReplyRequest: a text body, nothing else —
// ValidateReply's minimum (subject and replyMode are both optional; the
// design doc §4.1 "omitting replyMode is valid" case this exercises by
// omission).
func replyBody() map[string]any {
	return map[string]any{"textBody": "Thanks for reaching out."}
}

func doReply(c *modtest.Client, conversationID, idempotencyKey string, body map[string]any) *modtest.Response {
	return c.Do(http.MethodPost, "/api/v1/communications/conversations/"+conversationID+"/reply", body,
		modtest.Header("Idempotency-Key", idempotencyKey))
}

// ---- Step 2: validateReply, and "body validation wins over existence" ----

// TestReply_ValidationErrors pins ValidateReply's field table for a blank
// body (inventory §3.1: "TextBody or HtmlBody is required.").
func TestReply_ValidationErrors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))

	r := doReply(c, m.ConversationId, uuid.NewString(), map[string]any{})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	if body.Error.Code != "invalid_request" || body.Error.Message != "The request is invalid." {
		t.Errorf("error = %+v, want invalid_request / %q", body.Error, "The request is invalid.")
	}
	if got := body.field("body"); len(got) != 1 || got[0] != "TextBody or HtmlBody is required." {
		t.Errorf("fields[body] = %v, want the required-body message", got)
	}
}

// TestReply_ValidationWinsOverExistence pins the mutation-order test that
// swaps steps 2 and 4: an invalid body against an unknown conversation id
// answers 400, not 404 — the same ordering CreateConversation and
// StageAttachment already use (inventory §2's closing note, and task 7
// dispatch's "body validation wins over existence").
func TestReply_ValidationWinsOverExistence(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")

	r := doReply(c, uuid.NewString(), uuid.NewString(), map[string]any{})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400 (validation precedes existence)", r.Status, r.Body)
	}
}

// TestReply_InvalidReplyModeIs400 pins the replyMode field error, and that
// an explicit unrecognised value (not merely an omitted one) is rejected.
func TestReply_InvalidReplyModeIs400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))

	body := replyBody()
	body["replyMode"] = "not-a-mode"
	r := doReply(c, m.ConversationId, uuid.NewString(), body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if got := errBody.field("replyMode"); len(got) != 1 || got[0] != "ReplyMode must be reply or reply_all." {
		t.Errorf("fields[replyMode] = %v, want the required message", got)
	}
}

// TestReply_ReplyModeOmittedDefaultsToReply pins inventory §4.1's first
// half explicitly: a request that omits replyMode entirely passes
// validation and proceeds past step 2 exactly like an explicit "reply"
// would — both reach step 7's recipients_missing, never invalid_request.
func TestReply_ReplyModeOmittedDefaultsToReply(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))

	r := doReply(c, m.ConversationId, uuid.NewString(), replyBody()) // omits replyMode entirely
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422 (validation passed; an omitted replyMode must not itself 400)", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if errBody.Error.Code != "recipients_missing" {
		t.Errorf("code = %q, want recipients_missing", errBody.Error.Code)
	}
}

// TestReply_ReplyModeExplicitNullIs400 pins inventory §4.1's other half —
// the asymmetry a previous fix round parked as identical-behaviour and then
// un-parked once Task 9's pre-dispatch check traced it: .NET's
// `request?.ReplyMode?.Trim().ToLowerInvariant() is not ("reply" or
// "reply_all")` treats a present-but-null ReplyMode exactly like an
// unrecognised string (null matches neither pattern), defeating the DTO's
// own "reply" default — 400, unlike an omitted key. module.go's
// withRawJSONBody (extended to this route in this fix round, the same
// machinery task 5's PATCH handler already uses for customerId/assignedUserId)
// is what makes the distinction possible in Go at all, since encoding/json
// otherwise collapses "omitted" and "present as null" into the same nil
// pointer.
func TestReply_ReplyModeExplicitNullIs400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))

	body := replyBody()
	body["replyMode"] = nil
	r := doReply(c, m.ConversationId, uuid.NewString(), body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400 (an explicit null defeats the default, unlike an omitted key)", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if got := errBody.field("replyMode"); len(got) != 1 || got[0] != "ReplyMode must be reply or reply_all." {
		t.Errorf("fields[replyMode] = %v, want the required message", got)
	}
}

// TestReply_ReplyAllIsAcceptedButInert pins fix round 1's item 3: replyMode
// "reply_all" is a recognised mode and passes validation, but this port
// never derives a non-empty replyAllCc (design doc §1.1: doing so needs
// inbound thread metadata this port never writes), so it behaves
// identically to plain "reply" today — both reach the same
// recipients_missing 422, never a distinct code path or message.
func TestReply_ReplyAllIsAcceptedButInert(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))

	body := replyBody()
	body["replyMode"] = "reply_all"
	r := doReply(c, m.ConversationId, uuid.NewString(), body)
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422 (reply_all is accepted, not rejected)", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if errBody.Error.Code != "recipients_missing" {
		t.Errorf("code = %q, want recipients_missing (reply_all is inert here — no inbound metadata exists to derive cc from)", errBody.Error.Code)
	}
}

// TestReply_TooManyAttachmentIdsIs400 pins the attachmentIds count cap.
func TestReply_TooManyAttachmentIdsIs400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))

	ids := make([]string, 21)
	for i := range ids {
		ids[i] = uuid.NewString()
	}
	body := replyBody()
	body["attachmentIds"] = ids
	r := doReply(c, m.ConversationId, uuid.NewString(), body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if got := errBody.field("attachmentIds"); len(got) != 1 || got[0] != "At most 20 attachments are allowed." {
		t.Errorf("fields[attachmentIds] = %v, want the count-limit message", got)
	}
}

// TestReply_DuplicateAttachmentIdOverwritesCountMessage pins inventory
// §19.2 item 6 for this field: a list that is both too long and carries a
// duplicate reports only the duplicate message.
func TestReply_DuplicateAttachmentIdOverwritesCountMessage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))

	dup := uuid.NewString()
	ids := make([]string, 25)
	for i := range ids {
		ids[i] = dup
	}
	body := replyBody()
	body["attachmentIds"] = ids
	r := doReply(c, m.ConversationId, uuid.NewString(), body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if got := errBody.field("attachmentIds"); len(got) != 1 || got[0] != "An attachment may appear only once." {
		t.Errorf("fields[attachmentIds] = %v, want only the duplicate message", got)
	}
}

// TestReply_ValidationPrecedesIdempotencyKeyCheck is the mutation-order test
// for steps 2 vs 3: a request that is invalid on both counts (an empty body
// and an over-long Idempotency-Key) answers invalid_request, not
// idempotency_key_required — proving validateReply runs first. Swapping
// steps 2 and 3 would flip which code comes back.
func TestReply_ValidationPrecedesIdempotencyKeyCheck(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))

	r := doReply(c, m.ConversationId, strings.Repeat("k", 201), map[string]any{})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	if body.Error.Code != "invalid_request" {
		t.Errorf("code = %q, want invalid_request (body validation precedes the idempotency-key check), not idempotency_key_required", body.Error.Code)
	}
}

// ---- Step 3: Idempotency-Key validity, ahead of the 404 too ----

// TestReply_IdempotencyKeyTooLongIs400 pins the flat idempotency_key_required
// shape, mirroring TestCreateConversation_IdempotencyKeyTooLongIs400's own
// reasoning for why a too-long key (not an absent header) is the right way
// to exercise this module's own check rather than the platform decoder's.
func TestReply_IdempotencyKeyTooLongIs400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))

	r := doReply(c, m.ConversationId, strings.Repeat("k", 201), replyBody())
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

// TestReply_IdempotencyKeyInvalidWinsOverExistence pins the mutation-order
// test that swaps steps 3 and 4: an invalid key against an unknown
// conversation id answers 400, not 404.
func TestReply_IdempotencyKeyInvalidWinsOverExistence(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")

	r := doReply(c, uuid.NewString(), strings.Repeat("k", 201), replyBody())
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400 (idempotency-key validity precedes existence)", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	if body.Error.Code != "idempotency_key_required" {
		t.Errorf("code = %q, want idempotency_key_required, not a 404", body.Error.Code)
	}
}

// ---- Step 4: conversation lookup ----

func TestReply_NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")

	r := doReply(c, uuid.NewString(), uuid.NewString(), replyBody())
	if r.Status != http.StatusNotFound {
		t.Fatalf("status %d body %s, want 404", r.Status, r.Body)
	}
	if len(r.Body) != 0 {
		t.Errorf("body = %s, want empty (bare 404)", r.Body)
	}
}

// ---- Step 5: channel inactive ----

// TestReply_ChannelInactiveIs422 pins step 5 directly: a conversation whose
// channel has since been deactivated answers 422 channel_inactive rather
// than proceeding to the idempotency/recipient checks.
func TestReply_ChannelInactiveIs422(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	chID := setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))

	admin := h.SignIn(t, "communications:channels-manage")
	if r := admin.Do(http.MethodPut, "/api/v1/communications/channels/"+chID, map[string]any{"isActive": false}); r.Status != http.StatusOK {
		t.Fatalf("deactivate channel: status %d body %s, want 200", r.Status, r.Body)
	}

	r := doReply(c, m.ConversationId, uuid.NewString(), replyBody())
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	if body.Error.Code != "channel_inactive" {
		t.Errorf("code = %q, want channel_inactive", body.Error.Code)
	}
	if body.Error.Message != "The conversation channel is inactive." {
		t.Errorf("message = %q, want the exact channel_inactive text", body.Error.Message)
	}
}

// ---- Step 7: the outbound-only consequence ----

// TestReply_NoInboundParticipantIsRecipientsMissing pins design doc §1.1
// and task 7 dispatch's "outbound-only consequence": a conversation created
// through this API has no inbound message, so recipients_missing fires
// unconditionally, for every conversation, forever. Pinned rather than
// worked around.
func TestReply_NoInboundParticipantIsRecipientsMissing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))

	r := doReply(c, m.ConversationId, uuid.NewString(), replyBody())
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	if body.Error.Code != "recipients_missing" {
		t.Errorf("code = %q, want recipients_missing", body.Error.Code)
	}
	if body.Error.Message != "The conversation has no inbound participant to reply to." {
		t.Errorf("message = %q, want the exact recipients_missing text", body.Error.Message)
	}
}

// ---- Step 6, reachable through the real handler: a bare replay ----

// replyFingerprintForTest is communications.ReplyFingerprintForTest
// (export_test.go's re-export of the real, unexported replyFingerprint),
// used to build an idempotency_records fixture whose payload_fingerprint
// the handler will actually match. Fix round 1's review confirmed the
// original hand-duplicated struct shape here failed loudly rather than
// silently if it ever drifted from the real function, but a construction
// that cannot drift at all — the same func value — is strictly better. This
// is the one test in this file that reaches a genuine 200 through the real
// handler — the contract-coverage recorder (main_test.go) counts an
// operation exercised only on a below-400 response, and the recorder here
// is the shared package-level one, unlike conversations_reply_internal_test.go's
// own (whose white-box replay tests use replyFingerprint directly, but
// against a *different* recorder instance that this package's coverage
// gate never reads).
func replyFingerprintForTest(t *testing.T, conversationID uuid.UUID, textBody *string) string {
	t.Helper()
	fp, err := communications.ReplyFingerprintForTest(conversationID, gen.ReplyRequest{TextBody: textBody})
	if err != nil {
		t.Fatalf("ReplyFingerprintForTest: %v", err)
	}
	return fp
}

// TestReply_IdempotencyReplaySameFingerprintIs200 pins step 6's 200 half
// end to end through the real handler: a replay carrying the identical
// payload as an existing idempotency record returns that record's
// conversationId/messageId with 200, without ever reaching step 7 (this
// conversation genuinely has no inbound participant, so if the replay check
// did not short-circuit first, this would 422 instead — the same ordering
// conversations_reply_internal_test.go's white-box tests pin more directly
// for steps 5-vs-6).
func TestReply_IdempotencyReplaySameFingerprintIs200(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))
	convID := uuid.MustParse(m.ConversationId)

	key := uuid.NewString()
	text := "replayed body"
	fingerprint := replyFingerprintForTest(t, convID, &text)
	fakeMessageID := uuid.New()
	h.Exec(t, `INSERT INTO communications.idempotency_records (id, key, payload_fingerprint, conversation_id, message_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, uuid.New(), key, fingerprint, convID, fakeMessageID, h.Now())

	r := doReply(c, m.ConversationId, key, map[string]any{"textBody": text})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200 (replay)", r.Status, r.Body)
	}
	var replayed conversationMutationJSON
	r.JSON(&replayed)
	if replayed.ConversationId != m.ConversationId || replayed.MessageId == nil || *replayed.MessageId != fakeMessageID.String() {
		t.Errorf("replay = %+v, want conversationId=%s messageId=%s", replayed, m.ConversationId, fakeMessageID)
	}
}

// TestReply_RecipientsMissingPrecedesAttachmentPreflight is the
// mutation-order test for steps 7 vs 8: a request naming an attachmentId
// that does not exist (which would fail the step-8 preflight) against a
// conversation with no inbound participant still answers 422
// recipients_missing, not 409 attachments_not_ready — proving step 7 runs,
// and refuses, before queueReply (and its own step 8) is ever reached at
// all. This is enforced structurally (PostCommunicationsConversationsByIdReply
// only calls queueReply once resolveReplyRecipients succeeds), and this
// test is the wire-level proof of that structure.
func TestReply_RecipientsMissingPrecedesAttachmentPreflight(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))

	body := replyBody()
	body["attachmentIds"] = []string{uuid.NewString()} // matches nothing staged
	r := doReply(c, m.ConversationId, uuid.NewString(), body)
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if errBody.Error.Code != "recipients_missing" {
		t.Errorf("code = %q, want recipients_missing (step 7 precedes the attachment preflight), not attachments_not_ready", errBody.Error.Code)
	}
}

// ---- Permissions: the asymmetric pairing ----

// TestReply_RequiresReplyAndView pins the permission pairing task 7's
// dispatch names as the mirror image of CreateConversation's own asymmetry:
// Reply needs conversations-reply AND conversations-view together, unlike
// POST /conversations, which needs conversations-reply alone
// (TestCreateConversation_ReplyPermissionAloneSuffices in
// conversations_test.go pins that half).
func TestReply_RequiresReplyAndView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	owner := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, owner, newConversationBody("x@example.test"))

	if r := doReply(h.SignIn(t, "communications:conversations-reply"), m.ConversationId, uuid.NewString(), replyBody()); r.Status != http.StatusForbidden {
		t.Errorf("reply alone: status %d, want 403", r.Status)
	}
	if r := doReply(h.SignIn(t, "communications:conversations-view"), m.ConversationId, uuid.NewString(), replyBody()); r.Status != http.StatusForbidden {
		t.Errorf("view alone: status %d, want 403", r.Status)
	}
	both := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	if r := doReply(both, m.ConversationId, uuid.NewString(), replyBody()); r.Status == http.StatusForbidden {
		t.Errorf("reply+view: status %d, want anything but 403 (both permissions must suffice)", r.Status)
	}
}
