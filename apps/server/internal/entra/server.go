package entra

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, the module's contract
// operations.
type server struct {
	deps module.Deps
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server {
	return &server{deps: d}
}

// notBuilt is what an operation answers until its task builds it: the
// platform's module.ErrNotImplemented, which the response error handler
// turns into a 501 rather than a 500. Task 3 replaces every use.
func notBuilt(operation string) error {
	return fmt.Errorf("entra: %s: %w", operation, module.ErrNotImplemented)
}

// GetEntraConnection answers the connection. (GET /api/v1/entra/connection)
func (s *server) GetEntraConnection(context.Context, gen.GetEntraConnectionRequestObject) (gen.GetEntraConnectionResponseObject, error) {
	// Task 3 implements this.
	return nil, notBuilt("getEntraConnection")
}

// PutEntraConnection sets the tenant and the client. (PUT /api/v1/entra/connection)
func (s *server) PutEntraConnection(context.Context, gen.PutEntraConnectionRequestObject) (gen.PutEntraConnectionResponseObject, error) {
	// Task 3 implements this.
	return nil, notBuilt("putEntraConnection")
}

// PostEntraConnectionCredentials adds a client secret.
// (POST /api/v1/entra/connection/credentials)
func (s *server) PostEntraConnectionCredentials(context.Context, gen.PostEntraConnectionCredentialsRequestObject) (gen.PostEntraConnectionCredentialsResponseObject, error) {
	// Task 3 implements this.
	return nil, notBuilt("postEntraConnectionCredentials")
}

// DeleteEntraConnectionCredentialsById revokes a client secret.
// (DELETE /api/v1/entra/connection/credentials/{id})
func (s *server) DeleteEntraConnectionCredentialsById(context.Context, gen.DeleteEntraConnectionCredentialsByIdRequestObject) (gen.DeleteEntraConnectionCredentialsByIdResponseObject, error) {
	// Task 3 implements this.
	return nil, notBuilt("deleteEntraConnectionCredentialsById")
}

// PostEntraConnectionTest tests the connection.
// (POST /api/v1/entra/connection/test)
func (s *server) PostEntraConnectionTest(context.Context, gen.PostEntraConnectionTestRequestObject) (gen.PostEntraConnectionTestResponseObject, error) {
	// Task 3 implements this.
	return nil, notBuilt("postEntraConnectionTest")
}

// PutEntraConnectionSettings sets the account rules.
// (PUT /api/v1/entra/connection/settings)
func (s *server) PutEntraConnectionSettings(context.Context, gen.PutEntraConnectionSettingsRequestObject) (gen.PutEntraConnectionSettingsResponseObject, error) {
	// Task 3 implements this.
	return nil, notBuilt("putEntraConnectionSettings")
}

// GetEntraConnectionSkus answers the tenant's licences.
// (GET /api/v1/entra/connection/skus)
func (s *server) GetEntraConnectionSkus(context.Context, gen.GetEntraConnectionSkusRequestObject) (gen.GetEntraConnectionSkusResponseObject, error) {
	// Task 3 implements this.
	return nil, notBuilt("getEntraConnectionSkus")
}

// GetEntraEvents answers the module's recent events. (GET /api/v1/entra/events)
func (s *server) GetEntraEvents(context.Context, gen.GetEntraEventsRequestObject) (gen.GetEntraEventsResponseObject, error) {
	// Task 3 implements this.
	return nil, notBuilt("getEntraEvents")
}

// GetEntraAccounts answers the module's accounts. (GET /api/v1/entra/accounts)
func (s *server) GetEntraAccounts(context.Context, gen.GetEntraAccountsRequestObject) (gen.GetEntraAccountsResponseObject, error) {
	// Task 3 implements this.
	return nil, notBuilt("getEntraAccounts")
}
