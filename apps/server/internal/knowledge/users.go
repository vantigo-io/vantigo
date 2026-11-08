package knowledge

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/gen"
)

// assignableLimit caps the member picker's answer.
const assignableLimit = 20

// ListKnowledgeAssignableUsers answers active users for the access dialog's
// member picker (K4), from contracts.UserDirectory: identity's only user
// listing needs the authorisation-management permission, which a writer of
// an article need not hold. SearchUsers answers active users only, agents
// included: *Share with Hanna* is an ordinary membership (virtual employees
// design V9a).
func (s *server) ListKnowledgeAssignableUsers(ctx context.Context, req gen.ListKnowledgeAssignableUsersRequestObject) (gen.ListKnowledgeAssignableUsersResponseObject, error) {
	limit := int32(assignableLimit)
	var f fieldErrors
	if req.Params.Limit != nil {
		if *req.Params.Limit < 1 || *req.Params.Limit > assignableLimit {
			f.add("limit", fmt.Sprintf("Must be between 1 and %d.", assignableLimit))
		}
		limit = *req.Params.Limit
	}
	query := ""
	if req.Params.Query != nil {
		query = strings.TrimSpace(*req.Params.Query)
		if utf8.RuneCountInString(query) > 200 {
			f.add("query", "At most 200 characters.")
		}
	}
	if err := f.err(); err != nil {
		return answer(ctx, s, err, answers[gen.ListKnowledgeAssignableUsersResponseObject]{
			badRequest: func(p gen.KnowledgeProblem) gen.ListKnowledgeAssignableUsersResponseObject {
				return gen.ListKnowledgeAssignableUsers400ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	found, err := s.deps.Users.SearchUsers(ctx, query, int(limit), contracts.IncludeAgents())
	if err != nil {
		return nil, fmt.Errorf("knowledge: search the users: %w", err)
	}
	out := make(gen.KnowledgeAssignableUserList, 0, len(found))
	for _, u := range found {
		if len(out) == int(limit) {
			break
		}
		out = append(out, gen.KnowledgeAssignableUser{UserId: u.ID, DisplayName: u.DisplayName})
	}
	return gen.ListKnowledgeAssignableUsers200JSONResponse(out), nil
}
