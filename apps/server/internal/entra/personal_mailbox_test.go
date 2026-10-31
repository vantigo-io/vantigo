package entra_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// connectOutlookWithFeature completes a connect for userID with the given feature and granted scopes.
func connectOutlookWithFeature(t *testing.T, f *outlookFixture, c *modtest.Client, oid uuid.UUID, upn, feature string, granted []string) {
	t.Helper()
	u, _ := startConnectWithFeature(t, c, feature, "/settings/connections")
	q := u.Query()
	g := graphtest.CodeGrant{
		ClientID:    q.Get("client_id"),
		RedirectURI: q.Get("redirect_uri"),
		Challenge:   q.Get("code_challenge"),
		Scopes:      strings.Fields(q.Get("scope")),
		Nonce:       q.Get("nonce"),
		OID:         oid.String(),
		TID:         tenantID,
		UPN:         upn,
		Granted:     append([]string{"User.Read", "openid", "profile"}, granted...),
	}
	code := f.g.IssueCode(g)
	callQuery := url.Values{"code": {code}, "state": {q.Get("state")}}
	callback(t, c, callQuery)

	r := c.Do(http.MethodPost, outlookCompletePath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("POST /outlook/complete: %d %s", r.Status, r.Body)
	}
}

func startConnectWithFeature(t *testing.T, c *modtest.Client, feature, returnTo string) (*url.URL, string) {
	t.Helper()
	body := map[string]any{"feature": feature}
	if returnTo != "" {
		body["returnTo"] = returnTo
	}
	r := c.Do(http.MethodPost, outlookConnectPath, body)
	if r.Status != http.StatusOK {
		t.Fatalf("POST /outlook/connect: %d %s", r.Status, r.Body)
	}
	var a gen.EntraOutlookAuthorize
	r.JSON(&a)
	u, err := url.Parse(a.AuthorizeUrl)
	if err != nil {
		t.Fatal(err)
	}
	return u, r.Header("Set-Cookie")
}

func personalProvider(h *harness) contracts.PersonalMailbox {
	return entra.Module().PersonalMailbox(h.Deps())
}

func personalMIME(bcc string) []byte {
	m := "From: kari@nordlys.no\r\nTo: kunde@example.com\r\nReply-To: post@nordlys.no\r\n"
	if bcc != "" {
		m += "Bcc: " + bcc + "\r\n"
	}
	return []byte(m + "Subject: Personal send\r\nMessage-Id: <personal-123@vantigo.invalid>\r\n\r\nHello from Kari.\r\n")
}

func TestPersonalMailbox_Status_NotAvailableWhenSwitchOff(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, userID, oid := f.person(t, "kari@nordlys.no")
	connectOutlookWithFeature(t, f, c, oid, "kari@nordlys.no", "mail", []string{"Mail.Send"})

	// Turn switch off
	setOutlookConnect(t, f.o, false)

	prov := personalProvider(f.h)
	ctx := contracts.AsUser(context.Background(), userID)
	st, err := prov.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Available {
		t.Errorf("Available = true, want false when switch off")
	}
	if st.Reason != "unavailable" {
		t.Errorf("Reason = %q, want 'unavailable'", st.Reason)
	}
}

func TestPersonalMailbox_Status_NoAccount(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	_, userWithoutAccount := f.h.SignInUser(t)

	prov := personalProvider(f.h)
	ctx := contracts.AsUser(context.Background(), userWithoutAccount)
	st, err := prov.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Available {
		t.Errorf("Available = true, want false when user has no directory account")
	}
	if st.Reason != "no_account" {
		t.Errorf("Reason = %q, want 'no_account'", st.Reason)
	}
}

func TestPersonalMailbox_Status_NotConnectedWhenOnlyCalendarConnected(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, userID, oid := f.person(t, "kari@nordlys.no")
	connectOutlookWithFeature(t, f, c, oid, "kari@nordlys.no", "calendar", []string{"Calendars.ReadWrite"})

	prov := personalProvider(f.h)
	ctx := contracts.AsUser(context.Background(), userID)
	st, err := prov.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Available {
		t.Errorf("Available = true, want false when connection only has calendar scope")
	}
	if st.Reason != "not_connected" {
		t.Errorf("Reason = %q, want 'not_connected'", st.Reason)
	}
	if st.Mailbox != oid.String() || st.Address != "kari@nordlys.no" {
		t.Errorf("st = %+v, want mailbox %s and address kari@nordlys.no", st, oid)
	}
}

func TestPersonalMailbox_Status_AvailableWhenMailConnected(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, userID, oid := f.person(t, "kari@nordlys.no")
	connectOutlookWithFeature(t, f, c, oid, "kari@nordlys.no", "mail", []string{"Mail.Send"})

	prov := personalProvider(f.h)
	ctx := contracts.AsUser(context.Background(), userID)
	st, err := prov.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Available {
		t.Errorf("Available = false, want true; reason = %q", st.Reason)
	}
	if st.Mailbox != oid.String() || st.Address != "kari@nordlys.no" {
		t.Errorf("st = %+v, want mailbox %s and address kari@nordlys.no", st, oid)
	}
}

func TestPersonalMailbox_Send_SizeLimit(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, userID, oid := f.person(t, "kari@nordlys.no")
	connectOutlookWithFeature(t, f, c, oid, "kari@nordlys.no", "mail", []string{"Mail.Send"})

	prov := personalProvider(f.h)
	ctx := contracts.AsUser(context.Background(), userID)

	huge := make([]byte, contracts.MaxPersonalMessageBytes+1)
	err := prov.Send(ctx, huge, nil)
	if !errors.Is(err, contracts.ErrMailTooLarge) {
		t.Errorf("Send huge = %v, want ErrMailTooLarge", err)
	}
}

func TestPersonalMailbox_Send_FailsWhenConsentMissing(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, userID, oid := f.person(t, "kari@nordlys.no")
	// Only calendar scope granted
	connectOutlookWithFeature(t, f, c, oid, "kari@nordlys.no", "calendar", []string{"Calendars.ReadWrite"})

	prov := personalProvider(f.h)
	ctx := contracts.AsUser(context.Background(), userID)

	err := prov.Send(ctx, personalMIME(""), nil)
	var needed *contracts.ConnectionNeededError
	if !errors.As(err, &needed) {
		t.Fatalf("Send without mail scope = %v, want ConnectionNeededError", err)
	}
	if needed.Reason != "consent_missing" {
		t.Errorf("needed.Reason = %q, want 'consent_missing'", needed.Reason)
	}
}

func TestPersonalMailbox_Send_SuccessNoBcc(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, userID, oid := f.person(t, "kari@nordlys.no")
	connectOutlookWithFeature(t, f, c, oid, "kari@nordlys.no", "mail", []string{"Mail.Send"})

	// Add mailbox user to graphtest fake
	f.g.AddUser(graphtest.User{
		ID:                oid.String(),
		UserPrincipalName: "kari@nordlys.no",
		Mail:              "kari@nordlys.no",
		DisplayName:       "Kari Nordmann",
	})

	prov := personalProvider(f.h)
	ctx := contracts.AsUser(context.Background(), userID)

	callsBefore := len(f.g.Calls())
	err := prov.Send(ctx, personalMIME(""), nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	calls := f.g.Calls()[callsBefore:]
	sentMail := false
	for _, call := range calls {
		if call.Method == http.MethodPost && strings.Contains(call.Path, "/users/"+oid.String()+"/sendMail") {
			sentMail = true
			break
		}
	}
	if !sentMail {
		t.Errorf("expected sendMail call, calls were: %+v", calls)
	}
}

func TestPersonalMailbox_Send_SuccessWithBcc(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, userID, oid := f.person(t, "kari@nordlys.no")
	connectOutlookWithFeature(t, f, c, oid, "kari@nordlys.no", "mail", []string{"Mail.Send"})

	f.g.AddUser(graphtest.User{
		ID:                oid.String(),
		UserPrincipalName: "kari@nordlys.no",
		Mail:              "kari@nordlys.no",
		DisplayName:       "Kari Nordmann",
	})

	prov := personalProvider(f.h)
	ctx := contracts.AsUser(context.Background(), userID)

	callsBefore := len(f.g.Calls())
	bcc := []string{"blind@example.com"}
	err := prov.Send(ctx, personalMIME("blind@example.com"), bcc)
	if err != nil {
		t.Fatalf("Send with bcc: %v", err)
	}

	calls := f.g.Calls()[callsBefore:]
	createdDraft, sentDraft := false, false
	for _, call := range calls {
		if call.Method == http.MethodPost && strings.Contains(call.Path, "/users/"+oid.String()+"/messages") && !strings.Contains(call.Path, "/send") {
			createdDraft = true
		}
		if call.Method == http.MethodPost && strings.Contains(call.Path, "/messages/") && strings.Contains(call.Path, "/send") {
			sentDraft = true
		}
	}
	if !createdDraft || !sentDraft {
		t.Errorf("expected draft create and send, got created=%v sent=%v (calls: %+v)", createdDraft, sentDraft, calls)
	}
}

func TestConnect_IncrementalConsent_PreservesFeatures(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, _, oid := f.person(t, "kari@nordlys.no")

	// 1. Connect Calendar
	connectOutlookWithFeature(t, f, c, oid, "kari@nordlys.no", "calendar", []string{"Calendars.ReadWrite"})
	conn1 := getOutlook(t, c)
	if !slices.Equal(conn1.Features, []string{"calendar"}) {
		t.Fatalf("first connect features = %v, want ['calendar']", conn1.Features)
	}

	// 2. Incremental Connect for Mail: verify AuthorizeUrl asks for both scopes
	u, _ := startConnectWithFeature(t, c, "mail", "/settings/connections")
	scopeParam := u.Query().Get("scope")
	if !strings.Contains(scopeParam, "Calendars.ReadWrite") || !strings.Contains(scopeParam, "Mail.Send") {
		t.Errorf("incremental connect scope = %q, want both Calendars.ReadWrite and Mail.Send", scopeParam)
	}

	// 3. Complete the mail connect: Microsoft answers the union it consented
	// to, as it does when both were asked.
	completeConnect(t, f, c, u, oid, []string{"User.Read", "openid", "profile", "Calendars.ReadWrite", "Mail.Send"})

	// 4. Verify both features are present on the connection
	conn2 := getOutlook(t, c)
	if !slices.Equal(conn2.Features, []string{"calendar", "mail"}) {
		t.Errorf("after incremental connect features = %v, want ['calendar', 'mail']", conn2.Features)
	}
}

// TestComplete_RecordsTheAnswerAlone proves the row's scopes are the token's
// answer and nothing more: a connect that asked calendar again beside mail,
// answered without calendar — a consent withdrawn meanwhile —, leaves a
// connection with mail alone, which the calendar's calls then refuse as
// consent_missing instead of failing at Graph.
func TestComplete_RecordsTheAnswerAlone(t *testing.T) {
	t.Parallel()
	f := newOutlookFixture(t)
	c, _, oid := f.person(t, "kari@nordlys.no")
	connectOutlookWithFeature(t, f, c, oid, "kari@nordlys.no", "calendar", []string{"Calendars.ReadWrite"})

	u, _ := startConnectWithFeature(t, c, "mail", "/settings/connections")
	completeConnect(t, f, c, u, oid, []string{"User.Read", "openid", "profile", "Mail.Send"})

	conn := getOutlook(t, c)
	if !slices.Equal(conn.Features, []string{"mail"}) {
		t.Errorf("features after an answer without calendar = %v, want ['mail']", conn.Features)
	}
}

// completeConnect redeems the connect u started for oid with granted as the
// token's answer and completes it.
func completeConnect(t *testing.T, f *outlookFixture, c *modtest.Client, u *url.URL, oid uuid.UUID, granted []string) {
	t.Helper()
	q := u.Query()
	g := graphtest.CodeGrant{
		ClientID:    q.Get("client_id"),
		RedirectURI: q.Get("redirect_uri"),
		Challenge:   q.Get("code_challenge"),
		Scopes:      strings.Fields(q.Get("scope")),
		Nonce:       q.Get("nonce"),
		OID:         oid.String(),
		TID:         tenantID,
		UPN:         "kari@nordlys.no",
		Granted:     granted,
	}
	code := f.g.IssueCode(g)
	callback(t, c, url.Values{"code": {code}, "state": {q.Get("state")}})
	r := c.Do(http.MethodPost, outlookCompletePath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("POST /outlook/complete: %d %s", r.Status, r.Body)
	}
}
