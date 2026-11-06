package meta

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/meta/gen"
	"github.com/vantigo-io/vantigo/server/internal/meta/store"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// A Page's states in meta.pages.state.
const (
	statePageActive   = "active"
	statePageRejected = "token_rejected"
)

// pageIDPattern is a Page's id: digits.
var pageIDPattern = regexp.MustCompile(`^[0-9]{1,30}$`)

// subscribedFields are the webhook fields the app subscribes a Page to.
const subscribedFields = "messages,messaging_postbacks,message_reads,message_deliveries,message_echoes"

// pageAnswer is what GET /me answers for a Page access token.
type pageAnswer struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Tasks []string `json:"tasks"`
}

// PostMetaPages connects a Page from its access token. (POST /api/v1/meta/pages)
func (s *server) PostMetaPages(ctx context.Context, req gen.PostMetaPagesRequestObject) (gen.PostMetaPagesResponseObject, error) {
	token := ""
	if req.Body != nil {
		token = strings.TrimSpace(req.Body.AccessToken)
	}
	if token == "" {
		var f fieldErrors
		f.add("accessToken", "Paste the Page's access token.")
		return gen.PostMetaPages400ApplicationProblemPlusJSONResponse(invalid("Invalid Page", f)), nil
	}
	settings, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	if !configured(settings) {
		return gen.PostMetaPages422ApplicationProblemPlusJSONResponse(problem(422, codeNotConfigured,
			"The Meta app is not configured",
			"Set the app id and the app secret before adding a Page.")), nil
	}
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}

	var me pageAnswer
	if err := s.graph.get(ctx, token, "/me", url.Values{"fields": {"id,name,tasks"}}, &me); err != nil {
		return pageRefusal(err), nil
	}
	if !pageIDPattern.MatchString(me.ID) || !slices.Contains(me.Tasks, "MESSAGING") {
		return gen.PostMetaPages422ApplicationProblemPlusJSONResponse(insufficientToken()), nil
	}
	err = s.graph.postForm(ctx, token, pagePath(me.ID, "/subscribed_apps"), url.Values{"subscribed_fields": {subscribedFields}}, nil)
	if err != nil {
		return pageRefusal(err), nil
	}

	sealed, err := s.deps.Secrets.Seal(purposePageToken, []byte(token))
	if err != nil {
		return nil, fmt.Errorf("meta: seal a Page's token: %w", err)
	}
	name := strings.TrimSpace(me.Name)
	if name == "" {
		name = me.ID
	}
	row, err := s.q.UpsertPage(ctx, store.UpsertPageParams{PageID: me.ID, Name: name, TokenSealed: sealed, Now: s.now(), AddedBy: &actor})
	if err != nil {
		return nil, fmt.Errorf("meta: keep a Page: %w", err)
	}
	return gen.PostMetaPages201JSONResponse(pageBody(row)), nil
}

// insufficientToken is the 422 for a token that is not a Page's, or may not
// message it.
func insufficientToken() gen.MetaProblem {
	return problem(422, codePageTokenInsufficient, "The token cannot message the Page",
		"Use a Page access token, or a system user's token with the Page's Messenger task, from Meta Business Suite.")
}

// pageRefusal is the response to a failed call to Meta while a Page is added:
// 503 when Meta could not be asked or asked again later, 422 when it answered
// that the token is not good enough.
func pageRefusal(err error) gen.PostMetaPagesResponseObject {
	var ge graphError
	if isTemporaryGraph(err) || !errors.As(err, &ge) {
		return gen.PostMetaPages503ApplicationProblemPlusJSONResponse(problem(503, codeMetaUnreachable,
			"Meta could not be reached", "Meta did not answer; try again in a moment."))
	}
	return gen.PostMetaPages422ApplicationProblemPlusJSONResponse(insufficientToken())
}

// DeleteMetaPagesByPageId removes a Page. (DELETE /api/v1/meta/pages/{pageId})
func (s *server) DeleteMetaPagesByPageId(ctx context.Context, req gen.DeleteMetaPagesByPageIdRequestObject) (gen.DeleteMetaPagesByPageIdResponseObject, error) {
	page, err := s.q.GetPage(ctx, req.PageId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteMetaPagesByPageId404ApplicationProblemPlusJSONResponse(problem(404, codePageNotFound,
			"The Page is not connected", "No Page with that id is connected.")), nil
	}
	if err != nil {
		return nil, fmt.Errorf("meta: read the Page: %w", err)
	}

	// Withdrawing the subscription is best effort: a rejected token, or Meta
	// down, must not keep a Page from being removed.
	if token, err := s.pageToken(page); err == nil {
		if err := s.graph.delete(ctx, token, pagePath(page.PageID, "/subscribed_apps"), nil); err != nil {
			s.logger.WarnContext(ctx, "meta: a Page's subscription could not be withdrawn", "page", page.PageID)
		}
	}

	var keys []string
	err = s.inTx(ctx, func(q *store.Queries) error {
		rows, err := q.DeleteInboundForPage(ctx, page.PageID)
		if err != nil {
			return fmt.Errorf("meta: delete a Page's queued events: %w", err)
		}
		for _, r := range rows {
			keys = append(keys, r...)
		}
		if err := q.DeleteProfilesForPage(ctx, page.PageID); err != nil {
			return fmt.Errorf("meta: delete a Page's profiles: %w", err)
		}
		if _, err := q.DeletePage(ctx, page.PageID); err != nil {
			return fmt.Errorf("meta: delete the Page: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(keys) > 0 {
		objects, err := inboundStore(s.deps)
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			if err := objects.Delete(ctx, k); err != nil && !errors.Is(err, storage.ErrNotExist) {
				s.logger.WarnContext(ctx, "meta: a removed Page's file could not be deleted", "err", err)
			}
		}
	}
	return gen.DeleteMetaPagesByPageId204Response{}, nil
}

// inTx runs fn in one transaction on the module's pool.
func (c core) inTx(ctx context.Context, fn func(q *store.Queries) error) error {
	tx, err := c.deps.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("meta: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(c.q.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("meta: commit: %w", err)
	}
	return nil
}

// Endpoints is the Pages connected, each with its own status.
func (m *messenger) Endpoints(ctx context.Context) ([]contracts.MessengerEndpoint, error) {
	if err := m.requireConfigured(ctx); err != nil {
		return nil, err
	}
	pages, err := m.q.ListPages(ctx)
	if err != nil {
		return nil, fmt.Errorf("meta: list the Pages: %w", err)
	}
	out := make([]contracts.MessengerEndpoint, 0, len(pages))
	for _, p := range pages {
		out = append(out, endpointOf(p))
	}
	return out, nil
}

// endpointOf is a Page as the provider's Endpoints answer it.
func endpointOf(p store.MetaPage) contracts.MessengerEndpoint {
	e := contracts.MessengerEndpoint{Address: p.PageID, DisplayName: p.Name, Available: p.State == statePageActive}
	if p.State == statePageRejected {
		e.Reason = reasonTokenRejected
	}
	return e
}

// requireConfigured answers ErrMessengerUnavailable (wrapped) while the app id
// or secret is not stored.
func (m *messenger) requireConfigured(ctx context.Context) error {
	s, err := m.settings(ctx)
	if err != nil {
		return err
	}
	if !configured(s) {
		return fmt.Errorf("%w: %s", contracts.ErrMessengerUnavailable, reasonNotConfigured)
	}
	return nil
}

// Check resolves a Page's id to a connected Page, with a live read of the Page
// at Meta: its name refreshed, or its token found rejected.
func (m *messenger) Check(ctx context.Context, address string) (contracts.MessengerEndpoint, error) {
	if err := m.requireConfigured(ctx); err != nil {
		return contracts.MessengerEndpoint{}, err
	}
	page, err := m.q.GetPage(ctx, address)
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.MessengerEndpoint{}, contracts.ErrMessengerEndpointUnknown
	}
	if err != nil {
		return contracts.MessengerEndpoint{}, fmt.Errorf("meta: read the Page: %w", err)
	}
	token, err := m.pageToken(page)
	if err != nil {
		return contracts.MessengerEndpoint{}, err
	}
	var me pageAnswer
	if err := m.graph.get(ctx, token, "/me", url.Values{"fields": {"id,name"}}, &me); err != nil {
		var ge graphError
		if errors.As(err, &ge) && ge.Code == codeTokenRejected {
			m.markRejected(ctx, page.PageID)
			page.State = statePageRejected
			return endpointOf(page), nil
		}
		return contracts.MessengerEndpoint{}, fmt.Errorf("meta: check the Page: %w", err)
	}
	now := m.now()
	if name := strings.TrimSpace(me.Name); name != "" && name != page.Name {
		if err := m.q.SetPageName(ctx, store.SetPageNameParams{Name: name, Now: &now, PageID: page.PageID}); err != nil {
			return contracts.MessengerEndpoint{}, fmt.Errorf("meta: keep the Page's name: %w", err)
		}
		page.Name = name
	}
	if err := m.q.SetPageState(ctx, store.SetPageStateParams{State: statePageActive, Now: &now, PageID: page.PageID}); err != nil {
		return contracts.MessengerEndpoint{}, fmt.Errorf("meta: keep the Page's state: %w", err)
	}
	page.State = statePageActive
	return endpointOf(page), nil
}
