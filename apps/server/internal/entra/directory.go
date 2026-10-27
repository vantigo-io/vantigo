package entra

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
)

// The directory picker and the exact matches (Entra phase 2, delivery B;
// the plan's readings 3 and 4): an Owner's two reads of the tenant's
// accounts, for connecting existing users to the workforce provider. Until
// they are built both answer 503 tenant_unreachable.

// GetEntraDirectoryUsers searches the tenant's accounts. Not built yet: it
// answers 503 tenant_unreachable.
func (s *server) GetEntraDirectoryUsers(context.Context, gen.GetEntraDirectoryUsersRequestObject) (gen.GetEntraDirectoryUsersResponseObject, error) {
	return gen.GetEntraDirectoryUsers503ApplicationProblemPlusJSONResponse(tenantUnavailable(codeTenantUnreachable)), nil
}

// GetEntraDirectoryMatches proposes the tenant's exact matches for the users
// not yet connected. Not built yet: it answers 503 tenant_unreachable.
func (s *server) GetEntraDirectoryMatches(context.Context, gen.GetEntraDirectoryMatchesRequestObject) (gen.GetEntraDirectoryMatchesResponseObject, error) {
	return gen.GetEntraDirectoryMatches503ApplicationProblemPlusJSONResponse(tenantUnavailable(codeTenantUnreachable)), nil
}
