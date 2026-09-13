package communications_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the Conversations area's tests (EP/ConversationEndpoints.cs,
// communications inventory §1.2, §2, §4; task 5's brief, dispatch and their
// corrections are the authority for what must be pinned here) for
// getCommunicationsConversations, postCommunicationsConversations,
// getCommunicationsConversationsById, patchCommunicationsConversationsById,
// postCommunicationsConversationsByIdNotes and
// postCommunicationsConversationsByIdRead. Tag and tag-link tests live in
// tags_test.go.

type participantJSON struct {
	Address     string  `json:"address"`
	ChannelId   string  `json:"channelId"`
	ContactId   *int32  `json:"contactId"`
	DisplayName *string `json:"displayName"`
	Id          string  `json:"id"`
}

type conversationTagJSON struct {
	Color *string `json:"color"`
	Id    string  `json:"id"`
	Name  string  `json:"name"`
}

type conversationListItemJSON struct {
	Id                        string                `json:"id"`
	ChannelId                 string                `json:"channelId"`
	Subject                   *string               `json:"subject"`
	Status                    string                `json:"status"`
	AssignedUserId            *string               `json:"assignedUserId"`
	CustomerId                *int32                `json:"customerId"`
	CustomerAssociationSource *string               `json:"customerAssociationSource"`
	SuggestedCustomerId       *int32                `json:"suggestedCustomerId"`
	CandidateCustomerIds      []int32               `json:"candidateCustomerIds"`
	LastActivityAt            time.Time             `json:"lastActivityAt"`
	PreviewText               *string               `json:"previewText"`
	Participants              []participantJSON     `json:"participants"`
	Unread                    bool                  `json:"unread"`
	Tags                      []conversationTagJSON `json:"tags"`
}

type paginatedConversationsJSON struct {
	Data       []conversationListItemJSON `json:"data"`
	Pagination struct {
		Page            int32 `json:"page"`
		PageSize        int32 `json:"pageSize"`
		TotalCount      int32 `json:"totalCount"`
		TotalPages      int32 `json:"totalPages"`
		HasNextPage     bool  `json:"hasNextPage"`
		HasPreviousPage bool  `json:"hasPreviousPage"`
	} `json:"pagination"`
}

type conversationDetailJSON struct {
	Id                        string                `json:"id"`
	ChannelId                 string                `json:"channelId"`
	Subject                   *string               `json:"subject"`
	Status                    string                `json:"status"`
	AssignedUserId            *string               `json:"assignedUserId"`
	CustomerId                *int32                `json:"customerId"`
	CustomerAssociationSource *string               `json:"customerAssociationSource"`
	SuggestedCustomerId       *int32                `json:"suggestedCustomerId"`
	CandidateCustomerIds      []int32               `json:"candidateCustomerIds"`
	LastActivityAt            time.Time             `json:"lastActivityAt"`
	PreviewText               *string               `json:"previewText"`
	CreatedAt                 time.Time             `json:"createdAt"`
	Messages                  []map[string]any      `json:"messages"`
	Participants              []participantJSON     `json:"participants"`
	Tags                      []conversationTagJSON `json:"tags"`
	LastReadAt                *time.Time            `json:"lastReadAt"`
	ReplyRecipients           struct {
		CanReply    bool              `json:"canReply"`
		CanReplyAll bool              `json:"canReplyAll"`
		ReplyTo     *string           `json:"replyTo"`
		ReplyAllCc  []participantJSON `json:"replyAllCc"`
	} `json:"replyRecipients"`
}

type conversationPatchJSON struct {
	Id                        string  `json:"id"`
	Status                    string  `json:"status"`
	AssignedUserId            *string `json:"assignedUserId"`
	CustomerId                *int32  `json:"customerId"`
	CustomerAssociationSource *string `json:"customerAssociationSource"`
	SuggestedCustomerId       *int32  `json:"suggestedCustomerId"`
	CandidateCustomerIds      []int32 `json:"candidateCustomerIds"`
}

type conversationMutationJSON struct {
	ConversationId string  `json:"conversationId"`
	MessageId      *string `json:"messageId"`
	Status         string  `json:"status"`
	IdempotencyKey *string `json:"idempotencyKey"`
}

// setupChannel creates one active email channel through the channels API
// (channels-manage), for a conversation fixture that does not itself depend
// on the permission under test.
func setupChannel(t *testing.T, h *modtest.Harness) string {
	t.Helper()
	admin := h.SignIn(t, "communications:channels-manage")
	return createChannel(t, admin, newChannelBody(channelAddress(t))).Id
}

// newConversationBody is a valid CreateConversationRequest: a subject, a
// text body and one "to" recipient — the minimum ValidateConversation
// accepts (inventory §3.1's field table).
func newConversationBody(to string) map[string]any {
	return map[string]any{
		"subject":  "Subject " + uuid.NewString(),
		"textBody": "Body text",
		"to":       []map[string]any{{"email": to}},
	}
}

// createConversation posts body with a fresh Idempotency-Key and fails t
// unless the response is 201, returning the decoded mutation response.
func createConversation(t *testing.T, c *modtest.Client, body map[string]any) conversationMutationJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", uuid.NewString()))
	if r.Status != http.StatusCreated {
		t.Fatalf("create conversation: status %d body %s, want 201", r.Status, r.Body)
	}
	var m conversationMutationJSON
	r.JSON(&m)
	return m
}

// ---- GetCommunicationsConversations (List) ----

// TestListConversations_NeverReturns404 pins inventory §6 item 1 / task 5
// dispatch point 4: the contract declares a 404 for this operation that the
// handler can never produce. Ported as-is, not "fixed" — this test proves
// the port did not invent one either.
func TestListConversations_NeverReturns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view")

	r := c.Do(http.MethodGet, "/api/v1/communications/conversations?tagId="+uuid.NewString(), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200 (never 404, even for a tagId matching nothing)", r.Status, r.Body)
	}
	var page paginatedConversationsJSON
	r.JSON(&page)
	if len(page.Data) != 0 {
		t.Errorf("data = %v, want none for an unmatched tagId", page.Data)
	}
}

// TestListConversations_OrderedByLastActivityAtDescending pins ListConversations'
// `OrderByDescending(item => item.LastActivityAt)`.
func TestListConversations_OrderedByLastActivityAtDescending(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	chID := setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")

	first := createConversation(t, c, newConversationBody("a@example.test"))
	h.Advance(time.Minute)
	second := createConversation(t, c, newConversationBody("b@example.test"))

	r := c.Do(http.MethodGet, "/api/v1/communications/conversations", nil)
	var page paginatedConversationsJSON
	r.JSON(&page)
	if len(page.Data) < 2 {
		t.Fatalf("data = %d items, want at least 2", len(page.Data))
	}
	var firstIdx, secondIdx = -1, -1
	for i, item := range page.Data {
		if item.Id == first.ConversationId {
			firstIdx = i
		}
		if item.Id == second.ConversationId {
			secondIdx = i
		}
	}
	if firstIdx == -1 || secondIdx == -1 {
		t.Fatalf("both conversations must be present: first at %d, second at %d", firstIdx, secondIdx)
	}
	if secondIdx > firstIdx {
		t.Errorf("second (more recent) at index %d, first at %d, want second before first (LastActivityAt descending)", secondIdx, firstIdx)
	}
	_ = chID
}

// TestListConversations_UnknownStatusYieldsEmptyPageNot400 pins inventory
// §2's ListConversations bullet: an unknown status filters to nothing,
// never a validation error.
func TestListConversations_UnknownStatusYieldsEmptyPageNot400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view")

	r := c.Do(http.MethodGet, "/api/v1/communications/conversations?status=not-a-real-status", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var page paginatedConversationsJSON
	r.JSON(&page)
	if len(page.Data) != 0 {
		t.Errorf("data = %v, want none for an unknown status", page.Data)
	}
}

// TestListConversations_PageValuesAreClampedNotRejected pins PageValues
// (`:470`): out-of-range page/pageSize are silently clamped, never 400.
func TestListConversations_PageValuesAreClampedNotRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view")

	r := c.Do(http.MethodGet, "/api/v1/communications/conversations?page=-5&pageSize=99999", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200 (clamped, not rejected)", r.Status, r.Body)
	}
	var page paginatedConversationsJSON
	r.JSON(&page)
	if page.Pagination.Page != 1 {
		t.Errorf("page = %d, want clamped to 1", page.Pagination.Page)
	}
	if page.Pagination.PageSize != 100 {
		t.Errorf("pageSize = %d, want clamped to 100", page.Pagination.PageSize)
	}
}

// TestListConversations_RequiresView is the plain (non-asymmetric) pairing:
// conversations-view alone.
func TestListConversations_RequiresView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if r := h.SignIn(t).Do(http.MethodGet, "/api/v1/communications/conversations", nil); r.Status != http.StatusForbidden {
		t.Errorf("no permission: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t, "communications:conversations-view").Do(http.MethodGet, "/api/v1/communications/conversations", nil); r.Status != http.StatusOK {
		t.Errorf("conversations-view: status %d, want 200", r.Status)
	}
}

// ---- PostCommunicationsConversations (Create) ----

// TestCreateConversation_ReplyPermissionAloneSuffices pins task 5 dispatch
// point 6: POST /conversations needs communications:conversations-reply
// ALONE — the inventory calls this asymmetric with every other
// conversations-reply-gated endpoint, all of which also require +view. A
// client holding conversations-reply and nothing else (deliberately no
// conversations-view) must still succeed; if module.Router or
// communications.yaml ever grew a +view requirement here, this client would
// be forbidden and this test would fail.
func TestCreateConversation_ReplyPermissionAloneSuffices(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply") // deliberately no +view

	r := c.Do(http.MethodPost, "/api/v1/communications/conversations", newConversationBody("nobody@example.test"),
		modtest.Header("Idempotency-Key", uuid.NewString()))
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201 (conversations-reply alone must suffice)", r.Status, r.Body)
	}
}

// TestCreateConversation_ViewAloneIsForbidden is the mirror check: view
// alone (the permission every OTHER read/write pairing in this task
// includes) must NOT suffice for create, since the contract requires reply.
func TestCreateConversation_ViewAloneIsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view")
	r := c.Do(http.MethodPost, "/api/v1/communications/conversations", newConversationBody("nobody@example.test"),
		modtest.Header("Idempotency-Key", uuid.NewString()))
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}

// TestCreateConversation_ValidationErrors pins ValidateConversation's field
// table (inventory §3.1) and the error shape (§3, `:284`'s
// `error.code`/`error.message` assertion, ported here since :284 itself is
// out of this task's scope).
func TestCreateConversation_ValidationErrors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply")

	r := c.Do(http.MethodPost, "/api/v1/communications/conversations", map[string]any{"subject": "x"},
		modtest.Header("Idempotency-Key", uuid.NewString()))
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	if body.Error.Code != "invalid_request" || body.Error.Message != "The request is invalid." {
		t.Errorf("error = %+v, want code invalid_request, message %q", body.Error, "The request is invalid.")
	}
	if got := body.field("to"); len(got) != 1 || got[0] != "At least one recipient is required." {
		t.Errorf("fields[to] = %v, want the required-recipient message", got)
	}
	if got := body.field("body"); len(got) != 1 || got[0] != "TextBody or HtmlBody is required." {
		t.Errorf("fields[body] = %v, want the required-body message", got)
	}
}

// TestCreateConversation_IdempotencyKeyBlankIs400 pins the flat
// idempotency_key_required shape for a *present but invalid* key. A
// completely *absent* header never reaches this handler at all — refused
// earlier by the generated decoder, since the contract marks the header
// `required: true` (see conversations_create.go's own comment) — and
// neither, in practice, does an all-whitespace one: net/http's header
// parsing trims RFC 7230 optional whitespace from a header value before
// this module ever sees it, so a value of only spaces or tabs arrives as
// the same empty string an absent header would, intercepted by the
// generated binder's own "required" check before reaching here. This test
// instead uses a key over the 200-character limit — long enough to survive
// transport as a non-empty string, short enough for the assertion to still
// be about length, not content — to exercise this module's *own*
// idempotency_key_required rather than the platform's decode-error 400.
func TestCreateConversation_IdempotencyKeyTooLongIs400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply")

	r := c.Do(http.MethodPost, "/api/v1/communications/conversations", newConversationBody("x@example.test"),
		modtest.Header("Idempotency-Key", strings.Repeat("k", 201)))
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

// TestCreateConversation_IdempotencyReplaySameFingerprintIs200 pins the
// replay semantics (inventory §5.1): the same key with the identical
// payload returns the original result with 200, and does not create a
// second conversation.
func TestCreateConversation_IdempotencyReplaySameFingerprintIs200(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply")
	key := uuid.NewString()
	body := newConversationBody("replay@example.test")

	first := c.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", key))
	if first.Status != http.StatusCreated {
		t.Fatalf("first: status %d body %s, want 201", first.Status, first.Body)
	}
	var firstResp conversationMutationJSON
	first.JSON(&firstResp)

	second := c.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", key))
	if second.Status != http.StatusOK {
		t.Fatalf("second: status %d body %s, want 200 (replay)", second.Status, second.Body)
	}
	var secondResp conversationMutationJSON
	second.JSON(&secondResp)
	if secondResp.ConversationId != firstResp.ConversationId {
		t.Errorf("replay conversationId = %s, want %s", secondResp.ConversationId, firstResp.ConversationId)
	}
}

// TestCreateConversation_IdempotencyKeyReusedWithDifferentPayloadIs409
// pins the other half: a different payload under the same key is a
// conflict, never a silent overwrite.
func TestCreateConversation_IdempotencyKeyReusedWithDifferentPayloadIs409(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply")
	key := uuid.NewString()

	first := c.Do(http.MethodPost, "/api/v1/communications/conversations", newConversationBody("a@example.test"), modtest.Header("Idempotency-Key", key))
	if first.Status != http.StatusCreated {
		t.Fatalf("first: status %d body %s, want 201", first.Status, first.Body)
	}
	second := c.Do(http.MethodPost, "/api/v1/communications/conversations", newConversationBody("b@example.test"), modtest.Header("Idempotency-Key", key))
	if second.Status != http.StatusConflict {
		t.Fatalf("second: status %d body %s, want 409", second.Status, second.Body)
	}
	var body commErrorJSON
	second.JSON(&body)
	if body.Error.Code != "idempotency_key_reused" {
		t.Errorf("code = %q, want idempotency_key_reused", body.Error.Code)
	}
}

// TestCreateConversation_ConcurrentIdenticalCreateAnswersTheSameReplayTwice
// is fix round 2 item 2's teeth check for the ux_idempotency_records_key
// race: the replay lookup (`:224`) and this handler's own transactional
// INSERT into idempotency_records are not atomic together, so two
// concurrent POSTs carrying the identical body and Idempotency-Key can both
// pass the lookup (neither's own transaction has committed yet) and both
// attempt the final INSERT. Only one can win the unique index; the loser's
// failing INSERT aborts its entire transaction, so none of that request's
// other writes (its own conversation, message, deliveries, outbox job)
// persist either — only the winner's do. Before the fix the loser's unique
// violation reached the unhandled wrapped-error path (an opaque 500);
// after it, the loser is answered exactly the sequential replay path's 200
// with the SAME conversationId/messageId the winner's own 201 carries —
// the same defect class, and the same fix shape, as the suppression
// dedupe race (fix round 1) and the attachment-replay race (task 6 fix
// round 1). The gate (LOCK TABLE ... IN EXCLUSIVE MODE, released only once
// both requests are confirmed waiting) reuses
// channels_concurrency_test.go's race/awaitLockWaiters. Run at -count=5
// per fix round instructions since a race this narrow does not always land
// the same way twice.
func TestCreateConversation_ConcurrentIdenticalCreateAnswersTheSameReplayTwice(t *testing.T) {
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply")
	address := "race-" + uuid.NewString() + "@example.test"
	// Pre-create the "to" participant via one ordinary, completed request
	// first, so the concurrent race below never touches
	// findOrCreateParticipantByAddress's own find-or-create INSERT branch —
	// a separate, already-known unguarded race
	// (ux_participants_channel_id_address) this test is not about; gating
	// only idempotency_records (below) leaves that race free to fire too if
	// the participant does not already exist, which would fail this test
	// for the wrong reason.
	createConversation(t, c, newConversationBody(address))

	key := uuid.NewString()
	body := newConversationBody(address)

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
		fns[i] = func() *modtest.Response {
			return c.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", key))
		}
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
	var conversationIDs, messageIDs []string
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
		conversationIDs = append(conversationIDs, m.ConversationId)
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
	// Fix round 3's finding 2: these were conditional on len(...) == 2 /
	// len(...) > 0, which is vacuous exactly when it matters most — a race
	// that produced the wrong number of rows would skip the very
	// assertions meant to catch that, rather than fail. Asserted
	// unconditionally now: a short slice fails the test via Fatalf instead
	// of silently disarming the checks below it.
	if len(conversationIDs) != n {
		t.Fatalf("conversationIds = %v, want %d entries (one per response)", conversationIDs, n)
	}
	if conversationIDs[0] != conversationIDs[1] {
		t.Errorf("conversationIds = %v, want both responses to carry the winner's same id", conversationIDs)
	}
	if len(messageIDs) != n {
		t.Fatalf("messageIds = %v, want %d entries (one per response)", messageIDs, n)
	}
	if messageIDs[0] != messageIDs[1] {
		t.Errorf("messageIds = %v, want both responses to carry the winner's same id", messageIDs)
	}

	count := h.Count(t, `SELECT count(*) FROM communications.conversations WHERE id = $1`, uuid.MustParse(conversationIDs[0]))
	if count != 1 {
		t.Errorf("conversations rows for %s = %d, want exactly 1", conversationIDs[0], count)
	}
}

// TestCreateConversation_ChannelInvalid422 pins the 422 channel_invalid
// path for an unknown channelId.
func TestCreateConversation_ChannelInvalid422(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply")
	body := newConversationBody("x@example.test")
	body["channelId"] = uuid.NewString()

	r := c.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", uuid.NewString()))
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if errBody.Error.Code != "channel_invalid" {
		t.Errorf("code = %q, want channel_invalid", errBody.Error.Code)
	}
}

// TestCreateConversation_CustomerInvalid422 pins the 422 customer_invalid
// path for a customerId contracts.CustomerDirectory does not resolve
// (fakeDirectory in harness_test.go only knows 1001 and 1002).
func TestCreateConversation_CustomerInvalid422(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply")
	body := newConversationBody("x@example.test")
	body["customerId"] = 999999

	r := c.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", uuid.NewString()))
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if errBody.Error.Code != "customer_invalid" {
		t.Errorf("code = %q, want customer_invalid", errBody.Error.Code)
	}
}

// TestCreateConversation_ValidCustomerIsAssociatedManually proves the
// success path resolves a known customer and stamps customerAssociationSource
// "manual" (`request.CustomerId.HasValue ? CustomerAssociationSources.Manual : null`).
func TestCreateConversation_ValidCustomerIsAssociatedManually(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	body := newConversationBody("x@example.test")
	body["customerId"] = 1001

	m := createConversation(t, c, body)
	r := c.Do(http.MethodGet, "/api/v1/communications/conversations/"+m.ConversationId, nil)
	var detail conversationDetailJSON
	r.JSON(&detail)
	if detail.CustomerId == nil || *detail.CustomerId != 1001 {
		t.Errorf("customerId = %v, want 1001", detail.CustomerId)
	}
	if detail.CustomerAssociationSource == nil || *detail.CustomerAssociationSource != "manual" {
		t.Errorf("customerAssociationSource = %v, want manual", detail.CustomerAssociationSource)
	}
}

// ---- POST /conversations, the recipients[] (generic) delivery path ----
//
// Task 5 fix round 1, item 3: resolveGenericDeliveries
// (conversations_create.go) — AddGenericDeliveriesAsync's other branch,
// taken whenever the request supplies `recipients` — had no test coverage
// at all; neither did the recipients[]-specific validation rules in
// conversations_validation.go. The tests below close that gap.

// newConversationBodyWithRecipients is a valid CreateConversationRequest
// using the generic recipients[] path instead of to/cc: subject and
// textBody set, recipients populated, to/cc both omitted so
// resolveGenericDeliveries — not resolveSimpleDeliveries — is the branch
// PostCommunicationsConversations takes (conversations_create.go:
// `len(recipients) == 0 && channelType == "email"` is false whenever
// recipients is non-empty).
func newConversationBodyWithRecipients(recipients []map[string]any) map[string]any {
	return map[string]any{
		"subject":    "Subject " + uuid.NewString(),
		"textBody":   "Body text",
		"recipients": recipients,
	}
}

func messageDeliveries(t *testing.T, message map[string]any) []map[string]any {
	t.Helper()
	raw, ok := message["deliveries"].([]any)
	if !ok {
		t.Fatalf("message has no deliveries array: %v", message)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, d := range raw {
		m, ok := d.(map[string]any)
		if !ok {
			t.Fatalf("delivery is not an object: %v", d)
		}
		out = append(out, m)
	}
	return out
}

// TestCreateConversation_RecipientsArrayByAddress proves the recipients[]
// path resolves a bare address entry to a delivery: the participant is
// found-or-created by its *normalised* address (EmailSuppression.Normalize
// — uppercased, design doc D7), and — unlike the simple to/cc path, which
// keeps the caller's raw address on the delivery row — the generic path
// stores the delivery's destination from the resolved participant's own
// (already normalised) address (conversations_create.go:
// `recipientAddress: participant.Address`).
func TestCreateConversation_RecipientsArrayByAddress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")

	body := newConversationBodyWithRecipients([]map[string]any{
		{"address": "generic@example.test", "type": "cc"},
	})
	m := createConversation(t, c, body)

	r := c.Do(http.MethodGet, "/api/v1/communications/conversations/"+m.ConversationId, nil)
	var detail conversationDetailJSON
	r.JSON(&detail)
	if len(detail.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(detail.Messages))
	}
	deliveries := messageDeliveries(t, detail.Messages[0])
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(deliveries))
	}
	if got := deliveries[0]["destination"]; got != "GENERIC@EXAMPLE.TEST" {
		t.Errorf("destination = %v, want the normalised (uppercased) address", got)
	}
	if got := deliveries[0]["recipientType"]; got != "cc" {
		t.Errorf("recipientType = %v, want cc", got)
	}
}

// TestCreateConversation_RecipientsArrayByParticipantId proves the
// participantId branch: a recipient naming an existing participant on the
// same channel (by id) resolves without needing an address at all, and
// defaults recipientType to "to" when the entry omits it.
func TestCreateConversation_RecipientsArrayByParticipantId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")

	first := createConversation(t, c, newConversationBody("shared@example.test"))
	firstDetail := conversationDetailJSON{}
	c.Do(http.MethodGet, "/api/v1/communications/conversations/"+first.ConversationId, nil).JSON(&firstDetail)
	if len(firstDetail.Participants) != 1 {
		t.Fatalf("first conversation participants = %d, want 1", len(firstDetail.Participants))
	}
	participantID := firstDetail.Participants[0].Id

	body := newConversationBodyWithRecipients([]map[string]any{
		{"participantId": participantID},
	})
	second := createConversation(t, c, body)

	var secondDetail conversationDetailJSON
	c.Do(http.MethodGet, "/api/v1/communications/conversations/"+second.ConversationId, nil).JSON(&secondDetail)
	deliveries := messageDeliveries(t, secondDetail.Messages[0])
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(deliveries))
	}
	if got := deliveries[0]["recipientType"]; got != "to" {
		t.Errorf("recipientType = %v, want to (the default when omitted)", got)
	}
	if got := deliveries[0]["destination"]; got != "SHARED@EXAMPLE.TEST" {
		t.Errorf("destination = %v, want the existing participant's stored (normalised) address", got)
	}
}

// TestCreateConversation_RecipientsArrayValidation_InvalidEntry pins the
// per-index recipients[i] field error (inventory §3.1): an entry with
// neither a participantId nor a valid address is rejected before any
// delivery resolution is attempted.
func TestCreateConversation_RecipientsArrayValidation_InvalidEntry(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply")

	body := newConversationBodyWithRecipients([]map[string]any{
		{"address": "not-an-email"},
	})
	r := c.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", uuid.NewString()))
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if got := errBody.field("recipients[0]"); len(got) != 1 || got[0] != "A participant id or valid channel address is required." {
		t.Errorf("fields[recipients[0]] = %v, want the required message", got)
	}
}

// TestCreateConversation_RecipientsArrayValidation_TooMany pins the
// recipients[] count cap.
func TestCreateConversation_RecipientsArrayValidation_TooMany(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply")

	recipients := make([]map[string]any, 101)
	for i := range recipients {
		recipients[i] = map[string]any{"participantId": uuid.NewString()}
	}
	body := newConversationBodyWithRecipients(recipients)
	r := c.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", uuid.NewString()))
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var errBody commErrorJSON
	r.JSON(&errBody)
	if got := errBody.field("recipients"); len(got) != 1 || got[0] != "At most 100 recipients are allowed." {
		t.Errorf("fields[recipients] = %v, want the count-limit message", got)
	}
}

// TestCreateConversation_RecipientsArrayContactId proves the contactId
// branch both ways: a contactId contracts.CustomerDirectory resolves (2001,
// fakeDirectory in harness_test.go) succeeds; one it does not resolve
// throws unhandled in .NET (`AddGenericDeliveriesAsync:430-431`) and is
// ported as errContactNotFound, surfacing as a 500 — deliberately, not
// hardened into a 4xx (the same faithful treatment task 5 dispatch point 7
// asks for on PATCH's assignedUserId).
func TestCreateConversation_RecipientsArrayContactId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply")

	t.Run("known contact succeeds", func(t *testing.T) {
		t.Parallel()
		body := newConversationBodyWithRecipients([]map[string]any{
			{"address": "known-contact@example.test", "contactId": 2001},
		})
		r := c.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", uuid.NewString()))
		if r.Status != http.StatusCreated {
			t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
		}
	})

	t.Run("unknown contact is a 500", func(t *testing.T) {
		t.Parallel()
		body := newConversationBodyWithRecipients([]map[string]any{
			{"address": "unknown-contact@example.test", "contactId": 999999},
		})
		r := c.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", uuid.NewString()),
			modtest.SkipContract("deliberately unvalidated per .NET's unhandled InvalidOperationException; the 500 is expected, not documented as a contract response"))
		if r.Status != http.StatusInternalServerError {
			t.Fatalf("status %d body %s, want 500", r.Status, r.Body)
		}
	})
}

// TestCreateConversation_RecipientsArrayUnresolvableEntryIsSkippedSilently
// pins a genuine .NET oddity this port reproduces faithfully: a recipient
// naming a participantId that does not exist, with no address to fall back
// on, passes ValidateConversation (a participantId alone satisfies its
// per-entry check) but resolves to nothing at delivery time and is silently
// dropped — `if (participant is null) continue;` — rather than erroring.
// The conversation is still created, with a message that carries zero
// deliveries.
func TestCreateConversation_RecipientsArrayUnresolvableEntryIsSkippedSilently(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")

	body := newConversationBodyWithRecipients([]map[string]any{
		{"participantId": uuid.NewString()},
	})
	m := createConversation(t, c, body)

	var detail conversationDetailJSON
	c.Do(http.MethodGet, "/api/v1/communications/conversations/"+m.ConversationId, nil).JSON(&detail)
	if len(detail.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(detail.Messages))
	}
	deliveries := messageDeliveries(t, detail.Messages[0])
	if len(deliveries) != 0 {
		t.Errorf("deliveries = %v, want none (the recipient's participantId does not exist and it carries no address)", deliveries)
	}
}

// ---- GetCommunicationsConversationsById (Get) ----

func TestGetConversation_NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view")
	r := c.Do(http.MethodGet, "/api/v1/communications/conversations/"+uuid.NewString(), nil)
	if r.Status != http.StatusNotFound {
		t.Fatalf("status %d body %s, want 404", r.Status, r.Body)
	}
	if len(r.Body) != 0 {
		t.Errorf("body = %s, want empty (bare 404)", r.Body)
	}
}

// TestGetConversation_ReplyRecipientsAreAlwaysAllNegative pins task 5
// dispatch's explicit hazard: this port has no inbound path (design §1.1;
// no production writer ever produces a direction='inbound' message), so
// canReply is false and replyAllCc is empty for every conversation created
// through the API — the only value replyRecipientsOf can ever produce in
// production (see its own comment). Pinned rather than worked around.
func TestGetConversation_ReplyRecipientsAreAlwaysAllNegative(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))

	r := c.Do(http.MethodGet, "/api/v1/communications/conversations/"+m.ConversationId, nil)
	var detail conversationDetailJSON
	r.JSON(&detail)
	if detail.ReplyRecipients.CanReply {
		t.Error("canReply = true, want false")
	}
	if detail.ReplyRecipients.CanReplyAll {
		t.Error("canReplyAll = true, want false")
	}
	if detail.ReplyRecipients.ReplyTo != nil {
		t.Errorf("replyTo = %v, want nil", detail.ReplyRecipients.ReplyTo)
	}
	if len(detail.ReplyRecipients.ReplyAllCc) != 0 {
		t.Errorf("replyAllCc = %v, want empty", detail.ReplyRecipients.ReplyAllCc)
	}
}

// TestGetConversation_MessageParticipantIsNullNotZeroValue is task 5 fix
// round 1's item 4 teeth check: before the fix, participant was a
// non-pointer field in the generated contract, so a message with no
// participant (every message this port ever writes — neither AddNote nor
// CreateConversation sets participant_id, matching .NET) rendered as the
// zero-value participant object instead of JSON null. The contract now
// marks it nullable (openapi/communications.yaml) and the handler emits a
// nil pointer; this asserts the wire shape directly, since
// conversationDetailJSON's Messages field decodes generically
// (map[string]any) rather than through a typed, necessarily-non-nil struct
// that would hide the difference.
func TestGetConversation_MessageParticipantIsNullNotZeroValue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, c, newConversationBody("x@example.test"))

	r := c.Do(http.MethodGet, "/api/v1/communications/conversations/"+m.ConversationId, nil)
	var detail conversationDetailJSON
	r.JSON(&detail)
	if len(detail.Messages) != 1 {
		t.Fatalf("messages = %d, want 1 (the outbound message CreateConversation wrote)", len(detail.Messages))
	}
	participant, present := detail.Messages[0]["participant"]
	if !present {
		t.Fatal(`messages[0] has no "participant" key at all, want the key present with a null value`)
	}
	if participant != nil {
		t.Errorf("messages[0].participant = %v, want null (this message was written with no participant_id)", participant)
	}
}

func TestGetConversation_RequiresView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))

	if r := h.SignIn(t).Do(http.MethodGet, "/api/v1/communications/conversations/"+m.ConversationId, nil); r.Status != http.StatusForbidden {
		t.Errorf("no permission: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t, "communications:conversations-view").Do(http.MethodGet, "/api/v1/communications/conversations/"+m.ConversationId, nil); r.Status != http.StatusOK {
		t.Errorf("conversations-view: status %d, want 200", r.Status)
	}
}

// ---- PatchCommunicationsConversationsById ----

// TestPatchConversation_NullBodyIs400BeforeLookup pins the first half of
// task 5 dispatch correction 2: a missing body against a nonexistent
// conversation still answers 400, not 404 — the null/status check runs
// before the lookup.
func TestPatchConversation_NullBodyIs400BeforeLookup(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")

	r := c.Do(http.MethodPatch, "/api/v1/communications/conversations/"+uuid.NewString(), nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400 (null body precedes the 404)", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	if body.Error.Code != "invalid_request" || body.Error.Message != "Status must be open, closed, or archived." {
		t.Errorf("error = %+v, want invalid_request / %q", body.Error, "Status must be open, closed, or archived.")
	}
}

// TestPatchConversation_OversizedBodyIsDecodeErrorNotNullBody is task 5 fix
// round 1's item 5 teeth check: a body withRawPatchBody fails to *read* —
// here, one over this operation's MaxBytesReader cap — must not be treated
// as "the caller sent nothing." Before the fix, any read error fell through
// to the handler with an empty body, indistinguishable from a genuine null
// body and answering the same "Status must be open, closed, or archived."
// 400. The fix routes a read failure through the platform's own
// decode-error response instead (module.go's writeDecodeError,
// application/problem+json) — a different shape entirely from this
// module's flat CommunicationErrorResponse 400s.
func TestPatchConversation_OversizedBodyIsDecodeErrorNotNullBody(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))
	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")

	oversized := append([]byte(`{"status":"closed","padding":"`), bytes.Repeat([]byte("x"), 2*1024*1024)...)
	oversized = append(oversized, []byte(`"}`)...)
	r := c.Do(http.MethodPatch, "/api/v1/communications/conversations/"+m.ConversationId, nil,
		modtest.RawBody("application/json", oversized),
		modtest.SkipContract("an oversized body's 400 is the platform's generic decode-error problem, not this operation's documented CommunicationErrorResponse shape"))
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	if ct := r.Header("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json (the generic decode-error shape, not this module's own CommunicationErrorResponse)", ct)
	}
	if strings.Contains(string(r.Body), "Status must be open") {
		t.Errorf("body = %s, want the platform's generic decode-error problem, not PATCH's null-body message", r.Body)
	}
}

// TestPatchConversation_InvalidStatusIs400BeforeLookup is the same ordering
// with an explicit bad status rather than a missing body.
func TestPatchConversation_InvalidStatusIs400BeforeLookup(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")

	r := c.Do(http.MethodPatch, "/api/v1/communications/conversations/"+uuid.NewString(), map[string]any{"status": "not-a-status"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400 (status validation precedes the 404)", r.Status, r.Body)
	}
}

// TestPatchConversation_BadCustomerIdTypeAgainstMissingConversationIs404
// pins the SECOND, opposite-direction half of dispatch correction 2: a bad
// customerId *type* runs after the lookup, so against a conversation that
// does not exist it is 404, not 400 — the exact reverse of the status
// check's ordering, in the SAME handler. A mutation that validated
// customerId before the lookup (the natural, and wrong, symmetric choice
// matching status) would turn this into a 400 and this test would catch it.
func TestPatchConversation_BadCustomerIdTypeAgainstMissingConversationIs404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")

	r := c.Do(http.MethodPatch, "/api/v1/communications/conversations/"+uuid.NewString(), map[string]any{"customerId": "not-an-integer"})
	if r.Status != http.StatusNotFound {
		t.Fatalf("status %d body %s, want 404 (customerId validation follows the lookup)", r.Status, r.Body)
	}
	if len(r.Body) != 0 {
		t.Errorf("body = %s, want empty (bare 404)", r.Body)
	}
}

// TestPatchConversation_BadCustomerIdTypeAgainstExistingConversationIs400
// is the same bad value against a conversation that DOES exist: now the
// customerId check is reached and answers its own 400 shape — a DIFFERENT
// code path than the status check's 400 above, even though both happen to
// be flat CommunicationErrorResponse bodies.
func TestPatchConversation_BadCustomerIdTypeAgainstExistingConversationIs400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))
	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")

	r := c.Do(http.MethodPatch, "/api/v1/communications/conversations/"+m.ConversationId, map[string]any{"customerId": "not-an-integer"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	if body.Error.Code != "invalid_request" || body.Error.Message != "CustomerId must be an integer or null." {
		t.Errorf("error = %+v, want invalid_request / %q", body.Error, "CustomerId must be an integer or null.")
	}
}

// TestPatchConversation_InvalidManualCustomerUpdateDoesNotMutateExisting is
// ported from TS/Integration/CommunicationsEndpointsTests.cs:41-69
// (Invalid_manual_customer_update_is_rejected_without_mutating_existing_association).
func TestPatchConversation_InvalidManualCustomerUpdateDoesNotMutateExisting(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	chID := setupChannel(t, h)
	convID := uuid.New()
	manual := "manual"
	h.Exec(t, `INSERT INTO communications.conversations (id, channel_id, status, customer_id, customer_association_source, last_activity_at, created_at)
	           VALUES ($1, $2, 'open', 7, $3, $4, $4)`, convID, uuid.MustParse(chID), manual, h.Now())

	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")
	r := c.Do(http.MethodPatch, "/api/v1/communications/conversations/"+convID.String(), map[string]any{"customerId": 999999})
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422", r.Status, r.Body)
	}

	persistedCustomerID := modtest.One[int32](t, h, `SELECT customer_id FROM communications.conversations WHERE id = $1`, convID)
	if persistedCustomerID != 7 {
		t.Errorf("customer_id = %d, want unchanged 7", persistedCustomerID)
	}
	persistedSource := modtest.One[string](t, h, `SELECT customer_association_source FROM communications.conversations WHERE id = $1`, convID)
	if persistedSource != "manual" {
		t.Errorf("customer_association_source = %q, want unchanged manual", persistedSource)
	}
}

// TestPatchConversation_CustomerIdNullClearsCandidatesAndSource is ported
// from TS/Integration/CommunicationsEndpointsTests.cs:71-102
// (Manual_customer_clear_removes_candidate_ids_and_association_source_from_responses),
// and doubles as the customerId-omitted-vs-null tri-state pin (task 5's
// PATCH-body raw-JSON capture exists for exactly this): a request that
// sends `{"customerId":null}` clears everything; a request that omits the
// key entirely (proved by the "before" GET in this same test) must not.
func TestPatchConversation_CustomerIdNullClearsCandidatesAndSource(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	chID := setupChannel(t, h)
	convID := uuid.New()
	automatic := "automatic"
	h.Exec(t, `INSERT INTO communications.conversations (id, channel_id, status, customer_id, customer_association_source, last_activity_at, created_at)
	           VALUES ($1, $2, 'open', 7, $3, $4, $4)`, convID, uuid.MustParse(chID), automatic, h.Now())
	h.Exec(t, `INSERT INTO communications.conversation_customer_candidates (conversation_id, customer_id, created_at) VALUES ($1, 8, $2), ($1, 9, $2)`, convID, h.Now())

	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")

	r := c.Do(http.MethodPatch, "/api/v1/communications/conversations/"+convID.String(), map[string]any{"customerId": nil})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}

	got := c.Do(http.MethodGet, "/api/v1/communications/conversations/"+convID.String(), nil)
	var detail conversationDetailJSON
	got.JSON(&detail)
	if detail.CustomerId != nil {
		t.Errorf("customerId = %v, want nil", detail.CustomerId)
	}
	if detail.CustomerAssociationSource != nil {
		t.Errorf("customerAssociationSource = %v, want nil", detail.CustomerAssociationSource)
	}
	if len(detail.CandidateCustomerIds) != 0 {
		t.Errorf("candidateCustomerIds = %v, want empty", detail.CandidateCustomerIds)
	}
}

// TestPatchConversation_OmittingCustomerIdLeavesItUntouched is the
// tri-state's other side: a patch that never mentions customerId must not
// clear it — the bug this test would catch is collapsing "absent" and
// "null" into the same behaviour, which withRawPatchBody (module.go) exists
// specifically to avoid.
func TestPatchConversation_OmittingCustomerIdLeavesItUntouched(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	chID := setupChannel(t, h)
	convID := uuid.New()
	manual := "manual"
	h.Exec(t, `INSERT INTO communications.conversations (id, channel_id, status, customer_id, customer_association_source, last_activity_at, created_at)
	           VALUES ($1, $2, 'open', 7, $3, $4, $4)`, convID, uuid.MustParse(chID), manual, h.Now())

	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")
	r := c.Do(http.MethodPatch, "/api/v1/communications/conversations/"+convID.String(), map[string]any{"status": "closed"})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var patched conversationPatchJSON
	r.JSON(&patched)
	if patched.CustomerId == nil || *patched.CustomerId != 7 {
		t.Errorf("customerId = %v, want unchanged 7", patched.CustomerId)
	}
	if patched.CustomerAssociationSource == nil || *patched.CustomerAssociationSource != "manual" {
		t.Errorf("customerAssociationSource = %v, want unchanged manual", patched.CustomerAssociationSource)
	}
	if patched.Status != "closed" {
		t.Errorf("status = %q, want closed", patched.Status)
	}
}

// TestPatchConversation_AssignedUserIdNullClearsIt pins the analogous
// tri-state for assignedUserId.
func TestPatchConversation_AssignedUserIdNullClearsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))
	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")

	assigned := uuid.NewString()
	set := c.Do(http.MethodPatch, "/api/v1/communications/conversations/"+m.ConversationId, map[string]any{"assignedUserId": assigned})
	if set.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", set.Status, set.Body)
	}
	var setBody conversationPatchJSON
	set.JSON(&setBody)
	if setBody.AssignedUserId == nil || *setBody.AssignedUserId != assigned {
		t.Fatalf("assignedUserId = %v, want %s", setBody.AssignedUserId, assigned)
	}

	cleared := c.Do(http.MethodPatch, "/api/v1/communications/conversations/"+m.ConversationId, map[string]any{"assignedUserId": nil})
	if cleared.Status != http.StatusOK {
		t.Fatalf("clear: status %d body %s, want 200", cleared.Status, cleared.Body)
	}
	var clearedBody conversationPatchJSON
	cleared.JSON(&clearedBody)
	if clearedBody.AssignedUserId != nil {
		t.Errorf("assignedUserId = %v, want nil after clearing", clearedBody.AssignedUserId)
	}
}

// TestPatchConversation_AssignedUserIdIsUnvalidatedJSON pins task 5
// dispatch point 7: assignedUserId runs through no type check at all — a
// wrong-typed value (here, a JSON number) is read exactly as unsafely as a
// malformed GUID string, and both surface as a 500 that never echoes the
// underlying error, never a 400. This is deliberate fidelity to .NET's
// `assigned.GetGuid()`, not a gap to hardened away.
func TestPatchConversation_AssignedUserIdIsUnvalidatedJSON(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))
	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")

	cases := []struct {
		name string
		body map[string]any
	}{
		{"malformed guid string", map[string]any{"assignedUserId": "not-a-guid"}},
		{"wrong json type", map[string]any{"assignedUserId": 12345}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := c.Do(http.MethodPatch, "/api/v1/communications/conversations/"+m.ConversationId, tc.body,
				modtest.SkipContract("deliberately unvalidated per .NET's assigned.GetGuid(); the 500 is expected, not documented as a contract response"))
			if r.Status != http.StatusInternalServerError {
				t.Fatalf("status %d body %s, want 500", r.Status, r.Body)
			}
			if strings.Contains(string(r.Body), "not-a-guid") || strings.Contains(string(r.Body), "12345") {
				t.Errorf("body echoes the input: %s", r.Body)
			}
		})
	}
}

// TestPatchConversation_ManageAndViewBothRequired pins the plain (non-
// asymmetric) pairing task 5 dispatch point 6 names for PATCH: manage+view,
// unlike the tag-link routes on the same conversation resource.
func TestPatchConversation_ManageAndViewBothRequired(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))

	body := map[string]any{"status": "closed"}
	path := "/api/v1/communications/conversations/" + m.ConversationId
	if r := h.SignIn(t, "communications:conversations-manage").Do(http.MethodPatch, path, body); r.Status != http.StatusForbidden {
		t.Errorf("manage only: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t, "communications:conversations-view").Do(http.MethodPatch, path, body); r.Status != http.StatusForbidden {
		t.Errorf("view only: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view").Do(http.MethodPatch, path, body); r.Status != http.StatusOK {
		t.Errorf("manage+view: status %d, want 200", r.Status)
	}
}

// ---- PostCommunicationsConversationsByIdNotes ----

// TestAddNote_ValidationRunsBeforeExistence pins AddNote's ordering
// (inventory §2): a blank textBody against a nonexistent conversation is
// 400, not 404.
func TestAddNote_ValidationRunsBeforeExistence(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")

	r := c.Do(http.MethodPost, "/api/v1/communications/conversations/"+uuid.NewString()+"/notes", map[string]any{"textBody": "   "})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400 (validation precedes existence)", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	if got := body.field("textBody"); len(got) != 1 || got[0] != "TextBody is required." {
		t.Errorf("fields[textBody] = %v, want the required message", got)
	}
}

func TestAddNote_NotFoundAfterValidation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")

	r := c.Do(http.MethodPost, "/api/v1/communications/conversations/"+uuid.NewString()+"/notes", map[string]any{"textBody": "hello"})
	if r.Status != http.StatusNotFound {
		t.Fatalf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestAddNote_StatusIsCreatedNotQueued pins task 5 dispatch point 5: the
// mutation response's status string is exactly "created", the one
// message-producing endpoint in this module that never says "queued".
func TestAddNote_StatusIsCreatedNotQueued(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))
	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")

	r := c.Do(http.MethodPost, "/api/v1/communications/conversations/"+m.ConversationId+"/notes", map[string]any{"textBody": "an internal note"})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var body conversationMutationJSON
	r.JSON(&body)
	if body.Status != "created" {
		t.Errorf("status = %q, want exactly %q, not %q", body.Status, "created", "queued")
	}
	if body.IdempotencyKey != nil {
		t.Errorf("idempotencyKey = %v, want nil (notes carry no idempotency key)", body.IdempotencyKey)
	}
}

func TestAddNote_ManageAndViewBothRequired(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))

	path := "/api/v1/communications/conversations/" + m.ConversationId + "/notes"
	body := map[string]any{"textBody": "note"}
	if r := h.SignIn(t, "communications:conversations-manage").Do(http.MethodPost, path, body); r.Status != http.StatusForbidden {
		t.Errorf("manage only: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t, "communications:conversations-view").Do(http.MethodPost, path, body); r.Status != http.StatusForbidden {
		t.Errorf("view only: status %d, want 403", r.Status)
	}
}

// ---- PostCommunicationsConversationsByIdRead ----

// TestMarkRead_200EmptyBody pins task 5 dispatch point 3: a 200 with a
// genuinely empty body.
func TestMarkRead_200EmptyBody(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))
	c := h.SignIn(t, "communications:conversations-view")

	r := c.Do(http.MethodPost, "/api/v1/communications/conversations/"+m.ConversationId+"/read", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	if len(r.Body) != 0 {
		t.Errorf("body = %q, want empty", r.Body)
	}
}

func TestMarkRead_NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view")
	r := c.Do(http.MethodPost, "/api/v1/communications/conversations/"+uuid.NewString()+"/read", nil)
	if r.Status != http.StatusNotFound {
		t.Fatalf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestMarkRead_MakesTheConversationNotUnread proves the upsert actually
// takes effect: after marking read, the conversation stops appearing with
// unreadOnly=true.
func TestMarkRead_MakesTheConversationNotUnread(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))
	c := h.SignIn(t, "communications:conversations-view")

	before := c.Do(http.MethodGet, "/api/v1/communications/conversations?unreadOnly=true", nil)
	var beforePage paginatedConversationsJSON
	before.JSON(&beforePage)
	foundBefore := false
	for _, item := range beforePage.Data {
		if item.Id == m.ConversationId {
			foundBefore = true
		}
	}
	if !foundBefore {
		t.Fatalf("conversation %s should be unread before marking read", m.ConversationId)
	}

	if r := c.Do(http.MethodPost, "/api/v1/communications/conversations/"+m.ConversationId+"/read", nil); r.Status != http.StatusOK {
		t.Fatalf("mark read: status %d body %s, want 200", r.Status, r.Body)
	}

	after := c.Do(http.MethodGet, "/api/v1/communications/conversations?unreadOnly=true", nil)
	var afterPage paginatedConversationsJSON
	after.JSON(&afterPage)
	for _, item := range afterPage.Data {
		if item.Id == m.ConversationId {
			t.Errorf("conversation %s still listed as unread after marking read", m.ConversationId)
		}
	}
}
