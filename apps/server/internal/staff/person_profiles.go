package staff

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// The profiles assigned to a person (staff competence design S12, delivery
// C; the plan's readings 26, 32 and 34): staff:manage replaces the list
// whole. Who may is judged in a snapshot first — the bare 404 to a caller
// outside the Profiles group, the uniform 403 to anyone else but HR — before
// the body's rules, and again under the person's lock. A profile already
// assigned is kept even when archived since; a new one must exist and not be
// archived. The answer is every profile that reaches the person, their
// department's default included (effectiveProfiles).

// judgeProfileAssigner is whether the caller assigns person id's profiles:
// the 404 refusal outside the Profiles group, the 403 to anyone but
// staff:manage.
func judgeProfileAssigner(ctx context.Context, q *store.Queries, caller *callerContext, id int64) error {
	if _, err := readPerson(ctx, q, id); err != nil {
		return err
	}
	a, err := personAudience(ctx, q, caller, id)
	if err != nil {
		return err
	}
	switch {
	case !a.reads(groupProfiles):
		return notFound()
	case !a.Manage:
		return forbidden()
	}
	return nil
}

// profileKeys is the keys of a person's assigned profiles, in the list's
// order (by key).
func profileKeys(rows []store.ListPersonProfilesRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.StaffCompetenceProfile.Key)
	}
	return out
}

// PutStaffPeopleByIdCompetenceProfiles Replace the profiles assigned to a person
// (PUT /api/v1/staff/people/{id}/competence-profiles)
func (s *server) PutStaffPeopleByIdCompetenceProfiles(ctx context.Context, req gen.PutStaffPeopleByIdCompetenceProfilesRequestObject) (gen.PutStaffPeopleByIdCompetenceProfilesResponseObject, error) {
	var body gen.StaffPersonProfilesRequest
	if req.Body != nil {
		body = *req.Body
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var f fieldErrors
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		return judgeProfileAssigner(ctx, q, caller, req.Id)
	})
	if err == nil {
		if f = s.strictRows(ctx, personProfilesRequestSchema, "profileIds"); !f.failed() {
			f = parseProfileIDs(body.ProfileIds)
		}
	}
	var profiles []effectiveProfile
	if err == nil && !f.failed() {
		var who actor
		if who, err = s.actorFor(ctx); err != nil {
			return nil, err
		}
		err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
			if err := judgeProfileAssigner(ctx, txq, caller, req.Id); err != nil {
				return err
			}
			if err := s.assignProfiles(ctx, txq, req.Id, body.ProfileIds, who); err != nil {
				return err
			}
			var err error
			profiles, err = effectiveProfiles(ctx, txq, req.Id, caller.Today)
			return err
		})
	}
	outcome, p, err := outcomeOf(invalidPersonProfilesTitle, f, err)
	switch outcome {
	case wishesNotFound:
		return gen.PutStaffPeopleByIdCompetenceProfiles404Response{}, nil
	case wishesForbidden:
		return gen.PutStaffPeopleByIdCompetenceProfiles403JSONResponse(apicommon.ForbiddenBody()), nil
	case wishesInvalid:
		return gen.PutStaffPeopleByIdCompetenceProfiles400ApplicationProblemPlusJSONResponse(p), nil
	case wishesConflict:
		return gen.PutStaffPeopleByIdCompetenceProfiles409ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: replace person %d's profiles: %w", req.Id, err)
	}
	return gen.PutStaffPeopleByIdCompetenceProfiles200JSONResponse(gen.StaffPersonProfileList{
		Profiles: mapRows(profiles, personProfileResponse),
	}), nil
}

// assignProfiles replaces person id's assigned profiles with ids under the
// person's lock: a profile already assigned is kept; a new one is shared
// (ShareCompetenceProfile) and must exist and not be archived, a field error
// on its index otherwise (reading 26). A change of the set is
// profiles_assigned with the keys before and after (reading 34).
func (s *server) assignProfiles(ctx context.Context, txq *store.Queries, id int64, ids []int64, who actor) error {
	before, err := txq.ListPersonProfiles(ctx, id)
	if err != nil {
		return fmt.Errorf("staff: list person %d's profiles: %w", id, err)
	}
	held := make(map[int64]bool, len(before))
	for _, r := range before {
		held[r.StaffCompetenceProfile.ID] = true
	}
	var (
		f     fieldErrors
		added []int64
	)
	for i, pid := range ids {
		if held[pid] {
			continue
		}
		field := fmt.Sprintf("profileIds[%d]", i)
		p, err := txq.ShareCompetenceProfile(ctx, pid)
		if errors.Is(err, pgx.ErrNoRows) {
			f.add(field, fmt.Sprintf("No competence profile has id %d", pid))
			continue
		} else if err != nil {
			return fmt.Errorf("staff: read competence profile %d: %w", pid, err)
		}
		if p.ArchivedAt != nil {
			f.add(field, fmt.Sprintf("The profile %s is archived; nobody is assigned it", p.NameEn))
			continue
		}
		added = append(added, pid)
	}
	if f.failed() {
		return refuseInvalid(invalidPersonProfilesTitle, f)
	}
	for _, pid := range added {
		if err := txq.InsertPersonProfile(ctx, store.InsertPersonProfileParams{PersonID: id, ProfileID: pid, Now: s.deps.Clock()}); err != nil {
			return fmt.Errorf("staff: assign a profile to person %d: %w", id, err)
		}
	}
	removed, err := txq.DeletePersonProfilesNotIn(ctx, store.DeletePersonProfilesNotInParams{PersonID: id, Keep: append([]int64{}, ids...)})
	if err != nil {
		return fmt.Errorf("staff: remove person %d's profiles: %w", id, err)
	}
	if len(added) == 0 && removed == 0 {
		return nil
	}
	after, err := txq.ListPersonProfiles(ctx, id)
	if err != nil {
		return fmt.Errorf("staff: list person %d's profiles: %w", id, err)
	}
	var c fieldChange
	if keys := profileKeys(before); len(keys) > 0 {
		c.From = keys
	}
	if keys := profileKeys(after); len(keys) > 0 {
		c.To = keys
	}
	return s.recordChanges(ctx, txq, id, eventProfilesAssigned, who, changes{"profiles": c})
}
