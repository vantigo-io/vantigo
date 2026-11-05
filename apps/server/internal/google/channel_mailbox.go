package google

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's contracts.ChannelMailbox (Google Workspace design
// G33, G34; delivery F, docs/superpowers/plans/2026-11-03-google-mail-channels.md):
// the organisation's licensed Gmail mailboxes Communications' channels send
// through and read, reached through domain-wide delegation — every Gmail call
// impersonates the channel's mailbox with gmail.modify or gmail.send, never a
// person's own connection. Entra's channel_mailbox*.go is the template.
//
// Status reads the connection's row and the credentials and nothing else:
// available while the Mail channels switch is on, the connection connected
// and the last test granted both Gmail scopes — always with the reason
// tenant_wide, since domain-wide delegation reaches every mailbox in the
// domain (the channels page's warning); else unavailable with
// mailChannelsState's reason.
//
// Check resolves an address as the admin to impersonate: one outside the
// Workspace's verified domains is ErrMailboxUnknown with no call, a group's
// is refused (ErrMailboxGroup, P2), an unknown one ErrMailboxUnknown, a
// suspended or archived user ErrMailboxGone, an alias its user. A person's
// own mailbox — a user the module keeps an account for (a person Staff
// manages, by id or by address) or one who connected their own account — is
// answered Shared false and nothing more, and any row of it deleted: no call
// ever reaches a person's own mailbox. Any other user's mailbox is read once
// as itself (users.getProfile), which proves the delegation reaches it and
// the user has Gmail, and is recorded in google.channel_mailboxes. Send,
// Delta, Raw, Mark and EnsureFolder answer only for a recorded mailbox
// (ErrMailboxUnknown with no call otherwise) that is not, by now, a person's
// own (ErrMailboxGone with no call), so Communications can never point the
// provider at a mailbox no Check resolved. Before a fresh delta, and once a
// day otherwise, the mailbox is resolved again as the admin
// (users.get(<id>)): its primary address changed, or the user suspended or
// archived, is ErrMailboxGone. Every Gmail path is users/me — the token's
// subject names the mailbox — and every Gmail URL carries quotaUser, the
// mailbox's Directory id.
//
// Google's words are never logged, nor is an address: the client logs a
// refusal by its status and reason, a path carrying the typed address is
// logged without it (shownAs), and Check logs the Directory id.

// The channel mailboxes' figures and names.
const (
	// channelMailboxName is the provider's name as an admin reads it.
	channelMailboxName = "Google Workspace"
	// gmailMaxMIMEBytes is the largest MIME Send takes (P4, U10): Gmail's
	// 25 MB a message; the upload endpoint takes up to 35 MB.
	gmailMaxMIMEBytes = 25_000_000
	// gmailBase is the Gmail API's users/me base: the delegated token's
	// subject is the mailbox.
	gmailBase = "https://gmail.googleapis.com/gmail/v1/users/me"
	// gmailShownBase is gmailBase's path, as the log names a call.
	gmailShownBase = "/gmail/v1/users/me"
	// gmailSendURL is the media upload of messages.send: the MIME whole, as
	// message/rfc822.
	gmailSendURL = "https://gmail.googleapis.com/upload/gmail/v1/users/me/messages/send?uploadType=media"
	// channelMaxAddress bounds an address Check takes, RFC 5321's path.
	channelMaxAddress = 320
	// gmailMaxID bounds an id Gmail answers for a message, a thread or a
	// label.
	gmailMaxID = 128
	// mailMaxMessageID bounds a Message-ID read back, RFC 5322's line; a
	// longer one is answered as none.
	mailMaxMessageID = 998
	// channelRecheck is how long a recorded mailbox is trusted before a call
	// resolves it again as the admin.
	channelRecheck = 24 * time.Hour
	// readBackBudget bounds Send's read-back of the Message-ID, on a context
	// of its own: an upload that took its whole budget does not lose it.
	readBackBudget = 10 * time.Second
)

// gmailIDPattern is a message's, a thread's or a label's id as Gmail answers
// it: hex for a message and a thread, INBOX or Label_12 for a label.
var gmailIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validGmailID reports whether id is one Gmail could have answered.
func validGmailID(id string) bool {
	return id != "" && len(id) <= gmailMaxID && gmailIDPattern.MatchString(id)
}

// The Gmail labels the provider names.
const (
	labelInbox  = "INBOX"
	labelUnread = "UNREAD"
)

// channelMailbox is the module's contracts.ChannelMailbox over the keyring.
type channelMailbox struct {
	deps module.Deps
	keys *keyring
	q    *store.Queries
}

var _ contracts.ChannelMailbox = (*channelMailbox)(nil)

// newChannelMailbox builds the module's channel mailbox provider over d,
// for Compose and Workers alike.
func newChannelMailbox(d module.Deps) contracts.ChannelMailbox {
	q := store.New(d.Pool)
	return &channelMailbox{deps: d, keys: newKeyring(q, newGoogleClient(d)), q: q}
}

// Key is "google", a channel's provider value.
func (m *channelMailbox) Key() string { return providerKey }

// DisplayName is "Google Workspace".
func (m *channelMailbox) DisplayName() string { return channelMailboxName }

// MaxMessage is gmailMaxMIMEBytes.
func (m *channelMailbox) MaxMessage() int64 { return gmailMaxMIMEBytes }

// Status is whether channels may use the provider now, from the module's
// rows with no network: available with tenant_wide, or unavailable with
// mailChannelsState's reason.
func (m *channelMailbox) Status(ctx context.Context) (contracts.MailboxStatus, error) {
	conn, creds, err := readFooting(ctx, m.q)
	if err != nil {
		return contracts.MailboxStatus{}, err
	}
	return channelStatusOf(conn, creds), nil
}

// channelStatusOf is the provider's status over the connection's row.
func channelStatusOf(conn store.GoogleConnection, creds []store.GoogleCredential) contracts.MailboxStatus {
	if ok, reason := mailChannelsState(conn, creds); !ok {
		return contracts.MailboxStatus{State: contracts.MailboxUnavailable, Reason: reason}
	}
	return contracts.MailboxStatus{State: contracts.MailboxAvailable, Reason: contracts.MailboxTenantWide}
}

// ready is the connection once Status is available: ErrMailboxUnavailable
// with the reason otherwise.
func (m *channelMailbox) ready(ctx context.Context) (store.GoogleConnection, error) {
	conn, creds, err := readFooting(ctx, m.q)
	if err != nil {
		return store.GoogleConnection{}, err
	}
	if ok, reason := mailChannelsState(conn, creds); !ok {
		return store.GoogleConnection{}, fmt.Errorf("%w (%s)", contracts.ErrMailboxUnavailable, reason)
	}
	return conn, nil
}

// gmailSession is one call's footing on a recorded mailbox: its Directory
// id (the quotaUser), and the token sources of the two scopes, each
// impersonating the mailbox's address.
type gmailSession struct {
	gid          string
	modify, send tokenSource
}

// open is a call's footing for mailbox: a row of google.channel_mailboxes
// (ErrMailboxUnknown otherwise, with no call), not a person's own mailbox by
// now — an account the module keeps for a person under its id or its
// address, or a person's own connection (ErrMailboxGone, with no call, P2) —
// then the provider ready (ErrMailboxUnavailable otherwise). All are read on
// the pool and released before any call reaches Google. When resolve is set
// (a fresh delta), or the row was last resolved channelRecheck ago, the
// mailbox is resolved again as the admin (recheck).
func (m *channelMailbox) open(ctx context.Context, mailbox string, resolve bool) (gmailSession, error) {
	if !validUserID(mailbox) {
		return gmailSession{}, fmt.Errorf("%w: not a mailbox id", contracts.ErrMailboxUnknown)
	}
	row, err := m.q.GetChannelMailbox(ctx, mailbox)
	if errors.Is(err, pgx.ErrNoRows) {
		return gmailSession{}, fmt.Errorf("%w: no Check resolved the mailbox", contracts.ErrMailboxUnknown)
	}
	if err != nil {
		return gmailSession{}, fmt.Errorf("google: read the channel mailbox: %w", err)
	}
	held, err := m.q.PersonalMailboxHeld(ctx, store.PersonalMailboxHeldParams{GoogleUserID: row.GoogleUserID, Address: row.Address})
	if err != nil {
		return gmailSession{}, fmt.Errorf("google: read the accounts: %w", err)
	}
	if held {
		return gmailSession{}, fmt.Errorf("%w: the mailbox is a person's own", contracts.ErrMailboxGone)
	}
	conn, err := m.ready(ctx)
	if err != nil {
		return gmailSession{}, err
	}
	if resolve || !m.keys.c.now().Before(row.CheckedAt.Add(channelRecheck)) {
		if err := m.recheck(ctx, conn, row); err != nil {
			return gmailSession{}, err
		}
	}
	return gmailSession{
		gid:    row.GoogleUserID,
		modify: m.keys.tokenSource([]string{scopeGmailModify}, row.Address),
		send:   m.keys.tokenSource([]string{scopeGmailSend}, row.Address),
	}, nil
}

// recheck resolves row's mailbox again as the admin (users.get by its
// Directory id): a 404, a primary address other than the one recorded — the
// user renamed, the address now another's — or the user suspended or
// archived is ErrMailboxGone; the Directory's refusals are Check's. A
// mailbox still the user Check recorded has its checked_at moved on.
func (m *channelMailbox) recheck(ctx context.Context, conn store.GoogleConnection, row store.GoogleChannelMailbox) error {
	var user channelUser
	err := m.keys.c.get(ctx, m.keys.tokenSource([]string{scopeUsers}, deref(conn.AdminSubject)),
		userURL(row.GoogleUserID)+"?"+url.Values{"projection": {"basic"}, "fields": {"id,primaryEmail,suspended,archived"}}.Encode(),
		&user, shownAs("/admin/directory/v1/users/{user}"))
	switch {
	case isStatus(err, http.StatusNotFound):
		return fmt.Errorf("%w: the Directory holds the user no more", contracts.ErrMailboxGone)
	case err != nil:
		return directoryRefusal(err)
	}
	if user.ID != row.GoogleUserID || strings.ToLower(strings.TrimSpace(user.PrimaryEmail)) != row.Address {
		return fmt.Errorf("%w: the user's primary address is not the one recorded", contracts.ErrMailboxGone)
	}
	if user.Suspended || user.Archived {
		return fmt.Errorf("%w: the user is suspended or archived", contracts.ErrMailboxGone)
	}
	if err := m.q.TouchChannelMailbox(ctx, store.TouchChannelMailboxParams{
		GoogleUserID: row.GoogleUserID, CheckedAt: m.keys.c.now(),
	}); err != nil {
		return fmt.Errorf("google: record the channel mailbox checked: %w", err)
	}
	return nil
}

// gmailURL is a Gmail call's URL: gmailBase, path, the query and quotaUser
// the mailbox's Directory id.
func (s gmailSession) gmailURL(path string, query url.Values) string {
	if query == nil {
		query = url.Values{}
	}
	query.Set("quotaUser", s.gid)
	return gmailBase + path + "?" + query.Encode()
}

// gmailRefusal is a refusal of a Gmail call as the contract answers it
// (U17): the mailbox refused as the token's subject (a deleted, suspended or
// unknown user), a 400 failedPrecondition (Gmail not enabled for the user:
// no licence) and a 404 on the mailbox itself are ErrMailboxGone; a scope
// not delegated, or no key fetching a token, ErrMailboxUnavailable — the
// Owner's delegation to mend, held rather than refused; any other 403
// ErrMailboxDenied (the usage limits' 403s the client waited once on are
// errThrottled by now); a 413 ErrMailTooLarge. Anything else passes, a
// failure to retry.
func gmailRefusal(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errSubjectInvalid):
		return fmt.Errorf("%w: %w", contracts.ErrMailboxGone, err)
	case errors.Is(err, errNotDelegated):
		return fmt.Errorf("%w (%s): %w", contracts.ErrMailboxUnavailable, reasonNotDelegated, err)
	case errors.Is(err, errNoCredential), errors.Is(err, errCredentialInvalid), errors.Is(err, errNotConfigured):
		return fmt.Errorf("%w: %w", contracts.ErrMailboxUnavailable, err)
	case isStatus(err, http.StatusBadRequest) && isReason(err, "failedPrecondition"):
		return fmt.Errorf("%w: %w", contracts.ErrMailboxGone, err)
	case isStatus(err, http.StatusForbidden):
		return fmt.Errorf("%w: %w", contracts.ErrMailboxDenied, err)
	case isStatus(err, http.StatusRequestEntityTooLarge):
		return fmt.Errorf("%w: %w", contracts.ErrMailTooLarge, err)
	}
	return err
}

// itemRefusal is gmailRefusal for a call on one message: a 404 is the
// message gone (ErrMailItemGone), not the mailbox.
func itemRefusal(err error) error {
	if isStatus(err, http.StatusNotFound) {
		return fmt.Errorf("%w: %w", contracts.ErrMailItemGone, err)
	}
	return gmailRefusal(err)
}

// directoryRefusal is a refusal of Check's Directory reads, made as the
// admin to impersonate: a scope not delegated, no key, or the admin refused
// (403) is ErrMailboxUnavailable — the Owner's to mend on the Google
// Workspace page —; anything else passes.
func directoryRefusal(err error) error {
	switch {
	case errors.Is(err, errNotDelegated), errors.Is(err, errNoCredential), errors.Is(err, errCredentialInvalid),
		errors.Is(err, errNotConfigured), errors.Is(err, errSubjectInvalid), isStatus(err, http.StatusForbidden):
		return fmt.Errorf("%w: %w", contracts.ErrMailboxUnavailable, err)
	}
	return err
}

// channelAddress reports whether address is a bare address Check may send
// to the Directory: an addr-spec alone, no display name, no white space or
// control character, nothing that would change the path it is put in.
func channelAddress(address string) bool {
	if address == "" || len(address) > channelMaxAddress || strings.ContainsAny(address, `/\?#%`) {
		return false
	}
	for _, r := range address {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	a, err := mail.ParseAddress(address)
	return err == nil && a.Name == "" && a.Address == address
}

// inDomains reports whether address's domain is one of the Workspace's
// verified domains, as the last test read them, ignoring case.
func inDomains(address string, domains []string) bool {
	at := strings.LastIndexByte(address, '@')
	return at > 0 && domainAllowed(address[at+1:], domains)
}

// channelUser is what Check reads of the user.
type channelUser struct {
	ID           string `json:"id"`
	PrimaryEmail string `json:"primaryEmail"`
	Suspended    bool   `json:"suspended"`
	Archived     bool   `json:"archived"`
	Name         struct {
		FullName string `json:"fullName"`
	} `json:"name"`
}

// Check resolves address to a licensed user's mailbox and records it (G34,
// P2): groups.get(address) found is ErrMailboxGroup — a 404, or the 400
// Google may answer for a key that names no group, goes on —;
// users.get(address) 404 is ErrMailboxUnknown, a suspended or archived user
// ErrMailboxGone; a person's own mailbox — a user the module keeps an
// account for, by id or by primary address, or one with a connection of
// their own — is answered Shared false, its row deleted if one was
// recorded, with no call to it; then users.getProfile as the mailbox —
// refused as Send's are — and the row. An address that is not a bare
// addr-spec, or whose domain is none of the Workspace's verified domains, is
// ErrMailboxUnknown with no call; the provider not available
// ErrMailboxUnavailable. The typed address is
// never in an error or a log line.
func (m *channelMailbox) Check(ctx context.Context, address string) (contracts.ChannelMailboxInfo, error) {
	if !channelAddress(address) {
		return contracts.ChannelMailboxInfo{}, fmt.Errorf("%w: not an address", contracts.ErrMailboxUnknown)
	}
	conn, err := m.ready(ctx)
	if err != nil {
		return contracts.ChannelMailboxInfo{}, err
	}
	if !inDomains(address, conn.Domains) {
		return contracts.ChannelMailboxInfo{}, fmt.Errorf("%w: not an address of the Workspace's domains", contracts.ErrMailboxUnknown)
	}
	admin := deref(conn.AdminSubject)
	var group struct {
		ID string `json:"id"`
	}
	err = m.keys.c.get(ctx, m.keys.tokenSource([]string{scopeGroups}, admin), groupURL(address)+"?fields=id",
		&group, shownAs("/admin/directory/v1/groups/{group}"))
	switch {
	case err == nil:
		return contracts.ChannelMailboxInfo{}, fmt.Errorf("%w: the Directory holds a group of the address", contracts.ErrMailboxGroup)
	case isStatus(err, http.StatusNotFound), isStatus(err, http.StatusBadRequest):
	default:
		return contracts.ChannelMailboxInfo{}, directoryRefusal(err)
	}
	var user channelUser
	err = m.keys.c.get(ctx, m.keys.tokenSource([]string{scopeUsers}, admin),
		userURL(address)+"?"+url.Values{"projection": {"basic"}, "fields": {"id,primaryEmail,suspended,archived,name/fullName"}}.Encode(),
		&user, shownAs("/admin/directory/v1/users/{user}"))
	switch {
	case isStatus(err, http.StatusNotFound):
		return contracts.ChannelMailboxInfo{}, fmt.Errorf("%w: the Directory holds no user of the address", contracts.ErrMailboxUnknown)
	case err != nil:
		return contracts.ChannelMailboxInfo{}, directoryRefusal(err)
	}
	primary := strings.ToLower(strings.TrimSpace(user.PrimaryEmail))
	if !validUserID(user.ID) || !channelAddress(primary) {
		return contracts.ChannelMailboxInfo{}, fmt.Errorf("%w: the user's id or address does not read", errGoogleUnreachable)
	}
	if !inDomains(primary, conn.Domains) {
		return contracts.ChannelMailboxInfo{}, fmt.Errorf("%w: the user's address is not of the Workspace's domains", contracts.ErrMailboxUnknown)
	}
	if user.Suspended || user.Archived {
		return contracts.ChannelMailboxInfo{}, fmt.Errorf("%w: the user is suspended or archived", contracts.ErrMailboxGone)
	}
	info := contracts.ChannelMailboxInfo{Mailbox: user.ID, Address: primary, DisplayName: strings.TrimSpace(user.Name.FullName)}
	held, err := m.q.PersonalMailboxHeld(ctx, store.PersonalMailboxHeldParams{GoogleUserID: user.ID, Address: primary})
	if err != nil {
		return contracts.ChannelMailboxInfo{}, fmt.Errorf("google: read the accounts: %w", err)
	}
	if held {
		if err := m.q.DeleteChannelMailbox(ctx, user.ID); err != nil {
			return contracts.ChannelMailboxInfo{}, fmt.Errorf("google: forget the channel mailbox: %w", err)
		}
		m.keys.c.logger.InfoContext(ctx, "google: a channel mailbox checked", "mailbox", user.ID, "shared", false)
		return info, nil
	}
	s := gmailSession{gid: user.ID, modify: m.keys.tokenSource([]string{scopeGmailModify}, primary)}
	var profile struct {
		HistoryID string `json:"historyId"`
	}
	if err := m.keys.c.get(ctx, s.modify, s.gmailURL("/profile", url.Values{"fields": {"historyId"}}), &profile); err != nil {
		if isStatus(err, http.StatusNotFound) {
			return contracts.ChannelMailboxInfo{}, fmt.Errorf("%w: %w", contracts.ErrMailboxGone, err)
		}
		return contracts.ChannelMailboxInfo{}, gmailRefusal(err)
	}
	if err := m.q.UpsertChannelMailbox(ctx, store.UpsertChannelMailboxParams{
		GoogleUserID: user.ID, Address: primary, CheckedAt: m.keys.c.now(),
	}); err != nil {
		return contracts.ChannelMailboxInfo{}, fmt.Errorf("google: record the channel mailbox: %w", err)
	}
	info.Shared = true
	m.keys.c.logger.InfoContext(ctx, "google: a channel mailbox checked", "mailbox", user.ID, "shared", true)
	return info, nil
}

// Forget deletes the mailbox's row, so nothing is sent through it until
// another Check. A mailbox not recorded, or not an id, is done.
func (m *channelMailbox) Forget(ctx context.Context, mailbox string) error {
	if !validUserID(mailbox) {
		return nil
	}
	if err := m.q.DeleteChannelMailbox(ctx, mailbox); err != nil {
		return fmt.Errorf("google: forget the channel mailbox: %w", err)
	}
	return nil
}

// Send sends mime from mailbox, a row of google.channel_mailboxes
// (ErrMailboxUnknown otherwise, with no call) (G34): larger than MaxMessage
// ErrMailTooLarge, a blind copy the MIME's Bcc header does not carry
// ErrBccUnsupported, both before any call — Gmail delivers the header's
// blind copies and strips it, so the header is the only way they travel.
// The MIME is uploaded whole to messages.send as the mailbox (gmailUpload).
// It answers Gmail's thread id and the Message-ID Gmail sent the message
// with, read back (U6) on a context of its own (readBackBudget), so an upload that took
// the caller's time does not lose it; a read-back that fails answers the
// thread alone, the message being sent.
func (m *channelMailbox) Send(ctx context.Context, mailbox string, mime []byte, bcc []string) (contracts.SentMessage, error) {
	s, err := m.open(ctx, mailbox, false)
	if err != nil {
		return contracts.SentMessage{}, err
	}
	if int64(len(mime)) > m.MaxMessage() {
		return contracts.SentMessage{}, fmt.Errorf("%w: %d bytes of MIME", contracts.ErrMailTooLarge, len(mime))
	}
	if len(bcc) > 0 && !bccCarried(mime, bcc) {
		return contracts.SentMessage{}, fmt.Errorf("%w: the MIME's Bcc header does not carry every blind copy", contracts.ErrBccUnsupported)
	}
	sent, err := gmailUpload(ctx, m.keys.c, s.send, s.gid, mime)
	if err != nil {
		return contracts.SentMessage{}, gmailRefusal(err)
	}
	out := contracts.SentMessage{ThreadID: sent.threadID()}
	if !validGmailID(sent.ID) {
		return out, nil
	}
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), readBackBudget)
	defer cancel()
	meta, err := m.metadata(readCtx, s, sent.ID)
	if err != nil {
		m.keys.c.logger.WarnContext(ctx, "google: the Message-ID of a sent message was not read back",
			"mailbox", s.gid, "error", err.Error())
		return out, nil
	}
	out.MessageID = meta.messageID()
	return out, nil
}

// gmailSent is what messages.send answers of the message it sent.
type gmailSent struct {
	ID       string `json:"id"`
	ThreadID string `json:"threadId"`
}

// threadID is Gmail's thread id, "" when it is none Gmail could have
// answered.
func (g gmailSent) threadID() string {
	if validGmailID(g.ThreadID) {
		return g.ThreadID
	}
	return ""
}

// gmailUpload posts mime whole to the upload messages.send with src's token,
// quotaUser gid, for a channel's mailbox (F) and a person's own (G) alike:
// made once (a 5xx is not retried: Gmail may have taken it, and the outbox's
// own retry is the one), under a budget scaled to its size (sizedBudget).
// Gmail files it under the mailbox's SENT. It answers Gmail's message and
// thread ids, a message id that is none logged by gid alone; a failure is
// the client's, for the caller to name.
func gmailUpload(ctx context.Context, c *googleClient, src tokenSource, gid string, mime []byte) (gmailSent, error) {
	var sent gmailSent
	err := c.do(ctx, src, http.MethodPost, gmailSendURL+"&"+url.Values{"quotaUser": {gid}}.Encode(),
		&requestBody{contentType: "message/rfc822", data: mime}, &sent, maxBody,
		noRetry(), withBudget(sizedBudget(int64(len(mime)))), shownAs("/upload"+gmailShownBase+"/messages/send"))
	if err != nil {
		return gmailSent{}, err
	}
	if !validGmailID(sent.ID) {
		c.logger.WarnContext(ctx, "google: Gmail answered a send without a message id", "mailbox", gid)
	}
	return sent, nil
}

// bccCarried reports whether every address of bcc is in the Bcc header of
// mime, ignoring case.
func bccCarried(mime []byte, bcc []string) bool {
	msg, err := mail.ReadMessage(bytes.NewReader(mime))
	if err != nil {
		return false
	}
	list, err := msg.Header.AddressList("Bcc")
	if err != nil {
		return false
	}
	for _, want := range bcc {
		found := false
		for _, a := range list {
			if strings.EqualFold(strings.TrimSpace(a.Address), strings.TrimSpace(want)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// gmailMetadata is a message as messages.get(format=metadata) answers it,
// with the Message-ID header alone asked for.
type gmailMetadata struct {
	ID           string   `json:"id"`
	ThreadID     string   `json:"threadId"`
	LabelIDs     []string `json:"labelIds"`
	InternalDate string   `json:"internalDate"`
	Payload      struct {
		Headers []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
	} `json:"payload"`
}

// messageID is the Message-ID header's value, "" when it is missing or
// longer than a line.
func (g gmailMetadata) messageID() string {
	for _, h := range g.Payload.Headers {
		if strings.EqualFold(h.Name, "Message-ID") {
			v := strings.TrimSpace(h.Value)
			if len(v) > mailMaxMessageID || strings.ContainsAny(v, "\r\n") {
				return ""
			}
			return v
		}
	}
	return ""
}

// receivedAt is internalDate — milliseconds since the epoch — in UTC, zero
// when it does not read.
func (g gmailMetadata) receivedAt() time.Time {
	var ms int64
	if _, err := fmt.Sscan(g.InternalDate, &ms); err != nil || ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// metadata reads the message's metadata: its thread, labels, internalDate
// and Message-ID header, nothing of what it says.
func (m *channelMailbox) metadata(ctx context.Context, s gmailSession, id string) (gmailMetadata, error) {
	return readMetadata(ctx, m.keys.c, s, id)
}

// readMetadata is metadata with c, s.modify's token reading — a channel's
// gmail.modify, or a person's token whose scopes read (personal sending).
func readMetadata(ctx context.Context, c *googleClient, s gmailSession, id string) (gmailMetadata, error) {
	var meta gmailMetadata
	err := c.get(ctx, s.modify, s.gmailURL("/messages/"+url.PathEscape(id), url.Values{
		"format": {"metadata"}, "metadataHeaders": {"Message-ID"},
		"fields": {"id,threadId,labelIds,internalDate,payload/headers"},
	}), &meta, shownAs(gmailShownBase+"/messages/{id}"))
	return meta, err
}

// Watch answers contracts.ErrWatchUnsupported: v1 polls (Q7), and the
// mailbox sync keeps the channel on its normal poll rather than the safety
// interval a live subscription allows.
func (m *channelMailbox) Watch(context.Context, string, string) error {
	return contracts.ErrWatchUnsupported
}

// Unwatch answers nil: nothing is ever watched.
func (m *channelMailbox) Unwatch(context.Context, string, string) error { return nil }

// Notified answers nothing: no push reaches the provider (Q7).
func (m *channelMailbox) Notified(context.Context, time.Time) ([]contracts.MailboxNotice, error) {
	return nil, nil
}
