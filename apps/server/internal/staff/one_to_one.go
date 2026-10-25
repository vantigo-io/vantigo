package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is what the person wants (staff competence design S8–S10, §4
// "Who writes wishes", §5's Wishes group; plan readings 16–22): their
// interests in competences, their preferences of seven kinds and their
// availability declaration — each read by the person, their manager today,
// staff:manage and staff:personal-data, the 404 an unknown id is to anyone
// else (a colleague, an Approver); each replaced as a whole list by the
// person alone, through their self-service link, under the person's lock and
// without If-Match or a revision (reading 20). A replace is a diff, not a
// wipe (reading 18): a row equal to one on file keeps its row and its Seen
// stamp, a new or changed row is written unstamped, a row no longer sent is
// deleted — so "changed since the one-to-one" is simply "not yet stamped".
//
// The person's manager and HR stamp everything unstamped Seen in the
// one-to-one — the three lists and the person's current competence rows
// (reading 17); never the Approver, never the person (reading 16). Each write
// is one event whose payload carries counts and kinds, never a value
// (reading 21).

// stampsWishesSeen is whether the audience stamps the person's wishes Seen
// (reading 16): their manager today or HR, never the person themselves.
func (a audience) stampsWishesSeen() bool { return (a.Manager || a.Manage) && !a.Self }

// interestResponse is one interest as its readers read it, its competence
// as a competence row carries it.
func interestResponse(r store.ListPersonInterestsRow) gen.StaffPersonInterest {
	return gen.StaffPersonInterest{
		CompetenceId: r.StaffPersonInterest.CompetenceID,
		Competence: gen.StaffCompetenceSummary{
			Id:         r.StaffPersonInterest.CompetenceID,
			Key:        r.CompetenceKey,
			Names:      gen.StaffCompetenceNames{En: r.CompetenceNameEn, Nb: r.CompetenceNameNb},
			Kind:       gen.StaffCompetenceKind(r.CompetenceKind),
			HasLevel:   r.CompetenceHasLevel,
			CategoryId: r.CompetenceCategoryID,
			Archived:   r.CompetenceArchivedAt != nil,
		},
		Held:   r.Held,
		SeenAt: r.StaffPersonInterest.SeenAt,
	}
}

// preferenceResponse is one preference as its readers read it: the value
// as stored, which is its kind's canonical object.
func preferenceResponse(r store.StaffPersonPreference) gen.StaffPersonPreference {
	value := map[string]any{}
	_ = json.Unmarshal(r.Value, &value)
	return gen.StaffPersonPreference{Kind: gen.StaffPreferenceKind(r.Kind), Value: value, Weight: int32(r.Weight), SeenAt: r.SeenAt}
}

// declarationResponse is one availability row as its readers read it.
func declarationResponse(r store.StaffAvailabilityDeclaration) gen.StaffAvailabilityDeclaration {
	return gen.StaffAvailabilityDeclaration{
		Kind: gen.StaffAvailabilityKind(r.Kind), Weekday: int32Of(r.Weekday), FromDate: dateOf(r.FromDate), ToDate: dateOf(r.ToDate),
		StartMinute: int32Of(r.StartMinute), EndMinute: int32Of(r.EndMinute), SeenAt: r.SeenAt,
	}
}

// storedDeclaration is a stored row as the replace compares it.
func storedDeclaration(r store.StaffAvailabilityDeclaration) parsedDeclaration {
	return parsedDeclaration{kind: r.Kind, weekday: r.Weekday, start: r.StartMinute, end: r.EndMinute, from: r.FromDate, to: r.ToDate}
}

// readWishes reads one of the person's lists for the caller in one snapshot:
// a person they may not see, or whose wishes they do not read, is the 404
// refusal.
func (s *server) readWishes(ctx context.Context, id int64, read func(ctx context.Context, q *store.Queries) error) error {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return err
	}
	return s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := wishesAudience(ctx, q, caller, id); err != nil {
			return err
		}
		return read(ctx, q)
	})
}

// wishesAudience is the caller's audience on person id on q, or the 404
// refusal when the person is unknown or the caller reads no wishes of
// theirs.
func wishesAudience(ctx context.Context, q *store.Queries, caller *callerContext, id int64) (audience, error) {
	if _, err := readPerson(ctx, q, id); err != nil {
		return audience{}, err
	}
	a, err := personAudience(ctx, q, caller, id)
	if err != nil {
		return a, err
	}
	if !a.reads(groupWishes) {
		return a, notFound()
	}
	return a, nil
}

// selfOnly judges a wish write on q (§4 "Who writes wishes"): the person
// alone, as linked — the person the caller's self-service link names —
// says; anyone else who sees the person is the uniform 403, anyone who does
// not, and an unknown person, the 404.
func selfOnly(ctx context.Context, q *store.Queries, caller *callerContext, id int64, linked *int64) error {
	if _, err := readPerson(ctx, q, id); err != nil {
		return err
	}
	a, err := personAudience(ctx, q, caller, id)
	if err != nil {
		return err
	}
	a.Self = linked != nil && *linked == id
	switch {
	case !a.seesPerson():
		return notFound()
	case !a.Self:
		return forbidden()
	}
	return nil
}

// writeWishes runs one whole-list replace of person id's wishes as the
// caller, write being the diff under the person's lock: who may is judged in
// a snapshot first — before the body's rules, so a caller who may not
// learns nothing of them — and again under the lock with the caller's link
// read anew. judge is the body's rules, run in between.
func (s *server) writeWishes(ctx context.Context, id int64, judge func() fieldErrors,
	write func(ctx context.Context, txq *store.Queries, who actor) error,
) (fieldErrors, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return fieldErrors{}, err
	}
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		return selfOnly(ctx, q, caller, id, caller.Person)
	})
	if err != nil {
		return fieldErrors{}, err
	}
	if f := judge(); f.failed() {
		return f, nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return fieldErrors{}, err
	}
	return fieldErrors{}, s.withPersonLock(ctx, id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		linked, err := personForUser(ctx, txq, caller.UserID)
		if err != nil {
			return err
		}
		if err := selfOnly(ctx, txq, caller, id, linked); err != nil {
			return err
		}
		return write(ctx, txq, who)
	})
}

// wishesOutcome is how a replace ended, for its handler to answer: the
// field errors of the body's rules, else the error — a refusal by its
// status — else nil for success.
type wishesOutcome int

const (
	wishesOK wishesOutcome = iota
	wishesNotFound
	wishesForbidden
	wishesInvalid
	wishesConflict
)

// outcomeOf sorts a replace's result into its answer: the body's field
// errors are a 400 under title; a refusal its status, a 400 carrying its own
// problem; an unknown person the 404. Any other error is the handler's to
// return.
func outcomeOf(title string, f fieldErrors, err error) (wishesOutcome, gen.StaffProblem, error) {
	if f.failed() {
		return wishesInvalid, invalid(title, f), nil
	}
	if errors.Is(err, errPersonNotFound) {
		return wishesNotFound, gen.StaffProblem{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return wishesNotFound, gen.StaffProblem{}, nil
		case http.StatusForbidden:
			return wishesForbidden, gen.StaffProblem{}, nil
		case http.StatusConflict:
			return wishesConflict, r.problem, nil
		default:
			return wishesInvalid, r.problem, nil
		}
	}
	return wishesOK, gen.StaffProblem{}, err
}

// strictRows is the body's properties the closed schema does not name, at
// the top and on each row of list, then list itself when the body does not
// say it: the decoder reads a body without the list, or with it null, as an
// empty list, and a replace that does not say the list is not "remove them
// all" — [] says that.
func (s *server) strictRows(ctx context.Context, schema, list string) fieldErrors {
	if f := s.unknownProperties(ctx, schema); f.failed() {
		return f
	}
	if f := s.unknownRowProperties(ctx, schema, list); f.failed() {
		return f
	}
	var f fieldErrors
	if !bodyHasValue(ctx, list) {
		f.add(list, list+" is required: the whole list, or [] to remove every row")
	}
	return f
}

// GetStaffPeopleByIdInterests List a person's interests
// (GET /api/v1/staff/people/{id}/interests)
func (s *server) GetStaffPeopleByIdInterests(ctx context.Context, req gen.GetStaffPeopleByIdInterestsRequestObject) (gen.GetStaffPeopleByIdInterestsResponseObject, error) {
	var rows []store.ListPersonInterestsRow
	err := s.readWishes(ctx, req.Id, func(ctx context.Context, q *store.Queries) error {
		var err error
		if rows, err = q.ListPersonInterests(ctx, req.Id); err != nil {
			return fmt.Errorf("staff: list person %d's interests: %w", req.Id, err)
		}
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdInterests404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdInterests200JSONResponse(gen.StaffPersonInterestList{Interests: mapRows(rows, interestResponse)}), nil
}

// PutStaffPeopleByIdInterests Replace a person's interests
// (PUT /api/v1/staff/people/{id}/interests)
//
// Interests are kept by competence: a competence named again keeps its row
// and stamp; a new one must be offered — not unknown, hidden or archived
// (competence_unavailable on its index) — and is shared under the lock as a
// person's competence row shares it; one on file is kept as it is.
func (s *server) PutStaffPeopleByIdInterests(ctx context.Context, req gen.PutStaffPeopleByIdInterestsRequestObject) (gen.PutStaffPeopleByIdInterestsResponseObject, error) {
	var body gen.StaffPersonInterestsRequest
	if req.Body != nil {
		body = *req.Body
	}
	var (
		ids  []int64
		rows []store.ListPersonInterestsRow
	)
	f, err := s.writeWishes(ctx, req.Id, func() fieldErrors {
		if f := s.strictRows(ctx, interestsRequestSchema, "interests"); f.failed() {
			return f
		}
		var f fieldErrors
		ids, f = parseInterests(body)
		return f
	}, func(ctx context.Context, txq *store.Queries, who actor) error {
		before, err := txq.ListPersonInterests(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("staff: list person %d's interests: %w", req.Id, err)
		}
		held := make([]int64, 0, len(before))
		for _, r := range before {
			held = append(held, r.StaffPersonInterest.CompetenceID)
		}
		var f fieldErrors
		var added []int64
		for i, id := range ids {
			if slices.Contains(held, id) {
				continue
			}
			c, err := txq.ShareCompetence(ctx, id)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("staff: read an interest's competence: %w", err)
			}
			if err != nil || !competenceAvailable(c) {
				f.addCoded(fmt.Sprintf("interests[%d].competenceId", i), codeCompetenceUnavailable,
					fmt.Sprintf("Competence %d is unknown, hidden or archived, and no new interest names it", id))
				continue
			}
			added = append(added, id)
		}
		if f.failed() {
			return refuseInvalid(invalidInterestsTitle, f)
		}
		for _, id := range added {
			if err := txq.InsertPersonInterest(ctx, store.InsertPersonInterestParams{PersonID: req.Id, CompetenceID: id, Now: s.deps.Clock()}); err != nil {
				return fmt.Errorf("staff: add one of person %d's interests: %w", req.Id, err)
			}
		}
		removed, err := txq.DeletePersonInterestsNotIn(ctx, store.DeletePersonInterestsNotInParams{PersonID: req.Id, Keep: append([]int64{}, ids...)})
		if err != nil {
			return fmt.Errorf("staff: remove person %d's interests: %w", req.Id, err)
		}
		if rows, err = txq.ListPersonInterests(ctx, req.Id); err != nil {
			return fmt.Errorf("staff: list person %d's interests: %w", req.Id, err)
		}
		if len(added) == 0 && removed == 0 {
			return nil
		}
		return s.recordChanges(ctx, txq, req.Id, eventInterestsChanged, who,
			changes{"interests": fieldChange{From: len(before), To: len(rows)}})
	})
	outcome, p, err := outcomeOf(invalidInterestsTitle, f, err)
	switch outcome {
	case wishesNotFound:
		return gen.PutStaffPeopleByIdInterests404Response{}, nil
	case wishesForbidden:
		return gen.PutStaffPeopleByIdInterests403JSONResponse(apicommon.ForbiddenBody()), nil
	case wishesInvalid:
		return gen.PutStaffPeopleByIdInterests400ApplicationProblemPlusJSONResponse(p), nil
	case wishesConflict:
		return gen.PutStaffPeopleByIdInterests409ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: replace person %d's interests: %w", req.Id, err)
	}
	return gen.PutStaffPeopleByIdInterests200JSONResponse(gen.StaffPersonInterestList{Interests: mapRows(rows, interestResponse)}), nil
}

// GetStaffPeopleByIdPreferences List a person's preferences
// (GET /api/v1/staff/people/{id}/preferences)
func (s *server) GetStaffPeopleByIdPreferences(ctx context.Context, req gen.GetStaffPeopleByIdPreferencesRequestObject) (gen.GetStaffPeopleByIdPreferencesResponseObject, error) {
	var rows []store.StaffPersonPreference
	err := s.readWishes(ctx, req.Id, func(ctx context.Context, q *store.Queries) error {
		var err error
		if rows, err = q.ListPersonPreferences(ctx, req.Id); err != nil {
			return fmt.Errorf("staff: list person %d's preferences: %w", req.Id, err)
		}
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdPreferences404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdPreferences200JSONResponse(gen.StaffPersonPreferenceList{Preferences: mapRows(rows, preferenceResponse)}), nil
}

// PutStaffPeopleByIdPreferences Replace a person's preferences
// (PUT /api/v1/staff/people/{id}/preferences)
//
// Preferences are kept by kind: the same canonical value and weight keep
// the row and its stamp; another value or weight changes it and clears the
// stamp. A location preference new or changed names only locations that
// exist and are not archived.
func (s *server) PutStaffPeopleByIdPreferences(ctx context.Context, req gen.PutStaffPeopleByIdPreferencesRequestObject) (gen.PutStaffPeopleByIdPreferencesResponseObject, error) {
	var body gen.StaffPersonPreferencesRequest
	if req.Body != nil {
		body = *req.Body
	}
	var (
		list []parsedPreference
		rows []store.StaffPersonPreference
	)
	f, err := s.writeWishes(ctx, req.Id, func() fieldErrors {
		if f := s.strictRows(ctx, preferencesRequestSchema, "preferences"); f.failed() {
			return f
		}
		var f fieldErrors
		list, f = parsePreferences(body)
		return f
	}, func(ctx context.Context, txq *store.Queries, who actor) error {
		before, err := txq.ListPersonPreferences(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("staff: list person %d's preferences: %w", req.Id, err)
		}
		current := make(map[string]store.StaffPersonPreference, len(before))
		for _, r := range before {
			current[r.Kind] = r
		}
		var (
			f       fieldErrors
			touched []string
			keep    = make([]string, 0, len(list))
			write   []parsedPreference
		)
		for i, p := range list {
			keep = append(keep, p.kind)
			if r, ok := current[p.kind]; ok && r.Weight == p.weight {
				if stored, _, msg := canonicalPreference(r.Kind, r.Value); msg == "" && string(stored) == string(p.value) {
					continue
				}
			}
			if msg, err := s.unavailableLocations(ctx, txq, p.locations); err != nil {
				return err
			} else if msg != "" {
				f.add(fmt.Sprintf("preferences[%d].value", i), msg)
				continue
			}
			touched = append(touched, p.kind)
			write = append(write, p)
		}
		if f.failed() {
			return refuseInvalid(invalidPreferencesTitle, f)
		}
		for _, p := range write {
			if _, ok := current[p.kind]; ok {
				err = txq.UpdatePersonPreference(ctx, store.UpdatePersonPreferenceParams{
					Value: p.value, Weight: p.weight, Now: s.deps.Clock(), PersonID: req.Id, Kind: p.kind,
				})
			} else {
				err = txq.InsertPersonPreference(ctx, store.InsertPersonPreferenceParams{
					PersonID: req.Id, Kind: p.kind, Value: p.value, Weight: p.weight, Now: s.deps.Clock(),
				})
			}
			if err != nil {
				return fmt.Errorf("staff: write person %d's %s preference: %w", req.Id, p.kind, err)
			}
		}
		for _, r := range before {
			if !slices.Contains(keep, r.Kind) {
				touched = append(touched, r.Kind)
			}
		}
		if _, err := txq.DeletePersonPreferencesNotIn(ctx, store.DeletePersonPreferencesNotInParams{PersonID: req.Id, Keep: keep}); err != nil {
			return fmt.Errorf("staff: remove person %d's preferences: %w", req.Id, err)
		}
		if rows, err = txq.ListPersonPreferences(ctx, req.Id); err != nil {
			return fmt.Errorf("staff: list person %d's preferences: %w", req.Id, err)
		}
		if len(touched) == 0 {
			return nil
		}
		slices.Sort(touched)
		return s.recordChanges(ctx, txq, req.Id, eventPreferencesChanged, who, changes{"preferences": fieldChange{To: touched}})
	})
	outcome, p, err := outcomeOf(invalidPreferencesTitle, f, err)
	switch outcome {
	case wishesNotFound:
		return gen.PutStaffPeopleByIdPreferences404Response{}, nil
	case wishesForbidden:
		return gen.PutStaffPeopleByIdPreferences403JSONResponse(apicommon.ForbiddenBody()), nil
	case wishesInvalid:
		return gen.PutStaffPeopleByIdPreferences400ApplicationProblemPlusJSONResponse(p), nil
	case wishesConflict:
		return gen.PutStaffPeopleByIdPreferences409ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: replace person %d's preferences: %w", req.Id, err)
	}
	return gen.PutStaffPeopleByIdPreferences200JSONResponse(gen.StaffPersonPreferenceList{Preferences: mapRows(rows, preferenceResponse)}), nil
}

// unavailableLocations is why a preference may not name ids — one unknown
// or archived — "" when it may, or when it names none.
func (s *server) unavailableLocations(ctx context.Context, txq *store.Queries, ids []int64) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}
	found, err := txq.LocationsByIDs(ctx, ids)
	if err != nil {
		return "", fmt.Errorf("staff: read a location preference's locations: %w", err)
	}
	for _, id := range ids {
		i := slices.IndexFunc(found, func(l store.LocationsByIDsRow) bool { return l.ID == id })
		if i < 0 || found[i].ArchivedAt != nil {
			return fmt.Sprintf("Location %d is unknown or archived, and no new or changed preference names it", id), nil
		}
	}
	return "", nil
}

// GetStaffPeopleByIdAvailability List a person's availability declaration
// (GET /api/v1/staff/people/{id}/availability)
func (s *server) GetStaffPeopleByIdAvailability(ctx context.Context, req gen.GetStaffPeopleByIdAvailabilityRequestObject) (gen.GetStaffPeopleByIdAvailabilityResponseObject, error) {
	var rows []store.StaffAvailabilityDeclaration
	err := s.readWishes(ctx, req.Id, func(ctx context.Context, q *store.Queries) error {
		var err error
		if rows, err = q.ListAvailabilityDeclarations(ctx, req.Id); err != nil {
			return fmt.Errorf("staff: list person %d's availability declaration: %w", req.Id, err)
		}
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdAvailability404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdAvailability200JSONResponse(gen.StaffAvailabilityList{Declarations: mapRows(rows, declarationResponse)}), nil
}

// PutStaffPeopleByIdAvailability Replace a person's availability declaration
// (PUT /api/v1/staff/people/{id}/availability)
//
// Rows are kept by every field: a row sent equal to one on file keeps it and
// its stamp; any other is written new, and a row on file no longer sent is
// deleted.
func (s *server) PutStaffPeopleByIdAvailability(ctx context.Context, req gen.PutStaffPeopleByIdAvailabilityRequestObject) (gen.PutStaffPeopleByIdAvailabilityResponseObject, error) {
	var body gen.StaffAvailabilityRequest
	if req.Body != nil {
		body = *req.Body
	}
	var (
		list []parsedDeclaration
		rows []store.StaffAvailabilityDeclaration
	)
	f, err := s.writeWishes(ctx, req.Id, func() fieldErrors {
		if f := s.strictRows(ctx, availabilityRequestSchema, "declarations"); f.failed() {
			return f
		}
		var f fieldErrors
		list, f = parseDeclarations(body)
		return f
	}, func(ctx context.Context, txq *store.Queries, who actor) error {
		before, err := txq.ListAvailabilityDeclarations(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("staff: list person %d's availability declaration: %w", req.Id, err)
		}
		kept := make([]bool, len(before))
		var added []parsedDeclaration
		for _, d := range list {
			i := slices.IndexFunc(before, func(r store.StaffAvailabilityDeclaration) bool {
				return storedDeclaration(r).key() == d.key()
			})
			if i >= 0 && !kept[i] {
				kept[i] = true
				continue
			}
			added = append(added, d)
		}
		gone := []int64{}
		for i, r := range before {
			if !kept[i] {
				gone = append(gone, r.ID)
			}
		}
		if _, err := txq.DeleteAvailabilityDeclarations(ctx, store.DeleteAvailabilityDeclarationsParams{PersonID: req.Id, Ids: gone}); err != nil {
			return fmt.Errorf("staff: remove person %d's availability rows: %w", req.Id, err)
		}
		for _, d := range added {
			if err := txq.InsertAvailabilityDeclaration(ctx, store.InsertAvailabilityDeclarationParams{
				PersonID: req.Id, Kind: d.kind, Weekday: d.weekday, FromDate: d.from, ToDate: d.to,
				StartMinute: d.start, EndMinute: d.end, Now: s.deps.Clock(),
			}); err != nil {
				return fmt.Errorf("staff: add person %d's availability row: %w", req.Id, err)
			}
		}
		if rows, err = txq.ListAvailabilityDeclarations(ctx, req.Id); err != nil {
			return fmt.Errorf("staff: list person %d's availability declaration: %w", req.Id, err)
		}
		if len(added) == 0 && len(gone) == 0 {
			return nil
		}
		return s.recordChanges(ctx, txq, req.Id, eventAvailabilityChanged, who,
			changes{"declarations": fieldChange{From: len(before), To: len(rows)}})
	})
	outcome, p, err := outcomeOf(invalidAvailabilityTitle, f, err)
	switch outcome {
	case wishesNotFound:
		return gen.PutStaffPeopleByIdAvailability404Response{}, nil
	case wishesForbidden:
		return gen.PutStaffPeopleByIdAvailability403JSONResponse(apicommon.ForbiddenBody()), nil
	case wishesInvalid:
		return gen.PutStaffPeopleByIdAvailability400ApplicationProblemPlusJSONResponse(p), nil
	case wishesConflict:
		return gen.PutStaffPeopleByIdAvailability409ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: replace person %d's availability declaration: %w", req.Id, err)
	}
	return gen.PutStaffPeopleByIdAvailability200JSONResponse(gen.StaffAvailabilityList{Declarations: mapRows(rows, declarationResponse)}), nil
}

// PostStaffPeopleByIdWishesSeen Mark a person's wishes seen
// (POST /api/v1/staff/people/{id}/wishes/seen)
//
// The person's manager today or HR stamps, as themselves and now, every
// interest, preference and availability row without a stamp and every
// current competence row without one (readings 16 and 17), and the answer
// counts what was stamped. A caller who does not read the person's wishes
// gets the 404; one who reads them but does not stamp — the person,
// staff:personal-data — the uniform 403. A Seen that stamps nothing records
// nothing.
func (s *server) PostStaffPeopleByIdWishesSeen(ctx context.Context, req gen.PostStaffPeopleByIdWishesSeenRequestObject) (gen.PostStaffPeopleByIdWishesSeenResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	judge := func(ctx context.Context, q *store.Queries) error {
		a, err := wishesAudience(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		if !a.stampsWishesSeen() {
			return forbidden()
		}
		return nil
	}
	err = s.withReadTx(ctx, judge)
	var out gen.StaffWishesSeen
	if err == nil {
		var who actor
		if who, err = s.actorFor(ctx); err != nil {
			return nil, err
		}
		err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
			if err := judge(ctx, txq); err != nil {
				return err
			}
			now := s.deps.Clock()
			out.SeenAt = now
			for _, stamp := range []struct {
				what  string
				run   func() (int64, error)
				count *int32
			}{
				{"interests", func() (int64, error) {
					return txq.StampInterestsSeen(ctx, store.StampInterestsSeenParams{Now: now, UserID: caller.UserID, PersonID: req.Id})
				}, &out.Interests},
				{"preferences", func() (int64, error) {
					return txq.StampPreferencesSeen(ctx, store.StampPreferencesSeenParams{Now: now, UserID: caller.UserID, PersonID: req.Id})
				}, &out.Preferences},
				{"availability declaration", func() (int64, error) {
					return txq.StampDeclarationsSeen(ctx, store.StampDeclarationsSeenParams{Now: now, UserID: caller.UserID, PersonID: req.Id})
				}, &out.Declarations},
				{"competences", func() (int64, error) {
					return txq.StampCompetencesSeen(ctx, store.StampCompetencesSeenParams{Now: now, UserID: caller.UserID, PersonID: req.Id})
				}, &out.Competences},
			} {
				n, err := stamp.run()
				if err != nil {
					return fmt.Errorf("staff: stamp person %d's %s seen: %w", req.Id, stamp.what, err)
				}
				*stamp.count = int32(n)
			}
			total := out.Interests + out.Preferences + out.Declarations + out.Competences
			if total == 0 {
				return nil
			}
			return s.recordChanges(ctx, txq, req.Id, eventWishesSeen, who, changes{"seen": fieldChange{To: total}})
		})
	}
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdWishesSeen404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusForbidden:
			return gen.PostStaffPeopleByIdWishesSeen403JSONResponse(apicommon.ForbiddenBody()), nil
		case http.StatusConflict:
			return gen.PostStaffPeopleByIdWishesSeen409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PostStaffPeopleByIdWishesSeen404Response{}, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("staff: mark person %d's wishes seen: %w", req.Id, err)
	}
	return gen.PostStaffPeopleByIdWishesSeen200JSONResponse(out), nil
}
