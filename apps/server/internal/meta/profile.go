package meta

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/meta/store"
)

// profileMaxAge is how long a fetched name is kept before Meta is asked again
// (Messenger design N12a).
const profileMaxAge = 30 * 24 * time.Hour

// profileAnswer is what Meta knows of a person who wrote to the Page.
type profileAnswer struct {
	Name      string `json:"name"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// Profile is what Meta knows of one of the Page's participants: a display
// name, cached for thirty days. Meta answers no name for a person who has not
// let the Page see it, and the profile is then empty and not cached.
func (m *messenger) Profile(ctx context.Context, address, participant string) (contracts.MessengerProfile, error) {
	page, err := m.q.GetPage(ctx, address)
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.MessengerProfile{}, contracts.ErrMessengerEndpointUnknown
	}
	if err != nil {
		return contracts.MessengerProfile{}, fmt.Errorf("meta: read the Page: %w", err)
	}
	cached, err := m.q.GetProfile(ctx, store.GetProfileParams{PageID: page.PageID, Psid: participant})
	switch {
	case err == nil && m.now().Sub(cached.FetchedAt) < profileMaxAge:
		return contracts.MessengerProfile{DisplayName: cached.DisplayName}, nil
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return contracts.MessengerProfile{}, fmt.Errorf("meta: read a profile: %w", err)
	}
	if page.State != statePageActive {
		return contracts.MessengerProfile{}, fmt.Errorf("%w: %s", contracts.ErrMessengerUnavailable, reasonTokenRejected)
	}
	token, err := m.pageToken(page)
	if err != nil {
		return contracts.MessengerProfile{}, err
	}

	var answer profileAnswer
	query := url.Values{"fields": {"name,first_name,last_name"}}
	if err := m.graph.get(ctx, token, pagePath(participant), query, &answer); err != nil {
		var ge graphError
		if errors.As(err, &ge) && ge.Code == codeTokenRejected {
			m.markRejected(ctx, page.PageID)
		}
		return contracts.MessengerProfile{}, fmt.Errorf("meta: read a participant's profile: %w", err)
	}
	name := strings.TrimSpace(answer.Name)
	if name == "" {
		name = strings.TrimSpace(answer.FirstName + " " + answer.LastName)
	}
	if name == "" {
		return contracts.MessengerProfile{}, nil
	}
	err = m.q.UpsertProfile(ctx, store.UpsertProfileParams{PageID: page.PageID, Psid: participant, DisplayName: name, Now: m.now()})
	if err != nil {
		return contracts.MessengerProfile{}, fmt.Errorf("meta: keep a profile: %w", err)
	}
	return contracts.MessengerProfile{DisplayName: name}, nil
}
