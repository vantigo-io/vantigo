package google_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/googletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Mail channels' switch (Google Workspace design G33): the Owner's, the two
// Gmail scopes the page lists while it is on, Test connection asking them
// and no longer listing them unexpected, and the state the tab shows.

const mailChannelsPath = "/api/v1/google/connection/mail-channels"

// gmailScopes are Mail channels' two scopes.
var gmailScopes = []string{
	"https://www.googleapis.com/auth/gmail.modify",
	"https://www.googleapis.com/auth/gmail.send",
}

// setMailChannels is PUT /connection/mail-channels, which must answer 200.
func setMailChannels(t *testing.T, o *modtest.Client, enabled bool) gen.GoogleConnection {
	t.Helper()
	r := o.Do(http.MethodPut, mailChannelsPath, map[string]any{"enabled": enabled})
	if r.Status != http.StatusOK {
		t.Fatalf("PUT /connection/mail-channels: %d %s", r.Status, r.Body)
	}
	var conn gen.GoogleConnection
	r.JSON(&conn)
	return conn
}

func TestMailChannels_TheSwitchAndTheScopes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.g.Delegate(saClientID, append(slices.Clone(everyScope), gmailScopes...)...)
	o, _ := configured(t, h)
	testConnection(t, o)
	conn := getConnection(t, o)
	if conn.MailChannels.Enabled || conn.MailChannels.Status != gen.GoogleMailChannelsStatusOff || !slices.Equal(conn.ScopesToDelegate, everyScope) {
		t.Errorf("off: %+v, scopes %v", conn.MailChannels, conn.ScopesToDelegate)
	}
	// Off, the Gmail scopes delegated anyway are unexpected.
	if !slices.Equal(conn.ScopesUnexpected, gmailScopes) {
		t.Errorf("unexpected while off = %v, want the Gmail scopes", conn.ScopesUnexpected)
	}

	// Only the Owner turns it on.
	m, _ := manager(t, h)
	if r := m.Do(http.MethodPut, mailChannelsPath, map[string]any{"enabled": true}); r.Status != http.StatusForbidden {
		t.Errorf("a manager's PUT: %d, want 403", r.Status)
	}
	conn = setMailChannels(t, o, true)
	if !conn.MailChannels.Enabled || conn.MailChannels.Status != gen.GoogleMailChannelsStatusUnavailable ||
		conn.MailChannels.Reason == nil || *conn.MailChannels.Reason != "not_tested" {
		t.Errorf("on, untested: %+v", conn.MailChannels)
	}
	if want := append(slices.Clone(everyScope), gmailScopes...); !slices.Equal(conn.ScopesToDelegate, want) {
		t.Errorf("scopesToDelegate = %v, want %v", conn.ScopesToDelegate, want)
	}

	// Tested: granted, not unexpected, asked as the admin while no mailbox
	// is recorded.
	before := len(h.g.Calls())
	result := testConnection(t, o)
	if !slices.Contains(result.Granted, gmailScopes[1]) || slices.Contains(result.Unexpected, gmailScopes[1]) {
		t.Errorf("tested: %+v", result)
	}
	for _, c := range h.g.Calls()[before:] {
		if c.Host == googletest.TokenHost && slices.Contains(gmailScopes, c.Scope) && c.Subject != adminAddress {
			t.Errorf("a Gmail scope asked as %q, want the admin", c.Subject)
		}
	}
	if conn = getConnection(t, o); conn.MailChannels.Status != gen.GoogleMailChannelsStatusReady {
		t.Errorf("tested: %+v, want ready", conn.MailChannels)
	}

	// The Gmail scopes not delegated: not_delegated.
	h.g.Delegate(saClientID, everyScope...)
	testConnection(t, o)
	if conn = getConnection(t, o); conn.MailChannels.Reason == nil || *conn.MailChannels.Reason != "not_delegated" ||
		!slices.Equal(conn.ScopesMissing, gmailScopes) {
		t.Errorf("undelegated: %+v, missing %v", conn.MailChannels, conn.ScopesMissing)
	}

	// The same value again writes nothing; each switch is audited.
	setMailChannels(t, o, true)
	setMailChannels(t, o, false)
	if n := h.Count(t, `SELECT count(*) FROM google.events WHERE action = 'google.mail_channels.switched'`); n != 2 {
		t.Errorf("switch events = %d, want 2", n)
	}
}
