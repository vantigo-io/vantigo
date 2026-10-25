package staff

// The profiles that reach a person (staff competence design S12, delivery C,
// plan readings 25 and 26): those staff:manage assigned to them and the
// default of the department the terms in force today of their place name —
// the earliest employment in force today, by start then id, as a group's rule
// judges it (rulePlaces). A person with no employment in force has only the
// assigned ones. An archived profile reaches nobody: an assigned one is still
// listed, so HR sees the assignment, while a department whose default is
// archived has no default. The assignment, the gap list, the export and the
// competenceGap item read through here, so the rule is written once.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// effectiveProfile is one profile that reaches a person: assigned to them,
// their department's default (Department set), or both.
type effectiveProfile struct {
	Profile    store.StaffCompetenceProfile
	Assigned   bool
	Department *gen.StaffDepartmentSummary
}

// placeDepartmentOf is the department the terms in force on today of a
// person's place name: of employments — one person's — the earliest in force
// on today by start, then id (rulePlaces' rule), its terms row in force that
// day from chains, by employment id. Nil when no employment is in force, its
// terms in force name no department, or it has no terms row yet.
func placeDepartmentOf(employments []store.StaffEmployment, chains map[int64][]store.StaffEmploymentTerm, today time.Time) *int64 {
	var place *store.StaffEmployment
	for i := range employments {
		e := &employments[i]
		start := civil(e.StartDate)
		if start.After(today) || (e.EndDate.Valid && e.EndDate.Time.Before(today)) {
			continue
		}
		if place != nil {
			cur := civil(place.StartDate)
			if cur.Before(start) || (cur.Equal(start) && place.ID < e.ID) {
				continue
			}
		}
		place = e
	}
	if place == nil {
		return nil
	}
	t := termsInForce(*place, chains[place.ID], today)
	if t == nil {
		return nil
	}
	return t.DepartmentID
}

// departmentToday is the department of the person's place on today
// (placeDepartmentOf), read on q; nil when there is none.
func departmentToday(ctx context.Context, q *store.Queries, personID int64, today time.Time) (*store.StaffDepartment, error) {
	employments, err := q.ListPersonEmployments(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("staff: read the person's employments for their profiles: %w", err)
	}
	if len(employments) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(employments))
	for _, e := range employments {
		ids = append(ids, e.ID)
	}
	terms, err := q.ListTermsOfEmployments(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("staff: read the person's terms for their profiles: %w", err)
	}
	chains := map[int64][]store.StaffEmploymentTerm{}
	for _, t := range terms {
		chains[t.EmploymentID] = append(chains[t.EmploymentID], t)
	}
	id := placeDepartmentOf(employments, chains, today)
	if id == nil {
		return nil, nil
	}
	departments, err := q.DepartmentsByID(ctx, []int64{*id})
	if err != nil {
		return nil, fmt.Errorf("staff: read the person's department for their profiles: %w", err)
	}
	if len(departments) == 0 {
		return nil, nil
	}
	return &departments[0], nil
}

// effectiveProfiles is the profiles that reach the person on today, read on
// q: the assigned ones, archived included, merged by profile with their
// department's default when it is not archived, by key then id.
func effectiveProfiles(ctx context.Context, q *store.Queries, personID int64, today time.Time) ([]effectiveProfile, error) {
	assigned, err := q.ListPersonProfiles(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("staff: read the person's profiles: %w", err)
	}
	out := make([]effectiveProfile, 0, len(assigned)+1)
	for _, r := range assigned {
		out = append(out, effectiveProfile{Profile: r.StaffCompetenceProfile, Assigned: true})
	}
	department, err := departmentToday(ctx, q, personID, today)
	if err != nil {
		return nil, err
	}
	if department != nil && department.CompetenceProfileID != nil {
		summary := departmentSummaryOf(*department)
		i := slices.IndexFunc(out, func(p effectiveProfile) bool { return p.Profile.ID == *department.CompetenceProfileID })
		if i >= 0 {
			if out[i].Profile.ArchivedAt == nil {
				out[i].Department = &summary
			}
		} else {
			profile, err := q.GetCompetenceProfile(ctx, *department.CompetenceProfileID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("staff: read the department's default profile: %w", err)
			}
			if err == nil && profile.ArchivedAt == nil {
				out = append(out, effectiveProfile{Profile: profile, Department: &summary})
			}
		}
	}
	slices.SortFunc(out, func(a, b effectiveProfile) int {
		return cmp.Or(cmp.Compare(a.Profile.Key, b.Profile.Key), cmp.Compare(a.Profile.ID, b.Profile.ID))
	})
	return out, nil
}

// personProfileResponse is one profile that reaches a person, as
// GET /people/{id}/competence-profiles and the export carry it.
func personProfileResponse(p effectiveProfile) gen.StaffPersonProfile {
	return gen.StaffPersonProfile{Profile: profileSummary(p.Profile), Assigned: p.Assigned, Department: p.Department}
}

// profileSummary is a profile as a person's list and the gap list name it.
func profileSummary(row store.StaffCompetenceProfile) gen.StaffCompetenceProfileSummary {
	return gen.StaffCompetenceProfileSummary{
		Id:       row.ID,
		Key:      row.Key,
		Names:    gen.StaffCompetenceNames{En: row.NameEn, Nb: row.NameNb},
		Archived: row.ArchivedAt != nil,
	}
}

// departmentSummaryOf is a department as the work group names it.
func departmentSummaryOf(d store.StaffDepartment) gen.StaffDepartmentSummary {
	return gen.StaffDepartmentSummary{Id: d.ID, Code: d.Code, Name: d.Name}
}
