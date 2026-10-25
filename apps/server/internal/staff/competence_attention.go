package staff

import (
	"context"
	"fmt"
	"slices"
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
//     staff:manage for everyone, and to a manager for their reports;
//   - competenceReviewOverdue (delivery B, S11 and reading 8), a person in
//     employment today whose latest competence review, or the start of
//     their current unbroken employment run when that is later, lies more
//     than the review interval ago — to staff:manage for everyone and to a
//     manager for their reports, never to staff:approve; an interval of 0
//     raises none;
//   - competenceGap (delivery C, reading 31), a person whose unarchived
//     profiles have items not met (competence_gaps.go), one per person
//     with the count — to staff:manage for everyone and to a manager for
//     their reports, never to staff:approve; dated today, since a gap is
//     computed on read and has no day of its own.
//
// Only the rows of people in employment today and not erased raise items,
// and never a caller's own rows to the caller: the person sees their own on
// the Competence tab. A row's item is keyed <type>/<rowId>, the overdue
// review <type>/<personId>, its entity the person, its title the person's
// name; a declaration is dated its row's creation, an expiry its row's last
// valid day, an overdue review the day it fell due.

// competenceAttention is the competence items' rows: the current declared
// rows, the current rows with an end, and each person's latest review day
// (only when the caller's overdue items are judged).
type competenceAttention struct {
	declared []store.ListDeclaredCompetencesRow
	expiring []store.ListExpiringCompetencesRow
	reviews  map[int64]time.Time
	// gaps is the items not met per person the caller is told of, read
	// only for staff:manage and a manager of somebody (readCompetenceGaps);
	// a person without one is absent.
	gaps map[int64]int32
}

// readCompetenceAttention reads the competence items' rows on q, a
// snapshot's; the latest reviews only when withReviews — the caller has
// overdue items to judge.
func readCompetenceAttention(ctx context.Context, q *store.Queries, withReviews bool) (competenceAttention, error) {
	var a competenceAttention
	var err error
	if a.declared, err = q.ListDeclaredCompetences(ctx); err != nil {
		return a, fmt.Errorf("staff: read the declared competences for attention: %w", err)
	}
	if a.expiring, err = q.ListExpiringCompetences(ctx); err != nil {
		return a, fmt.Errorf("staff: read the expiring competences for attention: %w", err)
	}
	if !withReviews {
		return a, nil
	}
	latest, err := q.LatestCompetenceReviews(ctx)
	if err != nil {
		return a, fmt.Errorf("staff: read the latest competence reviews for attention: %w", err)
	}
	a.reviews = make(map[int64]time.Time, len(latest))
	for _, r := range latest {
		a.reviews[r.PersonID] = civil(r.ReviewedOn)
	}
	return a, nil
}

// reviewsOverdue reports whether the caller's overdue review items are
// judged: an interval above 0, and the caller staff:manage or a manager of
// somebody today — an approver alone never has one.
func reviewsOverdue(c *callerContext, reports []int64) bool {
	return c.Settings.CompetenceReviewIntervalMonths > 0 && (c.Manage || len(reports) > 0)
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
	if reviewsOverdue(c, in.reports) {
		out = append(out, in.reviewOverdueItems(c, reports, active)...)
	}
	for personID, count := range in.competences.gaps {
		item := personItem(gen.CompetenceGap, personID, personID, in.names[personID], c.Today)
		item.Count = &count
		out = append(out, item)
	}
	return out
}

// gapsJudged reports whether the caller's competenceGap items are judged:
// staff:manage, or a manager of somebody today — an approver alone never
// has one.
func gapsJudged(c *callerContext, reports []int64) bool { return c.Manage || len(reports) > 0 }

// readCompetenceGaps reads on q, the snapshot's, the items not met of each
// person the caller is told of (reading 31): in employment today, not
// erased, not the caller, and staff:manage's everyone or a manager's
// reports. A person's profiles are their unarchived assigned ones and the
// unarchived default of the department of their place today
// (placeDepartmentOf over the register already read); the items and the
// current rows are read only when a profile reaches somebody concerned.
func (in attentionInput) readCompetenceGaps(ctx context.Context, q *store.Queries, c *callerContext) (map[int64]int32, error) {
	reports := make(map[int64]bool, len(in.reports))
	for _, id := range in.reports {
		reports[id] = true
	}
	// in.people is read for HR's roles alone; nobody in employment today is
	// erased (reviewOverdueItems), so a plain manager's reports need no such
	// read.
	erased := map[int64]bool{}
	for _, p := range in.people {
		erased[p.ID] = p.ErasedAt != nil
	}
	spans := map[int64][]store.StaffEmployment{}
	for _, e := range in.employments {
		spans[e.PersonID] = append(spans[e.PersonID], e)
	}
	assignments, err := q.ListAllPersonProfiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: read the profile assignments for attention: %w", err)
	}
	defaults, err := q.DepartmentsWithDefaultProfile(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: read the departments' default profiles for attention: %w", err)
	}
	if len(assignments) == 0 && len(defaults) == 0 {
		return nil, nil
	}
	catalogue, err := q.ListCompetenceProfiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: read the profiles for attention: %w", err)
	}
	profiles := make(map[int64]store.StaffCompetenceProfile, len(catalogue))
	for _, p := range catalogue {
		if p.ArchivedAt == nil {
			profiles[p.ID] = p
		}
	}
	defaultOf := make(map[int64]int64, len(defaults))
	for _, d := range defaults {
		defaultOf[d.ID] = *d.CompetenceProfileID
	}
	assigned := map[int64][]int64{}
	for _, a := range assignments {
		assigned[a.PersonID] = append(assigned[a.PersonID], a.ProfileID)
	}
	reach := map[int64][]effectiveProfile{}
	var people, ids []int64
	wanted := map[int64]bool{}
	for personID := range in.activePeople(c.Today) {
		if erased[personID] || (c.Person != nil && *c.Person == personID) || (!c.Manage && !reports[personID]) {
			continue
		}
		var reached []effectiveProfile
		add := func(id int64) {
			p, ok := profiles[id]
			if !ok || slices.ContainsFunc(reached, func(e effectiveProfile) bool { return e.Profile.ID == id }) {
				return
			}
			reached = append(reached, effectiveProfile{Profile: p})
			if !wanted[id] {
				wanted[id] = true
				ids = append(ids, id)
			}
		}
		for _, id := range assigned[personID] {
			add(id)
		}
		if department := placeDepartmentOf(spans[personID], in.chains, c.Today); department != nil {
			if id, ok := defaultOf[*department]; ok {
				add(id)
			}
		}
		if len(reached) > 0 {
			reach[personID] = reached
			people = append(people, personID)
		}
	}
	if len(people) == 0 {
		return nil, nil
	}
	list, err := q.ListCompetenceProfileItems(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("staff: read the profiles' items for attention: %w", err)
	}
	items := map[int64][]store.ListCompetenceProfileItemsRow{}
	for _, it := range list {
		items[it.StaffCompetenceProfileItem.ProfileID] = append(items[it.StaffCompetenceProfileItem.ProfileID], it)
	}
	current, err := q.ListCurrentCompetenceRowsOf(ctx, people)
	if err != nil {
		return nil, fmt.Errorf("staff: read the current competences for attention: %w", err)
	}
	rows := map[int64]map[int64]store.ListCurrentCompetenceRowsOfRow{}
	for _, r := range current {
		if rows[r.PersonID] == nil {
			rows[r.PersonID] = map[int64]store.ListCurrentCompetenceRowsOfRow{}
		}
		rows[r.PersonID][r.CompetenceID] = r
	}
	out := map[int64]int32{}
	for _, personID := range people {
		if n := gapsOf(reach[personID], items, rows[personID], c.Today).NotMet; n > 0 {
			out[personID] = n
		}
	}
	return out, nil
}

// reviewOverdueItems is the overdue reviews the caller is told of: of every
// person in employment today for staff:manage, of a manager's reports
// otherwise, never the caller's own nor an erased person's. Each falls due
// the interval after the later of the person's latest review and the start
// of their current unbroken employment run, and is overdue the day after.
func (in attentionInput) reviewOverdueItems(c *callerContext, reports map[int64]bool, active map[int64]int64) []gen.StaffStatsAttentionItem {
	// The people, with their erase, are read for HR's roles alone; a plain
	// manager's reports need no such read, since nobody in employment today
	// is erased — the erase waits for every employment to end (DuePeopleD)
	// and a rehire clears it.
	erased := map[int64]bool{}
	for _, p := range in.people {
		erased[p.ID] = p.ErasedAt != nil
	}
	spans := map[int64][]store.StaffEmployment{}
	for _, e := range in.employments {
		spans[e.PersonID] = append(spans[e.PersonID], e)
	}
	interval := int(c.Settings.CompetenceReviewIntervalMonths)
	var out []gen.StaffStatsAttentionItem
	for personID := range active {
		if erased[personID] || (c.Person != nil && *c.Person == personID) || (!c.Manage && !reports[personID]) {
			continue
		}
		start := unbrokenStart(employmentSpans(spans[personID]), c.Today)
		if start == nil {
			continue
		}
		anchor := *start
		if reviewed, ok := in.competences.reviews[personID]; ok && reviewed.After(anchor) {
			anchor = reviewed
		}
		if due := addMonths(anchor, interval); c.Today.After(due) {
			out = append(out, personItem(gen.CompetenceReviewOverdue, personID, personID, in.names[personID], due))
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
