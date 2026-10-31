package entra

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
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the flow's first two steps (Outlook connection design M4):
// connect, which seals a fresh state, nonce and PKCE verifier with the
// session into the state cookie and answers Microsoft's authorization URL;
// and the anonymous callback, which checks the state before anything else —
// without a valid one it writes nothing, anywhere — then redeems the code
// (outlook_redeem.go) and seals the validated account into the connected
// cookie for complete.

// PostEntraOutlookConnect starts connecting the caller's Outlook for a
// feature: 422 feature_unknown, 409 switch_off, no_account or account_taken
// (refused early here; complete still decides), else the state cookie set,
// any connected cookie left from an earlier connect cleared, and the
// authorization URL answered. (POST /api/v1/entra/outlook/connect)
func (s *server) PostEntraOutlookConnect(ctx context.Context, req gen.PostEntraOutlookConnectRequestObject) (gen.PostEntraOutlookConnectResponseObject, error) {
	p, err := requestPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	feature := ""
	var returnTo *string
	if req.Body != nil {
		feature, returnTo = string(req.Body.Feature), req.Body.ReturnTo
	}
	scopes, ok := outlookFeatures[feature]
	if !ok {
		return gen.PostEntraOutlookConnect422ApplicationProblemPlusJSONResponse(problem(http.StatusUnprocessableEntity, codeFeatureUnknown,
			"Unknown feature", "The connect names no feature Vantigo offers.")), nil
	}
	conn, err := s.q.GetConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("entra: read the connection: %w", err)
	}
	if !conn.OutlookConnect || conn.TenantID == nil || conn.ClientID == nil {
		return gen.PostEntraOutlookConnect409ApplicationProblemPlusJSONResponse(switchOffProblem()), nil
	}
	account, err := s.certainAccount(ctx)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return gen.PostEntraOutlookConnect409ApplicationProblemPlusJSONResponse(noAccountProblem()), nil
	}
	// held is the caller's own live connection, whose granted features this
	// connect asks again beside feature's (design M20): Microsoft answers
	// the scopes it consents to as a whole, so a connect that named mail
	// alone would come back without calendar.
	existing, err := s.q.GetOutlookConnection(ctx, account.ObjectID)
	held := false
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, fmt.Errorf("entra: read the Outlook connection: %w", err)
	case existing.State != outlookClosing && existing.UserID != p.UserID:
		return gen.PostEntraOutlookConnect409ApplicationProblemPlusJSONResponse(accountTakenProblem()), nil
	default:
		held = existing.State != outlookClosing
	}

	mac, err := s.sessionMAC(p.SessionID)
	if err != nil {
		return nil, err
	}
	requestedScopes := slices.Clone(scopes)
	if held {
		for _, scope := range grantedFeatureScopes(existing.Scopes) {
			if !slices.Contains(requestedScopes, scope) {
				requestedScopes = append(requestedScopes, scope)
			}
		}
	}
	state := outlookStateBody{
		State:      randomOutlookValue(),
		Nonce:      randomOutlookValue(),
		Verifier:   oauth2.GenerateVerifier(),
		UserID:     p.UserID,
		SessionMAC: mac,
		Feature:    feature,
		Scopes:     requestedScopes,
		ReturnTo:   safeReturnTo(deref(returnTo)),
		ExpiresAt:  s.client.now().Add(outlookStateLifetime),
	}
	sealed, err := s.sealOutlook(outlookStatePurpose, state)
	if err != nil {
		return nil, err
	}

	q := url.Values{
		"client_id":             {conn.ClientID.String()},
		"response_type":         {"code"},
		"response_mode":         {"query"},
		"redirect_uri":          {outlookRedirectURI(s.deps.Config)},
		"scope":                 {outlookScopeWith(requestedScopes)},
		"state":                 {state.State},
		"nonce":                 {state.Nonce},
		"code_challenge":        {oauth2.S256ChallengeFromVerifier(state.Verifier)},
		"code_challenge_method": {"S256"},
		"login_hint":            {account.UserPrincipalName},
		"claims":                {outlookClaims},
	}
	if domain := upnDomain(account.UserPrincipalName); domain != "" && slices.ContainsFunc(conn.VerifiedDomains, func(d string) bool { return strings.EqualFold(d, domain) }) {
		q.Set("domain_hint", domain)
	}
	if err := setOutlookCookie(ctx, s.outlookCookie(outlookStateCookie, sealed, outlookStateLifetime)); err != nil {
		return nil, err
	}
	// A connected cookie an abandoned connect left behind can never
	// complete after this one starts.
	s.clearOutlookCookie(ctx, outlookConnectedCookie)
	authorize := fmt.Sprintf(outlookAuthorizeEndpoint, conn.TenantID.String()) + "?" + q.Encode()
	return gen.PostEntraOutlookConnect200JSONResponse(gen.EntraOutlookAuthorize{AuthorizeUrl: authorize}), nil
}

// upnDomain is the domain of a user principal name, "" without one.
func upnDomain(upn string) string {
	_, domain, ok := strings.Cut(upn, "@")
	if !ok {
		return ""
	}
	return domain
}

// switchOffProblem is the 409 while the Outlook connection switch is off,
// or the connection is not set up to connect with.
func switchOffProblem() gen.EntraProblem {
	return problem(http.StatusConflict, codeSwitchOff,
		"Outlook connection off", "Connecting Outlook is not turned on for this installation.")
}

// noAccountProblem is the 409 for a caller without a certain account.
func noAccountProblem() gen.EntraProblem {
	return problem(http.StatusConflict, codeNoAccount,
		"No Microsoft account", "Vantigo knows no Microsoft Entra account of yours that it manages.")
}

// accountTakenProblem is the 409 for an account another person's live
// connection holds.
func accountTakenProblem() gen.EntraProblem {
	return problem(http.StatusConflict, codeAccountTaken,
		"Account connected by someone else", "Another person's Outlook connection holds this Microsoft account.")
}

// GetEntraOutlookCallback reads Microsoft's answer to a connect. Every
// answer is a 302 to the Connections page and clears the state cookie;
// only a validated callback seals the connected cookie or records an
// outcome on the connection. (GET /api/v1/entra/outlook/callback)
func (s *server) GetEntraOutlookCallback(ctx context.Context, req gen.GetEntraOutlookCallbackRequestObject) (gen.GetEntraOutlookCallbackResponseObject, error) {
	// The state cookie is spent on every answer, a 500 included; deferred,
	// so a connected cookie set below comes first in the answer.
	defer s.clearOutlookCookie(ctx, outlookStateCookie)
	if w, ok := contracts.ResponseWriterFrom(ctx); ok {
		w.Header().Set("Cache-Control", "no-store")
	}
	code, reason, err := s.outlookCallback(ctx, req.Params)
	if err != nil {
		return nil, err
	}
	location := basePath(s.deps.Config) + outlookPagePath + "?outlook=complete"
	if code != "" {
		s.client.logger.InfoContext(ctx, "entra: an Outlook connect refused", "code", code, "reason", reason)
		location = basePath(s.deps.Config) + outlookPagePath + "?outlook=failed&code=" + code
	}
	return gen.GetEntraOutlookCallback302Response{Headers: gen.GetEntraOutlookCallback302ResponseHeaders{Location: &location}}, nil
}

// outlookCallback is the callback's steps in order (plan reading 12): the
// outcome's code ("" for success) and the reason to log, or an error for a
// fault. Nothing is written before the state has been checked.
func (s *server) outlookCallback(ctx context.Context, params gen.GetEntraOutlookCallbackParams) (string, string, error) {
	var state outlookStateBody
	if !s.openOutlookCookie(ctx, outlookStateCookie, outlookStatePurpose, &state) {
		return codeExpired, "no valid state cookie", nil
	}
	if params.State == nil || subtle.ConstantTimeCompare([]byte(*params.State), []byte(state.State)) != 1 {
		return codeExpired, "the state does not match the cookie's", nil
	}
	if e := deref(params.Error); e != "" {
		if e == "access_denied" {
			return outlookAccessDenied, "Microsoft answered access_denied", nil
		}
		return outlookFailed, "Microsoft answered an error", nil
	}
	if deref(params.Code) == "" {
		return outlookFailed, "Microsoft's answer carries no code", nil
	}

	conn, err := s.q.GetConnection(ctx)
	if err != nil {
		return "", "", fmt.Errorf("entra: read the connection: %w", err)
	}
	if !conn.OutlookConnect || conn.TenantID == nil || conn.ClientID == nil {
		return outlookFailed, "the switch is off or the connection not set up", nil
	}
	cred, err := credentialFor(ctx, s.q, s.client)
	switch {
	case errors.Is(err, errNotConfigured), errors.Is(err, errNoCredential), errors.Is(err, errTenantUnreachable),
		errors.Is(err, errCredentialInvalid), errors.Is(err, errThrottled):
		return outlookFailed, "no client secret: " + reasonFor(err), nil
	case err != nil:
		return "", "", err
	}

	got, code, reason, err := s.redeemOutlookCode(ctx, conn, cred, state, *params.Code)
	if err != nil || code != "" {
		return code, reason, err
	}
	if !hasScopes(got.scopes, outlookFeatures[state.Feature]) {
		missing := outlookConsentMissing
		if err := s.q.SetOutlookLastError(ctx, store.SetOutlookLastErrorParams{Code: &missing, Now: s.client.now()}); err != nil {
			return "", "", fmt.Errorf("entra: record consent_missing: %w", err)
		}
		return outlookConsentMissing, "the granted scope lacks the feature's", nil
	}

	sealed, err := s.sealOutlook(outlookConnectedPurpose, outlookConnectedBody{
		UserID:       state.UserID,
		SessionMAC:   state.SessionMAC,
		OID:          got.oid,
		TID:          got.tid,
		UPN:          got.upn,
		Scopes:       got.scopes,
		RefreshToken: got.refresh,
		Feature:      state.Feature,
		ReturnTo:     state.ReturnTo,
		ExpiresAt:    s.client.now().Add(outlookConnectedLifetime),
	})
	if err != nil {
		return "", "", err
	}
	if !connectedCookieFits(sealed) {
		s.client.logger.WarnContext(ctx, "entra: the Outlook connect's cookie is too large for a browser", "bytes", len(sealed), "limit", maxConnectedCookie)
		return outlookFailed, "the connected cookie is too large", nil
	}
	if err := setOutlookCookie(ctx, s.outlookCookie(outlookConnectedCookie, sealed, outlookConnectedLifetime)); err != nil {
		return "", "", err
	}
	return "", "", nil
}
