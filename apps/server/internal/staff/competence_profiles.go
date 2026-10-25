package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the competence profiles catalogue (staff competence design
// S12, delivery C; the plan's readings 27, 28 and 36): a named list of
// competences, each with a minimum level and whether the person's row must
// be valid, which HR assigns to people or makes a department's default. The
// catalogue is about no person: every caller reads every profile, archived
// ones included, and staff:manage writes them. A profile's key never
// changes; its items are replaced whole under its If-Match. Archiving one is
// never refused for the people or departments that name it — it simply
// reaches nobody until it is restored (reading 26). Each write is an
// installation event — competence_profile_created, competence_profile_updated
// — as the competences' are.

// competenceProfileItemResponse is one item with its competence's summary.
func competenceProfileItemResponse(r store.ListCompetenceProfileItemsRow) gen.StaffCompetenceProfileItem {
	i := r.StaffCompetenceProfileItem
	return gen.StaffCompetenceProfileItem{
		CompetenceId: i.CompetenceID,
		Competence: gen.StaffCompetenceSummary{
			Id:         i.CompetenceID,
			Key:        r.CompetenceKey,
			Names:      gen.StaffCompetenceNames{En: r.CompetenceNameEn, Nb: r.CompetenceNameNb},
			Kind:       gen.StaffCompetenceKind(r.CompetenceKind),
			HasLevel:   r.CompetenceHasLevel,
			CategoryId: r.CompetenceCategoryID,
			Archived:   r.CompetenceArchivedAt != nil,
		},
		MinLevel:    int32Of(i.MinLevel),
		MustBeValid: i.MustBeValid,
	}
}

// competenceProfileResponse is one profile in full with its items, which
// are the profile's own in the catalogue's order.
func competenceProfileResponse(p store.StaffCompetenceProfile, items []store.ListCompetenceProfileItemsRow) gen.StaffCompetenceProfile {
	return gen.StaffCompetenceProfile{
		Id:         p.ID,
		Key:        p.Key,
		Names:      gen.StaffCompetenceNames{En: p.NameEn, Nb: p.NameNb},
		ArchivedAt: p.ArchivedAt,
		Items:      mapRows(items, competenceProfileItemResponse),
		Revision:   p.Revision,
	}
}

// profileItemsOf is the items of one profile, read on q.
func profileItemsOf(ctx context.Context, q *store.Queries, id int64) ([]store.ListCompetenceProfileItemsRow, error) {
	items, err := q.ListCompetenceProfileItems(ctx, []int64{id})
	if err != nil {
		return nil, fmt.Errorf("staff: list competence profile %d's items: %w", id, err)
	}
	return items, nil
}

// GetStaffCompetenceProfiles List competence profiles
// (GET /api/v1/staff/competence-profiles)
//
// Every caller reads every profile, archived ones included, so Settings
// restores one and a person's list names its own.
func (s *server) GetStaffCompetenceProfiles(ctx context.Context, _ gen.GetStaffCompetenceProfilesRequestObject) (gen.GetStaffCompetenceProfilesResponseObject, error) {
	var (
		profiles []store.StaffCompetenceProfile
		items    []store.ListCompetenceProfileItemsRow
	)
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if profiles, err = q.ListCompetenceProfiles(ctx); err != nil {
			return fmt.Errorf("staff: list the competence profiles: %w", err)
		}
		ids := make([]int64, 0, len(profiles))
		for _, p := range profiles {
			ids = append(ids, p.ID)
		}
		if items, err = q.ListCompetenceProfileItems(ctx, ids); err != nil {
			return fmt.Errorf("staff: list the competence profiles' items: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	byProfile := make(map[int64][]store.ListCompetenceProfileItemsRow, len(profiles))
	for _, i := range items {
		byProfile[i.StaffCompetenceProfileItem.ProfileID] = append(byProfile[i.StaffCompetenceProfileItem.ProfileID], i)
	}
	return gen.GetStaffCompetenceProfiles200JSONResponse(mapRows(profiles, func(p store.StaffCompetenceProfile) gen.StaffCompetenceProfile {
		return competenceProfileResponse(p, byProfile[p.ID])
	})), nil
}

// PostStaffCompetenceProfiles Add a competence profile
// (POST /api/v1/staff/competence-profiles)
func (s *server) PostStaffCompetenceProfiles(ctx context.Context, req gen.PostStaffCompetenceProfilesRequestObject) (gen.PostStaffCompetenceProfilesResponseObject, error) {
	var body gen.StaffCompetenceProfileRequest
	if req.Body != nil {
		body = *req.Body
	}
	if f := s.strictRows(ctx, competenceProfileRequestSchema, "items"); f.failed() {
		return gen.PostStaffCompetenceProfiles400ApplicationProblemPlusJSONResponse(invalid(invalidCompetenceProfileTitle, f)), nil
	}
	var f fieldErrors
	if !competenceCategoryKeyPattern.MatchString(body.Key) {
		f.add("key", "A key is 1 to 40 characters of a–z, 0–9 and hyphens")
	}
	en, nb := parseCompetenceNames(body.Names, &f)
	items := parseProfileItems(body.Items, &f)
	if f.failed() {
		return gen.PostStaffCompetenceProfiles400ApplicationProblemPlusJSONResponse(invalid(invalidCompetenceProfileTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var out gen.StaffCompetenceProfile
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := judgeProfileItems(ctx, txq, items, nil); err != nil {
			return err
		}
		created, err := txq.InsertCompetenceProfile(ctx, store.InsertCompetenceProfileParams{
			Key: body.Key, NameEn: en, NameNb: nb, Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, competenceProfileKeyIndex) {
			return refuseInvalid(invalidCompetenceProfileTitle, fieldErrors{errs: fieldError("key", "Another profile already has this key")})
		}
		if err != nil {
			return fmt.Errorf("staff: add a competence profile: %w", err)
		}
		if err := s.writeProfileItems(ctx, txq, created.ID, items, nil); err != nil {
			return err
		}
		saved, err := profileItemsOf(ctx, txq, created.ID)
		if err != nil {
			return err
		}
		out = competenceProfileResponse(created, saved)
		return s.recordCompetenceProfileChange(ctx, txq, eventCompetenceProfileCreated, who, nil, out)
	})
	if r, ok := asRefusal(err); ok {
		return gen.PostStaffCompetenceProfiles400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostStaffCompetenceProfiles201JSONResponse(out), nil
}

// PutStaffCompetenceProfilesById Change a competence profile
// (PUT /api/v1/staff/competence-profiles/{id})
//
// The profile's row is taken FOR NO KEY UPDATE, which an assignment or a
// department's default sharing it (ShareCompetenceProfile) waits for, so an
// archive and a new assignment are judged one after the other. The items are
// replaced whole as a diff: an item on a competence kept keeps its row.
func (s *server) PutStaffCompetenceProfilesById(ctx context.Context, req gen.PutStaffCompetenceProfilesByIdRequestObject) (gen.PutStaffCompetenceProfilesByIdResponseObject, error) {
	var body gen.StaffCompetenceProfileChangeRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffCompetenceProfilesById400ApplicationProblemPlusJSONResponse(invalid(invalidCompetenceProfileTitle, badIfMatch())), nil
	}
	if f := s.strictRows(ctx, competenceProfileChangeRequestSchema, "items"); f.failed() {
		return gen.PutStaffCompetenceProfilesById400ApplicationProblemPlusJSONResponse(invalid(invalidCompetenceProfileTitle, f)), nil
	}
	var f fieldErrors
	en, nb := parseCompetenceNames(body.Names, &f)
	items := parseProfileItems(body.Items, &f)
	if f.failed() {
		return gen.PutStaffCompetenceProfilesById400ApplicationProblemPlusJSONResponse(invalid(invalidCompetenceProfileTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var out gen.StaffCompetenceProfile
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		row, err := txq.LockCompetenceProfile(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock competence profile %d: %w", req.Id, err)
		}
		if err := checkRevision("competence profile", row.Revision, revision); err != nil {
			return err
		}
		stored, err := profileItemsOf(ctx, txq, row.ID)
		if err != nil {
			return err
		}
		before := make(map[int64]store.StaffCompetenceProfileItem, len(stored))
		for _, r := range stored {
			before[r.StaffCompetenceProfileItem.CompetenceID] = r.StaffCompetenceProfileItem
		}
		if err := judgeProfileItems(ctx, txq, items, before); err != nil {
			return err
		}
		if err := s.writeProfileItems(ctx, txq, row.ID, items, before); err != nil {
			return err
		}
		saved, err := txq.UpdateCompetenceProfile(ctx, store.UpdateCompetenceProfileParams{
			ID: row.ID, NameEn: en, NameNb: nb, Archived: body.Archived, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("staff: change competence profile %d: %w", row.ID, err)
		}
		after, err := profileItemsOf(ctx, txq, row.ID)
		if err != nil {
			return err
		}
		old := competenceProfileResponse(row, stored)
		out = competenceProfileResponse(saved, after)
		return s.recordCompetenceProfileChange(ctx, txq, eventCompetenceProfileUpdated, who, &old, out)
	})
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffCompetenceProfilesById404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffCompetenceProfilesById409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffCompetenceProfilesById400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.PutStaffCompetenceProfilesById200JSONResponse(out), nil
}

// writeProfileItems writes profile id's items as the diff from before (by
// competence, nil for a new profile): a new competence inserted, a changed
// item updated, an unchanged one left as it is, and every competence no
// longer named removed.
func (s *server) writeProfileItems(ctx context.Context, txq *store.Queries, id int64, items []profileItem, before map[int64]store.StaffCompetenceProfileItem) error {
	keep := make([]int64, 0, len(items))
	for _, it := range items {
		keep = append(keep, it.CompetenceID)
		stored, ok := before[it.CompetenceID]
		switch {
		case !ok:
			if err := txq.InsertCompetenceProfileItem(ctx, store.InsertCompetenceProfileItemParams{
				ProfileID: id, CompetenceID: it.CompetenceID, MinLevel: it.MinLevel, MustBeValid: it.MustBeValid, Now: s.deps.Clock(),
			}); err != nil {
				return fmt.Errorf("staff: add an item to competence profile %d: %w", id, err)
			}
		case !it.sameItem(stored):
			if err := txq.UpdateCompetenceProfileItem(ctx, store.UpdateCompetenceProfileItemParams{
				ProfileID: id, CompetenceID: it.CompetenceID, MinLevel: it.MinLevel, MustBeValid: it.MustBeValid,
			}); err != nil {
				return fmt.Errorf("staff: change an item of competence profile %d: %w", id, err)
			}
		}
	}
	if before == nil {
		return nil
	}
	if _, err := txq.DeleteCompetenceProfileItemsNotIn(ctx, store.DeleteCompetenceProfileItemsNotInParams{ProfileID: id, Keep: keep}); err != nil {
		return fmt.Errorf("staff: remove items of competence profile %d: %w", id, err)
	}
	return nil
}

// profileItemPayload is an item as a profile's event names it: the
// competence, the minimum level and the validity rule — never the
// competence's names, which are the catalogue's own events'.
type profileItemPayload struct {
	CompetenceID int64  `json:"competenceId"`
	MinLevel     *int32 `json:"minLevel,omitempty"`
	MustBeValid  bool   `json:"mustBeValid"`
}

// profilePayload is a profile's fields as its events name them (reading
// 27): the key, the names, whether it is archived and the items.
type profilePayload struct {
	Key      string                   `json:"key"`
	Names    gen.StaffCompetenceNames `json:"names"`
	Archived bool                     `json:"archived,omitempty"`
	Items    []profileItemPayload     `json:"items"`
}

// profilePayloadOf is p's fields for an event.
func profilePayloadOf(p gen.StaffCompetenceProfile) profilePayload {
	return profilePayload{
		Key: p.Key, Names: p.Names, Archived: p.ArchivedAt != nil,
		Items: mapRows(p.Items, func(i gen.StaffCompetenceProfileItem) profileItemPayload {
			return profileItemPayload{CompetenceID: i.CompetenceId, MinLevel: i.MinLevel, MustBeValid: i.MustBeValid}
		}),
	}
}

// recordCompetenceProfileChange records a profile's create (before nil) or
// change as an installation event: the profile's id and the fields that
// changed.
func (s *server) recordCompetenceProfileChange(ctx context.Context, txq *store.Queries, kind string, who actor, before *gen.StaffCompetenceProfile, after gen.StaffCompetenceProfile) error {
	var old map[string]json.RawMessage
	if before != nil {
		var err error
		if old, err = payloadValues(profilePayloadOf(*before)); err != nil {
			return err
		}
	}
	values, err := payloadValues(profilePayloadOf(after))
	if err != nil {
		return err
	}
	return s.recordInstallationChange(ctx, txq, kind, who, "competenceProfileId", after.Id, diffFields(old, values))
}
