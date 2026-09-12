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
// the stubs of the area it implements.

// GetCustomers List all customers
// (GET /api/v1/customers)
func (s *server) GetCustomers(context.Context, gen.GetCustomersRequestObject) (gen.GetCustomersResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostCustomers Create a new customer
// (POST /api/v1/customers)
func (s *server) PostCustomers(context.Context, gen.PostCustomersRequestObject) (gen.PostCustomersResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCustomersContacts List all contacts
// (GET /api/v1/customers/contacts)
func (s *server) GetCustomersContacts(context.Context, gen.GetCustomersContactsRequestObject) (gen.GetCustomersContactsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostCustomersContacts Create a new contact
// (POST /api/v1/customers/contacts)
func (s *server) PostCustomersContacts(context.Context, gen.PostCustomersContactsRequestObject) (gen.PostCustomersContactsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeleteCustomersContactsById Delete a contact
// (DELETE /api/v1/customers/contacts/{id})
func (s *server) DeleteCustomersContactsById(context.Context, gen.DeleteCustomersContactsByIdRequestObject) (gen.DeleteCustomersContactsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetContact Get a contact by id
// (GET /api/v1/customers/contacts/{id})
func (s *server) GetContact(context.Context, gen.GetContactRequestObject) (gen.GetContactResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PutCustomersContactsById Update a contact
// (PUT /api/v1/customers/contacts/{id})
func (s *server) PutCustomersContactsById(context.Context, gen.PutCustomersContactsByIdRequestObject) (gen.PutCustomersContactsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCustomersContactsByIdCustomers List the customers a contact is associated with
// (GET /api/v1/customers/contacts/{id}/customers)
func (s *server) GetCustomersContactsByIdCustomers(context.Context, gen.GetCustomersContactsByIdCustomersRequestObject) (gen.GetCustomersContactsByIdCustomersResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCustomersLookupBrreg Look up business entities in Brønnøysundregisteret
// (GET /api/v1/customers/lookup/brreg)
func (s *server) GetCustomersLookupBrreg(context.Context, gen.GetCustomersLookupBrregRequestObject) (gen.GetCustomersLookupBrregResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCustomersStats Get tenant-wide customer key figures
// (GET /api/v1/customers/stats)
func (s *server) GetCustomersStats(context.Context, gen.GetCustomersStatsRequestObject) (gen.GetCustomersStatsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCustomersStatsAttention Get customer dashboard attention items
// (GET /api/v1/customers/stats/attention)
func (s *server) GetCustomersStatsAttention(context.Context, gen.GetCustomersStatsAttentionRequestObject) (gen.GetCustomersStatsAttentionResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCustomersStatsSummary Get customer dashboard summary
// (GET /api/v1/customers/stats/summary)
func (s *server) GetCustomersStatsSummary(context.Context, gen.GetCustomersStatsSummaryRequestObject) (gen.GetCustomersStatsSummaryResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCustomersStatsTimeseries Get customer dashboard time series
// (GET /api/v1/customers/stats/timeseries)
func (s *server) GetCustomersStatsTimeseries(context.Context, gen.GetCustomersStatsTimeseriesRequestObject) (gen.GetCustomersStatsTimeseriesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeleteCustomersById Archive a customer (customers are never hard-deleted)
// (DELETE /api/v1/customers/{id})
func (s *server) DeleteCustomersById(context.Context, gen.DeleteCustomersByIdRequestObject) (gen.DeleteCustomersByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCustomer Get a customer by id
// (GET /api/v1/customers/{id})
func (s *server) GetCustomer(context.Context, gen.GetCustomerRequestObject) (gen.GetCustomerResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PutCustomersById Update a customer
// (PUT /api/v1/customers/{id})
func (s *server) PutCustomersById(context.Context, gen.PutCustomersByIdRequestObject) (gen.PutCustomersByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCustomersByIdContacts List the contacts associated with a customer
// (GET /api/v1/customers/{id}/contacts)
func (s *server) GetCustomersByIdContacts(context.Context, gen.GetCustomersByIdContactsRequestObject) (gen.GetCustomersByIdContactsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostCustomersByIdContacts Associate a contact with a customer
// (POST /api/v1/customers/{id}/contacts)
func (s *server) PostCustomersByIdContacts(context.Context, gen.PostCustomersByIdContactsRequestObject) (gen.PostCustomersByIdContactsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeleteCustomersByIdContactsByContactId Remove a contact association from a customer
// (DELETE /api/v1/customers/{id}/contacts/{contactId})
func (s *server) DeleteCustomersByIdContactsByContactId(context.Context, gen.DeleteCustomersByIdContactsByContactIdRequestObject) (gen.DeleteCustomersByIdContactsByContactIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PutCustomersByIdContactsByContactId Update a customer's contact association
// (PUT /api/v1/customers/{id}/contacts/{contactId})
func (s *server) PutCustomersByIdContactsByContactId(context.Context, gen.PutCustomersByIdContactsByContactIdRequestObject) (gen.PutCustomersByIdContactsByContactIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeleteCustomersByIdLegalIdentity Remove a customer's legal identity
// (DELETE /api/v1/customers/{id}/legal-identity)
func (s *server) DeleteCustomersByIdLegalIdentity(context.Context, gen.DeleteCustomersByIdLegalIdentityRequestObject) (gen.DeleteCustomersByIdLegalIdentityResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCustomersByIdLegalIdentity Get a customer's legal identity
// (GET /api/v1/customers/{id}/legal-identity)
func (s *server) GetCustomersByIdLegalIdentity(context.Context, gen.GetCustomersByIdLegalIdentityRequestObject) (gen.GetCustomersByIdLegalIdentityResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PutCustomersByIdLegalIdentity Replace a customer's legal identity
// (PUT /api/v1/customers/{id}/legal-identity)
func (s *server) PutCustomersByIdLegalIdentity(context.Context, gen.PutCustomersByIdLegalIdentityRequestObject) (gen.PutCustomersByIdLegalIdentityResponseObject, error) {
	return nil, module.ErrNotImplemented
}

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
