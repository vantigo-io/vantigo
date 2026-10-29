package graphtest

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/mail"
	"regexp"
	"slices"
	"strings"
)

// This file is the mail a shared mailbox sends (Outlook connection and
// Microsoft 365 mail, delivery C's plan reading 11 and 12), reached with the
// application's token — the channel mailboxes' — or a delegated token for
// that very user, never another's (403 ErrorAccessDenied):
//
//   - GET /users/{id}/mailboxSettings/userPurpose answers {"value": "shared"}
//     for a user marked Shared, "user" otherwise;
//   - GET /users/{id}/mailFolders/inbox answers the Inbox's id;
//   - POST /users/{id}/sendMail takes a MIME message base64-encoded as
//     text/plain — 400 ErrorMimeContentInvalidBase64String when it does not
//     decode, 413 over mailMaxBody bytes of body, 400 for JSON, which the
//     fake does not take — and answers 202, recorded by Sent with no Bcc:
//     Graph's MIME send is reported not to honour a Bcc header;
//   - POST /users/{id}/messages takes the same MIME as a draft, answering
//     201 with the message whole, as Graph does — its id, the
//     bccRecipients its Bcc header names (none while DropDraftBcc is set)
//     and its body, the MIME's text —; POST …/messages/{id}/send sends it, 202,
//     recorded by Sent with those recipients, and leaves no draft;
//     DELETE …/messages/{id} deletes it, 204; a draft not there is 404
//     ErrorItemNotFound. Drafts lists the drafts not sent.
//
// A MIME message larger than SetMailSizeLimit's bytes is refused as
// Exchange refuses one over a mailbox's send limit: 400
// ErrorMessageSizeExceeded, on sendMail and on a draft's create.
//
// A user marked MailboxDenied answers 403 ErrorAccessDenied on every mail
// route (outside the RBAC for Applications scope), NoMailbox 404
// MailboxNotEnabledForRESTAPI. A user is found by its id or its
// userPrincipalName, never its mail (graph.go's userByID); GET /users takes
// $filter=mail eq '…' (listUsers); a userPrincipalName beginning with $ is
// reached as /users('$…') (api), and bare is 400.

// mailMaxBody is the most of a sendMail's or a draft's body the fake takes:
// Graph's 4 MB, read as decimal megabytes, of base64.
const mailMaxBody = 4_000_000

// mailPath is a user's mail: what a channel mailbox is checked and sent
// through.
var mailPath = regexp.MustCompile(`(?i)^/users/([^/]+)/(sendMail|messages|mailFolders|mailboxSettings)(/|$)`)

// keyPath is a user addressed by the key form, /users('…'), its quotes
// doubled inside.
var keyPath = regexp.MustCompile(`^/users\('((?:[^']|'')*)'\)(/.*)?$`)

// SentMail is one message a mailbox sent: the user's id, the MIME as it was
// decoded, and the blind copies the send carried.
type SentMail struct {
	UserID string
	MIME   []byte
	Bcc    []string
}

// Draft is a message created in a mailbox and not sent.
type Draft struct {
	ID     string
	UserID string
	MIME   []byte
	Bcc    []string
}

// Sent is every message sent, in order.
func (g *FakeGraph) Sent() []SentMail {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]SentMail, len(g.sent))
	for i, s := range g.sent {
		out[i] = SentMail{UserID: s.UserID, MIME: slices.Clone(s.MIME), Bcc: slices.Clone(s.Bcc)}
	}
	return out
}

// Drafts is every draft not sent nor deleted, in order.
func (g *FakeGraph) Drafts() []Draft {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Draft, len(g.drafts))
	for i, d := range g.drafts {
		out[i] = Draft{ID: d.ID, UserID: d.UserID, MIME: slices.Clone(d.MIME), Bcc: slices.Clone(d.Bcc)}
	}
	return out
}

// DropDraftBcc, while on, makes a draft's create keep no Bcc recipient,
// whatever its MIME names — a Graph that does not read the header.
func (g *FakeGraph) DropDraftBcc(on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.dropDraftBcc = on
}

// FailNextDraftSend makes the next send of a draft answer status, the
// draft kept.
func (g *FakeGraph) FailNextDraftSend(status int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failDraftSend = append(g.failDraftSend, status)
}

// SetMailSizeLimit makes a MIME message larger than n bytes refused 400
// ErrorMessageSizeExceeded, as a mailbox's send limit is; 0 sets no limit.
func (g *FakeGraph) SetMailSizeLimit(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mailSizeLimit = n
}

// keyForm rewrites a /users('…') path to /users/… . g.mu is held.
func keyForm(path string) string {
	m := keyPath.FindStringSubmatch(path)
	if m == nil {
		return path
	}
	return "/users/" + strings.ReplaceAll(m[1], "''", "'") + m[2]
}

// mailRoute answers a request to u's mail; seg starts at the route's
// first segment after the user.
func (g *FakeGraph) mailRoute(u *User, method string, seg []string, contentType string, body []byte) answer {
	if a, refused := mailboxRefused(u); refused {
		return a
	}
	rest := strings.Join(seg, "/")
	switch {
	case method == http.MethodGet && rest == "mailboxSettings/userPurpose":
		purpose := "user"
		if u.Shared {
			purpose = "shared"
		}
		return answer{status: http.StatusOK, body: map[string]any{"value": purpose}}
	case method == http.MethodGet && strings.EqualFold(rest, "mailFolders/inbox"):
		return answer{status: http.StatusOK, body: map[string]any{"id": "AQMkADinbox" + strings.ReplaceAll(u.ID, "-", "")}}
	case method == http.MethodPost && rest == "sendMail":
		mime, a, ok := g.decodeMIME(contentType, body)
		if !ok {
			return a
		}
		g.sent = append(g.sent, SentMail{UserID: u.ID, MIME: mime})
		return answer{status: http.StatusAccepted}
	case method == http.MethodPost && rest == "messages":
		mime, a, ok := g.decodeMIME(contentType, body)
		if !ok {
			return a
		}
		d := &Draft{ID: g.itemID(), UserID: u.ID, MIME: mime}
		if !g.dropDraftBcc {
			d.Bcc = bccOf(mime)
		}
		g.drafts = append(g.drafts, d)
		recipients := []any{}
		for _, addr := range d.Bcc {
			recipients = append(recipients, map[string]any{"emailAddress": map[string]any{"address": addr}})
		}
		return answer{status: http.StatusCreated, body: map[string]any{
			"id": d.ID, "isDraft": true, "bccRecipients": recipients,
			"body": map[string]any{"contentType": "text", "content": string(mime)},
		}}
	case len(seg) == 3 && seg[0] == "messages" && seg[2] == "send" && method == http.MethodPost:
		i := g.draftIndex(u, seg[1])
		if i < 0 {
			return itemNotFound()
		}
		if len(g.failDraftSend) > 0 {
			status := g.failDraftSend[0]
			g.failDraftSend = g.failDraftSend[1:]
			return graphError(status, failureCode(status), "The fake was told to fail this send.")
		}
		d := g.drafts[i]
		g.drafts = slices.Delete(g.drafts, i, i+1)
		g.sent = append(g.sent, SentMail{UserID: u.ID, MIME: d.MIME, Bcc: d.Bcc})
		return answer{status: http.StatusAccepted}
	case len(seg) == 2 && seg[0] == "messages" && method == http.MethodDelete:
		i := g.draftIndex(u, seg[1])
		if i < 0 {
			return itemNotFound()
		}
		g.drafts = slices.Delete(g.drafts, i, i+1)
		return answer{status: http.StatusNoContent}
	}
	return graphError(http.StatusBadRequest, "BadRequest", "Unsupported request: "+method+" users/{id}/"+rest+".")
}

// draftIndex is the index of u's draft id among g.drafts, -1 when there is
// none.
func (g *FakeGraph) draftIndex(u *User, id string) int {
	return slices.IndexFunc(g.drafts, func(d *Draft) bool { return d.ID == id && strings.EqualFold(d.UserID, u.ID) })
}

// decodeMIME is a sendMail's or a draft's body: base64 MIME as text/plain,
// at most mailMaxBody bytes, and the MIME within SetMailSizeLimit's. g.mu is
// held.
func (g *FakeGraph) decodeMIME(contentType string, body []byte) ([]byte, answer, bool) {
	if !strings.HasPrefix(strings.ToLower(contentType), "text/plain") {
		return nil, graphError(http.StatusBadRequest, "ErrorInvalidRequest", "The fake takes a MIME message as text/plain alone."), false
	}
	if len(body) > mailMaxBody {
		return nil, graphError(http.StatusRequestEntityTooLarge, "ErrorRequestEntityTooLarge", "The request is larger than 4 MB."), false
	}
	mime, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(body)))
	if err != nil || len(mime) == 0 {
		return nil, graphError(http.StatusBadRequest, "ErrorMimeContentInvalidBase64String", "Invalid base64 string for MIME content."), false
	}
	if g.mailSizeLimit > 0 && len(mime) > g.mailSizeLimit {
		return nil, graphError(http.StatusBadRequest, "ErrorMessageSizeExceeded", "The message exceeds the maximum supported size."), false
	}
	return mime, answer{}, true
}

// bccOf is the addresses the MIME's Bcc header names, none when it names
// none or does not parse.
func bccOf(mime []byte) []string {
	msg, err := mail.ReadMessage(bytes.NewReader(mime))
	if err != nil {
		return nil
	}
	list, err := msg.Header.AddressList("Bcc")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.Address)
	}
	return out
}

// mailCaller reports whether the request's token may reach path's mail: the
// application's, or a delegated token for that very user. g.mu is held.
func (g *FakeGraph) mailCaller(path string) bool {
	m := mailPath.FindStringSubmatch(path)
	return m == nil || g.caller == nil || g.callerIs(m[1])
}
