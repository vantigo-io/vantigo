package entra

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
)

// STUB (Entra delivery D, plan task 2): GET /groups as the contract declares
// it, answering the uniform 403 until plan task 4 implements it in
// groups_endpoints.go and deletes this file. It is listed in main_test.go's
// pendingOperations meanwhile.

// GetEntraGroups is a stub (task 4). (GET /api/v1/entra/groups)
func (s *server) GetEntraGroups(context.Context, gen.GetEntraGroupsRequestObject) (gen.GetEntraGroupsResponseObject, error) {
	return gen.GetEntraGroups403JSONResponse(apicommon.ForbiddenBody()), nil
}
