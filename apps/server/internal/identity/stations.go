package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// This file is the shared shop-floor stations' endpoints (manufacturing phase
// 2 M129–M143): an administrator registers, changes, re-pairs and revokes
// stations (identity:manage); a tablet pairs itself with a one-time code and
// then, with its cookie alone, lists who may switch in, switches operators in
// with a PIN or a badge and switches them out. Access (access_station.go)
// resolves an operator's requests. Neither a PIN nor a badge is ever a sign-in
// credential: sign-in, MFA, passkeys and SSO never read their tables (M132).

// pairingCodeLifetime is how long a pairing code may be used (M129).
const pairingCodeLifetime = 15 * time.Minute

// pairingCodeLength is a pairing code's length in Crockford base32 symbols:
// 40 bits, enough for 15 minutes behind a per-address rate limit.
const pairingCodeLength = 8

// crockford is Crockford's base32 alphabet: no I, L, O or U.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// maxOperatorsPerStation bounds the operators signed in at once (M133).
const maxOperatorsPerStation = 10

// recentSignInWindow is how recent a session's sign-in must be for a PIN to
// be set without the current password (M132).
const recentSignInWindow = 10 * time.Minute

// The derived keys' purposes (srv/secrets): the PIN's pepper and the badge's
// MAC, each its own key, neither one Seal uses.
const (
	pinPepperPurpose = "identity/station-pin-pepper"
	badgeMACPurpose  = "identity/station-badge"
)

// The backoff per (station, user) (M133): from the fifth consecutive failure
// each attempt waits, 30 seconds doubling up to 15 minutes; every tenth
// failure alerts the administrators. A run whose last failure is backoffDecay
// old starts again at one.
const (
	backoffFrom     = 5
	backoffFirst    = 30 * time.Second
	backoffMax      = 15 * time.Minute
	backoffDecay    = time.Hour
	alertEvery      = 10
	stationAlertKey = "identity:manage"
)

// operatorKeyVersion is the version of the derived keys a PIN's pepper and a
// badge's MAC are made under, stored beside each hash. A rotation of those
// keys raises it: a PIN or a badge of an older version then no longer
// verifies, and the person's credentials read "needs re-enrolment" until it
// is set again.
const operatorKeyVersion int32 = 1

// operatorVerifySlots bounds the argon2id verifications of operator PINs
// running at once in this process: each takes the password parameters'
// memory, so a burst of sign-ins cannot exhaust it. An attempt that finds no
// slot within operatorVerifyWait is refused 429.
var operatorVerifySlots = make(chan struct{}, 6)

const operatorVerifyWait = 2 * time.Second

// policyStationPairing limits pairing per client address: ten codes tried in
// 15 minutes (M129).
var policyStationPairing = ratelimit.Policy{Name: "StationPairing", Limit: 10, Window: 15 * time.Minute, Message: authRateLimitMessage}

// policyStationPairingGlobal caps pairing attempts across the installation,
// whatever their addresses: 60 an hour.
var policyStationPairingGlobal = ratelimit.Policy{Name: "StationPairingGlobal", Limit: 60, Window: time.Hour, Message: authRateLimitMessage}

// stationPairingGlobalKey is policyStationPairingGlobal's one client.
const stationPairingGlobalKey = "installation"

// policyStationSignIn limits failed operator sign-ins per station: ten in five
// minutes, then 429 for the station for the rest of the window (M133). Only
// failures are counted.
var policyStationSignIn = ratelimit.Policy{
	Name:         "station-operator-sign-in",
	Limit:        10,
	Window:       5 * time.Minute,
	Message:      authRateLimitMessage,
	NoRetryAfter: true,
}

// policyStationAttempts limits every operator sign-in a station verifies,
// failed or not: thirty in five minutes. Its hit is taken before the
// verification, atomically, so parallel attempts cannot verify more than the
// limit between them however they interleave.
var policyStationAttempts = ratelimit.Policy{
	Name:         "station-operator-attempts",
	Limit:        30,
	Window:       5 * time.Minute,
	Message:      authRateLimitMessage,
	NoRetryAfter: true,
}

// policyStationOperatorsIP limits operator sign-ins per client address before
// any work, as the router applies it: generous, since a site's tablets may
// share one address.
var policyStationOperatorsIP = ratelimit.Policy{Name: "StationOperatorSignIn", Limit: 120, Window: time.Minute, Message: authRateLimitMessage}

// policyBadgeEnrolment limits enrolling badges per client address.
var policyBadgeEnrolment = ratelimit.Policy{Name: "BadgeEnrolment", Limit: 60, Window: 15 * time.Minute, Message: authRateLimitMessage}

// The audit actions (M139).
const (
	auditStationRegistered      = "station_registered"
	auditStationPaired          = "station_paired"
	auditStationRevoked         = "station_revoked"
	auditStationRepaired        = "station_repaired"
	auditStationNetworksChanged = "station_networks_changed"
	auditStationChanged         = "station_changed"
	auditStationPairingFailed   = "station_pairing_failed"
	auditStationSessionSignOut  = "station_session_signed_out"
	auditOperatorBackoffReset   = "operator_backoff_reset"
	auditPinSet                 = "pin_set"
	auditPinCleared             = "pin_cleared"
	auditPinDisabled            = "pin_disabled"
	auditBadgeEnrolled          = "badge_enrolled"
	auditBadgeCleared           = "badge_cleared"
	auditOperatorSignedIn       = "operator_signed_in"
	auditOperatorSignInFailed   = "operator_sign_in_failed"
	auditOperatorBackoff        = "operator_backoff"
	auditOperatorSignedOut      = "operator_signed_out"
)

// The refusals' words.
const (
	stationNameMessage        = "A station needs a name of at most 100 characters."
	stationIdleMessage        = "The idle timeout is 1 to 60 minutes."
	stationNetworksMessage    = "Each network is an address or a CIDR network such as 192.168.10.0/24; at most 50."
	pairingCodeInvalidMessage = "The pairing code is not valid. It may have expired or been used; ask an administrator for a new one."
	operatorSignInMessage     = "The sign-in did not succeed. Check the PIN or the badge and try again."
	operatorRequestMessage    = "Choose a person and enter their PIN, or scan a badge."
	tooManyOperatorsMessage   = "Ten operators are signed in at this station. Switch one out first."
	operatorUnknownMessage    = "That operator is not signed in at this station."
	pinInvalidMessage         = "A PIN is six digits."
	pinTooSimpleMessage       = "Choose a PIN that is not one digit repeated or a run such as 123456."
	pinReauthMessage          = "Enter your current password to set a PIN."
	pinPasswordMessage        = "The current password is not right."
	badgeInvalidMessage       = "A badge code is 4 to 128 characters."
	badgeUnavailableMessage   = "That card cannot be enrolled. Scan another card."
	badgeNoShopFloorMessage   = "A badge can only be enrolled for a person who may use the shop floor (manufacturing:shop-floor)."
	reauthMessage             = "Enter your current password to make this change."
	backoffUnknownMessage     = "That person has no failed sign-ins to reset at this station."
)

// stationAudit is a station's audit snapshot: what the station is, never a
// secret.
type stationAudit struct {
	StationID       uuid.UUID `json:"stationId"`
	Name            string    `json:"name"`
	IdleMinutes     int32     `json:"idleMinutes,omitempty"`
	RequirePin      *bool     `json:"requirePinWithBadge,omitempty"`
	AllowedNetworks []string  `json:"allowedNetworks,omitempty"`
}

// operatorAudit is an operator event's snapshot: the station and, for an
// ending, why. Never the PIN or the badge.
type operatorAudit struct {
	StationID uuid.UUID `json:"stationId"`
	Station   string    `json:"station"`
	SessionID uuid.UUID `json:"sessionId,omitzero"`
	Reason    string    `json:"reason,omitempty"`
	Failures  int32     `json:"failures,omitempty"`
}

// newPairingCode returns a fresh pairing code and the hash to store.
func newPairingCode() (code string, hash []byte) {
	b := make([]byte, pairingCodeLength)
	_, _ = rand.Read(b)
	out := make([]byte, pairingCodeLength)
	for i, v := range b {
		out[i] = crockford[int(v)%len(crockford)] // 256 is a multiple of 32: no bias
	}
	code = string(out)
	return code, hashToken([]byte(code))
}

// normalizePairingCode reads a typed code as Crockford base32 reads it: case
// and separators ignored, I and L as 1, O as 0. It reports false for anything
// that cannot be a code.
func normalizePairingCode(raw string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(raw) {
		switch {
		case r == '-' || unicode.IsSpace(r):
			continue
		case r == 'I' || r == 'L':
			r = '1'
		case r == 'O':
			r = '0'
		}
		if !strings.ContainsRune(crockford, r) {
			return "", false
		}
		b.WriteRune(r)
	}
	if b.Len() != pairingCodeLength {
		return "", false
	}
	return b.String(), true
}

// validatePIN checks a PIN's shape (M132): six digits, not one digit six
// times, not an ascending or descending run. It answers the refusal's code,
// "" for a valid PIN.
func validatePIN(pin string) string {
	if len(pin) != 6 {
		return "invalid_request"
	}
	for _, c := range pin {
		if c < '0' || c > '9' {
			return "invalid_request"
		}
	}
	same, up, down := true, true, true
	for i := 1; i < len(pin); i++ {
		d := int(pin[i]) - int(pin[i-1])
		same = same && d == 0
		up = up && d == 1
		down = down && d == -1
	}
	if same || up || down {
		return "pin_too_simple"
	}
	return ""
}

// pepperedPIN is the PIN as argon2id hashes it: HMAC-SHA-256 under the
// pepper's derived key, so a leaked table cannot be searched over the PIN's
// million values without APP_SECRET (M132).
func (s *server) pepperedPIN(pin string) (string, error) {
	if s.deps.Secrets == nil {
		return "", errors.New("identity: no secrets box for the PIN pepper")
	}
	mac, err := s.deps.Secrets.MAC(pinPepperPurpose, []byte(pin))
	if err != nil {
		return "", fmt.Errorf("identity: pepper the PIN: %w", err)
	}
	return base64.RawStdEncoding.EncodeToString(mac), nil
}

// badgeHash is a badge code as stored: HMAC-SHA-256 under its own derived key.
func (s *server) badgeHash(code string) ([]byte, error) {
	if s.deps.Secrets == nil {
		return nil, errors.New("identity: no secrets box for the badge MAC")
	}
	return s.deps.Secrets.MAC(badgeMACPurpose, []byte(code))
}

// normalizeBadge is a scanned code without the reader's surrounding blanks;
// false for one outside 4..128 characters.
func normalizeBadge(raw string) (string, bool) {
	code := strings.TrimSpace(raw)
	n := len([]rune(code))
	return code, n >= 4 && n <= 128
}

// initials are a name's first letters: the first and the last word's.
func initials(name string) string {
	words := strings.Fields(name)
	if len(words) == 0 {
		return "?"
	}
	first := []rune(words[0])
	out := strings.ToUpper(string(first[0]))
	if len(words) > 1 {
		last := []rune(words[len(words)-1])
		out += strings.ToUpper(string(last[0]))
	}
	return out
}

// parseNetworks reads an allowlist: CIDR networks or single addresses, at
// most 50; empty or absent is none. It reports false for any entry that does
// not parse.
func parseNetworks(in *[]string) ([]netip.Prefix, bool) {
	if in == nil || len(*in) == 0 {
		return nil, true
	}
	if len(*in) > 50 {
		return nil, false
	}
	out := make([]netip.Prefix, 0, len(*in))
	for _, raw := range *in {
		raw = strings.TrimSpace(raw)
		if p, err := netip.ParsePrefix(raw); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(raw)
		if err != nil {
			return nil, false
		}
		a = a.Unmap().WithZone("")
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, true
}

func networkStrings(ps []netip.Prefix) []string {
	if len(ps) == 0 {
		return nil
	}
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

// stationState is a station's state for the list. A station in use with a new
// code issued is still paired (its pairingExpiresAt tells the re-pairing is
// pending): its token works until the code is used.
func stationState(st store.IdentityStation, now time.Time) gen.StationState {
	switch {
	case stationLive(st, now):
		return "paired"
	case st.PairingHash != nil && st.PairingExpiresAt != nil && st.PairingExpiresAt.After(now):
		return "pairing"
	case st.RevokedAt != nil:
		return "revoked"
	case st.TokenHash != nil:
		return "expired"
	default:
		return "unpaired"
	}
}

// operatorSessionBody is an active operator session as both the station and
// the list answer it.
func operatorSessionBody(row store.ListActiveOperatorSessionsRow) gen.StationOperatorSession {
	return gen.StationOperatorSession{
		SessionId:      row.ID,
		UserId:         row.UserID,
		DisplayName:    row.DisplayName,
		Initials:       initials(row.DisplayName),
		StartedAt:      row.StartedAt,
		LastActivityAt: row.LastActivityAt,
	}
}

// operatorStillActive reports whether a session the table holds open is still
// one at now: not idle past the station's timeout, not past 12 hours.
func operatorStillActive(st store.IdentityStation, row store.ListActiveOperatorSessionsRow, now time.Time) bool {
	return now.Sub(row.StartedAt) < operatorSessionAbsolute &&
		now.Sub(row.LastActivityAt) < time.Duration(st.IdleMinutes)*time.Minute
}

// stationBody is a station as the list answers it, with its active operators
// and the runs of failed sign-ins at it that have not decayed.
func stationBody(st store.IdentityStation, ops []store.ListActiveOperatorSessionsRow, backoff []store.ListOperatorBackoffRow, now time.Time) gen.Station {
	runs := []gen.StationBackoff{}
	for _, b := range backoff {
		if b.StationID == st.ID {
			runs = append(runs, gen.StationBackoff{
				UserId: b.UserID, DisplayName: b.DisplayName, Failures: int(b.Failures),
				LastFailureAt: b.LastFailureAt, NextAttemptAt: b.NextAttemptAt,
			})
		}
	}
	active := []gen.StationOperatorSession{}
	if stationLive(st, now) {
		for _, row := range ops {
			if row.StationID == st.ID && operatorStillActive(st, row, now) {
				active = append(active, operatorSessionBody(row))
			}
		}
	}
	var networks *[]string
	if ns := networkStrings(st.AllowedNetworks); ns != nil {
		networks = &ns
	}
	return gen.Station{
		Id:                  st.ID,
		Name:                st.Name,
		State:               stationState(st, now),
		IdleMinutes:         int(st.IdleMinutes),
		RequirePinWithBadge: st.RequirePinWithBadge,
		AllowedNetworks:     networks,
		CreatedAt:           st.CreatedAt,
		PairedAt:            st.PairedAt,
		PairingExpiresAt:    st.PairingExpiresAt,
		TokenExpiresAt:      st.TokenExpiresAt,
		LastSeenAt:          st.LastSeenAt,
		LastSeenIp:          st.LastSeenIp,
		RevokedAt:           st.RevokedAt,
		AlertedAt:           st.AlertedAt,
		ActiveOperators:     active,
		Backoff:             runs,
	}
}

// stationInput is a StationRequest judged: its values, or the refusal.
type stationInput struct {
	name       string
	idle       int32
	requirePin bool
	networks   []netip.Prefix
	password   string // the caller's current password, for a station made badge-only
}

func readStationRequest(body *gen.StationRequest) (stationInput, *refusal) {
	var in gen.StationRequest
	if body != nil {
		in = *body
	}
	fields := map[string][]string{}
	name := strings.TrimSpace(in.Name)
	if name == "" || len([]rune(name)) > 100 {
		fields["name"] = []string{stationNameMessage}
	}
	idle := 5
	if in.IdleMinutes != nil {
		idle = *in.IdleMinutes
	}
	if idle < 1 || idle > 60 {
		fields["idleMinutes"] = []string{stationIdleMessage}
	}
	requirePin := true
	if in.RequirePinWithBadge != nil {
		requirePin = *in.RequirePinWithBadge
	}
	networks, ok := parseNetworks(in.AllowedNetworks)
	if !ok {
		fields["allowedNetworks"] = []string{stationNetworksMessage}
	}
	if len(fields) > 0 {
		r := refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage, fields)
		return stationInput{}, &r
	}
	return stationInput{name: name, idle: int32(idle), requirePin: requirePin, networks: networks, password: deref(in.CurrentPassword)}, nil
}

// GetIdentityStations lists every station with its active operators.
// (GET /api/v1/identity/stations)
func (s *server) GetIdentityStations(ctx context.Context, _ gen.GetIdentityStationsRequestObject) (gen.GetIdentityStationsResponseObject, error) {
	stations, err := s.q.ListStations(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: list stations: %w", err)
	}
	ops, err := s.q.ListActiveOperatorSessions(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("identity: list operator sessions: %w", err)
	}
	now := s.deps.Clock()
	backoff, err := s.q.ListOperatorBackoff(ctx, store.ListOperatorBackoffParams{Now: now, Decay: interval(backoffDecay)})
	if err != nil {
		return nil, fmt.Errorf("identity: list operator backoff: %w", err)
	}
	items := make([]gen.Station, 0, len(stations))
	for _, st := range stations {
		items = append(items, stationBody(st, ops, backoff, now))
	}
	return gen.GetIdentityStations200JSONResponse{Items: items}, nil
}

// stationAnswer is st as the administrator's endpoints answer it: with its
// active operators and its runs of failed sign-ins.
func (s *server) stationAnswer(ctx context.Context, st store.IdentityStation, now time.Time) (gen.Station, error) {
	ops, err := s.q.ListActiveOperatorSessions(ctx, &st.ID)
	if err != nil {
		return gen.Station{}, fmt.Errorf("identity: list operator sessions: %w", err)
	}
	backoff, err := s.q.ListOperatorBackoff(ctx, store.ListOperatorBackoffParams{Now: now, Decay: interval(backoffDecay)})
	if err != nil {
		return gen.Station{}, fmt.Errorf("identity: list operator backoff: %w", err)
	}
	return stationBody(st, ops, backoff, now), nil
}

// stationSnapshot is a station's settings for the audit, never a secret.
func stationSnapshot(st store.IdentityStation) stationAudit {
	return stationAudit{
		StationID: st.ID, Name: st.Name, IdleMinutes: st.IdleMinutes, RequirePin: &st.RequirePinWithBadge,
		AllowedNetworks: networkStrings(st.AllowedNetworks),
	}
}

// recentAuth is the refusal, nil when there is none, of a change that needs
// the caller to have proved who they are lately (M132): setting their PIN,
// enrolling a badge, making a station take a badge alone. A session that
// signed in within recentSignInWindow is enough. Otherwise password must be
// the caller's current one: an attempt counts on the per-account sign-in
// throttle (policyLoginAttempts, keyed on the account), checked before the
// password, so a session in other hands cannot guess it faster than sign-in
// could. reauth is the words of the 403 when neither is given.
func (s *server) recentAuth(ctx context.Context, p contracts.Principal, password, reauth string, now time.Time) (*refusal, error) {
	if password == "" {
		created, err := s.q.GetSessionCreatedAt(ctx, p.SessionID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("identity: read session: %w", err)
		}
		if err != nil || now.Sub(created) > recentSignInWindow {
			ref := refuse(http.StatusForbidden, "reauthentication_required", reauth, nil)
			return &ref, nil
		}
		return nil, nil
	}
	throttle := "account:" + p.UserID.String()
	d, err := s.deps.Limiter.Blocked(ctx, policyLoginAttempts, throttle)
	if err != nil {
		return nil, fmt.Errorf("identity: password throttle: %w", err)
	}
	if !d.Allowed {
		ref := refuse(http.StatusTooManyRequests, "rate_limited", authRateLimitMessage, nil)
		return &ref, nil
	}
	u, err := s.q.GetUserByID(ctx, p.UserID)
	if err != nil {
		return nil, fmt.Errorf("identity: read user: %w", err)
	}
	ok, err := s.checkPassword(u.PasswordHash, password)
	if err != nil {
		return nil, fmt.Errorf("identity: verify password: %w", err)
	}
	if !ok {
		if _, err := s.deps.Limiter.Hit(ctx, policyLoginAttempts, throttle); err != nil {
			return nil, fmt.Errorf("identity: password throttle: %w", err)
		}
		ref := refuse(http.StatusUnauthorized, "invalid_credentials", pinPasswordMessage, nil)
		return &ref, nil
	}
	return nil, nil
}

// mailAdmins mails the holders of identity:manage through the system mail.
// A mail that fails is logged: what caused it stands either way.
func (s *server) mailAdmins(ctx context.Context, subject, body string) {
	if s.deps.Mail == nil {
		return
	}
	sso, err := s.access.sso.get(ctx)
	if err != nil {
		s.deps.Logger.Warn("identity: could not read who to mail", "error", err)
		return
	}
	holders, err := s.q.DirectoryPermissionHolders(ctx, store.DirectoryPermissionHoldersParams{
		PermissionKey: stationAlertKey, ScimEnabled: sso.SCIM != nil,
	})
	if err != nil {
		s.deps.Logger.Warn("identity: could not read who to mail", "error", err)
		return
	}
	for _, h := range holders {
		if h.Email == "" {
			continue
		}
		if err := s.deps.Mail.Send(ctx, mail.Message{To: h.Email, Subject: subject, TextBody: body}); err != nil {
			s.deps.Logger.Warn("identity: could not mail an administrator", "subject", subject, "error", err)
		}
	}
}

// mailBadgeOnly tells the administrators that st now takes a badge alone.
func (s *server) mailBadgeOnly(ctx context.Context, st store.IdentityStation, now time.Time) {
	s.mailAdmins(ctx, "The station "+st.Name+" now takes a badge alone",
		"The shop-floor station "+st.Name+" was set to sign operators in with a badge alone, without their PIN ("+
			now.UTC().Format(time.RFC3339)+"). Anyone holding a colleague's card can then act as them at that station."+
			" If this was not intended, turn Require the PIN with a badge back on under Workspace → Stations.")
}

// PostIdentityStations registers a station and answers its pairing code once.
// A station that takes a badge alone needs the caller's recent sign-in or
// password, and the administrators are told of it.
// (POST /api/v1/identity/stations)
func (s *server) PostIdentityStations(ctx context.Context, req gen.PostIdentityStationsRequestObject) (gen.PostIdentityStationsResponseObject, error) {
	in, bad := readStationRequest(req.Body)
	if bad != nil {
		return *bad, nil
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	if !in.requirePin {
		if ref, err := s.recentAuth(ctx, p, in.password, reauthMessage, now); err != nil || ref != nil {
			if err != nil {
				return nil, err
			}
			return *ref, nil
		}
	}
	code, hash := newPairingCode()
	expires := now.Add(pairingCodeLifetime)
	var st store.IdentityStation
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		st, err = q.InsertStation(ctx, store.InsertStationParams{
			ID: uuid.New(), Name: in.name, IdleMinutes: in.idle, RequirePinWithBadge: in.requirePin,
			AllowedNetworks: in.networks, PairingHash: hash, PairingExpiresAt: &expires, CreatedBy: &p.UserID, Now: now,
		})
		if err != nil {
			return fmt.Errorf("identity: register station: %w", err)
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, action: auditStationRegistered, before: struct{}{}, after: stationSnapshot(st), mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return nil, err
	}
	if !st.RequirePinWithBadge {
		s.mailBadgeOnly(ctx, st, now)
	}
	return gen.PostIdentityStations200JSONResponse{
		Station: stationBody(st, nil, nil, now), PairingCode: code, PairingExpiresAt: expires,
	}, nil
}

// PutIdentityStationsById changes a station's settings, audited
// station_changed with them before and after; a changed allowlist is audited
// station_networks_changed as well. Making the station take a badge alone
// needs the caller's recent sign-in or password, and the administrators are
// told of it.
// (PUT /api/v1/identity/stations/{id})
func (s *server) PutIdentityStationsById(ctx context.Context, req gen.PutIdentityStationsByIdRequestObject) (gen.PutIdentityStationsByIdResponseObject, error) {
	in, bad := readStationRequest(req.Body)
	if bad != nil {
		return *bad, nil
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	notFound := refuse(http.StatusNotFound, "not_found", "No such station.", nil)
	// Proving who the caller is happens before the transaction; the
	// transaction refuses still if the station took a PIN after all.
	authed := false
	if !in.requirePin {
		current, err := s.q.GetStation(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound, nil
		}
		if err != nil {
			return nil, fmt.Errorf("identity: read station: %w", err)
		}
		if current.RequirePinWithBadge {
			if ref, err := s.recentAuth(ctx, p, in.password, reauthMessage, now); err != nil || ref != nil {
				if err != nil {
					return nil, err
				}
				return *ref, nil
			}
			authed = true
		}
	}
	var before, st store.IdentityStation
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		before, err = q.LockStation(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound
		}
		if err != nil {
			return err
		}
		if before.RequirePinWithBadge && !in.requirePin && !authed {
			return refuse(http.StatusForbidden, "reauthentication_required", reauthMessage, nil)
		}
		st, err = q.UpdateStation(ctx, store.UpdateStationParams{
			ID: req.Id, Name: in.name, IdleMinutes: in.idle, RequirePinWithBadge: in.requirePin, AllowedNetworks: in.networks,
		})
		if err != nil {
			return err
		}
		was, is := stationSnapshot(before), stationSnapshot(st)
		wasNets, isNets := strings.Join(was.AllowedNetworks, ","), strings.Join(is.AllowedNetworks, ",")
		if was.Name == is.Name && was.IdleMinutes == is.IdleMinutes && *was.RequirePin == *is.RequirePin && wasNets == isNets {
			return nil
		}
		if err := writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, action: auditStationChanged, before: was, after: is, mfa: p.MFAVerified,
		}); err != nil {
			return err
		}
		if wasNets == isNets {
			return nil
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor:  &p.UserID,
			action: auditStationNetworksChanged,
			before: stationAudit{StationID: st.ID, Name: before.Name, AllowedNetworks: was.AllowedNetworks},
			after:  stationAudit{StationID: st.ID, Name: st.Name, AllowedNetworks: is.AllowedNetworks},
			mfa:    p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PutIdentityStationsByIdResponseObject](err)
	}
	if before.RequirePinWithBadge && !st.RequirePinWithBadge {
		s.mailBadgeOnly(ctx, st, now)
	}
	body, err := s.stationAnswer(ctx, st, now)
	if err != nil {
		return nil, err
	}
	return gen.PutIdentityStationsById200JSONResponse(body), nil
}

// PostIdentityStationsByIdPairing issues a new pairing code (re-pairing).
// (POST /api/v1/identity/stations/{id}/pairing)
func (s *server) PostIdentityStationsByIdPairing(ctx context.Context, req gen.PostIdentityStationsByIdPairingRequestObject) (gen.PostIdentityStationsByIdPairingResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	code, hash := newPairingCode()
	expires := now.Add(pairingCodeLifetime)
	var st store.IdentityStation
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.LockStation(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
			return refuse(http.StatusNotFound, "not_found", "No such station.", nil)
		} else if err != nil {
			return err
		}
		var err error
		st, err = q.SetStationPairing(ctx, store.SetStationPairingParams{ID: req.Id, PairingHash: hash, PairingExpiresAt: &expires})
		if err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, action: auditStationRepaired, before: struct{}{},
			after: stationAudit{StationID: st.ID, Name: st.Name}, mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PostIdentityStationsByIdPairingResponseObject](err)
	}
	body, err := s.stationAnswer(ctx, st, now)
	if err != nil {
		return nil, err
	}
	return gen.PostIdentityStationsByIdPairing200JSONResponse{Station: body, PairingCode: code, PairingExpiresAt: expires}, nil
}

// auditOperatorEnded audits one operator session ended for reason on the
// caller's transaction q: operator_signed_out, with the station and the
// session, by actor (nil for none: the station, the idle timer).
func auditOperatorEnded(ctx context.Context, q *store.Queries, r *http.Request, now time.Time, actor, user *uuid.UUID, st store.IdentityStation, sessionID uuid.UUID, reason string) error {
	return writeAudit(ctx, q, r, now, auditEvent{
		actor: actor, targetUser: user, action: auditOperatorSignedOut, before: struct{}{},
		after: operatorAudit{StationID: st.ID, Station: st.Name, SessionID: sessionID, Reason: reason},
	})
}

// endStationOperators ends every active operator session of st for reason on
// q, auditing each one.
func endStationOperators(ctx context.Context, q *store.Queries, r *http.Request, now time.Time, actor *uuid.UUID, st store.IdentityStation, reason string) error {
	ended, err := q.EndStationOperatorSessions(ctx, store.EndStationOperatorSessionsParams{StationID: st.ID, Now: &now, EndReason: &reason})
	if err != nil {
		return err
	}
	for _, row := range ended {
		if err := auditOperatorEnded(ctx, q, r, now, actor, row.UserID, st, row.ID, reason); err != nil {
			return err
		}
	}
	return nil
}

// PostIdentityStationsByIdRevoke revokes a station: its token and code are
// cleared and every operator session at it ends, in one transaction, so the
// very next request is station_unknown.
// (POST /api/v1/identity/stations/{id}/revoke)
func (s *server) PostIdentityStationsByIdRevoke(ctx context.Context, req gen.PostIdentityStationsByIdRevokeRequestObject) (gen.PostIdentityStationsByIdRevokeResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	var st store.IdentityStation
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.LockStation(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
			return refuse(http.StatusNotFound, "not_found", "No such station.", nil)
		} else if err != nil {
			return err
		}
		var err error
		st, err = q.RevokeStation(ctx, store.RevokeStationParams{ID: req.Id, Now: &now, RevokedBy: &p.UserID})
		if err != nil {
			return err
		}
		if err := endStationOperators(ctx, q, r, now, &p.UserID, st, endRevoked); err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, action: auditStationRevoked, before: struct{}{},
			after: stationAudit{StationID: st.ID, Name: st.Name}, mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PostIdentityStationsByIdRevokeResponseObject](err)
	}
	return gen.PostIdentityStationsByIdRevoke200JSONResponse(stationBody(st, nil, nil, now)), nil
}

// PostIdentityStationsByIdOperatorsBySessionIdEnd ends an operator's session
// at a station from Workspace › Stations (M136), audited operator_signed_out
// with the reason ended_by_admin.
// (POST /api/v1/identity/stations/{id}/operators/{sessionId}/end)
func (s *server) PostIdentityStationsByIdOperatorsBySessionIdEnd(ctx context.Context, req gen.PostIdentityStationsByIdOperatorsBySessionIdEndRequestObject) (gen.PostIdentityStationsByIdOperatorsBySessionIdEndResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		st, err := q.GetStation(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return refuse(http.StatusNotFound, "not_found", "No such station.", nil)
		}
		if err != nil {
			return err
		}
		reason := endByAdmin
		userID, err := q.EndActiveOperatorSessionAtStation(ctx, store.EndActiveOperatorSessionAtStationParams{
			ID: req.SessionId, StationID: st.ID, Now: &now, EndReason: &reason,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return refuse(http.StatusNotFound, "operator_session_unknown", operatorUnknownMessage, nil)
		}
		if err != nil {
			return err
		}
		return auditOperatorEnded(ctx, q, r, now, &p.UserID, userID, st, req.SessionId, reason)
	})
	if err != nil {
		return refusalOr[gen.PostIdentityStationsByIdOperatorsBySessionIdEndResponseObject](err)
	}
	return gen.PostIdentityStationsByIdOperatorsBySessionIdEnd204Response{}, nil
}

// DeleteIdentityStationsByIdBackoffByUserId resets a person's run of failed
// sign-ins at a station (M133), audited operator_backoff_reset.
// (DELETE /api/v1/identity/stations/{id}/backoff/{userId})
func (s *server) DeleteIdentityStationsByIdBackoffByUserId(ctx context.Context, req gen.DeleteIdentityStationsByIdBackoffByUserIdRequestObject) (gen.DeleteIdentityStationsByIdBackoffByUserIdResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		st, err := q.GetStation(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return refuse(http.StatusNotFound, "not_found", "No such station.", nil)
		}
		if err != nil {
			return err
		}
		n, err := q.DeleteOperatorBackoff(ctx, store.DeleteOperatorBackoffParams{StationID: st.ID, UserID: req.UserId})
		if err != nil {
			return err
		}
		if n == 0 {
			return refuse(http.StatusNotFound, "not_found", backoffUnknownMessage, nil)
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &req.UserId, action: auditOperatorBackoffReset, before: struct{}{},
			after: operatorAudit{StationID: st.ID, Station: st.Name}, mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.DeleteIdentityStationsByIdBackoffByUserIdResponseObject](err)
	}
	return gen.DeleteIdentityStationsByIdBackoffByUserId204Response{}, nil
}

// stationPaired is pairing's 200: the station cookie, the session cookie's
// removal when the request carried one, then the body.
type stationPaired struct {
	cookies
	body gen.StationSelf
}

func (r stationPaired) VisitPostIdentityStationPairResponse(w http.ResponseWriter) error {
	r.set(w)
	writeJSON(w, http.StatusOK, "application/json", r.body)
	return nil
}

// pairingAudit is a failed pairing's snapshot: where it came from and, when
// the code was a station's, which.
type pairingAudit struct {
	IP        string    `json:"ip,omitempty"`
	StationID uuid.UUID `json:"stationId,omitzero"`
}

// pairingFailed audits a refused pairing attempt, station_pairing_failed, on
// its own transaction: the refusal itself writes nothing.
func (s *server) pairingFailed(ctx context.Context, r *http.Request, stationID uuid.UUID, now time.Time) error {
	return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return writeAudit(ctx, store.New(tx), r, now, auditEvent{
			action: auditStationPairingFailed, before: struct{}{}, after: pairingAudit{IP: httpx.ClientIP(r), StationID: stationID},
		})
	})
}

// PostIdentityStationPair pairs this browser with a one-time code (M129). The
// code's row is locked, checked and spent in one transaction with the new
// token, so a code pairs one browser. A code tried from outside the station's
// network allowlist is refused as an invalid one. A session the request
// carries is ended in the same transaction and its cookie cleared in the
// answer (M138). Beside the router's limit per address, attempts are capped
// across the installation, and every refused one is audited.
// (POST /api/v1/identity/station/pair)
func (s *server) PostIdentityStationPair(ctx context.Context, req gen.PostIdentityStationPairRequestObject) (gen.PostIdentityStationPairResponseObject, error) {
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	d, err := s.deps.Limiter.Hit(ctx, policyStationPairingGlobal, stationPairingGlobalKey)
	if err != nil {
		return nil, fmt.Errorf("identity: pairing limit: %w", err)
	}
	if !d.Allowed {
		return refuse(http.StatusTooManyRequests, "rate_limited", authRateLimitMessage, nil), nil
	}
	now := s.deps.Clock()
	invalid := gen.PostIdentityStationPair400JSONResponse(authErrorBody("pairing_code_invalid", pairingCodeInvalidMessage, nil))
	var code string
	if req.Body != nil {
		code = req.Body.Code
	}
	normalized, ok := normalizePairingCode(code)
	if !ok {
		if err := s.pairingFailed(ctx, r, uuid.Nil, now); err != nil {
			return nil, err
		}
		return invalid, nil
	}
	token, tokenHash := newToken()
	expires := now.Add(stationTokenLifetime)
	var sessionID *uuid.UUID
	if p, ok := contracts.PrincipalFrom(ctx); ok && p.SessionID != uuid.Nil && p.Via == nil {
		sessionID = &p.SessionID
	}
	refused := refuse(http.StatusBadRequest, "pairing_code_invalid", pairingCodeInvalidMessage, nil)
	var tried uuid.UUID // the station whose code was tried, for a refusal's audit
	var st store.IdentityStation
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		found, err := q.LockStationByPairingHash(ctx, hashToken([]byte(normalized)))
		if errors.Is(err, pgx.ErrNoRows) {
			return refused
		}
		if err != nil {
			return err
		}
		tried = found.ID
		if found.PairingExpiresAt == nil || !found.PairingExpiresAt.After(now) {
			return refused
		}
		if !addressAllowed(found.AllowedNetworks, httpx.ClientIP(r)) {
			return refused
		}
		// A new token: whatever browser held the old one is no longer this
		// station, and neither are its operators.
		if err := endStationOperators(ctx, q, r, now, nil, found, endReplaced); err != nil {
			return err
		}
		st, err = q.PairStation(ctx, store.PairStationParams{ID: found.ID, TokenHash: tokenHash, TokenExpiresAt: &expires, Now: &now})
		if err != nil {
			return err
		}
		if err := writeAudit(ctx, q, r, now, auditEvent{
			action: auditStationPaired, before: struct{}{}, after: stationAudit{StationID: st.ID, Name: st.Name},
		}); err != nil {
			return err
		}
		if sessionID == nil {
			return nil
		}
		if err := q.RevokeSession(ctx, store.RevokeSessionParams{ID: *sessionID, Now: now}); err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			action: auditStationSessionSignOut, targetUser: principalUser(ctx), before: struct{}{},
			after: stationAudit{StationID: st.ID, Name: st.Name},
		})
	})
	if err != nil {
		var ref refusal
		if errors.As(err, &ref) {
			if err := s.pairingFailed(ctx, r, tried, now); err != nil {
				return nil, err
			}
			return invalid, nil
		}
		return nil, err
	}
	zone, err := (&businessCalendar{q: s.q}).zoneName(ctx)
	if err != nil {
		return nil, err
	}
	answer := stationPaired{
		cookies: cookies{s.access.newStationCookie(token)},
		body: gen.StationSelf{
			Id: st.ID, Name: st.Name, IdleMinutes: int(st.IdleMinutes), RequirePinWithBadge: st.RequirePinWithBadge,
			TimeZone: zone, Operators: []gen.StationOperatorSession{},
		},
	}
	if _, has := sessionTokenFrom(r); has {
		answer.cookies = append(answer.cookies, s.access.expiredSessionCookie())
	}
	return answer, nil
}

// principalUser is the request's signed-in person, nil when there is none.
func principalUser(ctx context.Context) *uuid.UUID {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok || p.UserID == uuid.Nil {
		return nil
	}
	return &p.UserID
}

// stationOfRequest resolves the station a station endpoint is called by: a
// browser that also holds a live session is refused (station_has_session,
// M134), then the cookie is resolved (station_unknown).
func (s *server) stationOfRequest(ctx context.Context) (store.IdentityStation, *http.Request, error) {
	r, err := requestFrom(ctx)
	if err != nil {
		return store.IdentityStation{}, nil, err
	}
	now := s.deps.Clock()
	live, err := s.access.liveSessionCookie(ctx, r, now)
	if err != nil {
		return store.IdentityStation{}, nil, err
	}
	if live {
		return store.IdentityStation{}, nil, errStationHasSession
	}
	st, err := s.access.resolveStation(ctx, r, now)
	return st, r, err
}

// stationRefusalOf is a station refusal as a refusal, false for any other
// error.
func stationRefusalOf(err error) (refusal, bool) {
	var sr *stationRefusal
	if errors.As(err, &sr) {
		return refuse(sr.status, sr.code, sr.message, nil), true
	}
	return refusal{}, false
}

// activeOperators is the station's operators still signed in at now.
func (s *server) activeOperators(ctx context.Context, st store.IdentityStation, now time.Time) ([]gen.StationOperatorSession, error) {
	rows, err := s.q.ListActiveOperatorSessions(ctx, &st.ID)
	if err != nil {
		return nil, fmt.Errorf("identity: list operator sessions: %w", err)
	}
	out := []gen.StationOperatorSession{}
	for _, row := range rows {
		if operatorStillActive(st, row, now) {
			out = append(out, operatorSessionBody(row))
		}
	}
	return out, nil
}

// GetIdentityStation is the station as it reads itself (M130).
// (GET /api/v1/identity/station)
func (s *server) GetIdentityStation(ctx context.Context, _ gen.GetIdentityStationRequestObject) (gen.GetIdentityStationResponseObject, error) {
	st, _, err := s.stationOfRequest(ctx)
	if err != nil {
		if ref, ok := stationRefusalOf(err); ok {
			return ref, nil
		}
		return nil, err
	}
	now := s.deps.Clock()
	ops, err := s.activeOperators(ctx, st, now)
	if err != nil {
		return nil, err
	}
	zone, err := (&businessCalendar{q: s.q}).zoneName(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetIdentityStation200JSONResponse{
		Id: st.ID, Name: st.Name, IdleMinutes: int(st.IdleMinutes), RequirePinWithBadge: st.RequirePinWithBadge,
		TimeZone: zone, Operators: ops,
	}, nil
}

// GetIdentityStationOperators lists who may switch in (M133): the people
// holding manufacturing:shop-floor with a PIN or a badge, by name, with
// initials and nothing more.
// (GET /api/v1/identity/station/operators)
func (s *server) GetIdentityStationOperators(ctx context.Context, _ gen.GetIdentityStationOperatorsRequestObject) (gen.GetIdentityStationOperatorsResponseObject, error) {
	if _, _, err := s.stationOfRequest(ctx); err != nil {
		if ref, ok := stationRefusalOf(err); ok {
			return ref, nil
		}
		return nil, err
	}
	sso, err := s.access.sso.get(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.StationOperatorCandidates(ctx, store.StationOperatorCandidatesParams{
		ScimEnabled: sso.SCIM != nil, PermissionKey: "manufacturing:shop-floor",
	})
	if err != nil {
		return nil, fmt.Errorf("identity: list station operators: %w", err)
	}
	items := make([]gen.StationOperatorCandidate, 0, len(rows))
	for _, row := range rows {
		items = append(items, gen.StationOperatorCandidate{
			UserId: row.ID, DisplayName: row.DisplayName, Initials: initials(row.DisplayName),
			HasPin: row.HasPin, HasBadge: row.HasBadge,
		})
	}
	return gen.GetIdentityStationOperators200JSONResponse{Items: items}, nil
}

// operatorSignInFailed is the one answer of every failed operator sign-in.
var operatorSignInFailed = gen.PostIdentityStationOperators401JSONResponse(authErrorBody("operator_sign_in_failed", operatorSignInMessage, nil))

// operatorTooMany is the 429 of an operator sign-in refused before verifying.
var operatorTooMany = gen.PostIdentityStationOperators429JSONResponse{Body: authErrorBody("rate_limited", authRateLimitMessage, nil)}

// acquireVerifySlot takes one of operatorVerifySlots, waiting at most
// operatorVerifyWait; false when none came free.
func acquireVerifySlot(ctx context.Context) (release func(), ok bool) {
	t := time.NewTimer(operatorVerifyWait)
	defer t.Stop()
	select {
	case operatorVerifySlots <- struct{}{}:
		return func() { <-operatorVerifySlots }, true
	case <-t.C:
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
}

// inBackoff reports whether b, a person's run at a station (or none),
// refuses an attempt at now without verifying.
func inBackoff(b *store.IdentityOperatorBackoff, now time.Time) bool {
	return b != nil && b.NextAttemptAt != nil && b.NextAttemptAt.After(now)
}

// readBackoff is the person's run at the station on q, nil for none.
func readBackoff(ctx context.Context, q *store.Queries, stationID, userID uuid.UUID) (*store.IdentityOperatorBackoff, error) {
	b, err := q.GetOperatorBackoff(ctx, store.GetOperatorBackoffParams{StationID: stationID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: read backoff: %w", err)
	}
	return &b, nil
}

// signInOutcome is what one operator sign-in's transaction came to.
type signInOutcome int

const (
	signInSucceeded signInOutcome = iota
	signInFailed                  // verified and wrong: counts on the station's limit
	signInInBackoff               // refused without verifying: counts on nothing
)

// PostIdentityStationOperators switches an operator in (M133). In order:
//
//  1. The station's failure limit, already full: 429.
//  2. Who the attempt is for: the badge's holder, or the person chosen. The
//     same reads are made for a person who does not exist.
//  3. A person in backoff at this station is refused without verifying and
//     without counting on the station's limits.
//  4. The station's attempts limit is hit — atomically, before verifying —
//     so parallel attempts verify no more than its limit between them: 429.
//  5. A verification slot of the process (operatorVerifySlots): 429 when
//     none comes free.
//  6. In one transaction, holding the (station, person) lock so attempts for
//     one person are taken one at a time: the backoff read again, the PIN
//     verified against the person's hash or the dummy hash (a person with
//     none, an unknown person, a PIN of an older key version), the person's
//     right to act and the shop floor, and then either the failure recorded
//     (the backoff, the audit) or the session made. Nothing in it waits for
//     a second connection.
//
// Every failure is the same 401. A failure counts on the station's failure
// limit; the tenth of a run, or that limit tripping, alerts the
// administrators.
// (POST /api/v1/identity/station/operators)
func (s *server) PostIdentityStationOperators(ctx context.Context, req gen.PostIdentityStationOperatorsRequestObject) (gen.PostIdentityStationOperatorsResponseObject, error) {
	st, r, err := s.stationOfRequest(ctx)
	if err != nil {
		if ref, ok := stationRefusalOf(err); ok {
			return ref, nil
		}
		return nil, err
	}
	var in gen.StationOperatorSignInRequest
	if req.Body != nil {
		in = *req.Body
	}
	badge := ""
	if in.Badge != nil {
		badge = strings.TrimSpace(*in.Badge)
	}
	pin := deref(in.Pin)
	switch {
	case badge != "":
		if st.RequirePinWithBadge && pin == "" {
			return gen.PostIdentityStationOperators400JSONResponse(authErrorBody("invalid_request", operatorRequestMessage,
				map[string][]string{"pin": {operatorRequestMessage}})), nil
		}
	case in.UserId == nil || pin == "":
		return gen.PostIdentityStationOperators400JSONResponse(authErrorBody("invalid_request", operatorRequestMessage, nil)), nil
	}

	limitKey := st.ID.String()
	d, err := s.deps.Limiter.Blocked(ctx, policyStationSignIn, limitKey)
	if err != nil {
		return nil, fmt.Errorf("identity: station sign-in limit: %w", err)
	}
	if !d.Allowed {
		return operatorTooMany, nil
	}

	now := s.deps.Clock()
	sso, err := s.access.sso.get(ctx)
	if err != nil {
		return nil, err
	}
	// Who the attempt is for: the badge's holder, or the person chosen.
	var userID uuid.UUID
	known := false
	if badge != "" {
		if code, ok := normalizeBadge(badge); ok {
			hash, err := s.badgeHash(code)
			if err != nil {
				return nil, err
			}
			row, err := s.q.GetUserBadgeByHash(ctx, hash)
			switch {
			case err == nil:
				userID, known = row.UserID, row.KeyVersion == operatorKeyVersion
			case !errors.Is(err, pgx.ErrNoRows):
				return nil, fmt.Errorf("identity: read badge: %w", err)
			}
		}
	} else {
		userID = *in.UserId
		present, err := s.q.UserExists(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("identity: read user: %w", err)
		}
		known = present
	}
	// The same read for a person who does not exist (L8): the time taken
	// does not tell them apart.
	b, err := readBackoff(ctx, s.q, st.ID, userID)
	if err != nil {
		return nil, err
	}
	if inBackoff(b, now) {
		if err := s.operatorRefused(ctx, r, st, userID, b.Failures, now); err != nil {
			return nil, err
		}
		return operatorSignInFailed, nil
	}
	if d, err := s.deps.Limiter.Hit(ctx, policyStationAttempts, limitKey); err != nil {
		return nil, fmt.Errorf("identity: station attempts limit: %w", err)
	} else if !d.Allowed {
		return operatorTooMany, nil
	}
	release, ok := acquireVerifySlot(ctx)
	if !ok {
		return operatorTooMany, nil
	}
	defer release()

	var (
		outcome   signInOutcome
		failures  int32
		alertRun  bool
		name      string
		token     string
		sessionID = uuid.New()
	)
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.LockOperatorAttempt(ctx, store.LockOperatorAttemptParams{StationID: st.ID, UserID: userID}); err != nil {
			return err
		}
		b, err := readBackoff(ctx, q, st.ID, userID)
		if err != nil {
			return err
		}
		if inBackoff(b, now) {
			// An attempt for the same person finished first and put them in
			// backoff: this one is refused as if it had come after it.
			outcome, failures = signInInBackoff, b.Failures
			return auditOperatorFailed(ctx, q, r, st, userID, failures, now)
		}
		verified, err := s.verifyOperator(ctx, q, userID, known, badge != "", pin)
		if err != nil {
			return err
		}
		if verified {
			// The person must be one who may act and hold the shop floor's
			// keys, as a session's request would be judged (rule 12's person
			// checks).
			if verified, err = userHoldsOn(ctx, q, sso.SCIM != nil, userID, now, stationPermissions...); err != nil {
				return err
			}
		}
		if !verified {
			outcome = signInFailed
			if known {
				b, err := q.RecordOperatorFailure(ctx, store.RecordOperatorFailureParams{
					StationID: st.ID, UserID: userID, Now: now, Decay: interval(backoffDecay),
				})
				if err != nil {
					return err
				}
				failures = b.Failures
				if wait := backoffAfter(b.Failures); wait > 0 {
					next := now.Add(wait)
					if err := q.SetOperatorNextAttempt(ctx, store.SetOperatorNextAttemptParams{StationID: st.ID, UserID: userID, NextAttemptAt: &next}); err != nil {
						return err
					}
				}
				alertRun = b.Failures%alertEvery == 0
			}
			return auditOperatorFailed(ctx, q, r, st, userID, failures, now)
		}
		outcome = signInSucceeded
		name, err = q.GetUserDisplayName(ctx, userID)
		if err != nil {
			return err
		}
		var tokenHash []byte
		token, tokenHash = newToken()
		return s.startOperatorSession(ctx, q, r, st, userID, name, sessionID, tokenHash, now)
	})
	if err != nil {
		if ref, ok := stationRefusalOf(err); ok {
			return ref, nil
		}
		return refusalOr[gen.PostIdentityStationOperatorsResponseObject](err)
	}
	switch outcome {
	case signInInBackoff:
		return operatorSignInFailed, nil
	case signInFailed:
		if err := s.operatorFailed(ctx, r, st, userID, failures, alertRun, now); err != nil {
			return nil, err
		}
		return operatorSignInFailed, nil
	}
	return gen.PostIdentityStationOperators200JSONResponse{
		SessionId: sessionID,
		Token:     token,
		StartedAt: now,
		Operator:  gen.StationOperator{UserId: userID, DisplayName: name, Initials: initials(name)},
	}, nil
}

// startOperatorSession makes the operator session of a verified sign-in on
// q: the station's row locked so the count holds until this one is in, the
// station's stale sessions and the person's own session here ended (each
// audited), the bound of ten, the session, the person's backoff here
// cleared, and operator_signed_in.
func (s *server) startOperatorSession(ctx context.Context, q *store.Queries, r *http.Request, st store.IdentityStation, userID uuid.UUID, name string, sessionID uuid.UUID, tokenHash []byte, now time.Time) error {
	locked, err := q.LockStation(ctx, st.ID)
	if err != nil {
		return err
	}
	if !stationLive(locked, now) {
		return errStationUnknown
	}
	stale, err := q.EndStaleOperatorSessions(ctx, store.EndStaleOperatorSessionsParams{
		StationID: st.ID, Now: now, Idle: interval(time.Duration(locked.IdleMinutes) * time.Minute), Absolute: interval(operatorSessionAbsolute),
	})
	if err != nil {
		return err
	}
	for _, row := range stale {
		if err := auditOperatorEnded(ctx, q, r, now, nil, row.UserID, st, row.ID, deref(row.EndReason)); err != nil {
			return err
		}
	}
	reason := endReplaced
	replaced, err := q.EndUserOperatorSessionsAtStation(ctx, store.EndUserOperatorSessionsAtStationParams{
		StationID: st.ID, UserID: &userID, Now: &now, EndReason: &reason,
	})
	if err != nil {
		return err
	}
	for _, id := range replaced {
		if err := auditOperatorEnded(ctx, q, r, now, nil, &userID, st, id, reason); err != nil {
			return err
		}
	}
	n, err := q.CountActiveOperatorSessions(ctx, st.ID)
	if err != nil {
		return err
	}
	if n >= maxOperatorsPerStation {
		return refuse(http.StatusConflict, "too_many_operators", tooManyOperatorsMessage, nil)
	}
	if err := q.InsertOperatorSession(ctx, store.InsertOperatorSessionParams{
		ID: sessionID, StationID: st.ID, UserID: &userID, UserDisplay: name, TokenHash: tokenHash, Now: now,
	}); err != nil {
		return err
	}
	if _, err := q.DeleteOperatorBackoff(ctx, store.DeleteOperatorBackoffParams{StationID: st.ID, UserID: userID}); err != nil {
		return err
	}
	return writeAudit(ctx, q, r, now, auditEvent{
		actor: &userID, targetUser: &userID, action: auditOperatorSignedIn, before: struct{}{},
		after: operatorAudit{StationID: st.ID, Station: st.Name, SessionID: sessionID},
	})
}

// verifyOperator verifies an attempt for userID on q. real is false for an
// unknown person (or a badge of an older key version): the PIN is then
// verified against the dummy hash and the answer is false whatever it is,
// and so it is for a PIN set under an older key version. The person's PIN is
// read whether they exist or not. A badge alone (the station does not
// require the PIN with it) is verified by the badge's lookup, which found
// userID; with a PIN as well, the PIN must be the badge holder's.
func (s *server) verifyOperator(ctx context.Context, q *store.Queries, userID uuid.UUID, real, byBadge bool, pin string) (bool, error) {
	if byBadge && pin == "" {
		return real, nil
	}
	peppered, err := s.pepperedPIN(pin)
	if err != nil {
		return false, err
	}
	hash := s.dummyPasswordHash
	hasPin := false
	row, err := q.GetUserPin(ctx, userID)
	switch {
	case err == nil:
		if real && row.KeyVersion == operatorKeyVersion {
			hash, hasPin = row.PinHash, true
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return false, fmt.Errorf("identity: read PIN: %w", err)
	}
	ok, err := verifyPassword(hash, peppered)
	if err != nil {
		return false, fmt.Errorf("identity: verify PIN: %w", err)
	}
	return ok && hasPin, nil
}

// backoffAfter is the wait after the failures-th consecutive failure; zero
// before the fifth.
func backoffAfter(failures int32) time.Duration {
	if failures < backoffFrom {
		return 0
	}
	d := backoffFirst
	for i := backoffFrom; i < int(failures) && d < backoffMax; i++ {
		d *= 2
	}
	return min(d, backoffMax)
}

// auditOperatorFailed audits a failed sign-in on q: the station, the person
// chosen or the badge's holder — recorded even when no such person exists,
// so the audit says who was tried — and the run's failures; never the PIN.
func auditOperatorFailed(ctx context.Context, q *store.Queries, r *http.Request, st store.IdentityStation, userID uuid.UUID, failures int32, now time.Time) error {
	var target *uuid.UUID
	if userID != uuid.Nil {
		target = &userID
	}
	return writeAudit(ctx, q, r, now, auditEvent{
		targetUser: target, action: auditOperatorSignInFailed, before: struct{}{},
		after: operatorAudit{StationID: st.ID, Station: st.Name, Failures: failures},
	})
}

// operatorRefused audits an attempt refused for a person in backoff, on its
// own transaction: it counts on neither of the station's limits and does not
// lengthen the backoff.
func (s *server) operatorRefused(ctx context.Context, r *http.Request, st store.IdentityStation, userID uuid.UUID, failures int32, now time.Time) error {
	return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return auditOperatorFailed(ctx, store.New(tx), r, st, userID, failures, now)
	})
}

// operatorFailed counts a verified failure on the station's failure limit
// and, when the run reached a tenth failure (alertRun) or the failure fills
// the limit's window, audits operator_backoff and alerts the administrators.
func (s *server) operatorFailed(ctx context.Context, r *http.Request, st store.IdentityStation, userID uuid.UUID, failures int32, alertRun bool, now time.Time) error {
	if _, err := s.deps.Limiter.Hit(ctx, policyStationSignIn, st.ID.String()); err != nil {
		return fmt.Errorf("identity: station sign-in limit: %w", err)
	}
	// The failure that fills the window trips the station's limit: the next
	// attempt is refused before any verification, and the administrators
	// hear of it.
	d, err := s.deps.Limiter.Blocked(ctx, policyStationSignIn, st.ID.String())
	if err != nil {
		return fmt.Errorf("identity: station sign-in limit: %w", err)
	}
	if d.Allowed && !alertRun {
		return nil
	}
	var target *uuid.UUID
	if userID != uuid.Nil {
		target = &userID
	}
	if err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return writeAudit(ctx, store.New(tx), r, now, auditEvent{
			targetUser: target, action: auditOperatorBackoff, before: struct{}{},
			after: operatorAudit{StationID: st.ID, Station: st.Name, Failures: failures},
		})
	}); err != nil {
		return err
	}
	s.alertStation(ctx, st, now)
	return nil
}

// alertStation mails the holders of identity:manage that a station sees
// failed operator sign-ins, at most once an hour per station (M133). A mail
// that fails is logged: the refusal stands either way.
func (s *server) alertStation(ctx context.Context, st store.IdentityStation, now time.Time) {
	claimed, err := s.q.MarkStationAlerted(ctx, store.MarkStationAlertedParams{ID: st.ID, Now: now})
	if err != nil || claimed == 0 {
		if err != nil {
			s.deps.Logger.Warn("identity: could not record a station alert", "station", st.ID, "error", err)
		}
		return
	}
	s.mailAdmins(ctx, "Failed sign-ins at the station "+st.Name,
		"Repeated sign-ins with a wrong PIN or badge were made at the shop-floor station "+st.Name+
			" ("+now.UTC().Format(time.RFC3339)+"). The station slows further attempts down and goes on working."+
			" Check who is using it; you can see its activity, reset a person's backoff and revoke it under Workspace → Stations."+
			" This alert is sent at most once an hour per station.")
}

// PostIdentityStationOperatorsBySessionIdEnd switches an operator out with the
// station cookie alone (M136).
// (POST /api/v1/identity/station/operators/{sessionId}/end)
func (s *server) PostIdentityStationOperatorsBySessionIdEnd(ctx context.Context, req gen.PostIdentityStationOperatorsBySessionIdEndRequestObject) (gen.PostIdentityStationOperatorsBySessionIdEndResponseObject, error) {
	st, r, err := s.stationOfRequest(ctx)
	if err != nil {
		if ref, ok := stationRefusalOf(err); ok {
			return ref, nil
		}
		return nil, err
	}
	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		reason := endSwitchedOut
		userID, err := q.EndActiveOperatorSessionAtStation(ctx, store.EndActiveOperatorSessionAtStationParams{
			ID: req.SessionId, StationID: st.ID, Now: &now, EndReason: &reason,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return refuse(http.StatusNotFound, "operator_session_unknown", operatorUnknownMessage, nil)
		}
		if err != nil {
			return err
		}
		return auditOperatorEnded(ctx, q, r, now, nil, userID, st, req.SessionId, reason)
	})
	if err != nil {
		return refusalOr[gen.PostIdentityStationOperatorsBySessionIdEndResponseObject](err)
	}
	return gen.PostIdentityStationOperatorsBySessionIdEnd204Response{}, nil
}

// credentialsChanged is what a PIN or a badge set or cleared does beside
// itself, on the same transaction q (M136): the person's operator sessions at
// every station end (credentials_changed, audited with actor) and their runs
// of failures at every station are cleared.
func credentialsChanged(ctx context.Context, q *store.Queries, r *http.Request, actor *uuid.UUID, userID uuid.UUID, now time.Time) error {
	if err := endUserOperatorSessions(ctx, q, r, actor, userID, endCredentialsChanged, now); err != nil {
		return err
	}
	if err := q.DeleteUserOperatorBackoff(ctx, userID); err != nil {
		return fmt.Errorf("identity: clear operator backoff: %w", err)
	}
	return nil
}

// GetIdentityAccountPin tells whether the caller has a PIN, and whether it
// was set under an older key version and must be set again.
// (GET /api/v1/identity/account/pin)
func (s *server) GetIdentityAccountPin(ctx context.Context, _ gen.GetIdentityAccountPinRequestObject) (gen.GetIdentityAccountPinResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.q.GetUserPin(ctx, p.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetIdentityAccountPin200JSONResponse{Set: false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: read PIN: %w", err)
	}
	return gen.GetIdentityAccountPin200JSONResponse{Set: true, SetAt: &row.SetAt, NeedsReenrolment: row.KeyVersion != operatorKeyVersion}, nil
}

// PutIdentityAccountPin sets the caller's PIN (M132), with the current
// password or within 10 minutes of signing in. Their operator sessions end
// and their backoff is cleared with it.
// (PUT /api/v1/identity/account/pin)
func (s *server) PutIdentityAccountPin(ctx context.Context, req gen.PutIdentityAccountPinRequestObject) (gen.PutIdentityAccountPinResponseObject, error) {
	var in gen.AccountPinRequest
	if req.Body != nil {
		in = *req.Body
	}
	switch validatePIN(in.Pin) {
	case "invalid_request":
		return gen.PutIdentityAccountPin400JSONResponse(authErrorBody("invalid_request", pinInvalidMessage, map[string][]string{"pin": {pinInvalidMessage}})), nil
	case "pin_too_simple":
		return gen.PutIdentityAccountPin400JSONResponse(authErrorBody("pin_too_simple", pinTooSimpleMessage, map[string][]string{"pin": {pinTooSimpleMessage}})), nil
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	if ref, err := s.recentAuth(ctx, p, deref(in.CurrentPassword), pinReauthMessage, now); err != nil || ref != nil {
		if err != nil {
			return nil, err
		}
		return *ref, nil
	}
	peppered, err := s.pepperedPIN(in.Pin)
	if err != nil {
		return nil, err
	}
	hash, err := hashPassword(peppered)
	if err != nil {
		return nil, fmt.Errorf("identity: hash PIN: %w", err)
	}
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.UpsertUserPin(ctx, store.UpsertUserPinParams{UserID: p.UserID, PinHash: hash, KeyVersion: operatorKeyVersion, Now: now}); err != nil {
			return err
		}
		if err := credentialsChanged(ctx, q, r, &p.UserID, p.UserID, now); err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &p.UserID, action: auditPinSet, before: struct{}{}, after: struct{}{}, mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("identity: set PIN: %w", err)
	}
	return gen.PutIdentityAccountPin204Response{}, nil
}

// DeleteIdentityAccountPin removes the caller's PIN; their operator sessions
// end with it.
// (DELETE /api/v1/identity/account/pin)
func (s *server) DeleteIdentityAccountPin(ctx context.Context, _ gen.DeleteIdentityAccountPinRequestObject) (gen.DeleteIdentityAccountPinResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		n, err := q.DeleteUserPin(ctx, p.UserID)
		if err != nil || n == 0 {
			return err
		}
		if err := credentialsChanged(ctx, q, r, &p.UserID, p.UserID, now); err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &p.UserID, action: auditPinDisabled, before: struct{}{}, after: struct{}{}, mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("identity: remove PIN: %w", err)
	}
	return gen.DeleteIdentityAccountPin204Response{}, nil
}

// personExists reports whether the person id names exists.
func (s *server) personExists(ctx context.Context, id uuid.UUID) (bool, error) {
	present, err := s.q.UserExists(ctx, id)
	if err != nil {
		return false, fmt.Errorf("identity: read user: %w", err)
	}
	return present, nil
}

// GetIdentityUsersByIdOperatorCredentials tells whether a person has a PIN or
// a badge, and whether either needs re-enrolment, never either.
// (GET /api/v1/identity/users/{id}/operator-credentials)
func (s *server) GetIdentityUsersByIdOperatorCredentials(ctx context.Context, req gen.GetIdentityUsersByIdOperatorCredentialsRequestObject) (gen.GetIdentityUsersByIdOperatorCredentialsResponseObject, error) {
	present, err := s.personExists(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !present {
		return refuse(http.StatusNotFound, "not_found", "No such person.", nil), nil
	}
	var out gen.OperatorCredentials
	if pin, err := s.q.GetUserPin(ctx, req.Id); err == nil {
		out.HasPin, out.PinSetAt, out.PinNeedsReenrolment = true, &pin.SetAt, pin.KeyVersion != operatorKeyVersion
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("identity: read PIN: %w", err)
	}
	if badge, err := s.q.GetUserBadge(ctx, req.Id); err == nil {
		out.HasBadge, out.BadgeSetAt, out.BadgeNeedsReenrolment = true, &badge.SetAt, badge.KeyVersion != operatorKeyVersion
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("identity: read badge: %w", err)
	}
	return gen.GetIdentityUsersByIdOperatorCredentials200JSONResponse(out), nil
}

// adminCredentialWrite runs a PIN's or a badge's administrative change for
// the person id on one transaction with its audit, answering 404 for an
// unknown person. A change ends the person's operator sessions and clears
// their backoff (credentialsChanged).
func (s *server) adminCredentialWrite(ctx context.Context, id uuid.UUID, action string, write func(q *store.Queries, actor uuid.UUID, now time.Time) (bool, error)) error {
	present, err := s.personExists(ctx, id)
	if err != nil {
		return err
	}
	if !present {
		return refuse(http.StatusNotFound, "not_found", "No such person.", nil)
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return err
	}
	now := s.deps.Clock()
	return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		changed, err := write(q, p.UserID, now)
		if err != nil || !changed {
			return err
		}
		if err := credentialsChanged(ctx, q, r, &p.UserID, id, now); err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &id, action: action, before: struct{}{}, after: struct{}{}, mfa: p.MFAVerified,
		})
	})
}

// DeleteIdentityUsersByIdPin clears a person's PIN (M132).
// (DELETE /api/v1/identity/users/{id}/pin)
func (s *server) DeleteIdentityUsersByIdPin(ctx context.Context, req gen.DeleteIdentityUsersByIdPinRequestObject) (gen.DeleteIdentityUsersByIdPinResponseObject, error) {
	err := s.adminCredentialWrite(ctx, req.Id, auditPinCleared, func(q *store.Queries, _ uuid.UUID, _ time.Time) (bool, error) {
		n, err := q.DeleteUserPin(ctx, req.Id)
		return n > 0, err
	})
	if err != nil {
		return refusalOr[gen.DeleteIdentityUsersByIdPinResponseObject](err)
	}
	return gen.DeleteIdentityUsersByIdPin204Response{}, nil
}

// PutIdentityUsersByIdBadge enrols a person's badge (M132): only for a
// person who holds manufacturing:shop-floor, with the caller's recent
// sign-in or password. A card that cannot be enrolled is one generic 409,
// which does not say whose it is. The person is mailed that a badge was
// enrolled for them.
// (PUT /api/v1/identity/users/{id}/badge)
func (s *server) PutIdentityUsersByIdBadge(ctx context.Context, req gen.PutIdentityUsersByIdBadgeRequestObject) (gen.PutIdentityUsersByIdBadgeResponseObject, error) {
	var in gen.BadgeRequest
	if req.Body != nil {
		in = *req.Body
	}
	code, ok := normalizeBadge(in.Badge)
	if !ok {
		return refuse(http.StatusBadRequest, "invalid_request", badgeInvalidMessage, map[string][]string{"badge": {badgeInvalidMessage}}), nil
	}
	present, err := s.personExists(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !present {
		return refuse(http.StatusNotFound, "not_found", "No such person.", nil), nil
	}
	floor, err := s.access.UserHolds(ctx, req.Id, "manufacturing:shop-floor")
	if err != nil {
		return nil, err
	}
	if !floor {
		return refuse(http.StatusBadRequest, "invalid_request", badgeNoShopFloorMessage, map[string][]string{"badge": {badgeNoShopFloorMessage}}), nil
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	if ref, err := s.recentAuth(ctx, p, deref(in.CurrentPassword), reauthMessage, s.deps.Clock()); err != nil || ref != nil {
		if err != nil {
			return nil, err
		}
		return *ref, nil
	}
	hash, err := s.badgeHash(code)
	if err != nil {
		return nil, err
	}
	err = s.adminCredentialWrite(ctx, req.Id, auditBadgeEnrolled, func(q *store.Queries, actor uuid.UUID, now time.Time) (bool, error) {
		return true, q.UpsertUserBadge(ctx, store.UpsertUserBadgeParams{UserID: req.Id, BadgeHash: hash, KeyVersion: operatorKeyVersion, Now: now, SetBy: &actor})
	})
	if db.IsUniqueViolation(err, "ux_user_badges_hash") {
		return refuse(http.StatusConflict, "badge_unavailable", badgeUnavailableMessage, nil), nil
	}
	if err != nil {
		return refusalOr[gen.PutIdentityUsersByIdBadgeResponseObject](err)
	}
	s.mailBadgeEnrolled(ctx, req.Id)
	return gen.PutIdentityUsersByIdBadge204Response{}, nil
}

// mailBadgeEnrolled tells a person a badge was enrolled for them, so a card
// enrolled behind their back does not go unnoticed. A mail that fails is
// logged.
func (s *server) mailBadgeEnrolled(ctx context.Context, userID uuid.UUID) {
	if s.deps.Mail == nil {
		return
	}
	u, err := s.q.GetUserByID(ctx, userID)
	if err != nil || u.Email == "" {
		if err != nil {
			s.deps.Logger.Warn("identity: could not read who to tell of a badge", "error", err)
		}
		return
	}
	if err := s.deps.Mail.Send(ctx, mail.Message{
		To:      u.Email,
		Subject: "A badge was enrolled for you",
		TextBody: "An administrator enrolled a badge for you to switch in with at Vantigo's shared shop-floor stations." +
			" It replaces any badge you had. If you did not hand over a card for this, tell your administrator.",
	}); err != nil {
		s.deps.Logger.Warn("identity: could not tell a person of a badge", "error", err)
	}
}

// DeleteIdentityUsersByIdBadge removes a person's badge.
// (DELETE /api/v1/identity/users/{id}/badge)
func (s *server) DeleteIdentityUsersByIdBadge(ctx context.Context, req gen.DeleteIdentityUsersByIdBadgeRequestObject) (gen.DeleteIdentityUsersByIdBadgeResponseObject, error) {
	err := s.adminCredentialWrite(ctx, req.Id, auditBadgeCleared, func(q *store.Queries, _ uuid.UUID, _ time.Time) (bool, error) {
		n, err := q.DeleteUserBadge(ctx, req.Id)
		return n > 0, err
	})
	if err != nil {
		return refusalOr[gen.DeleteIdentityUsersByIdBadgeResponseObject](err)
	}
	return gen.DeleteIdentityUsersByIdBadge204Response{}, nil
}

// The refusals each station operation answers with.

func (r refusal) VisitGetIdentityStationResponse(w http.ResponseWriter) error { return r.write(w) }
func (r refusal) VisitGetIdentityStationOperatorsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitPostIdentityStationOperatorsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitPostIdentityStationOperatorsBySessionIdEndResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitPostIdentityStationPairResponse(w http.ResponseWriter) error { return r.write(w) }
func (r refusal) VisitPostIdentityStationsResponse(w http.ResponseWriter) error    { return r.write(w) }
func (r refusal) VisitPutIdentityStationsByIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitPostIdentityStationsByIdPairingResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitPostIdentityStationsByIdRevokeResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitPostIdentityStationsByIdOperatorsBySessionIdEndResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitDeleteIdentityStationsByIdBackoffByUserIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitGetIdentityUsersByIdOperatorCredentialsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitDeleteIdentityUsersByIdPinResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitPutIdentityUsersByIdBadgeResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitDeleteIdentityUsersByIdBadgeResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitPutIdentityAccountPinResponse(w http.ResponseWriter) error { return r.write(w) }
