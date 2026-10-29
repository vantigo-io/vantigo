package graphtest_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
)

// mime is a message as the outbox renders one, its Bcc header when bcc is
// not "".
func mime(bcc string) []byte {
	m := "From: post@nordlys.no\r\nTo: kunde@example.com\r\n"
	if bcc != "" {
		m += "Bcc: " + bcc + "\r\n"
	}
	return []byte(m + "Subject: Hei\r\nMessage-Id: <abc@vantigo.invalid>\r\n\r\nHello.\r\n")
}

// mailRequest sends body as it is, with contentType, and answers the
// status and the body decoded into a map (nil without one).
func (c *client) mailRequest(method, path, contentType string, body []byte) (int, map[string]any) {
	c.t.Helper()
	req, err := http.NewRequest(method, "https://graph.microsoft.com/v1.0"+path, bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			c.t.Fatalf("%s %s: decode %s: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode, out
}

func encoded(b []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(b)) }

// A shared mailbox found by its UPN, case aside, or by its mail; its
// userPurpose and its Inbox; a personal one's purpose user; a $ UPN
// through the key form; with the application's token.
func TestMail_TheMailboxReads(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	post := g.AddUser(graphtest.User{UserPrincipalName: "post@nordlys.onmicrosoft.com", Mail: "Post@nordlys.no", DisplayName: "Post", Shared: true})
	kari := g.AddUser(graphtest.User{UserPrincipalName: "kari@nordlys.no", Mail: "kari@nordlys.no", AccountEnabled: true})
	dollar := g.AddUser(graphtest.User{UserPrincipalName: "$faktura@nordlys.no", Shared: true})
	c := newClient(t, g)

	if status, _, body := c.do(http.MethodGet, "/users/POST@nordlys.onmicrosoft.com?%24select=id,mail", nil); status != http.StatusOK || body["id"] != post.ID || body["mail"] != "Post@nordlys.no" {
		t.Errorf("GET by the UPN: %d %v", status, body)
	}
	if status, _, body := c.do(http.MethodGet, "/users/post@nordlys.no", nil); status != http.StatusNotFound {
		t.Errorf("GET by the mail address: %d %v, want 404 (the path takes a UPN)", status, body)
	}
	filter := "/users?" + strings.ReplaceAll(url.Values{"$filter": {"mail eq 'post@NORDLYS.no'"}, "$select": {"id"}}.Encode(), "+", "%20")
	if status, _, body := c.do(http.MethodGet, filter, nil); status != http.StatusOK || len(body["value"].([]any)) != 1 || body["value"].([]any)[0].(map[string]any)["id"] != post.ID {
		t.Errorf("mail eq: %d %v", status, body)
	}
	if status, _, body := c.do(http.MethodGet, "/users/$faktura@nordlys.no", nil); status != http.StatusBadRequest {
		t.Errorf("a $ UPN bare: %d %v, want 400", status, body)
	}
	if status, _, body := c.do(http.MethodGet, "/users('"+url.PathEscape("$faktura@nordlys.no")+"')?%24select=id", nil); status != http.StatusOK || body["id"] != dollar.ID {
		t.Errorf("a $ UPN in the key form: %d %v", status, body)
	}

	for _, u := range []struct {
		user    graphtest.User
		purpose string
	}{{post, "shared"}, {kari, "user"}} {
		if status, _, body := c.do(http.MethodGet, "/users/"+u.user.ID+"/mailboxSettings/userPurpose", nil); status != http.StatusOK || body["value"] != u.purpose {
			t.Errorf("%s's userPurpose: %d %v, want %s", u.user.UserPrincipalName, status, body, u.purpose)
		}
	}
	if status, _, body := c.do(http.MethodGet, "/users/"+post.ID+"/mailFolders/inbox?%24select=id", nil); status != http.StatusOK || body["id"] == "" || body["id"] == nil {
		t.Errorf("the Inbox: %d %v", status, body)
	}
}

// sendMail takes base64 MIME as text/plain, 202, recorded with no Bcc —
// Graph's MIME send is reported not to honour the header; not base64 is
// 400, over 4 000 000 bytes of body 413.
func TestMail_SendMail(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	post := g.AddUser(graphtest.User{UserPrincipalName: "post@nordlys.no", Shared: true})
	c := newClient(t, g)
	path := "/users/" + post.ID + "/sendMail"

	if status, body := c.mailRequest(http.MethodPost, path, "text/plain", encoded(mime("skjult@example.com"))); status != http.StatusAccepted {
		t.Fatalf("sendMail: %d %v", status, body)
	}
	sent := g.Sent()
	if len(sent) != 1 || sent[0].UserID != post.ID || !bytes.Equal(sent[0].MIME, mime("skjult@example.com")) || len(sent[0].Bcc) != 0 {
		t.Errorf("sent = %+v", sent)
	}
	if status, body := c.mailRequest(http.MethodPost, path, "text/plain", []byte("not base64 at all!")); status != http.StatusBadRequest || errorCode(body) != "ErrorMimeContentInvalidBase64String" {
		t.Errorf("not base64: %d %v", status, body)
	}
	if status, body := c.mailRequest(http.MethodPost, path, "text/plain", bytes.Repeat([]byte("A"), 4_000_004)); status != http.StatusRequestEntityTooLarge {
		t.Errorf("over 4 MB: %d %v, want 413", status, body)
	}
	if status, body := c.mailRequest(http.MethodPost, path, "application/json", []byte(`{}`)); status != http.StatusBadRequest {
		t.Errorf("JSON: %d %v, want 400 (the fake takes MIME alone)", status, body)
	}
	if len(g.Sent()) != 1 {
		t.Errorf("a refused send was recorded: %+v", g.Sent())
	}
}

// A MIME draft reads its Bcc header into bccRecipients; its send records
// the mail with them and leaves no draft; a draft deleted; one not there
// 404.
func TestMail_ADraftWithBcc(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	post := g.AddUser(graphtest.User{UserPrincipalName: "post@nordlys.no", Shared: true})
	c := newClient(t, g)
	base := "/users/" + post.ID + "/messages"

	status, draft := c.mailRequest(http.MethodPost, base, "text/plain", encoded(mime("Skjult <skjult@example.com>, to@example.com")))
	id, _ := draft["id"].(string)
	if status != http.StatusCreated || id == "" || draft["isDraft"] != true {
		t.Fatalf("POST messages: %d %v", status, draft)
	}
	var bcc []string
	for _, r := range draft["bccRecipients"].([]any) {
		bcc = append(bcc, r.(map[string]any)["emailAddress"].(map[string]any)["address"].(string))
	}
	if !slices.Equal(bcc, []string{"skjult@example.com", "to@example.com"}) {
		t.Errorf("bccRecipients = %v", bcc)
	}
	if b, _ := draft["body"].(map[string]any); b == nil || b["content"] != string(mime("Skjult <skjult@example.com>, to@example.com")) {
		t.Errorf("the draft's body = %v, want the message whole as Graph answers it", draft["body"])
	}
	if drafts := g.Drafts(); len(drafts) != 1 || drafts[0].ID != id || drafts[0].UserID != post.ID {
		t.Errorf("drafts = %+v", drafts)
	}
	if status, body := c.mailRequest(http.MethodPost, base+"/"+url.PathEscape(id)+"/send", "", nil); status != http.StatusAccepted {
		t.Fatalf("send the draft: %d %v", status, body)
	}
	sent := g.Sent()
	if len(sent) != 1 || !slices.Equal(sent[0].Bcc, bcc) || len(g.Drafts()) != 0 {
		t.Errorf("after the send: sent %+v, drafts %+v", sent, g.Drafts())
	}
	if status, body := c.mailRequest(http.MethodPost, base+"/"+url.PathEscape(id)+"/send", "", nil); status != http.StatusNotFound || errorCode(body) != "ErrorItemNotFound" {
		t.Errorf("a draft sent already: %d %v", status, body)
	}

	_, draft = c.mailRequest(http.MethodPost, base, "text/plain", encoded(mime("")))
	id, _ = draft["id"].(string)
	g.DropDraftBcc(true)
	_, lost := c.mailRequest(http.MethodPost, base, "text/plain", encoded(mime("skjult@example.com")))
	if r, _ := lost["bccRecipients"].([]any); len(r) != 0 {
		t.Errorf("DropDraftBcc: %v", lost["bccRecipients"])
	}
	g.DropDraftBcc(false)
	g.FailNextDraftSend(http.StatusInternalServerError)
	if status, _ := c.mailRequest(http.MethodPost, base+"/"+url.PathEscape(id)+"/send", "", nil); status != http.StatusInternalServerError {
		t.Errorf("FailNextDraftSend: %d, want 500", status)
	}
	if status, _ := c.mailRequest(http.MethodDelete, base+"/"+url.PathEscape(id), "", nil); status != http.StatusNoContent {
		t.Errorf("DELETE the draft: %d", status)
	}
	if status, _ := c.mailRequest(http.MethodDelete, base+"/"+url.PathEscape(id), "", nil); status != http.StatusNotFound {
		t.Errorf("DELETE it again: %d, want 404", status)
	}
	if len(g.Drafts()) != 1 || len(g.Sent()) != 1 {
		t.Errorf("drafts %d sent %d, want the lost-Bcc draft and the one send", len(g.Drafts()), len(g.Sent()))
	}
}

// The mail routes' refusals: MailboxDenied 403 ErrorAccessDenied on each,
// NoMailbox 404 MailboxNotEnabledForRESTAPI; a person's delegated token
// reaches only their own mailbox; the application's reaches any.
func TestMail_Refusals(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	denied := g.AddUser(graphtest.User{UserPrincipalName: "denied@nordlys.no", Shared: true, MailboxDenied: true})
	none := g.AddUser(graphtest.User{UserPrincipalName: "none@nordlys.no", NoMailbox: true})
	post := g.AddUser(graphtest.User{UserPrincipalName: "post@nordlys.no", Shared: true})
	kari := g.AddUser(graphtest.User{UserPrincipalName: "kari@nordlys.no", AccountEnabled: true})
	app := newClient(t, g)

	routes := []struct {
		method, rest, contentType string
		body                      []byte
	}{
		{http.MethodGet, "/mailboxSettings/userPurpose", "", nil},
		{http.MethodGet, "/mailFolders/inbox", "", nil},
		{http.MethodPost, "/sendMail", "text/plain", encoded(mime(""))},
		{http.MethodPost, "/messages", "text/plain", encoded(mime(""))},
	}
	for _, r := range routes {
		if status, body := app.mailRequest(r.method, "/users/"+denied.ID+r.rest, r.contentType, r.body); status != http.StatusForbidden || errorCode(body) != "ErrorAccessDenied" {
			t.Errorf("%s %s denied: %d %v", r.method, r.rest, status, body)
		}
		if status, body := app.mailRequest(r.method, "/users/"+none.ID+r.rest, r.contentType, r.body); status != http.StatusNotFound || errorCode(body) != "MailboxNotEnabledForRESTAPI" {
			t.Errorf("%s %s without a mailbox: %d %v", r.method, r.rest, status, body)
		}
	}
	person := personClient(t, g, kari.ID)
	if status, body := person.mailRequest(http.MethodGet, "/users/"+post.ID+"/mailFolders/inbox", "", nil); status != http.StatusForbidden || errorCode(body) != "ErrorAccessDenied" {
		t.Errorf("another's delegated token: %d %v", status, body)
	}
	if status, body := person.mailRequest(http.MethodGet, "/users/"+kari.ID+"/mailFolders/inbox", "", nil); status != http.StatusOK {
		t.Errorf("the person's own: %d %v", status, body)
	}
	if len(g.Sent()) != 0 || len(g.Drafts()) != 0 {
		t.Errorf("a refusal wrote: sent %d drafts %d", len(g.Sent()), len(g.Drafts()))
	}
}

// SetMailSizeLimit: a MIME message over the limit is 400
// ErrorMessageSizeExceeded on sendMail and on a draft's create; at the limit
// it is taken.
func TestMail_TheMailboxSizeLimit(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	post := g.AddUser(graphtest.User{UserPrincipalName: "post@nordlys.no", Shared: true})
	c := newClient(t, g)
	m := mime("")
	g.SetMailSizeLimit(len(m) - 1)
	for _, path := range []string{"/users/" + post.ID + "/sendMail", "/users/" + post.ID + "/messages"} {
		if status, body := c.mailRequest(http.MethodPost, path, "text/plain", encoded(m)); status != http.StatusBadRequest || errorCode(body) != "ErrorMessageSizeExceeded" {
			t.Errorf("POST %s over the limit: %d %v", path, status, body)
		}
	}
	g.SetMailSizeLimit(len(m))
	if status, body := c.mailRequest(http.MethodPost, "/users/"+post.ID+"/sendMail", "text/plain", encoded(m)); status != http.StatusAccepted {
		t.Errorf("sendMail at the limit: %d %v", status, body)
	}
}
