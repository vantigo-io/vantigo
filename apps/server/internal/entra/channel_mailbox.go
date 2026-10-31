package entra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's contracts.ChannelMailbox (Outlook connection
// design M10, M11, M13; delivery C's plan readings 9, 11 and 12): the
// Exchange Online shared mailboxes Communications' channels send through,
// reached with the application's own token — never a person's — and scoped
// to the channel mailboxes by Exchange RBAC for Applications
// (Application Mail Full Access, Application MailboxSettings.Read).
//
// Status reads the connection's row and the secrets and nothing else:
// available while the Mail channels switch is on and the connection
// connected, with the reason tenant_wide while the last test's token held a
// mail role reaching every mailbox (a warning); else unavailable,
// switch_off or the connection's reason.
//
// Check resolves an address — by its userPrincipalName, else by $filter=mail
// eq — reads the mailbox's userPurpose and its Inbox (proving the RBAC
// scope reaches it), and records a shared mailbox's object id in
// entra.channel_mailboxes.
// Send answers only for a mailbox recorded there, so Communications can
// never point it at a mailbox no Check resolved: without blind copies it
// posts the MIME, base64-encoded, to sendMail; with them, as a MIME draft
// whose Bcc header Graph reads, its bccRecipients checked before it is
// sent — a draft that lost one is ErrBccUnsupported, nothing sent. Neither
// is retried by the client on a 5xx (noRetry): Exchange may
// have taken the message, and the outbox's own retry is the one. Forget
// deletes the row. Delta and Raw, the inbound reads, are
// channel_mailbox_inbound.go's, behind the same gate as Send.
//
// Graph's words are never logged — they may echo an address or the
// message — nor is an address: Check logs the tenant and the object id.

// The channel mailboxes' figures and names.
const (
	// channelMailboxName is the provider's name as an admin reads it.
	channelMailboxName = "Microsoft 365"
	// graphMaxMIMEBytes is the largest MIME Send takes: base64 makes it
	// 4 000 000 bytes of request body, Graph's documented 4 MB for a
	// message posted whole.
	graphMaxMIMEBytes = 3_000_000
	// channelMaxAddress bounds an address Check takes, RFC 5321's path.
	channelMaxAddress = 320
	// channelCheckSelect is what Check reads of the user.
	channelCheckSelect = "id,mail,userPrincipalName,displayName,accountEnabled"
	// channelDraftCleanup bounds the delete of a draft a failed send left.
	channelDraftCleanup = 30 * time.Second
	// purposeShared is a shared mailbox's userPurpose.
	purposeShared = "shared"
)

// errBccLost is a draft whose bccRecipients do not hold every blind copy
// Send was given: deleted and not sent, contracts.ErrBccUnsupported — a
// retry would build the same draft from the same MIME.
var errBccLost = fmt.Errorf("%w: the draft did not keep every Bcc recipient", contracts.ErrBccUnsupported)

// mailSizeCodes are the codes Exchange refuses a message too large with,
// on a 400 rather than a 413: the mailbox's own send limit is below
// Graph's.
var mailSizeCodes = []string{"ErrorMessageSizeExceeded", "MessageSizeExceeded", "ErrorMessageTooBig"}

// channelMailbox is the module's contracts.ChannelMailbox over client.
type channelMailbox struct {
	deps   module.Deps
	client *graphClient
}

var _ contracts.ChannelMailbox = (*channelMailbox)(nil)

// newChannelMailbox builds the module's channel mailbox provider over d,
// for Compose and Workers alike.
func newChannelMailbox(d module.Deps) contracts.ChannelMailbox {
	return &channelMailbox{deps: d, client: newGraphClient(d)}
}

// Key is "entra", a channel's provider value.
func (m *channelMailbox) Key() string { return providerKey }

// DisplayName is "Microsoft 365".
func (m *channelMailbox) DisplayName() string { return channelMailboxName }

// MaxMessage is graphMaxMIMEBytes.
func (m *channelMailbox) MaxMessage() int64 { return graphMaxMIMEBytes }

// Status is whether channels may use the provider now, from the
// connection's row and the secrets with no network.
func (m *channelMailbox) Status(ctx context.Context) (contracts.MailboxStatus, error) {
	q := store.New(m.deps.Pool)
	conn, err := q.GetConnection(ctx)
	if err != nil {
		return contracts.MailboxStatus{}, fmt.Errorf("entra: read the connection: %w", err)
	}
	creds, err := q.ListCredentials(ctx)
	if err != nil {
		return contracts.MailboxStatus{}, fmt.Errorf("entra: read the secrets: %w", err)
	}
	return channelStatusOf(conn, creds, m.client.now()), nil
}

// channelStatusOf is the provider's state at now: unavailable switch_off
// while the Mail channels switch is off; available while the connection is
// connected, Reason tenant_wide when its token held a mail role reaching
// every mailbox; else unavailable with the connection's reason, or its
// status when it names none.
func channelStatusOf(conn store.EntraConnection, creds []store.EntraCredential, now time.Time) contracts.MailboxStatus {
	if !conn.MailChannels {
		return contracts.MailboxStatus{State: contracts.MailboxUnavailable, Reason: reasonSwitchOff}
	}
	status, reason := statusOf(conn, creds, now)
	if status == contracts.ProviderConnected {
		if tenantWideMail(conn.PermissionsPresent) {
			return contracts.MailboxStatus{State: contracts.MailboxAvailable, Reason: contracts.MailboxTenantWide}
		}
		return contracts.MailboxStatus{State: contracts.MailboxAvailable}
	}
	if reason == "" {
		reason = status
	}
	if !reasonPattern.MatchString(reason) {
		reason = ""
	}
	return contracts.MailboxStatus{State: contracts.MailboxUnavailable, Reason: reason}
}

// ready is the application's credential for a call, once Status is
// available: ErrMailboxUnavailable otherwise, and when no tenant, client or
// secret is set up to fetch a token; any other failure passes.
func (m *channelMailbox) ready(ctx context.Context) (graphCredential, error) {
	st, err := m.Status(ctx)
	if err != nil {
		return graphCredential{}, err
	}
	if st.State != contracts.MailboxAvailable {
		return graphCredential{}, fmt.Errorf("%w (%s)", contracts.ErrMailboxUnavailable, st.Reason)
	}
	cred, err := credentialFor(ctx, store.New(m.deps.Pool), m.client)
	switch {
	case errors.Is(err, errNotConfigured), errors.Is(err, errNoCredential):
		return graphCredential{}, fmt.Errorf("%w: %w", contracts.ErrMailboxUnavailable, err)
	case err != nil:
		return graphCredential{}, err
	}
	return cred, nil
}

// channelAddress reports whether address is a bare address Check may send
// to Graph: an addr-spec alone, no display name, no white space or control
// character, nothing that would change the path it is put in.
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

// userByAddress is the path of the user whose userPrincipalName is
// address: /users/{upn}, or the key form /users('{upn}') — its quotes
// doubled — for one beginning with $, which Graph would otherwise read as
// a system segment.
func userByAddress(address string) string {
	if strings.HasPrefix(address, "$") {
		return "/users('" + url.PathEscape(strings.ReplaceAll(address, "'", "''")) + "')"
	}
	return "/users/" + url.PathEscape(address)
}

// channelUser is what Check reads of the user.
type channelUser struct {
	ID                string `json:"id"`
	Mail              string `json:"mail"`
	UserPrincipalName string `json:"userPrincipalName"`
	DisplayName       string `json:"displayName"`
}

// channelRefusal is a refusal of a mailbox call as the contract answers it:
// 403 ErrMailboxDenied (outside the RBAC scope, or a role missing); 404
// with a mailbox-gone code ErrMailboxGone; 413, or a 400 with a size code
// (mailSizeCodes), ErrMailTooLarge. Anything else passes, a failure to
// retry.
func channelRefusal(err error) error {
	switch {
	case err == nil:
		return nil
	case isStatus(err, http.StatusForbidden):
		return fmt.Errorf("%w: %w", contracts.ErrMailboxDenied, err)
	case isStatus(err, http.StatusNotFound) && isCode(err, mailboxGoneCodes...):
		return fmt.Errorf("%w: %w", contracts.ErrMailboxGone, err)
	case isStatus(err, http.StatusRequestEntityTooLarge),
		isStatus(err, http.StatusBadRequest) && isCode(err, mailSizeCodes...):
		return fmt.Errorf("%w: %w", contracts.ErrMailTooLarge, err)
	}
	return err
}

// Check resolves address to a mailbox with the application's token and
// records it when it is shared: GET /users/{upn} (a 404 then tries
// $filter=mail eq, which must find one user), GET
// …/mailboxSettings/userPurpose and GET …/mailFolders/inbox. A personal
// mailbox is answered (Shared false) but not recorded, so Send refuses it.
// An address that is not a bare addr-spec is ErrMailboxUnknown with no
// call; the switch off ErrMailboxUnavailable. The typed address is never
// in an error or a log line: its call is made addressInPath.
func (m *channelMailbox) Check(ctx context.Context, address string) (contracts.ChannelMailboxInfo, error) {
	if !channelAddress(address) {
		return contracts.ChannelMailboxInfo{}, fmt.Errorf("%w: not an address", contracts.ErrMailboxUnknown)
	}
	cred, err := m.ready(ctx)
	if err != nil {
		return contracts.ChannelMailboxInfo{}, err
	}
	var user channelUser
	err = m.client.get(ctx, cred, userByAddress(address)+"?"+query(url.Values{"$select": {channelCheckSelect}}), &user, noWords, addressInPath)
	if isStatus(err, http.StatusNotFound) {
		var found struct {
			Value []channelUser `json:"value"`
		}
		err = m.client.get(ctx, cred, "/users?"+query(url.Values{
			"$filter": {"mail eq '" + strings.ReplaceAll(address, "'", "''") + "'"}, "$select": {channelCheckSelect},
		}), &found, noWords)
		if err == nil && len(found.Value) != 1 {
			return contracts.ChannelMailboxInfo{}, fmt.Errorf("%w: %d users hold the address", contracts.ErrMailboxUnknown, len(found.Value))
		}
		if err == nil {
			user = found.Value[0]
		}
	}
	if err != nil {
		return contracts.ChannelMailboxInfo{}, channelRefusal(err)
	}
	oid, err := uuid.Parse(user.ID)
	if err != nil {
		return contracts.ChannelMailboxInfo{}, fmt.Errorf("%w: the user's id is not a GUID", errTenantUnreachable)
	}
	var purpose struct {
		Value string `json:"value"`
	}
	if err := m.client.get(ctx, cred, "/users/"+oid.String()+"/mailboxSettings/userPurpose", &purpose, noWords); err != nil {
		return contracts.ChannelMailboxInfo{}, channelRefusal(err)
	}
	var inbox struct {
		ID string `json:"id"`
	}
	if err := m.client.get(ctx, cred, "/users/"+oid.String()+"/mailFolders/inbox?"+query(url.Values{"$select": {"id"}}), &inbox, noWords); err != nil {
		return contracts.ChannelMailboxInfo{}, channelRefusal(err)
	}
	primary := user.Mail
	if primary == "" {
		primary = user.UserPrincipalName
	}
	info := contracts.ChannelMailboxInfo{
		Mailbox:     oid.String(),
		Address:     strings.ToLower(primary),
		DisplayName: user.DisplayName,
		Shared:      strings.EqualFold(purpose.Value, purposeShared),
	}
	if !info.Shared {
		return info, nil
	}
	if err := store.New(m.deps.Pool).UpsertChannelMailbox(ctx, store.UpsertChannelMailboxParams{
		ObjectID: oid, Address: info.Address, CheckedAt: m.client.now(),
	}); err != nil {
		return contracts.ChannelMailboxInfo{}, fmt.Errorf("entra: record the channel mailbox: %w", err)
	}
	m.client.logger.InfoContext(ctx, "entra: a channel mailbox checked",
		"tenant", cred.tenantID.String(), "mailbox", oid.String(), "shared", info.Shared)
	return info, nil
}

// Forget deletes the mailbox's row, so nothing is sent through it until
// another Check. A mailbox not recorded, or not an id, is done.
func (m *channelMailbox) Forget(ctx context.Context, mailbox string) error {
	oid, err := uuid.Parse(mailbox)
	if err != nil {
		return nil
	}
	if err := store.New(m.deps.Pool).DeleteChannelMailbox(ctx, oid); err != nil {
		return fmt.Errorf("entra: forget the channel mailbox: %w", err)
	}
	return nil
}

// Send sends mime from mailbox, a row of entra.channel_mailboxes
// (ErrMailboxUnknown otherwise, with no call): larger than MaxMessage
// ErrMailTooLarge, the provider not available ErrMailboxUnavailable, both
// before any call. Without bcc, POST /users/{id}/sendMail; with them, a
// draft (sendMIME). Neither is retried on a 5xx.
func (m *channelMailbox) Send(ctx context.Context, mailbox string, mime []byte, bcc []string) error {
	oid, err := m.recorded(ctx, mailbox)
	if err != nil {
		return err
	}
	if int64(len(mime)) > m.MaxMessage() {
		return fmt.Errorf("%w: %d bytes of MIME", contracts.ErrMailTooLarge, len(mime))
	}
	cred, err := m.ready(ctx)
	if err != nil {
		return err
	}
	src := credentialSource{c: m.client, cred: cred}
	return m.client.sendMIME(ctx, src, "/users/"+oid.String(), mime, bcc, func(err error) error { return err })
}
