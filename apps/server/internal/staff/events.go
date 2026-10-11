package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the history (design S21, §3.6): every mutation of a person is
// one event, written in the same transaction as the change and attributed to
// an actor snapshotted from the user directory before the transaction opened.
// A payload names the fields a change touched with their values before and
// after; who reads which value is shaping.go's per-field table, applied when
// the history is read. payload_classes, the retention classes the payload
// touches, is for the erase alone (delivery A2).

// The event kinds delivery A1's Task 3a writes.
const (
	eventPersonCreated     = "person_created"
	eventPersonUpdated     = "person_updated"
	eventNextOfKinUpdated  = "next_of_kin_updated"
	eventRolesUpdated      = "roles_updated"
	eventPersonDeleted     = "person_deleted"
	actorKindUser          = "user"
	actorKindSystem        = "system"
	unknownUserDisplay     = "Unknown user"
	systemActorDisplayName = "System"
)

// actor is who an event is attributed to: the three values every event
// carries (actor_kind, actor_display, actor_user_id), resolved once per
// request — the customers module's shape (internal/customers/actor.go),
// copied because no module imports another.
type actor struct {
	Kind    string
	Display string
	UserID  *uuid.UUID
}

// actorFor resolves the request's principal into the actor its events are
// attributed to. It reads the user directory, so it is called before the
// transaction opens (contractscalls.go refuses it inside one). A user the
// directory no longer knows is still the actor, as "Unknown user", so the
// record exists although the account is gone; a lookup that fails fails the
// write, since history that guesses at who wrote it is worse than a 500.
// Every operation of this module is authenticated, so the system actor is
// the answer only to a caller that some later path makes without a user.
func (s *server) actorFor(ctx context.Context) (actor, error) {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok || p.SCIM || p.UserID == uuid.Nil {
		return actor{Kind: actorKindSystem, Display: systemActorDisplayName}, nil
	}
	user, err := s.usersUser(ctx, p.UserID)
	if err != nil {
		return actor{}, fmt.Errorf("staff: resolve the actor: %w", err)
	}
	id := p.UserID
	if user == nil {
		return actor{Kind: actorKindUser, Display: unknownUserDisplay, UserID: &id}, nil
	}
	return actor{Kind: actorKindUser, Display: user.DisplayName, UserID: &id}, nil
}

// fieldChange is one field's before and after in a payload. Either is absent
// when the field was empty on that side.
type fieldChange struct {
	From any `json:"from,omitempty"`
	To   any `json:"to,omitempty"`
}

// changes is a payload's fields, by the contract's field names.
type changes map[string]fieldChange

// fieldClasses is the retention class (design §3.1, S15) of every field a
// payload can name, for payload_classes: what the erase must reach. It is not
// a visibility table — shaping.go's fieldGroups is.
var fieldClasses = map[string]string{
	"employeeNumber": "C", "givenName": "C", "familyName": "C", "displayName": "C",
	"birthDate": "C", "gender": "C", "workCountry": "C", "preferredLanguage": "C", "roles": "C",
	"workEmail": "D", "phoneWork": "D", "phoneMobile": "D", "phonePrivate": "D", "privateEmail": "D",
	"street": "D", "postalCode": "D", "city": "D", "country": "D", "notes": "D", "nextOfKin": "D",
}

// classes is the retention classes c's fields belong to, sorted and each
// once. A field the table does not know counts as D, the class the erase
// blanks, so an unknown field is erased rather than kept.
func (c changes) classes() []string {
	var out []string
	for name := range c {
		class, ok := fieldClasses[name]
		if !ok {
			class = "D"
		}
		if !slices.Contains(out, class) {
			out = append(out, class)
		}
	}
	slices.Sort(out)
	return out
}

// recordEvent appends one event inside the caller's transaction, so the
// change and its history commit or roll back together. personID nil is an
// installation-level event.
func (s *server) recordEvent(ctx context.Context, txq *store.Queries, personID *int64, kind string, who actor, payload any, classes []string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("staff: encode a %s event: %w", kind, err)
	}
	if classes == nil {
		classes = []string{}
	}
	display := who.Display
	if err := txq.InsertEvent(ctx, store.InsertEventParams{
		PersonID: personID, Kind: kind, ActorKind: who.Kind, ActorUserID: who.UserID, ActorDisplay: &display,
		At: s.deps.Clock(), Payload: body, PayloadClasses: classes,
	}); err != nil {
		return fmt.Errorf("staff: record a %s event: %w", kind, err)
	}
	return nil
}

// recordChanges records kind for personID with c as its fields; nothing when
// c is empty, since a write that changed nothing has nothing to say.
func (s *server) recordChanges(ctx context.Context, txq *store.Queries, personID int64, kind string, who actor, c changes) error {
	if len(c) == 0 {
		return nil
	}
	return s.recordEvent(ctx, txq, &personID, kind, who, map[string]any{"fields": c}, c.classes())
}

// GetStaffPeopleByIdEvents List a person's history
// (GET /api/v1/staff/people/{id}/events)
//
// The person themselves, their manager (Task 4) and HR read it; to anyone
// else — a colleague holding staff:view included — the person's history is
// the 404 an unknown id is.
func (s *server) GetStaffPeopleByIdEvents(ctx context.Context, req gen.GetStaffPeopleByIdEventsRequestObject) (gen.GetStaffPeopleByIdEventsResponseObject, error) {
	if f := validatePageParams(req.Params.Page, req.Params.PageSize); f.failed() {
		return gen.GetStaffPeopleByIdEvents400ApplicationProblemPlusJSONResponse(invalid(invalidPagingTitle, f)), nil
	}
	page, pageSize := pageParams(req.Params.Page, req.Params.PageSize)
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var (
		a     audience
		rows  []store.ListPersonEventsRow
		total int64
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := q.GetPerson(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: read person %d: %w", req.Id, err)
		}
		manager, err := managesPerson(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		if a = caller.audienceOn(req.Id, manager); !a.seesEvents() {
			return notFound()
		}
		if total, err = q.CountPersonEvents(ctx, &req.Id); err != nil {
			return fmt.Errorf("staff: count person %d's events: %w", req.Id, err)
		}
		rows, err = q.ListPersonEvents(ctx, store.ListPersonEventsParams{
			PersonID: &req.Id, PageSize: pageSize, PageOffset: (page - 1) * pageSize,
		})
		if err != nil {
			return fmt.Errorf("staff: list person %d's events: %w", req.Id, err)
		}
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdEvents404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdEvents200JSONResponse(gen.PaginatedResponseOfStaffEvent{
		Data:       mapRows(rows, func(r store.ListPersonEventsRow) gen.StaffEvent { return eventResponse(r, a) }),
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}), nil
}
