package identity_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// The stations' hardening after review (manufacturing phase 2 S): the
// sign-in's limits under concurrency, sessions ended with every change of a
// person's credentials or sessions, the audit of every ending, the backoff's
// decay and reset, the network allowlist on the socket and on pairing,
// erasure, the badge's enrolment and the key version.

// parallelSwitchIns makes n sign-ins at once at station, for the users who
// returns, each with a wrong PIN, and answers their statuses.
func parallelSwitchIns(t *testing.T, station *client, n int, who func(i int) uuid.UUID) []int {
	t.Helper()
	statuses := make([]int, n)
	var ready, done sync.WaitGroup
	begin := make(chan struct{})
	for i := range n {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			ready.Done()
			<-begin
			statuses[i] = station.do(http.MethodPost, stationPath+"/operators", map[string]any{"userId": who(i), "pin": "000001"}).status
		}()
	}
	ready.Wait()
	close(begin)
	done.Wait()
	return statuses
}

func TestStation_ParallelWrongPINsVerifyAtMostTheLimit(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	station := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "Rush"}).PairingCode)

	// Fifty wrong PINs at once, for fifty people: the attempts limit is
	// taken before verifying, so no more than it are verified (401) however
	// they interleave; the rest are refused before verifying (429).
	statuses := parallelSwitchIns(t, station, 50, func(int) uuid.UUID { return uuid.New() })
	verified, refused := 0, 0
	for _, s := range statuses {
		switch s {
		case http.StatusUnauthorized:
			verified++
		case http.StatusTooManyRequests:
			refused++
		default:
			t.Fatalf("statuses %v: want only 401 and 429", statuses)
		}
	}
	t.Logf("%d verified, %d refused", verified, refused)
	if verified > identity.StationAttemptsLimit || verified+refused != 50 {
		t.Fatalf("%d verified, %d refused, want at most %d verified", verified, refused, identity.StationAttemptsLimit)
	}
	if n := len(h.auditEvents(t, "operator_sign_in_failed")); n != verified {
		t.Fatalf("%d failures audited, %d verified", n, verified)
	}

	// Fifty at once for one person at a fresh station: attempts for one
	// person are taken one at a time, so the backoff each reads is the one
	// the attempt before it left, and the run stops at the fifth failure.
	other := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "Queue"}).PairingCode)
	opID, _ := h.operator(t, "op", shopFloorKeys...)
	parallelSwitchIns(t, other, 50, func(int) uuid.UUID { return opID })
	if n := h.count(t, `SELECT failures FROM identity.operator_backoff WHERE user_id = $1`, opID); n > 5 {
		t.Fatalf("%d failures recorded in parallel, want at most 5: the backoff was read before the one before it was written", n)
	}
}

// signedOutFor reports the operator session's end reason.
func (h *harness) endReason(t *testing.T, sessionID uuid.UUID) string {
	t.Helper()
	var reason *string
	if err := h.pool.QueryRow(context.Background(), `SELECT end_reason FROM identity.station_operator_sessions WHERE id = $1`, sessionID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason == nil {
		return ""
	}
	return *reason
}

func TestStation_CredentialsAndSignOutsEndOperatorSessions(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	station := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "Tablet"}).PairingCode)
	opID, own := h.operator(t, "op", shopFloorKeys...)
	userPath := "/api/v1/identity/users/" + opID.String()

	ended := func(step string, op operatorSignIn, reason string) {
		t.Helper()
		if _, err := h.access.Check(h.stationRequest(station, op.Token), allowedRule); !errors.Is(err, contracts.ErrUnauthenticated) {
			t.Fatalf("%s: the operator's token still works: %v", step, err)
		}
		if got := h.endReason(t, op.SessionID); got != reason {
			t.Fatalf("%s: end reason %q, want %q", step, got, reason)
		}
	}

	// The person sets a new PIN.
	op := mustSwitchIn(t, station, opID)
	if r := own.do(http.MethodPut, "/api/v1/identity/account/pin", map[string]any{"pin": "739164", "currentPassword": userPassword}); r.status != http.StatusNoContent {
		t.Fatalf("new PIN: %d %s", r.status, r.body)
	}
	ended("own PIN set", op, "credentials_changed")
	if r := switchIn(t, station, opID, "739164"); r.status != http.StatusOK {
		t.Fatalf("the new PIN: %d %s", r.status, r.body)
	}
	// An administrator enrols a badge.
	var op2 operatorSignIn
	switchIn(t, station, opID, "739164").json(&op2)
	if r := owner.do(http.MethodPut, userPath+"/badge", map[string]any{"badge": "CARD-0001"}); r.status != http.StatusNoContent {
		t.Fatalf("enrol: %d %s", r.status, r.body)
	}
	ended("badge enrolled", op2, "credentials_changed")
	// An administrator clears the PIN.
	var op3 operatorSignIn
	switchIn(t, station, opID, "739164").json(&op3)
	if r := owner.do(http.MethodDelete, userPath+"/pin", nil); r.status != http.StatusNoContent {
		t.Fatalf("clear PIN: %d %s", r.status, r.body)
	}
	ended("PIN cleared", op3, "credentials_changed")
	// The person signs out everywhere.
	if r := own.do(http.MethodPut, "/api/v1/identity/account/pin", map[string]any{"pin": operatorPIN, "currentPassword": userPassword}); r.status != http.StatusNoContent {
		t.Fatalf("PIN again: %d %s", r.status, r.body)
	}
	op4 := mustSwitchIn(t, station, opID)
	if r := own.do(http.MethodPost, "/api/v1/identity/account/sessions/revoke", nil); r.status != http.StatusOK && r.status != http.StatusNoContent {
		t.Fatalf("sign out everywhere: %d %s", r.status, r.body)
	}
	ended("signed out everywhere", op4, "signed_out")

	// Every ending is audited with its reason.
	reasons := map[string]int{}
	for _, e := range h.auditEvents(t, "operator_signed_out") {
		for _, reason := range []string{"credentials_changed", "signed_out", "replaced"} {
			if strings.Contains(e.After, `"reason":"`+reason+`"`) {
				reasons[reason]++
			}
		}
	}
	if reasons["credentials_changed"] < 3 || reasons["signed_out"] != 1 || reasons["replaced"] < 1 {
		t.Fatalf("operator_signed_out reasons = %v", reasons)
	}
}

func TestStation_AdministratorEndsASessionAndEveryEndingIsAudited(t *testing.T) {
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	p := registerStation(t, owner, map[string]any{"name": "Tablet", "idleMinutes": 5})
	station := h.pairStation(t, p.PairingCode)
	opID, _ := h.operator(t, "op", shopFloorKeys...)
	base := stationsPath + "/" + p.Station.ID.String()

	op := mustSwitchIn(t, station, opID)
	if r := owner.do(http.MethodPost, base+"/operators/"+uuid.NewString()+"/end", nil); r.status != http.StatusNotFound || r.code() != "operator_session_unknown" {
		t.Fatalf("an unknown session: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPost, base+"/operators/"+op.SessionID.String()+"/end", nil); r.status != http.StatusNoContent {
		t.Fatalf("end: %d %s", r.status, r.body)
	}
	if got := h.endReason(t, op.SessionID); got != "ended_by_admin" {
		t.Fatalf("end reason %q", got)
	}
	audited := func(reason string) []auditRow {
		var out []auditRow
		for _, e := range h.auditEvents(t, "operator_signed_out") {
			if strings.Contains(e.After, `"reason":"`+reason+`"`) {
				out = append(out, e)
			}
		}
		return out
	}
	if got := audited("ended_by_admin"); len(got) != 1 || got[0].Actor == nil || *got[0].Actor != ownerID {
		t.Fatalf("ended_by_admin audit = %+v", got)
	}

	// Idle, at the operator's own request and at another's sign-in.
	idle := mustSwitchIn(t, station, opID)
	h.advance(6 * time.Minute)
	if _, err := h.access.Check(h.stationRequest(station, idle.Token), allowedRule); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("an idle session: %v", err)
	}
	if len(audited("idle")) != 1 {
		t.Fatal("the idle ending was not audited")
	}
	otherID, _ := h.operator(t, "other", shopFloorKeys...)
	stale := mustSwitchIn(t, station, opID)
	h.advance(6 * time.Minute)
	owner = h.signIn(t, ownerID, false)
	mustSwitchIn(t, station, otherID) // ends the stale session before counting
	if got := h.endReason(t, stale.SessionID); got != "idle" || len(audited("idle")) != 2 {
		t.Fatalf("a stale session ended by a sign-in: %q, %d audited", got, len(audited("idle")))
	}
	// Revoking ends the rest, audited.
	if r := owner.do(http.MethodPost, base+"/revoke", nil); r.status != http.StatusOK {
		t.Fatalf("revoke: %d %s", r.status, r.body)
	}
	if len(audited("revoked")) != 1 {
		t.Fatal("the revocation's endings were not audited")
	}
}

func TestStation_StationChangedAndFailedPairingsAreAudited(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	p := registerStation(t, owner, map[string]any{"name": "Tablet", "idleMinutes": 5})
	path := stationsPath + "/" + p.Station.ID.String()
	if r := owner.do(http.MethodPut, path, map[string]any{"name": "Tablet", "idleMinutes": 15}); r.status != http.StatusOK {
		t.Fatalf("change: %d %s", r.status, r.body)
	}
	got := h.auditEvents(t, "station_changed")
	if len(got) != 1 || !strings.Contains(got[0].Before, `"idleMinutes":5`) || !strings.Contains(got[0].After, `"idleMinutes":15`) ||
		!strings.Contains(got[0].After, `"requirePinWithBadge":true`) {
		t.Fatalf("station_changed = %+v", got)
	}
	// The same settings again change nothing and audit nothing.
	owner.do(http.MethodPut, path, map[string]any{"name": "Tablet", "idleMinutes": 15})
	if len(h.auditEvents(t, "station_changed")) != 1 {
		t.Fatal("an unchanged station was audited")
	}

	h.client(t).do(http.MethodPost, stationPath+"/pair", map[string]any{"code": "garbage"})
	h.client(t).do(http.MethodPost, stationPath+"/pair", map[string]any{"code": "ABCD2345"})
	if got := h.auditEvents(t, "station_pairing_failed"); len(got) != 2 || !strings.Contains(got[0].After, `"ip":"198.18.`) {
		t.Fatalf("station_pairing_failed = %+v", got)
	}
}

func TestStation_AllowlistOnPairingAndOnTheSocket(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	p := registerStation(t, owner, map[string]any{"name": "Fenced", "allowedNetworks": []string{"10.0.0.0/8"}})

	// A code tried from outside the allowlist is refused as an invalid one,
	// and is not spent.
	if r := h.client(t).do(http.MethodPost, stationPath+"/pair", map[string]any{"code": p.PairingCode}); r.status != http.StatusBadRequest || r.code() != "pairing_code_invalid" {
		t.Fatalf("pairing from outside: %d %s", r.status, r.body)
	}
	if got := h.auditEvents(t, "station_pairing_failed"); len(got) != 1 || !strings.Contains(got[0].After, p.Station.ID.String()) {
		t.Fatalf("station_pairing_failed = %+v", got)
	}
	inside := h.client(t)
	inside.ip = "10.1.2.3"
	if r := inside.do(http.MethodPost, stationPath+"/pair", map[string]any{"code": p.PairingCode}); r.status != http.StatusOK {
		t.Fatalf("pairing from inside: %d %s", r.status, r.body)
	}

	// The socket's re-check holds the handshake's address to the allowlist.
	ctx := context.Background()
	pairing := h.stationPairing(t, p.Station.ID)
	if live, err := h.access.StationLive(ctx, p.Station.ID, pairing, "10.1.2.3"); err != nil || !live {
		t.Fatalf("inside: %v %v", live, err)
	}
	if live, err := h.access.StationLive(ctx, p.Station.ID, pairing, "192.168.1.1"); err != nil || live {
		t.Fatalf("outside: %v %v", live, err)
	}

	// Paired again in another browser, the socket of the first one ends: its
	// pairing is no longer the station's.
	r := owner.do(http.MethodPost, stationsPath+"/"+p.Station.ID.String()+"/pairing", nil)
	if r.status != http.StatusOK {
		t.Fatalf("a new code: %d %s", r.status, r.body)
	}
	var again stationPairing
	r.json(&again)
	other := h.client(t)
	other.ip = "10.1.2.4"
	if r := other.do(http.MethodPost, stationPath+"/pair", map[string]any{"code": again.PairingCode}); r.status != http.StatusOK {
		t.Fatalf("pairing again: %d %s", r.status, r.body)
	}
	if live, err := h.access.StationLive(ctx, p.Station.ID, pairing, "10.1.2.3"); err != nil || live {
		t.Fatalf("the old pairing after pairing again: %v %v", live, err)
	}
	if live, err := h.access.StationLive(ctx, p.Station.ID, h.stationPairing(t, p.Station.ID), "10.1.2.3"); err != nil || !live {
		t.Fatalf("the new pairing: %v %v", live, err)
	}
}

func TestStation_BackoffDecaysAndIsReset(t *testing.T) {
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	p := registerStation(t, owner, map[string]any{"name": "Tablet"})
	station := h.pairStation(t, p.PairingCode)
	opID, _ := h.operator(t, "op", shopFloorKeys...)
	failures := func() int {
		return h.count(t, `SELECT coalesce(max(failures), 0) FROM identity.operator_backoff WHERE user_id = $1`, opID)
	}

	for range 4 {
		switchIn(t, station, opID, "000001")
	}
	// An hour after the last failure the run starts again.
	h.advance(61 * time.Minute)
	switchIn(t, station, opID, "000001")
	if n := failures(); n != 1 {
		t.Fatalf("failures after an hour %d, want 1", n)
	}
	for range 4 {
		switchIn(t, station, opID, "000001")
	}
	if n := failures(); n != 5 {
		t.Fatalf("failures %d, want 5", n)
	}

	// The administrator sees the run and resets it.
	owner = h.signIn(t, ownerID, false)
	var list struct {
		Items []struct {
			Backoff []struct {
				UserID   uuid.UUID `json:"userId"`
				Failures int       `json:"failures"`
			} `json:"backoff"`
		} `json:"items"`
	}
	owner.do(http.MethodGet, stationsPath, nil).json(&list)
	if len(list.Items) != 1 || len(list.Items[0].Backoff) != 1 || list.Items[0].Backoff[0].UserID != opID || list.Items[0].Backoff[0].Failures != 5 {
		t.Fatalf("the list's backoff = %+v", list.Items)
	}
	reset := stationsPath + "/" + p.Station.ID.String() + "/backoff/" + opID.String()
	if r := owner.do(http.MethodDelete, reset, nil); r.status != http.StatusNoContent {
		t.Fatalf("reset: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodDelete, reset, nil); r.status != http.StatusNotFound {
		t.Fatalf("reset again: %d %s", r.status, r.body)
	}
	if got := h.auditEvents(t, "operator_backoff_reset"); len(got) != 1 || got[0].TargetUser == nil || *got[0].TargetUser != opID {
		t.Fatalf("operator_backoff_reset = %+v", got)
	}
	if r := switchIn(t, station, opID, operatorPIN); r.status != http.StatusOK {
		t.Fatalf("after the reset: %d %s", r.status, r.body)
	}

	// A PIN set clears the person's backoff at every station.
	for range 5 {
		switchIn(t, station, opID, "000001")
	}
	own := h.signIn(t, opID, false)
	if r := own.do(http.MethodPut, "/api/v1/identity/account/pin", map[string]any{"pin": "739164"}); r.status != http.StatusNoContent {
		t.Fatalf("new PIN: %d %s", r.status, r.body)
	}
	if n := failures(); n != 0 {
		t.Fatalf("a new PIN left %d failures", n)
	}
	// Attempts refused in backoff do not count on the station's limit: in a
	// fresh window, five failures and ten attempts in backoff leave the
	// station open.
	h.advance(6 * time.Minute)
	for range 5 {
		switchIn(t, station, opID, "000001")
	}
	for range 10 {
		switchIn(t, station, opID, "739164")
	}
	if r := switchIn(t, station, uuid.New(), "000001"); r.status != http.StatusUnauthorized {
		t.Fatalf("after attempts in backoff the station answers %d %s, want 401", r.status, r.body)
	}
}

func TestStation_ErasureKeepsTheOperatorSessions(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	station := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "Tablet"}).PairingCode)

	// A deleted person's session ends and its row stays, with the name.
	gone, _ := h.operator(t, "gone", shopFloorKeys...)
	op := mustSwitchIn(t, station, gone)
	if r := owner.do(http.MethodDelete, "/api/v1/identity/owner/users/"+gone.String(), nil); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.station_operator_sessions
	    WHERE id = $1 AND user_id IS NULL AND token_hash IS NULL AND ended_at IS NOT NULL AND end_reason = 'user_inactive' AND user_display = 'gone@example.test'`, op.SessionID); n != 1 {
		t.Fatal("the deleted person's session was not kept, ended and named")
	}

	// An erased person's session stays too, naming no one (M141).
	erased, _ := h.operator(t, "erased", shopFloorKeys...)
	op = mustSwitchIn(t, station, erased)
	h.exec(t, `UPDATE identity.users SET is_disabled = true, disabled_by = 'staff', managed_by = 'staff', employee_number = 'S-ERASE', employee_number_key = 'S-ERASE' WHERE id = $1`, erased)
	if err := h.accounts(t).Erase(context.Background(), contracts.Actor{System: contracts.SystemActorStaffRetention}, erased); err != nil {
		t.Fatalf("erase: %v", err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.station_operator_sessions
	    WHERE id = $1 AND user_id IS NULL AND token_hash IS NULL AND ended_at IS NOT NULL AND user_display = '[erased]'`, op.SessionID); n != 1 {
		t.Fatal("the erased person's session was not kept, ended and pseudonymised")
	}
}

func TestStation_BadgeEnrolmentAndBadgeOnlyStations(t *testing.T) {
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	opID, _ := h.operator(t, "op", shopFloorKeys...)
	readerID, _ := h.operator(t, "reader", "manufacturing:access")

	// Only for a person who may use the shop floor.
	if r := owner.do(http.MethodPut, "/api/v1/identity/users/"+readerID.String()+"/badge", map[string]any{"badge": "CARD-0002"}); r.status != http.StatusBadRequest {
		t.Fatalf("a badge for someone off the shop floor: %d %s", r.status, r.body)
	}
	// A session older than ten minutes needs the password.
	h.advance(11 * time.Minute)
	owner = h.signIn(t, ownerID, false)
	h.advance(11 * time.Minute)
	path := "/api/v1/identity/users/" + opID.String() + "/badge"
	if r := owner.do(http.MethodPut, path, map[string]any{"badge": "CARD-0001"}); r.status != http.StatusForbidden || r.code() != "reauthentication_required" {
		t.Fatalf("a stale session: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPut, path, map[string]any{"badge": "CARD-0001", "currentPassword": "wrong"}); r.status != http.StatusUnauthorized || r.code() != "invalid_credentials" {
		t.Fatalf("a wrong password: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPut, path, map[string]any{"badge": "CARD-0001", "currentPassword": ownerPassword}); r.status != http.StatusNoContent {
		t.Fatalf("with the password: %d %s", r.status, r.body)
	}
	if got := h.mailTo("op@example.test"); len(got) != 1 || !strings.Contains(got[0].Subject, "badge") {
		t.Fatalf("mail to the person = %+v", got)
	}

	// Making a station badge-only needs the same, and mails the
	// administrators.
	p := registerStation(t, owner, map[string]any{"name": "Cards", "currentPassword": ownerPassword})
	station := stationsPath + "/" + p.Station.ID.String()
	if r := owner.do(http.MethodPut, station, map[string]any{"name": "Cards", "requirePinWithBadge": false}); r.status != http.StatusForbidden || r.code() != "reauthentication_required" {
		t.Fatalf("badge-only with a stale session: %d %s", r.status, r.body)
	}
	before := len(h.mailTo(ownerEmail))
	if r := owner.do(http.MethodPut, station, map[string]any{"name": "Cards", "requirePinWithBadge": false, "currentPassword": ownerPassword}); r.status != http.StatusOK {
		t.Fatalf("badge-only with the password: %d %s", r.status, r.body)
	}
	if got := h.mailTo(ownerEmail); len(got) != before+1 || !strings.Contains(got[len(got)-1].Subject, "badge alone") {
		t.Fatalf("mail to the administrators = %+v", got)
	}
	// Saving it again, still badge-only, asks for nothing more.
	if r := owner.do(http.MethodPut, station, map[string]any{"name": "Cards 2", "requirePinWithBadge": false}); r.status != http.StatusOK {
		t.Fatalf("an already badge-only station: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPost, stationsPath, map[string]any{"name": "More cards", "requirePinWithBadge": false}); r.status != http.StatusForbidden {
		t.Fatalf("registering badge-only with a stale session: %d %s", r.status, r.body)
	}
}

func TestStation_WrongPasswordsForAPINCountOnTheAccount(t *testing.T) {
	h := newHarness(t)
	id := h.seedUser(t, "pins@example.test", userPassword, h.operatorRole(t, shopFloorKeys...))
	c := h.signIn(t, id, false)
	h.advance(11 * time.Minute)
	for i := range 10 {
		if r := c.do(http.MethodPut, "/api/v1/identity/account/pin", map[string]any{"pin": operatorPIN, "currentPassword": "wrong"}); r.status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d %s", i+1, r.status, r.body)
		}
	}
	if r := c.do(http.MethodPut, "/api/v1/identity/account/pin", map[string]any{"pin": operatorPIN, "currentPassword": userPassword}); r.status != http.StatusTooManyRequests {
		t.Fatalf("the eleventh: %d %s, want 429", r.status, r.body)
	}
}

func TestStation_CredentialsOfAnOlderKeyVersionNeedReenrolment(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	station := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "Tablet"}).PairingCode)
	opID, own := h.operator(t, "op", shopFloorKeys...)
	h.exec(t, `UPDATE identity.user_pins SET key_version = 0 WHERE user_id = $1`, opID)

	if r := switchIn(t, station, opID, operatorPIN); r.status != http.StatusUnauthorized {
		t.Fatalf("a PIN of an older key: %d %s", r.status, r.body)
	}
	var creds struct {
		HasPin              bool `json:"hasPin"`
		PinNeedsReenrolment bool `json:"pinNeedsReenrolment"`
	}
	owner.do(http.MethodGet, "/api/v1/identity/users/"+opID.String()+"/operator-credentials", nil).json(&creds)
	if !creds.HasPin || !creds.PinNeedsReenrolment {
		t.Fatalf("credentials = %+v", creds)
	}
	var mine struct {
		NeedsReenrolment bool `json:"needsReenrolment"`
	}
	own.do(http.MethodGet, "/api/v1/identity/account/pin", nil).json(&mine)
	if !mine.NeedsReenrolment {
		t.Fatal("the person is not told to set the PIN again")
	}
}

func TestStation_APairedStationWithANewCodeStaysPaired(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	p := registerStation(t, owner, map[string]any{"name": "Tablet"})
	station := h.pairStation(t, p.PairingCode)
	if r := owner.do(http.MethodPost, stationsPath+"/"+p.Station.ID.String()+"/pairing", nil); r.status != http.StatusOK {
		t.Fatalf("new code: %d %s", r.status, r.body)
	}
	var list struct {
		Items []stationPairingStation `json:"items"`
	}
	owner.do(http.MethodGet, stationsPath, nil).json(&list)
	if len(list.Items) != 1 || list.Items[0].State != "paired" {
		t.Fatalf("state = %+v, want paired while the new code waits", list.Items)
	}
	if r := station.do(http.MethodGet, stationPath, nil); r.status != http.StatusOK {
		t.Fatalf("the station still works: %d %s", r.status, r.body)
	}
}

func TestStation_LosingTheShopFloorEndsEvenReads(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	station := h.pairStation(t, registerStation(t, owner, map[string]any{"name": "Tablet"}).PairingCode)
	opID, _ := h.operator(t, "op", shopFloorKeys...)
	op := mustSwitchIn(t, station, opID)
	h.exec(t, `DELETE FROM identity.role_permissions WHERE permission_key = 'manufacturing:shop-floor'
	           AND role_id IN (SELECT role_id FROM identity.user_roles WHERE user_id = $1)`, opID)
	read := contracts.Rule{Kind: contracts.RulePermission, Names: []string{"manufacturing:access"}, Station: contracts.StationAllowed}
	if _, err := h.access.Check(h.stationRequest(station, op.Token), read); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("a read after losing the shop floor: %v, want the session ended", err)
	}
	if got := h.endReason(t, op.SessionID); got != "user_inactive" {
		t.Fatalf("end reason %q", got)
	}
}

func TestStation_TheSocketsHandshakeJudgesASessionCookieAsHTTPDoes(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	p := registerStation(t, owner, map[string]any{"name": "Tablet"})
	station := h.pairStation(t, p.PairingCode)
	personID, _ := h.operator(t, "person", shopFloorKeys...)
	handshake := func(session string) (uuid.UUID, bool, error) {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/live", nil)
		r.AddCookie(&http.Cookie{Name: identity.StationCookieName, Value: station.cookie(identity.StationCookieName)})
		r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: session})
		id, _, isStation, err := h.access.CheckStation(r)
		return id, isStation, err
	}

	// A live session beside the station's cookie makes the handshake a person's.
	live := h.session(t, personID, false)
	if _, isStation, err := handshake(live); isStation || err != nil {
		t.Fatalf("with a live session: station %v, %v", isStation, err)
	}
	// A dead one does not: the handshake is the station's, as on the HTTP path.
	if err := identity.RevokeAllSessions(h.access, context.Background(), store.New(h.pool), personID); err != nil {
		t.Fatal(err)
	}
	id, isStation, err := handshake(live)
	if !isStation || err != nil || id != p.Station.ID {
		t.Fatalf("with a dead session: %v %v %v, want the station", id, isStation, err)
	}
}
