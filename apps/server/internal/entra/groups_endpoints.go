package entra

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// GetEntraGroups answers every group the module keeps in the tenant for
// Staff's groups, deleted and retired ones included, by display name — from
// the module's rows, nothing asked of the tenant. entra:manage's, as the
// router checks. (GET /api/v1/entra/groups)
func (s *server) GetEntraGroups(ctx context.Context, _ gen.GetEntraGroupsRequestObject) (gen.GetEntraGroupsResponseObject, error) {
	rows, err := s.q.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("entra: list the groups: %w", err)
	}
	out := make(gen.GetEntraGroups200JSONResponse, 0, len(rows))
	for _, g := range rows {
		out = append(out, groupBody(g))
	}
	return out, nil
}

// groupBody is a group's row as the contract answers it.
func groupBody(g store.EntraGroup) gen.EntraGroup {
	return gen.EntraGroup{
		ObjectId:            openapi_types.UUID(g.ObjectID),
		GroupId:             g.GroupID,
		Key:                 g.Key,
		DisplayName:         g.DisplayName,
		MailNickname:        g.MailNickname,
		Type:                gen.EntraGroupType(g.Type),
		Team:                g.TeamCreatedAt != nil,
		State:               gen.EntraGroupState(g.State),
		CreatedByUs:         g.CreatedByUs,
		AdoptionConfirmedAt: g.AdoptionConfirmedAt,
		LastMembersAt:       g.LastMembersAt,
		CreatedAt:           g.CreatedAt,
	}
}

// PostEntraGroupsByObjectIdConfirm confirms the adoption of a group the
// module found by its mail nickname rather than made: from now on EnsureGroup,
// SetMembers and DeleteGroup write to it as to one the module made — though
// the module never deletes it in the tenant — and Staff's next cycle brings
// it to the Staff group. The confirmation and the actor are kept on the row
// and audited entra.group.adoption_confirmed, once. Idempotent: a group
// confirmed already, or one the module made, is answered as it is. 404 for
// a group the module does not keep. (POST /api/v1/entra/groups/{objectId}/confirm)
func (s *server) PostEntraGroupsByObjectIdConfirm(ctx context.Context, req gen.PostEntraGroupsByObjectIdConfirmRequestObject) (gen.PostEntraGroupsByObjectIdConfirmResponseObject, error) {
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	id := uuid.UUID(req.ObjectId)
	now := s.deps.Clock()
	var (
		row   store.EntraGroup
		found bool
	)
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		before, err := q.GetGroup(ctx, id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("entra: read the group: %w", err)
		}
		found = true
		if row, err = q.ConfirmGroupAdoption(ctx, store.ConfirmGroupAdoptionParams{Now: now, ConfirmedByUserID: *actor.UserID, ObjectID: id}); err != nil {
			return fmt.Errorf("entra: confirm the group's adoption: %w", err)
		}
		if before.CreatedByUs || before.AdoptionConfirmedAt != nil {
			return nil
		}
		return recordEvent(ctx, q, now, actor, actionGroupAdoptionConfirmed, &id, "", nil)
	})
	switch {
	case err != nil:
		return nil, err
	case !found:
		return gen.PostEntraGroupsByObjectIdConfirm404Response{}, nil
	}
	return gen.PostEntraGroupsByObjectIdConfirm200JSONResponse(groupBody(row)), nil
}
