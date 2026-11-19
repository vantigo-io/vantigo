package staff

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// brandTargets is this module's contracts.BrandTargets (brands design B9): the
// legal entities, and the ones a user works under today, read by identity to
// assign a brand. It authorizes nothing — identity decides before it calls —
// and fails rather than under-reports.
type brandTargets struct {
	q        *store.Queries
	calendar contracts.BusinessCalendar
	clock    func() time.Time
}

var _ contracts.BrandTargets = (*brandTargets)(nil)

// newBrandTargets is Module's BrandTargets. It is built after the business
// calendar (module.withBrandTargets), whose zone decides "today".
func newBrandTargets(d module.Deps) contracts.BrandTargets {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	return &brandTargets{q: store.New(d.Pool), calendar: d.Calendar, clock: clock}
}

// LegalEntities is every legal entity by name, archived ones marked.
func (b *brandTargets) LegalEntities(ctx context.Context) ([]contracts.LegalEntityTarget, error) {
	rows, err := b.q.BrandLegalEntities(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: read the legal entities: %w", err)
	}
	out := make([]contracts.LegalEntityTarget, 0, len(rows))
	for _, r := range rows {
		out = append(out, contracts.LegalEntityTarget{ID: strconv.FormatInt(r.ID, 10), Name: r.Name, Archived: r.Archived})
	}
	return out, nil
}

// LegalEntitiesOf is, for each user id, the legal entities of the employments
// in force today (a calendar day in the business time zone).
func (b *brandTargets) LegalEntitiesOf(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID][]string, error) {
	out := map[uuid.UUID][]string{}
	if len(userIDs) == 0 {
		return out, nil
	}
	zone, err := b.calendar.Zone(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: read the business time zone: %w", err)
	}
	ids := make([]string, 0, len(userIDs))
	for _, id := range userIDs {
		ids = append(ids, id.String())
	}
	rows, err := b.q.BrandLegalEntitiesOfUsers(ctx, store.BrandLegalEntitiesOfUsersParams{
		UserIds: ids, Today: dayParam(civilToday(b.clock(), zone)),
	})
	if err != nil {
		return nil, fmt.Errorf("staff: read the legal entities of users: %w", err)
	}
	for _, r := range rows {
		id, err := uuid.Parse(r.UserID)
		if err != nil {
			continue // an identity link whose external id is not a user id names no one
		}
		out[id] = append(out[id], strconv.FormatInt(r.LegalEntityID, 10))
	}
	return out, nil
}
