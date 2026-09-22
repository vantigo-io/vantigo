package customers_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins the customer-row lock a registry refresh takes first
// (Brreg in full design D2, D4; registry.go's refreshRegistryRecord, using
// queries/addresses.sql's LockCustomer): two refreshes of the same customer
// serialize through that one FOR NO KEY UPDATE lock, so the second one diffs
// against what the first actually stored instead of against the same "no
// record on file" the first saw.
//
// It has to be the customer row. The record's own FOR UPDATE cannot
// serialize the case that matters most — a first refresh has no record row
// to lock, so two of them would each find nothing on file, each compare the
// registry's name with the legal identity's, and each write their own
// registry.change event for the same single difference. gateCustomerLock,
// race and awaitLockWaiters are addresses_concurrency_test.go's and
// contacts_concurrency_test.go's, reused rather than declared a fourth time.

// TestRegistryRefresh_ConcurrentFirstFetches_RecordOneEvent forces two first
// refreshes of one customer to overlap at the database with the same lock
// gate the address tests use, and pins the outcome: both answer 200 (neither
// is refused — they queue), the customer ends up with exactly one registry
// row, and exactly one registry.change event exists, written by whichever
// refresh the lock admitted first. The second, admitted after it, finds the
// stored record identical to what the registry just said and stays quiet.
//
// A mutation dropping LockCustomer from the refresh transaction fails this
// two ways over: neither request would ever wait on the gate (awaitLockWaiters
// fails outright), and both would record their own name-change event.
func TestRegistryRefresh_ConcurrentFirstFetches_RecordOneEvent(t *testing.T) {
	h := newRegistryHarness(t, registryBody(equinorRegistryBody))
	c := authenticatedClient(t, h)
	// The legal name deliberately differs from the registry's, so a first
	// fetch has exactly one thing to report: two events instead of one is
	// then unmistakably a lost race, not a difference in what was fetched.
	created := createCustomerWithIdentity(t, c, "Equinor", "no", "923609016")

	release := gateCustomerLock(t, h, created.Id)

	refresh := func() *modtest.Response { return postRegistryRefresh(t, c, created.Id) }

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(refresh, refresh)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	release()
	responses := <-done

	for i, r := range responses {
		if r.Status != http.StatusOK {
			t.Errorf("response %d: status %d body %s, want 200", i, r.Status, r.Body)
		}
	}
	if n := registryRowCount(t, h, created.Id); n != 1 {
		t.Errorf("registry rows = %d, want exactly 1", n)
	}
	entries := fetchRegistryEvents(t, c, created.Id)
	if len(entries) != 1 {
		t.Fatalf("timeline events = %d, want exactly 1 (the two refreshes serialize)", len(entries))
	}
	if str(entries[0].Summary) != "Registry record updated: name" {
		t.Errorf("summary = %q, want the first fetch's name difference", str(entries[0].Summary))
	}
}
