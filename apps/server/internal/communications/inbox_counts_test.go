package communications_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The inbox counts (messenger design N27, delivery E): the caller's unread open
// conversations, and how many of them are assigned to the caller.

type inboxCountsJSON struct {
	Unread             int `json:"unread"`
	AssignedToMeUnread int `json:"assignedToMeUnread"`
}

func getInboxCounts(t *testing.T, c *modtest.Client) inboxCountsJSON {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/communications/inbox/counts", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("inbox counts: status %d body %s, want 200", r.Status, r.Body)
	}
	var counts inboxCountsJSON
	r.JSON(&counts)
	return counts
}

func TestInboxCounts_CountsOpenUnreadAndTheCallersOwn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	sender := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	c, me := h.SignInUser(t, "communications:conversations-view")

	if got := getInboxCounts(t, c); got != (inboxCountsJSON{}) {
		t.Fatalf("empty inbox: counts %+v, want zeros", got)
	}

	mine := createConversation(t, sender, newConversationBody("a@example.test"))
	h.Advance(time.Minute)
	createConversation(t, sender, newConversationBody("b@example.test"))
	h.Advance(time.Minute)
	closed := createConversation(t, sender, newConversationBody("c@example.test"))
	h.Exec(t, `UPDATE communications.conversations SET assigned_user_id = $1 WHERE id = $2`, me, mine.ConversationId)
	h.Exec(t, `UPDATE communications.conversations SET status = 'closed', assigned_user_id = $1 WHERE id = $2`, me, closed.ConversationId)

	// Two open conversations are unread, one of them mine; the closed one is not counted.
	if got, want := getInboxCounts(t, c), (inboxCountsJSON{Unread: 2, AssignedToMeUnread: 1}); got != want {
		t.Fatalf("counts %+v, want %+v", got, want)
	}

	// Reading mine takes it out of both counts; another person's counts are their own.
	if r := c.Do(http.MethodPost, "/api/v1/communications/conversations/"+mine.ConversationId+"/read", nil); r.Status != http.StatusOK {
		t.Fatalf("mark read: status %d body %s", r.Status, r.Body)
	}
	if got, want := getInboxCounts(t, c), (inboxCountsJSON{Unread: 1, AssignedToMeUnread: 0}); got != want {
		t.Fatalf("after reading: counts %+v, want %+v", got, want)
	}
	if got, want := getInboxCounts(t, sender), (inboxCountsJSON{Unread: 2, AssignedToMeUnread: 0}); got != want {
		t.Fatalf("another caller: counts %+v, want %+v (their own read state, nothing assigned to them)", got, want)
	}

	// New activity after the read makes it unread again.
	h.Advance(time.Minute)
	h.Exec(t, `UPDATE communications.conversations SET last_activity_at = $1 WHERE id = $2`, h.Now(), mine.ConversationId)
	if got, want := getInboxCounts(t, c), (inboxCountsJSON{Unread: 2, AssignedToMeUnread: 1}); got != want {
		t.Fatalf("after new activity: counts %+v, want %+v", got, want)
	}
}

func TestInboxCounts_RequiresView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if r := h.SignIn(t, "communications:conversations-reply").Do(http.MethodGet, "/api/v1/communications/inbox/counts", nil); r.Status != http.StatusForbidden {
		t.Errorf("without view: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t, "communications:conversations-view").Do(http.MethodGet, "/api/v1/communications/inbox/counts", nil); r.Status != http.StatusOK {
		t.Errorf("with view: status %d, want 200", r.Status)
	}
}
