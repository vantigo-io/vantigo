package communications_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is Reply's black-box tests (Reply -> QueueOutboundAsync,
// EP/ConversationEndpoints.cs:96-101, :264-338; communications inventory
// §2's "Reply -> QueueOutboundAsync" bullet, §19.1 item 3; design doc
// §1.1). Every one of the ten steps, including 6, 8, 9 and 10, is reachable
// and pinned through the real HTTP handler here — no white-box test file
// exists for this operation any more.
//
// Steps 8, 9 and 10 need a resolved recipient, which needs a genuine
// direction='inbound' conversation_messages row. No production path in
// this port ever writes one (design doc §1.1: the inbound worker was the
// only writer and it is out of scope), but replyableConversation below
// inserts one directly as a fixture — legal since task 7 fix round 2
// widened conversation_messages.direction's CHECK to match .NET's full
// Direction domain (inbound included). Before that fix, no fixture could
// construct this row at all, and fix round 1 worked around it with an
// overridable recipient-resolution seam on *server; that seam is gone
// (server.go's own comment on the removed field has the history) because
// this file's own fixtures now drive the real query, and the real handler,
// end to end.

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

// insertInboundParticipant inserts a participant on chID with address, plus
// a direction='inbound' conversation_messages row on convID naming that
// participant as its author — the fixture a real inbound provider would
// eventually write, legal only since task 7 fix round 2 (see this file's
// own top-of-file comment). address is stored verbatim: callers pass it
// already normalised (uppercased, D7) to match what a real participant row
// actually contains, so a test reading it back needs no further
// transformation.
func insertInboundParticipant(t *testing.T, h *modtest.Harness, chID, convID, address string) uuid.UUID {
	t.Helper()
	participantID := uuid.New()
	h.Exec(t, `INSERT INTO communications.participants (id, channel_id, address, created_at) VALUES ($1, $2, $3, $4)`,
		participantID, uuid.MustParse(chID), address, h.Now())
	h.Exec(t, `INSERT INTO communications.conversation_messages (id, conversation_id, direction, participant_id, occurred_at, created_at)
		VALUES ($1, $2, 'inbound', $3, $4, $4)`, uuid.New(), uuid.MustParse(convID), participantID, h.Now())
	return participantID
}

// replyableConversation creates a channel and a conversation through the
// real API, then attaches a genuine inbound participant so a subsequent
// reply resolves it as the recipient and reaches steps 8, 9 and 10 for
// real. Returns the conversation id and the resolved (already-normalised,
// uppercased) recipient address.
func replyableConversation(t *testing.T, h *modtest.Harness, c *modtest.Client) (conversationID, address string) {
	t.Helper()
	chID := setupChannel(t, h)
	m := createConversation(t, c, newConversationBody("seed@example.test"))
	address = strings.ToUpper("inbound-" + uuid.NewString() + "@example.test")
	insertInboundParticipant(t, h, chID, m.ConversationId, address)
	return m.ConversationId, address
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

// ---- Step 6: idempotency replay, and step 5 preceding it ----

// replyFingerprintForTest is communications.ReplyFingerprintForTest
// (export_test.go's re-export of the real, unexported replyFingerprint),
// used to build an idempotency_records fixture whose payload_fingerprint
// the handler will actually match. Fix round 1's review confirmed the
// original hand-duplicated struct shape here failed loudly rather than
// silently if it ever drifted from the real function, but a construction
// that cannot drift at all — the same func value — is strictly better.
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
// did not short-circuit first, this would 422 instead).
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

// TestReply_IdempotencyReplayDifferentPayloadIs409 pins step 6's 409 half.
func TestReply_IdempotencyReplayDifferentPayloadIs409(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))
	convID := uuid.MustParse(m.ConversationId)

	key := uuid.NewString()
	original := "original body"
	fingerprint := replyFingerprintForTest(t, convID, &original)
	h.Exec(t, `INSERT INTO communications.idempotency_records (id, key, payload_fingerprint, conversation_id, message_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, uuid.New(), key, fingerprint, convID, uuid.New(), h.Now())

	r := doReply(c, m.ConversationId, key, map[string]any{"textBody": "a different body"})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var body commErrorJSON
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
	h := newHarness(t)
	chID := setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))
	convID := uuid.MustParse(m.ConversationId)

	key := uuid.NewString()
	text := "accepted, then the channel dies"
	fingerprint := replyFingerprintForTest(t, convID, &text)
	h.Exec(t, `INSERT INTO communications.idempotency_records (id, key, payload_fingerprint, conversation_id, message_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, uuid.New(), key, fingerprint, convID, uuid.New(), h.Now())

	admin := h.SignIn(t, "communications:channels-manage")
	if r := admin.Do(http.MethodPut, "/api/v1/communications/channels/"+chID, map[string]any{"isActive": false}); r.Status != http.StatusOK {
		t.Fatalf("deactivate channel: status %d body %s, want 200", r.Status, r.Body)
	}

	r := doReply(c, m.ConversationId, key, map[string]any{"textBody": text})
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422 (channel_inactive precedes the cached replay)", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if errBody.Error.Code != "channel_inactive" {
		t.Errorf("code = %q, want channel_inactive, not the replay's cached 200", errBody.Error.Code)
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

// ---- Step 8: the staged-attachment preflight ----

// TestReply_AttachmentsNotReady_UnknownId pins the preflight for an
// attachmentIds entry that matches no staged upload at all.
func TestReply_AttachmentsNotReady_UnknownId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID, _ := replyableConversation(t, h, c)

	body := replyBody()
	body["attachmentIds"] = []string{uuid.NewString()}
	r := doReply(c, convID, uuid.NewString(), body)
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if errBody.Error.Code != "attachments_not_ready" {
		t.Errorf("code = %q, want attachments_not_ready", errBody.Error.Code)
	}
	if errBody.Error.Message != "One or more attachments are still being scanned or are unavailable." {
		t.Errorf("message = %q, want the exact attachments_not_ready text", errBody.Error.Message)
	}
}

// TestReply_AttachmentsNotReady_Expired pins the preflight's expires_at >
// now half of the gate: an otherwise-clean upload past its expiry is
// treated exactly like a missing one.
func TestReply_AttachmentsNotReady_Expired(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID, _ := replyableConversation(t, h, c)
	upload := mustStageAttachment(t, c, convID, []byte("hello"))
	h.Exec(t, `UPDATE communications.attachment_uploads SET expires_at = $1 WHERE id = $2`,
		h.Now().Add(-time.Minute), uuid.MustParse(upload.Id))

	body := replyBody()
	body["attachmentIds"] = []string{upload.Id}
	r := doReply(c, convID, uuid.NewString(), body)
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if errBody.Error.Code != "attachments_not_ready" {
		t.Errorf("code = %q, want attachments_not_ready", errBody.Error.Code)
	}
}

// TestReply_AttachmentsNotReady_WrongUploader pins the preflight's own
// uploader scoping: a staged upload that exists, is clean and unexpired,
// but belongs to a different caller, is invisible to this reply.
func TestReply_AttachmentsNotReady_WrongUploader(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID, _ := replyableConversation(t, h, owner)
	upload := mustStageAttachment(t, owner, convID, []byte("hello"))

	other := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	body := replyBody()
	body["attachmentIds"] = []string{upload.Id}
	r := doReply(other, convID, uuid.NewString(), body)
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if errBody.Error.Code != "attachments_not_ready" {
		t.Errorf("code = %q, want attachments_not_ready", errBody.Error.Code)
	}
}

// TestReply_AttachmentsPrecedeSuppression is the mutation-order test for
// steps 8 vs 9: an unready attachment AND a suppressed recipient in the
// same request must answer 409 attachments_not_ready, never 422
// recipient_suppressed — proving the preflight runs first. Swapping these
// two steps would flip this test's expectation.
func TestReply_AttachmentsPrecedeSuppression(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID, address := replyableConversation(t, h, c)
	h.Exec(t, `INSERT INTO communications.suppressions (id, normalized_email_address, reason, created_at) VALUES ($1, $2, NULL, $3)`,
		uuid.New(), address, h.Now())

	body := replyBody()
	body["attachmentIds"] = []string{uuid.NewString()} // unknown -> preflight fails
	r := doReply(c, convID, uuid.NewString(), body)
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409 (attachments_not_ready precedes suppression)", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if errBody.Error.Code != "attachments_not_ready" {
		t.Errorf("code = %q, want attachments_not_ready, not recipient_suppressed", errBody.Error.Code)
	}
}

// ---- Step 9: suppression ----

// TestReply_RecipientSuppressed pins step 9's shape exactly: 422
// recipient_suppressed, fields.recipients carrying the suppressed
// address(es) — the resolved recipient, already stored in its normalised
// (uppercased, D7) form on the fixture participant row, exactly as a real
// participant would carry it.
func TestReply_RecipientSuppressed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID, address := replyableConversation(t, h, c)
	h.Exec(t, `INSERT INTO communications.suppressions (id, normalized_email_address, reason, created_at) VALUES ($1, $2, NULL, $3)`,
		uuid.New(), address, h.Now())

	r := doReply(c, convID, uuid.NewString(), replyBody())
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if errBody.Error.Code != "recipient_suppressed" {
		t.Errorf("code = %q, want recipient_suppressed", errBody.Error.Code)
	}
	if errBody.Error.Message != "One or more recipients are suppressed." {
		t.Errorf("message = %q, want the exact recipient_suppressed text", errBody.Error.Message)
	}
	got := errBody.field("recipients")
	if len(got) != 1 || got[0] != address {
		t.Errorf("fields[recipients] = %v, want [%q]", got, address)
	}
}

// TestReply_SuppressionPrecedesAttachmentClaim is the mutation-order test
// for steps 9 vs 10: a suppressed recipient with an otherwise-claimable
// attachment must never reach the transaction at all — the staged upload's
// scan_status must still read 'clean' afterward, proving step 10's claim
// never ran. Swapping steps 9 and 10 would let the claim fire before the
// suppression check and this assertion would fail.
func TestReply_SuppressionPrecedesAttachmentClaim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID, address := replyableConversation(t, h, c)
	h.Exec(t, `INSERT INTO communications.suppressions (id, normalized_email_address, reason, created_at) VALUES ($1, $2, NULL, $3)`,
		uuid.New(), address, h.Now())
	upload := mustStageAttachment(t, c, convID, []byte("hello"))

	body := replyBody()
	body["attachmentIds"] = []string{upload.Id}
	r := doReply(c, convID, uuid.NewString(), body)
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422 (suppression precedes the transactional claim)", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if errBody.Error.Code != "recipient_suppressed" {
		t.Errorf("code = %q, want recipient_suppressed", errBody.Error.Code)
	}

	status := modtest.One[string](t, h, `SELECT scan_status FROM communications.attachment_uploads WHERE id = $1`, uuid.MustParse(upload.Id))
	if status != "clean" {
		t.Errorf("attachment scan_status = %q after a suppressed reply, want unchanged clean (the transaction must never have run)", status)
	}
}

// ---- Step 10: the transactional claim, success, and its promotion ----

// TestReply_Success_PromotesAttachmentVerbatim is the happy path with one
// staged attachment: 201, a message_attachments row carrying the staged
// upload's scan_status and content_hash verbatim (not recomputed), the
// attachment_uploads row deleted, its cleanup record transitioned to
// 'owned', an outbox job and idempotency record written, and the
// conversation's activity fields advanced (Subject filled once since this
// conversation already has one from CreateConversation).
func TestReply_Success_PromotesAttachmentVerbatim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID, _ := replyableConversation(t, h, c)
	upload := mustStageAttachment(t, c, convID, []byte("A reply attachment"))
	wantHash := modtest.One[string](t, h, `SELECT content_hash FROM communications.attachment_uploads WHERE id = $1`, uuid.MustParse(upload.Id))
	storageKey := modtest.One[string](t, h, `SELECT storage_key FROM communications.attachment_uploads WHERE id = $1`, uuid.MustParse(upload.Id))

	body := replyBody()
	body["attachmentIds"] = []string{upload.Id}
	key := uuid.NewString()
	r := doReply(c, convID, key, body)
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var resp conversationMutationJSON
	r.JSON(&resp)
	if resp.Status != "queued" {
		t.Errorf("status = %q, want queued", resp.Status)
	}
	if resp.MessageId == nil {
		t.Fatal("messageId is nil")
	}
	messageID := uuid.MustParse(*resp.MessageId)

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

	if n := h.Count(t, `SELECT count(*) FROM communications.attachment_uploads WHERE id = $1`, uuid.MustParse(upload.Id)); n != 0 {
		t.Errorf("attachment_uploads rows for %s = %d, want 0 (deleted after promotion)", upload.Id, n)
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

	subject := modtest.One[*string](t, h, `SELECT subject FROM communications.conversations WHERE id = $1`, uuid.MustParse(convID))
	if subject == nil {
		t.Fatal("conversation subject is nil, want the one CreateConversation set (fill-once must not clear it)")
	}
}

// TestReply_ConcurrentAttachmentClaimRace is the teeth check for why the
// transactional claim (step 10) exists at all, not just the preflight
// (step 8): two replies — through the real handler, each with its own
// idempotency key — race to claim the same staged attachment. Both
// preflights (plain SELECTs) can pass — nothing locks between them — but
// only one transaction's conditional clean -> claimed UPDATE can actually
// flip the row; the loser's claimed count comes back short and it answers
// the same 409 attachments_not_ready the preflight itself would, rather
// than double-sending the attachment or corrupting either message. The gate
// (LOCK TABLE ... IN EXCLUSIVE MODE, released only once both requests are
// confirmed waiting) reuses channels_concurrency_test.go's race/
// awaitLockWaiters — same package, no duplication needed now that this
// runs through the real handler rather than a white-box call.
func TestReply_ConcurrentAttachmentClaimRace(t *testing.T) {
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID, _ := replyableConversation(t, h, c)
	upload := mustStageAttachment(t, c, convID, []byte("racing bytes"))

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.attachment_uploads IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock attachment_uploads: %v", err)
	}

	body := replyBody()
	body["attachmentIds"] = []string{upload.Id}
	fns := []func() *modtest.Response{
		func() *modtest.Response { return doReply(c, convID, uuid.NewString(), body) },
		func() *modtest.Response { return doReply(c, convID, uuid.NewString(), body) },
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(fns...)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	var created, conflicted int
	for _, r := range responses {
		switch r.Status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicted++
			var errBody commErrorJSON
			r.JSON(&errBody)
			if errBody.Error.Code != "attachments_not_ready" {
				t.Errorf("loser code = %q, want attachments_not_ready", errBody.Error.Code)
			}
		default:
			t.Errorf("status %d body %s, want 201 or 409", r.Status, r.Body)
		}
	}
	if created != 1 {
		t.Errorf("created = %d, want exactly 1", created)
	}
	if conflicted != 1 {
		t.Errorf("conflicted = %d, want exactly 1", conflicted)
	}
}

// TestReply_ConcurrentIdenticalReplyAnswersTheSameReplayTwice is fix round
// 2 item 2's teeth check for reply's own ux_idempotency_records_key race —
// the mirror image of
// TestCreateConversation_ConcurrentIdenticalCreateAnswersTheSameReplayTwice
// (conversations_test.go's own comment has the full reasoning, which
// applies here unchanged): two concurrent replies carrying the identical
// body and Idempotency-Key against a replyable conversation race past step
// 6's replay check and both attempt the final INSERT into
// idempotency_records inside queueReply's transaction. Only one can win
// the unique index; the loser's failing INSERT aborts its whole
// transaction, so none of that reply's other writes persist either — only
// the winner's do. Run at -count=5 per fix round instructions since a race
// this narrow does not always land the same way twice.
func TestReply_ConcurrentIdenticalReplyAnswersTheSameReplayTwice(t *testing.T) {
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	convID, _ := replyableConversation(t, h, c)
	key := uuid.NewString()
	body := replyBody()

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.idempotency_records IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock idempotency_records: %v", err)
	}

	const n = 2
	fns := make([]func() *modtest.Response, n)
	for i := 0; i < n; i++ {
		fns[i] = func() *modtest.Response { return doReply(c, convID, key, body) }
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(fns...)
		close(finished)
	}()
	awaitLockWaiters(t, h, n, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	var created, replayed int
	var messageIDs []string
	for _, r := range responses {
		switch r.Status {
		case http.StatusCreated:
			created++
		case http.StatusOK:
			replayed++
		default:
			t.Fatalf("status %d body %s, want 200 or 201 (never 500)", r.Status, r.Body)
		}
		var m conversationMutationJSON
		r.JSON(&m)
		if m.MessageId != nil {
			messageIDs = append(messageIDs, *m.MessageId)
		}
	}
	if created != 1 {
		t.Errorf("created (201) = %d, want exactly 1", created)
	}
	if replayed != n-1 {
		t.Errorf("replayed (200) = %d, want exactly %d", replayed, n-1)
	}
	if len(messageIDs) == 2 && messageIDs[0] != messageIDs[1] {
		t.Errorf("messageIds = %v, want both responses to carry the winner's same id", messageIDs)
	}

	if len(messageIDs) > 0 {
		count := h.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE id = $1`, uuid.MustParse(messageIDs[0]))
		if count != 1 {
			t.Errorf("conversation_messages rows for %s = %d, want exactly 1", messageIDs[0], count)
		}
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
