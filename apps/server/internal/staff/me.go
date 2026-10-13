package staff

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is My page (design S17, §6 Me): the caller's own person, found
// through their self-service link, read as the person themselves reads it —
// the Self audience and nothing more, whatever else the caller holds, so My
// page shows what the person sees of themselves — and the short list of
// fields they edit themselves: the private phones and email, the home
// address and the next of kin. Everything else on the record is HR's; a
// field of it sent here is refused as a property the request does not name
// (strictbody.go). A caller linked to nobody gets no_person.

// selfAudience is the caller reading their own record on My page.
var selfAudience = audience{Self: true}

// errNoPerson is the caller's link gone between reading the caller and taking
// the person's lock: the person is no longer theirs to edit.
var errNoPerson = errors.New("staff: the caller is linked to no person")

// parsedMe is one validated StaffMeRequest: the fields a person edits
// themselves, each nil when left out (cleared — the request is a full
// replace), and their next of kin.
type parsedMe struct {
	PhoneMobile  *string
	PhonePrivate *string
	PrivateEmail *string
	Street       *string
	PostalCode   *string
	City         *string
	Country      *string
	NextOfKin    []gen.StaffNextOfKin
}

// parseMe runs each field's rule — the same rules HR's replace of the record
// runs — and collects every failure.
func parseMe(body gen.StaffMeRequest) (parsedMe, fieldErrors) {
	var out parsedMe
	var msg string
	kin, f := parseNextOfKin(gen.StaffNextOfKinRequest{NextOfKin: body.NextOfKin})
	out.NextOfKin = kin
	out.PhoneMobile, msg = optionalText("A mobile phone", body.PhoneMobile, phoneMaxLength)
	f.add("phoneMobile", msg)
	out.PhonePrivate, msg = optionalText("A private phone", body.PhonePrivate, phoneMaxLength)
	f.add("phonePrivate", msg)
	out.PrivateEmail, msg = parseEmail("A private email", body.PrivateEmail)
	f.add("privateEmail", msg)
	out.Street, msg = optionalText("A street", body.Street, streetMaxLength)
	f.add("street", msg)
	out.PostalCode, msg = optionalText("A postal code", body.PostalCode, postalCodeMaxLength)
	f.add("postalCode", msg)
	out.City, msg = optionalText("A city", body.City, cityMaxLength)
	f.add("city", msg)
	if body.Country != nil {
		var country string
		country, msg = parseCountry(body.Country, "")
		f.add("country", msg)
		out.Country = &country
	}
	return out, f
}

// applyTo is the row p makes of current: the person's own fields replaced,
// every other field as it was.
func (p parsedMe) applyTo(current store.GetPersonRow) store.GetPersonRow {
	next := current
	next.PhoneMobile = p.PhoneMobile
	next.PhonePrivate = p.PhonePrivate
	next.PrivateEmail = p.PrivateEmail
	next.Street = p.Street
	next.PostalCode = p.PostalCode
	next.City = p.City
	next.Country = p.Country
	return next
}

// respondSelf is the person as they read themselves, placed on today, the
// linked user read from the directory after any transaction.
func (s *server) respondSelf(ctx context.Context, caller *callerContext, row store.GetPersonRow, link *string) (gen.StaffPerson, error) {
	placements, err := placementsOf(ctx, store.New(s.deps.Pool), []int64{row.ID}, caller.Today)
	if err != nil {
		return gen.StaffPerson{}, err
	}
	user, err := s.linkedUser(ctx, link)
	if err != nil {
		return gen.StaffPerson{}, err
	}
	return personResponse(row, placements[row.ID], selfAudience, user), nil
}

// GetStaffMe Get my own record
// (GET /api/v1/staff/me)
func (s *server) GetStaffMe(ctx context.Context, _ gen.GetStaffMeRequestObject) (gen.GetStaffMeResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if caller.Person == nil {
		return gen.GetStaffMe404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	var (
		row  store.GetPersonRow
		link *string
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if row, err = readPerson(ctx, q, *caller.Person); err != nil {
			return err
		}
		link, err = linkedUserID(ctx, q, *caller.Person)
		return err
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffMe404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	if err != nil {
		return nil, err
	}
	out, err := s.respondSelf(ctx, caller, row, link)
	if err != nil {
		return nil, err
	}
	return gen.GetStaffMe200JSONResponse(out), nil
}

// PutStaffMe Change my own record
// (PUT /api/v1/staff/me)
//
// A full replace of the person's own fields under their revision. The
// record's change and the next of kin's are each an event, the person their
// actor, and together they move the revision on once.
func (s *server) PutStaffMe(ctx context.Context, req gen.PutStaffMeRequestObject) (gen.PutStaffMeResponseObject, error) {
	var body gen.StaffMeRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffMe400ApplicationProblemPlusJSONResponse(invalid(invalidPersonTitle, badIfMatch())), nil
	}
	if f := s.unknownProperties(ctx, meRequestSchema); f.failed() {
		return gen.PutStaffMe400ApplicationProblemPlusJSONResponse(invalid(invalidPersonTitle, f)), nil
	}
	parsed, f := parseMe(body)
	if f.failed() {
		return gen.PutStaffMe400ApplicationProblemPlusJSONResponse(invalid(invalidPersonTitle, f)), nil
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if caller.Person == nil {
		return gen.PutStaffMe404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}

	id := *caller.Person
	var (
		saved store.GetPersonRow
		link  *string
	)
	err = s.withPersonLock(ctx, id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		// The link is judged again under the person's lock: HR may have
		// unlinked the caller since they were read, and then this record is
		// no longer theirs to change.
		still, err := personForUser(ctx, txq, caller.UserID)
		if err != nil {
			return err
		}
		if still == nil || *still != id {
			return errNoPerson
		}
		current, err := readPerson(ctx, txq, id)
		if err != nil {
			return err
		}
		if err := checkRevision("person", current.Revision, revision); err != nil {
			return err
		}
		next := parsed.applyTo(current)
		diff := diffPerson(&current, next)
		if len(diff) > 0 {
			if err := txq.UpdatePerson(ctx, store.UpdatePersonParams{
				ID: id, GivenName: next.GivenName, FamilyName: next.FamilyName, DisplayName: next.DisplayName,
				BirthDate: next.BirthDate, Gender: next.Gender, WorkEmail: next.WorkEmail, PhoneWork: next.PhoneWork,
				PhoneMobile: next.PhoneMobile, PhonePrivate: next.PhonePrivate, PrivateEmail: next.PrivateEmail,
				Street: next.Street, PostalCode: next.PostalCode, City: next.City, Country: next.Country,
				WorkCountry: next.WorkCountry, PreferredLanguage: next.PreferredLanguage, Notes: next.Notes,
				Now: s.deps.Clock(),
			}); err != nil {
				return fmt.Errorf("staff: change person %d's own fields: %w", id, err)
			}
			if err := s.recordChanges(ctx, txq, id, eventPersonUpdated, who, diff); err != nil {
				return err
			}
		}
		before, err := txq.ListNextOfKin(ctx, id)
		if err != nil {
			return fmt.Errorf("staff: list person %d's next of kin: %w", id, err)
		}
		if err := txq.DeleteNextOfKin(ctx, id); err != nil {
			return fmt.Errorf("staff: clear person %d's next of kin: %w", id, err)
		}
		for i, k := range parsed.NextOfKin {
			if err := txq.InsertNextOfKin(ctx, store.InsertNextOfKinParams{
				PersonID: id, Position: int16(i), Name: k.Name, Relationship: string(k.Relationship),
				Phone: k.Phone, Note: k.Note, Now: s.deps.Clock(),
			}); err != nil {
				return fmt.Errorf("staff: add person %d's next of kin: %w", id, err)
			}
		}
		after, err := txq.ListNextOfKin(ctx, id)
		if err != nil {
			return fmt.Errorf("staff: list person %d's next of kin: %w", id, err)
		}
		if c, differ := listChange(mapRows(before, nextOfKinResponse), mapRows(after, nextOfKinResponse)); differ {
			// One write moves the revision on once: UpdatePerson already did
			// when the record changed too.
			if len(diff) == 0 {
				if _, err := bumpPersonRevision(ctx, txq, id, s.deps.Clock()); err != nil {
					return err
				}
			}
			if err := s.recordChanges(ctx, txq, id, eventNextOfKinUpdated, who, changes{"nextOfKin": c}); err != nil {
				return err
			}
		}
		if saved, err = readPerson(ctx, txq, id); err != nil {
			return err
		}
		link, err = linkedUserID(ctx, txq, id)
		return err
	})
	if errors.Is(err, errPersonNotFound) || errors.Is(err, errNoPerson) {
		return gen.PutStaffMe404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusConflict:
			return gen.PutStaffMe409ApplicationProblemPlusJSONResponse(r.problem), nil
		case http.StatusNotFound:
			return gen.PutStaffMe404ApplicationProblemPlusJSONResponse(noPerson()), nil
		default:
			return gen.PutStaffMe400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, err
	}
	out, err := s.respondSelf(ctx, caller, saved, link)
	if err != nil {
		return nil, err
	}
	return gen.PutStaffMe200JSONResponse(out), nil
}

// GetStaffMeEvents List my own history
// (GET /api/v1/staff/me/events)
//
// The person's history as they read it: each payload shaped by the Self
// audience, so a change to HR's notes is left out here — the field dropped,
// not "changed" — even for a caller who is HR.
func (s *server) GetStaffMeEvents(ctx context.Context, req gen.GetStaffMeEventsRequestObject) (gen.GetStaffMeEventsResponseObject, error) {
	if f := validatePageParams(req.Params.Page, req.Params.PageSize); f.failed() {
		return gen.GetStaffMeEvents400ApplicationProblemPlusJSONResponse(invalid(invalidPagingTitle, f)), nil
	}
	page, pageSize := pageParams(req.Params.Page, req.Params.PageSize)
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if caller.Person == nil {
		return gen.GetStaffMeEvents404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	id := *caller.Person
	var (
		rows  []store.ListPersonEventsRow
		total int64
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if total, err = q.CountPersonEvents(ctx, store.CountPersonEventsParams{
			PersonID: &id, WithHealth: selfAudience.reads(groupHealth), WithHold: selfAudience.seesHoldEvents(), HoldKinds: holdEventKinds, AbsenceKinds: absenceEventKinds,
		}); err != nil {
			return fmt.Errorf("staff: count person %d's events: %w", id, err)
		}
		rows, err = q.ListPersonEvents(ctx, store.ListPersonEventsParams{
			PersonID: &id, WithHealth: selfAudience.reads(groupHealth), WithHold: selfAudience.seesHoldEvents(), HoldKinds: holdEventKinds, AbsenceKinds: absenceEventKinds,
			PageSize: pageSize, PageOffset: (page - 1) * pageSize,
		})
		if err != nil {
			return fmt.Errorf("staff: list person %d's events: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.GetStaffMeEvents200JSONResponse(gen.PaginatedResponseOfStaffEvent{
		Data:       mapRows(rows, func(r store.ListPersonEventsRow) gen.StaffEvent { return eventResponse(r, selfAudience, nil) }),
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}), nil
}
