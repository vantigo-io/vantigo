package assistant_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

type conversationList struct {
	Items      []conversation `json:"items"`
	NextCursor string         `json:"nextCursor"`
}

func listConversations(t *testing.T, c *modtest.Client, query string) conversationList {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/assistant/conversations"+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list: %d %s", r.Status, r.Body)
	}
	var l conversationList
	r.JSON(&l)
	return l
}

func patch(t *testing.T, c *modtest.Client, id string, body map[string]any) conversation {
	t.Helper()
	r := c.Do(http.MethodPatch, "/api/v1/assistant/conversations/"+id, body)
	if r.Status != http.StatusOK {
		t.Fatalf("patch: %d %s", r.Status, r.Body)
	}
	var conv conversation
	r.JSON(&conv)
	return conv
}

func TestConversations_ListPinRenameArchiveDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	c, _ := person(t, h)

	a := startConversation(t, c)
	h.Advance(time.Minute)
	b := startConversation(t, c)
	h.Advance(time.Minute)
	d := startConversation(t, c)
	if a.OriginApp != "customers" || a.OriginRoute != "/customers" || a.Title != "" {
		t.Errorf("a new conversation = %+v, want its origin and no title", a)
	}
	// Newest activity first.
	if ids := idsOf(listConversations(t, c, "")); strings.Join(ids, ",") != strings.Join([]string{d.ID, b.ID, a.ID}, ",") {
		t.Errorf("order = %v, want newest first", ids)
	}
	// Pinned first.
	if !patch(t, c, a.ID, map[string]any{"pinned": true}).Pinned {
		t.Error("pin did not stick")
	}
	if ids := idsOf(listConversations(t, c, "")); ids[0] != a.ID {
		t.Errorf("order = %v, want the pinned one first", ids)
	}
	// Rename.
	if got := patch(t, c, b.ID, map[string]any{"title": "  Overdue invoices  "}); got.Title != "Overdue invoices" {
		t.Errorf("title = %q", got.Title)
	}
	if r := c.Do(http.MethodPatch, "/api/v1/assistant/conversations/"+b.ID, map[string]any{"title": "   "}); r.Status != http.StatusBadRequest {
		t.Errorf("a blank title = %d, want 400", r.Status)
	}
	// Search finds a title.
	if ids := idsOf(listConversations(t, c, "?q=overdue")); len(ids) != 1 || ids[0] != b.ID {
		t.Errorf("search = %v, want only the renamed one", ids)
	}
	// Archive moves it to the archived list and back.
	if !patch(t, c, d.ID, map[string]any{"archived": true}).Archived {
		t.Error("archive did not stick")
	}
	if ids := idsOf(listConversations(t, c, "")); contains(ids, d.ID) {
		t.Errorf("the live list holds the archived one: %v", ids)
	}
	if ids := idsOf(listConversations(t, c, "?archived=true")); len(ids) != 1 || ids[0] != d.ID {
		t.Errorf("archived list = %v", ids)
	}
	if patch(t, c, d.ID, map[string]any{"archived": false}).Archived {
		t.Error("unarchive did not stick")
	}
	// Delete, and the last open conversation goes with it.
	r := c.Do(http.MethodPut, "/api/v1/assistant/me", map[string]any{"lastConversationId": a.ID})
	if r.Status != http.StatusOK {
		t.Fatalf("put me: %d %s", r.Status, r.Body)
	}
	if r := c.Do(http.MethodDelete, "/api/v1/assistant/conversations/"+a.ID, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := c.Do(http.MethodGet, "/api/v1/assistant/conversations/"+a.ID, nil); r.Status != http.StatusNotFound {
		t.Errorf("a deleted conversation = %d, want 404", r.Status)
	}
	var me struct {
		LastConversationID *string `json:"lastConversationId"`
	}
	c.Do(http.MethodGet, "/api/v1/assistant/me", nil).JSON(&me)
	if me.LastConversationID != nil {
		t.Errorf("me = %v, want the deleted conversation forgotten", *me.LastConversationID)
	}
}

func TestConversations_TheLastOpenOneIsRememberedAndFoundBySearchOfItsWords(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel(modtest.ChatTurn{Text: "The tariff page lists them."}))
	c, _ := person(t, h)
	var me struct {
		LastConversationID *string `json:"lastConversationId"`
	}
	c.Do(http.MethodGet, "/api/v1/assistant/me", nil).JSON(&me)
	if me.LastConversationID != nil {
		t.Fatalf("a new person has a last conversation: %v", *me.LastConversationID)
	}
	conv := startConversation(t, c) // starting one makes it the last open one
	c.Do(http.MethodGet, "/api/v1/assistant/me", nil).JSON(&me)
	if me.LastConversationID == nil || *me.LastConversationID != conv.ID {
		t.Fatalf("me = %v, want the new conversation", me.LastConversationID)
	}
	ask(t, c, conv.ID, "Where are the prices?")
	// The conversation's words are searched, not only its title.
	if ids := idsOf(listConversations(t, c, "?q=tariff")); len(ids) != 1 {
		t.Errorf("search by an answer's words = %v, want the conversation", ids)
	}
	if ids := idsOf(listConversations(t, c, "?q=zzzz")); len(ids) != 0 {
		t.Errorf("search = %v, want none", ids)
	}
}

func TestConversations_PagesByCursor(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	c, _ := person(t, h)
	var made []string
	for range 5 {
		made = append(made, startConversation(t, c).ID)
		h.Advance(time.Minute)
	}
	first := listConversations(t, c, "?limit=2")
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v, want 2 and a cursor", first)
	}
	second := listConversations(t, c, "?limit=2&cursor="+first.NextCursor)
	third := listConversations(t, c, "?limit=2&cursor="+second.NextCursor)
	got := append(append(idsOf(first), idsOf(second)...), idsOf(third)...)
	want := []string{made[4], made[3], made[2], made[1], made[0]}
	if strings.Join(got, ",") != strings.Join(want, ",") || third.NextCursor != "" {
		t.Errorf("pages = %v (next %q), want %v and no more", got, third.NextCursor, want)
	}
	if r := c.Do(http.MethodGet, "/api/v1/assistant/conversations?cursor=garbage", nil); r.Status != http.StatusBadRequest {
		t.Errorf("a bad cursor = %d, want 400", r.Status)
	}
}

func TestConversations_BadOrigins(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	c, _ := person(t, h)
	for _, origin := range []map[string]any{{"app": "", "route": "/x"}, {"app": "customers", "route": "customers"}, {"app": "customers", "route": ""}} {
		if r := c.Do(http.MethodPost, "/api/v1/assistant/conversations", map[string]any{"origin": origin}); r.Status != http.StatusBadRequest {
			t.Errorf("origin %v = %d, want 400", origin, r.Status)
		}
	}
}

// Privacy (S9): one person can never read or touch another's conversation,
// message, run or action through any endpoint, and the answer is a 404.
func TestPrivacy_NoOneReachesAnotherPersonsConversation(t *testing.T) {
	t.Parallel()
	model := modtest.NewFakeChatModel(
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{{Name: "search_docs", Args: map[string]any{"query": "invoice"}}}},
		modtest.ChatTurn{Text: "Alice's private answer."},
	)
	h := newHarness(t, model)
	alice, _ := person(t, h)
	bob, _ := person(t, h)

	conv := startConversation(t, alice)
	ask(t, alice, conv.ID, "Alice's private question")
	runID := modtest.One[string](t, h, `SELECT id::text FROM assistant.runs`)
	actionRun := modtest.One[string](t, h, `SELECT run_id::text FROM assistant.actions LIMIT 1`)
	if actionRun != runID {
		t.Fatalf("the action belongs to run %s, not %s", actionRun, runID)
	}

	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"read it", http.MethodGet, "/api/v1/assistant/conversations/" + conv.ID, nil},
		{"rename it", http.MethodPatch, "/api/v1/assistant/conversations/" + conv.ID, map[string]any{"title": "Mine now"}},
		{"delete it", http.MethodDelete, "/api/v1/assistant/conversations/" + conv.ID, nil},
		{"post into it", http.MethodPost, "/api/v1/assistant/conversations/" + conv.ID + "/messages", map[string]any{"text": "hello"}},
		{"stop its run", http.MethodPost, "/api/v1/assistant/runs/" + runID + "/stop", nil},
		{"make it the last open one", http.MethodPut, "/api/v1/assistant/me", map[string]any{"lastConversationId": conv.ID}},
	} {
		r := bob.Do(tc.method, tc.path, tc.body)
		if r.Status != http.StatusNotFound {
			t.Errorf("bob tries to %s = %d %s, want 404", tc.name, r.Status, r.Body)
		}
		if strings.Contains(string(r.Body), "Alice") {
			t.Errorf("bob tries to %s and the answer leaks: %s", tc.name, r.Body)
		}
	}
	// Bob's list holds none of Alice's, whatever he searches for.
	for _, q := range []string{"", "?q=private", "?q=Alice", "?archived=true"} {
		if l := listConversations(t, bob, q); len(l.Items) != 0 {
			t.Errorf("bob's list%s = %+v, want none", q, l.Items)
		}
	}
	// Alice's conversation is intact, and her run was not stopped by him.
	d := getConversation(t, alice, conv.ID)
	if d.Conversation.Title != "Alice's private question" || len(d.Messages) != 4 || len(d.Actions) != 1 {
		t.Errorf("alice's conversation changed: %+v", d)
	}
	if stop := modtest.One[bool](t, h, `SELECT stop_requested FROM assistant.runs`); stop {
		t.Error("bob requested a stop on alice's run")
	}
}

func idsOf(l conversationList) []string {
	out := make([]string, len(l.Items))
	for i, c := range l.Items {
		out[i] = c.ID
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

var _ = fmt.Sprint
