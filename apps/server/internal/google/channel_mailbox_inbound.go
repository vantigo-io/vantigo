package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the channel mailboxes' inbound reads (Google Workspace design
// G34; delivery F), for a mailbox Check recorded — Send's gate exactly.
//
// Delta reads one page of the changes to the mailbox's Inbox. A fresh read
// takes users.getProfile's historyId first, then lists messages.list
// (labelIds=INBOX, q=after:<since>) page by page; its last page answers the
// history link, Done. A continuing link reads history.list from its start
// (labelId=INBOX, the four history types), page by page under that start,
// and the last page's historyId is the next start. A message added with
// INBOX, or given it, is an item; INBOX taken off or the message deleted is
// Removed, the last record of a message winning. Each new item is
// completed by its metadata — Message-ID, internalDate, threadId — and a 404
// there is Removed. A history page naming more messages than a page holds
// keeps the rest in the link's queue, answered before the next page is read.
// Raw reads a message's RFC 5322 bytes, format=raw, through a cap made of
// the caller's.
//
// Neither reads, decodes or logs what a message says: the metadata asks for
// the Message-ID header alone.

// The delta's figures.
const (
	// mailLinkMax bounds a link the caller hands back: the queue of a large
	// history page included.
	mailLinkMax = 64 << 10
	// mailPageTokenMax bounds a page token a link carries.
	mailPageTokenMax = 1024
	// mailFolderInbox is the one folder the delta reads.
	mailFolderInbox = "inbox"
	// rawAllowance is what a raw answer may hold besides the base64url of
	// the message: the JSON around it.
	rawAllowance = 64 << 10
)

// historyIDPattern is a history id: an unsigned 64-bit decimal.
var historyIDPattern = regexp.MustCompile(`^[0-9]{1,20}$`)

// mailLink is the provider's delta link (G34): the mailbox it is for (m),
// the history id to read from (h), the page token of the listing or the
// history page under way (p), whether it is the fresh listing (l) and the
// instant it lists after (a, Unix seconds), and the queue of a history page
// larger than a delta page (q: ids, "-" before a removed one). A link with
// neither l nor p is a delta read to its end: the next read starts from h.
type mailLink struct {
	M string   `json:"m"`
	H string   `json:"h"`
	P string   `json:"p,omitempty"`
	L bool     `json:"l,omitempty"`
	A int64    `json:"a,omitempty"`
	Q []string `json:"q,omitempty"`
}

// encode is the link as the contract carries it: g1. and the base64url of
// its JSON.
func (l mailLink) encode() string {
	b, _ := json.Marshal(l)
	return deltaLinkPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// done reports whether the link is a read to its end.
func (l mailLink) done() bool { return !l.L && l.P == "" && len(l.Q) == 0 }

// parseMailLink reads a link the provider answered for mailbox, false for
// one it did not: not g1., too long, another mailbox's, or any of its parts
// not as the provider writes them.
func parseMailLink(link, mailbox string) (mailLink, bool) {
	rest, ok := strings.CutPrefix(link, deltaLinkPrefix)
	if !ok || len(link) > mailLinkMax {
		return mailLink{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return mailLink{}, false
	}
	var l mailLink
	if json.Unmarshal(raw, &l) != nil || l.M != mailbox || !historyIDPattern.MatchString(l.H) ||
		len(l.P) > mailPageTokenMax || strings.ContainsFunc(l.P, func(r rune) bool { return r < 0x21 || r > 0x7e }) ||
		(l.L && l.A <= 0) || (!l.L && l.A != 0) || len(l.Q) > 5000 {
		return mailLink{}, false
	}
	for _, q := range l.Q {
		if !validGmailID(strings.TrimPrefix(q, "-")) {
			return mailLink{}, false
		}
	}
	return l, true
}

// Delta reads one page of the changes to folder ("inbox") of mailbox, a
// mailbox Check recorded (ErrMailboxUnknown otherwise, with no call). link
// "" starts afresh with the messages received after since; any other link
// must be one the provider answered for this mailbox, else it is refused
// unsent. A history start Gmail no longer has — 404, or 400 — and a listing's
// page token refused 400 are ErrMailboxDeltaExpired (U15); the rest of the
// refusals are Send's.
func (m *channelMailbox) Delta(ctx context.Context, mailbox, folder, link string, since time.Time) (contracts.MailDelta, error) {
	if !validUserID(mailbox) {
		return contracts.MailDelta{}, fmt.Errorf("%w: not a mailbox id", contracts.ErrMailboxUnknown)
	}
	if folder != mailFolderInbox {
		return contracts.MailDelta{}, errors.New("google: not a mail folder the provider reads")
	}
	var l mailLink
	if link != "" {
		var ok bool
		if l, ok = parseMailLink(link, mailbox); !ok {
			return contracts.MailDelta{}, errors.New("google: a delta link that is not this mailbox's")
		}
	} else if since.IsZero() {
		return contracts.MailDelta{}, errors.New("google: a fresh delta names no instant to start from")
	}
	s, err := m.open(ctx, mailbox)
	if err != nil {
		return contracts.MailDelta{}, err
	}
	switch {
	case link == "":
		var profile struct {
			HistoryID string `json:"historyId"`
		}
		if err := m.keys.c.get(ctx, s.modify, s.gmailURL("/profile", url.Values{"fields": {"historyId"}}), &profile); err != nil {
			if isStatus(err, http.StatusNotFound) {
				return contracts.MailDelta{}, fmt.Errorf("%w: %w", contracts.ErrMailboxGone, err)
			}
			return contracts.MailDelta{}, gmailRefusal(err)
		}
		if !historyIDPattern.MatchString(profile.HistoryID) {
			return contracts.MailDelta{}, fmt.Errorf("%w: the mailbox's history id does not read", errGoogleUnreachable)
		}
		after := max(since.Unix()-1, 1)
		return m.listPage(ctx, s, mailLink{M: mailbox, H: profile.HistoryID, L: true, A: after})
	case len(l.Q) > 0:
		return m.queuePage(ctx, s, l)
	case l.L:
		return m.listPage(ctx, s, l)
	default:
		return m.historyPage(ctx, s, l)
	}
}

// listPage is one page of the fresh listing: the Inbox's messages received
// after l.A, each completed by its metadata. Its last page answers the
// history link from l.H, Done.
func (m *channelMailbox) listPage(ctx context.Context, s gmailSession, l mailLink) (contracts.MailDelta, error) {
	q := url.Values{
		"labelIds":   {labelInbox},
		"q":          {"after:" + strconv.FormatInt(l.A, 10)},
		"maxResults": {strconv.Itoa(contracts.MailDeltaPageMax)},
		"fields":     {"messages(id),nextPageToken"},
	}
	if l.P != "" {
		q.Set("pageToken", l.P)
	}
	var answer struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := m.keys.c.get(ctx, s.modify, s.gmailURL("/messages", q), &answer); err != nil {
		if l.P != "" && isStatus(err, http.StatusBadRequest) {
			return contracts.MailDelta{}, fmt.Errorf("%w: %w", contracts.ErrMailboxDeltaExpired, err)
		}
		return contracts.MailDelta{}, gmailRefusal(err)
	}
	ids := make([]string, 0, len(answer.Messages))
	for _, msg := range answer.Messages {
		ids = append(ids, msg.ID)
	}
	items, err := m.resolve(ctx, s, ids)
	if err != nil {
		return contracts.MailDelta{}, err
	}
	next := mailLink{M: l.M, H: l.H}
	if answer.NextPageToken != "" {
		next.L, next.A, next.P = true, l.A, answer.NextPageToken
	}
	return m.page(items, next)
}

// historyRecord is one record of history.list as the provider decodes it:
// the messages added, deleted, or labelled, with their labels.
type historyRecord struct {
	MessagesAdded   []historyMessage `json:"messagesAdded"`
	MessagesDeleted []historyMessage `json:"messagesDeleted"`
	LabelsAdded     []historyMessage `json:"labelsAdded"`
	LabelsRemoved   []historyMessage `json:"labelsRemoved"`
}

// historyMessage is a message a history record names, and the labels it
// added or removed (labelIds), or the message's own labels for an add.
type historyMessage struct {
	Message struct {
		ID       string   `json:"id"`
		LabelIDs []string `json:"labelIds"`
	} `json:"message"`
	LabelIDs []string `json:"labelIds"`
}

// historyPage is one page of history.list from l.H: the Inbox's changes,
// the last word on each message kept, in the order they first appear. A
// page with a next one answers it under the same start; the last answers
// the next start, Gmail's historyId. More messages than a delta page holds
// carry over in the link's queue.
func (m *channelMailbox) historyPage(ctx context.Context, s gmailSession, l mailLink) (contracts.MailDelta, error) {
	q := url.Values{
		"startHistoryId": {l.H},
		"labelId":        {labelInbox},
		"historyTypes":   {"messageAdded", "labelAdded", "labelRemoved", "messageDeleted"},
		"maxResults":     {strconv.Itoa(contracts.MailDeltaPageMax)},
	}
	if l.P != "" {
		q.Set("pageToken", l.P)
	}
	var answer struct {
		History       []historyRecord `json:"history"`
		NextPageToken string          `json:"nextPageToken"`
		HistoryID     string          `json:"historyId"`
	}
	if err := m.keys.c.get(ctx, s.modify, s.gmailURL("/history", q), &answer); err != nil {
		if isStatus(err, http.StatusNotFound) || isStatus(err, http.StatusBadRequest) {
			return contracts.MailDelta{}, fmt.Errorf("%w: %w", contracts.ErrMailboxDeltaExpired, err)
		}
		return contracts.MailDelta{}, gmailRefusal(err)
	}
	var order []string
	inbox := map[string]bool{}
	note := func(id string, in bool) {
		if !validGmailID(id) {
			return
		}
		if _, seen := inbox[id]; !seen {
			order = append(order, id)
		}
		inbox[id] = in
	}
	for _, rec := range answer.History {
		for _, a := range rec.MessagesAdded {
			note(a.Message.ID, a.Message.LabelIDs == nil || containsLabel(a.Message.LabelIDs, labelInbox))
		}
		for _, a := range rec.LabelsAdded {
			if containsLabel(a.LabelIDs, labelInbox) {
				note(a.Message.ID, true)
			}
		}
		for _, r := range rec.LabelsRemoved {
			if containsLabel(r.LabelIDs, labelInbox) {
				note(r.Message.ID, false)
			}
		}
		for _, d := range rec.MessagesDeleted {
			note(d.Message.ID, false)
		}
	}
	queue := make([]string, 0, len(order))
	for _, id := range order {
		if inbox[id] {
			queue = append(queue, id)
		} else {
			queue = append(queue, "-"+id)
		}
	}
	next := mailLink{M: l.M, H: l.H}
	switch {
	case answer.NextPageToken != "":
		next.P = answer.NextPageToken
	case historyIDPattern.MatchString(answer.HistoryID):
		next.H = answer.HistoryID
	}
	next.Q = queue
	return m.queuePage(ctx, s, next)
}

// queuePage answers the first MailDeltaPageMax of l's queue, completed, and
// the link with the rest; Done once the queue is spent and the read is at
// its end.
func (m *channelMailbox) queuePage(ctx context.Context, s gmailSession, l mailLink) (contracts.MailDelta, error) {
	take := l.Q[:min(len(l.Q), contracts.MailDeltaPageMax)]
	var added []string
	var removed []contracts.MailItem
	for _, q := range take {
		if id, ok := strings.CutPrefix(q, "-"); ok {
			removed = append(removed, contracts.MailItem{ID: id, Removed: true})
			continue
		}
		added = append(added, q)
	}
	items, err := m.resolve(ctx, s, added)
	if err != nil {
		return contracts.MailDelta{}, err
	}
	next := l
	next.Q = nil
	if rest := l.Q[len(take):]; len(rest) > 0 {
		next.Q = append([]string(nil), rest...)
	}
	return m.page(append(items, removed...), next)
}

// page is a delta page of items whose link is next.
func (m *channelMailbox) page(items []contracts.MailItem, next mailLink) (contracts.MailDelta, error) {
	link := next.encode()
	if len(link) > mailLinkMax {
		return contracts.MailDelta{}, fmt.Errorf("%w: a delta link longer than the provider keeps", errGoogleUnreachable)
	}
	if items == nil {
		items = []contracts.MailItem{}
	}
	return contracts.MailDelta{Items: items, Link: link, Done: next.done()}, nil
}

// resolve completes each message id by its metadata: the Message-ID,
// internalDate as ReceivedAt and threadId as ConversationID; a message gone
// meanwhile (404) is Removed. An id that is no Gmail id is left out.
func (m *channelMailbox) resolve(ctx context.Context, s gmailSession, ids []string) ([]contracts.MailItem, error) {
	out := make([]contracts.MailItem, 0, len(ids))
	for _, id := range ids {
		if !validGmailID(id) {
			continue
		}
		meta, err := m.metadata(ctx, s, id)
		switch {
		case isStatus(err, http.StatusNotFound):
			out = append(out, contracts.MailItem{ID: id, Removed: true})
			continue
		case err != nil:
			return nil, gmailRefusal(err)
		}
		item := contracts.MailItem{ID: id, ReceivedAt: meta.receivedAt(), MessageID: meta.messageID()}
		if validGmailID(meta.ThreadID) {
			item.ConversationID = meta.ThreadID
		}
		out = append(out, item)
	}
	return out, nil
}

// containsLabel reports whether labels holds label.
func containsLabel(labels []string, label string) bool {
	for _, l := range labels {
		if l == label {
			return true
		}
	}
	return false
}

// Raw reads the RFC 5322 bytes of mailbox's message messageID, at most max
// of them, from a mailbox Check recorded (ErrMailboxUnknown otherwise, with
// no call): messages.get(format=raw), read through the base64url of max and
// the JSON around it. More than max, either way, is ErrMailTooLarge; a 404 —
// deleted since the delta named it — ErrMailItemGone; the rest of the
// refusals are Send's.
func (m *channelMailbox) Raw(ctx context.Context, mailbox, messageID string, max int64) ([]byte, error) {
	if !validGmailID(messageID) {
		return nil, errors.New("google: not a message id")
	}
	if max <= 0 {
		return nil, errors.New("google: a raw read takes no byte")
	}
	s, err := m.open(ctx, mailbox)
	if err != nil {
		return nil, err
	}
	limit := (max+2)/3*4 + rawAllowance
	var answer struct {
		Raw string `json:"raw"`
	}
	err = m.keys.c.do(ctx, s.modify, http.MethodGet,
		s.gmailURL("/messages/"+url.PathEscape(messageID), url.Values{"format": {"raw"}, "fields": {"raw"}}),
		nil, &answer, limit, shownAs(gmailShownBase+"/messages/{id}"))
	switch {
	case errors.Is(err, errResponseTooLarge):
		return nil, fmt.Errorf("%w: %w", contracts.ErrMailTooLarge, err)
	case err != nil:
		return nil, itemRefusal(err)
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(answer.Raw, "="))
	if err != nil {
		return nil, fmt.Errorf("%w: the raw message does not decode", errGoogleUnreachable)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: %d bytes", contracts.ErrMailTooLarge, len(data))
	}
	return data, nil
}
