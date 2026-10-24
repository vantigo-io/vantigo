package staff

import (
	"context"
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// workforce is this module's contracts.Workforce (staff competence design
// S15, §4 "The contract"; plan reading 7): the catalogue, and who holds
// what, valid on a date, read by another module — Staffing — through
// Deps.Workforce. It is its own gatekeeper (module-boundaries rule 12): the
// Colleague rule, staff:view and the person active today, applied to the
// principal in ctx through contracts.HasPermission — the request's caller,
// or the user contracts.AsUser names — and nothing, never an error, for
// anyone else. A context with neither answers nothing.
type workforce struct {
	q        *store.Queries
	access   contracts.Access
	calendar contracts.BusinessCalendar
	clock    func() time.Time
}

var _ contracts.Workforce = (*workforce)(nil)

// newWorkforce is Module's Workforce. It is built after the business
// calendar (module.withWorkforce), whose zone decides "today".
func newWorkforce(d module.Deps) contracts.Workforce {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	return &workforce{q: store.New(d.Pool), access: d.Access, calendar: d.Calendar, clock: clock}
}

// Competences is every competence not archived, hidden ones included, in the
// catalogue's order, for staff:view or staff:manage; nothing otherwise.
func (w *workforce) Competences(ctx context.Context) ([]contracts.CompetenceEntry, error) {
	if !contracts.HasPermission(ctx, w.access, permView) && !contracts.HasPermission(ctx, w.access, permManage) {
		return nil, nil
	}
	categories, err := w.q.ListCompetenceCategories(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: read the competence categories: %w", err)
	}
	keys := make(map[int64]string, len(categories))
	for _, c := range categories {
		keys[c.ID] = c.Key
	}
	rows, err := w.q.ListCompetences(ctx, store.ListCompetencesParams{IncludeHidden: true})
	if err != nil {
		return nil, fmt.Errorf("staff: read the competences: %w", err)
	}
	out := make([]contracts.CompetenceEntry, 0, len(rows))
	for _, c := range rows {
		out = append(out, contracts.CompetenceEntry{
			ID: c.ID, Key: c.Key, NameEn: c.NameEn, NameNb: c.NameNb, CategoryKey: keys[c.CategoryID],
			Kind: c.Kind, HasLevel: c.HasLevel, ValidityMonths: c.ValidityMonths, Hidden: c.Hidden,
		})
	}
	return out, nil
}

// HeldCompetences is, for staff:view, each of personIDs active today with
// the competences they hold valid on on: the current row, within its
// validity — a declared certificate not, a declared skill with Verified
// false. A person not active today is absent; so is one who holds nothing.
func (w *workforce) HeldCompetences(ctx context.Context, on time.Time, personIDs []int64) (map[int64][]contracts.HeldCompetence, error) {
	if !contracts.HasPermission(ctx, w.access, permView) {
		return nil, nil
	}
	zone, err := w.calendar.Zone(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: read the business time zone: %w", err)
	}
	activeIDs, err := w.q.PeopleActiveOn(ctx, dayParam(civilToday(w.clock(), zone)))
	if err != nil {
		return nil, fmt.Errorf("staff: read the people active today: %w", err)
	}
	active := make(map[int64]bool, len(activeIDs))
	for _, id := range activeIDs {
		active[id] = true
	}
	asked := make([]int64, 0, len(personIDs))
	for _, id := range personIDs {
		if active[id] {
			asked = append(asked, id)
		}
	}
	out := map[int64][]contracts.HeldCompetence{}
	if len(asked) == 0 {
		return out, nil
	}
	day := time.Date(on.Year(), on.Month(), on.Day(), 0, 0, 0, 0, time.UTC)
	rows, err := w.q.HeldCompetencesOn(ctx, store.HeldCompetencesOnParams{PersonIds: asked, Day: dayParam(day)})
	if err != nil {
		return nil, fmt.Errorf("staff: read the competences held: %w", err)
	}
	for _, r := range rows {
		verified := r.VerifiedAt != nil
		if !verified && r.CompetenceKind == "certificate" {
			continue
		}
		held := contracts.HeldCompetence{
			CompetenceID: r.CompetenceID, Key: r.CompetenceKey, Kind: r.CompetenceKind, Level: r.Level,
			Verified: verified, ValidFrom: civil(r.ValidFrom),
		}
		if r.ValidTo.Valid {
			to := civil(r.ValidTo)
			held.ValidTo = &to
		}
		out[r.PersonID] = append(out[r.PersonID], held)
	}
	return out, nil
}
