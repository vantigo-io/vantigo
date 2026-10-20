package identity

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// errExpectNotBuilt answers Expect and Unexpect until the announced account
// (Entra design D4) is built — Task 3 of the delivery B plan replaces both
// with the real provider. A caller reads it as a failure to retry.
var errExpectNotBuilt = errors.New("identity: the announced account is not built yet")

// Expect records or replaces the announcement for spec.EmployeeNumber.
// Task 3: not yet built.
func (p *provisioning) Expect(context.Context, contracts.Actor, contracts.ExpectedAccount) error {
	return errExpectNotBuilt
}

// Unexpect withdraws an announcement nobody fulfilled. Task 3: not yet built.
func (p *provisioning) Unexpect(context.Context, contracts.Actor, string) error {
	return errExpectNotBuilt
}
