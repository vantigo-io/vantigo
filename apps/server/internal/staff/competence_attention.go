package staff

import (
	"context"
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the competence attention items (staff competence design §4's
// table; plan Task 3b), read in the dashboard's snapshot and computed on
// read, never waiting for the staff-competence worker:
//
//   - competenceDeclared, a current row not yet verified — to staff:manage
//     and staff:approve for everyone, and to a manager for their reports;
//   - competenceExpiring, a current row whose end is within its
//     competence's lead (a lead of null raises nothing), and
//     competenceExpired, a current row whose end has passed — to
//     staff:manage for everyone, and to a manager for their reports.
//
// Only the rows of people in employment today and not erased raise items,
// and never a caller's own rows to the caller: the person sees their own on
// the Competence tab. Each item is keyed <type>/<rowId>, its entity the
// person, its title the person's name; a declaration is dated its row's
// creation, an expiry its row's last valid day.

// competenceAttention is the competence items' rows: the current declared
// rows and the current rows with an end.
type competenceAttention struct {
	declared []store.ListDeclaredCompetencesRow
	expiring []store.ListExpiringCompetencesRow
}

// readCompetenceAttention reads the competence items' rows on q, a
// snapshot's.
func readCompetenceAttention(ctx context.Context, q *store.Queries) (competenceAttention, error) {
	var a competenceAttention
	var err error
	if a.declared, err = q.ListDeclaredCompetences(ctx); err != nil {
		return a, fmt.Errorf("staff: read the declared competences for attention: %w", err)
	}
	if a.expiring, err = q.ListExpiringCompetences(ctx); err != nil {
		return a, fmt.Errorf("staff: read the expiring competences for attention: %w", err)
	}
	return a, nil
}

// competenceItems is the caller's competence items: every active person's
// for staff:manage (and the declared ones for staff:approve), a manager's
// reports' otherwise, never the caller's own.
func (in attentionInput) competenceItems(c *callerContext) []gen.StaffStatsAttentionItem {
	reports := make(map[int64]bool, len(in.reports))
	for _, id := range in.reports {
		reports[id] = true
	}
	own := func(personID int64) bool { return c.Person != nil && *c.Person == personID }
	active := in.activePeople(c.Today) // keyed by everyone in employment today
	inactive := func(personID int64) bool { _, ok := active[personID]; return !ok }
	var out []gen.StaffStatsAttentionItem
	for _, r := range in.competences.declared {
		pc := r.StaffPersonCompetence
		if r.PersonErasedAt != nil || own(pc.PersonID) || inactive(pc.PersonID) {
			continue
		}
		if c.Manage || c.Approve || reports[pc.PersonID] {
			out = append(out, personItem(gen.CompetenceDeclared, pc.ID, pc.PersonID, r.PersonDisplayName, pc.CreatedAt))
		}
	}
	for _, r := range in.competences.expiring {
		pc := r.StaffPersonCompetence
		if r.PersonErasedAt != nil || own(pc.PersonID) || inactive(pc.PersonID) {
			continue
		}
		if !c.Manage && !reports[pc.PersonID] {
			continue
		}
		if kind, ok := competenceExpiryItem(pc, r.CompetenceReminderLeadDays, c.Today); ok {
			out = append(out, personItem(kind, pc.ID, pc.PersonID, r.PersonDisplayName, civil(pc.ValidTo)))
		}
	}
	return out
}

// competenceExpiryItem is the item a current row with an end raises today,
// whatever the worker has stamped: expired once its last valid day has
// passed, expiring within its competence's lead of it.
func competenceExpiryItem(pc store.StaffPersonCompetence, lead *int16, today time.Time) (gen.StaffStatsAttentionItemType, bool) {
	if !pc.ValidTo.Valid {
		return "", false
	}
	validTo := civil(pc.ValidTo)
	if validTo.Before(today) {
		return gen.CompetenceExpired, true
	}
	if lead != nil && int(validTo.Sub(today).Hours()/24) <= int(*lead) {
		return gen.CompetenceExpiring, true
	}
	return "", false
}
