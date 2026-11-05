package google_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google"
	"github.com/vantigo-io/vantigo/server/internal/google/googletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Sending from a person's own Gmail address (Google Workspace design G35,
// delivery G): the status's reasons, the mail feature connected through D's
// flow beside the calendar, the upload send on the person's own token into
// their Sent, the Message-ID read back only when the scopes read, and every
// refusal as the contract names it, against the fake's Gmail.

// The send's upload path, as FailOn matches it.
const personalSendPath = "POST /upload/gmail/v1/users/me/messages/send"

// personalMailFixture is a connected organisation with Connection for
// people on and the module's personal mailbox provider.
type personalMailFixture struct {
	*personalFixture
	p contracts.PersonalMailbox
}

func newPersonalMailFixture(t *testing.T, opts ...modtest.Option) *personalMailFixture {
	t.Helper()
	f := newPersonalFixture(t, opts...)
	return &personalMailFixture{personalFixture: f, p: google.Module().PersonalMailbox(f.h.Deps())}
}

// connectFeature runs the whole flow for c as gid for feature, change
// applied to Google's grant; complete must answer 200.
func (f *personalMailFixture) connectFeature(t *testing.T, c *modtest.Client, feature, gid, email string, change func(*googletest.CodeGrant)) {
	t.Helper()
	r := f.authorizeAs(t, c, feature, gid, email, change)
	if _, _, outcome, code := personalOutcome(t, r); outcome != "complete" {
		t.Fatalf("callback for %s: outcome=%s code=%s", feature, outcome, code)
	}
	if r := completePersonal(c); r.Status != http.StatusOK {
		t.Fatalf("complete for %s: %d %s", feature, r.Status, r.Body)
	}
}

// mailPerson is a person connected for the calendar and then for Gmail, as
// the composer's Connect Google leaves them: the user and the Directory id.
func (f *personalMailFixture) mailPerson(t *testing.T, email string) (*modtest.Client, uuid.UUID, string) {
	t.Helper()
	c, userID, gid := f.person(t, email)
	f.connectFeature(t, c, "calendar", gid, email, nil)
	f.connectFeature(t, c, "mail", gid, email, nil)
	return c, userID, gid
}

// gmailMIME is a message from email to a customer, Reply-To the channel,
// with bcc in its Bcc header when not empty.
func gmailMIME(email, bcc string) []byte {
	m := "From: Kari Nordmann <" + email + ">\r\nTo: kunde@example.test\r\nReply-To: Nordlys <post@nordlys.no>\r\n"
	if bcc != "" {
		m += "Bcc: " + bcc + "\r\n"
	}
	return []byte(m + "Subject: Avtalen\r\nMessage-ID: <personal-1@vantigo.invalid>\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\nHei fra Kari.\r\n")
}

// gmailReads are the calls to Gmail other than the upload made from call
// from on.
func (f *personalMailFixture) gmailReads(from int) []googletest.Call {
	var out []googletest.Call
	for _, c := range f.g.Calls()[from:] {
		if c.Host == googletest.GmailHost && !strings.HasPrefix(c.Path, "/upload/") {
			out = append(out, c)
		}
	}
	return out
}

// needed is the reason of err's ErrConnectionNeeded, failing when it is not
// one.
func needed(t *testing.T, err error) string {
	t.Helper()
	var n *contracts.ConnectionNeededError
	if !errors.As(err, &n) || !errors.Is(err, contracts.ErrConnectionNeeded) {
		t.Fatalf("err = %v, want ErrConnectionNeeded", err)
	}
	return n.Reason
}

func TestPersonalMailbox_Status(t *testing.T) {
	t.Parallel()
	f := newPersonalMailFixture(t)
	if f.p.Key() != "google" || f.p.DisplayName() != "Gmail" {
		t.Fatalf("key %q, name %q", f.p.Key(), f.p.DisplayName())
	}
	status := func(userID uuid.UUID) contracts.PersonalMailStatus {
		t.Helper()
		st, err := f.p.Status(contracts.AsUser(context.Background(), userID))
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		return st
	}

	if st, err := f.p.Status(context.Background()); err != nil || st.Available || st.Reason != "unavailable" {
		t.Errorf("no principal: %+v %v, want unavailable", st, err)
	}
	_, stranger := f.h.SignInUser(t)
	if st := status(stranger); st.Reason != "no_account" || st.Address != "" {
		t.Errorf("no account: %+v, want no_account with no address", st)
	}

	c, userID, gid := f.person(t, "kari@nordlys.no")
	if st := status(userID); st.Available || st.Reason != "not_connected" || st.Mailbox != gid || st.Address != "kari@nordlys.no" {
		t.Errorf("not connected: %+v", st)
	}
	f.connectFeature(t, c, "calendar", gid, "kari@nordlys.no", nil)
	if st := status(userID); st.Available || st.Reason != "not_connected" {
		t.Errorf("calendar alone: %+v, want not_connected (M20)", st)
	}

	// The mail feature joins the live connection, the calendar kept (U9).
	f.connectFeature(t, c, "mail", gid, "kari@nordlys.no", nil)
	if me := getMe(t, c); strings.Join(me.Features, ",") != "calendar,mail" {
		t.Errorf("features = %v, want calendar and mail", me.Features)
	}
	if st := status(userID); !st.Available || st.Reason != "" || st.Mailbox != gid || st.Address != "kari@nordlys.no" {
		t.Errorf("connected for Gmail: %+v, want available", st)
	}

	f.h.Exec(t, `UPDATE google.connections SET state = 'reconnect', error_code = 'expired' WHERE google_user_id = $1`, gid)
	if st := status(userID); st.Available || st.Reason != "reconnect" || st.ErrorCode != "expired" {
		t.Errorf("reconnect: %+v, want reconnect expired", st)
	}
	f.h.Exec(t, `UPDATE google.connections SET state = 'closing', error_code = NULL WHERE google_user_id = $1`, gid)
	if st := status(userID); st.Reason != "not_connected" {
		t.Errorf("closing: %+v, want not_connected", st)
	}

	// Staff re-points Kari to another trusted Google account: her live
	// connection is the old account's, never the new one's.
	second := f.account(t, "kari.n@nordlys.no")
	f.w.set(userID, &contracts.DirectoryAccount{Provider: "google", ExternalID: second, Enabled: true})
	if st := status(userID); st.Available || st.Reason != "not_connected" || st.Mailbox != second || st.Address != "kari.n@nordlys.no" {
		t.Errorf("another account: %+v, want not_connected on the new account", st)
	}

	// Someone else's account: a row of the account that is not the caller's
	// is never theirs to send from.
	_, other := f.h.SignInUser(t)
	f.w.set(other, &contracts.DirectoryAccount{Provider: "entra", ExternalID: gid, Enabled: true})
	if st := status(other); st.Reason != "no_account" {
		t.Errorf("an Entra account: %+v, want no_account", st)
	}

	setPersonal(t, f.o, false)
	if st := status(userID); st.Reason != "unavailable" {
		t.Errorf("switch off: %+v, want unavailable", st)
	}
}

func TestPersonalMailbox_SendsFromTheirOwnGmail(t *testing.T) {
	t.Parallel()
	f := newPersonalMailFixture(t, modtest.WithPoolMaxConns(1))
	_, userID, gid := f.mailPerson(t, "kari@nordlys.no")
	f.g.SetReplaceMessageID(true)
	from := len(f.g.Calls())

	sent, err := f.p.Send(contracts.AsUser(context.Background(), userID), gmailMIME("kari@nordlys.no", "sjef@example.test"), []string{"sjef@example.test"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	msgs := f.g.GmailMessages("kari@nordlys.no")
	if len(msgs) != 1 || msgs[0].Labels[0] != "SENT" {
		t.Fatalf("Kari's mailbox = %+v, want the message in her Sent", msgs)
	}
	if !bytes.Contains(msgs[0].Raw, []byte("Reply-To: Nordlys <post@nordlys.no>")) || !bytes.Contains(msgs[0].Raw, []byte("Bcc: sjef@example.test")) {
		t.Errorf("the message lost its Reply-To or its blind copy:\n%s", msgs[0].Raw)
	}
	if len(f.g.GmailMessages("post@nordlys.no")) != 0 {
		t.Error("the channel's mailbox sent it")
	}
	if sent.ThreadID != msgs[0].ThreadID || sent.MessageID != "" {
		t.Errorf("sent = %+v, want Gmail's thread %s and no Message-ID: gmail.send reads nothing back", sent, msgs[0].ThreadID)
	}
	if reads := f.gmailReads(from); len(reads) != 0 {
		t.Errorf("Gmail was read with the send scope alone: %+v", reads)
	}
	for _, c := range f.g.Calls()[from:] {
		if strings.HasPrefix(c.Path, "/upload/") {
			q, _ := url.ParseQuery(c.Query)
			if q.Get("quotaUser") != gid || q.Get("uploadType") != "media" {
				t.Errorf("the upload's query = %s", c.Query)
			}
		}
	}
	if logs := f.h.Logs(); strings.Contains(logs, "kari@nordlys.no") || strings.Contains(logs, "Hei fra Kari") {
		t.Error("the log holds an address or the message")
	}
	f.noSecretsLeaked(t, gid)
}

// TestPersonalMailbox_ReadsBackWhenTheScopesRead: a connection whose
// granted scopes read metadata has the Message-ID Gmail sent with read back.
func TestPersonalMailbox_ReadsBackWhenTheScopesRead(t *testing.T) {
	t.Parallel()
	f := newPersonalMailFixture(t)
	c, userID, gid := f.person(t, "ola@nordlys.no")
	f.connectFeature(t, c, "mail", gid, "ola@nordlys.no", func(g *googletest.CodeGrant) {
		g.Granted = append(append([]string{}, g.Scopes...), "https://www.googleapis.com/auth/gmail.modify")
	})
	f.g.SetReplaceMessageID(true)
	sent, err := f.p.Send(contracts.AsUser(context.Background(), userID), gmailMIME("ola@nordlys.no", ""), nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.HasSuffix(sent.MessageID, "@mail.gmail.com>") || sent.ThreadID == "" {
		t.Errorf("sent = %+v, want Gmail's Message-ID read back and its thread", sent)
	}
}

func TestPersonalMailbox_SendRefusedBeforeAnyCall(t *testing.T) {
	t.Parallel()
	f := newPersonalMailFixture(t)
	c, userID, gid := f.person(t, "kari@nordlys.no")
	ctx := contracts.AsUser(context.Background(), userID)
	from := len(f.g.Calls())

	if _, err := f.p.Send(context.Background(), gmailMIME("kari@nordlys.no", ""), nil); needed(t, err) != contracts.ConnectionNotConnected {
		t.Errorf("no principal: %v", err)
	}
	if _, err := f.p.Send(ctx, gmailMIME("kari@nordlys.no", ""), nil); needed(t, err) != contracts.ConnectionNotConnected {
		t.Errorf("not connected: %v", err)
	}
	big := append(gmailMIME("kari@nordlys.no", ""), bytes.Repeat([]byte("x"), int(contracts.MaxPersonalMessageBytes))...)
	if _, err := f.p.Send(ctx, big, nil); !errors.Is(err, contracts.ErrMailTooLarge) {
		t.Errorf("over 3 MB: %v, want ErrMailTooLarge", err)
	}
	_, stranger := f.h.SignInUser(t)
	if _, err := f.p.Send(contracts.AsUser(context.Background(), stranger), gmailMIME("x@nordlys.no", ""), nil); needed(t, err) != contracts.ConnectionClosingReason {
		t.Errorf("no account: %v", err)
	}

	f.connectFeature(t, c, "mail", gid, "kari@nordlys.no", nil)
	if _, err := f.p.Send(ctx, gmailMIME("kari@nordlys.no", ""), []string{"sjef@example.test"}); !errors.Is(err, contracts.ErrBccUnsupported) {
		t.Errorf("a blind copy not in the header: %v, want ErrBccUnsupported", err)
	}
	if n := len(f.g.Calls()) - from; n == 0 {
		t.Fatal("the connect made no call")
	}
	for _, call := range f.g.Calls()[from:] {
		if call.Host == googletest.GmailHost {
			t.Errorf("a refusal reached Gmail: %+v", call)
		}
	}

	setPersonal(t, f.o, false)
	if _, err := f.p.Send(ctx, gmailMIME("kari@nordlys.no", ""), nil); needed(t, err) != contracts.ConnectionClosingReason {
		t.Errorf("switch off: %v", err)
	}
}

// TestPersonalMailbox_SendRefusedForAnotherAccount: Staff re-points Kari to
// another trusted Google account while her connection is the old one's; the
// send is not_connected and Gmail is never called.
func TestPersonalMailbox_SendRefusedForAnotherAccount(t *testing.T) {
	t.Parallel()
	f := newPersonalMailFixture(t)
	_, userID, _ := f.mailPerson(t, "kari@nordlys.no")
	ctx := contracts.AsUser(context.Background(), userID)

	second := f.account(t, "kari.n@nordlys.no")
	f.w.set(userID, &contracts.DirectoryAccount{Provider: "google", ExternalID: second, Enabled: true})
	from := len(f.g.Calls())
	if _, err := f.p.Send(ctx, gmailMIME("kari.n@nordlys.no", ""), nil); needed(t, err) != contracts.ConnectionNotConnected {
		t.Errorf("another account: %v, want not_connected", err)
	}
	for _, call := range f.g.Calls()[from:] {
		if call.Host == googletest.GmailHost {
			t.Errorf("a refusal reached Gmail: %+v", call)
		}
	}
	if n := len(f.g.GmailMessages("kari@nordlys.no")) + len(f.g.GmailMessages("kari.n@nordlys.no")); n != 0 {
		t.Errorf("%d messages sent, want 0", n)
	}
}

// TestPersonalMailbox_SendRefusedAfterTheAccountIsDisabled: the connected
// account is disabled after the connect; the send answers closing and Gmail
// is never called. (Send's own account check answers before the token's
// check on use, so this send does not close the row.)
func TestPersonalMailbox_SendRefusedAfterTheAccountIsDisabled(t *testing.T) {
	t.Parallel()
	f := newPersonalMailFixture(t)
	_, userID, gid := f.mailPerson(t, "kari@nordlys.no")
	ctx := contracts.AsUser(context.Background(), userID)

	f.h.Exec(t, `UPDATE google.accounts SET state = 'disabled' WHERE user_id = $1`, gid)
	from := len(f.g.Calls())
	if _, err := f.p.Send(ctx, gmailMIME("kari@nordlys.no", ""), nil); needed(t, err) != contracts.ConnectionClosingReason {
		t.Errorf("a disabled account: %v, want closing", err)
	}
	for _, call := range f.g.Calls()[from:] {
		if call.Host == googletest.GmailHost {
			t.Errorf("a refusal reached Gmail: %+v", call)
		}
	}
	if n := len(f.g.GmailMessages("kari@nordlys.no")); n != 0 {
		t.Errorf("%d messages sent, want 0", n)
	}
}

// TestPersonalMailbox_TheConnectionIsNeeded: a connection for the calendar
// alone is consent_missing, its row untouched; a refused refresh moves it to
// reconnect; a 401 after a fresh token does too; never a send.
func TestPersonalMailbox_TheConnectionIsNeeded(t *testing.T) {
	t.Parallel()
	f := newPersonalMailFixture(t)

	c, userID, gid := f.person(t, "kari@nordlys.no")
	f.connectFeature(t, c, "calendar", gid, "kari@nordlys.no", nil)
	ctx := contracts.AsUser(context.Background(), userID)
	if _, err := f.p.Send(ctx, gmailMIME("kari@nordlys.no", ""), nil); needed(t, err) != "consent_missing" {
		t.Errorf("calendar alone: %v, want consent_missing", err)
	}
	if state, code := f.rowState(t, gid); state != "active" || code != "" {
		t.Errorf("the calendar's connection = %s %s, want active and untouched", state, code)
	}

	_, ola, olaGID := f.mailPerson(t, "ola@nordlys.no")
	for _, token := range f.g.RefreshTokens(olaGID) {
		f.g.RefuseRefresh(token, "invalid_grant")
	}
	if _, err := f.p.Send(contracts.AsUser(context.Background(), ola), gmailMIME("ola@nordlys.no", ""), nil); needed(t, err) != "expired" {
		t.Errorf("a refused refresh: %v, want expired", err)
	}
	if state, code := f.rowState(t, olaGID); state != "reconnect" || code != "expired" {
		t.Errorf("Ola's connection = %s %s, want reconnect expired", state, code)
	}

	_, per, perGID := f.mailPerson(t, "per@nordlys.no")
	f.g.FailOn(personalSendPath, http.StatusUnauthorized)
	if _, err := f.p.Send(contracts.AsUser(context.Background(), per), gmailMIME("per@nordlys.no", ""), nil); needed(t, err) != "sign_in_again" {
		t.Errorf("a 401: %v, want sign_in_again", err)
	}
	f.g.FailOn(personalSendPath, 0)
	if state, code := f.rowState(t, perGID); state != "reconnect" || code != "sign_in_again" {
		t.Errorf("Per's connection = %s %s, want reconnect sign_in_again", state, code)
	}
	for _, address := range []string{"kari@nordlys.no", "ola@nordlys.no", "per@nordlys.no"} {
		if n := len(f.g.GmailMessages(address)); n != 0 {
			t.Errorf("%s sent %d messages", address, n)
		}
	}
}

func TestPersonalMailbox_GmailsRefusals(t *testing.T) {
	t.Parallel()
	f := newPersonalMailFixture(t)
	_, userID, _ := f.mailPerson(t, "kari@nordlys.no")
	ctx := contracts.AsUser(context.Background(), userID)
	for _, tc := range []struct {
		status int
		reason string
		want   error
	}{
		{http.StatusBadRequest, "invalidArgument", contracts.ErrMailboxDenied},
		{http.StatusForbidden, "forbidden", contracts.ErrMailboxDenied},
		{http.StatusRequestEntityTooLarge, "uploadTooLarge", contracts.ErrMailTooLarge},
		{http.StatusInternalServerError, "backendError", contracts.ErrMailboxUnavailable},
		{http.StatusForbidden, "dailyLimitExceeded", contracts.ErrMailboxUnavailable},
	} {
		f.g.RefuseOn(personalSendPath, tc.status, tc.reason)
		from := len(f.g.Calls())
		_, err := f.p.Send(ctx, gmailMIME("kari@nordlys.no", ""), nil)
		if !errors.Is(err, tc.want) {
			t.Errorf("%d %s: %v, want %v", tc.status, tc.reason, err, tc.want)
		}
		uploads := 0
		for _, c := range f.g.Calls()[from:] {
			if strings.HasPrefix(c.Path, "/upload/") {
				uploads++
			}
		}
		if uploads != 1 {
			t.Errorf("%d %s: %d uploads, want one: a send is never retried", tc.status, tc.reason, uploads)
		}
	}
	f.g.RefuseOn(personalSendPath, 0, "")
	f.g.Throttle(2)
	if _, err := f.p.Send(ctx, gmailMIME("kari@nordlys.no", ""), nil); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("throttled past the wait: %v, want ErrMailboxUnavailable", err)
	}
	if _, err := f.p.Send(ctx, gmailMIME("kari@nordlys.no", ""), nil); err != nil {
		t.Errorf("after the refusals: %v", err)
	}
	if strings.Contains(f.h.Logs(), f.g.Words) {
		t.Error("the log holds Google's words")
	}
}
