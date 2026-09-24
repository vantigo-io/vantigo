package customers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is GET /customers/{id}/personal-data (customers GDPR design D3),
// and the fakes the anonymisation tests share: another module's
// contracts.CustomerPersonalData, faked because depguard keeps communications,
// energy and projects out of this package even in a test — each proves its own
// SQL in its own package.

// fakePersonalData stands in for another module's contracts.CustomerPersonalData.
// It answers the section it was given, records every call, and — through
// during — can look into the anonymisation's own transaction while it is open,
// or fail it.
type fakePersonalData struct {
	section any
	erased  []contracts.ErasedData
	during  func(ctx context.Context, tx pgx.Tx, customerID int32) error

	mu      sync.Mutex
	exports []int32
	erases  []int32
}

func (f *fakePersonalData) ExportCustomerData(_ context.Context, customerID int32) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exports = append(f.exports, customerID)
	return f.section, nil
}

func (f *fakePersonalData) EraseCustomerData(ctx context.Context, tx pgx.Tx, customerID int32) ([]contracts.ErasedData, error) {
	f.mu.Lock()
	f.erases = append(f.erases, customerID)
	f.mu.Unlock()
	if f.during != nil {
		if err := f.during(ctx, tx, customerID); err != nil {
			return nil, err
		}
	}
	return f.erased, nil
}

// erasesSoFar is every customer EraseCustomerData was called for, in call
// order, copied under the lock each call records under.
func (f *fakePersonalData) erasesSoFar() []int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.erases)
}

// personalDataClient signs in the caller this delivery adds: its key and view,
// nothing else. The export needs nothing else — it is shaped by nothing else
// (design D3) — and the scheduling needs nothing else either (D4).
func personalDataClient(t *testing.T, h *modtest.Harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "customers:view", "customers:personal-data")
}

func getPersonalData(t *testing.T, c *modtest.Client, id int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/personal-data", id), nil)
}

type anonymisationJSON struct {
	AnonymiseOn  string     `json:"anonymiseOn"`
	AnonymisedAt *time.Time `json:"anonymisedAt"`
}

// personalDataJSON decodes the file, as much of it as the tests read.
type personalDataJSON struct {
	ExportedAt time.Time `json:"exportedAt"`
	Customer   struct {
		Id             int32  `json:"id"`
		CustomerNumber int64  `json:"customerNumber"`
		Name           string `json:"name"`
		Type           string `json:"type"`
		Status         string `json:"status"`
		Identity       *struct {
			Country string `json:"country"`
			Id      string `json:"id"`
		} `json:"identity"`
		ContactInfo    contactInfoJSON `json:"contactInfo"`
		Addresses      []addressJSON   `json:"addresses"`
		BillingProfile struct {
			InvoiceEmail *string `json:"invoiceEmail"`
			Currency     *string `json:"currency"`
		} `json:"billingProfile"`
		Owner         *ownerJSON         `json:"owner"`
		Group         *groupRefJSON      `json:"group"`
		Tags          []tagJSON          `json:"tags"`
		Anonymisation *anonymisationJSON `json:"anonymisation"`
	} `json:"customer"`
	Contacts []struct {
		Contact struct {
			Id        int32   `json:"id"`
			FirstName string  `json:"firstName"`
			Email     *string `json:"email"`
		} `json:"contact"`
		Title *string `json:"title"`
	} `json:"contacts"`
	Timeline []struct {
		timelineEntryJSON
		FollowUp *struct {
			DueOn string `json:"dueOn"`
		} `json:"followUp"`
	} `json:"timeline"`
	Modules map[string]json.RawMessage `json:"modules"`
}

// setPersonIdentity gives a customer a private person's legal identity
// directly — Swedish, so the national-identity rule (Norway's) has nothing to
// say about the fixture.
func setPersonIdentity(t *testing.T, h *modtest.Harness, id int32) {
	t.Helper()
	h.Exec(t, `UPDATE customers.customers SET legal_country = 'se', legal_id = '19800101-1234', legal_name = 'Kari Nordmann',
	           legal_source = 'manual', legal_type = 'person' WHERE id = $1`, id)
}

// TestGetCustomersByIdPersonalData_HandsAPersonEverythingInOneFile is design D3:
// the customer with everything on its row and beside it — the legal identity
// for a caller who holds neither legal-identity-view nor contacts-view — its
// contacts, every timeline entry with its follow-up — a deleted one too — and
// each module's section under its name, a module with nothing having no key.
// It is an attachment, named by the customer number, never cached.
func TestGetCustomersByIdPersonalData_HandsAPersonEverythingInOneFile(t *testing.T) {
	t.Parallel()
	alpha := &fakePersonalData{section: map[string]any{"things": []string{"one"}}}
	quiet := &fakePersonalData{}
	h := newHarness(t, modtest.WithCustomerPersonalData(
		contracts.CustomerPersonalDataHolder{Module: "alpha", Data: alpha},
		contracts.CustomerPersonalDataHolder{Module: "quiet", Data: quiet},
	))
	c, userID := authenticatedClientWithID(t, h)
	setDisplayName(t, h, userID, "Siri Saksbehandler")
	person := createCustomerOfType(t, c, "Kari Nordmann", "person")
	setPersonIdentity(t, h, person.Id)
	if r := putContactInfo(t, c, person.Id, map[string]any{"email": "kari@example.test"}); r.Status != http.StatusOK {
		t.Fatalf("contact info: status %d body %s", r.Status, r.Body)
	}
	createAddress(t, c, person.Id, fullAddressBody("postal", nil))
	if r := putBillingProfile(t, c, person.Id, map[string]any{"invoiceEmail": "faktura@example.test", "currency": "NOK"}); r.Status != http.StatusOK {
		t.Fatalf("billing profile: status %d body %s", r.Status, r.Body)
	}
	if r := putOwner(t, c, person.Id, map[string]any{"ownerUserId": userID}); r.Status != http.StatusOK {
		t.Fatalf("owner: status %d body %s", r.Status, r.Body)
	}
	group := createGroup(t, c, map[string]any{"name": "Privatkunder"})
	if r := putCustomerGroup(t, c, person.Id, map[string]any{"groupId": group.Id}); r.Status != http.StatusOK {
		t.Fatalf("group: status %d body %s", r.Status, r.Body)
	}
	tag := createTag(t, c, map[string]any{"name": "Nabo"})
	if r := putCustomerTags(t, c, person.Id, []string{tag.Id}); r.Status != http.StatusOK {
		t.Fatalf("tags: status %d body %s", r.Status, r.Body)
	}
	contact := createContact(t, c, map[string]any{"firstName": "Per", "lastName": "Nordmann", "email": "per@example.test"})
	attachContact(t, c, person.Id, contact.Id, "Ektefelle")
	entry := createWithFollowUp(t, c, person.Id, day(h, 0), "Ringte om strømavtalen", map[string]any{"dueOn": day(h, 7)})
	// A soft-deleted note is still held, so it is still handed over.
	deleted := createManual(t, c, person.Id, day(h, 0), "Feilført notat")
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/timeline/%d?expectedRevision=1", person.Id, deleted.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete note: status %d body %s", r.Status, r.Body)
	}

	r := getPersonalData(t, personalDataClient(t, h), person.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("personal data: status %d body %s, want 200", r.Status, r.Body)
	}
	if got := r.Header("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got, want := r.Header("Content-Disposition"), fmt.Sprintf(`attachment; filename="customer-%d-personal-data.json"`, person.CustomerNumber); got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
	if got := r.Header("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control = %q, want private, no-store", got)
	}

	var file personalDataJSON
	r.JSON(&file)
	got := file.Customer
	if !file.ExportedAt.Equal(h.Now()) || got.Id != person.Id || got.CustomerNumber != person.CustomerNumber || got.Name != "Kari Nordmann" || got.Type != "person" {
		t.Errorf("file = %+v, want Kari Nordmann's, made now", file)
	}
	if got.Identity == nil || got.Identity.Country != "se" || got.Identity.Id != "19800101-1234" {
		t.Errorf("identity = %+v, want hers, whatever the caller's legal-identity keys", got.Identity)
	}
	if str(got.ContactInfo.Email) != "kari@example.test" || len(got.Addresses) != 1 ||
		str(got.BillingProfile.InvoiceEmail) != "faktura@example.test" || str(got.BillingProfile.Currency) != "NOK" {
		t.Errorf("contact info %+v, addresses %+v, billing %+v", got.ContactInfo, got.Addresses, got.BillingProfile)
	}
	if got.Owner == nil || got.Owner.DisplayName != "Siri Saksbehandler" || got.Group == nil || got.Group.Name != "Privatkunder" ||
		!slices.Equal(tagNamesOf(got.Tags), []string{"Nabo"}) || got.Anonymisation != nil {
		t.Errorf("owner %+v, group %+v, tags %+v, anonymisation %+v", got.Owner, got.Group, got.Tags, got.Anonymisation)
	}
	if len(file.Contacts) != 1 || file.Contacts[0].Contact.Id != contact.Id || file.Contacts[0].Contact.FirstName != "Per" ||
		str(file.Contacts[0].Title) != "Ektefelle" {
		t.Errorf("contacts = %+v, want Per as Ektefelle", file.Contacts)
	}
	var manual, gone bool
	for _, e := range file.Timeline {
		switch e.Id {
		case entry.Id:
			manual = str(e.Note) == "Ringte om strømavtalen" && e.FollowUp != nil && e.FollowUp.DueOn == day(h, 7)
		case deleted.Id:
			gone = str(e.Note) == "Feilført notat" && e.State == "deleted"
		}
	}
	if !manual || !gone || len(file.Timeline) < 3 {
		t.Errorf("timeline = %+v, want every entry, the note with its follow-up and the deleted note among them", file.Timeline)
	}
	if string(file.Modules["alpha"]) != `{"things":["one"]}` {
		t.Errorf("modules.alpha = %s, want the fake's section", file.Modules["alpha"])
	}
	if _, ok := file.Modules["quiet"]; ok || len(file.Modules) != 1 {
		t.Errorf("modules = %v, want alpha's alone: a module with nothing has no key", file.Modules)
	}
	if !slices.Equal(alpha.exports, []int32{person.Id}) || !slices.Equal(quiet.exports, []int32{person.Id}) {
		t.Errorf("exports asked for = %v / %v, want the one customer each", alpha.exports, quiet.exports)
	}
}

// A business is not a data subject (design D3): its export is refused, and the
// fakes are never asked. The key, and view, are both needed; a customer that
// does not exist is a 404.
func TestGetCustomersByIdPersonalData_IsAPrivatePersonsAlone(t *testing.T) {
	t.Parallel()
	fake := &fakePersonalData{}
	h := newHarness(t, modtest.WithCustomerPersonalData(contracts.CustomerPersonalDataHolder{Module: "fake", Data: fake}))
	c := authenticatedClient(t, h)
	business := createCustomer(t, c, "Acme AS")
	person := createCustomerOfType(t, c, "Kari Nordmann", "person")

	problem := refusedWith(t, getPersonalData(t, personalDataClient(t, h), business.Id), "personal_data_not_a_person")
	if problem.Title != "Customer is not a private person" {
		t.Errorf("title = %q", problem.Title)
	}
	if len(fake.exports) != 0 {
		t.Errorf("the fake was asked about a business: %v", fake.exports)
	}
	if r := getPersonalData(t, personalDataClient(t, h), 999999); r.Status != http.StatusNotFound {
		t.Errorf("unknown customer: status %d, want 404", r.Status)
	}
	for _, keys := range [][]string{{"customers:view", "customers:legal-identity-view", "customers:contacts-view"}, {"customers:personal-data"}} {
		if r := getPersonalData(t, h.SignIn(t, keys...), person.Id); r.Status != http.StatusForbidden {
			t.Errorf("%v: status %d, want 403", keys, r.Status)
		}
	}
}

// Every customer response carries its anonymisation, scheduled or done, through
// the batched decoration (design D4) — absent on a customer never scheduled,
// the date alone while it waits, the moment too once it ran — and the file
// carries it the same way.
func TestSafeCustomerResponse_CarriesTheAnonymisation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	waiting := createCustomerOfType(t, c, "Kari Nordmann", "person")
	done := createCustomerOfType(t, c, "Ola Nordmann", "person")
	never := createCustomerOfType(t, c, "Per Nordmann", "person")
	h.Exec(t, `UPDATE customers.customers SET anonymise_on = '2026-12-01' WHERE id = $1`, waiting.Id)
	ran := h.Now().Add(-time.Hour).UTC()
	h.Exec(t, `UPDATE customers.customers SET anonymise_on = '2026-09-01', anonymised_at = $2 WHERE id = $1`, done.Id, ran)

	anonymisationOf := func(id int32) *anonymisationJSON {
		t.Helper()
		r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", id), nil)
		if r.Status != http.StatusOK {
			t.Fatalf("get customer %d: status %d body %s", id, r.Status, r.Body)
		}
		var body struct {
			Anonymisation *anonymisationJSON `json:"anonymisation"`
		}
		r.JSON(&body)
		return body.Anonymisation
	}
	if a := anonymisationOf(waiting.Id); a == nil || a.AnonymiseOn != "2026-12-01" || a.AnonymisedAt != nil {
		t.Errorf("scheduled: anonymisation = %+v, want 2026-12-01 and no anonymisedAt", a)
	}
	if a := anonymisationOf(done.Id); a == nil || a.AnonymiseOn != "2026-09-01" || a.AnonymisedAt == nil || !a.AnonymisedAt.Equal(ran) {
		t.Errorf("anonymised: anonymisation = %+v, want 2026-09-01 and %s", a, ran)
	}
	if a := anonymisationOf(never.Id); a != nil {
		t.Errorf("never scheduled: anonymisation = %+v, want absent", a)
	}

	var file personalDataJSON
	r := getPersonalData(t, personalDataClient(t, h), waiting.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("personal data: status %d body %s", r.Status, r.Body)
	}
	r.JSON(&file)
	if a := file.Customer.Anonymisation; a == nil || a.AnonymiseOn != "2026-12-01" {
		t.Errorf("file's anonymisation = %+v, want the schedule", a)
	}
}
