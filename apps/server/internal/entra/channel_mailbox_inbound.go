package entra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the channel mailboxes' inbound reads (Outlook connection
// design M14; delivery D's plan readings 10 and 11), with the
// application's token, for a mailbox Check recorded in
// entra.channel_mailboxes alone — Send's gate exactly: the id parsed, the
// row required (ErrMailboxUnknown with no call), the provider ready, then
// the call.
//
// Delta reads one page of a folder's message delta, GET
// /users/{oid}/mailFolders/{folder}/messages/delta: afresh with
// $filter=receivedDateTime ge {since, UTC} and $select of the four fields
// the contract names; else a link an earlier page answered, followed only
// when it is this mailbox's message delta (messageDeltaPath). Every page is
// asked with Prefer: IdType="ImmutableId", odata.maxpagesize=50, so a
// message keeps its id when it is moved between the mailbox's folders.
// Raw reads a message's MIME, GET /users/{oid}/messages/{id}/$value, with
// immutable ids too, through the caller's cap.
//
// Neither decodes, logs or answers anything of what a message is about:
// the delta's $select names no subject, sender or body, and Graph's words
// on a refusal are not logged (noWords).

// inboxDeltaSelect is what a fresh Delta reads of each message.
const inboxDeltaSelect = "id,receivedDateTime,internetMessageId,conversationId"

// mailMaxMessageID bounds the Internet Message-Id a delta answers, RFC
// 5322's line; a longer one is answered as none.
const mailMaxMessageID = 998

// itemGoneCodes are the codes of Graph's 404 for a message that is no
// longer in the mailbox.
var itemGoneCodes = []string{"ErrorItemNotFound"}

// recorded is mailbox's object id when Check recorded it in
// entra.channel_mailboxes: ErrMailboxUnknown when it is no id or no row.
func (m *channelMailbox) recorded(ctx context.Context, mailbox string) (uuid.UUID, error) {
	oid, err := uuid.Parse(mailbox)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("%w: not a mailbox id", contracts.ErrMailboxUnknown)
	}
	if _, err := store.New(m.deps.Pool).GetChannelMailbox(ctx, oid); errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, fmt.Errorf("%w: no Check resolved the mailbox", contracts.ErrMailboxUnknown)
	} else if err != nil {
		return uuid.UUID{}, fmt.Errorf("entra: read the channel mailbox: %w", err)
	}
	return oid, nil
}

// mailFolder reports whether folder may name a folder in a path: a
// well-known name ("inbox") or a folder's id, one segment either way.
func mailFolder(folder string) bool {
	return validID(folder) && !strings.ContainsAny(folder, `/\?#`)
}

// messageDeltaBase is the path of the message delta of oid's folder.
func messageDeltaBase(oid uuid.UUID, folder string) string {
	return userPath(oid, "/mailFolders/", url.PathEscape(folder), "/messages/delta")
}

// messageDeltaPath is the path, after /v1.0, to send for link, refused
// unless link is a link of oid's message delta — so a stored link, or one
// Graph answered, can never point the client at another mailbox or off
// Graph. The link must parse as https on Graph's host exactly, with no user
// and no fragment, no percent-encoding in its path, a query, and the path
// /v1.0/users/{oid}/mailFolders/{f}/messages/delta, f being "inbox" or one
// segment that is a validID, compared whatever its case: Graph may answer
// mailfolders and the folder's id rather than its name. The path sent is
// ours, built from oid and folder, with the link's query alone after it —
// nothing of the link's path reaches the request.
func messageDeltaPath(oid uuid.UUID, folder, link string) (string, bool) {
	if strings.ContainsAny(link, "#\\ ") {
		return "", false
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host != graphAPIHost || u.User != nil || u.Opaque != "" ||
		u.Fragment != "" || u.RawPath != "" || u.RawQuery == "" {
		return "", false
	}
	prefix := graphAPIPath + userPath(oid, "/mailFolders/")
	if len(u.Path) <= len(prefix) || !strings.EqualFold(u.Path[:len(prefix)], prefix) {
		return "", false
	}
	segment, tail, _ := strings.Cut(u.Path[len(prefix):], "/")
	if !strings.EqualFold(tail, "messages/delta") || !mailFolder(segment) {
		return "", false
	}
	return messageDeltaBase(oid, folder) + "?" + u.RawQuery, true
}

// inboxDeltaItem is a message of a delta page as the provider decodes it:
// these fields and no other.
type inboxDeltaItem struct {
	ID                string          `json:"id"`
	Removed           json.RawMessage `json:"@removed"`
	ReceivedDateTime  string          `json:"receivedDateTime"`
	InternetMessageID string          `json:"internetMessageId"`
	ConversationID    string          `json:"conversationId"`
}

// mailItemOf is it as the contract answers it, false for one whose id is no
// validID or whose receivedDateTime does not read — left out rather than
// failing the page, which would hold the delta at it for ever. A removed
// message is its id alone.
func mailItemOf(it inboxDeltaItem) (contracts.MailItem, bool) {
	if !validID(it.ID) {
		return contracts.MailItem{}, false
	}
	if len(it.Removed) > 0 && string(it.Removed) != "null" {
		return contracts.MailItem{ID: it.ID, Removed: true}, true
	}
	received, err := time.Parse(time.RFC3339, it.ReceivedDateTime)
	if err != nil {
		return contracts.MailItem{}, false
	}
	out := contracts.MailItem{ID: it.ID, ReceivedAt: received.UTC(), MessageID: strings.TrimSpace(it.InternetMessageID)}
	if len(out.MessageID) > mailMaxMessageID {
		out.MessageID = ""
	}
	if validID(it.ConversationID) {
		out.ConversationID = it.ConversationID
	}
	return out, true
}

// Delta reads one page of the changes to folder of mailbox, a mailbox Check
// recorded (ErrMailboxUnknown otherwise, with no call). link "" starts
// afresh with the messages received at or after since; any other link is
// followed only when it is this mailbox's message delta (messageDeltaPath),
// else refused unsent. The page answers the next link, or at its end the
// delta link and Done — a link off the mailbox's delta is a failure. Graph
// refusing link — 410, or a 4xx with a sync state's code — is
// ErrMailboxDeltaExpired; the rest of the refusals are Send's
// (channelRefusal). The items are the page as Graph answered it, in no
// promised order; Prefer asks fifty.
func (m *channelMailbox) Delta(ctx context.Context, mailbox, folder, link string, since time.Time) (contracts.MailDelta, error) {
	oid, err := m.recorded(ctx, mailbox)
	if err != nil {
		return contracts.MailDelta{}, err
	}
	if !mailFolder(folder) {
		return contracts.MailDelta{}, errors.New("entra: not a mail folder")
	}
	var path string
	if link == "" {
		if since.IsZero() {
			return contracts.MailDelta{}, errors.New("entra: a fresh delta names no instant to start from")
		}
		path = messageDeltaBase(oid, folder) + "?" + query(url.Values{
			"$filter": {"receivedDateTime ge " + since.UTC().Format(time.RFC3339)},
			"$select": {inboxDeltaSelect},
		})
	} else {
		var ok bool
		if path, ok = messageDeltaPath(oid, folder, link); !ok {
			return contracts.MailDelta{}, errors.New("entra: a delta link that is not this mailbox's")
		}
	}
	cred, err := m.ready(ctx)
	if err != nil {
		return contracts.MailDelta{}, err
	}
	var answer struct {
		Value     []inboxDeltaItem `json:"value"`
		NextLink  string           `json:"@odata.nextLink"`
		DeltaLink string           `json:"@odata.deltaLink"`
	}
	if err := m.client.get(ctx, cred, path, &answer, noWords, mailDeltaPage); err != nil {
		if deltaExpired(err) {
			return contracts.MailDelta{}, fmt.Errorf("%w: %w", contracts.ErrMailboxDeltaExpired, err)
		}
		return contracts.MailDelta{}, channelRefusal(err)
	}
	out := contracts.MailDelta{Items: make([]contracts.MailItem, 0, len(answer.Value))}
	skipped := 0
	for _, it := range answer.Value {
		item, ok := mailItemOf(it)
		if !ok {
			skipped++
			continue
		}
		out.Items = append(out.Items, item)
	}
	if skipped > 0 {
		m.client.logger.DebugContext(ctx, "entra: a mail delta page held messages whose ids or times do not read",
			"tenant", cred.tenantID.String(), "mailbox", oid.String(), "skipped", skipped)
	}
	switch {
	case answer.DeltaLink != "":
		if _, ok := messageDeltaPath(oid, folder, answer.DeltaLink); !ok {
			return contracts.MailDelta{}, fmt.Errorf("%w: a delta link off the mailbox's message delta", errTenantUnreachable)
		}
		out.Link, out.Done = answer.DeltaLink, true
	case answer.NextLink != "":
		if _, ok := messageDeltaPath(oid, folder, answer.NextLink); !ok {
			return contracts.MailDelta{}, fmt.Errorf("%w: a next page off the mailbox's message delta", errTenantUnreachable)
		}
		out.Link = answer.NextLink
	default:
		return contracts.MailDelta{}, fmt.Errorf("%w: a delta page with neither a next nor a delta link", errTenantUnreachable)
	}
	return out, nil
}

// Raw reads the MIME of mailbox's message messageID, at most max bytes of
// it, from a mailbox Check recorded (ErrMailboxUnknown otherwise, with no
// call); an id that is no validID is refused unsent. More than max is
// ErrMailTooLarge; a 404 ErrorItemNotFound — deleted or moved since the
// delta named it — ErrMailItemGone; the rest of the refusals are Send's.
func (m *channelMailbox) Raw(ctx context.Context, mailbox, messageID string, max int64) ([]byte, error) {
	oid, err := m.recorded(ctx, mailbox)
	if err != nil {
		return nil, err
	}
	if !validID(messageID) {
		return nil, errors.New("entra: not a message id")
	}
	if max <= 0 {
		return nil, errors.New("entra: a raw read takes no byte")
	}
	cred, err := m.ready(ctx)
	if err != nil {
		return nil, err
	}
	data, err := m.client.getRaw(ctx, cred, userPath(oid, "/messages/", url.PathEscape(messageID), "/$value"), max)
	switch {
	case err == nil:
		return data, nil
	case errors.Is(err, errResponseTooLarge):
		return nil, fmt.Errorf("%w: %w", contracts.ErrMailTooLarge, err)
	case isStatus(err, http.StatusNotFound) && isCode(err, itemGoneCodes...):
		return nil, fmt.Errorf("%w: %w", contracts.ErrMailItemGone, err)
	}
	return nil, channelRefusal(err)
}
