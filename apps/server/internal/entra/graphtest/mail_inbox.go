package graphtest

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// This file is a shared mailbox's Inbox as Communications' channels receive
// from it (Outlook connection and Microsoft 365 mail, delivery D's plan
// reading 12), reached as the rest of the mail is (mail.go):
//
//   - GET /users/{id}/mailFolders/inbox/messages/delta — the folder also by
//     its id, the path whatever its case — Graph's message delta, answered
//     from a change log: a fresh query answers every message of the Inbox,
//     narrowed by $filter=receivedDateTime ge|gt {RFC 3339} (any other
//     $filter 400 ErrorInvalidUrlQuery); a $deltatoken answers every change
//     since it was issued — a message added or marked read whole, one gone
//     {"id", "@removed": {"reason": "deleted"}} — the filter no longer
//     applied, as Graph's message delta "can return change events that
//     don't match the filter conditions". $select narrows each message to
//     the fields it names, id always; pages hold Prefer: odata.maxpagesize
//     messages (10 without; DeltaPageSize caps it), with an
//     @odata.nextLink carrying a $skiptoken until the last, which carries
//     the @odata.deltaLink and its $deltatoken. The links name the folder
//     as Graph may, mailfolders/{the Inbox's id} in lower case. A token the
//     fake did not issue for the user is 400 syncStateNotFound;
//     ExpireMailDelta makes every token of the user's answer a refusal until
//     a fresh query;
//   - GET /users/{id}/messages/{id}/$value answers the message's MIME byte
//     for byte, message/rfc822; a message not in the Inbox is 404
//     ErrorItemNotFound.
//
// Every request's Prefer header is recorded in its Call, so a test asserts
// IdType="ImmutableId" was sent; the fake answers the same ids with or
// without it.

// InboxMessage is a message arriving in a mailbox's Inbox: when it was
// received, its MIME whole, and the internetMessageId and conversationId
// Graph answers for it. ID, when set, is the id it is answered with in
// place of a fresh one — a test's odd id.
type InboxMessage struct {
	ID                string
	ReceivedAt        time.Time
	MIME              []byte
	InternetMessageID string
	ConversationID    string
}

// inboxItem is a message held in an Inbox: its id, whether it was read, and
// the serial of its last change.
type inboxItem struct {
	InboxMessage
	id     string
	isRead bool
	seq    int
}

// mailDeltaState is what a message delta token stands for: the user, the
// fresh query's filter (zero for none), the changes after since up to upto,
// and, for a $skiptoken, how many are answered; selected is $select's.
type mailDeltaState struct {
	user        string
	after       time.Time
	strict      bool // gt rather than ge
	selected    string
	since, upto int
	offset      int
}

// inboxDeltaRoute is the path of a user's Inbox delta: the folder by its
// well-known name or its id, whatever the case.
var inboxDeltaRoute = regexp.MustCompile(`(?i)^/users/([^/]+)/mailfolders/([^/]+)/messages/delta$`)

// receivedFilter is the one $filter a message delta takes.
var receivedFilter = regexp.MustCompile(`^receivedDateTime (ge|gt) (\S+)$`)

// AddInboxMessage delivers m to the user's Inbox and answers its id, an
// immutable-looking one unless m names its own.
func (g *FakeGraph) AddInboxMessage(userID string, m InboxMessage) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	id := uuid.New()
	g.serial++
	item := &inboxItem{InboxMessage: m, id: "AAkALgAAAAAAHYQDEapmEc2byACqAC-EWg0A" + base64.URLEncoding.EncodeToString(id[:]), seq: g.serial}
	item.MIME = slices.Clone(m.MIME)
	if m.ID != "" {
		item.id = m.ID
	}
	mb := g.mailboxOf(userID)
	mb.inbox = append(mb.inbox, item)
	return item.id
}

// RemoveInboxMessage deletes the user's message id from the Inbox, as a
// person deleting or moving it would; false when there is no such message.
func (g *FakeGraph) RemoveInboxMessage(userID, id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	mb := g.mailboxOf(userID)
	i := slices.IndexFunc(mb.inbox, func(m *inboxItem) bool { return m.id == id })
	if i < 0 {
		return false
	}
	mb.inbox = slices.Delete(mb.inbox, i, i+1)
	g.serial++
	mb.inboxRemoved = append(mb.inboxRemoved, tombstone{id: id, seq: g.serial})
	return true
}

// MarkInboxMessageRead marks the user's message id read, a change the delta
// answers as an update; false when there is no such message.
func (g *FakeGraph) MarkInboxMessageRead(userID, id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	mb := g.mailboxOf(userID)
	i := slices.IndexFunc(mb.inbox, func(m *inboxItem) bool { return m.id == id })
	if i < 0 {
		return false
	}
	g.serial++
	mb.inbox[i].isRead, mb.inbox[i].seq = true, g.serial
	return true
}

// ExpireMailDelta makes every message delta token of the user's answer
// status with the code syncStateNotFound until a fresh query; 0 lifts it.
func (g *FakeGraph) ExpireMailDelta(userID string, status int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mailboxOf(userID).mailDeltaExpired = status
}

// inboxID is the id the fake answers for u's Inbox.
func inboxID(u *User) string {
	return "AQMkADinbox" + strings.ReplaceAll(u.ID, "-", "")
}

// inboxDelta answers GET /users/{userID}/mailFolders/{folder}/messages/delta.
// g.mu is held.
func (g *FakeGraph) inboxDelta(userID, folder string, query url.Values, prefer string) answer {
	u := g.userByID(userID, false)
	if u == nil {
		return notFound(userID)
	}
	if a, refused := mailboxRefused(u); refused {
		return a
	}
	if !strings.EqualFold(folder, "inbox") && folder != inboxID(u) {
		return itemNotFound()
	}
	mb := g.mailboxOf(u.ID)
	key := strings.ToLower(u.ID)
	var st mailDeltaState
	skip, deltaTok := query.Get("$skiptoken"), query.Get("$deltatoken")
	switch {
	case skip == "" && deltaTok == "":
		st = mailDeltaState{user: key, selected: query.Get("$select"), upto: g.serial}
		if f := query.Get("$filter"); f != "" {
			m := receivedFilter.FindStringSubmatch(f)
			if m == nil {
				return graphError(http.StatusBadRequest, "ErrorInvalidUrlQuery", "The query filter contains one or more invalid nodes.")
			}
			after, err := time.Parse(time.RFC3339, m[2])
			if err != nil {
				return graphError(http.StatusBadRequest, "ErrorInvalidUrlQuery", "The DateTimeOffset of the filter does not parse.")
			}
			st.after, st.strict = after, m[1] == "gt"
		}
		mb.mailDeltaExpired = 0
	default:
		if mb.mailDeltaExpired != 0 {
			return graphError(mb.mailDeltaExpired, "syncStateNotFound", "The sync state generation is not found.")
		}
		known, ok := g.mailDeltas[skip+deltaTok]
		if !ok || known.user != key || (skip != "" && deltaTok != "") {
			return graphError(http.StatusBadRequest, "syncStateNotFound", "The sync state generation is not found.")
		}
		st = *known
		if deltaTok != "" {
			st = mailDeltaState{user: key, selected: known.selected, since: known.upto, upto: g.serial}
		}
	}

	size := 10
	if m := deltaPageSize.FindStringSubmatch(prefer); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			size = n
		}
	}
	if g.DeltaPageSize > 0 {
		size = min(size, g.DeltaPageSize)
	}

	changes := g.inboxChanges(mb, st)
	end := min(st.offset+size, len(changes))
	value := make([]any, 0, end-st.offset)
	for _, c := range changes[st.offset:end] {
		value = append(value, c)
	}
	body := map[string]any{"value": value}
	link := "https://" + GraphHost + "/v1.0/users/" + u.ID + "/mailfolders/" + inboxID(u) + "/messages/delta?"
	if end < len(changes) {
		next := st
		next.offset = end
		body["@odata.nextLink"] = link + "$skiptoken=" + g.mailDeltaToken(next)
	} else {
		body["@odata.deltaLink"] = link + "$deltatoken=" + g.mailDeltaToken(mailDeltaState{user: key, selected: st.selected, upto: st.upto})
	}
	return answer{status: http.StatusOK, body: body}
}

// mailDeltaToken keeps st under a fresh token and answers it.
func (g *FakeGraph) mailDeltaToken(st mailDeltaState) string {
	if g.mailDeltas == nil {
		g.mailDeltas = map[string]*mailDeltaState{}
	}
	g.serial++
	token := "M" + strconv.Itoa(g.serial)
	g.mailDeltas[token] = &st
	return token
}

// inboxChanges are the Inbox's changes st stands for, in the order they
// were made: since 0 every message the filter keeps; else every message
// changed after since up to upto, whatever the filter, and every removal.
func (g *FakeGraph) inboxChanges(mb *mailbox, st mailDeltaState) []map[string]any {
	type entry struct {
		seq int
		out map[string]any
	}
	var all []entry
	for _, m := range mb.inbox {
		if m.seq > st.upto || m.seq <= st.since {
			continue
		}
		if st.since == 0 && !st.after.IsZero() &&
			(m.ReceivedAt.Before(st.after) || (st.strict && m.ReceivedAt.Equal(st.after))) {
			continue
		}
		all = append(all, entry{m.seq, inboxJSON(m, st.selected)})
	}
	if st.since > 0 {
		for _, r := range mb.inboxRemoved {
			if r.seq > st.since && r.seq <= st.upto {
				all = append(all, entry{r.seq, removedJSON(r.id)})
			}
		}
	}
	slices.SortStableFunc(all, func(a, b entry) int { return a.seq - b.seq })
	out := make([]map[string]any, len(all))
	for i, e := range all {
		out[i] = e.out
	}
	return out
}

// inboxJSON is m as the delta answers it, narrowed to selected's fields
// when it names any; id always.
func inboxJSON(m *inboxItem, selected string) map[string]any {
	subject := ""
	if msg, err := mail.ReadMessage(bytes.NewReader(m.MIME)); err == nil {
		subject = msg.Header.Get("Subject")
	}
	full := map[string]any{
		"id":                m.id,
		"receivedDateTime":  m.ReceivedAt.UTC().Format(time.RFC3339),
		"internetMessageId": m.InternetMessageID,
		"conversationId":    m.ConversationID,
		"isRead":            m.isRead,
		"subject":           subject,
	}
	if selected == "" {
		return full
	}
	out := map[string]any{"id": m.id}
	for _, f := range strings.Split(selected, ",") {
		if v, ok := full[strings.TrimSpace(f)]; ok {
			out[strings.TrimSpace(f)] = v
		}
	}
	return out
}

// inboxValue answers GET /users/{id}/messages/{id}/$value: the MIME of a
// message in u's Inbox. g.mu is held.
func (g *FakeGraph) inboxValue(u *User, id string) answer {
	for _, m := range g.mailboxOf(u.ID).inbox {
		if m.id == id {
			return answer{status: http.StatusOK, header: http.Header{"Content-Type": {"message/rfc822"}}, raw: slices.Clone(m.MIME)}
		}
	}
	return itemNotFound()
}
