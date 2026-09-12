package customers_test

import (
	"context"
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every customers test against
// customers.yaml and records which operations answered successfully. It is
// shared by all tests, parallel ones included; TestMain turns it into the
// coverage gate.
var recorder = contracttest.New(loadContract())

func loadContract() *openapi3.T {
	doc, err := openapi.Load(context.Background(), "customers")
	if err != nil {
		panic(err)
	}
	return doc
}

// pendingOperations are the contract's operations no test covers yet. Every
// one of the module's 29 operations is a stub answering 501, so all 29 are
// pending; each later task removes the operations of the area it implements,
// and RequireCoverage fails the run if an entry left here was in fact
// exercised.
var pendingOperations = []string{
	"deleteCustomersById",
	"deleteCustomersByIdContactsByContactId",
	"deleteCustomersByIdLegalIdentity",
	"deleteCustomersByIdTimelineByEntryId",
	"deleteCustomersContactsById",
	"getContact",
	"getCustomer",
	"getCustomers",
	"getCustomersByIdContacts",
	"getCustomersByIdLegalIdentity",
	"getCustomersByIdTimeline",
	"getCustomersByIdTimelineByEntryId",
	"getCustomersByIdTimelineByEntryIdRevisions",
	"getCustomersContacts",
	"getCustomersContactsByIdCustomers",
	"getCustomersLookupBrreg",
	"getCustomersStats",
	"getCustomersStatsAttention",
	"getCustomersStatsSummary",
	"getCustomersStatsTimeseries",
	"postCustomers",
	"postCustomersByIdContacts",
	"postCustomersByIdTimeline",
	"postCustomersContacts",
	"putCustomersById",
	"putCustomersByIdContactsByContactId",
	"putCustomersByIdLegalIdentity",
	"putCustomersByIdTimelineByEntryId",
	"putCustomersContactsById",
}

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder, pendingOperations...))
}
