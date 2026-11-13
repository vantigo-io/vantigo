package identity_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// The shared shop-floor stations (manufacturing phase 2 M129–M143): pairing,
// the station cookie, operators switching in with a PIN or a badge, the
// backoff and the alert, and how Access answers an operator's request.

const (
	stationsPath = "/api/v1/identity/stations"
	stationPath  = "/api/v1/identity/station"
	operatorPIN  = "482915"
)

// shopFloorKeys are what a station's operator may hold at most.
var shopFloorKeys = []string{"manufacturing:access", "manufacturing:shop-floor"}

type stationPairing struct {
	Station struct {
		ID              uuid.UUID `json:"id"`
		State           string    `json:"state"`
		AlertedAt       *string   `json:"alertedAt"`
		AllowedNetworks []string  `json:"allowedNetworks"`
		ActiveOperators []struct {
			UserID uuid.UUID `json:"userId"`
		} `json:"activeOperators"`
	} `json:"station"`
	PairingCode string `json:"pairingCode"`
}

type operatorSignIn struct {
	SessionID uuid.UUID `json:"sessionId"`
	Token     string    `json:"token"`
	Operator  struct {
		UserID   uuid.UUID `json:"userId"`
		Initials string    `json:"initials"`
	} `json:"operator"`
}

// operatorRole is a custom role holding keys, written directly as modtest does.
func (h *harness) operatorRole(t testing.TB, keys ...string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	name := "station-role-" + id.String()[:8]
	h.exec(t, `INSERT INTO identity.roles (id, name, normalized_name, display_name, version, created_at, updated_at)
	           VALUES ($1, $2, upper($2), $2, $3, $4, $4)`, id, name, uuid.New(), h.now())
	for _, key := range keys {
		h.exec(t, `INSERT INTO identity.role_permissions (role_id, permission_key) VALUES ($1, $2)`, id, key)
	}
	return id
}

// operator seeds a person holding keys who set operatorPIN through their own
// browser, and returns their id and that browser.
func (h *harness) operator(t testing.TB, name string, keys ...string) (uuid.UUID, *client) {
	t.Helper()
	id := h.seedUser(t, name+"@example.test", userPassword, h.operatorRole(t, keys...))
	c := h.signIn(t, id, false)
	if r := c.do(http.MethodPut, "/api/v1/identity/account/pin", map[string]any{"pin": operatorPIN}); r.status != http.StatusNoContent {
		t.Fatalf("set PIN: %d %s", r.status, r.body)
	}
	return id, c
}

// registerStation registers a station as owner and answers its pairing.
func registerStation(t testing.TB, owner *client, body map[string]any) stationPairing {
	t.Helper()
	r := owner.do(http.MethodPost, stationsPath, body)
	if r.status != http.StatusOK {
		t.Fatalf("register station: %d %s", r.status, r.body)
	}
	var p stationPairing
	r.json(&p)
	return p
}

// pairStation pairs a new browser with code and returns it.
func (h *harness) pairStation(t testing.TB, code string) *client {
	t.Helper()
	c := h.client(t)
	if r := c.do(http.MethodPost, stationPath+"/pair", map[string]any{"code": code}); r.status != http.StatusOK {
		t.Fatalf("pair: %d %s", r.status, r.body)
	}
	return c
}

// switchIn signs userID in at the station with pin.
func switchIn(t testing.TB, station *client, userID uuid.UUID, pin string) *resp {
	t.Helper()
	return station.do(http.MethodPost, stationPath+"/operators", map[string]any{"userId": userID, "pin": pin})
}

func mustSwitchIn(t testing.TB, station *client, userID uuid.UUID) operatorSignIn {
	t.Helper()
	r := switchIn(t, station, userID, operatorPIN)
	if r.status != http.StatusOK {
		t.Fatalf("switch in: %d %s", r.status, r.body)
	}
	var s operatorSignIn
	r.json(&s)
	return s
}

// stationRequest is a request from station's browser with token as the
// operator's Authorization, for Access.Check.
func (h *harness) stationRequest(station *client, token string, extra ...*http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/manufacturing/meta", nil)
	r.AddCookie(&http.Cookie{Name: identity.StationCookieName, Value: station.cookie(identity.StationCookieName)})
	for _, c := range extra {
		r.AddCookie(c)
	}
	if token != "" {
		r.Header.Set("Authorization", "Station "+token)
	}
	return r
}

var allowedRule = contracts.Rule{Kind: contracts.RulePermission, Names: []string{"manufacturing:access"}, Station: contracts.StationAllowed}

func stationCode(err error, w *httptest.ResponseRecorder) string {
	return fmt.Sprintf("%v %d %s", err, w.Code, w.Body.String())
}

// rejected is what Reject writes for err under rule.
func (h *harness) rejected(r *http.Request, rule contracts.Rule, err error) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.access.Reject(w, r, rule, err)
	return w
}

func TestStation_RegisterPairAndTheCookie(t *testing.T) {
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)

	p := registerStation(t, owner, map[string]any{"name": "Press line 1"})
	if p.Station.State != "pairing" || len(p.PairingCode) != 8 {
		t.Fatalf("registered = %+v, want pairing with an eight-character code", p)
	}
	if got := h.auditEvents(t, "station_registered"); len(got) != 1 || got[0].Actor == nil || *got[0].Actor != ownerID {
		t.Fatalf("station_registered audit = %+v", got)
	}

	// The code typed in lower case with a separator pairs; the cookie is the
	// station's: HttpOnly, SameSite=Strict, 90 days, Secure as APP_URL is.
	typed := strings.ToLower(p.PairingCode[:4] + "-" + p.PairingCode[4:])
	c := h.client(t)
	r := c.do(http.MethodPost, stationPath+"/pair", map[string]any{"code": typed})
	if r.status != http.StatusOK {
		t.Fatalf("pair: %d %s", r.status, r.body)
	}
	ck := r.setCookie(identity.StationCookieName)
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.MaxAge != 90*24*3600 || ck.Path != "/" || ck.Secure {
		t.Fatalf("station cookie = %+v, want HttpOnly, SameSite=Strict, 90 days, path /, not Secure on http", ck)
	}
	if len(h.auditEvents(t, "station_paired")) != 1 {
		t.Fatal("station_paired was not audited")
	}
	if r := c.do(http.MethodGet, stationPath, nil); r.status != http.StatusOK {
		t.Fatalf("GET station: %d %s", r.status, r.body)
	}

	// The code is spent: a second browser cannot use it.
	if r := h.client(t).do(http.MethodPost, stationPath+"/pair", map[string]any{"code": p.PairingCode}); r.status != http.StatusBadRequest || r.code() != "pairing_code_invalid" {
		t.Fatalf("second use: %d %s, want 400 pairing_code_invalid", r.status, r.body)
	}
	// A code past its 15 minutes is refused.
	late := registerStation(t, owner, map[string]any{"name": "Late"})
	h.advance(16 * time.Minute)
	if r := h.client(t).do(http.MethodPost, stationPath+"/pair", map[string]any{"code": late.PairingCode}); r.code() != "pairing_code_invalid" {
		t.Fatalf("expired code: %d %s", r.status, r.body)
	}
	// Garbage is refused before any lookup.
	if r := h.client(t).do(http.MethodPost, stationPath+"/pair", map[string]any{"code": "UUUUUUUU"}); r.code() != "pairing_code_invalid" {
		t.Fatalf("garbage code: %d %s", r.status, r.body)
	}
	// No station cookie is no station.
	if r := h.client(t).do(http.MethodGet, stationPath, nil); r.status != http.StatusUnauthorized || r.code() != "station_unknown" {
		t.Fatalf("no cookie: %d %s", r.status, r.body)
	}
}

func TestStation_PairingSignsTheSessionOut(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	p := registerStation(t, owner, map[string]any{"name": "Tablet"})
	personID, person := h.operator(t, "person", shopFloorKeys...)

	r := person.do(http.MethodPost, stationPath+"/pair", map[string]any{"code": p.PairingCode})
	if r.status != http.StatusOK {
		t.Fatalf("pair: %d %s", r.status, r.body)
	}
	if ck := r.setCookie(identity.SessionCookieName); ck == nil || ck.MaxAge >= 0 {
		t.Fatalf("session cookie = %+v, want cleared", ck)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1 AND revoked_at IS NULL`, personID); n != 0 {
		t.Fatalf("%d live sessions after pairing, want 0", n)
	}
	if got := h.auditEvents(t, "station_session_signed_out"); len(got) != 1 || got[0].TargetUser == nil || *got[0].TargetUser != personID {
		t.Fatalf("station_session_signed_out = %+v", got)
	}
	if r := person.do(http.MethodGet, stationPath, nil); r.status != http.StatusOK {
		t.Fatalf("the paired browser: %d %s", r.status, r.body)
	}
}

func TestStation_ABrowserIsAStationOrAPersons(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	p := registerStation(t, owner, map[string]any{"name": "Tablet"})
	opID, _ := h.operator(t, "op", shopFloorKeys...)
	station := h.pairStation(t, p.PairingCode)
	op := mustSwitchIn(t, station, opID)

	// A person signs in on the tablet: station requests refuse.
	session := h.session(t, opID, false)
	station.http.Jar.SetCookies(h.base, []*http.Cookie{{Name: identity.SessionCookieName, Value: session, Path: "/"}})
	if r := station.do(http.MethodGet, stationPath, nil); r.status != http.StatusConflict || r.code() != "station_has_session" {
		t.Fatalf("station read with a session: %d %s", r.status, r.body)
	}
	req := h.stationRequest(station, op.Token, &http.Cookie{Name: identity.SessionCookieName, Value: session})
	_, err := h.access.Check(req, allowedRule)
	w := h.rejected(req, allowedRule, err)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "station_has_session") {
		t.Fatalf("operator request with a session: %s", stationCode(err, w))
	}
	// The refusal leaves the person's session cookie alone.
	if strings.Contains(w.Header().Get("Set-Cookie"), identity.SessionCookieName) {
		t.Fatalf("Reject cleared the session cookie: %q", w.Header().Get("Set-Cookie"))
	}
}

func TestStation_TokenExpiresAfter90DaysAndRePairs(t *testing.T) {
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	p := registerStation(t, owner, map[string]any{"name": "Tablet"})
	station := h.pairStation(t, p.PairingCode)

	h.advance(89 * 24 * time.Hour)
	if r := station.do(http.MethodGet, stationPath, nil); r.status != http.StatusOK {
		t.Fatalf("day 89: %d %s", r.status, r.body)
	}
	h.advance(2 * 24 * time.Hour)
	if r := station.do(http.MethodGet, stationPath, nil); r.status != http.StatusUnauthorized || r.code() != "station_unknown" {
		t.Fatalf("day 91: %d %s", r.status, r.body)
	}
	owner = h.signIn(t, ownerID, false) // the Owner's own session has long ended
	var again stationPairing
	r := owner.do(http.MethodPost, stationsPath+"/"+p.Station.ID.String()+"/pairing", nil)
	if r.status != http.StatusOK {
		t.Fatalf("re-pair: %d %s", r.status, r.body)
	}
	r.json(&again)
	if len(h.auditEvents(t, "station_repaired")) != 1 {
		t.Fatal("station_repaired was not audited")
	}
	if r := station.do(http.MethodPost, stationPath+"/pair", map[string]any{"code": again.PairingCode}); r.status != http.StatusOK {
		t.Fatalf("pair again: %d %s", r.status, r.body)
	}
	if r := station.do(http.MethodGet, stationPath, nil); r.status != http.StatusOK {
		t.Fatalf("after re-pairing: %d %s", r.status, r.body)
	}
}

func TestStation_NetworkAllowlist(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	p := registerStation(t, owner, map[string]any{"name": "Tablet"})
	station := h.pairStation(t, p.PairingCode)
	path := stationsPath + "/" + p.Station.ID.String()

	if r := owner.do(http.MethodPut, path, map[string]any{"name": "Tablet", "allowedNetworks": []string{"nonsense"}}); r.status != http.StatusBadRequest {
		t.Fatalf("bad network: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPut, path, map[string]any{"name": "Tablet", "allowedNetworks": []string{"10.0.0.0/8"}}); r.status != http.StatusOK {
		t.Fatalf("set allowlist: %d %s", r.status, r.body)
	}
	if got := h.auditEvents(t, "station_networks_changed"); len(got) != 1 || !strings.Contains(got[0].After, "10.0.0.0/8") {
		t.Fatalf("station_networks_changed = %+v", got)
	}
	if r := station.do(http.MethodGet, stationPath, nil); r.status != http.StatusUnauthorized || r.code() != "station_unknown" {
		t.Fatalf("outside the allowlist: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPut, path, map[string]any{"name": "Tablet", "allowedNetworks": []string{station.ip}}); r.status != http.StatusOK {
		t.Fatalf("set allowlist: %d %s", r.status, r.body)
	}
	if r := station.do(http.MethodGet, stationPath, nil); r.status != http.StatusOK {
		t.Fatalf("inside the allowlist: %d %s", r.status, r.body)
	}
}

func TestStation_PINRulesAndStorage(t *testing.T) {
	h := newHarness(t)
	id := h.seedUser(t, "pin@example.test", userPassword, h.operatorRole(t, shopFloorKeys...))
	c := h.signIn(t, id, false)
	for _, pin := range []string{"111111", "123456", "654321", "890123"[0:0] + "345678"} {
		if r := c.do(http.MethodPut, "/api/v1/identity/account/pin", map[string]any{"pin": pin}); r.status != http.StatusBadRequest || r.code() != "pin_too_simple" {
			t.Errorf("PIN %s: %d %s, want 400 pin_too_simple", pin, r.status, r.body)
		}
	}
	for _, pin := range []string{"12345", "1234567", "12a456", ""} {
		if r := c.do(http.MethodPut, "/api/v1/identity/account/pin", map[string]any{"pin": pin}); r.status != http.StatusBadRequest || r.code() != "invalid_request" {
			t.Errorf("PIN %q: %d %s, want 400 invalid_request", pin, r.status, r.body)
		}
	}
	// Past ten minutes since signing in, the current password is needed.
	h.advance(11 * time.Minute)
	if r := c.do(http.MethodPut, "/api/v1/identity/account/pin", map[string]any{"pin": operatorPIN}); r.status != http.StatusForbidden || r.code() != "reauthentication_required" {
		t.Fatalf("stale session: %d %s", r.status, r.body)
	}
	if r := c.do(http.MethodPut, "/api/v1/identity/account/pin", map[string]any{"pin": operatorPIN, "currentPassword": "wrong"}); r.status != http.StatusUnauthorized || r.code() != "invalid_credentials" {
		t.Fatalf("wrong password: %d %s", r.status, r.body)
	}
	if r := c.do(http.MethodPut, "/api/v1/identity/account/pin", map[string]any{"pin": operatorPIN, "currentPassword": userPassword}); r.status != http.StatusNoContent {
		t.Fatalf("set PIN: %d %s", r.status, r.body)
	}
	var state struct {
		Set   bool    `json:"set"`
		SetAt *string `json:"setAt"`
	}
	c.do(http.MethodGet, "/api/v1/identity/account/pin", nil).json(&state)
	if !state.Set || state.SetAt == nil {
		t.Fatalf("PIN state = %+v", state)
	}

	// The stored hash is argon2id over the peppered PIN, never the PIN.
	var hash string
	if err := h.pool.QueryRow(context.Background(), `SELECT pin_hash FROM identity.user_pins WHERE user_id = $1`, id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("pin hash %q is not argon2id", hash)
	}
	if ok, _ := identity.VerifyPassword(hash, operatorPIN); ok {
		t.Fatal("the hash verifies the bare PIN: it is not peppered")
	}
	peppered, err := identity.PepperPIN(h.deps.Secrets, operatorPIN)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := identity.VerifyPassword(hash, peppered); !ok {
		t.Fatal("the hash does not verify the peppered PIN")
	}
	if len(h.auditEvents(t, "pin_set")) != 1 {
		t.Fatal("pin_set was not audited")
	}

	if r := c.do(http.MethodDelete, "/api/v1/identity/account/pin", nil); r.status != http.StatusNoContent {
		t.Fatalf("remove PIN: %d %s", r.status, r.body)
	}
	if len(h.auditEvents(t, "pin_disabled")) != 1 {
		t.Fatal("pin_disabled was not audited")
	}
}

func TestStation_PINShapes(t *testing.T) {
	for pin, want := range map[string]string{
		"482915": "", "111111": "pin_too_simple", "012345": "pin_too_simple", "987654": "pin_too_simple",
		"12345": "invalid_request", "abcdef": "invalid_request", "１２３４５６": "invalid_request", "135791": "",
	} {
		if got := identity.ValidatePIN(pin); got != want {
			t.Errorf("ValidatePIN(%q) = %q, want %q", pin, got, want)
		}
	}
	if code, ok := identity.NormalizePairingCode(" ab12-cdef "); !ok || code != "AB12CDEF" {
		t.Errorf("NormalizePairingCode = %q %v", code, ok)
	}
	if code, ok := identity.NormalizePairingCode("io0l-ABCD"); !ok || code != "1001ABCD" {
		t.Errorf("NormalizePairingCode reads I, O, L as Crockford does: %q %v", code, ok)
	}
	for failures, want := range map[int32]time.Duration{
		1: 0, 4: 0, 5: 30 * time.Second, 6: time.Minute, 7: 2 * time.Minute, 9: 8 * time.Minute, 10: 15 * time.Minute, 30: 15 * time.Minute,
	} {
		if got := identity.BackoffAfter(failures); got != want {
			t.Errorf("BackoffAfter(%d) = %v, want %v", failures, got, want)
		}
	}
}

func TestStation_OperatorsSwitchInAndOut(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	p := registerStation(t, owner, map[string]any{"name": "Tablet"})
	station := h.pairStation(t, p.PairingCode)
	opID, _ := h.operator(t, "ada lovelace", shopFloorKeys...)
	readerID, _ := h.operator(t, "reader", "manufacturing:access") // no shop floor
	h.seedUser(t, "nopin@example.test", userPassword, h.operatorRole(t, shopFloorKeys...))

	var list struct {
		Items []struct {
			UserID   uuid.UUID `json:"userId"`
			Initials string    `json:"initials"`
			HasPin   bool      `json:"hasPin"`
		} `json:"items"`
	}
	station.do(http.MethodGet, stationPath+"/operators", nil).json(&list)
	if len(list.Items) != 1 || list.Items[0].UserID != opID || !list.Items[0].HasPin || list.Items[0].Initials != "AL" {
		t.Fatalf("operators = %+v, want only the shop-floor holder with a PIN", list.Items)
	}

	// A person without the shop floor fails as any failure does.
	if r := switchIn(t, station, readerID, operatorPIN); r.status != http.StatusUnauthorized || r.code() != "operator_sign_in_failed" {
		t.Fatalf("reader: %d %s", r.status, r.body)
	}
	if r := station.do(http.MethodPost, stationPath+"/operators", map[string]any{"pin": operatorPIN}); r.status != http.StatusBadRequest {
		t.Fatalf("no person: %d %s", r.status, r.body)
	}

	op := mustSwitchIn(t, station, opID)
	if op.Operator.UserID != opID || op.Token == "" {
		t.Fatalf("sign-in = %+v", op)
	}
	if len(h.auditEvents(t, "operator_signed_in")) != 1 {
		t.Fatal("operator_signed_in was not audited")
	}
	// Signing in again replaces the session: one chip per person.
	again := mustSwitchIn(t, station, opID)
	var self struct {
		Operators []struct {
			SessionID uuid.UUID `json:"sessionId"`
		} `json:"operators"`
	}
	station.do(http.MethodGet, stationPath, nil).json(&self)
	if len(self.Operators) != 1 || self.Operators[0].SessionID != again.SessionID {
		t.Fatalf("operators = %+v, want the second session only", self.Operators)
	}
	if _, err := h.access.Check(h.stationRequest(station, op.Token), allowedRule); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("the replaced token: %v, want unauthenticated", err)
	}

	// Switch out with the station cookie alone; an unknown session is 404.
	if r := station.do(http.MethodPost, stationPath+"/operators/"+uuid.NewString()+"/end", nil); r.status != http.StatusNotFound || r.code() != "operator_session_unknown" {
		t.Fatalf("unknown session: %d %s", r.status, r.body)
	}
	if r := station.do(http.MethodPost, stationPath+"/operators/"+again.SessionID.String()+"/end", nil); r.status != http.StatusNoContent {
		t.Fatalf("switch out: %d %s", r.status, r.body)
	}
	if got := h.auditEvents(t, "operator_signed_out"); len(got) != 1 || !strings.Contains(got[0].After, "switched_out") {
		t.Fatalf("operator_signed_out = %+v", got)
	}
	if _, err := h.access.Check(h.stationRequest(station, again.Token), allowedRule); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("a switched-out token: %v", err)
	}
	// Another station's cookie cannot end this station's session.
	other := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "Other"}).PairingCode)
	third := mustSwitchIn(t, station, opID)
	if r := other.do(http.MethodPost, stationPath+"/operators/"+third.SessionID.String()+"/end", nil); r.status != http.StatusNotFound {
		t.Fatalf("another station's switch out: %d %s", r.status, r.body)
	}
}

func TestStation_TenOperatorsAtMost(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	station := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "Busy"}).PairingCode)
	role := h.operatorRole(t, shopFloorKeys...)
	var ids []uuid.UUID
	for i := range 11 {
		id := h.seedUser(t, fmt.Sprintf("op%d@example.test", i), userPassword, role)
		c := h.signIn(t, id, false)
		c.do(http.MethodPut, "/api/v1/identity/account/pin", map[string]any{"pin": operatorPIN})
		ids = append(ids, id)
	}
	for _, id := range ids[:10] {
		mustSwitchIn(t, station, id)
	}
	if r := switchIn(t, station, ids[10], operatorPIN); r.status != http.StatusConflict || r.code() != "too_many_operators" {
		t.Fatalf("eleventh: %d %s", r.status, r.body)
	}
}

func TestStation_CheckAnswersAnOperatorOnlyToWrap(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	station := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "Tablet"}).PairingCode)
	opID, _ := h.operator(t, "op", append(shopFloorKeys, "identity:manage")...)
	op := mustSwitchIn(t, station, opID)

	p, err := h.access.Check(h.stationRequest(station, op.Token), allowedRule)
	if err != nil {
		t.Fatalf("an allowed operation: %v", err)
	}
	stationID, ok := p.Station()
	if !ok || p.UserID != opID || p.SessionID != op.SessionID || p.MFAVerified || stationID == uuid.Nil {
		t.Fatalf("principal = %+v", p)
	}

	// Outside wrap — a handler's own Check, a platform endpoint — station
	// credentials are no caller at all.
	for _, rule := range []contracts.Rule{
		{Kind: contracts.RulePermission, Names: []string{"manufacturing:access"}},
		{Kind: contracts.RuleSession},
		{Kind: contracts.RuleAnonymous},
	} {
		if _, err := h.access.Check(h.stationRequest(station, op.Token), rule); !errors.Is(err, contracts.ErrUnauthenticated) || errors.Is(err, contracts.ErrForbidden) {
			t.Errorf("rule %v outside wrap: %v, want unauthenticated", rule, err)
		}
	}
	// An operation not open to stations is station_scope, whatever the
	// operator holds elsewhere.
	out := contracts.Rule{Kind: contracts.RulePermission, Names: []string{"identity:manage"}, Station: contracts.StationOutOfScope}
	req := h.stationRequest(station, op.Token)
	_, err = h.access.Check(req, out)
	if !errors.Is(err, contracts.ErrStationScope) {
		t.Fatalf("out of scope: %v", err)
	}
	if w := h.rejected(req, out, err); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "station_scope") {
		t.Fatalf("out of scope answered %d %s", w.Code, w.Body.String())
	}
	// The permissions are intersected: a key beyond the shop floor is
	// refused even on an allowed operation.
	wide := contracts.Rule{Kind: contracts.RulePermission, Names: []string{"identity:manage"}, Station: contracts.StationAllowed}
	if _, err := h.access.Check(h.stationRequest(station, op.Token), wide); !errors.Is(err, contracts.ErrForbidden) || errors.Is(err, contracts.ErrStationScope) {
		t.Fatalf("a key beyond the shop floor: %v, want forbidden", err)
	}
	// HasPermission inside a station handler asks with the same leave and the
	// same intersection.
	ctx := contracts.WithRequest(contracts.WithPrincipal(context.Background(), p), httptest.NewRecorder(), h.stationRequest(station, op.Token))
	if !contracts.HasPermission(ctx, h.access, "manufacturing:shop-floor") || contracts.HasPermission(ctx, h.access, "identity:manage") {
		t.Fatal("HasPermission inside a station handler does not intersect")
	}
	// A request without the principal in its context — any other handler's
	// own question — is refused.
	bare := contracts.WithRequest(context.Background(), httptest.NewRecorder(), h.stationRequest(station, op.Token))
	if contracts.HasPermission(bare, h.access, "manufacturing:access") {
		t.Fatal("HasPermission without the router's principal answered a station")
	}
}

func TestStation_AnOwnerAtAStationHoldsTheShopFloorOnly(t *testing.T) {
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	station := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "Tablet"}).PairingCode)
	if r := owner.do(http.MethodPut, "/api/v1/identity/account/pin", map[string]any{"pin": operatorPIN, "currentPassword": ownerPassword}); r.status != http.StatusNoContent {
		t.Fatalf("owner PIN: %d %s", r.status, r.body)
	}
	op := mustSwitchIn(t, station, ownerID)
	if _, err := h.access.Check(h.stationRequest(station, op.Token), allowedRule); err != nil {
		t.Fatalf("owner at a station on the shop floor: %v", err)
	}
	customers := contracts.Rule{Kind: contracts.RulePermission, Names: []string{"customers:view"}, Station: contracts.StationAllowed}
	if _, err := h.access.Check(h.stationRequest(station, op.Token), customers); !errors.Is(err, contracts.ErrForbidden) {
		t.Fatalf("owner at a station reading customers: %v, want forbidden", err)
	}
	policy := contracts.Rule{Kind: contracts.RulePolicy, Names: []string{"Owner"}, Station: contracts.StationAllowed}
	if _, err := h.access.Check(h.stationRequest(station, op.Token), policy); !errors.Is(err, contracts.ErrForbidden) {
		t.Fatalf("owner at a station under the Owner policy: %v, want forbidden", err)
	}
}

func TestStation_AStolenTokenAtAnotherStation(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	a := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "A"}).PairingCode)
	b := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "B"}).PairingCode)
	opID, _ := h.operator(t, "op", shopFloorKeys...)
	op := mustSwitchIn(t, a, opID)

	req := h.stationRequest(b, op.Token)
	_, err := h.access.Check(req, allowedRule)
	if w := h.rejected(req, allowedRule, err); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "operator_session_ended") {
		t.Fatalf("token at another station: %s", stationCode(err, w))
	}
	// The token still serves its own station.
	if _, err := h.access.Check(h.stationRequest(a, op.Token), allowedRule); err != nil {
		t.Fatalf("its own station: %v", err)
	}
	// A token with no station cookie at all is station_unknown.
	bare := httptest.NewRequest(http.MethodGet, "/", nil)
	bare.Header.Set("Authorization", "Station "+op.Token)
	_, err = h.access.Check(bare, allowedRule)
	if w := h.rejected(bare, allowedRule, err); !strings.Contains(w.Body.String(), "station_unknown") {
		t.Fatalf("no cookie: %s", stationCode(err, w))
	}
}

func TestStation_IdleAndAbsoluteExpiry(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	station := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "Tablet", "idleMinutes": 5}).PairingCode)
	opID, _ := h.operator(t, "op", shopFloorKeys...)

	op := mustSwitchIn(t, station, opID)
	h.advance(4 * time.Minute)
	if _, err := h.access.Check(h.stationRequest(station, op.Token), allowedRule); err != nil {
		t.Fatalf("after 4 idle minutes: %v", err)
	}
	h.advance(4 * time.Minute) // active 4 minutes ago: still within 5
	if _, err := h.access.Check(h.stationRequest(station, op.Token), allowedRule); err != nil {
		t.Fatalf("activity slid the window: %v", err)
	}
	h.advance(6 * time.Minute)
	if _, err := h.access.Check(h.stationRequest(station, op.Token), allowedRule); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("after 6 idle minutes: %v", err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.station_operator_sessions WHERE id = $1 AND end_reason = 'idle'`, op.SessionID); n != 1 {
		t.Fatal("the idle session was not ended as idle")
	}

	// A one-minute timeout slides with activity inside the minute.
	brisk := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "Brisk", "idleMinutes": 1}).PairingCode)
	quick := mustSwitchIn(t, brisk, opID)
	for range 4 {
		h.advance(50 * time.Second)
		if _, err := h.access.Check(h.stationRequest(brisk, quick.Token), allowedRule); err != nil {
			t.Fatalf("active every 50 seconds at a one-minute station: %v", err)
		}
	}

	// Twelve hours end a session however active it is.
	patient := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "Patient", "idleMinutes": 60}).PairingCode)
	station = patient
	op = mustSwitchIn(t, station, opID)
	for range 25 {
		h.advance(30 * time.Minute)
		if _, err := h.access.Check(h.stationRequest(station, op.Token), allowedRule); err != nil {
			break
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.station_operator_sessions WHERE id = $1 AND end_reason = 'expired'`, op.SessionID); n != 1 {
		t.Fatal("the 12-hour session was not ended as expired")
	}
}

func TestStation_RevokeTakesEffectAtOnce(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	p := registerStation(t, owner, map[string]any{"name": "Tablet"})
	station := h.pairStation(t, p.PairingCode)
	opID, _ := h.operator(t, "op", shopFloorKeys...)
	op := mustSwitchIn(t, station, opID)

	var list struct {
		Items []stationPairingStation `json:"items"`
	}
	owner.do(http.MethodGet, stationsPath, nil).json(&list)
	if len(list.Items) != 1 || list.Items[0].State != "paired" || len(list.Items[0].ActiveOperators) != 1 {
		t.Fatalf("stations = %+v", list.Items)
	}

	if r := owner.do(http.MethodPost, stationsPath+"/"+p.Station.ID.String()+"/revoke", nil); r.status != http.StatusOK {
		t.Fatalf("revoke: %d %s", r.status, r.body)
	}
	if len(h.auditEvents(t, "station_revoked")) != 1 {
		t.Fatal("station_revoked was not audited")
	}
	req := h.stationRequest(station, op.Token)
	_, err := h.access.Check(req, allowedRule)
	if w := h.rejected(req, allowedRule, err); !strings.Contains(w.Body.String(), "station_unknown") {
		t.Fatalf("after revoke: %s", stationCode(err, w))
	}
	if n := h.count(t, `SELECT count(*) FROM identity.station_operator_sessions WHERE station_id = $1 AND ended_at IS NULL`, p.Station.ID); n != 0 {
		t.Fatalf("%d operator sessions still open after revoke", n)
	}
	live, err := h.access.StationLive(context.Background(), p.Station.ID)
	if err != nil || live {
		t.Fatalf("StationLive after revoke = %v %v", live, err)
	}
}

type stationPairingStation struct {
	State           string  `json:"state"`
	AlertedAt       *string `json:"alertedAt"`
	ActiveOperators []struct {
		UserID uuid.UUID `json:"userId"`
	} `json:"activeOperators"`
}

func TestStation_BackoffPerStationAndPerson(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	a := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "A"}).PairingCode)
	b := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "B"}).PairingCode)
	opID, _ := h.operator(t, "op", shopFloorKeys...)

	nextAttempt := func() *time.Time {
		var at *time.Time
		_ = h.pool.QueryRow(context.Background(), `SELECT next_attempt_at FROM identity.operator_backoff WHERE user_id = $1`, opID).Scan(&at)
		return at
	}
	for i := 1; i <= 4; i++ {
		if r := switchIn(t, a, opID, "000001"); r.code() != "operator_sign_in_failed" {
			t.Fatalf("failure %d: %d %s", i, r.status, r.body)
		}
	}
	if at := nextAttempt(); at != nil {
		t.Fatalf("after four failures next attempt %v, want none", at)
	}
	switchIn(t, a, opID, "000001") // the fifth: 30 seconds
	if at := nextAttempt(); at == nil || !at.Equal(h.now().Add(30*time.Second)) {
		t.Fatalf("after five failures next attempt %v, want now + 30s", at)
	}
	// In backoff even the right PIN is refused, without lengthening it.
	if r := switchIn(t, a, opID, operatorPIN); r.status != http.StatusUnauthorized || r.code() != "operator_sign_in_failed" {
		t.Fatalf("the right PIN in backoff: %d %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT failures FROM identity.operator_backoff WHERE user_id = $1`, opID); n != 5 {
		t.Fatalf("failures %d after an attempt in backoff, want 5", n)
	}
	// The same person at another station is not slowed.
	if r := switchIn(t, b, opID, operatorPIN); r.status != http.StatusOK {
		t.Fatalf("another station: %d %s", r.status, r.body)
	}
	// The wait doubles.
	h.advance(31 * time.Second)
	switchIn(t, a, opID, "000001")
	if at := nextAttempt(); at == nil || !at.Equal(h.now().Add(time.Minute)) {
		t.Fatalf("after six failures next attempt %v, want now + 1m", at)
	}
	// A success after the wait clears the run.
	h.advance(61 * time.Second)
	if r := switchIn(t, a, opID, operatorPIN); r.status != http.StatusOK {
		t.Fatalf("after the wait: %d %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.operator_backoff WHERE user_id = $1`, opID); n != 0 {
		t.Fatal("a success left the backoff")
	}
	if got := h.auditEvents(t, "operator_sign_in_failed"); len(got) != 7 || strings.Contains(got[0].After, "000001") {
		t.Fatalf("operator_sign_in_failed audited %d times (want 7), or with the PIN", len(got))
	}
}

func TestStation_RateLimitAndTheAlert(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	p := registerStation(t, owner, map[string]any{"name": "Floor"})
	station := h.pairStation(t, p.PairingCode)

	// Ten failures in five minutes, for people who do not exist: the tenth
	// trips the station's limit, the eleventh is refused before verifying,
	// and the holders of identity:manage are mailed once.
	for i := range 10 {
		if r := switchIn(t, station, uuid.New(), "000001"); r.status != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d %s", i+1, r.status, r.body)
		}
	}
	if r := switchIn(t, station, uuid.New(), "000001"); r.status != http.StatusTooManyRequests || r.header("Retry-After") != "" {
		t.Fatalf("eleventh: %d %s (Retry-After %q)", r.status, r.body, r.header("Retry-After"))
	}
	if got := h.mailTo(ownerEmail); len(got) != 1 || !strings.Contains(got[0].Subject, "Floor") {
		t.Fatalf("alerts = %+v, want one about Floor", got)
	}
	if len(h.auditEvents(t, "operator_backoff")) != 1 {
		t.Fatal("operator_backoff was not audited")
	}
	var list struct {
		Items []stationPairingStation `json:"items"`
	}
	owner.do(http.MethodGet, stationsPath, nil).json(&list)
	if len(list.Items) != 1 || list.Items[0].AlertedAt == nil {
		t.Fatalf("the list does not mark the station: %+v", list.Items)
	}

	// The next window trips again within the hour: audited, not mailed.
	h.advance(6 * time.Minute)
	for range 10 {
		switchIn(t, station, uuid.New(), "000001")
	}
	if got := h.mailTo(ownerEmail); len(got) != 1 {
		t.Fatalf("%d alerts within the hour, want 1", len(got))
	}
	if len(h.auditEvents(t, "operator_backoff")) != 2 {
		t.Fatal("the second trip was not audited")
	}
	// An hour on, it mails again.
	h.advance(time.Hour)
	for range 10 {
		switchIn(t, station, uuid.New(), "000001")
	}
	if got := h.mailTo(ownerEmail); len(got) != 2 {
		t.Fatalf("%d alerts after an hour, want 2", len(got))
	}
}

func TestStation_BadgesAndTheAdministrator(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	opID, _ := h.operator(t, "op", shopFloorKeys...)
	otherID, _ := h.operator(t, "other", shopFloorKeys...)
	userPath := "/api/v1/identity/users/" + opID.String()

	if r := owner.do(http.MethodPut, userPath+"/badge", map[string]any{"badge": "ab"}); r.status != http.StatusBadRequest {
		t.Fatalf("short badge: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPut, userPath+"/badge", map[string]any{"badge": "0004211337"}); r.status != http.StatusNoContent {
		t.Fatalf("enrol: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPut, "/api/v1/identity/users/"+otherID.String()+"/badge", map[string]any{"badge": "0004211337"}); r.status != http.StatusConflict || r.code() != "badge_taken" {
		t.Fatalf("a taken badge: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPut, "/api/v1/identity/users/"+uuid.NewString()+"/badge", map[string]any{"badge": "0004211338"}); r.status != http.StatusNotFound {
		t.Fatalf("an unknown person: %d %s", r.status, r.body)
	}
	var creds struct {
		HasPin   bool `json:"hasPin"`
		HasBadge bool `json:"hasBadge"`
	}
	owner.do(http.MethodGet, userPath+"/operator-credentials", nil).json(&creds)
	if !creds.HasPin || !creds.HasBadge {
		t.Fatalf("credentials = %+v", creds)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_badges WHERE badge_hash = convert_to('0004211337', 'UTF8')`); n != 0 {
		t.Fatal("the badge is stored as typed")
	}

	// By default a badge needs its holder's PIN too.
	pinned := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "Pinned"}).PairingCode)
	if r := pinned.do(http.MethodPost, stationPath+"/operators", map[string]any{"badge": "0004211337"}); r.status != http.StatusBadRequest {
		t.Fatalf("badge alone at a PIN station: %d %s", r.status, r.body)
	}
	if r := pinned.do(http.MethodPost, stationPath+"/operators", map[string]any{"badge": "0004211337", "pin": "000001"}); r.code() != "operator_sign_in_failed" {
		t.Fatalf("badge with a wrong PIN: %d %s", r.status, r.body)
	}
	if r := pinned.do(http.MethodPost, stationPath+"/operators", map[string]any{"badge": "0004211337", "pin": operatorPIN}); r.status != http.StatusOK {
		t.Fatalf("badge with the PIN: %d %s", r.status, r.body)
	}
	// A card-only station takes the badge alone; an unknown card fails.
	cardOnly := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "Cards", "requirePinWithBadge": false}).PairingCode)
	if r := cardOnly.do(http.MethodPost, stationPath+"/operators", map[string]any{"badge": " 0004211337\n"}); r.status != http.StatusOK {
		t.Fatalf("card-only: %d %s", r.status, r.body)
	}
	if r := cardOnly.do(http.MethodPost, stationPath+"/operators", map[string]any{"badge": "9999999999"}); r.code() != "operator_sign_in_failed" {
		t.Fatalf("unknown card: %d %s", r.status, r.body)
	}

	// The administrator clears; never sets or reads.
	if r := owner.do(http.MethodDelete, userPath+"/pin", nil); r.status != http.StatusNoContent {
		t.Fatalf("clear PIN: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodDelete, userPath+"/badge", nil); r.status != http.StatusNoContent {
		t.Fatalf("clear badge: %d %s", r.status, r.body)
	}
	for _, action := range []string{"badge_enrolled", "pin_cleared", "badge_cleared"} {
		if len(h.auditEvents(t, action)) != 1 {
			t.Errorf("%s was not audited once", action)
		}
	}
	// A person without identity:manage reaches none of it.
	_, op := h.operator(t, "nosy", shopFloorKeys...)
	for _, r := range []*resp{
		op.do(http.MethodGet, stationsPath, nil),
		op.do(http.MethodDelete, userPath+"/pin", nil),
		op.do(http.MethodGet, userPath+"/operator-credentials", nil),
	} {
		if r.status != http.StatusForbidden {
			t.Errorf("without identity:manage: %d %s", r.status, r.body)
		}
	}
}

func TestStation_SignInNeverReadsTheOperatorCredentials(t *testing.T) {
	h := newHarness(t)
	id := h.seedUser(t, "pinonly@example.test", userPassword, h.operatorRole(t, shopFloorKeys...))
	c := h.signIn(t, id, false)
	c.do(http.MethodPut, "/api/v1/identity/account/pin", map[string]any{"pin": operatorPIN})
	if r := h.client(t).do(http.MethodPost, "/api/v1/identity/login", map[string]any{"email": "pinonly@example.test", "password": operatorPIN}); r.status == http.StatusOK {
		t.Fatal("a PIN signed a person in")
	}
}
