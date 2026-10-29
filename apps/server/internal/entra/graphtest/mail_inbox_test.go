package graphtest_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
)

// inboxGet sends GET path with prefer as its Prefer header and answers the
// status, the header and the body as it came.
func (c *client) inboxGet(path, prefer string) (int, http.Header, []byte) {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		c.t.Fatal(err)
	}
	if !strings.HasPrefix(path, "https://") {
		req.URL, _ = url.Parse("https://graph.microsoft.com/v1.0" + path)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if prefer != "" {
		req.Header.Set("Prefer", prefer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

// inboxPage is a delta page decoded.
type inboxPage struct {
	Value     []map[string]any `json:"value"`
	NextLink  string           `json:"@odata.nextLink"`
	DeltaLink string           `json:"@odata.deltaLink"`
}

func (c *client) inboxDeltaPage(path, prefer string) (int, inboxPage) {
	c.t.Helper()
	status, _, b := c.inboxGet(path, prefer)
	var p inboxPage
	if status == http.StatusOK {
		if err := json.Unmarshal(b, &p); err != nil {
			c.t.Fatalf("decode %s: %v", b, err)
		}
	}
	return status, p
}

func inboxMIME(subject string) []byte {
	return []byte("From: kunde@example.com\r\nTo: post@nordlys.no\r\nSubject: " + subject + "\r\nMessage-Id: <" + subject + "@example.com>\r\n\r\nHei.\r\n")
}

// A fresh delta answers the Inbox narrowed by $filter, $select's fields
// and id alone, in pages Prefer sizes, the links naming mailfolders/{the
// Inbox's id} in lower case; its delta link then answers the changes
// whatever the filter — a message added, one marked read, one removed
// (@removed). An unsupported $filter is 400.
func TestMailInbox_Delta(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	post := g.AddUser(graphtest.User{UserPrincipalName: "post@nordlys.no", Shared: true})
	c := newClient(t, g)
	since := time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)
	old := g.AddInboxMessage(post.ID, graphtest.InboxMessage{ReceivedAt: since.Add(-time.Hour), MIME: inboxMIME("old")})
	var ids []string
	for i := range 3 {
		ids = append(ids, g.AddInboxMessage(post.ID, graphtest.InboxMessage{
			ReceivedAt: since.Add(time.Duration(i) * time.Minute), MIME: inboxMIME("m" + string(rune('a'+i))),
			InternetMessageID: "<m@example.com>", ConversationID: "AAQkconv",
		}))
	}
	q := url.Values{"$filter": {"receivedDateTime ge " + since.Format(time.RFC3339)}, "$select": {"id,receivedDateTime,internetMessageId,conversationId"}}
	path := "/users/" + post.ID + "/mailFolders/inbox/messages/delta?" + strings.ReplaceAll(q.Encode(), "+", "%20")
	prefer := `IdType="ImmutableId", odata.maxpagesize=2`

	status, p1 := c.inboxDeltaPage(path, prefer)
	if status != http.StatusOK || len(p1.Value) != 2 || p1.NextLink == "" || p1.DeltaLink != "" {
		t.Fatalf("the first page: %d %+v", status, p1)
	}
	if !strings.Contains(p1.NextLink, "/v1.0/users/"+post.ID+"/mailfolders/AQMkADinbox") || !strings.Contains(p1.NextLink, "$skiptoken=") {
		t.Errorf("the next link: %s", p1.NextLink)
	}
	first := p1.Value[0]
	if first["id"] != ids[0] || first["receivedDateTime"] != since.Format(time.RFC3339) || first["internetMessageId"] != "<m@example.com>" ||
		first["conversationId"] != "AAQkconv" || first["subject"] != nil || first["isRead"] != nil {
		t.Errorf("a message selected: %v", first)
	}
	status, p2 := c.inboxDeltaPage(p1.NextLink, prefer)
	if status != http.StatusOK || len(p2.Value) != 1 || p2.Value[0]["id"] != ids[2] || p2.DeltaLink == "" || p2.NextLink != "" {
		t.Fatalf("the last page: %d %+v", status, p2)
	}
	if calls := g.Calls(); calls[len(calls)-1].Prefer != prefer {
		t.Errorf("the Prefer recorded: %q", calls[len(calls)-1].Prefer)
	}

	// Changes since: one new (before the filter's instant — the filter no
	// longer applies), one read, one removed.
	late := g.AddInboxMessage(post.ID, graphtest.InboxMessage{ReceivedAt: since.Add(-2 * time.Hour), MIME: inboxMIME("late")})
	g.MarkInboxMessageRead(post.ID, ids[0])
	g.RemoveInboxMessage(post.ID, ids[1])
	status, p3 := c.inboxDeltaPage(p2.DeltaLink, `odata.maxpagesize=50`)
	if status != http.StatusOK || len(p3.Value) != 3 || p3.DeltaLink == "" {
		t.Fatalf("the changes: %d %+v", status, p3)
	}
	if p3.Value[0]["id"] != late || p3.Value[1]["id"] != ids[0] || p3.Value[2]["id"] != ids[1] || p3.Value[2]["@removed"] == nil {
		t.Errorf("the changes: %+v", p3.Value)
	}
	status, p4 := c.inboxDeltaPage(p3.DeltaLink, "")
	if status != http.StatusOK || len(p4.Value) != 0 || p4.DeltaLink == "" {
		t.Errorf("nothing changed: %d %+v", status, p4)
	}

	// No filter: every message, the old one too, whole.
	status, all := c.inboxDeltaPage("/users/"+post.ID+"/mailFolders/inbox/messages/delta", "odata.maxpagesize=50")
	if status != http.StatusOK || len(all.Value) != 4 || all.Value[0]["id"] != old || all.Value[0]["subject"] != "old" {
		t.Errorf("unfiltered: %d %+v", status, all)
	}

	bad := "/users/" + post.ID + "/mailFolders/inbox/messages/delta?" + url.Values{"$filter": {"subject eq 'x'"}}.Encode()
	if status, _, b := c.inboxGet(bad, ""); status != http.StatusBadRequest {
		t.Errorf("an unsupported filter: %d %s", status, b)
	}
}

// ExpireMailDelta answers every token the status with syncStateNotFound,
// until a fresh query; a token the fake did not issue is 400.
func TestMailInbox_DeltaExpired(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	post := g.AddUser(graphtest.User{UserPrincipalName: "post@nordlys.no", Shared: true})
	c := newClient(t, g)
	fresh := "/users/" + post.ID + "/mailFolders/inbox/messages/delta"
	_, p := c.inboxDeltaPage(fresh, "")
	if p.DeltaLink == "" {
		t.Fatal("no delta link")
	}
	g.ExpireMailDelta(post.ID, http.StatusGone)
	status, _, b := c.inboxGet(p.DeltaLink, "")
	if status != http.StatusGone || !bytes.Contains(b, []byte("syncStateNotFound")) {
		t.Errorf("expired: %d %s", status, b)
	}
	if status, _ := c.inboxDeltaPage(fresh, ""); status != http.StatusOK {
		t.Errorf("a fresh query: %d", status)
	}
	if status, _ := c.inboxDeltaPage(p.DeltaLink, ""); status != http.StatusOK {
		t.Errorf("after a fresh query: %d", status)
	}
	if status, _, b := c.inboxGet(fresh+"?%24deltatoken=nope", ""); status != http.StatusBadRequest || !bytes.Contains(b, []byte("syncStateNotFound")) {
		t.Errorf("a token never issued: %d %s", status, b)
	}
}

// $value answers the MIME byte for byte as message/rfc822; a message
// removed is 404 ErrorItemNotFound; a mailbox denied 403 and one with no
// mailbox 404 on both routes.
func TestMailInbox_Value(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	post := g.AddUser(graphtest.User{UserPrincipalName: "post@nordlys.no", Shared: true})
	c := newClient(t, g)
	mime := append(inboxMIME("hei"), 0xff, 0x00, '\r', '\n')
	id := g.AddInboxMessage(post.ID, graphtest.InboxMessage{ReceivedAt: now, MIME: mime})
	value := "/users/" + post.ID + "/messages/" + id + "/$value"

	status, h, b := c.inboxGet(value, `IdType="ImmutableId"`)
	if status != http.StatusOK || !bytes.Equal(b, mime) || h.Get("Content-Type") != "message/rfc822" {
		t.Errorf("$value: %d %s %q", status, h.Get("Content-Type"), b)
	}
	g.RemoveInboxMessage(post.ID, id)
	if status, _, b := c.inboxGet(value, ""); status != http.StatusNotFound || !bytes.Contains(b, []byte("ErrorItemNotFound")) {
		t.Errorf("removed: %d %s", status, b)
	}
	g.EditUser(post.ID, func(u *graphtest.User) { u.MailboxDenied = true })
	if status, _, _ := c.inboxGet(value, ""); status != http.StatusForbidden {
		t.Errorf("denied $value: %d", status)
	}
	if status, _, _ := c.inboxGet("/users/"+post.ID+"/mailFolders/inbox/messages/delta", ""); status != http.StatusForbidden {
		t.Errorf("denied delta: %d", status)
	}
	g.EditUser(post.ID, func(u *graphtest.User) { u.MailboxDenied, u.NoMailbox = false, true })
	if status, _, b := c.inboxGet("/users/"+post.ID+"/mailFolders/inbox/messages/delta", ""); status != http.StatusNotFound || !bytes.Contains(b, []byte("MailboxNotEnabledForRESTAPI")) {
		t.Errorf("no mailbox: %d %s", status, b)
	}
}

func TestMailInbox_FoldersAndMessageMarks(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	post := g.AddUser(graphtest.User{UserPrincipalName: "post@nordlys.no", Shared: true})
	c := newClient(t, g)
	id := g.AddInboxMessage(post.ID, graphtest.InboxMessage{ReceivedAt: now, MIME: inboxMIME("sak")})

	// Create folder, 409 on duplicate, filter by displayName.
	status, _, folder := c.do(http.MethodPost, "/users/"+post.ID+"/mailFolders", map[string]any{"displayName": "Handled in Vantigo"})
	folderID, _ := folder["id"].(string)
	if status != http.StatusCreated || folderID == "" {
		t.Fatalf("create folder: %d %v", status, folder)
	}
	if status, _, body := c.do(http.MethodPost, "/users/"+post.ID+"/mailFolders", map[string]any{"displayName": "handled in vantigo"}); status != http.StatusConflict || errorCode(body) != "ErrorFolderExists" {
		t.Errorf("duplicate folder: %d %v, want 409 ErrorFolderExists", status, body)
	}
	q := url.Values{"$filter": {"displayName eq 'Handled in Vantigo'"}}
	status, _, list := c.do(http.MethodGet, "/users/"+post.ID+"/mailFolders?"+strings.ReplaceAll(q.Encode(), "+", "%20"), nil)
	if status != http.StatusOK || len(list["value"].([]any)) != 1 {
		t.Errorf("filter folder: %d %v", status, list)
	}

	// PATCH isRead + categories, then POST move.
	if status, _, body := c.do(http.MethodPatch, "/users/"+post.ID+"/messages/"+id, map[string]any{"isRead": true, "categories": []string{"Handled"}}); status != http.StatusOK || body["isRead"] != true {
		t.Errorf("patch message: %d %v", status, body)
	}
	if status, _, body := c.do(http.MethodPost, "/users/"+post.ID+"/messages/"+id+"/move", map[string]any{"destinationId": folderID}); status != http.StatusCreated || body["id"] != id {
		t.Errorf("move message: %d %v", status, body)
	}
	st, ok := g.MessageState(post.ID, id)
	if !ok || !st.IsRead || len(st.Categories) != 1 || st.Categories[0] != "Handled" || st.FolderID != folderID || st.FolderName != "Handled in Vantigo" {
		t.Errorf("MessageState = %+v, %v", st, ok)
	}
	// After moving out of Inbox, $value from Inbox is 404, while a repeat move by immutable ID still succeeds.
	if status, _, _ := c.inboxGet("/users/"+post.ID+"/messages/"+id+"/$value", ""); status != http.StatusNotFound {
		t.Errorf("$value after move: %d, want 404", status)
	}
	if status, _, _ := c.do(http.MethodPost, "/users/"+post.ID+"/messages/"+id+"/move", map[string]any{"destinationId": folderID}); status != http.StatusCreated {
		t.Errorf("repeat move: %d, want 201", status)
	}
}
