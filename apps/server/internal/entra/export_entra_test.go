//go:build entra

package entra

import (
	"context"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/entra/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// DeleteTenantGroup deletes the group oid in the tenant through the module's
// Graph client on d — DELETE /groups/{id}, the connection's credential —
// with no row and no event: the tenant test's cleanup of the group it made,
// which DeleteGroup retires rather than deletes when it is a security group.
func DeleteTenantGroup(ctx context.Context, d module.Deps, oid uuid.UUID) error {
	c := newGraphClient(d)
	cred, err := credentialFor(ctx, store.New(d.Pool), c)
	if err != nil {
		return err
	}
	return c.del(ctx, cred, groupPath(oid))
}
