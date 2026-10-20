package entra

import (
	"context"
	"fmt"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
)

// GetEntraGroups answers every group the module keeps in the tenant for
// Staff's groups, deleted ones included, by display name — from the
// module's rows, nothing asked of the tenant. entra:manage's, as the router
// checks. (GET /api/v1/entra/groups)
func (s *server) GetEntraGroups(ctx context.Context, _ gen.GetEntraGroupsRequestObject) (gen.GetEntraGroupsResponseObject, error) {
	rows, err := s.q.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("entra: list the groups: %w", err)
	}
	out := make(gen.GetEntraGroups200JSONResponse, 0, len(rows))
	for _, g := range rows {
		out = append(out, gen.EntraGroup{
			ObjectId:      openapi_types.UUID(g.ObjectID),
			GroupId:       g.GroupID,
			Key:           g.Key,
			DisplayName:   g.DisplayName,
			MailNickname:  g.MailNickname,
			Type:          gen.EntraGroupType(g.Type),
			Team:          g.TeamCreatedAt != nil,
			State:         gen.EntraGroupState(g.State),
			CreatedByUs:   g.CreatedByUs,
			LastMembersAt: g.LastMembersAt,
			CreatedAt:     g.CreatedAt,
		})
	}
	return out, nil
}
