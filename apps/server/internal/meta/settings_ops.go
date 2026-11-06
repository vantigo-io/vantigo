package meta

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/meta/gen"
	"github.com/vantigo-io/vantigo/server/internal/meta/store"
)

// The settings' figures.
const (
	maxAppID     = 40
	maxAppSecret = 200
	// verifyTokenBytes is the verify token's randomness: 32 bytes, 64 hex characters.
	verifyTokenBytes = 32
)

// appIDPattern is a Meta app id: digits.
var appIDPattern = regexp.MustCompile(`^[0-9]{1,40}$`)

// GetMetaSettings answers the settings. (GET /api/v1/meta/settings)
func (s *server) GetMetaSettings(ctx context.Context, _ gen.GetMetaSettingsRequestObject) (gen.GetMetaSettingsResponseObject, error) {
	body, err := s.settingsBody(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetMetaSettings200JSONResponse(body), nil
}

// PutMetaSettings sets the app id, the secret and the Human Agent switch.
// (PUT /api/v1/meta/settings)
func (s *server) PutMetaSettings(ctx context.Context, req gen.PutMetaSettingsRequestObject) (gen.PutMetaSettingsResponseObject, error) {
	var in gen.MetaSettingsRequest
	if req.Body != nil {
		in = *req.Body
	}
	var f fieldErrors
	var appID *string
	if in.AppId != nil {
		id := strings.TrimSpace(*in.AppId)
		if !appIDPattern.MatchString(id) {
			f.add("appId", "The app id is the number Meta shows for the app, digits only.")
		}
		appID = &id
	}
	var secret []byte
	if in.AppSecret != nil {
		v := strings.TrimSpace(*in.AppSecret)
		if utf8.RuneCountInString(v) > maxAppSecret {
			f.add("appSecret", fmt.Sprintf("The app secret is at most %d characters.", maxAppSecret))
		}
		if v != "" {
			secret = []byte(v)
		}
	}
	if f.failed() {
		return gen.PutMetaSettings400ApplicationProblemPlusJSONResponse(invalid("Invalid settings", f)), nil
	}
	if s.deps.Config == nil || s.deps.Config.PublicBaseURL == "" {
		return gen.PutMetaSettings422ApplicationProblemPlusJSONResponse(problem(422, codePublicHostRequired,
			"The instance has no public host",
			"Meta posts to the public host, so the settings cannot be saved until PUBLIC_BASE_URL is set.")), nil
	}
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	params := store.UpdateSettingsParams{
		AppID: appID, HumanAgentGranted: in.HumanAgentGranted, Now: s.now(), UpdatedBy: &actor,
	}
	if secret != nil {
		sealed, err := s.deps.Secrets.Seal(purposeAppSecret, secret)
		if err != nil {
			return nil, fmt.Errorf("meta: seal the app secret: %w", err)
		}
		params.AppSecretSealed = sealed
	}
	if _, err := s.q.UpdateSettings(ctx, params); err != nil {
		return nil, fmt.Errorf("meta: update the settings: %w", err)
	}
	body, err := s.settingsBody(ctx)
	if err != nil {
		return nil, err
	}
	return gen.PutMetaSettings200JSONResponse(body), nil
}

// PostMetaSettingsVerifyToken replaces the verify token.
// (POST /api/v1/meta/settings/verify-token)
func (s *server) PostMetaSettingsVerifyToken(ctx context.Context, _ gen.PostMetaSettingsVerifyTokenRequestObject) (gen.PostMetaSettingsVerifyTokenResponseObject, error) {
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	raw := make([]byte, verifyTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("meta: generate a verify token: %w", err)
	}
	if _, err := s.q.SetVerifyToken(ctx, store.SetVerifyTokenParams{VerifyToken: hex.EncodeToString(raw), Now: s.now(), UpdatedBy: &actor}); err != nil {
		return nil, fmt.Errorf("meta: store the verify token: %w", err)
	}
	body, err := s.settingsBody(ctx)
	if err != nil {
		return nil, err
	}
	return gen.PostMetaSettingsVerifyToken200JSONResponse(body), nil
}

// settingsBody reads the settings and the Pages into the contract's shape.
func (s *server) settingsBody(ctx context.Context) (gen.MetaSettings, error) {
	row, err := s.settings(ctx)
	if err != nil {
		return gen.MetaSettings{}, err
	}
	pages, err := s.q.ListPages(ctx)
	if err != nil {
		return gen.MetaSettings{}, fmt.Errorf("meta: list the Pages: %w", err)
	}
	body := gen.MetaSettings{
		AppId:             row.AppID,
		AppSecretSet:      len(row.AppSecretSealed) > 0,
		HumanAgentGranted: row.HumanAgentGranted,
		Pages:             make([]gen.MetaPage, 0, len(pages)),
		VerifyToken:       row.VerifyToken,
		Status: gen.MetaStatus{
			LastGraphErrorAt: row.LastGraphErrorAt, LastGraphErrorCode: row.LastGraphErrorCode,
			LastGraphErrorMessage: row.LastGraphError, LastRejectedAt: row.LastRejectedAt, LastWebhookAt: row.LastWebhookAt,
		},
	}
	if s.deps.Config != nil && s.deps.Config.PublicBaseURL != "" {
		u := s.deps.Config.PublicBaseURL + webhookPath
		body.PublicHostConfigured, body.WebhookUrl = true, &u
	}
	for _, p := range pages {
		body.Pages = append(body.Pages, pageBody(p))
	}
	return body, nil
}

// pageBody is a Page in the contract's shape.
func pageBody(p store.MetaPage) gen.MetaPage {
	return gen.MetaPage{
		AddedAt: p.AddedAt, CheckedAt: p.CheckedAt, LastError: p.LastError,
		Name: p.Name, PageId: p.PageID, State: gen.MetaPageState(p.State),
	}
}
