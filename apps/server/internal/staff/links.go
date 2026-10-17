package staff

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the self-service link (design S24 as amended, §3.6; plan
// reading 1): HR ties a person to an existing, active user, which makes that
// user the person themselves to this module — the Self audience, My page —
// and nothing else. The link is the identity provider's row in
// identity_links, state linked and managed false; it never makes the user
// managed, and delivery C's accounts worker leaves an unmanaged link alone.
// The user is read from the directory before the transaction opens; the
// rules that need the table — one person per user — are judged under the
// person's lock, and the unique index settles two writers that race.

// linkExternalIndex is the unique index that holds one person per user under
// a provider.
const linkExternalIndex = "ux_identity_links_provider_external"

// linkedElsewhere is the refusal of a user already linked to another person.
// It names nobody: who the user is linked to is the other person's link.
func linkedElsewhere() error {
	return refuseInvalid(invalidLinkTitle, fieldErrors{
		errs: fieldError("userId", "This user is linked to another person; unlink them there first"),
		code: codeUserLinkedElsewhere,
	})
}

// linkWriteError is the link write's own database refusal: the
// (provider, external_id) index's unique violation — a second writer that
// linked the user to another person between the check and the write — is
// user_linked_elsewhere, as if the check had seen it. Anything else is the
// error it was.
func linkWriteError(err error) error {
	if db.IsUniqueViolation(err, linkExternalIndex) {
		return linkedElsewhere()
	}
	return err
}

// personLink is a person's link under the identity provider on q, nil when
// they have none. A link may name no user — the accounts worker's preparing
// link, before identity has created one (00051) — which every reader takes
// as linked to nobody.
func personLink(ctx context.Context, q *store.Queries, personID int64) (*store.PersonLinkRow, error) {
	row, err := q.PersonLink(ctx, personID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: read person %d's link: %w", personID, err)
	}
	return &row, nil
}

// linkResponse is a link as the contract answers it; user is the linked user
// as the directory named them, read outside any transaction.
func linkResponse(row *store.PersonLinkRow, user *gen.StaffPersonUser) gen.StaffPersonLink {
	if row == nil || row.ExternalID == nil {
		return gen.StaffPersonLink{Linked: false}
	}
	state, managed := row.State, row.Managed
	return gen.StaffPersonLink{Linked: true, State: &state, Managed: &managed, User: user}
}

// linkName is a link's user as the directory named them.
type linkName struct{ id, name string }

// currentLinkName is the user a person is linked to now, read on the pool and
// named through the directory — never inside a transaction — and nil when
// the person is linked to nobody (no link, or one naming no user yet). A user
// the directory no longer knows is "Unknown user".
func (s *server) currentLinkName(ctx context.Context, personID int64) (*linkName, error) {
	current, err := personLink(ctx, store.New(s.deps.Pool), personID)
	if err != nil || current == nil || current.ExternalID == nil {
		return nil, err
	}
	user, err := s.linkedUser(ctx, current.ExternalID)
	if err != nil {
		return nil, err
	}
	return &linkName{id: *current.ExternalID, name: user.DisplayName}, nil
}

// GetStaffPeopleByIdUser Get a person's self-service link
// (GET /api/v1/staff/people/{id}/user)
//
// The link is the "Account flag, links" group of design §5: the person and
// HR read it; to anyone else it is the unknown id's 404.
func (s *server) GetStaffPeopleByIdUser(ctx context.Context, req gen.GetStaffPeopleByIdUserRequestObject) (gen.GetStaffPeopleByIdUserResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var row *store.PersonLinkRow
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := readPerson(ctx, q, req.Id); err != nil {
			return err
		}
		a, err := personAudience(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		if !a.show("user") {
			return notFound()
		}
		row, err = personLink(ctx, q, req.Id)
		return err
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdUser404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	var user *gen.StaffPersonUser
	if row != nil {
		if user, err = s.linkedUser(ctx, row.ExternalID); err != nil {
			return nil, err
		}
	}
	return gen.GetStaffPeopleByIdUser200JSONResponse(linkResponse(row, user)), nil
}

// PutStaffPeopleByIdUser Link a person to a user
// (PUT /api/v1/staff/people/{id}/user)
//
// userId names the user; null unlinks. The body must name userId, since the
// decoder reads a body without it as null, and an unlink is not what an empty
// body should mean. Linking to the user already linked writes nothing;
// linking to another replaces the link, one user_linked event from the old
// user to the new.
func (s *server) PutStaffPeopleByIdUser(ctx context.Context, req gen.PutStaffPeopleByIdUserRequestObject) (gen.PutStaffPeopleByIdUserResponseObject, error) {
	var body gen.StaffPersonLinkRequest
	if req.Body != nil {
		body = *req.Body
	}
	if !bodyHas(ctx, "userId") {
		return gen.PutStaffPeopleByIdUser400ApplicationProblemPlusJSONResponse(invalid(invalidLinkTitle, fieldErrors{
			errs: fieldError("userId", "userId is required: the id of the user to link, or null to unlink"),
		})), nil
	}
	var target *contracts.UserEntry
	if body.UserId != nil {
		user, err := s.usersUser(ctx, *body.UserId)
		if err != nil {
			return nil, fmt.Errorf("staff: read the user to link person %d to: %w", req.Id, err)
		}
		switch {
		case user == nil:
			return gen.PutStaffPeopleByIdUser400ApplicationProblemPlusJSONResponse(invalid(invalidLinkTitle, fieldErrors{
				errs: fieldError("userId", "No user has this id"),
			})), nil
		case !user.Active:
			return gen.PutStaffPeopleByIdUser400ApplicationProblemPlusJSONResponse(invalid(invalidLinkTitle, fieldErrors{
				errs: fieldError("userId", "This user is disabled; only an active user can be linked"),
				code: codeUserInactive,
			})), nil
		}
		target = user
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	// The user linked now, named by the directory before the transaction so
	// an unlink or a relink names who it took the person from, as a link
	// names who it gave them to. A link that changed in between is named by
	// its id alone.
	was, err := s.currentLinkName(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	from := func(current *store.PersonLinkRow) any {
		if current == nil || current.ExternalID == nil {
			return nil
		}
		out := map[string]any{"id": *current.ExternalID}
		if was != nil && was.id == *current.ExternalID {
			out["displayName"] = was.name
		}
		return out
	}

	// An unlink is one of the writes an erased person still allows (plan
	// reading 7), so the user can be linked to someone else; a link is not.
	rule := refuseErased
	if target == nil {
		rule = allowErased
	}
	var row *store.PersonLinkRow
	err = s.withPersonLock(ctx, req.Id, rule, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := personLink(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if target == nil {
			if current == nil {
				return nil
			}
			if err := txq.DeletePersonLink(ctx, req.Id); err != nil {
				return fmt.Errorf("staff: unlink person %d: %w", req.Id, err)
			}
			return s.recordChanges(ctx, txq, req.Id, eventUserUnlinked, who, changes{"user": fieldChange{From: from(current)}})
		}
		id := target.ID.String()
		if current != nil && current.ExternalID != nil && *current.ExternalID == id {
			row = current
			return nil
		}
		other, err := personForUser(ctx, txq, target.ID)
		if err != nil {
			return err
		}
		if other != nil && *other != req.Id {
			return linkedElsewhere()
		}
		if err := txq.SetPersonLink(ctx, store.SetPersonLinkParams{PersonID: req.Id, ExternalID: id, Now: s.deps.Clock()}); err != nil {
			return linkWriteError(fmt.Errorf("staff: link person %d: %w", req.Id, err))
		}
		if row, err = personLink(ctx, txq, req.Id); err != nil {
			return err
		}
		return s.recordChanges(ctx, txq, req.Id, eventUserLinked, who, changes{"user": fieldChange{
			From: from(current),
			To:   map[string]any{"id": id, "displayName": target.DisplayName},
		}})
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PutStaffPeopleByIdUser404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffPeopleByIdUser404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffPeopleByIdUser409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PutStaffPeopleByIdUser400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	// The user was read from the directory before the transaction; it is the
	// one the link now names, so it is answered without reading it again.
	var user *gen.StaffPersonUser
	if row != nil && target != nil {
		user = userResponse(target.ID, target.DisplayName, target.Active)
	}
	return gen.PutStaffPeopleByIdUser200JSONResponse(linkResponse(row, user)), nil
}
