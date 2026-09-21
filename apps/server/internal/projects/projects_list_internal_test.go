package projects

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// countingDirectory is contracts.CustomerDirectory answering Customers from a
// fixed map and counting how many times it was called: this file's own proof
// that customerNamesForPage's doc comment (projects_list.go) is true — one
// call for the whole page, never one per row and never one per distinct
// customer id either. Customer, Contact, ContactsByEmail and BillingProfile
// are never exercised by customerNamesForPage; they are implemented only to
// satisfy the interface.
type countingDirectory struct {
	customers map[int32]contracts.CustomerEntry
	calls     int
}

var _ contracts.CustomerDirectory = (*countingDirectory)(nil)

func (d *countingDirectory) Customer(context.Context, int32) (*contracts.CustomerEntry, error) {
	return nil, nil
}

func (d *countingDirectory) Customers(_ context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
	d.calls++
	entries := []contracts.CustomerEntry{}
	for _, id := range ids {
		if c, ok := d.customers[id]; ok {
			entries = append(entries, c)
		}
	}
	return entries, nil
}

func (d *countingDirectory) Contact(context.Context, int32) (*contracts.ContactEntry, error) {
	return nil, nil
}

func (d *countingDirectory) ContactsByEmail(context.Context, string) ([]contracts.ContactMatch, error) {
	return nil, nil
}

func (d *countingDirectory) BillingProfile(context.Context, int32) (*contracts.CustomerBillingProfile, error) {
	return nil, nil
}

func customerIDPtr(id int32) *int32 { return &id }

// TestCustomerNamesForPage_OneDirectoryCallForTheWholePage proves the
// batching customerNamesForPage's own doc comment promises: several rows
// sharing one customer, and rows spanning several distinct customers, all
// cost exactly one Customers call.
func TestCustomerNamesForPage_OneDirectoryCallForTheWholePage(t *testing.T) {
	t.Parallel()
	dir := &countingDirectory{customers: map[int32]contracts.CustomerEntry{
		1001: {ID: 1001, Name: "Kraft-Verket AS"},
		1002: {ID: 1002, Name: "Acme Industrier AS"},
	}}
	s := &server{deps: module.Deps{Directory: dir}}

	rows := []store.ProjectsProject{
		{ID: 1, CustomerID: customerIDPtr(1001)},
		{ID: 2, CustomerID: customerIDPtr(1002)},
		{ID: 3, CustomerID: customerIDPtr(1001)},
	}
	names, err := s.customerNamesForPage(context.Background(), rows)
	if err != nil {
		t.Fatalf("customerNamesForPage: %v", err)
	}
	if dir.calls != 1 {
		t.Errorf("Customers calls = %d, want exactly 1", dir.calls)
	}
	if names[1] == nil || *names[1] != "Kraft-Verket AS" || names[3] == nil || *names[3] != "Kraft-Verket AS" {
		t.Errorf("names[1]/names[3] = %v/%v, want both %q", names[1], names[3], "Kraft-Verket AS")
	}
	if names[2] == nil || *names[2] != "Acme Industrier AS" {
		t.Errorf("names[2] = %v, want %q", names[2], "Acme Industrier AS")
	}
}

// TestCustomerNamesForPage_SkipsTheCallWithNoCustomerIds proves an
// all-internal page — no project on it carries a customer id — never calls
// the directory at all (the controller ruling for this batching).
func TestCustomerNamesForPage_SkipsTheCallWithNoCustomerIds(t *testing.T) {
	t.Parallel()
	dir := &countingDirectory{customers: map[int32]contracts.CustomerEntry{}}
	s := &server{deps: module.Deps{Directory: dir}}

	rows := []store.ProjectsProject{{ID: 1}, {ID: 2}}
	names, err := s.customerNamesForPage(context.Background(), rows)
	if err != nil {
		t.Fatalf("customerNamesForPage: %v", err)
	}
	if dir.calls != 0 {
		t.Errorf("Customers calls = %d, want 0 for a page with no customer ids", dir.calls)
	}
	if len(names) != 0 {
		t.Errorf("names = %v, want empty", names)
	}
}

// TestCustomerNamesForPage_ArchivedResolvesMissingIsAbsent proves the list
// still names an archived customer, and leaves a customer id the directory
// does not know about unset rather than an empty string — the same two
// cases the single-customer path (customerName, responses.go) has always
// handled, now through the batch call.
func TestCustomerNamesForPage_ArchivedResolvesMissingIsAbsent(t *testing.T) {
	t.Parallel()
	dir := &countingDirectory{customers: map[int32]contracts.CustomerEntry{
		1001: {ID: 1001, Name: "Nedlagt Handel AS", Archived: true},
	}}
	s := &server{deps: module.Deps{Directory: dir}}

	rows := []store.ProjectsProject{
		{ID: 1, CustomerID: customerIDPtr(1001)},
		{ID: 2, CustomerID: customerIDPtr(9999)},
	}
	names, err := s.customerNamesForPage(context.Background(), rows)
	if err != nil {
		t.Fatalf("customerNamesForPage: %v", err)
	}
	if names[1] == nil || *names[1] != "Nedlagt Handel AS" {
		t.Errorf("names[1] = %v, want the archived customer's name — archived still resolves", names[1])
	}
	if names[2] != nil {
		t.Errorf("names[2] = %v, want nil for a customer the directory does not know", names[2])
	}
}
