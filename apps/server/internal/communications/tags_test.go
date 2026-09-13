package communications_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the Tags area's tests (EP/TagEndpoints.cs, communications
// inventory §1.4, §2's CreateTag/AddTag/RemoveTag bullets; task 5's
// dispatch point 6 is the authority for the tag-link permission pairing)
// for getCommunicationsTags, postCommunicationsTags,
// putCommunicationsConversationsByIdTagsByTagId and
// deleteCommunicationsConversationsByIdTagsByTagId.

type tagResponseJSON struct {
	Id    string  `json:"id"`
	Name  string  `json:"name"`
	Color *string `json:"color"`
}

// createTag posts body with c and fails t unless the response is 201.
func createTag(t *testing.T, c *modtest.Client, body map[string]any) tagResponseJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/communications/tags", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create tag: status %d body %s, want 201", r.Status, r.Body)
	}
	var tag tagResponseJSON
	r.JSON(&tag)
	return tag
}

// ---- GetCommunicationsTags ----

func TestListTags_EmptyBareArray(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view")

	r := c.Do(http.MethodGet, "/api/v1/communications/tags", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var tags []tagResponseJSON
	r.JSON(&tags)
	if len(tags) != 0 {
		t.Errorf("tags = %v, want none", tags)
	}
}

// TestListTags_OrderedByNameAscending pins inventory §1.4: ordered by Name
// ascending.
func TestListTags_OrderedByNameAscending(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view", "communications:conversations-manage")

	zebra := createTag(t, c, map[string]any{"name": "zebra-" + uuid.NewString()})
	apple := createTag(t, c, map[string]any{"name": "apple-" + uuid.NewString()})

	r := c.Do(http.MethodGet, "/api/v1/communications/tags", nil)
	var tags []tagResponseJSON
	r.JSON(&tags)
	var appleIdx, zebraIdx = -1, -1
	for i, tg := range tags {
		if tg.Id == apple.Id {
			appleIdx = i
		}
		if tg.Id == zebra.Id {
			zebraIdx = i
		}
	}
	if appleIdx == -1 || zebraIdx == -1 {
		t.Fatalf("both tags must be present: apple at %d, zebra at %d", appleIdx, zebraIdx)
	}
	if appleIdx > zebraIdx {
		t.Errorf("apple at %d, zebra at %d, want apple before zebra (Name ascending)", appleIdx, zebraIdx)
	}
}

func TestListTags_RequiresView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if r := h.SignIn(t).Do(http.MethodGet, "/api/v1/communications/tags", nil); r.Status != http.StatusForbidden {
		t.Errorf("no permission: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t, "communications:conversations-view").Do(http.MethodGet, "/api/v1/communications/tags", nil); r.Status != http.StatusOK {
		t.Errorf("conversations-view: status %d, want 200", r.Status)
	}
}

// ---- PostCommunicationsTags ----

// TestCreateTag_BlankNameIsFlat400 pins inventory §1.4/§2: CreateTag's 400
// is flat (no `fields`), unlike ValidateConversation's field-keyed 400s.
func TestCreateTag_BlankNameIsFlat400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-manage")

	r := c.Do(http.MethodPost, "/api/v1/communications/tags", map[string]any{"name": "   "})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	if body.Error.Code != "invalid_request" || body.Error.Message != "A tag name is required." {
		t.Errorf("error = %+v, want invalid_request / %q", body.Error, "A tag name is required.")
	}
	if len(body.Error.Fields) != 0 {
		t.Errorf("fields = %v, want none (flat error)", body.Error.Fields)
	}
}

// TestCreateTag_DuplicateNameIs409 pins the unique-name conflict.
func TestCreateTag_DuplicateNameIs409(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-manage")
	name := "dup-" + uuid.NewString()
	createTag(t, c, map[string]any{"name": name})

	r := c.Do(http.MethodPost, "/api/v1/communications/tags", map[string]any{"name": name})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	if body.Error.Code != "tag_exists" {
		t.Errorf("code = %q, want tag_exists", body.Error.Code)
	}
}

// TestCreateTag_RequiresManageAlone pins the plain (non-asymmetric)
// conversations-manage-only pairing for POST /tags — the "alone" here
// contrasts with the manage+view pairing notes and PATCH need, not with an
// asymmetry of POST /tags' own; conversations-view alone must not suffice.
func TestCreateTag_RequiresManageAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := map[string]any{"name": "perm-" + uuid.NewString()}
	if r := h.SignIn(t, "communications:conversations-view").Do(http.MethodPost, "/api/v1/communications/tags", body); r.Status != http.StatusForbidden {
		t.Errorf("view only: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t, "communications:conversations-manage").Do(http.MethodPost, "/api/v1/communications/tags", body); r.Status != http.StatusCreated {
		t.Errorf("manage: status %d, want 201", r.Status)
	}
}

// ---- Tag links: PUT/DELETE /conversations/{id}/tags/{tagId} ----

// tagLinkFixture seeds one channel, one conversation and one tag through an
// admin client holding every relevant permission, so the fixture itself
// never depends on the permission under test in the tests below.
func tagLinkFixture(t *testing.T, h *modtest.Harness) (conversationID, tagID string) {
	t.Helper()
	admin := h.SignIn(t, "communications:channels-manage", "communications:conversations-reply",
		"communications:conversations-view", "communications:conversations-manage")
	ch := createChannel(t, admin, newChannelBody(channelAddress(t)))
	conv := createConversation(t, admin, newConversationBody("x@example.test"))
	_ = ch
	tag := createTag(t, admin, map[string]any{"name": "link-" + uuid.NewString()})
	return conv.ConversationId, tag.Id
}

// TestAddTagLink_IdempotentPut204Twice pins inventory §1.4: adding an
// already-present tag is 204, not an error — a repeated PUT on the same
// pair must behave identically both times.
func TestAddTagLink_IdempotentPut204Twice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	convID, tagID := tagLinkFixture(t, h)
	c := h.SignIn(t, "communications:conversations-manage")
	path := "/api/v1/communications/conversations/" + convID + "/tags/" + tagID

	first := c.Do(http.MethodPut, path, nil)
	if first.Status != http.StatusNoContent {
		t.Fatalf("first: status %d body %s, want 204", first.Status, first.Body)
	}
	second := c.Do(http.MethodPut, path, nil)
	if second.Status != http.StatusNoContent {
		t.Fatalf("second: status %d body %s, want 204 (idempotent)", second.Status, second.Body)
	}
}

func TestAddTagLink_NotFoundWhenConversationOrTagMissing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, tagID := tagLinkFixture(t, h)
	convID, _ := tagLinkFixture(t, h)
	c := h.SignIn(t, "communications:conversations-manage")

	if r := c.Do(http.MethodPut, "/api/v1/communications/conversations/"+uuid.NewString()+"/tags/"+tagID, nil); r.Status != http.StatusNotFound {
		t.Errorf("missing conversation: status %d, want 404", r.Status)
	}
	if r := c.Do(http.MethodPut, "/api/v1/communications/conversations/"+convID+"/tags/"+uuid.NewString(), nil); r.Status != http.StatusNotFound {
		t.Errorf("missing tag: status %d, want 404", r.Status)
	}
}

// TestAddTagLink_ManageAloneSuffices pins task 5 dispatch point 6: the
// tag-link routes require communications:conversations-manage ALONE,
// without conversations-view — unlike notes/PATCH on the same conversation
// resource. A client holding only manage (deliberately no view) must
// succeed; if +view were ever added here, this client would be forbidden
// and this test would fail.
func TestAddTagLink_ManageAloneSuffices(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	convID, tagID := tagLinkFixture(t, h)
	c := h.SignIn(t, "communications:conversations-manage") // deliberately no +view

	r := c.Do(http.MethodPut, "/api/v1/communications/conversations/"+convID+"/tags/"+tagID, nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s, want 204 (conversations-manage alone must suffice)", r.Status, r.Body)
	}
}

// TestAddTagLink_ViewAloneIsForbidden is the mirror check: view alone must
// not suffice, since the contract requires manage.
func TestAddTagLink_ViewAloneIsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	convID, tagID := tagLinkFixture(t, h)
	c := h.SignIn(t, "communications:conversations-view")

	r := c.Do(http.MethodPut, "/api/v1/communications/conversations/"+convID+"/tags/"+tagID, nil)
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}

// TestRemoveTagLink_NotIdempotentSecondCallIs404 pins the PUT/DELETE
// idempotency asymmetry inventory §1.4 states explicitly: adding an
// already-present tag is 204, but removing an already-absent tag is 404 —
// the two are NOT mirror images of each other.
func TestRemoveTagLink_NotIdempotentSecondCallIs404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	convID, tagID := tagLinkFixture(t, h)
	c := h.SignIn(t, "communications:conversations-manage")
	path := "/api/v1/communications/conversations/" + convID + "/tags/" + tagID
	if r := c.Do(http.MethodPut, path, nil); r.Status != http.StatusNoContent {
		t.Fatalf("add: status %d body %s, want 204", r.Status, r.Body)
	}

	first := c.Do(http.MethodDelete, path, nil)
	if first.Status != http.StatusNoContent {
		t.Fatalf("first remove: status %d body %s, want 204", first.Status, first.Body)
	}
	second := c.Do(http.MethodDelete, path, nil)
	if second.Status != http.StatusNotFound {
		t.Fatalf("second remove: status %d body %s, want 404 (NOT idempotent)", second.Status, second.Body)
	}
}

// TestRemoveTagLink_ManageAloneSuffices is DELETE's half of task 5 dispatch
// point 6's tag-link pairing.
func TestRemoveTagLink_ManageAloneSuffices(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	convID, tagID := tagLinkFixture(t, h)
	admin := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")
	path := "/api/v1/communications/conversations/" + convID + "/tags/" + tagID
	if r := admin.Do(http.MethodPut, path, nil); r.Status != http.StatusNoContent {
		t.Fatalf("add: status %d body %s, want 204", r.Status, r.Body)
	}

	c := h.SignIn(t, "communications:conversations-manage") // deliberately no +view
	r := c.Do(http.MethodDelete, path, nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s, want 204 (conversations-manage alone must suffice)", r.Status, r.Body)
	}
}
