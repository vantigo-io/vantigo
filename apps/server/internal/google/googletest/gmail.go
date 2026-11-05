package googletest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// This file is the fake's Gmail (delivery F's mail channels): the mailboxes
// of the Directory's users, each reached by a delegated token whose subject
// is the user's address — users/me — with gmail.modify (gmail.send for a
// send). It answers users.getProfile, messages.list (labelIds and
// q=after:<epoch>), messages.get (format metadata or raw), messages.modify,
// history.list (labelId, a start older than the mailbox keeps answering 404,
// U15), labels.list and labels.create, and the upload messages.send, which
// threads a reply by its In-Reply-To and — switchable — replaces the
// Message-ID the MIME carried, as Gmail is reported to (U6).

// GmailHost is Gmail's host.
const GmailHost = "gmail.googleapis.com"

// The Gmail scopes.
const (
	ScopeGmailModify = "https://www.googleapis.com/auth/gmail.modify"
	ScopeGmailSend   = "https://www.googleapis.com/auth/gmail.send"
)

// GmailMessage is a message in a fake mailbox.
type GmailMessage struct {
	ID, ThreadID string
	Labels       []string
	InternalDate time.Time
	MessageID    string
	Raw          []byte
}

// gmailRecord is one history record: a message added (its labels), deleted,
// or labels added to or removed from it.
type gmailRecord struct {
	id     uint64
	kind   string // messageAdded, messageDeleted, labelAdded, labelRemoved
	msg    string
	labels []string // the labels changed, or for an add the message's
}

// gmailBox is one user's mailbox.
type gmailBox struct {
	messages map[string]*GmailMessage
	order    []string
	labels   []map[string]string
	history  []gmailRecord
	current  uint64
	oldest   uint64
}

// gmailState is the fake's Gmail, under the fake's lock.
type gmailState struct {
	boxes        map[string]*gmailBox
	next         int
	replaceMsgID bool
	sendLimit    int
	noGmail      map[string]bool
}

// box is address's mailbox, made on first use.
func (g *FakeGoogle) box(address string) *gmailBox {
	address = strings.ToLower(address)
	if g.gmail.boxes == nil {
		g.gmail.boxes = map[string]*gmailBox{}
	}
	b, ok := g.gmail.boxes[address]
	if !ok {
		b = &gmailBox{messages: map[string]*GmailMessage{}, current: 1000}
		g.gmail.boxes[address] = b
	}
	return b
}

// nextGmailID is a fresh message, thread or label number, as hex.
func (g *FakeGoogle) nextGmailID() string {
	g.gmail.next++
	return fmt.Sprintf("18c%013x", g.gmail.next)
}

// record appends a history record to b.
func (b *gmailBox) record(kind, msg string, labels []string) {
	b.current++
	b.history = append(b.history, gmailRecord{id: b.current, kind: kind, msg: msg, labels: slices.Clone(labels)})
}

// SetReplaceMessageID makes messages.send replace the Message-ID of every
// message it sends (U6).
func (g *FakeGoogle) SetReplaceMessageID(on bool) {
	g.Configure(func(g *FakeGoogle) { g.gmail.replaceMsgID = on })
}

// SetGmailSendLimit makes messages.send answer 413 for a message larger
// than n bytes; 35 MB when zero.
func (g *FakeGoogle) SetGmailSendLimit(n int) {
	g.Configure(func(g *FakeGoogle) { g.gmail.sendLimit = n })
}

// SetNoGmail makes every Gmail call as address answer 400
// failedPrecondition, as for a user without a Gmail licence.
func (g *FakeGoogle) SetNoGmail(address string, on bool) {
	g.Configure(func(g *FakeGoogle) {
		if g.gmail.noGmail == nil {
			g.gmail.noGmail = map[string]bool{}
		}
		g.gmail.noGmail[strings.ToLower(address)] = on
	})
}

// ExpireHistory makes every history start address's mailbox has answered so
// far too old: history.list from it answers 404.
func (g *FakeGoogle) ExpireHistory(address string) {
	g.Configure(func(g *FakeGoogle) {
		b := g.box(address)
		b.current += 10
		b.oldest = b.current
	})
}

// Deliver puts raw in address's Inbox, unread, received at: threaded onto
// the thread of the message its In-Reply-To names, else a thread of its own.
func (g *FakeGoogle) Deliver(address string, raw []byte, at time.Time) GmailMessage {
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.box(address)
	msg := &GmailMessage{ID: g.nextGmailID(), Labels: []string{"INBOX", "UNREAD"}, InternalDate: at.UTC(), Raw: slices.Clone(raw)}
	msg.MessageID, msg.ThreadID = g.threadOf(b, raw)
	b.messages[msg.ID] = msg
	b.order = append(b.order, msg.ID)
	b.record("messageAdded", msg.ID, msg.Labels)
	return *msg
}

// DeliverInThread puts raw in address's Inbox as Deliver does, in thread
// whatever its headers say, as Gmail threads by subject too.
func (g *FakeGoogle) DeliverInThread(address string, raw []byte, at time.Time, thread string) GmailMessage {
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.box(address)
	msg := &GmailMessage{ID: g.nextGmailID(), Labels: []string{"INBOX", "UNREAD"}, InternalDate: at.UTC(), Raw: slices.Clone(raw), ThreadID: thread}
	msg.MessageID, _ = g.threadOf(b, raw)
	b.messages[msg.ID] = msg
	b.order = append(b.order, msg.ID)
	b.record("messageAdded", msg.ID, msg.Labels)
	return *msg
}

// threadOf is the Message-ID raw carries and the thread it joins: the
// thread of a message whose Message-ID its In-Reply-To or References names,
// else a new one.
func (g *FakeGoogle) threadOf(b *gmailBox, raw []byte) (string, string) {
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", g.nextGmailID()
	}
	refs := strings.Fields(m.Header.Get("In-Reply-To") + " " + m.Header.Get("References"))
	for _, id := range b.order {
		if other := b.messages[id]; other.MessageID != "" && slices.Contains(refs, other.MessageID) {
			return strings.TrimSpace(m.Header.Get("Message-ID")), other.ThreadID
		}
	}
	return strings.TrimSpace(m.Header.Get("Message-ID")), g.nextGmailID()
}

// RemoveFromInbox takes INBOX off address's message id, as a person
// archiving it in Gmail does.
func (g *FakeGoogle) RemoveFromInbox(address, id string) {
	g.Configure(func(g *FakeGoogle) {
		b := g.box(address)
		if msg, ok := b.messages[id]; ok {
			msg.Labels = slices.DeleteFunc(msg.Labels, func(l string) bool { return l == "INBOX" })
			b.record("labelRemoved", id, []string{"INBOX"})
		}
	})
}

// DeleteGmailMessage deletes address's message id for good.
func (g *FakeGoogle) DeleteGmailMessage(address, id string) {
	g.Configure(func(g *FakeGoogle) {
		b := g.box(address)
		if _, ok := b.messages[id]; ok {
			delete(b.messages, id)
			b.order = slices.DeleteFunc(b.order, func(o string) bool { return o == id })
			b.record("messageDeleted", id, nil)
		}
	})
}

// GmailMessages are address's messages, oldest first.
func (g *FakeGoogle) GmailMessages(address string) []GmailMessage {
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.box(address)
	out := make([]GmailMessage, 0, len(b.order))
	for _, id := range b.order {
		m := *b.messages[id]
		m.Labels = slices.Clone(m.Labels)
		out = append(out, m)
	}
	return out
}

// GmailMessageOf is address's message id, false when there is none.
func (g *FakeGoogle) GmailMessageOf(address, id string) (GmailMessage, bool) {
	for _, m := range g.GmailMessages(address) {
		if m.ID == id {
			return m, true
		}
	}
	return GmailMessage{}, false
}

// GmailLabels are address's user labels, name to id.
func (g *FakeGoogle) GmailLabels(address string) map[string]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[string]string{}
	for _, l := range g.box(address).labels {
		out[l["name"]] = l["id"]
	}
	return out
}

// knownSubject reports whether sub may be a delegated token's subject: an
// admin, or a Directory user who is not suspended (a suspended one is
// refused as an unknown one is, U17).
func (g *FakeGoogle) knownSubject(sub string) bool {
	sub = strings.ToLower(sub)
	if slices.Contains(g.Admins, sub) {
		return true
	}
	for _, u := range g.users {
		if strings.EqualFold(u.PrimaryEmail, sub) {
			return !u.Suspended
		}
	}
	return false
}

// mailboxOf is the address of the mailbox a token for sub reaches: a
// delegated token's subject is the address itself; a person's own token's
// (delivery G) is their Directory id, whose user's primary address it is.
// False for a subject that may not reach one — a suspended user's.
func (g *FakeGoogle) mailboxOf(sub string) (string, bool) {
	if g.knownSubject(sub) {
		return strings.ToLower(sub), true
	}
	for _, u := range g.users {
		if u.ID == sub {
			return strings.ToLower(u.PrimaryEmail), !u.Suspended
		}
	}
	return "", false
}

// gmailCall answers one Gmail call. g.mu is held.
func (g *FakeGoogle) gmailCall(req *http.Request, body []byte) answer {
	tok, ok := g.bearer(req)
	address := ""
	if ok {
		address, ok = g.mailboxOf(tok.subject)
	}
	if !ok {
		// A user suspended since the token was issued: its tokens no longer
		// work, and a fresh one is refused.
		return answer{status: http.StatusUnauthorized, body: g.apiError(http.StatusUnauthorized, "authError")}
	}
	has := func(scope string) bool { return slices.Contains(tok.scopes, scope) }
	denied := answer{status: http.StatusForbidden, body: g.apiError(http.StatusForbidden, "insufficientPermissions")}
	if g.gmail.noGmail[address] {
		return answer{status: http.StatusBadRequest, body: g.apiError(http.StatusBadRequest, "failedPrecondition")}
	}
	b := g.box(address)
	if req.Method == http.MethodPost && req.URL.Path == "/upload/gmail/v1/users/me/messages/send" {
		if !has(ScopeGmailSend) && !has(ScopeGmailModify) {
			return denied
		}
		return g.gmailSend(req, b, body)
	}
	rest, ok := strings.CutPrefix(req.URL.Path, "/gmail/v1/users/me/")
	if !ok {
		return g.notFound()
	}
	if !has(ScopeGmailModify) {
		return denied
	}
	parts := strings.Split(rest, "/")
	q := req.URL.Query()
	switch {
	case rest == "profile" && req.Method == http.MethodGet:
		return answer{status: http.StatusOK, body: map[string]any{
			"emailAddress": address, "messagesTotal": len(b.messages), "historyId": strconv.FormatUint(b.current, 10),
		}}
	case rest == "messages" && req.Method == http.MethodGet:
		return g.gmailList(b, q)
	case len(parts) == 2 && parts[0] == "messages" && req.Method == http.MethodGet:
		return g.gmailGet(b, parts[1], q)
	case len(parts) == 3 && parts[0] == "messages" && parts[2] == "modify" && req.Method == http.MethodPost:
		return g.gmailModify(b, parts[1], body)
	case rest == "history" && req.Method == http.MethodGet:
		return g.gmailHistory(b, q)
	case rest == "labels" && req.Method == http.MethodGet:
		labels := []map[string]string{{"id": "INBOX", "name": "INBOX", "type": "system"}, {"id": "UNREAD", "name": "UNREAD", "type": "system"}}
		return answer{status: http.StatusOK, body: map[string]any{"labels": append(labels, b.labels...)}}
	case rest == "labels" && req.Method == http.MethodPost:
		var in struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(body, &in) != nil || strings.TrimSpace(in.Name) == "" {
			return g.badRequest()
		}
		for _, l := range b.labels {
			if strings.EqualFold(l["name"], in.Name) {
				return answer{status: http.StatusConflict, body: g.apiError(http.StatusConflict, "duplicate")}
			}
		}
		g.gmail.next++
		l := map[string]string{"id": "Label_" + strconv.Itoa(g.gmail.next), "name": in.Name, "type": "user"}
		b.labels = append(b.labels, l)
		return answer{status: http.StatusOK, body: l}
	}
	return g.notFound()
}

// gmailList is messages.list: the messages carrying every labelIds, received
// after q's after:<epoch>, newest first, maxResults a page; the page token
// is the offset, and any other is a 400.
func (g *FakeGoogle) gmailList(b *gmailBox, q url.Values) answer {
	var after int64
	for _, term := range strings.Fields(q.Get("q")) {
		if v, ok := strings.CutPrefix(term, "after:"); ok {
			after, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	var ids []string
	for i := len(b.order) - 1; i >= 0; i-- {
		m := b.messages[b.order[i]]
		if slices.ContainsFunc(q["labelIds"], func(l string) bool { return !slices.Contains(m.Labels, l) }) {
			continue
		}
		if m.InternalDate.Unix() <= after {
			continue
		}
		ids = append(ids, m.ID)
	}
	return pageOf(q, ids, func(id string) map[string]any { return map[string]any{"id": id, "threadId": b.messages[id].ThreadID} }, "messages", g)
}

// pageOf answers one page of ids as key, maxResults at most, the page token
// the offset.
func pageOf(q url.Values, ids []string, item func(string) map[string]any, key string, g *FakeGoogle) answer {
	size, _ := strconv.Atoi(q.Get("maxResults"))
	if size <= 0 {
		size = 100
	}
	from := 0
	if t := q.Get("pageToken"); t != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(t, "p"))
		if err != nil || !strings.HasPrefix(t, "p") || n < 0 || n > len(ids) {
			return g.badRequest()
		}
		from = n
	}
	to := min(from+size, len(ids))
	out := map[string]any{"resultSizeEstimate": len(ids)}
	var items []map[string]any
	for _, id := range ids[from:to] {
		items = append(items, item(id))
	}
	if len(items) > 0 {
		out[key] = items
	}
	if to < len(ids) {
		out["nextPageToken"] = "p" + strconv.Itoa(to)
	}
	return answer{status: http.StatusOK, body: out}
}

// gmailGet is messages.get: format=raw answers the bytes in base64url,
// anything else the metadata with the headers metadataHeaders names.
func (g *FakeGoogle) gmailGet(b *gmailBox, id string, q url.Values) answer {
	m, ok := b.messages[id]
	if !ok {
		return g.notFound()
	}
	out := map[string]any{"id": m.ID, "threadId": m.ThreadID, "labelIds": m.Labels,
		"internalDate": strconv.FormatInt(m.InternalDate.UnixMilli(), 10)}
	if q.Get("format") == "raw" {
		out["raw"] = base64.URLEncoding.EncodeToString(m.Raw)
		return answer{status: http.StatusOK, body: out}
	}
	var headers []map[string]string
	if parsed, err := mail.ReadMessage(bytes.NewReader(m.Raw)); err == nil {
		for _, name := range q["metadataHeaders"] {
			if v := parsed.Header.Get(name); v != "" {
				headers = append(headers, map[string]string{"name": "Message-Id", "value": v})
			}
		}
	}
	out["payload"] = map[string]any{"headers": headers}
	return answer{status: http.StatusOK, body: out}
}

// gmailModify is messages.modify, each label change a history record.
func (g *FakeGoogle) gmailModify(b *gmailBox, id string, body []byte) answer {
	m, ok := b.messages[id]
	if !ok {
		return g.notFound()
	}
	var in struct {
		Add    []string `json:"addLabelIds"`
		Remove []string `json:"removeLabelIds"`
	}
	if json.Unmarshal(body, &in) != nil {
		return g.badRequest()
	}
	for _, l := range in.Add {
		known := l == "INBOX" || l == "UNREAD" || slices.ContainsFunc(b.labels, func(x map[string]string) bool { return x["id"] == l })
		if !known {
			return g.badRequest()
		}
		if !slices.Contains(m.Labels, l) {
			m.Labels = append(m.Labels, l)
			b.record("labelAdded", id, []string{l})
		}
	}
	for _, l := range in.Remove {
		if slices.Contains(m.Labels, l) {
			m.Labels = slices.DeleteFunc(m.Labels, func(x string) bool { return x == l })
			b.record("labelRemoved", id, []string{l})
		}
	}
	return answer{status: http.StatusOK, body: map[string]any{"id": m.ID, "threadId": m.ThreadID, "labelIds": m.Labels}}
}

// gmailHistory is history.list from startHistoryId: 404 for a start older
// than the mailbox keeps, the records after it involving labelId, a page of
// maxResults records.
func (g *FakeGoogle) gmailHistory(b *gmailBox, q url.Values) answer {
	start, err := strconv.ParseUint(q.Get("startHistoryId"), 10, 64)
	if err != nil {
		return g.badRequest()
	}
	if start < b.oldest {
		return g.notFound()
	}
	label := q.Get("labelId")
	var recs []gmailRecord
	for _, r := range b.history {
		if r.id <= start {
			continue
		}
		if label != "" {
			m, live := b.messages[r.msg]
			involves := slices.Contains(r.labels, label) || (live && slices.Contains(m.Labels, label)) || r.kind == "messageDeleted"
			if !involves {
				continue
			}
		}
		recs = append(recs, r)
	}
	ids := make([]string, len(recs))
	for i := range recs {
		ids[i] = strconv.Itoa(i)
	}
	a := pageOf(q, ids, func(i string) map[string]any {
		n, _ := strconv.Atoi(i)
		r := recs[n]
		entry := map[string]any{"message": map[string]any{"id": r.msg}}
		switch r.kind {
		case "messageAdded":
			entry = map[string]any{"message": map[string]any{"id": r.msg, "labelIds": r.labels}}
			return map[string]any{"id": strconv.FormatUint(r.id, 10), "messagesAdded": []any{entry}}
		case "messageDeleted":
			return map[string]any{"id": strconv.FormatUint(r.id, 10), "messagesDeleted": []any{entry}}
		case "labelAdded":
			entry["labelIds"] = r.labels
			return map[string]any{"id": strconv.FormatUint(r.id, 10), "labelsAdded": []any{entry}}
		default:
			entry["labelIds"] = r.labels
			return map[string]any{"id": strconv.FormatUint(r.id, 10), "labelsRemoved": []any{entry}}
		}
	}, "history", g)
	if a.status == http.StatusOK {
		a.body.(map[string]any)["historyId"] = strconv.FormatUint(b.current, 10)
	}
	return a
}

// gmailSend is the upload messages.send: message/rfc822 whole, 413 above
// the limit; the message filed under SENT, threaded by its In-Reply-To, its
// Message-ID replaced when SetReplaceMessageID asked.
func (g *FakeGoogle) gmailSend(req *http.Request, b *gmailBox, body []byte) answer {
	if req.URL.Query().Get("uploadType") != "media" || !strings.HasPrefix(req.Header.Get("Content-Type"), "message/rfc822") {
		return g.badRequest()
	}
	limit := g.gmail.sendLimit
	if limit == 0 {
		limit = 35 << 20
	}
	if len(body) > limit {
		return answer{status: http.StatusRequestEntityTooLarge, body: g.apiError(http.StatusRequestEntityTooLarge, "uploadTooLarge")}
	}
	raw := slices.Clone(body)
	if g.gmail.replaceMsgID {
		raw = replaceHeader(raw, "Message-ID", "<CAfake"+g.nextGmailID()+"@mail.gmail.com>")
	}
	msg := &GmailMessage{ID: g.nextGmailID(), Labels: []string{"SENT"}, InternalDate: g.now().UTC(), Raw: raw}
	msg.MessageID, msg.ThreadID = g.threadOf(b, raw)
	b.messages[msg.ID] = msg
	b.order = append(b.order, msg.ID)
	b.record("messageAdded", msg.ID, msg.Labels)
	return answer{status: http.StatusOK, body: map[string]any{"id": msg.ID, "threadId": msg.ThreadID, "labelIds": msg.Labels}}
}

// replaceHeader is raw with the header name's value replaced by value.
func replaceHeader(raw []byte, name, value string) []byte {
	head, rest, found := bytes.Cut(raw, []byte("\r\n\r\n"))
	if !found {
		return raw
	}
	lines := strings.Split(string(head), "\r\n")
	for i, l := range lines {
		if k, _, ok := strings.Cut(l, ":"); ok && strings.EqualFold(strings.TrimSpace(k), name) {
			lines[i] = name + ": " + value
		}
	}
	return append([]byte(strings.Join(lines, "\r\n")+"\r\n\r\n"), rest...)
}
