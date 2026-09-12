package customers

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// Every operation of customers.yaml, each answering module.ErrNotImplemented,
// which the strict server's response-error handler turns into a 501. Mounting
// them all is what satisfies module.Router's "never registered" check, so the
// contract is fully routed from the first commit and each later task replaces
// the stubs of the area it implements. Contacts and customer-contact
// associations (Task 7), customer CRUD/stats (Task 6) and legal identity and
// the Brreg lookup (Task 8) are implemented in contacts.go,
// customers.go/stats.go and legal_identity.go/brreg.go respectively; the
// timeline remains here.

// GetCustomersByIdTimeline List a customer's timeline
// (GET /api/v1/customers/{id}/timeline)
func (s *server) GetCustomersByIdTimeline(context.Context, gen.GetCustomersByIdTimelineRequestObject) (gen.GetCustomersByIdTimelineResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostCustomersByIdTimeline Create a manual customer timeline entry
// (POST /api/v1/customers/{id}/timeline)
func (s *server) PostCustomersByIdTimeline(context.Context, gen.PostCustomersByIdTimelineRequestObject) (gen.PostCustomersByIdTimelineResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeleteCustomersByIdTimelineByEntryId Delete a manual customer timeline entry
// (DELETE /api/v1/customers/{id}/timeline/{entryId})
func (s *server) DeleteCustomersByIdTimelineByEntryId(context.Context, gen.DeleteCustomersByIdTimelineByEntryIdRequestObject) (gen.DeleteCustomersByIdTimelineByEntryIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCustomersByIdTimelineByEntryId Get a customer timeline entry
// (GET /api/v1/customers/{id}/timeline/{entryId})
func (s *server) GetCustomersByIdTimelineByEntryId(context.Context, gen.GetCustomersByIdTimelineByEntryIdRequestObject) (gen.GetCustomersByIdTimelineByEntryIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PutCustomersByIdTimelineByEntryId Update a manual customer timeline entry
// (PUT /api/v1/customers/{id}/timeline/{entryId})
func (s *server) PutCustomersByIdTimelineByEntryId(context.Context, gen.PutCustomersByIdTimelineByEntryIdRequestObject) (gen.PutCustomersByIdTimelineByEntryIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCustomersByIdTimelineByEntryIdRevisions List timeline entry revisions
// (GET /api/v1/customers/{id}/timeline/{entryId}/revisions)
func (s *server) GetCustomersByIdTimelineByEntryIdRevisions(context.Context, gen.GetCustomersByIdTimelineByEntryIdRevisionsRequestObject) (gen.GetCustomersByIdTimelineByEntryIdRevisionsResponseObject, error) {
	return nil, module.ErrNotImplemented
}
