package entra

import (
	"context"
	"fmt"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
)

// This file is the person's Outlook connection (Outlook connection and
// Microsoft 365 mail design M2–M8,
// docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md;
// delivery A, docs/superpowers/plans/2026-10-29-entra-outlook-connection.md):
// a delegated authorization code flow behind the Owner's Outlook connection
// switch, its rows in entra.outlook_connections. Until the flow lands the
// five operations answer as if nobody could connect: the switch is judged,
// nothing is sealed, nothing is written.

// The flow's paths: the redirect URI's, on APP_URL with the base path and
// never the public host, and the page every answer of the callback returns
// to.
const (
	outlookCallbackPath = "/api/v1/entra/outlook/callback"
	outlookPagePath     = "/settings/connections"
)

// The Outlook connection's refusal codes (the contract's EntraProblem).
const (
	codeSwitchOff = "switch_off"
	codeExpired   = "expired"
)

// outlookRedirectURI is the redirect URI to register on the app
// registration's Web platform: APP_URL, the base path and the callback.
// Empty without a configuration.
func outlookRedirectURI(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.AppOrigin + cfg.BasePath + outlookCallbackPath
}

// basePath is the configuration's base path, empty without one.
func basePath(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.BasePath
}

// PostEntraOutlookConnect would start a connect; until the flow lands it
// answers 409 switch_off. (POST /api/v1/entra/outlook/connect)
func (s *server) PostEntraOutlookConnect(_ context.Context, _ gen.PostEntraOutlookConnectRequestObject) (gen.PostEntraOutlookConnectResponseObject, error) {
	return gen.PostEntraOutlookConnect409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, codeSwitchOff,
		"Outlook connection off", "Connecting Outlook is not turned on for this installation.")), nil
}

// GetEntraOutlookCallback would read Microsoft's answer to a connect; until
// the flow lands it sends the browser back to the page with failed.
// (GET /api/v1/entra/outlook/callback)
func (s *server) GetEntraOutlookCallback(_ context.Context, _ gen.GetEntraOutlookCallbackRequestObject) (gen.GetEntraOutlookCallbackResponseObject, error) {
	location := basePath(s.deps.Config) + outlookPagePath + "?outlook=failed&code=failed"
	return gen.GetEntraOutlookCallback302Response{Headers: gen.GetEntraOutlookCallback302ResponseHeaders{Location: &location}}, nil
}

// PostEntraOutlookComplete would complete a connect; until the flow lands
// there is never a cookie to complete it with: 410 expired.
// (POST /api/v1/entra/outlook/complete)
func (s *server) PostEntraOutlookComplete(_ context.Context, _ gen.PostEntraOutlookCompleteRequestObject) (gen.PostEntraOutlookCompleteResponseObject, error) {
	return gen.PostEntraOutlookComplete410ApplicationProblemPlusJSONResponse(problem(http.StatusGone, codeExpired,
		"Connect expired", "The connect has expired or was started in another session. Connect Outlook again.")), nil
}

// GetEntraOutlookConnection answers the caller's own connection; until the
// flow lands nothing is offered: switch_off while the switch is, no_account
// otherwise. (GET /api/v1/entra/outlook/connection)
func (s *server) GetEntraOutlookConnection(ctx context.Context, _ gen.GetEntraOutlookConnectionRequestObject) (gen.GetEntraOutlookConnectionResponseObject, error) {
	conn, err := s.q.GetConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("entra: read the connection: %w", err)
	}
	reason := gen.NoAccount
	if !conn.OutlookConnect {
		reason = gen.SwitchOff
	}
	return gen.GetEntraOutlookConnection200JSONResponse(gen.EntraOutlookConnection{
		Offered:  false,
		Reason:   &reason,
		State:    gen.EntraOutlookConnectionStateNotConnected,
		Scopes:   []string{},
		Features: []string{},
	}), nil
}

// DeleteEntraOutlookConnection disconnects the caller's Outlook; until the
// flow lands there is nothing to close. 204 either way.
// (DELETE /api/v1/entra/outlook/connection)
func (s *server) DeleteEntraOutlookConnection(_ context.Context, _ gen.DeleteEntraOutlookConnectionRequestObject) (gen.DeleteEntraOutlookConnectionResponseObject, error) {
	return gen.DeleteEntraOutlookConnection204Response{}, nil
}
