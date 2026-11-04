package google

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is the flow's first two steps (G26): connect, which seals a
// fresh state, nonce and PKCE verifier with the session into the state
// cookie and answers Google's authorization URL; and the anonymous callback,
// which checks the state before anything else — without a valid one it
// writes nothing, anywhere — then redeems the code (personal_redeem.go) and
// seals the validated account into the connected cookie for complete.

// PostGoogleConnect starts connecting the caller's own Google account for a
// feature: 422 feature_unknown, 409 switch_off, no_account or account_taken
// (refused early here; complete still decides), else the state cookie set,
// any connected cookie left from an earlier connect cleared, and the
// authorization URL answered. (POST /api/v1/google/connect)
func (s *server) PostGoogleConnect(ctx context.Context, req gen.PostGoogleConnectRequestObject) (gen.PostGoogleConnectResponseObject, error) {
	p, err := requestPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	feature := ""
	var returnTo *string
	if req.Body != nil {
		feature, returnTo = string(req.Body.Feature), req.Body.ReturnTo
	}
	if _, ok := personalFeatures[feature]; !ok {
		return gen.PostGoogleConnect422ApplicationProblemPlusJSONResponse(problem(http.StatusUnprocessableEntity, codeFeatureUnknown,
			"Unknown feature", "The connect names no feature Vantigo offers.")), nil
	}
	conn, creds, err := readFooting(ctx, s.q)
	if err != nil {
		return nil, err
	}
	if !personalOffered(conn, creds) {
		return gen.PostGoogleConnect409ApplicationProblemPlusJSONResponse(switchOffProblem()), nil
	}
	account, err := s.certainAccount(ctx)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return gen.PostGoogleConnect409ApplicationProblemPlusJSONResponse(noAccountProblem()), nil
	}
	// held is the caller's own live connection, whose granted features this
	// connect asks again beside feature's, so the answer never depends on
	// Google answering the union of an incremental grant (U9).
	var held []string
	existing, err := s.q.GetPersonal(ctx, account.UserID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, fmt.Errorf("google: read the personal connection: %w", err)
	case existing.State != personalClosing && existing.UserID != p.UserID:
		return gen.PostGoogleConnect409ApplicationProblemPlusJSONResponse(accountTakenProblem()), nil
	case existing.State != personalClosing:
		held = existing.Scopes
	}

	mac, err := s.sessionMAC(p.SessionID)
	if err != nil {
		return nil, err
	}
	scopes := connectScopes(feature, held)
	state := personalStateBody{
		State:      randomValue(),
		Nonce:      randomValue(),
		Verifier:   oauth2.GenerateVerifier(),
		UserID:     p.UserID,
		SessionMAC: mac,
		Feature:    feature,
		Scopes:     scopes,
		ReturnTo:   safeReturnTo(deref(returnTo)),
		ExpiresAt:  s.keys.c.now().Add(personalStateLifetime),
	}
	sealed, err := s.sealPersonal(personalStatePurpose, state)
	if err != nil {
		return nil, err
	}

	q := url.Values{
		"client_id":              {*conn.OauthClientID},
		"response_type":          {"code"},
		"redirect_uri":           {personalRedirectURI(s.deps.Config)},
		"scope":                  {strings.Join(scopes, " ")},
		"state":                  {state.State},
		"nonce":                  {state.Nonce},
		"code_challenge":         {oauth2.S256ChallengeFromVerifier(state.Verifier)},
		"code_challenge_method":  {"S256"},
		"access_type":            {"offline"},
		"prompt":                 {"consent"},
		"include_granted_scopes": {"true"},
		"login_hint":             {account.PrimaryEmail},
	}
	if domain := emailDomain(account.PrimaryEmail); domain != "" {
		q.Set("hd", domain)
	}
	if err := setPersonalCookie(ctx, s.personalCookie(personalStateCookie, sealed, personalStateLifetime)); err != nil {
		return nil, err
	}
	// A connected cookie an abandoned connect left behind can never complete
	// after this one starts.
	s.clearPersonalCookie(ctx, personalConnectedCookie)
	return gen.PostGoogleConnect200JSONResponse(gen.GooglePersonalAuthorize{AuthorizeUrl: authorizeEndpoint + "?" + q.Encode()}), nil
}

// switchOffProblem is the 409 while Connection for people is off, or the web
// client is not set up to connect with.
func switchOffProblem() gen.GoogleProblem {
	return problem(http.StatusConflict, codeSwitchOff,
		"Google connection off", "Connecting a Google account is not turned on for this installation.")
}

// noAccountProblem is the 409 for a caller without a certain account.
func noAccountProblem() gen.GoogleProblem {
	return problem(http.StatusConflict, codeNoAccount,
		"No Google account", "Vantigo knows no Google Workspace account of yours that it manages.")
}

// accountTakenProblem is the 409 for an account another person's live
// connection holds.
func accountTakenProblem() gen.GoogleProblem {
	return problem(http.StatusConflict, codeAccountTaken,
		"Account connected by someone else", "Another person's Google connection holds this Google account.")
}

// GetGoogleConnectCallback reads Google's answer to a connect. Every answer
// is a 302 to the Connections page and clears the state cookie; only a
// validated callback seals the connected cookie or records an outcome on the
// connection. (GET /api/v1/google/connect/callback)
func (s *server) GetGoogleConnectCallback(ctx context.Context, req gen.GetGoogleConnectCallbackRequestObject) (gen.GetGoogleConnectCallbackResponseObject, error) {
	// The state cookie is spent on every answer, a 500 included; deferred,
	// so a connected cookie set below comes first in the answer.
	defer s.clearPersonalCookie(ctx, personalStateCookie)
	if w, ok := contracts.ResponseWriterFrom(ctx); ok {
		w.Header().Set("Cache-Control", "no-store")
	}
	code, reason, err := s.personalCallback(ctx, req.Params)
	if err != nil {
		return nil, err
	}
	location := basePath(s.deps.Config) + personalPagePath + "?connection=google&outcome=complete"
	if code != "" {
		s.keys.c.logger.InfoContext(ctx, "google: a personal connect refused", "code", code, "reason", reason)
		location = basePath(s.deps.Config) + personalPagePath + "?connection=google&outcome=failed&code=" + code
	}
	return gen.GetGoogleConnectCallback302Response{Headers: gen.GetGoogleConnectCallback302ResponseHeaders{Location: &location}}, nil
}

// personalCallback is the callback's steps in order: the outcome's code (""
// for success) and the reason to log, or an error for a fault. Nothing is
// written before the state has been checked.
func (s *server) personalCallback(ctx context.Context, params gen.GetGoogleConnectCallbackParams) (string, string, error) {
	var state personalStateBody
	if !s.openPersonalCookie(ctx, personalStateCookie, personalStatePurpose, &state) {
		return codeExpired, "no valid state cookie", nil
	}
	if params.State == nil || subtle.ConstantTimeCompare([]byte(*params.State), []byte(state.State)) != 1 {
		return codeExpired, "the state does not match the cookie's", nil
	}
	if e := deref(params.Error); e != "" {
		if e == "access_denied" {
			return personalAccessDenied, "Google answered access_denied", nil
		}
		return personalFailed, "Google answered an error", nil
	}
	if deref(params.Code) == "" {
		return personalFailed, "Google's answer carries no code", nil
	}

	conn, creds, err := readFooting(ctx, s.q)
	if err != nil {
		return "", "", err
	}
	secret := liveSecret(creds)
	if !personalOffered(conn, creds) || secret == nil {
		return personalFailed, "the switch is off or the web client not set up", nil
	}

	got, code, reason, err := s.redeemPersonalCode(ctx, conn, *secret, state, *params.Code)
	if err != nil || code != "" {
		return code, reason, err
	}
	if !hasScopes(got.scopes, personalFeatures[state.Feature]) {
		if err := s.q.SetPersonalLastError(ctx, store.SetPersonalLastErrorParams{Code: ptr(personalConsentMissing), Now: s.keys.c.now()}); err != nil {
			return "", "", fmt.Errorf("google: record consent_missing: %w", err)
		}
		return personalConsentMissing, "the granted scope lacks the feature's", nil
	}

	sealed, err := s.sealPersonal(personalConnectedPurpose, personalConnectedBody{
		UserID:       state.UserID,
		SessionMAC:   state.SessionMAC,
		Sub:          got.sub,
		Email:        got.email,
		Scopes:       got.scopes,
		RefreshToken: got.refresh,
		Feature:      state.Feature,
		ReturnTo:     state.ReturnTo,
		ExpiresAt:    s.keys.c.now().Add(personalConnectedLifetime),
	})
	if err != nil {
		return "", "", err
	}
	if len(sealed) > maxConnectedCookie {
		s.keys.c.logger.WarnContext(ctx, "google: the connect's cookie is too large for a browser", "bytes", len(sealed), "limit", maxConnectedCookie)
		return personalFailed, "the connected cookie is too large", nil
	}
	if err := setPersonalCookie(ctx, s.personalCookie(personalConnectedCookie, sealed, personalConnectedLifetime)); err != nil {
		return "", "", err
	}
	return "", "", nil
}

// domainAllowed reports whether hd is one of the organisation's verified
// domains, ignoring case.
func domainAllowed(hd string, domains []string) bool {
	return hd != "" && slices.ContainsFunc(domains, func(d string) bool { return strings.EqualFold(d, hd) })
}

// ptr is v's address.
func ptr[T any](v T) *T { return &v }
