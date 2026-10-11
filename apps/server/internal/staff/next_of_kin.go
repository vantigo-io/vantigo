package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is a person's next of kin (design §3.1): at most three, in the
// order HR lists them, the private group — so read by the person and HR, and
// replaced whole by HR, each change one next_of_kin_updated event naming the
// list before and after.

const (
	nextOfKinMax     = 3
	nextOfKinNameMax = 200
	nextOfKinNoteMax = 500
)

var relationships = []string{"spouse", "partner", "parent", "child", "sibling", "other"}

// parseNextOfKin judges a replace: at most three, each field by its rule,
// named by the next of kin's place in the list (nextOfKin[1].name).
func parseNextOfKin(body gen.StaffNextOfKinRequest) ([]gen.StaffNextOfKin, fieldErrors) {
	var f fieldErrors
	if len(body.NextOfKin) > nextOfKinMax {
		f.add("nextOfKin", fmt.Sprintf("A person has at most %d next of kin, but %d were given", nextOfKinMax, len(body.NextOfKin)))
		return nil, f
	}
	out := make([]gen.StaffNextOfKin, 0, len(body.NextOfKin))
	for i, k := range body.NextOfKin {
		field := func(name string) string { return fmt.Sprintf("nextOfKin[%d].%s", i, name) }
		var parsed gen.StaffNextOfKin
		var msg string
		parsed.Name, msg = requiredText("A name", k.Name, nextOfKinNameMax)
		f.add(field("name"), msg)
		relationship, msg := oneOf("A relationship", string(k.Relationship), relationships)
		f.add(field("relationship"), msg)
		if relationship != nil {
			parsed.Relationship = gen.StaffNextOfKinRelationship(*relationship)
		}
		parsed.Phone, msg = optionalText("A phone", k.Phone, phoneMaxLength)
		f.add(field("phone"), msg)
		parsed.Note, msg = optionalText("A note", k.Note, nextOfKinNoteMax)
		f.add(field("note"), msg)
		out = append(out, parsed)
	}
	return out, f
}

func nextOfKinResponse(row store.ListNextOfKinRow) gen.StaffNextOfKin {
	return gen.StaffNextOfKin{
		Name: row.Name, Relationship: gen.StaffNextOfKinRelationship(row.Relationship), Phone: row.Phone, Note: row.Note,
	}
}

// listChange is a list field's before and after for a payload — either absent
// when the list was empty — and whether the two differ at all, judged on what
// a client reads.
func listChange[T any](before, after []T) (fieldChange, bool) {
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) == string(b) {
		return fieldChange{}, false
	}
	var c fieldChange
	if len(before) > 0 {
		c.From = before
	}
	if len(after) > 0 {
		c.To = after
	}
	return c, true
}

// GetStaffPeopleByIdNextOfKin List a person's next of kin
// (GET /api/v1/staff/people/{id}/next-of-kin)
//
// The private group: to an audience that may see the person but not it — a
// colleague — the list is the 404 an unknown id is, as for anyone else.
func (s *server) GetStaffPeopleByIdNextOfKin(ctx context.Context, req gen.GetStaffPeopleByIdNextOfKinRequestObject) (gen.GetStaffPeopleByIdNextOfKinResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var (
		rows     []store.ListNextOfKinRow
		revision int32
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		person, err := readPerson(ctx, q, req.Id)
		if err != nil {
			return err
		}
		revision = person.Revision
		a, err := personAudience(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		if !a.show("nextOfKin") {
			return notFound()
		}
		if rows, err = q.ListNextOfKin(ctx, req.Id); err != nil {
			return fmt.Errorf("staff: list person %d's next of kin: %w", req.Id, err)
		}
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdNextOfKin404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdNextOfKin200JSONResponse(gen.StaffNextOfKinList{NextOfKin: mapRows(rows, nextOfKinResponse), Revision: revision}), nil
}

// PutStaffPeopleByIdNextOfKin Replace a person's next of kin
// (PUT /api/v1/staff/people/{id}/next-of-kin)
func (s *server) PutStaffPeopleByIdNextOfKin(ctx context.Context, req gen.PutStaffPeopleByIdNextOfKinRequestObject) (gen.PutStaffPeopleByIdNextOfKinResponseObject, error) {
	var body gen.StaffNextOfKinRequest
	if req.Body != nil {
		body = *req.Body
	}
	list, f := parseNextOfKin(body)
	if f.failed() {
		return gen.PutStaffPeopleByIdNextOfKin400ApplicationProblemPlusJSONResponse(invalid(invalidNextOfKinTitle, f)), nil
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffPeopleByIdNextOfKin400ApplicationProblemPlusJSONResponse(invalid(invalidNextOfKinTitle, badIfMatch())), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var rows []store.ListNextOfKinRow
	err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		person, err := readPerson(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if err := checkRevision("person", person.Revision, revision); err != nil {
			return err
		}
		before, err := txq.ListNextOfKin(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("staff: list person %d's next of kin: %w", req.Id, err)
		}
		if err := txq.DeleteNextOfKin(ctx, req.Id); err != nil {
			return fmt.Errorf("staff: clear person %d's next of kin: %w", req.Id, err)
		}
		for i, k := range list {
			if err := txq.InsertNextOfKin(ctx, store.InsertNextOfKinParams{
				PersonID: req.Id, Position: int16(i), Name: k.Name, Relationship: string(k.Relationship),
				Phone: k.Phone, Note: k.Note, Now: s.deps.Clock(),
			}); err != nil {
				return fmt.Errorf("staff: add person %d's next of kin: %w", req.Id, err)
			}
		}
		if rows, err = txq.ListNextOfKin(ctx, req.Id); err != nil {
			return fmt.Errorf("staff: list person %d's next of kin: %w", req.Id, err)
		}
		c, differ := listChange(mapRows(before, nextOfKinResponse), mapRows(rows, nextOfKinResponse))
		if !differ {
			return nil
		}
		if revision, err = bumpPersonRevision(ctx, txq, req.Id, s.deps.Clock()); err != nil {
			return err
		}
		return s.recordChanges(ctx, txq, req.Id, eventNextOfKinUpdated, who, changes{"nextOfKin": c})
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PutStaffPeopleByIdNextOfKin404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		return gen.PutStaffPeopleByIdNextOfKin409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: replace person %d's next of kin: %w", req.Id, err)
	}
	return gen.PutStaffPeopleByIdNextOfKin200JSONResponse(gen.StaffNextOfKinList{NextOfKin: mapRows(rows, nextOfKinResponse), Revision: revision}), nil
}
