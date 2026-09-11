package identity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const (
	scimToken             = "identity-harness-scim-token"
	scimPreviousToken     = "identity-harness-previous-scim-token"
	scimPath              = "/api/v1/identity/scim/v2"
	scimUserSchema        = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimGroupSchema       = "urn:ietf:params:scim:schemas:core:2.0:Group"
	scimPatchSchema       = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimErrorSchema       = "urn:ietf:params:scim:api:messages:2.0:Error"
	scimUnauthorizedText  = "A valid SCIM bearer token is required."
	scimStaleText         = "The resource version is stale."
	scimStaleVersionText  = "The SCIM resource version is stale."
	scimProtectedText     = "The selected user is protected."
	scimNotFoundText      = "The requested resource was not found."
	scimImmutableText     = "externalId is immutable once mapped."
	scimUserConflictText  = "The SCIM resource conflicts with an existing resource."
	scimGroupConflictText = "The SCIM group conflicts with an existing group."
)

// newScimHarness is a harness with SCIM configured.
func newScimHarness(t *testing.T, opts ...harnessOption) *harness {
	t.Helper()
	return newHarness(t, append([]harnessOption{withEnv("SCIM_TOKEN", scimToken)}, opts...)...)
}

// withPreviousScimToken also configures the previous token, expiring at
// expires. Configuration checks the expiry against the wall clock, so a
// test using it moves the harness clock there (toWallClock).
func withPreviousScimToken(expires time.Time) harnessOption {
	return func(s *harnessSetup) {
		s.env["SCIM_PREVIOUS_TOKEN"] = scimPreviousToken
		s.env["SCIM_PREVIOUS_TOKEN_EXPIRES_AT"] = expires.UTC().Format(time.RFC3339)
	}
}

// toWallClock moves h's clock to the wall clock.
func (h *harness) toWallClock() {
	h.advance(time.Since(h.now()))
}

// scimClient is a SCIM client: every request carries its bearer token, and
// a body goes as application/scim+json. Each has its own client address.
type scimClient struct {
	*client
	token string
}

func (h *harness) scim(t testing.TB, token string) *scimClient {
	return &scimClient{client: h.client(t), token: token}
}

// do sends method to path under the SCIM base path, with body, when not
// nil, as application/scim+json.
func (c *scimClient) do(method, path string, body any, opts ...reqOpt) *resp {
	c.t.Helper()
	all := []reqOpt{header("Authorization", "Bearer "+c.token)}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("%s %s: encode body: %v", method, path, err)
		}
		all = append(all, rawBody("application/scim+json", b))
	}
	return c.client.do(method, scimPath+path, nil, append(all, opts...)...)
}

// addHeader adds a header line, keeping any already set.
func addHeader(k, v string) reqOpt {
	return func(r *request) { r.header.Add(k, v) }
}

// ifMatch is an If-Match header naming etag.
func ifMatch(etag string) reqOpt { return header("If-Match", `"`+etag+`"`) }

// etagOf is r's ETag without its quotes.
func etagOf(r *resp) string { return strings.Trim(r.header("ETag"), `"`) }

// wantScimError fails t unless r is the SCIM error with status, scimType
// ("" for null) and detail, as application/scim+json.
func wantScimError(t testing.TB, what string, r *resp, status int, scimType, detail string) {
	t.Helper()
	var e struct {
		Schemas  []string `json:"schemas"`
		Status   string   `json:"status"`
		ScimType *string  `json:"scimType"`
		Detail   string   `json:"detail"`
	}
	_ = json.Unmarshal(r.body, &e)
	got := ""
	if e.ScimType != nil {
		got = *e.ScimType
	}
	if r.status != status || r.header("Content-Type") != "application/scim+json" || e.Status != strconv.Itoa(status) ||
		got != scimType || (scimType == "") != (e.ScimType == nil) || e.Detail != detail || !slices.Equal(e.Schemas, []string{scimErrorSchema}) {
		t.Errorf("%s: %d %q %s\nwant %d %s %q", what, r.status, r.header("Content-Type"), r.body, status, scimType, detail)
	}
}

type scimMeta struct {
	ResourceType string    `json:"resourceType"`
	Created      time.Time `json:"created"`
	LastModified time.Time `json:"lastModified"`
	Location     string    `json:"location"`
	Version      string    `json:"version"`
}

type scimUser struct {
	ID          string `json:"id"`
	ExternalID  string `json:"externalId"`
	UserName    string `json:"userName"`
	Active      bool   `json:"active"`
	DisplayName string `json:"displayName"`
	Name        *struct {
		GivenName  *string `json:"givenName"`
		FamilyName *string `json:"familyName"`
	} `json:"name"`
	Emails []struct {
		Value   string `json:"value"`
		Type    string `json:"type"`
		Primary bool   `json:"primary"`
	} `json:"emails"`
	Meta scimMeta `json:"meta"`
}

type scimMember struct {
	Value string `json:"value"`
	Type  string `json:"type"`
	Ref   string `json:"ref"`
}

type scimGroup struct {
	ID          string        `json:"id"`
	ExternalID  *string       `json:"externalId"`
	DisplayName string        `json:"displayName"`
	Active      bool          `json:"active"`
	Members     *[]scimMember `json:"members"`
	Meta        scimMeta      `json:"meta"`
}

type scimList[T any] struct {
	TotalResults int `json:"totalResults"`
	StartIndex   int `json:"startIndex"`
	ItemsPerPage int `json:"itemsPerPage"`
	Resources    []T `json:"resources"`
}

// scimOf decodes r, which must have status, as a T.
func scimOf[T any](t testing.TB, what string, r *resp, status int) T {
	t.Helper()
	if r.status != status {
		t.Fatalf("%s: status %d body %s, want %d", what, r.status, r.body, status)
	}
	var v T
	r.json(&v)
	return v
}

// memberIDs is g's member ids, in the order listed.
func memberIDs(g scimGroup) []string {
	var ids []string
	if g.Members != nil {
		for _, m := range *g.Members {
			ids = append(ids, m.Value)
		}
	}
	return ids
}

// userResource is a User resource for userName with a new externalId and
// a primary work email at example.test, with extra over it.
func userResource(userName string, extra map[string]any) map[string]any {
	body := map[string]any{
		"schemas":    []string{scimUserSchema},
		"userName":   userName,
		"externalId": uuid.NewString(),
		"emails":     []map[string]any{{"value": userName + "@example.test", "type": "work", "primary": true}},
	}
	maps.Copy(body, extra)
	return body
}

// createUser creates the User resource as c, which must succeed, and
// returns it and its ETag.
func createUser(t testing.TB, c *scimClient, resource map[string]any) (scimUser, string) {
	t.Helper()
	r := c.do(http.MethodPost, "/Users", resource)
	return scimOf[scimUser](t, "create user", r, http.StatusCreated), etagOf(r)
}

// createGroup creates an active Group named displayName with the members
// as c, which must succeed, and returns it and its ETag.
func createScimGroup(t testing.TB, c *scimClient, displayName string, members ...string) (scimGroup, string) {
	t.Helper()
	r := c.do(http.MethodPost, "/Groups", map[string]any{"schemas": []string{scimGroupSchema}, "displayName": displayName, "members": memberValues(members...)})
	return scimOf[scimGroup](t, "create group "+displayName, r, http.StatusCreated), etagOf(r)
}

// memberValues is a members array of the Users ids.
func memberValues(ids ...string) []map[string]any {
	out := []map[string]any{}
	for _, id := range ids {
		out = append(out, map[string]any{"value": id, "type": "User"})
	}
	return out
}

// patchOps is a PatchOp of the operations.
func patchOps(ops ...map[string]any) map[string]any {
	return map[string]any{"schemas": []string{scimPatchSchema}, "Operations": ops}
}

// patchOp is one operation; an empty path and a nil value are left out.
func patchOp(op, path string, value any) map[string]any {
	m := map[string]any{"op": op}
	if path != "" {
		m["path"] = path
	}
	if value != nil {
		m["value"] = value
	}
	return m
}

// userOf is the account a User resource maps to.
func (h *harness) userOf(t testing.TB, resourceID string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := h.pool.QueryRow(context.Background(), `SELECT user_id FROM identity.scim_user_mappings WHERE resource_id = $1`, resourceID).Scan(&id); err != nil {
		t.Fatalf("harness: user of %s: %v", resourceID, err)
	}
	return id
}

// scimHeartbeat is the last authenticated SCIM request the Owner's system
// status reports.
func scimHeartbeat(t testing.TB, owner *client) *time.Time {
	t.Helper()
	var status ownerSystemStatusBody
	scimOfStatus := owner.do(http.MethodGet, systemStatusPath, nil)
	if scimOfStatus.status != http.StatusOK {
		t.Fatalf("system status: %d %s", scimOfStatus.status, scimOfStatus.body)
	}
	scimOfStatus.json(&status)
	return status.LastAuthenticatedScimRequestAtUtc
}

// Ported from StaticScimProtocolIntegrationTests.StaticTokenUsersGroupsEtagsLifecycleAndAuditRemainProtocolOnly.
// Extended: the created and deleted Users' audit rows are .NET's exactly,
// and every row the flow wrote is a SCIM event with no actor.
func TestScim_StaticTokenUsersGroupsEtagsLifecycleAndAuditRemainProtocolOnly(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)

	wantScimError(t, "an invalid token", h.scim(t, "not-the-static-token").do(http.MethodGet, "/ServiceProviderConfig", nil),
		http.StatusUnauthorized, "invalidValue", scimUnauthorizedText)

	c := h.scim(t, scimToken)
	userName := "scim-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	r := c.do(http.MethodPost, "/Users", map[string]any{
		"schemas":     []string{scimUserSchema},
		"externalId":  uuid.NewString(),
		"userName":    userName,
		"active":      true,
		"displayName": "SCIM Protocol User",
		"emails":      []map[string]any{{"value": userName + "@integration.test", "type": "work", "primary": true}},
	})
	user := scimOf[scimUser](t, "create", r, http.StatusCreated)
	initialETag := etagOf(r)
	if initialETag == "" || user.Meta.Version != initialETag {
		t.Fatalf("ETag %q, meta.version %q", initialETag, user.Meta.Version)
	}

	listed := scimOf[scimList[scimUser]](t, "filter", c.do(http.MethodGet, "/Users?filter="+url.QueryEscape(`userName eq "`+userName+`"`), nil), http.StatusOK)
	if listed.TotalResults != 1 {
		t.Errorf("totalResults = %d, want 1", listed.TotalResults)
	}

	r = c.do(http.MethodPatch, "/Users/"+user.ID, patchOps(patchOp("replace", "active", false)), header("If-Match", `"stale-version"`))
	wantScimError(t, "a stale PATCH", r, http.StatusPreconditionFailed, "invalidVers", scimStaleText)

	r = c.do(http.MethodPatch, "/Users/"+user.ID, patchOps(patchOp("replace", "active", false)), ifMatch(initialETag))
	patched := scimOf[scimUser](t, "patch", r, http.StatusOK)
	patchedETag := etagOf(r)
	if patchedETag == initialETag || patched.Meta.Version != patchedETag || patched.Active {
		t.Errorf("patch: ETag %q (was %q), active %t", patchedETag, initialETag, patched.Active)
	}

	r = c.do(http.MethodPost, "/Groups", map[string]any{
		"schemas":     []string{scimGroupSchema},
		"externalId":  uuid.NewString(),
		"displayName": "SCIM Protocol Group",
		"active":      true,
		"members":     memberValues(user.ID),
	})
	group := scimOf[scimGroup](t, "create group", r, http.StatusCreated)
	groupETag := etagOf(r)
	if ids := memberIDs(group); !slices.Equal(ids, []string{user.ID}) {
		t.Errorf("members = %v, want [%s]", ids, user.ID)
	}

	r = c.do(http.MethodPatch, "/Groups/"+group.ID, patchOps(patchOp("replace", "active", false)), ifMatch(groupETag))
	if g := scimOf[scimGroup](t, "patch group", r, http.StatusOK); g.Active {
		t.Error("the patched group is still active")
	}

	if r := c.do(http.MethodDelete, "/Users/"+user.ID, nil, ifMatch(patchedETag)); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if after := scimOf[scimUser](t, "get after delete", c.do(http.MethodGet, "/Users/"+user.ID, nil), http.StatusOK); after.Active {
		t.Error("the deleted user is still active")
	}

	for _, action := range []string{"scim.user.created", "scim.user.patched", "scim.user.deleted", "scim.group.created", "scim.group.patched"} {
		if n := len(h.auditEvents(t, action)); n != 1 {
			t.Errorf("%d %s rows, want 1", n, action)
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_audit_events WHERE action NOT LIKE 'scim.%' OR actor_user_id IS NOT NULL OR mfa_authenticated`); n != 0 {
		t.Errorf("%d audit rows are not SCIM events without an actor", n)
	}
	userID := h.userOf(t, user.ID)
	conn := identity.ScimConnectionID.String()
	facts := func(active bool, groups ...string) string {
		quoted := make([]string, len(groups))
		for i, g := range groups {
			quoted[i] = `"` + g + `"`
		}
		return `{"ConnectionId":"` + conn + `","ResourceId":"` + user.ID + `","Active":` + strconv.FormatBool(active) +
			`,"UpstreamGroupResourceIds":[` + strings.Join(quoted, ",") + `]}`
	}
	for _, want := range []struct {
		action, before, after string
	}{
		{"scim.user.created", `{"ScimConnectionId":"` + conn + `","ResourceId":"` + user.ID + `"}`, facts(true)},
		{"scim.user.patched", facts(true), facts(false)},
		{"scim.user.deleted", facts(false, group.ID), facts(false)},
	} {
		e := h.auditEvents(t, want.action)[0]
		if e.Actor != nil || e.TargetUser == nil || *e.TargetUser != userID || e.TargetRole != nil || e.MFA ||
			e.Details != `{"action":"`+want.action+`"}` || e.Before != want.before || e.After != want.after || !e.At.Equal(h.now()) {
			t.Errorf("%s: actor %v target %v details %s at %s\nbefore %s\nafter  %s\nwant before %s\nwant after  %s",
				want.action, e.Actor, e.TargetUser, e.Details, e.At, e.Before, e.After, want.before, want.after)
		}
	}
}

// Ported from StaticScimSafetyIntegrationTests.StaticStateUsesDeterministicConnection.
// The connection table is gone (spec *SCIM 2.0*). What stays deterministic
// is the one SCIM state per installation: the current and the previous
// token address the same Users and Groups, and every SCIM audit row names
// the one static connection, .NET's ScimConnection.StaticId.
func TestScimSafety_StaticStateUsesDeterministicConnection(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t, withPreviousScimToken(time.Now().Add(time.Hour)))
	h.toWallClock()

	if n := h.count(t, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'identity' AND table_name LIKE 'scim_connection%'`); n != 0 {
		t.Errorf("%d SCIM connection tables, want none", n)
	}
	current, previous := h.scim(t, scimToken), h.scim(t, scimPreviousToken)
	user, _ := createUser(t, current, userResource("one-state", nil))
	if got := scimOf[scimUser](t, "the previous token's GET", previous.do(http.MethodGet, "/Users/"+user.ID, nil), http.StatusOK); got.ID != user.ID {
		t.Errorf("the previous token sees %s, want %s", got.ID, user.ID)
	}
	group, _ := createScimGroup(t, previous, "One state", user.ID)
	listed := scimOf[scimList[scimGroup]](t, "the current token's list", current.do(http.MethodGet, "/Groups", nil), http.StatusOK)
	if listed.TotalResults != 1 || listed.Resources[0].ID != group.ID || !slices.Equal(memberIDs(listed.Resources[0]), []string{user.ID}) {
		t.Errorf("the current token lists %+v", listed)
	}

	conn := identity.ScimConnectionID.String()
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_audit_events WHERE action LIKE 'scim.%'`); n != 2 {
		t.Errorf("%d SCIM audit rows, want 2", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_audit_events WHERE action LIKE 'scim.%' AND NOT (after_json LIKE '%"ConnectionId":"`+conn+`"%')`); n != 0 {
		t.Errorf("%d SCIM audit rows name another connection", n)
	}
}

// Ported from StaticScimSafetyIntegrationTests.StaticProtocolRejectsRemovedControlPlaneRoutes.
func TestScimSafety_TheLegacyControlPlaneRouteIsGone(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	owner, _ := h.bootstrapOwner(t)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		// Off-contract by design: the removed route is not in the contract.
		r := owner.do(method, "/api/v1/identity/access/scim", nil, skipContract("the removed SCIM control-plane route"))
		if r.status != http.StatusNotFound {
			t.Errorf("%s /access/scim: status %d, want 404", method, r.status)
		}
	}
	if r := h.scim(t, scimToken).do(http.MethodGet, "/ServiceProviderConfig", nil); r.status != http.StatusOK {
		t.Errorf("ServiceProviderConfig: status %d, want 200", r.status)
	}
}

// New: the three discovery documents answer the bearer token, carrying
// the installation's base path in their locations (the documents
// themselves are pinned in TestScimDiscoveryDocuments).
func TestScim_DiscoveryAnswersTheBearerToken(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	c := h.scim(t, scimToken)

	var config struct {
		Patch  struct{ Supported bool } `json:"patch"`
		Filter struct {
			Supported  bool `json:"supported"`
			MaxResults int  `json:"maxResults"`
		} `json:"filter"`
		Etag struct{ Supported bool } `json:"etag"`
	}
	r := c.do(http.MethodGet, "/ServiceProviderConfig", nil)
	r.json(&config)
	if r.status != http.StatusOK || r.header("Content-Type") != "application/scim+json" || !config.Patch.Supported || !config.Etag.Supported || config.Filter.MaxResults != 100 {
		t.Errorf("ServiceProviderConfig: %d %s", r.status, r.body)
	}
	if schemas := scimOf[scimList[json.RawMessage]](t, "Schemas", c.do(http.MethodGet, "/Schemas", nil), http.StatusOK); schemas.TotalResults != 2 || len(schemas.Resources) != 2 {
		t.Errorf("Schemas: %+v", schemas)
	}
	types := scimOf[scimList[struct {
		ID       string `json:"id"`
		Endpoint string `json:"endpoint"`
	}]](t, "ResourceTypes", c.do(http.MethodGet, "/ResourceTypes", nil), http.StatusOK)
	if types.TotalResults != 2 || types.Resources[0].Endpoint != scimPath+"/Users" || types.Resources[1].Endpoint != scimPath+"/Groups" {
		t.Errorf("ResourceTypes: %+v", types)
	}
	for _, path := range []string{"/ServiceProviderConfig", "/Schemas", "/ResourceTypes"} {
		wantScimError(t, path+" without a token", h.client(t).do(http.MethodGet, scimPath+path, nil), http.StatusUnauthorized, "invalidValue", scimUnauthorizedText)
	}
}

// New: the bearer token is the configured one, compared exactly; anything
// else, SCIM off included, is the SCIM 401, and no 401 is a heartbeat.
func TestScim_TheBearerTokenIsTheConfiguredOne(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	owner, _ := h.bootstrapOwner(t)

	for _, authorization := range []string{"", "Basic " + scimToken, "Bearer", "Bearer ", "Bearer " + scimToken + "x", "Bearer " + scimToken[1:],
		"Bearer " + strings.ToUpper(scimToken), "Bearer " + scimToken + " extra", "Token " + scimToken} {
		r := h.client(t).do(http.MethodGet, scimPath+"/ServiceProviderConfig", nil, header("Authorization", authorization))
		wantScimError(t, fmt.Sprintf("Authorization %q", authorization), r, http.StatusUnauthorized, "invalidValue", scimUnauthorizedText)
	}
	if at := scimHeartbeat(t, owner); at != nil {
		t.Errorf("a refused request was recorded as a heartbeat at %s", at)
	}
	for _, authorization := range []string{"Bearer " + scimToken, "bearer " + scimToken, "BEARER  " + scimToken + " "} {
		if r := h.client(t).do(http.MethodGet, scimPath+"/ServiceProviderConfig", nil, header("Authorization", authorization)); r.status != http.StatusOK {
			t.Errorf("Authorization %q: status %d, want 200", authorization, r.status)
		}
	}

	off := newHarness(t)
	for _, token := range []string{scimToken, "anything"} {
		wantScimError(t, "SCIM off", off.scim(t, token).do(http.MethodGet, "/ServiceProviderConfig", nil), http.StatusUnauthorized, "invalidValue", scimUnauthorizedText)
	}
}

// New: during a rotation the previous token is accepted while the clock is
// before its expiry, and refused from the expiry on; the current one is
// accepted throughout.
func TestScim_ThePreviousTokenIsAcceptedUntilItExpires(t *testing.T) {
	t.Parallel()
	expires := time.Now().Add(time.Hour).Truncate(time.Second)
	h := newScimHarness(t, withPreviousScimToken(expires))
	h.toWallClock()
	current, previous := h.scim(t, scimToken), h.scim(t, scimPreviousToken)

	if r := previous.do(http.MethodGet, "/ServiceProviderConfig", nil); r.status != http.StatusOK {
		t.Fatalf("previous token: status %d, want 200", r.status)
	}
	h.advance(expires.Sub(h.now()) - time.Second)
	if r := previous.do(http.MethodGet, "/ServiceProviderConfig", nil); r.status != http.StatusOK {
		t.Errorf("previous token a second before its expiry: status %d, want 200", r.status)
	}
	h.advance(time.Second)
	wantScimError(t, "the previous token at its expiry", previous.do(http.MethodGet, "/ServiceProviderConfig", nil), http.StatusUnauthorized, "invalidValue", scimUnauthorizedText)
	h.advance(time.Hour)
	wantScimError(t, "the previous token after its expiry", previous.do(http.MethodGet, "/Users", nil), http.StatusUnauthorized, "invalidValue", scimUnauthorizedText)
	if r := current.do(http.MethodGet, "/ServiceProviderConfig", nil); r.status != http.StatusOK {
		t.Errorf("current token: status %d, want 200", r.status)
	}
}

// New: without a valid token the ingress limit is per client address (the
// harness sends each client's own X-Forwarded-For): 120 requests a minute,
// then the SCIM 429 with no Retry-After, as .NET answered.
func TestScim_WithoutAValidTokenTheRateLimitIsPerClientAddress(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	a, b := h.scim(t, "wrong-token"), h.scim(t, "wrong-token")

	for i := range 120 {
		if r := a.do(http.MethodGet, "/ServiceProviderConfig", nil); r.status != http.StatusUnauthorized {
			t.Fatalf("request %d: status %d, want 401", i+1, r.status)
		}
	}
	r := a.do(http.MethodGet, "/ServiceProviderConfig", nil)
	wantScimError(t, "the 121st request", r, http.StatusTooManyRequests, "tooMany", "SCIM request rate limit exceeded.")
	if r.header("Retry-After") != "" {
		t.Errorf("Retry-After = %q, want none", r.header("Retry-After"))
	}
	wantScimError(t, "another address", b.do(http.MethodGet, "/ServiceProviderConfig", nil), http.StatusUnauthorized, "invalidValue", scimUnauthorizedText)
	sameAddress := &scimClient{client: a.client, token: scimToken}
	if r := sameAddress.do(http.MethodGet, "/ServiceProviderConfig", nil); r.status != http.StatusOK {
		t.Errorf("the valid token from the limited address: status %d, want 200 (a valid token is counted by the token)", r.status)
	}
	h.advance(time.Minute)
	wantScimError(t, "the next window", a.do(http.MethodGet, "/ServiceProviderConfig", nil), http.StatusUnauthorized, "invalidValue", scimUnauthorizedText)
}

// New: with a valid token the ingress limit is per token, whatever the
// address, and a limited request is no heartbeat.
func TestScim_WithAValidTokenTheRateLimitIsPerToken(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	owner, _ := h.bootstrapOwner(t)
	a := h.scim(t, scimToken)

	for i := range 120 {
		if r := a.do(http.MethodGet, "/ServiceProviderConfig", nil); r.status != http.StatusOK {
			t.Fatalf("request %d: status %d, want 200", i+1, r.status)
		}
	}
	last := h.now()
	h.advance(time.Second)
	wantScimError(t, "the 121st request", a.do(http.MethodPost, "/Users", userResource("limited", nil)), http.StatusTooManyRequests, "tooMany", "SCIM request rate limit exceeded.")
	wantScimError(t, "the token from another address", h.scim(t, scimToken).do(http.MethodGet, "/Users", nil), http.StatusTooManyRequests, "tooMany", "SCIM request rate limit exceeded.")
	wantScimError(t, "a wrong token", h.scim(t, "wrong-token").do(http.MethodGet, "/Users", nil), http.StatusUnauthorized, "invalidValue", scimUnauthorizedText)
	if n := h.count(t, `SELECT count(*) FROM identity.scim_user_mappings`); n != 0 {
		t.Errorf("the limited create made %d Users", n)
	}
	if at := scimHeartbeat(t, owner); at == nil || !at.Equal(last) {
		t.Errorf("heartbeat at %v, want the last admitted request's %s", at, last)
	}
	h.advance(time.Minute)
	if r := a.do(http.MethodGet, "/ServiceProviderConfig", nil); r.status != http.StatusOK {
		t.Errorf("the next window: status %d, want 200", r.status)
	}
}

// expectContinueClient is a client that sends Expect: 100-continue and
// waits for the server before sending a body, so a body the server refuses
// by its announced length is never sent. It bypasses contract validation.
func (h *harness) expectContinueClient(t testing.TB) *http.Client {
	t.Helper()
	tr := h.srv.Client().Transport.(*http.Transport).Clone()
	tr.ExpectContinueTimeout = 10 * time.Second
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr}
}

// sendRaw sends body to path with the client address ip, the token and
// Expect: 100-continue; chunked hides the body's length.
func (h *harness) sendRaw(t testing.TB, c *http.Client, ip, path string, body []byte, chunked bool) *resp {
	t.Helper()
	var reader io.Reader = bytes.NewReader(body)
	if chunked {
		reader = io.MultiReader(reader)
	}
	req, err := http.NewRequest(http.MethodPost, h.url+scimPath+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/scim+json")
	req.Header.Set("Authorization", "Bearer "+scimToken)
	req.Header.Set("Expect", "100-continue")
	req.Header.Set("X-Forwarded-For", ip)
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("POST %s: read body: %v", path, err)
	}
	return &resp{t: t, status: res.StatusCode, body: b, headers: res.Header}
}

// New: a body must be application/scim+json (415) of at most 256 KiB
// (413), announced or streamed, and one JSON document of at most 64 levels
// (400 invalidSyntax), as .NET's Body helper required; each only once the
// token is valid, since .NET authenticated first.
func TestScim_BodiesMustBeScimJSONWithinTheSizeLimit(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	c := h.scim(t, scimToken)
	body, _ := json.Marshal(userResource("media", nil))

	for _, contentType := range []string{"application/json", "text/plain", ""} {
		// Off-contract by design: the contract documents only application/scim+json bodies.
		r := c.do(http.MethodPost, "/Users", nil, rawBody(contentType, body), skipContract("a body in another media type"))
		wantScimError(t, "Content-Type "+contentType, r, http.StatusUnsupportedMediaType, "invalidSyntax", "SCIM request bodies must use application/scim+json.")
	}
	for _, path := range []string{"/Users/" + uuid.NewString(), "/Groups/" + uuid.NewString()} {
		// Off-contract by design: as above.
		r := c.do(http.MethodPatch, path, nil, rawBody("application/json", []byte(`{}`)), skipContract("a body in another media type"))
		wantScimError(t, "PATCH "+path, r, http.StatusUnsupportedMediaType, "invalidSyntax", "SCIM request bodies must use application/scim+json.")
	}
	// Off-contract by design: as above; the token is checked first.
	r := h.scim(t, "wrong-token").do(http.MethodPost, "/Users", nil, rawBody("text/plain", body), skipContract("a body in another media type"))
	wantScimError(t, "a wrong token and media type", r, http.StatusUnauthorized, "invalidValue", scimUnauthorizedText)

	if r := c.do(http.MethodPost, "/Users", nil, rawBody("application/scim+json; charset=utf-8", body)); r.status != http.StatusCreated {
		t.Errorf("a charset parameter: status %d %s, want 201", r.status, r.body)
	}
	upper, _ := json.Marshal(userResource("upper", nil))
	// Off-contract by design: the contract spells the media type in lower case.
	if r := c.do(http.MethodPost, "/Users", nil, rawBody("APPLICATION/SCIM+JSON", upper), skipContract("the media type in upper case")); r.status != http.StatusCreated {
		t.Errorf("an upper-case media type: status %d %s, want 201", r.status, r.body)
	}

	// Exactly 256 KiB is read; its display name is then too long.
	sized := func(n int) []byte {
		resource := userResource("sized", map[string]any{"displayName": ""})
		b, _ := json.Marshal(resource)
		resource["displayName"] = strings.Repeat("x", n-len(b))
		b, _ = json.Marshal(resource)
		if len(b) != n {
			t.Fatalf("sized body is %d bytes, want %d", len(b), n)
		}
		return b
	}
	r = c.do(http.MethodPost, "/Users", nil, rawBody("application/scim+json", sized(256*1024)))
	wantScimError(t, "exactly 256 KiB", r, http.StatusBadRequest, "invalidValue", "displayName is invalid.")
	expect := h.expectContinueClient(t)
	for _, chunked := range []bool{false, true} {
		r := h.sendRaw(t, expect, "198.19.0."+strconv.Itoa(len(fmt.Sprint(chunked))), "/Users", sized(256*1024+1), chunked)
		wantScimError(t, fmt.Sprintf("a byte over (chunked %t)", chunked), r, http.StatusRequestEntityTooLarge, "tooLarge", "The SCIM request body exceeds the 256 KiB limit.")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.scim_user_mappings WHERE user_name = 'sized'`); n != 0 {
		t.Errorf("%d oversized Users made", n)
	}

	for name, bad := range map[string]string{
		"truncated":      `{"userName":`,
		"two documents":  `{} {}`,
		"65 levels deep": strings.Repeat("[", 65) + strings.Repeat("]", 65),
		"not UTF-8":      "{\"userName\":\"\xff\"}",
	} {
		// Off-contract by design: the body is not a valid document.
		r := c.do(http.MethodPost, "/Users", nil, rawBody("application/scim+json", []byte(bad)), skipContract("a malformed body"))
		wantScimError(t, name, r, http.StatusBadRequest, "invalidSyntax", "The request body is not valid JSON.")
	}
	// Off-contract by design: 64 levels is a document .NET read, and not a User.
	r = c.do(http.MethodPost, "/Users", nil, rawBody("application/scim+json", []byte(strings.Repeat("[", 64)+strings.Repeat("]", 64))), skipContract("an array body"))
	wantScimError(t, "64 levels deep", r, http.StatusBadRequest, "invalidSyntax", "A SCIM resource must be a JSON object.")
	bom, _ := json.Marshal(userResource("bom", nil))
	// Off-contract by design: a byte order mark precedes the document.
	if r := c.do(http.MethodPost, "/Users", nil, rawBody("application/scim+json", append([]byte("\xef\xbb\xbf"), bom...)), skipContract("a byte order mark")); r.status != http.StatusCreated {
		t.Errorf("a byte order mark: status %d %s, want 201", r.status, r.body)
	}
	// Off-contract by design: a GET with a body; .NET read no body for one.
	if r := c.do(http.MethodGet, "/ServiceProviderConfig", nil, rawBody("text/plain", []byte("ignored")), skipContract("a body on a GET")); r.status != http.StatusOK {
		t.Errorf("a GET with a body: status %d, want 200", r.status)
	}
}

// New: input .NET's handlers read themselves answers in the SCIM shape,
// with .NET's scimType and detail, never identity's invalid_request: a
// malformed or mistyped body, and a query or header the generated binder
// would refuse or read differently.
func TestScim_MalformedInputAnswersInTheScimShape(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	c := h.scim(t, scimToken)
	user, etag := createUser(t, c, userResource("shape", nil))

	for _, tc := range []struct {
		body, scimType, detail string
	}{
		{`{"userName":`, "invalidSyntax", "The request body is not valid JSON."},
		{`{"userName":"x","active":"yes"}`, "invalidValue", "active must be boolean."},
		{`{"userName":5}`, "invalidValue", "userName is required."},
		{`{"userName":"x","emails":{"value":"x@example.test"}}`, "invalidValue", "emails must be an array."},
		{`["x"]`, "invalidSyntax", "A SCIM resource must be a JSON object."},
	} {
		// Off-contract by design: a malformed or mistyped body.
		r := c.do(http.MethodPost, "/Users", nil, rawBody("application/scim+json", []byte(tc.body)), skipContract("a mistyped body"))
		wantScimError(t, tc.body, r, http.StatusBadRequest, tc.scimType, tc.detail)
	}
	// Off-contract by design: Operations must be an array.
	r := c.do(http.MethodPatch, "/Users/"+user.ID, nil, rawBody("application/scim+json", []byte(`{"Operations":{"op":"add"}}`)), skipContract("a mistyped PatchOp"))
	wantScimError(t, "a mistyped PatchOp", r, http.StatusBadRequest, "invalidSyntax", "PatchOp Operations must be a non-empty array.")

	// Off-contract by design: startIndex is not an integer.
	r = c.do(http.MethodGet, "/Users?startIndex=abc", nil, skipContract("a non-integer startIndex"))
	wantScimError(t, "startIndex=abc", r, http.StatusBadRequest, "invalidValue", "startIndex and count are invalid.")
	// Off-contract by design: a padded integer, which .NET counted as the default.
	if list := scimOf[scimList[scimUser]](t, "a padded startIndex", c.do(http.MethodGet, "/Users?startIndex=%202&count=%201", nil, skipContract("a padded integer")), http.StatusOK); list.StartIndex != 1 || list.ItemsPerPage != 1 {
		t.Errorf("a padded startIndex and count: %+v, want the defaults", list)
	}
	// Off-contract by design: the parameters in another case, which .NET read.
	if list := scimOf[scimList[scimUser]](t, "parameters in another case", c.do(http.MethodGet, "/Users?StartIndex=2&FILTER="+url.QueryEscape(`userName eq "shape"`), nil, skipContract("query keys in another case")), http.StatusOK); list.StartIndex != 2 || list.TotalResults != 1 {
		t.Errorf("parameters in another case: %+v", list)
	}
	// Off-contract by design: If-Match on two lines, which .NET read as one list.
	r = c.do(http.MethodPatch, "/Users/"+user.ID, patchOps(patchOp("replace", "displayName", "Shaped")), addHeader("If-Match", `"other"`), addHeader("If-Match", `"`+etag+`"`), skipContract("If-Match on two lines"))
	if r.status != http.StatusOK {
		t.Errorf("If-Match on two lines: status %d %s, want 200", r.status, r.body)
	}
}

// New: listing Users with .NET's filter and paging, in resource id order.
func TestScim_UsersListWithTheFilterAndPaging(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	c := h.scim(t, scimToken)
	alice, _ := createUser(t, c, userResource("alice", nil))
	bob, _ := createUser(t, c, userResource("bob", nil))
	carol, _ := createUser(t, c, userResource("carol", nil))
	ordered := []string{alice.ID, bob.ID, carol.ID}
	slices.Sort(ordered)

	list := func(query string) scimList[scimUser] {
		t.Helper()
		return scimOf[scimList[scimUser]](t, query, c.do(http.MethodGet, "/Users?"+query, nil), http.StatusOK)
	}
	filter := func(f string) string { return "filter=" + url.QueryEscape(f) }
	ids := func(l scimList[scimUser]) []string {
		var out []string
		for _, u := range l.Resources {
			out = append(out, u.ID)
		}
		return out
	}

	if l := list(""); l.TotalResults != 3 || l.StartIndex != 1 || l.ItemsPerPage != 3 || !slices.Equal(ids(l), ordered) {
		t.Errorf("all: %+v, want %v", l, ordered)
	}
	for f, want := range map[string][]string{
		`userName eq "bob"`:                        {bob.ID},
		`USERNAME Eq "bob"`:                        {bob.ID},
		`userName eq "Bob"`:                        nil,
		`externalId eq "` + carol.ExternalID + `"`: {carol.ID},
		`userName eq "bob" and externalId eq "` + carol.ExternalID + `"`:                             nil,
		`userName eq "alice" and nickName eq "ignored" and externalId eq "` + alice.ExternalID + `"`: {alice.ID},
	} {
		if l := list(filter(f)); !slices.Equal(ids(l), want) || l.TotalResults != len(want) {
			t.Errorf("%s: %v (total %d), want %v", f, ids(l), l.TotalResults, want)
		}
	}
	for f, detail := range map[string]string{
		`title eq "x"`:       "The filter field or value is invalid.",
		`displayName eq "x"`: "The filter field or value is invalid.",
		`userName co "a"`:    `Only field eq "value" filters joined by and are supported.`,
		`userName eq "` + strings.Repeat("a", 500) + `"`: "The filter is too long.",
	} {
		wantScimError(t, f, c.do(http.MethodGet, "/Users?"+filter(f), nil), http.StatusBadRequest, "invalidFilter", detail)
	}

	if l := list("count=2"); l.TotalResults != 3 || l.ItemsPerPage != 2 || !slices.Equal(ids(l), ordered[:2]) {
		t.Errorf("count=2: %+v", l)
	}
	if l := list("startIndex=3&count=2"); l.StartIndex != 3 || !slices.Equal(ids(l), ordered[2:]) {
		t.Errorf("startIndex=3: %+v", l)
	}
	if l := list("count=0"); l.TotalResults != 3 || l.ItemsPerPage != 0 || l.Resources == nil || len(l.Resources) != 0 {
		t.Errorf("count=0: %+v", l)
	}
	if l := list("startIndex=9"); l.TotalResults != 3 || l.ItemsPerPage != 0 {
		t.Errorf("startIndex=9: %+v", l)
	}
	if l := list("startIndex=-1"); l.StartIndex != 1 || l.ItemsPerPage != 3 {
		t.Errorf("startIndex=-1 counts as 1, as .NET read it: %+v", l)
	}
	for _, q := range []string{"count=101", "startIndex=0"} {
		wantScimError(t, q, c.do(http.MethodGet, "/Users?"+q, nil), http.StatusBadRequest, "invalidValue", "startIndex and count are invalid.")
	}
}

// New: a User's creation and replacement follow .NET's rules: a confirmed,
// enabled account with no role, group or password; the display name
// derived; an email required and unique; the userName and externalId
// unique among the mappings; externalId immutable; and every refusal with
// .NET's detail.
func TestScim_UsersAreCreatedAndReplacedByDotNetsRules(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	h.bootstrapOwner(t)
	c := h.scim(t, scimToken)

	r := c.do(http.MethodPost, "/Users", userResource("ann", map[string]any{
		"externalId": "{6F9619FF-8B86-D011-B42D-00CF4FC964FF}",
		"name":       map[string]any{"givenName": " Ann ", "familyName": "Lee"},
	}))
	ann := scimOf[scimUser](t, "create ann", r, http.StatusCreated)
	if ann.DisplayName != "Ann Lee" || ann.ExternalID != "6f9619ff-8b86-d011-b42d-00cf4fc964ff" || !ann.Active || ann.UserName != "ann" ||
		ann.Name == nil || *ann.Name.GivenName != "Ann" || *ann.Name.FamilyName != "Lee" ||
		len(ann.Emails) != 1 || ann.Emails[0].Value != "ann@example.test" || ann.Emails[0].Type != "work" || !ann.Emails[0].Primary ||
		r.header("Location") != scimPath+"/Users/"+ann.ID || ann.Meta.Location != scimPath+"/Users/"+ann.ID || ann.Meta.ResourceType != "User" ||
		!ann.Meta.Created.Equal(h.now()) || !ann.Meta.LastModified.Equal(h.now()) {
		t.Errorf("created %+v, Location %q", ann, r.header("Location"))
	}
	annID := h.userOf(t, ann.ID)
	var confirmed, disabled bool
	var password *string
	if err := h.pool.QueryRow(context.Background(), `SELECT email_confirmed, is_disabled, password_hash FROM identity.users WHERE id = $1`, annID).Scan(&confirmed, &disabled, &password); err != nil {
		t.Fatal(err)
	}
	if !confirmed || disabled || password != nil || h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1`, annID) != 0 {
		t.Errorf("the account: confirmed %t disabled %t password %v roles %d", confirmed, disabled, password != nil, h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1`, annID))
	}
	if named := scimOf[scimUser](t, "create bare", c.do(http.MethodPost, "/Users", userResource("bare", nil)), http.StatusCreated); named.DisplayName != "bare" || named.Name != nil {
		t.Errorf("no display name or name: %+v", named)
	}

	for _, tc := range []struct {
		name     string
		resource map[string]any
		status   int
		scimType string
		detail   string
	}{
		{"no email", userResource("noemail", map[string]any{"emails": []any{}}), 400, "invalidValue", "InvalidEmail"},
		{"another account's email", userResource("dup", map[string]any{"emails": []map[string]any{{"value": ownerEmail, "type": "work"}}}), 400, "invalidValue", "DuplicateEmail"},
		{"an invalid email", userResource("bad", map[string]any{"emails": []map[string]any{{"value": "not an email", "type": "work"}}}), 400, "invalidValue", "The work email is invalid."},
		{"a taken userName", userResource("ann", nil), 409, "uniqueness", "userName is already used by this connection."},
		{"a taken externalId", userResource("ann2", map[string]any{"externalId": "6F9619FF8B86D011B42D00CF4FC964FF"}), 409, "uniqueness", "externalId is already used by this connection."},
		{"a long userName", userResource(strings.Repeat("u", 513), nil), 400, "invalidValue", "userName and externalId must be non-empty safe values."},
		{"a long display name", userResource("long", map[string]any{"displayName": strings.Repeat("d", 201)}), 400, "invalidValue", "displayName is invalid."},
		{"a blank name part", userResource("blank", map[string]any{"name": map[string]any{"givenName": " "}}), 400, "invalidValue", "name values are invalid."},
		{"another schema", userResource("schema", map[string]any{"schemas": []string{scimGroupSchema}}), 400, "invalidValue", "The resource schema is not supported."},
	} {
		wantScimError(t, tc.name, c.do(http.MethodPost, "/Users", tc.resource), tc.status, tc.scimType, tc.detail)
	}
	if long := scimOf[scimUser](t, "a 512-character externalId", c.do(http.MethodPost, "/Users", userResource("longext", map[string]any{"externalId": strings.Repeat("e", 512)})), http.StatusCreated); len(long.ExternalID) != 512 {
		t.Errorf("externalId of %d characters", len(long.ExternalID))
	}

	for _, id := range []string{uuid.NewString(), strings.ToUpper(ann.ID), "not-an-id"} {
		wantScimError(t, "GET "+id, c.do(http.MethodGet, "/Users/"+id, nil), http.StatusNotFound, "", scimNotFoundText)
	}

	// PUT replaces: no email leaves the account with a reserved address no
	// response shows.
	r = c.do(http.MethodPut, "/Users/"+ann.ID, map[string]any{"schemas": []string{scimUserSchema}, "userName": "ann.lee", "displayName": "Ann Replaced"})
	replaced := scimOf[scimUser](t, "PUT", r, http.StatusOK)
	if replaced.UserName != "ann.lee" || replaced.DisplayName != "Ann Replaced" || replaced.ExternalID != ann.ExternalID || len(replaced.Emails) != 0 ||
		replaced.Meta.Version == ann.Meta.Version || etagOf(r) != replaced.Meta.Version || replaced.Name == nil {
		t.Errorf("replaced %+v", replaced)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND email = 'scim-' || $2 || '@sso.invalid'`, annID, ann.ID); n != 1 {
		t.Error("the account without an email has no reserved address")
	}
	wantScimError(t, "PUT another externalId", c.do(http.MethodPut, "/Users/"+ann.ID, map[string]any{"userName": "ann.lee", "externalId": "other"}), http.StatusConflict, "mutability", scimImmutableText)
	wantScimError(t, "PUT a taken userName", c.do(http.MethodPut, "/Users/"+ann.ID, map[string]any{"userName": "bare"}), http.StatusConflict, "uniqueness", "userName is already used by this connection.")
	wantScimError(t, "PUT an unknown User", c.do(http.MethodPut, "/Users/"+uuid.NewString(), map[string]any{"userName": "x"}), http.StatusNotFound, "", scimNotFoundText)

	// PATCH: Entra's pathless object, then the externalId rules.
	r = c.do(http.MethodPatch, "/Users/"+ann.ID, patchOps(patchOp("Replace", "", map[string]any{
		"displayName": "Ann Patched",
		"name":        map[string]any{"givenName": "Annie"},
		"emails":      []map[string]any{{"type": "work", "value": "annie@example.test", "primary": true}},
	})))
	if p := scimOf[scimUser](t, "a pathless PATCH", r, http.StatusOK); p.DisplayName != "Ann Patched" || *p.Name.GivenName != "Annie" || *p.Name.FamilyName != "Lee" ||
		len(p.Emails) != 1 || p.Emails[0].Value != "annie@example.test" || !p.Active {
		t.Errorf("patched %+v", p)
	}
	upper := strings.ToUpper(ann.ExternalID)
	if r := c.do(http.MethodPatch, "/Users/"+ann.ID, patchOps(patchOp("replace", "externalId", upper))); r.status != http.StatusOK {
		t.Errorf("PATCH the externalId in another form: status %d %s, want 200", r.status, r.body)
	}
	wantScimError(t, "PATCH another externalId", c.do(http.MethodPatch, "/Users/"+ann.ID, patchOps(patchOp("replace", "externalId", "other"))), http.StatusConflict, "mutability", scimImmutableText)
	wantScimError(t, "PATCH another externalId second", c.do(http.MethodPatch, "/Users/"+ann.ID,
		patchOps(patchOp("replace", "externalId", ann.ExternalID), patchOp("replace", "externalId", "other"))), http.StatusConflict, "mutability", scimImmutableText)
	wantScimError(t, "PATCH remove userName", c.do(http.MethodPatch, "/Users/"+ann.ID, patchOps(patchOp("remove", "userName", nil))), http.StatusBadRequest, "mutability", "userName cannot be removed.")
	wantScimError(t, "PATCH a taken userName", c.do(http.MethodPatch, "/Users/"+ann.ID, patchOps(patchOp("replace", "userName", "bare"))), http.StatusConflict, "uniqueness", scimUserConflictText)
	wantScimError(t, "PATCH another account's email", c.do(http.MethodPatch, "/Users/"+ann.ID, patchOps(patchOp("replace", "emails.value", ownerEmail))), http.StatusConflict, "uniqueness", scimUserConflictText)
	if got := scimOf[scimUser](t, "GET after the refusals", c.do(http.MethodGet, "/Users/"+ann.ID, nil), http.StatusOK); got.ExternalID != ann.ExternalID || got.UserName != "ann.lee" || got.Emails[0].Value != "annie@example.test" {
		t.Errorf("a refusal changed the User: %+v", got)
	}
	for _, action := range []string{"scim.user.replaced", "scim.user.patched"} {
		if events := h.auditEvents(t, action); len(events) < 1 || events[0].TargetUser == nil || *events[0].TargetUser != annID {
			t.Errorf("%s: %d rows", action, len(events))
		}
	}
}

// New: two creates racing for one externalId both pass validation, which
// runs before the transaction as .NET's did, and the second then replaces
// the User the first made, as CreateUserAsync's replace branch did
// (SV/ScimProtocolService.cs:156-164). A gate holding the SCIM lock queues
// both.
func TestScim_ACreateRacingAnotherForItsExternalIdReplacesTheUser(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	ctx := context.Background()
	gate, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Rollback(ctx) }()
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(identity.ScimLockKey)); err != nil {
		t.Fatal(err)
	}

	externalID := uuid.NewString()
	results := make([]*resp, 2)
	var wg sync.WaitGroup
	finished := make(chan struct{})
	for i := range results {
		c := h.scim(t, scimToken)
		wg.Go(func() {
			name := fmt.Sprintf("race-%d", i)
			results[i] = c.do(http.MethodPost, "/Users", userResource(name, map[string]any{"externalId": externalID}))
		})
	}
	go func() { wg.Wait(); close(finished) }()
	awaitLockWaiters(t, h, 2, finished)
	if err := gate.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	<-finished

	var users []scimUser
	for _, r := range results {
		users = append(users, scimOf[scimUser](t, "a racing create", r, http.StatusCreated))
	}
	names := []string{users[0].UserName, users[1].UserName}
	slices.Sort(names)
	if users[0].ID != users[1].ID || !slices.Equal(names, []string{"race-0", "race-1"}) || users[0].Meta.Version == users[1].Meta.Version {
		t.Errorf("the creates answered %+v and %+v", users[0], users[1])
	}
	if n := h.count(t, `SELECT count(*) FROM identity.scim_user_mappings`); n != 1 {
		t.Errorf("%d mappings, want 1", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != 1 {
		t.Errorf("%d accounts, want 1", n)
	}
	if n := len(h.auditEvents(t, "scim.user.created")); n != 2 {
		t.Errorf("%d scim.user.created rows, want 2, as .NET audited the replacement as a creation", n)
	}
}

// New: of concurrent PATCHes with one ETag exactly one succeeds; the others
// answer 412, and one change is stored and audited.
func TestScim_ConcurrentPatchesWithOneETagSucceedOnce(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	user, etag := createUser(t, h.scim(t, scimToken), userResource("concurrent", nil))

	const n = 5
	results := make([]*resp, n)
	var wg sync.WaitGroup
	for i := range n {
		c := h.scim(t, scimToken)
		wg.Go(func() {
			results[i] = c.do(http.MethodPatch, "/Users/"+user.ID, patchOps(patchOp("replace", "displayName", fmt.Sprintf("Writer %d", i))), ifMatch(etag))
		})
	}
	wg.Wait()
	ok := 0
	for _, r := range results {
		if r.status == http.StatusOK {
			ok++
			continue
		}
		wantScimError(t, "a losing PATCH", r, http.StatusPreconditionFailed, "invalidVers", scimStaleText)
	}
	if ok != 1 {
		t.Errorf("%d PATCHes succeeded, want 1", ok)
	}
	if got := len(h.auditEvents(t, "scim.user.patched")); got != 1 {
		t.Errorf("%d scim.user.patched rows, want 1", got)
	}
}

// New: every ETag rule: a User's ETag is random and changes with every
// write, and If-Match guards PUT, PATCH and DELETE; a Group's is its
// version, which the Owner's role mapping changes too, and If-Match, a
// PatchOp's meta.version and X-SCIM-Meta-Version guard it.
func TestScim_ETagsGuardEveryWrite(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	owner, _ := h.bootstrapOwner(t)
	c := h.scim(t, scimToken)

	user, etag := createUser(t, c, userResource("etag", nil))
	if r := c.do(http.MethodGet, "/Users/"+user.ID, nil); etagOf(r) != etag || len(etag) != 32 || strings.Trim(etag, "0123456789abcdef") != "" {
		t.Errorf("GET ETag %q, created %q", etagOf(r), etag)
	}
	patch := patchOps(patchOp("replace", "displayName", "E"))
	for _, stale := range []reqOpt{header("If-Match", `"stale"`), header("If-Match", `W/"`+etag+`"`)} {
		wantScimError(t, "a stale PATCH", c.do(http.MethodPatch, "/Users/"+user.ID, patch, stale), http.StatusPreconditionFailed, "invalidVers", scimStaleText)
		wantScimError(t, "a stale PUT", c.do(http.MethodPut, "/Users/"+user.ID, map[string]any{"userName": "etag"}, stale), http.StatusPreconditionFailed, "invalidVers", scimStaleText)
		wantScimError(t, "a stale DELETE", c.do(http.MethodDelete, "/Users/"+user.ID, nil, stale), http.StatusPreconditionFailed, "invalidVers", scimStaleText)
	}
	seen := map[string]bool{etag: true}
	for _, match := range []func(etag string) reqOpt{
		func(string) reqOpt { return header("If-Match", "*") },
		func(etag string) reqOpt { return header("If-Match", `"other", "`+etag+`"`) },
	} {
		r := c.do(http.MethodPatch, "/Users/"+user.ID, patch, match(etag))
		if r.status != http.StatusOK || seen[etagOf(r)] {
			t.Errorf("If-Match: status %d ETag %q, want 200 and a new ETag", r.status, etagOf(r))
		}
		etag = etagOf(r)
		seen[etag] = true
	}
	r := c.do(http.MethodPut, "/Users/"+user.ID, map[string]any{"userName": "etag", "emails": []map[string]any{{"type": "work", "value": "etag@example.test"}}}, ifMatch(etag))
	if r.status != http.StatusOK || seen[etagOf(r)] {
		t.Errorf("PUT: status %d ETag %q", r.status, etagOf(r))
	}
	etag = etagOf(r)
	if r := c.do(http.MethodDelete, "/Users/"+user.ID, nil, ifMatch(etag)); r.status != http.StatusNoContent || etagOf(r) == etag || etagOf(r) == "" {
		t.Errorf("DELETE: status %d ETag %q", r.status, etagOf(r))
	}

	group, gtag := createScimGroup(t, c, "Etag group")
	if gtag != group.Meta.Version {
		t.Errorf("group ETag %q, meta.version %q", gtag, group.Meta.Version)
	}
	role := h.insertRole(t, "Directory readers")
	if r := owner.do(http.MethodPost, mappingPath(uuid.MustParse(group.ID), role), stamped(gtag)); r.status != http.StatusOK {
		t.Fatalf("map a role: %d %s", r.status, r.body)
	}
	wantScimError(t, "a PATCH with the ETag before the Owner's mapping", c.do(http.MethodPatch, "/Groups/"+group.ID, patchOps(patchOp("replace", "active", true)), ifMatch(gtag)),
		http.StatusPreconditionFailed, "invalidVers", scimStaleText)
	gtag = etagOf(c.do(http.MethodGet, "/Groups/"+group.ID, nil))
	stalePatch := patchOps(patchOp("replace", "active", true))
	stalePatch["meta"] = map[string]any{"version": "stale"}
	wantScimError(t, "a stale meta.version", c.do(http.MethodPatch, "/Groups/"+group.ID, stalePatch, ifMatch(gtag)), http.StatusPreconditionFailed, "invalidVers", scimStaleVersionText)
	wantScimError(t, "a stale group If-Match", c.do(http.MethodDelete, "/Groups/"+group.ID, nil, header("If-Match", `"stale"`)), http.StatusPreconditionFailed, "invalidVers", scimStaleText)
	wantScimError(t, "a stale X-SCIM-Meta-Version", c.do(http.MethodDelete, "/Groups/"+group.ID, nil, header("X-SCIM-Meta-Version", "stale")), http.StatusPreconditionFailed, "invalidVers", scimStaleVersionText)
	current := patchOps(patchOp("replace", "displayName", "Etag group renamed"))
	current["meta"] = map[string]any{"version": gtag}
	r = c.do(http.MethodPatch, "/Groups/"+group.ID, current, ifMatch(gtag))
	if r.status != http.StatusOK || etagOf(r) == gtag {
		t.Fatalf("PATCH with the current version: %d %s", r.status, r.body)
	}
	gtag = etagOf(r)
	if r := c.do(http.MethodDelete, "/Groups/"+group.ID, nil, ifMatch(gtag), header("X-SCIM-Meta-Version", gtag)); r.status != http.StatusNoContent || etagOf(r) == gtag {
		t.Errorf("DELETE the group: status %d ETag %q", r.status, etagOf(r))
	}
}

// New: an Owner is immune to SCIM, the installation's break-glass account:
// every change is 400 mutability, and even a directory-deactivated Owner
// keeps their session.
func TestScim_OwnersAreImmune(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	c := h.scim(t, scimToken)
	user, etag := createUser(t, c, userResource("promoted", nil))
	if r := c.do(http.MethodPatch, "/Users/"+user.ID, patchOps(patchOp("replace", "active", false)), ifMatch(etag)); r.status != http.StatusOK {
		t.Fatalf("deactivate: %d %s", r.status, r.body)
	}
	etag = etagOf(c.do(http.MethodGet, "/Users/"+user.ID, nil))
	userID := h.userOf(t, user.ID)
	h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, userID, identity.RoleOwnerID)

	if r := h.signIn(t, userID, false).do(http.MethodGet, sessionPath, nil); r.status != http.StatusOK {
		t.Errorf("a directory-deactivated Owner's session: status %d, want 200", r.status)
	}
	wantScimError(t, "PATCH an Owner", c.do(http.MethodPatch, "/Users/"+user.ID, patchOps(patchOp("replace", "displayName", "Changed"))), http.StatusBadRequest, "mutability", scimProtectedText)
	wantScimError(t, "PUT an Owner", c.do(http.MethodPut, "/Users/"+user.ID, map[string]any{"userName": "promoted"}), http.StatusBadRequest, "mutability", scimProtectedText)
	wantScimError(t, "DELETE an Owner", c.do(http.MethodDelete, "/Users/"+user.ID, nil), http.StatusBadRequest, "mutability", scimProtectedText)
	wantScimError(t, "a stale If-Match comes first", c.do(http.MethodDelete, "/Users/"+user.ID, nil, header("If-Match", `"stale"`)), http.StatusPreconditionFailed, "invalidVers", scimStaleText)
	if r := c.do(http.MethodGet, "/Users/"+user.ID, nil); r.status != http.StatusOK || etagOf(r) != etag {
		t.Errorf("GET the Owner: status %d ETag %q, want 200 and the ETag unchanged", r.status, etagOf(r))
	}
	if n := len(h.auditEvents(t, "scim.user.deleted")) + len(h.auditEvents(t, "scim.user.replaced")); n != 0 {
		t.Errorf("%d audit rows for refused changes", n)
	}
}

// New: a user the directory deactivates, by PATCH, PUT or DELETE, is
// refused at their very next request, while SCIM is configured, although
// no session is revoked: .NET rotated no security stamp for a SCIM change,
// and session validation reads the mapping on every request. Reactivated,
// the same session is admitted again.
func TestScim_ADeactivatedUsersSessionEndsAtTheNextRequest(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	c := h.scim(t, scimToken)

	for _, how := range []string{"PATCH", "PUT", "DELETE"} {
		user, _ := createUser(t, c, userResource("session-"+strings.ToLower(how), nil))
		userID := h.userOf(t, user.ID)
		token := h.session(t, userID, false)
		browser := func() *client {
			b := h.client(t)
			b.http.Jar.SetCookies(h.base, []*http.Cookie{{Name: identity.SessionCookieName, Value: token, Path: "/"}})
			return b
		}
		if r := browser().do(http.MethodGet, sessionPath, nil); r.status != http.StatusOK {
			t.Fatalf("%s: the session before: %d", how, r.status)
		}
		var r *resp
		switch how {
		case "PATCH":
			r = c.do(http.MethodPatch, "/Users/"+user.ID, patchOps(patchOp("replace", "active", false)))
		case "PUT":
			r = c.do(http.MethodPut, "/Users/"+user.ID, map[string]any{"userName": user.UserName, "active": false})
		default:
			r = c.do(http.MethodDelete, "/Users/"+user.ID, nil)
		}
		if r.status >= 300 {
			t.Fatalf("%s: %d %s", how, r.status, r.body)
		}
		if r := browser().do(http.MethodGet, sessionPath, nil); r.status != http.StatusUnauthorized || r.code() != "unauthenticated" {
			t.Errorf("%s: the next request: status %d code %q, want 401 unauthenticated", how, r.status, r.code())
		}
		if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1 AND revoked_at IS NOT NULL`, userID); n != 0 {
			t.Errorf("%s revoked %d sessions; .NET rotated no stamp", how, n)
		}
		if r := c.do(http.MethodPatch, "/Users/"+user.ID, patchOps(patchOp("replace", "active", true))); r.status != http.StatusOK {
			t.Fatalf("%s: reactivate: %d %s", how, r.status, r.body)
		}
		if r := browser().do(http.MethodGet, sessionPath, nil); r.status != http.StatusOK {
			t.Errorf("%s: the session after reactivation: status %d, want 200", how, r.status)
		}
	}
}

// New: SCIM groups are SCIM-sourced, refused on a duplicate or a foreign
// member, capped at 100 members, and deleted softly; the Owner's group API
// still refuses their content.
func TestScim_GroupsAreScimSourcedAndDeletedSoftly(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	owner, _ := h.bootstrapOwner(t)
	c := h.scim(t, scimToken)
	u1, _ := createUser(t, c, userResource("g1", nil))
	u2, _ := createUser(t, c, userResource("g2", nil))

	r := c.do(http.MethodPost, "/Groups", map[string]any{"displayName": " Directory Staff ", "members": memberValues(u1.ID, u1.ID)})
	group := scimOf[scimGroup](t, "create", r, http.StatusCreated)
	gid := uuid.MustParse(group.ID)
	if group.DisplayName != "Directory Staff" || group.ExternalID == nil || uuid.Validate(*group.ExternalID) != nil || !group.Active ||
		!slices.Equal(memberIDs(group), []string{u1.ID}) || r.header("Location") != scimPath+"/Groups/"+group.ID || group.Meta.ResourceType != "Group" {
		t.Errorf("created %+v, Location %q", group, r.header("Location"))
	}
	if m := (*group.Members)[0]; m.Type != "User" || m.Ref != scimPath+"/Users/"+u1.ID {
		t.Errorf("member %+v", m)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.access_groups WHERE id = $1 AND source = 'scim'`, gid); n != 1 {
		t.Error("the group is not SCIM-sourced")
	}
	if got := h.membershipRow(t, gid, h.userOf(t, u1.ID)); got != "scim/true/none" {
		t.Errorf("u1's row = %s", got)
	}

	createGroup(t, owner, "Local Staff")
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = u2.ID
	}
	for _, tc := range []struct {
		name     string
		body     map[string]any
		status   int
		scimType string
		detail   string
	}{
		{"a local group's name", map[string]any{"displayName": "Local Staff"}, 409, "uniqueness", scimGroupConflictText},
		{"a SCIM group's name", map[string]any{"displayName": "Directory Staff"}, 409, "uniqueness", scimGroupConflictText},
		{"a SCIM group's externalId", map[string]any{"displayName": "Other", "externalId": *group.ExternalID}, 409, "uniqueness", scimGroupConflictText},
		{"a Group member", map[string]any{"displayName": "Other", "members": []map[string]any{{"value": u1.ID, "type": "Group"}}}, 400, "invalidValue", "Only User members are supported."},
		{"an unknown member", map[string]any{"displayName": "Other", "members": memberValues(uuid.NewString())}, 400, "invalidValue", "Every group member must be a known SCIM User from this connection."},
		{"101 members", map[string]any{"displayName": "Other", "members": memberValues(tooMany...)}, 400, "tooMany", "A group membership mutation exceeds the safe member cap."},
		{"a long name", map[string]any{"displayName": strings.Repeat("n", 201)}, 400, "invalidValue", "displayName is invalid."},
		{"a long externalId", map[string]any{"displayName": "Other", "externalId": strings.Repeat("e", 257)}, 400, "invalidValue", "externalId is invalid."},
		{"no name", map[string]any{"displayName": " "}, 400, "invalidValue", "displayName is required."},
	} {
		wantScimError(t, tc.name, c.do(http.MethodPost, "/Groups", tc.body), tc.status, tc.scimType, tc.detail)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.access_groups`); n != 2 {
		t.Errorf("%d groups, want 2: a refusal created one", n)
	}

	if r := owner.do(http.MethodPut, memberPath(gid, h.userOf(t, u2.ID)), stamped(group.Meta.Version)); r.status != http.StatusConflict || r.code() != "scim_group_managed" {
		t.Errorf("the Owner's member PUT on a SCIM group: status %d code %q, want 409 scim_group_managed", r.status, r.code())
	}

	// A User's DELETE makes its SCIM memberships absent.
	if r := c.do(http.MethodDelete, "/Users/"+u1.ID, nil); r.status != http.StatusNoContent {
		t.Fatalf("delete u1: %d", r.status)
	}
	if got := h.membershipRow(t, gid, h.userOf(t, u1.ID)); got != "scim/false/none" {
		t.Errorf("after the User's DELETE u1's row = %s", got)
	}

	// The Group's DELETE deactivates it and makes its SCIM members absent.
	added := c.do(http.MethodPatch, "/Groups/"+group.ID, patchOps(patchOp("add", "members", memberValues(u2.ID))))
	if !slices.Equal(memberIDs(scimOf[scimGroup](t, "add u2", added, http.StatusOK)), []string{u2.ID}) {
		t.Errorf("after adding u2: %s", added.body)
	}
	if r := c.do(http.MethodDelete, "/Groups/"+group.ID, nil); r.status != http.StatusNoContent {
		t.Fatalf("delete the group: %d %s", r.status, r.body)
	}
	after := scimOf[scimGroup](t, "GET the deleted group", c.do(http.MethodGet, "/Groups/"+group.ID, nil), http.StatusOK)
	if after.Active || len(memberIDs(after)) != 0 || h.membershipRow(t, gid, h.userOf(t, u2.ID)) != "scim/false/none" {
		t.Errorf("after DELETE: %+v, u2's row %s", after, h.membershipRow(t, gid, h.userOf(t, u2.ID)))
	}
	for _, id := range []string{strings.ToUpper(group.ID), "{" + group.ID + "}", strings.ReplaceAll(group.ID, "-", "")} {
		if r := c.do(http.MethodGet, "/Groups/"+url.PathEscape(id), nil); r.status != http.StatusOK {
			t.Errorf("GET /Groups/%s: status %d, want 200: .NET parsed a group id as any GUID form", id, r.status)
		}
	}
	for _, id := range []string{uuid.NewString(), "not-a-guid"} {
		wantScimError(t, "GET group "+id, c.do(http.MethodGet, "/Groups/"+id, nil), http.StatusNotFound, "", scimNotFoundText)
	}
	for _, action := range []string{"scim.group.created", "scim.group.patched", "scim.group.deleted"} {
		if events := h.auditEvents(t, action); len(events) != 1 || events[0].TargetUser != nil || events[0].Actor != nil {
			t.Errorf("%s: %+v", action, events)
		}
	}
}

// New: SCIM writes only is_upstream_present, so a local override always
// wins: a local force_non_member survives a SCIM add, a local
// force_member survives a SCIM remove, and a SCIM-sourced row keeps its
// override while its upstream presence changes.
func TestScim_LocalOverridesWinOverScimMembership(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	owner, _ := h.bootstrapOwner(t)
	c := h.scim(t, scimToken)
	var users []scimUser
	var ids []uuid.UUID
	for i := range 4 {
		u, _ := createUser(t, c, userResource(fmt.Sprintf("o%d", i), nil))
		users = append(users, u)
		ids = append(ids, h.userOf(t, u.ID))
	}
	group, _ := createScimGroup(t, c, "Overrides", users[0].ID)
	gid := uuid.MustParse(group.ID)
	forceMember, forceNonMember := "force_member", "force_non_member"
	h.seedMembership(t, gid, ids[1], "local", false, &forceNonMember)
	h.seedMembership(t, gid, ids[2], "local", false, &forceMember)
	h.seedMembership(t, gid, ids[3], "scim", true, &forceNonMember)

	effective := func() []uuid.UUID {
		t.Helper()
		var groups []accessGroup
		r := owner.do(http.MethodGet, groupsPath, nil)
		r.json(&groups)
		for _, g := range groups {
			if g.ID == gid {
				return g.MemberUserIDs
			}
		}
		t.Fatalf("the Owner's list lacks the group: %s", r.body)
		return nil
	}
	patch := func(what string, ops ...map[string]any) scimGroup {
		t.Helper()
		return scimOf[scimGroup](t, what, c.do(http.MethodPatch, "/Groups/"+group.ID, patchOps(ops...)), http.StatusOK)
	}

	g := patch("add the excluded and the forced", patchOp("add", "members", memberValues(users[1].ID, users[3].ID)))
	if got := h.membershipRow(t, gid, ids[1]); got != "local/false/force_non_member" {
		t.Errorf("a local force_non_member after a SCIM add: %s", got)
	}
	if got := h.membershipRow(t, gid, ids[3]); got != "scim/true/force_non_member" {
		t.Errorf("a SCIM row's override after a SCIM add: %s", got)
	}
	if !slices.Equal(memberIDs(g), []string{users[0].ID}) {
		t.Errorf("SCIM lists %v, want only the upstream member", memberIDs(g))
	}
	if got, want := effective(), byUUID(ids[0], ids[2]); !slices.Equal(got, want) {
		t.Errorf("effective members %v, want %v", got, want)
	}

	patch("remove the forced", patchOp("remove", `members[value eq "`+users[2].ID+`"]`, nil))
	if got := h.membershipRow(t, gid, ids[2]); got != "local/false/force_member" {
		t.Errorf("a local force_member after a SCIM remove: %s", got)
	}
	h.exec(t, `UPDATE identity.access_group_memberships SET membership_override = 'force_member' WHERE group_id = $1 AND user_id = $2`, gid, ids[3])
	patch("remove the SCIM-forced", patchOp("remove", `members[value eq "`+users[3].ID+`"]`, nil))
	if got := h.membershipRow(t, gid, ids[3]); got != "scim/false/force_member" {
		t.Errorf("a SCIM row's override after a SCIM remove: %s", got)
	}
	if got, want := effective(), byUUID(ids[0], ids[2], ids[3]); !slices.Equal(got, want) {
		t.Errorf("effective members %v, want %v", got, want)
	}

	g = patch("replace", patchOp("replace", "members", memberValues(users[3].ID)))
	if got := h.membershipRow(t, gid, ids[0]); got != "scim/false/none" {
		t.Errorf("a member replace left out: %s", got)
	}
	if !slices.Equal(memberIDs(g), []string{users[3].ID}) || h.membershipRow(t, gid, ids[1]) != "local/false/force_non_member" || h.membershipRow(t, gid, ids[2]) != "local/false/force_member" {
		t.Errorf("after replace: SCIM lists %v; rows %s, %s", memberIDs(g), h.membershipRow(t, gid, ids[1]), h.membershipRow(t, gid, ids[2]))
	}
	g = patch("a pathless remove", patchOp("remove", "", map[string]any{"members": memberValues(users[0].ID)}))
	if len(memberIDs(g)) != 1 || h.membershipRow(t, gid, ids[3]) != "scim/false/force_member" {
		t.Errorf("after a pathless remove: SCIM lists %v, row %s", memberIDs(g), h.membershipRow(t, gid, ids[3]))
	}
	if got, want := effective(), byUUID(ids[2], ids[3]); !slices.Equal(got, want) {
		t.Errorf("effective members %v, want %v: the overrides survive every SCIM change", got, want)
	}
}

// New: listing Groups: only SCIM groups, in id order, with the filter on
// displayName and externalId, paging, and members unless excluded.
func TestScim_GroupsListWithTheFilterPagingAndExcludedMembers(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	owner, _ := h.bootstrapOwner(t)
	c := h.scim(t, scimToken)
	member, _ := createUser(t, c, userResource("lister", nil))
	var ordered []string
	byName := map[string]scimGroup{}
	for _, name := range []string{"Alpha", "Beta", "Gamma"} {
		g, _ := createScimGroup(t, c, name, member.ID)
		ordered = append(ordered, g.ID)
		byName[name] = g
	}
	slices.Sort(ordered)
	createGroup(t, owner, "Local only")

	list := func(query string) scimList[scimGroup] {
		t.Helper()
		return scimOf[scimList[scimGroup]](t, query, c.do(http.MethodGet, "/Groups?"+query, nil), http.StatusOK)
	}
	ids := func(l scimList[scimGroup]) []string {
		var out []string
		for _, g := range l.Resources {
			out = append(out, g.ID)
		}
		return out
	}
	if l := list(""); l.TotalResults != 3 || !slices.Equal(ids(l), ordered) || l.Resources[0].Members == nil {
		t.Errorf("all: %+v", l)
	}
	if l := list("filter=" + url.QueryEscape(`displayName eq "Beta"`)); !slices.Equal(ids(l), []string{byName["Beta"].ID}) {
		t.Errorf("displayName: %v", ids(l))
	}
	if l := list("filter=" + url.QueryEscape(`externalId eq "`+*byName["Gamma"].ExternalID+`"`)); !slices.Equal(ids(l), []string{byName["Gamma"].ID}) {
		t.Errorf("externalId: %v", ids(l))
	}
	if l := list("filter=" + url.QueryEscape(`displayName eq "Local only"`)); l.TotalResults != 0 {
		t.Errorf("a local group listed: %v", ids(l))
	}
	wantScimError(t, "a Users attribute", c.do(http.MethodGet, "/Groups?filter="+url.QueryEscape(`userName eq "x"`), nil), http.StatusBadRequest, "invalidFilter", "The filter field or value is invalid.")
	if l := list("startIndex=2&count=1"); !slices.Equal(ids(l), ordered[1:2]) || l.TotalResults != 3 || l.ItemsPerPage != 1 {
		t.Errorf("paging: %+v", l)
	}
	if l := list("excludedAttributes=members"); l.Resources[0].Members != nil || !bytes.Contains(c.do(http.MethodGet, "/Groups?excludedAttributes=members", nil).body, []byte(`"displayName"`)) {
		t.Errorf("excludedAttributes=members still lists members: %+v", l.Resources[0])
	}
	one := c.do(http.MethodGet, "/Groups/"+byName["Alpha"].ID+"?excludedAttributes=members", nil)
	if g := scimOf[scimGroup](t, "GET without members", one, http.StatusOK); g.Members != nil || etagOf(one) != g.Meta.Version {
		t.Errorf("GET without members: %+v", g)
	}
	if g := scimOf[scimGroup](t, "GET with members", c.do(http.MethodGet, "/Groups/"+byName["Alpha"].ID, nil), http.StatusOK); !slices.Equal(memberIDs(g), []string{member.ID}) {
		t.Errorf("GET with members: %v", memberIDs(g))
	}
	wantScimError(t, "count=101", c.do(http.MethodGet, "/Groups?count=101", nil), http.StatusBadRequest, "invalidValue", "startIndex and count are invalid.")
}

// New (the Task 17 heartbeat, end to end): an authenticated SCIM request,
// and only one, records the heartbeat the Owner's system status reports,
// even one whose body is then refused, as .NET recorded it before the body
// was read; the token never appears in the status.
func TestScim_TheHeartbeatRecordsAuthenticatedRequestsOnly(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	owner, _ := h.bootstrapOwner(t)

	h.scim(t, "wrong-token").do(http.MethodGet, "/Schemas", nil)
	if at := scimHeartbeat(t, owner); at != nil {
		t.Fatalf("a refused request was recorded at %s", at)
	}
	h.advance(time.Minute)
	c := h.scim(t, scimToken)
	c.do(http.MethodGet, "/Schemas", nil)
	if at := scimHeartbeat(t, owner); at == nil || !at.Equal(h.now()) {
		t.Errorf("heartbeat at %v, want %s", at, h.now())
	}
	h.advance(time.Minute)
	// Off-contract by design: a body in another media type, refused after authentication.
	c.do(http.MethodPost, "/Users", nil, rawBody("text/plain", []byte("x")), skipContract("a body in another media type"))
	if at := scimHeartbeat(t, owner); at == nil || !at.Equal(h.now()) {
		t.Errorf("heartbeat at %v, want %s", at, h.now())
	}
	r := owner.do(http.MethodGet, systemStatusPath, nil)
	var status ownerSystemStatusBody
	r.json(&status)
	if !status.StaticScimEnabled || strings.Contains(string(r.body), scimToken) {
		t.Errorf("system status %s", r.body)
	}
}
