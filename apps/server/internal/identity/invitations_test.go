package identity_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const (
	ownerInvitationsPath    = "/api/v1/identity/owner/invitations"
	invitationsValidatePath = "/api/v1/identity/invitations/validate"
	invitationsAcceptPath   = "/api/v1/identity/invitations/accept"

	inviteePassword = "InviteePassword123"

	// mailFailureMarker is in every mail error a test injects, so a test can
	// tell that the error reached no answer and no log.
	mailFailureMarker = "mail-failure-marker"
)

// invitation is InvitationResponse as a test reads it.
type invitation struct {
	ID          uuid.UUID  `json:"id"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	DisplayName *string    `json:"displayName"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	RevokedAt   *time.Time `json:"revokedAt"`
	AcceptedAt  *time.Time `json:"acceptedAt"`
}

// invite creates an invitation through POST /owner/invitations as owner and
// returns it with the token its mail carried.
func invite(t *testing.T, h *harness, owner *client, body map[string]string) (invitation, string) {
	t.Helper()
	r := owner.do(http.MethodPost, ownerInvitationsPath, body)
	if r.status != http.StatusCreated {
		t.Fatalf("invite %s: status %d body %s", body["email"], r.status, r.body)
	}
	var inv invitation
	r.json(&inv)
	return inv, mailedLink(t, h, inv.Email).Query().Get("token")
}

// accept sends POST /invitations/accept from c, with displayName only when
// it is set.
func accept(c *client, token, password, displayName string) *resp {
	body := map[string]string{"token": token, "password": password}
	if displayName != "" {
		body["displayName"] = displayName
	}
	return c.do(http.MethodPost, invitationsAcceptPath, body)
}

// validation is InvitationAcceptanceResponse as a test reads it.
type validation struct {
	Valid     bool       `json:"valid"`
	Email     *string    `json:"email"`
	Role      *string    `json:"role"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

func validateToken(t *testing.T, c *client, token string) validation {
	t.Helper()
	r := c.do(http.MethodGet, invitationsValidatePath+"?token="+url.QueryEscape(token), nil)
	if r.status != http.StatusOK {
		t.Fatalf("validate: status %d body %s", r.status, r.body)
	}
	var v validation
	r.json(&v)
	return v
}

// ownerInvitations lists the invitations as owner, newest first.
func ownerInvitations(t *testing.T, owner *client) []invitation {
	t.Helper()
	r := owner.do(http.MethodGet, ownerInvitationsPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("list invitations: status %d body %s", r.status, r.body)
	}
	var list []invitation
	r.json(&list)
	return list
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// TestInvitations_CreateValidateAndAcceptRoundTrip follows one invitation
// from the Owner's request, through the link its mail carries, to the new
// account's first session.
func TestInvitations_CreateValidateAndAcceptRoundTrip(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)

	r := owner.do(http.MethodPost, ownerInvitationsPath, map[string]string{
		"email": "  invitee@example.test ", "role": identity.RoleUser, "displayName": " Invited Person ",
	})
	if r.status != http.StatusCreated {
		t.Fatalf("invite: status %d body %s", r.status, r.body)
	}
	var inv invitation
	r.json(&inv)
	if got, want := r.header("Location"), ownerInvitationsPath+"/"+inv.ID.String(); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if inv.Email != "invitee@example.test" || inv.Role != identity.RoleUser || str(inv.DisplayName) != "Invited Person" ||
		!inv.CreatedAt.Equal(start) || !inv.ExpiresAt.Equal(start.Add(7*24*time.Hour)) {
		t.Errorf("invitation = %+v, want the trimmed email and name, made at the clock and expiring 7 days later", inv)
	}
	if !strings.Contains(string(r.body), `"revokedAt":null`) || !strings.Contains(string(r.body), `"acceptedAt":null`) {
		t.Errorf("body %s, want explicit null revokedAt and acceptedAt", r.body)
	}

	// The mail is .NET's template, the link APP_URL's accept page.
	msgs := h.mailTo("invitee@example.test")
	if len(msgs) != 1 {
		t.Fatalf("%d mails to the invitee, want 1", len(msgs))
	}
	token := mailedLink(t, h, "invitee@example.test").Query().Get("token")
	wantBody := "Use this link to create your Vantigo account: " + h.srv.URL + "/invitations/accept?token=" + token
	if msgs[0].Subject != "You are invited to Vantigo" || msgs[0].TextBody != wantBody {
		t.Errorf("mail = %q / %q, want %q / %q", msgs[0].Subject, msgs[0].TextBody, "You are invited to Vantigo", wantBody)
	}
	// Only the token's SHA-256 is stored.
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("token %q: %d bytes, err %v; want 32 bytes of base64url", token, len(raw), err)
	}
	hash := sha256.Sum256(raw)
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE id = $1 AND token_hash = $2`, inv.ID, hash[:]); n != 1 {
		t.Errorf("the stored token_hash is not SHA-256 of the mailed token")
	}

	anonymous := h.client(t)
	if v := validateToken(t, anonymous, token); !v.Valid || str(v.Email) != "invitee@example.test" || str(v.Role) != identity.RoleUser ||
		v.ExpiresAt == nil || !v.ExpiresAt.Equal(inv.ExpiresAt) {
		t.Errorf("validate = %+v, want valid for the invitee's email, role and expiry", v)
	}
	if r := anonymous.do(http.MethodGet, invitationsValidatePath+"?token=garbage", nil); strings.TrimSpace(string(r.body)) != `{"valid":false}` {
		t.Errorf("validate garbage: body %s, want {\"valid\":false}", r.body)
	}

	invitee := h.client(t)
	a := accept(invitee, token, inviteePassword, "")
	if a.status != http.StatusCreated {
		t.Fatalf("accept: status %d body %s", a.status, a.body)
	}
	if got := a.header("Location"); got != "/api/v1/identity/session" {
		t.Errorf("Location = %q, want /api/v1/identity/session", got)
	}
	if c := a.setCookie(identity.SessionCookieName); c == nil || c.Value == "" || !c.HttpOnly || c.MaxAge != 0 {
		t.Errorf("session cookie = %+v, want a non-persistent HttpOnly session", c)
	}
	var body struct {
		User struct {
			ID          uuid.UUID `json:"id"`
			DisplayName string    `json:"displayName"`
			Email       *string   `json:"email"`
			Roles       []string  `json:"roles"`
		} `json:"user"`
		RequiresTwoFactor     bool `json:"requiresTwoFactor"`
		MfaEnrollmentRequired bool `json:"mfaEnrollmentRequired"`
	}
	a.json(&body)
	userID := body.User.ID
	if body.User.DisplayName != "Invited Person" || str(body.User.Email) != "invitee@example.test" ||
		!slices.Equal(body.User.Roles, []string{identity.RoleUser}) || body.RequiresTwoFactor || body.MfaEnrollmentRequired {
		t.Errorf("accept body = %s", a.body)
	}
	if !strings.Contains(string(a.body), `"tenants":[]`) || !strings.Contains(string(a.body), `"activeTenantId":null`) {
		t.Errorf("accept body %s, want tenants [] and activeTenantId null", a.body)
	}
	admitted(t, invitee)

	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND email_confirmed AND normalized_email = 'INVITEE@EXAMPLE.TEST'`, userID); n != 1 {
		t.Errorf("the account is not the invitee's with a confirmed email")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE id = $1 AND accepted_at = $2`, inv.ID, start); n != 1 {
		t.Errorf("the invitation is not marked accepted at the clock")
	}
	events := h.auditEvents(t, "invitation.accepted-with-role")
	if len(events) != 1 {
		t.Fatalf("%d invitation.accepted-with-role events, want 1", len(events))
	}
	e := events[0]
	wantBefore := `{"InvitationId":"` + inv.ID.String() + `","Roles":[]}`
	wantAfter := `{"UserId":"` + userID.String() + `","Roles":["User"],"PermissionKeys":[],"IsDisabled":false,"IsDeleted":false}`
	if e.Actor != nil || e.TargetUser == nil || *e.TargetUser != userID || e.Before != wantBefore || e.After != wantAfter || e.MFA {
		t.Errorf("audit = %+v, want no actor, the new user, before %s and after %s", e, wantBefore, wantAfter)
	}

	// Single use: the token is spent.
	if v := validateToken(t, anonymous, token); v.Valid {
		t.Errorf("validate after acceptance = %+v, want invalid", v)
	}
	if r := accept(h.client(t), token, inviteePassword, ""); r.status != http.StatusBadRequest || r.code() != "invitation_invalid" {
		t.Errorf("second acceptance: status %d code %q, want 400 invitation_invalid", r.status, r.code())
	}
	list := ownerInvitations(t, owner)
	if len(list) != 1 || list[0].AcceptedAt == nil || !list[0].AcceptedAt.Equal(start) || list[0].RevokedAt != nil {
		t.Errorf("invitations = %+v, want the one accepted invitation", list)
	}
	h.login(t, "invitee@example.test", inviteePassword)
	_ = ownerID
}

// TestInvitations_DisplayNameComesFromTheRequestThenTheInvitationThenTheEmail
// pins acceptance's display-name precedence (EA/AuthAccountEndpoints.cs:1319-1321).
func TestInvitations_DisplayNameComesFromTheRequestThenTheInvitationThenTheEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	for _, c := range []struct {
		email, invited, requested, want string
	}{
		{"requested@example.test", "From Invitation", "  From Request ", "From Request"},
		{"invited@example.test", "From Invitation", "", "From Invitation"},
		{"neither@example.test", "", "", "neither@example.test"},
	} {
		body := map[string]string{"email": c.email, "role": identity.RoleUser}
		if c.invited != "" {
			body["displayName"] = c.invited
		}
		_, token := invite(t, h, owner, body)
		r := accept(h.client(t), token, inviteePassword, c.requested)
		if r.status != http.StatusCreated {
			t.Fatalf("accept %s: status %d body %s", c.email, r.status, r.body)
		}
		var got struct {
			User struct {
				DisplayName string `json:"displayName"`
			} `json:"user"`
		}
		r.json(&got)
		if got.User.DisplayName != c.want {
			t.Errorf("%s: display name %q, want %q", c.email, got.User.DisplayName, c.want)
		}
	}
}

// TestInvitations_OwnerInvitationGrantsOwner accepts an Owner invitation
// (under the owner lock) and uses the new Owner's session on an owner
// endpoint.
func TestInvitations_OwnerInvitationGrantsOwner(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	_, token := invite(t, h, owner, map[string]string{"email": "second-owner@example.test", "role": identity.RoleOwner})

	newOwner := h.client(t)
	r := accept(newOwner, token, inviteePassword, "")
	if r.status != http.StatusCreated {
		t.Fatalf("accept: status %d body %s", r.status, r.body)
	}
	var got struct {
		User struct {
			Roles []string `json:"roles"`
		} `json:"user"`
	}
	r.json(&got)
	if !slices.Equal(got.User.Roles, []string{identity.RoleOwner}) {
		t.Errorf("roles %v, want [Owner]", got.User.Roles)
	}
	if r := newOwner.do(http.MethodGet, ownerUsersPath, nil); r.status != http.StatusOK {
		t.Errorf("the new Owner's GET /owner/users: status %d, want 200", r.status)
	}
}

// TestInvitations_ResendMintsANewTokenAndRevokesTheOld resends an
// invitation twice: each resend is a new invitation with a new token, and
// every earlier token stops working. An accepted invitation cannot be
// resent.
func TestInvitations_ResendMintsANewTokenAndRevokesTheOld(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	first, firstToken := invite(t, h, owner, map[string]string{"email": "resent@example.test", "role": identity.RoleUser, "displayName": "Resent"})

	h.advance(time.Hour)
	r := owner.do(http.MethodPost, ownerInvitationsPath+"/"+first.ID.String()+"/resend", map[string]any{})
	if r.status != http.StatusOK {
		t.Fatalf("resend: status %d body %s", r.status, r.body)
	}
	var second invitation
	r.json(&second)
	secondToken := mailedLink(t, h, "resent@example.test").Query().Get("token")
	if second.ID == first.ID || second.Email != first.Email || second.Role != first.Role || str(second.DisplayName) != "Resent" ||
		!second.CreatedAt.Equal(start.Add(time.Hour)) || !second.ExpiresAt.Equal(start.Add(time.Hour+7*24*time.Hour)) {
		t.Errorf("resent invitation = %+v, want a new one for the same email, role and name, made now", second)
	}
	if n := len(h.mailTo("resent@example.test")); n != 2 || secondToken == firstToken {
		t.Fatalf("%d mails, tokens equal %v; want a second mail with a new token", n, secondToken == firstToken)
	}
	anonymous := h.client(t)
	if validateToken(t, anonymous, firstToken).Valid || !validateToken(t, anonymous, secondToken).Valid {
		t.Errorf("after the resend the first token must be invalid and the second valid")
	}

	// The resent invitation is revoked; resending it again is still allowed
	// (it was never accepted), and replaces the second.
	list := ownerInvitations(t, owner)
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID || list[1].RevokedAt == nil || list[0].RevokedAt != nil {
		t.Fatalf("invitations = %+v, want the second active, then the first revoked", list)
	}
	r = owner.do(http.MethodPost, ownerInvitationsPath+"/"+first.ID.String()+"/resend", nil)
	if r.status != http.StatusOK {
		t.Fatalf("resend the revoked one: status %d body %s", r.status, r.body)
	}
	var third invitation
	r.json(&third)
	thirdToken := mailedLink(t, h, "resent@example.test").Query().Get("token")
	if validateToken(t, anonymous, secondToken).Valid {
		t.Errorf("the second token survived the third invitation")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE normalized_email = 'RESENT@EXAMPLE.TEST' AND revoked_at IS NULL AND accepted_at IS NULL`); n != 1 {
		t.Errorf("%d active invitations for the email, want 1", n)
	}

	if r := accept(h.client(t), thirdToken, inviteePassword, ""); r.status != http.StatusCreated {
		t.Fatalf("accept: status %d body %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPost, ownerInvitationsPath+"/"+third.ID.String()+"/resend", nil); r.status != http.StatusConflict || r.code() != "invitation_not_active" {
		t.Errorf("resend accepted: status %d code %q, want 409 invitation_not_active", r.status, r.code())
	}
	if r := owner.do(http.MethodPost, ownerInvitationsPath+"/"+uuid.NewString()+"/resend", nil); r.status != http.StatusNotFound || len(r.body) != 0 {
		t.Errorf("resend unknown: status %d body %q, want a bare 404", r.status, r.body)
	}
}

// TestInvitations_ExpiredInvitationIsRejected crosses the invitation's
// lifetime on the harness clock: valid up to the last second, then refused.
func TestInvitations_ExpiredInvitationIsRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	_, token := invite(t, h, owner, map[string]string{"email": "late@example.test", "role": identity.RoleUser})

	anonymous := h.client(t)
	h.advance(7*24*time.Hour - time.Second)
	if !validateToken(t, anonymous, token).Valid {
		t.Fatalf("one second before expiry the invitation is not valid")
	}
	h.advance(time.Second)
	if v := validateToken(t, anonymous, token); v.Valid {
		t.Errorf("at expiry validate = %+v, want invalid", v)
	}
	if r := accept(h.client(t), token, inviteePassword, ""); r.status != http.StatusBadRequest || r.code() != "invitation_invalid" {
		t.Errorf("accept at expiry: status %d code %q, want 400 invitation_invalid", r.status, r.code())
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE normalized_email = 'LATE@EXAMPLE.TEST'`); n != 0 {
		t.Errorf("an expired invitation created an account")
	}
}

// TestInvitations_LifetimeComesFromConfiguration sets INVITATION_LIFETIME.
func TestInvitations_LifetimeComesFromConfiguration(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("INVITATION_LIFETIME", "48h"))
	owner, _ := h.bootstrapOwner(t)
	inv, _ := invite(t, h, owner, map[string]string{"email": "soon@example.test", "role": identity.RoleUser})
	if !inv.ExpiresAt.Equal(start.Add(48 * time.Hour)) {
		t.Errorf("expiresAt = %v, want %v", inv.ExpiresAt, start.Add(48*time.Hour))
	}
}

// TestInvitations_AFailedSendRevokesTheInvitation fails the mail of a new
// invitation and of a resend: each answers 500 without the mail error, and
// leaves its invitation revoked, so no invitation is pending that nobody
// holds the link to.
func TestInvitations_AFailedSendRevokesTheInvitation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "unreachable@example.test"
	active := func() int {
		return h.count(t, `SELECT count(*) FROM identity.invitations WHERE normalized_email = 'UNREACHABLE@EXAMPLE.TEST' AND revoked_at IS NULL AND accepted_at IS NULL`)
	}

	h.mail.FailNext(errors.New("smtp: mailbox unavailable (" + mailFailureMarker + "), affected recipient(s): " + email))
	// Off-contract by design: the send failure's 500 problem is not in identity.yaml.
	r := owner.do(http.MethodPost, ownerInvitationsPath, map[string]string{"email": email, "role": identity.RoleUser}, skipContract("send failure 500"))
	if r.status != http.StatusInternalServerError || r.header("Content-Type") != "application/problem+json" {
		t.Fatalf("failed send: status %d Content-Type %q, want the 500 problem", r.status, r.header("Content-Type"))
	}
	if strings.Contains(string(r.body), mailFailureMarker) || strings.Contains(string(r.body), email) {
		t.Errorf("the 500 echoes the mail error: %s", r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE normalized_email = 'UNREACHABLE@EXAMPLE.TEST' AND revoked_at IS NOT NULL`); n != 1 || active() != 0 {
		t.Errorf("%d revoked and %d active invitations, want the one revoked", n, active())
	}
	if len(h.mailTo(email)) != 0 {
		t.Errorf("a mail was recorded for the failed send")
	}

	inv, _ := invite(t, h, owner, map[string]string{"email": email, "role": identity.RoleUser})
	h.mail.FailNext(errors.New("smtp: try again later (" + mailFailureMarker + ")"))
	if r := owner.do(http.MethodPost, ownerInvitationsPath+"/"+inv.ID.String()+"/resend", nil, skipContract("send failure 500")); r.status != http.StatusInternalServerError {
		t.Fatalf("failed resend: status %d, want 500", r.status)
	}
	if active() != 0 {
		t.Errorf("a failed resend left %d active invitations, want 0: the replacement and the original are both revoked", active())
	}
	for _, rec := range h.logRecords(t) {
		if line := strings.Join(logValues(rec), " "); strings.Contains(line, email) || strings.Contains(line, mailFailureMarker) {
			t.Errorf("a log record carries the address or the mail error: %v", rec)
		}
	}
}

// logValues flattens a log record's values to strings.
func logValues(rec map[string]any) []string {
	var out []string
	for _, v := range rec {
		switch v := v.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			out = append(out, logValues(v)...)
		}
	}
	return out
}

// TestInvitations_CreateRefusesInvalidRequestsAndExistingAccounts covers
// creation's refusals, and the one active invitation per email.
func TestInvitations_CreateRefusesInvalidRequestsAndExistingAccounts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)

	for _, c := range []struct {
		body map[string]string
		want map[string][]string
	}{
		{map[string]string{"email": "", "role": ""}, map[string][]string{
			"email": {"A valid email is required."}, "role": {"Role must be User or Owner."}}},
		{map[string]string{"email": "no-at-sign", "role": "Administrator"}, map[string][]string{
			"email": {"A valid email is required."}, "role": {"Role must be User or Owner."}}},
		{map[string]string{"email": "long@example.test", "role": identity.RoleUser, "displayName": strings.Repeat("n", 201)}, map[string][]string{
			"displayName": {"Display name must be at most 200 characters."}}},
	} {
		r := owner.do(http.MethodPost, ownerInvitationsPath, c.body)
		if e := errorOf(t, r); r.status != http.StatusBadRequest || e.Code != "invalid_request" || e.Message != "The invitation request is invalid." || !fieldsEqual(e.Fields, c.want) {
			t.Errorf("%v: status %d error %+v, want 400 invalid_request with %v", c.body, r.status, e, c.want)
		}
	}
	r := owner.do(http.MethodPost, ownerInvitationsPath, map[string]string{"email": " OWNER@example.test", "role": identity.RoleUser})
	if e := errorOf(t, r); r.status != http.StatusConflict || e.Code != "account_exists" || e.Message != "An account already exists for this email address." {
		t.Errorf("existing account: status %d error %+v, want 409 account_exists", r.status, e)
	}

	first, firstToken := invite(t, h, owner, map[string]string{"email": "twice@example.test", "role": identity.RoleUser})
	h.advance(time.Minute) // newest first, so the second lists first
	_, secondToken := invite(t, h, owner, map[string]string{"email": "TWICE@example.test", "role": identity.RoleOwner})
	anonymous := h.client(t)
	if validateToken(t, anonymous, firstToken).Valid || !validateToken(t, anonymous, secondToken).Valid {
		t.Errorf("a second invitation for the email must revoke the first")
	}
	if list := ownerInvitations(t, owner); len(list) != 2 || list[1].ID != first.ID || list[1].RevokedAt == nil {
		t.Errorf("invitations = %+v, want the first revoked", list)
	}
}

// TestInvitations_RevokeIsIdempotent revokes a pending invitation twice,
// an accepted one, and an unknown one.
func TestInvitations_RevokeIsIdempotent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	pending, pendingToken := invite(t, h, owner, map[string]string{"email": "pending@example.test", "role": identity.RoleUser})
	revokePath := ownerInvitationsPath + "/" + pending.ID.String() + "/revoke"

	var revoked invitation
	r := owner.do(http.MethodPost, revokePath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("revoke: status %d body %s", r.status, r.body)
	}
	r.json(&revoked)
	if revoked.RevokedAt == nil || !revoked.RevokedAt.Equal(start) {
		t.Fatalf("revokedAt = %v, want the clock", revoked.RevokedAt)
	}
	h.advance(time.Minute)
	var again invitation
	owner.do(http.MethodPost, revokePath, nil).json(&again)
	if again.RevokedAt == nil || !again.RevokedAt.Equal(start) {
		t.Errorf("revoking again moved revokedAt to %v, want it unchanged at %v", again.RevokedAt, start)
	}
	if validateToken(t, h.client(t), pendingToken).Valid {
		t.Errorf("a revoked invitation still validates")
	}
	if r := accept(h.client(t), pendingToken, inviteePassword, ""); r.status != http.StatusBadRequest || r.code() != "invitation_invalid" {
		t.Errorf("accept revoked: status %d code %q, want 400 invitation_invalid", r.status, r.code())
	}

	accepted, acceptedToken := invite(t, h, owner, map[string]string{"email": "accepted@example.test", "role": identity.RoleUser})
	if r := accept(h.client(t), acceptedToken, inviteePassword, ""); r.status != http.StatusCreated {
		t.Fatalf("accept: status %d body %s", r.status, r.body)
	}
	var kept invitation
	owner.do(http.MethodPost, ownerInvitationsPath+"/"+accepted.ID.String()+"/revoke", nil).json(&kept)
	if kept.RevokedAt != nil || kept.AcceptedAt == nil {
		t.Errorf("revoking an accepted invitation = %+v, want it left accepted and unrevoked", kept)
	}
	if r := owner.do(http.MethodPost, ownerInvitationsPath+"/"+uuid.NewString()+"/revoke", nil); r.status != http.StatusNotFound || len(r.body) != 0 {
		t.Errorf("revoke unknown: status %d body %q, want a bare 404", r.status, r.body)
	}
}

// TestInvitations_AcceptRefusesAnExistingAccountAndMissingFields: an
// account created for the email after the invitation (here directly, as
// OIDC provisioning would) makes acceptance a validation failure, and the
// invitation stays pending.
func TestInvitations_AcceptRefusesAnExistingAccountAndMissingFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	inv, token := invite(t, h, owner, map[string]string{"email": "taken@example.test", "role": identity.RoleUser})
	h.insertUser(t, "taken@example.test", identity.RoleUserID)

	r := accept(h.client(t), token, inviteePassword, "")
	want := map[string][]string{
		"DuplicateUserName": {"Username 'taken@example.test' is already taken."},
		"DuplicateEmail":    {"Email 'taken@example.test' is already taken."},
	}
	if e := errorOf(t, r); r.status != http.StatusBadRequest || e.Code != "identity_validation_failed" ||
		e.Message != "The invitation account could not be created." || !fieldsEqual(e.Fields, want) {
		t.Errorf("accept for an existing account: status %d error %+v", r.status, e)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE id = $1 AND accepted_at IS NULL`, inv.ID); n != 1 {
		t.Errorf("the refused acceptance marked the invitation accepted")
	}

	for _, body := range []map[string]string{
		{"token": "", "password": ""},
		{"token": token, "password": ""},
		{"token": "", "password": inviteePassword},
		{"token": "not-a-token", "password": inviteePassword},
	} {
		if r := h.client(t).do(http.MethodPost, invitationsAcceptPath, body); r.status != http.StatusBadRequest || r.code() != "invitation_invalid" {
			t.Errorf("accept %v: status %d code %q, want 400 invitation_invalid", body, r.status, r.code())
		}
	}
}

// TestInvitations_ConcurrentAcceptancesCreateOneAccount races two
// acceptances of one token: the second waits on the invitation's row lock,
// then fails to serialize and, retried, finds the invitation accepted.
// Run with -count to repeat the race.
func TestInvitations_ConcurrentAcceptancesCreateOneAccount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	_, token := invite(t, h, owner, map[string]string{"email": "racer@example.test", "role": identity.RoleOwner})

	a, b := h.client(t), h.client(t)
	responses := race(
		func() *resp { return accept(a, token, inviteePassword, "") },
		func() *resp { return accept(b, token, inviteePassword, "") },
	)
	statuses := []int{responses[0].status, responses[1].status}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusCreated, http.StatusBadRequest}) {
		t.Fatalf("statuses %v, want one 201 and one 400", statuses)
	}
	for _, r := range responses {
		if r.status == http.StatusBadRequest && r.code() != "invitation_invalid" {
			t.Errorf("the loser's code = %q, want invitation_invalid", r.code())
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE normalized_email = 'RACER@EXAMPLE.TEST'`); n != 1 {
		t.Errorf("%d accounts, want 1", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions s JOIN identity.users u ON u.id = s.user_id WHERE u.normalized_email = 'RACER@EXAMPLE.TEST'`); n != 1 {
		t.Errorf("%d sessions, want 1", n)
	}
}

// TestInvitations_ConcurrentCreatesForOneEmailLeaveOneActive races two
// invitations for one email, forced to overlap: the test holds the row lock
// of the email's pending invitation, so both creations take their
// snapshots and queue on it. Released, one revokes and inserts; the other
// fails to serialize and, retried from a fresh snapshot, revokes the
// winner's invitation in turn, exactly as an invitation sent a moment later
// would. Both answer 201, one invitation is active, and only its mailed
// link validates. Without the retry the loser would be 409
// account_conflict. Run with -count to repeat the race.
func TestInvitations_ConcurrentCreatesForOneEmailLeaveOneActive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "contested@example.test"
	pending, pendingToken := invite(t, h, owner, map[string]string{"email": email, "role": identity.RoleUser})

	ctx := context.Background()
	gate, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM identity.invitations WHERE id = $1 FOR UPDATE`, pending.ID); err != nil {
		t.Fatalf("gate: lock the pending invitation: %v", err)
	}
	create := func() *resp {
		return owner.do(http.MethodPost, ownerInvitationsPath, map[string]string{"email": email, "role": identity.RoleUser})
	}
	done := make(chan []*resp, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(create, create)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	for i, r := range responses {
		if r.status != http.StatusCreated {
			t.Errorf("create %d: status %d body %s, want 201", i, r.status, r.body)
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE normalized_email = 'CONTESTED@EXAMPLE.TEST' AND revoked_at IS NULL AND accepted_at IS NULL`); n != 1 {
		t.Fatalf("%d active invitations for the email, want 1", n)
	}
	msgs := h.mailTo(email)
	if len(msgs) != 3 {
		t.Fatalf("%d mails, want 3", len(msgs))
	}
	anonymous := h.client(t)
	valid := 0
	for _, m := range msgs[1:] {
		body := m.TextBody
		link, err := url.Parse(body[strings.LastIndex(body, " ")+1:])
		if err != nil {
			t.Fatalf("mail without a link: %q", body)
		}
		if validateToken(t, anonymous, link.Query().Get("token")).Valid {
			valid++
		}
	}
	if valid != 1 || validateToken(t, anonymous, pendingToken).Valid {
		t.Errorf("%d of the two new links validate (want 1), or the first invitation's still does", valid)
	}
}

// TestInvitations_OwnerEndpointsRequireAnOwner: anonymous callers get 401
// and a standard user 403 on each owner invitation operation.
func TestInvitations_OwnerEndpointsRequireAnOwner(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	h.createUser(t, owner, "user@example.test", identity.RoleUser)
	standard := h.login(t, "user@example.test", userPassword)
	id := uuid.NewString()
	for _, op := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, ownerInvitationsPath, nil},
		{http.MethodPost, ownerInvitationsPath, map[string]string{"email": "x@example.test", "role": identity.RoleUser}},
		{http.MethodPost, ownerInvitationsPath + "/" + id + "/resend", nil},
		{http.MethodPost, ownerInvitationsPath + "/" + id + "/revoke", nil},
	} {
		if r := h.client(t).do(op.method, op.path, op.body); r.status != http.StatusUnauthorized || r.code() != "unauthenticated" {
			t.Errorf("anonymous %s %s: status %d code %q, want 401", op.method, op.path, r.status, r.code())
		}
		if r := standard.do(op.method, op.path, op.body); r.status != http.StatusForbidden || r.code() != "forbidden" {
			t.Errorf("standard %s %s: status %d code %q, want 403", op.method, op.path, r.status, r.code())
		}
	}
	if len(h.mailTo("x@example.test")) != 0 {
		t.Errorf("a refused caller sent an invitation")
	}
}
