package staff

import (
	"context"
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is what a person's profiles ask of them (staff competence design
// S12, S13; plan readings 23–26 and 32): the profiles that reach the person
// and, item by item, how their current rows meet each unarchived one. Both
// reads are the Profiles and gaps group's — the person, their manager, the
// Approver, staff:manage and staff:personal-data — and the bare 404 to
// anyone else, the colleague included. The competenceGap item
// (competence_attention.go) counts through the same rule.

// gapInput is one profile item judged on the person's current row of its
// competence (nil when they hold none) on today.
type gapInput struct {
	item  store.ListCompetenceProfileItemsRow
	row   *store.ListCurrentCompetenceRowsOfRow
	today time.Time
}

// gapStatus is how the item is met (reading 23), in this order: no current
// row is missing; a declared certificate is declared_only; a minimum level
// the row's level is below is below_level; when the item asks a valid row,
// one whose end has passed is expired and one not yet valid missing, and one
// whose end lies within the competence's lead expiring — the lead rule of
// competenceExpiry; otherwise met. An item that does not ask a valid row
// never judges the validity (reading 24). declared is whether the row is
// unverified: a skill declared at the level is met, with the flag.
func gapStatus(in gapInput) (status gen.StaffCompetenceGapStatus, declared bool) {
	if in.row == nil {
		return gen.GapMissing, false
	}
	item, row := in.item.StaffCompetenceProfileItem, in.row
	declared = row.VerifiedAt == nil
	if declared && in.item.CompetenceKind == kindCertificate {
		return gen.GapDeclaredOnly, declared
	}
	if item.MinLevel != nil && row.Level != nil && *row.Level < *item.MinLevel {
		return gen.GapBelowLevel, declared
	}
	if !item.MustBeValid {
		return gen.GapMet, declared
	}
	if row.ValidFrom.Valid && civil(row.ValidFrom).After(in.today) {
		return gen.GapMissing, declared
	}
	if row.ValidTo.Valid {
		end := civil(row.ValidTo)
		if end.Before(in.today) {
			return gen.GapExpired, declared
		}
		if lead := in.item.CompetenceReminderLeadDays; lead != nil && !end.After(in.today.AddDate(0, 0, int(*lead))) {
			return gen.GapExpiring, declared
		}
	}
	return gen.GapMet, declared
}

// notMet reports whether status counts as a gap: every status but met and
// expiring.
func notMet(status gen.StaffCompetenceGapStatus) bool {
	return status != gen.GapMet && status != gen.GapExpiring
}

// gapsOf is the gap list of the profiles that reach a person: each
// unarchived profile with its items (by profile id) judged on the person's
// current rows (by competence id) on today — an archived profile reaches
// nobody (reading 26), while an archived competence's item stays, reported
// and counted — and the items not met per profile and in all.
func gapsOf(profiles []effectiveProfile, items map[int64][]store.ListCompetenceProfileItemsRow,
	rows map[int64]store.ListCurrentCompetenceRowsOfRow, today time.Time) gen.StaffCompetenceGaps {
	out := gen.StaffCompetenceGaps{Profiles: []gen.StaffCompetenceGapProfile{}}
	for _, p := range profiles {
		if p.Profile.ArchivedAt != nil {
			continue
		}
		gp := gen.StaffCompetenceGapProfile{
			Profile: profileSummary(p.Profile), Assigned: p.Assigned, Department: p.Department,
			Items: []gen.StaffCompetenceGapItem{},
		}
		for _, it := range items[p.Profile.ID] {
			var row *store.ListCurrentCompetenceRowsOfRow
			if r, ok := rows[it.StaffCompetenceProfileItem.CompetenceID]; ok {
				row = &r
			}
			status, declared := gapStatus(gapInput{item: it, row: row, today: today})
			gi := gen.StaffCompetenceGapItem{
				CompetenceId: it.StaffCompetenceProfileItem.CompetenceID,
				Competence:   gapCompetenceSummary(it),
				MinLevel:     int32Of(it.StaffCompetenceProfileItem.MinLevel),
				MustBeValid:  it.StaffCompetenceProfileItem.MustBeValid,
				Status:       status,
				Declared:     declared,
				Archived:     it.CompetenceArchivedAt != nil,
			}
			if row != nil {
				gi.Verified = row.VerifiedAt != nil
				gi.Level = int32Of(row.Level)
				gi.ValidTo = dateOf(row.ValidTo)
			}
			if notMet(status) {
				gp.NotMet++
			}
			gp.Items = append(gp.Items, gi)
		}
		out.NotMet += gp.NotMet
		out.Profiles = append(out.Profiles, gp)
	}
	return out
}

// gapCompetenceSummary is a profile item's competence as a row carries it.
func gapCompetenceSummary(it store.ListCompetenceProfileItemsRow) gen.StaffCompetenceSummary {
	return gen.StaffCompetenceSummary{
		Id:         it.StaffCompetenceProfileItem.CompetenceID,
		Key:        it.CompetenceKey,
		Names:      gen.StaffCompetenceNames{En: it.CompetenceNameEn, Nb: it.CompetenceNameNb},
		Kind:       gen.StaffCompetenceKind(it.CompetenceKind),
		HasLevel:   it.CompetenceHasLevel,
		CategoryId: it.CompetenceCategoryID,
		Archived:   it.CompetenceArchivedAt != nil,
	}
}

// readGaps reads on q the gap list of the profiles that reach personID on
// today: their unarchived profiles' items and the person's current rows.
func readGaps(ctx context.Context, q *store.Queries, personID int64, profiles []effectiveProfile, today time.Time) (gen.StaffCompetenceGaps, error) {
	ids := make([]int64, 0, len(profiles))
	for _, p := range profiles {
		if p.Profile.ArchivedAt == nil {
			ids = append(ids, p.Profile.ID)
		}
	}
	if len(ids) == 0 {
		return gapsOf(nil, nil, nil, today), nil
	}
	list, err := q.ListCompetenceProfileItems(ctx, ids)
	if err != nil {
		return gen.StaffCompetenceGaps{}, fmt.Errorf("staff: read the items of person %d's profiles: %w", personID, err)
	}
	items := map[int64][]store.ListCompetenceProfileItemsRow{}
	for _, it := range list {
		items[it.StaffCompetenceProfileItem.ProfileID] = append(items[it.StaffCompetenceProfileItem.ProfileID], it)
	}
	current, err := q.ListCurrentCompetenceRowsOf(ctx, []int64{personID})
	if err != nil {
		return gen.StaffCompetenceGaps{}, fmt.Errorf("staff: read person %d's current competences: %w", personID, err)
	}
	rows := make(map[int64]store.ListCurrentCompetenceRowsOfRow, len(current))
	for _, r := range current {
		rows[r.CompetenceID] = r
	}
	return gapsOf(profiles, items, rows, today), nil
}

// readProfilesOf is the snapshot both GETs answer from: the person, the
// caller's audience on them — the bare 404 outside the Profiles and gaps
// group — and the profiles that reach them today.
func (s *server) readProfilesOf(ctx context.Context, personID int64, then func(ctx context.Context, q *store.Queries, profiles []effectiveProfile, today time.Time) error) error {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return err
	}
	return s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := readPerson(ctx, q, personID); err != nil {
			return err
		}
		a, err := personAudience(ctx, q, caller, personID)
		if err != nil {
			return err
		}
		if !a.reads(groupProfiles) {
			return notFound()
		}
		profiles, err := effectiveProfiles(ctx, q, personID, caller.Today)
		if err != nil {
			return err
		}
		return then(ctx, q, profiles, caller.Today)
	})
}

// GetStaffPeopleByIdCompetenceProfiles List the profiles that reach a person
// (GET /api/v1/staff/people/{id}/competence-profiles)
//
// Assigned ones, archived included so HR sees the assignment, and the
// default of the department of the person's place today, each saying which
// (reading 25).
func (s *server) GetStaffPeopleByIdCompetenceProfiles(ctx context.Context, req gen.GetStaffPeopleByIdCompetenceProfilesRequestObject) (gen.GetStaffPeopleByIdCompetenceProfilesResponseObject, error) {
	var out gen.StaffPersonProfileList
	err := s.readProfilesOf(ctx, req.Id, func(_ context.Context, _ *store.Queries, profiles []effectiveProfile, _ time.Time) error {
		out.Profiles = mapRows(profiles, personProfileResponse)
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdCompetenceProfiles404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdCompetenceProfiles200JSONResponse(out), nil
}

// GetStaffPeopleByIdCompetenceGaps What a person's profiles ask and how it
// is met (GET /api/v1/staff/people/{id}/competence-gaps)
func (s *server) GetStaffPeopleByIdCompetenceGaps(ctx context.Context, req gen.GetStaffPeopleByIdCompetenceGapsRequestObject) (gen.GetStaffPeopleByIdCompetenceGapsResponseObject, error) {
	var out gen.StaffCompetenceGaps
	err := s.readProfilesOf(ctx, req.Id, func(ctx context.Context, q *store.Queries, profiles []effectiveProfile, today time.Time) error {
		var err error
		out, err = readGaps(ctx, q, req.Id, profiles, today)
		return err
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdCompetenceGaps404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdCompetenceGaps200JSONResponse(out), nil
}
