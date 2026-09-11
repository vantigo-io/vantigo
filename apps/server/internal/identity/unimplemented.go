package identity

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// Every gen.StrictServerInterface operation not yet implemented answers 501
// through module.ResponseError. Generated once from the interface; each area
// moves the operations it implements into its own file and deletes them
// here, so the build keeps proving the interface is complete. The test
// package's pendingOperations lists exactly these operations.

func (*server) DeleteIdentityScimV2GroupsById(context.Context, gen.DeleteIdentityScimV2GroupsByIdRequestObject) (gen.DeleteIdentityScimV2GroupsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) DeleteIdentityScimV2UsersById(context.Context, gen.DeleteIdentityScimV2UsersByIdRequestObject) (gen.DeleteIdentityScimV2UsersByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityScimV2Groups(context.Context, gen.GetIdentityScimV2GroupsRequestObject) (gen.GetIdentityScimV2GroupsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityScimV2GroupsById(context.Context, gen.GetIdentityScimV2GroupsByIdRequestObject) (gen.GetIdentityScimV2GroupsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityScimV2ResourceTypes(context.Context, gen.GetIdentityScimV2ResourceTypesRequestObject) (gen.GetIdentityScimV2ResourceTypesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityScimV2Schemas(context.Context, gen.GetIdentityScimV2SchemasRequestObject) (gen.GetIdentityScimV2SchemasResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityScimV2ServiceProviderConfig(context.Context, gen.GetIdentityScimV2ServiceProviderConfigRequestObject) (gen.GetIdentityScimV2ServiceProviderConfigResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityScimV2Users(context.Context, gen.GetIdentityScimV2UsersRequestObject) (gen.GetIdentityScimV2UsersResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) GetIdentityScimV2UsersById(context.Context, gen.GetIdentityScimV2UsersByIdRequestObject) (gen.GetIdentityScimV2UsersByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PatchIdentityScimV2GroupsById(context.Context, gen.PatchIdentityScimV2GroupsByIdRequestObject) (gen.PatchIdentityScimV2GroupsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PatchIdentityScimV2UsersById(context.Context, gen.PatchIdentityScimV2UsersByIdRequestObject) (gen.PatchIdentityScimV2UsersByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityScimV2Groups(context.Context, gen.PostIdentityScimV2GroupsRequestObject) (gen.PostIdentityScimV2GroupsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PostIdentityScimV2Users(context.Context, gen.PostIdentityScimV2UsersRequestObject) (gen.PostIdentityScimV2UsersResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (*server) PutIdentityScimV2UsersById(context.Context, gen.PutIdentityScimV2UsersByIdRequestObject) (gen.PutIdentityScimV2UsersByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}
