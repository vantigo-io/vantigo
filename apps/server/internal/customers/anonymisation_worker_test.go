package customers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the anonymisation worker (customers GDPR design D4), driven
// through RunCycle with h.Advance: nothing waits on a real interval. Another
// module's eraser is a fake (personal_data_test.go's fakePersonalData), for
// depguard's reason; each real one proves its SQL in its own package.

func runAnonymisation(t *testing.T, h *modtest.Harness) {
	t.Helper()
	if ran, err := customers.NewAnonymisationWorker(h.Deps()).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil", ran, err)
	}
}

// scheduleOn schedules an archived person's anonymisation, failing the test on
// anything but 200.
func scheduleOn(t *testing.T, h *modtest.Harness, id int32, anonymiseOn string) {
	t.Helper()
	if r := putAnonymisation(t, personalDataClient(t, h), id, anonymiseOn); r.Status != http.StatusOK {
		t.Fatalf("schedule %d on %s: status %d body %s", id, anonymiseOn, r.Status, r.Body)
	}
}

type anonymisedPayloadJSON struct {
	CustomerId int32 `json:"customerId"`
	Erased     []struct {
		Kind  string `json:"kind"`
		Count int64  `json:"count"`
	} `json:"erased"`
}

// personalTraces counts a customer's timeline rows — entries or revisions —
// whose text still says anything a fixture put there about the person: every
// value of TakesThePersonOutAndKeepsTheBookkeeping's fixture — the names, the
// emails, the buyer reference, the identity, the phone, the addresses' street
// and label, the shared contact's name. Whole words where a short one could
// sit inside an unrelated one ("anne" in "planned").
func personalTraces(t *testing.T, h *modtest.Harness, table string, customerID int32) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM customers.`+pgx.Identifier{table}.Sanitize()+`
	                   WHERE customer_id = $1
	                     AND (summary || coalesce(note, '') || coalesce(source_url, '') || coalesce(payload_json::text, ''))
	                         ~* '(kari|nordmann|per@|faktura@|purring@|KARI-1|19800101|storgata|900 00|\mHQ\M|\manne\M|hansen)'`, customerID)
}

// TestAnonymisationWorker_TakesThePersonOutAndKeepsTheBookkeeping is design D4's
// whole list on one customer that has something in every place it names: not
// before the day; on the day, the row cleared exactly as listed and kept
// exactly as listed, addresses, Peppol answer and registry record gone,
// associations detached and the contact only this customer had deleted, every
// timeline entry and revision kept with its content anonymised and its shape
// intact, the other module's eraser called inside the transaction, and
// customer.anonymised written last, in words. Afterwards the customer is
// read-only and its export answers what is left.
func TestAnonymisationWorker_TakesThePersonOutAndKeepsTheBookkeeping(t *testing.T) {
	t.Parallel()
	var sawAddresses atomic.Int64
	var sawName atomic.Value
	fake := &fakePersonalData{
		erased: []contracts.ErasedData{{Kind: "fake.things", Count: 3}},
		during: func(ctx context.Context, tx pgx.Tx, id int32) error {
			// Read through the anonymisation's own transaction: its address
			// delete is visible here and nowhere else yet, and the row is not
			// cleared until every module has run.
			var n int64
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM customers.customer_addresses WHERE customer_id = $1`, id).Scan(&n); err != nil {
				return err
			}
			sawAddresses.Store(n)
			var name string
			if err := tx.QueryRow(ctx, `SELECT name FROM customers.customers WHERE id = $1`, id).Scan(&name); err != nil {
				return err
			}
			sawName.Store(name)
			return nil
		},
	}
	h := newHarness(t, modtest.WithCustomerPersonalData(contracts.CustomerPersonalDataHolder{Module: "fake", Data: fake}))
	c, userID := h.SignInUser(t, mergeKeys...)
	ctx := context.Background()

	person := createCustomerOfType(t, c, "Kari Nordmann", "person")
	setPersonIdentity(t, h, person.Id)
	if r := putContactInfo(t, c, person.Id, map[string]any{"email": "kari@example.test", "phone": "+47 900 00 000", "website": "https://kari.example.test"}); r.Status != http.StatusOK {
		t.Fatalf("contact info: status %d body %s", r.Status, r.Body)
	}
	createAddress(t, c, person.Id, fullAddressBody("postal", nil))
	createAddress(t, c, person.Id, fullAddressBody("invoice", nil))
	if r := putBillingProfile(t, c, person.Id, map[string]any{
		"invoiceEmail": "faktura@example.test", "reminderEmail": "purring@example.test", "buyerReference": "KARI-1",
		"currency": "NOK", "paymentTermsDays": 14, "language": "nb", "invoiceDelivery": "email",
	}); r.Status != http.StatusOK {
		t.Fatalf("billing profile: status %d body %s", r.Status, r.Body)
	}
	h.Exec(t, `UPDATE customers.customers SET peppol_id = '9908:910000000', gln = '7080000000001' WHERE id = $1`, person.Id)
	if r := putOwner(t, c, person.Id, map[string]any{"ownerUserId": userID}); r.Status != http.StatusOK {
		t.Fatalf("owner: status %d body %s", r.Status, r.Body)
	}
	group := createGroup(t, c, map[string]any{"name": "Privatkunder"})
	putCustomerGroup(t, c, person.Id, map[string]any{"groupId": group.Id})
	tag := createTag(t, c, map[string]any{"name": "Nabo"})
	putCustomerTags(t, c, person.Id, []string{tag.Id})
	orphan := createContact(t, c, map[string]any{"firstName": "Per", "lastName": "Nordmann", "email": "per@example.test"})
	attachContact(t, c, person.Id, orphan.Id, "Ektefelle")
	shared := createContact(t, c, map[string]any{"firstName": "Anne", "lastName": "Hansen"})
	// Linked with the association's own phone, so contact_attached carries one.
	if r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", person.Id), map[string]any{
		"contactId": shared.Id, "title": "Nabo", "phone": "+47 900 00 111",
	}); r.Status != http.StatusOK {
		t.Fatalf("attach the shared contact: status %d body %s", r.Status, r.Body)
	}
	neighbour := createCustomer(t, c, "Hansen Rør AS")
	attachContact(t, c, neighbour.Id, shared.Id, "Daglig leder")
	entry := createWithFollowUp(t, c, person.Id, day(h, 0), "Kari ringte om strømmen", map[string]any{"dueOn": day(h, 7)})
	putEntryWithFollowUp(t, c, person.Id, entry.Id, entry.CurrentRevision, map[string]any{"dueOn": day(h, 14)})
	h.Exec(t, `UPDATE customers.customers_timeline_entries SET source_url = 'https://kari.example.test/profil' WHERE id = $1`, entry.Id)
	if r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", person.Id), map[string]any{"name": "Kari Nordmann Hansen", "status": "active"}); r.Status != http.StatusOK {
		t.Fatalf("rename: status %d body %s", r.Status, r.Body)
	}
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", person.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s", r.Status, r.Body)
	}
	insertRegistryAndPeppol(t, h, person.Id)
	scheduleOn(t, h, person.Id, day(h, 3))
	shape := func() string {
		return modtest.One[string](t, h, `SELECT string_agg(id || ':' || event_type || ':' || occurred_on || ':' || state, ',' ORDER BY id)
		                                  FROM customers.customers_timeline_entries WHERE customer_id = $1`, person.Id)
	}
	entriesBefore := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1`, person.Id)
	shapeBefore := shape()
	revisionBefore := fetchCustomerJSON(t, c, person.Id).Revision

	// Not the day yet: nothing happens.
	runAnonymisation(t, h)
	if got := fetchCustomerJSON(t, c, person.Id); got.Name != "Kari Nordmann Hansen" || len(fake.erasesSoFar()) != 0 {
		t.Fatalf("before the day: name %q, erases %v — want nothing done", got.Name, fake.erasesSoFar())
	}

	h.Advance(72 * time.Hour)
	// Three days is past the 8-hour idle timeout (SESSION_IDLE_TIMEOUT): the
	// session from before the advance answers 401 now, so sign in again.
	c = h.SignIn(t, mergeKeys...)
	runAnonymisation(t, h)

	// The row: cleared as listed, kept as listed.
	type rowState struct {
		Name, Status                                               string
		Number                                                     int64
		LegalID, Email, Phone, Website                             *string
		InvoiceEmail, ReminderEmail, PeppolID, Gln, BuyerReference *string
		Currency, Language, InvoiceDelivery                        *string
		Terms                                                      *int32
		OwnerSet, GroupSet                                         bool
		AnonymisedAt                                               *time.Time
		AnonymiseOn                                                string
		Revision                                                   int32
	}
	var row rowState
	if err := h.Pool().QueryRow(ctx, `SELECT name, status, customer_number, legal_id, email, phone, website,
	        invoice_email, reminder_email, peppol_id, gln, buyer_reference, currency, language, invoice_delivery,
	        payment_terms_days, owner_user_id IS NOT NULL, group_id IS NOT NULL, anonymised_at, anonymise_on::text, revision
	    FROM customers.customers WHERE id = $1`, person.Id).Scan(&row.Name, &row.Status, &row.Number, &row.LegalID, &row.Email,
		&row.Phone, &row.Website, &row.InvoiceEmail, &row.ReminderEmail, &row.PeppolID, &row.Gln, &row.BuyerReference,
		&row.Currency, &row.Language, &row.InvoiceDelivery, &row.Terms, &row.OwnerSet, &row.GroupSet, &row.AnonymisedAt,
		&row.AnonymiseOn, &row.Revision); err != nil {
		t.Fatalf("read the row: %v", err)
	}
	if row.Name != "Anonymised person" || row.Status != "archived" || row.Number != person.CustomerNumber {
		t.Errorf("name/status/number = %q/%q/%d, want Anonymised person, archived, %d", row.Name, row.Status, row.Number, person.CustomerNumber)
	}
	for field, v := range map[string]*string{"legal_id": row.LegalID, "email": row.Email, "phone": row.Phone, "website": row.Website,
		"invoice_email": row.InvoiceEmail, "reminder_email": row.ReminderEmail, "peppol_id": row.PeppolID, "gln": row.Gln,
		"buyer_reference": row.BuyerReference} {
		if v != nil {
			t.Errorf("%s = %q, want cleared", field, *v)
		}
	}
	if str(row.Currency) != "NOK" || str(row.Language) != "nb" || str(row.InvoiceDelivery) != "email" || row.Terms == nil || *row.Terms != 14 ||
		!row.OwnerSet || row.GroupSet {
		t.Errorf("row = %+v, want terms, currency, language, delivery and owner kept, and the group left", row)
	}
	// Left, so the group can still be emptied and deleted: the anonymised
	// customer refuses the group PUT that would otherwise take it out.
	if r := c.Do(http.MethodDelete, "/api/v1/customers/groups/"+group.Id, nil); r.Status != http.StatusNoContent {
		t.Errorf("delete the anonymised customer's old group: status %d body %s, want 204", r.Status, r.Body)
	}
	if row.AnonymisedAt == nil || !row.AnonymisedAt.Equal(h.Now()) || row.AnonymiseOn != day(h, 0) || row.Revision != revisionBefore+1 {
		t.Errorf("anonymised at %v on %s, revision %d (was %d); want now, the scheduled day, one on", row.AnonymisedAt, row.AnonymiseOn, row.Revision, revisionBefore)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_tags WHERE customer_id = $1`, person.Id); n != 1 {
		t.Errorf("tags = %d, want the one kept", n)
	}

	// What hung off it.
	for table, want := range map[string]int{"customer_addresses": 0, "customer_peppol_lookups": 0, "customer_registry_records": 0, "customers_contacts": 0} {
		if n := h.Count(t, `SELECT count(*) FROM customers.`+pgx.Identifier{table}.Sanitize()+` WHERE customer_id = $1`, person.Id); n != want {
			t.Errorf("%s = %d, want %d", table, n, want)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.contacts WHERE id = $1`, orphan.Id); n != 0 {
		t.Error("the contact only this customer had is still there")
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_contacts WHERE contact_id = $1 AND customer_id = $2`, shared.Id, neighbour.Id); n != 1 {
		t.Error("the contact another customer shares lost its other association")
	}

	// The timeline: every entry kept, typed and dated as it was; nothing of the
	// person left in any entry or revision; the kept summaries kept.
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1 AND event_type <> 'customer.anonymised'`, person.Id); n != entriesBefore {
		t.Errorf("entries = %d, want the %d there were", n, entriesBefore)
	}
	if got := modtest.One[string](t, h, `SELECT string_agg(id || ':' || event_type || ':' || occurred_on || ':' || state, ',' ORDER BY id)
	                                     FROM customers.customers_timeline_entries WHERE customer_id = $1 AND event_type <> 'customer.anonymised'`, person.Id); got != shapeBefore {
		t.Errorf("the timeline's shape moved:\n got %s\nwant %s", got, shapeBefore)
	}
	for _, table := range []string{"customers_timeline_entries", "customers_timeline_entries_revisions"} {
		if n := personalTraces(t, h, table, person.Id); n != 0 {
			t.Errorf("%s: %d rows still name the person", table, n)
		}
	}
	manual := modtest.One[string](t, h, `SELECT summary || '|' || note || '|' || coalesce(source_url, '-') || '|' || follow_up_on::text
	                                      FROM customers.customers_timeline_entries WHERE id = $1`, entry.Id)
	// The follow-up's day is the one the edit set, fourteen days on from the
	// start — eleven from the clock now that it has moved three.
	if manual != "[anonymised]|[anonymised]|-|"+day(h, 11) {
		t.Errorf("the manual entry = %q, want its words gone and its follow-up day kept", manual)
	}
	entries := timelineOf(t, c, person.Id)
	if status := entriesOfType(entries, "customer.status_changed"); len(status) == 0 || !strings.HasPrefix(str(status[0].Summary), "Customer status changed:") {
		t.Errorf("status changes = %+v, want their summary kept", status)
	}
	var created struct {
		CustomerId   int32  `json:"customerId"`
		CustomerName string `json:"customerName"`
	}
	if ev := entriesOfType(entries, "customer.created"); len(ev) != 1 || json.Unmarshal(ev[0].Payload, &created) != nil ||
		created.CustomerId != person.Id || created.CustomerName != "[anonymised]" || str(ev[0].Summary) != "[anonymised]" {
		t.Errorf("customer.created = %+v (%+v), want its id kept and its name gone", ev, created)
	}
	attachedEvents := entriesOfType(entries, "customer.contact_attached")
	if len(attachedEvents) != 2 {
		t.Errorf("contact_attached = %+v, want both attachments kept", attachedEvents)
	}
	for _, ev := range attachedEvents {
		var attached struct {
			ContactId   int32  `json:"contactId"`
			DisplayName string `json:"displayName"`
		}
		if json.Unmarshal(ev.Payload, &attached) != nil || attached.ContactId == 0 || attached.DisplayName != "[anonymised]" {
			t.Errorf("contact_attached %s, want the contact's id kept and its name gone", ev.Payload)
		}
	}

	// The other module, inside the transaction, before the row was cleared.
	if got := fake.erasesSoFar(); !slices.Equal(got, []int32{person.Id}) {
		t.Errorf("erases = %v, want the one customer once", got)
	}
	if sawAddresses.Load() != 0 || sawName.Load() != "Kari Nordmann Hansen" {
		t.Errorf("the eraser saw %d addresses and name %v, want 0 (inside the transaction) and the name not yet cleared", sawAddresses.Load(), sawName.Load())
	}

	// customer.anonymised, last and in words.
	last := entries[0]
	var payload anonymisedPayloadJSON
	if last.EventType != "customer.anonymised" || str(last.Summary) != "Customer anonymised" || last.ActorKind != "system" ||
		json.Unmarshal(last.Payload, &payload) != nil || payload.CustomerId != person.Id {
		t.Fatalf("newest entry = %+v, want customer.anonymised by the system", last)
	}
	want := fmt.Sprintf("[{customers.addresses 2} {customers.contactAssociations 2} {customers.contacts 1} {customers.timelineEntries %d} {fake.things 3}]", entriesBefore)
	if got := fmt.Sprint(payload.Erased); got != want {
		t.Errorf("erased = %s, want %s", got, want)
	}

	// Read-only from here on, and its export is what is left.
	refusedWith(t, putContactInfo(t, c, person.Id, map[string]any{"email": "kari@example.test"}), "customer_anonymised")
	var file personalDataJSON
	getPersonalData(t, personalDataClient(t, h), person.Id).JSON(&file)
	if file.Customer.Name != "Anonymised person" || file.Customer.Identity != nil || len(file.Contacts) != 0 ||
		file.Customer.Anonymisation == nil || file.Customer.Anonymisation.AnonymisedAt == nil {
		t.Errorf("the export afterwards = %+v, want what is left", file.Customer)
	}
}

// Not due, not archived, not a person: none of them is touched however far the
// clock moves. (A customer already anonymised is AnonymisesACustomerOnce's.)
func TestAnonymisationWorker_TakesOnlyWhatIsDue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	later := archivedPerson(t, c, "Later Nordmann")
	scheduleOn(t, h, later.Id, day(h, 10))
	active := createCustomerOfType(t, c, "Active Nordmann", "person")
	business := createCustomer(t, c, "Acme AS")
	// Neither can be scheduled through the API; the worker must still refuse
	// them if a row ever says so.
	h.Exec(t, `UPDATE customers.customers SET anonymise_on = $2 WHERE id = ANY($1)`, []int32{active.Id, business.Id}, day(h, 0))
	c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", business.Id), nil)

	h.Advance(48 * time.Hour)
	runAnonymisation(t, h)
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE anonymised_at IS NOT NULL`); n != 0 {
		t.Errorf("%d customers anonymised, want none", n)
	}
}

// A chain of merges is one person (design D4): the customer merged into the
// one that is due is anonymised in the same run, with its own event, and the
// customer.merged entry that describes it — on the due customer's own
// timeline — loses the snapshot with the rest of that timeline.
func TestAnonymisationWorker_AnonymisesTheWholeMergeChain(t *testing.T) {
	t.Parallel()
	fake := &fakePersonalData{}
	h := newHarness(t, modtest.WithCustomerPersonalData(contracts.CustomerPersonalDataHolder{Module: "fake", Data: fake}))
	c := mergeClient(t, h)
	absorbed := createCustomerOfType(t, c, "Kari Nordmann", "person")
	putContactInfo(t, c, absorbed.Id, map[string]any{"email": "kari@example.test"})
	survivor := createCustomerOfType(t, c, "Kari N.", "person")
	mergeOK(t, c, survivor.Id, absorbed.Id)
	c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", survivor.Id), nil)
	scheduleOn(t, h, survivor.Id, day(h, 0))

	runAnonymisation(t, h)

	for _, id := range []int32{survivor.Id, absorbed.Id} {
		if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND name = 'Anonymised person' AND email IS NULL
		                     AND anonymised_at IS NOT NULL AND anonymise_on = $2::date`, id, day(h, 0)); n != 1 {
			t.Errorf("customer %d is not anonymised on the chain's day", id)
		}
		if n := len(entriesOfType(timelineOf(t, c, id), "customer.anonymised")); n != 1 {
			t.Errorf("customer %d has %d customer.anonymised events, want its own one", id, n)
		}
	}
	merged := entriesOfType(timelineOf(t, c, survivor.Id), "customer.merged")
	var payload struct {
		Absorbed json.RawMessage `json:"absorbed"`
	}
	if len(merged) != 1 || json.Unmarshal(merged[0].Payload, &payload) != nil || string(payload.Absorbed) != `"[anonymised]"` {
		t.Errorf("customer.merged = %+v, want its absorbed snapshot gone", merged)
	}
	if got := fake.erasesSoFar(); !slices.Equal(got, []int32{survivor.Id, absorbed.Id}) {
		t.Errorf("erases = %v, want the survivor then the customer merged into it", got)
	}
}

// A chain member due on the same day as its survivor is anonymised once
// (design D4): the survivor, first in the batch by its lower id, anonymises it
// as part of its chain, and when its own turn in the same batch comes the
// under-lock re-check finds it done — no second customer.anonymised, no second
// call to any module, no second revision.
func TestAnonymisationWorker_AChainMemberDueTheSameDayIsAnonymisedOnce(t *testing.T) {
	t.Parallel()
	fake := &fakePersonalData{}
	h := newHarness(t, modtest.WithCustomerPersonalData(contracts.CustomerPersonalDataHolder{Module: "fake", Data: fake}))
	c := mergeClient(t, h)
	survivor := createCustomerOfType(t, c, "Kari N.", "person")
	absorbed := archivedPerson(t, c, "Kari Nordmann")
	scheduleOn(t, h, absorbed.Id, day(h, 0))
	mergeOK(t, c, survivor.Id, absorbed.Id)
	c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", survivor.Id), nil)
	scheduleOn(t, h, survivor.Id, day(h, 0))

	runAnonymisation(t, h)

	if n := len(entriesOfType(timelineOf(t, c, absorbed.Id), "customer.anonymised")); n != 1 {
		t.Errorf("the merged-away customer has %d customer.anonymised events, want 1", n)
	}
	if got := fake.erasesSoFar(); !slices.Equal(got, []int32{survivor.Id, absorbed.Id}) {
		t.Errorf("erases = %v, want the survivor then the customer merged into it, once each", got)
	}
}

// A customer scheduled and then merged away is anonymised on its day as itself
// (design D4): its row, and its snapshot on the survivor's customer.merged
// entry. Nothing else of the survivor's changes — its own name and history, and
// the history that moved to it with the merge, which is the survivor's now.
func TestAnonymisationWorker_AMergedAwayCustomerTakesItsSnapshotOffTheSurvivor(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := mergeClient(t, h)
	absorbed := archivedPerson(t, c, "Kari Nordmann")
	scheduleOn(t, h, absorbed.Id, day(h, 0))
	survivor := createCustomerOfType(t, c, "Kari N.", "person")
	putContactInfo(t, c, survivor.Id, map[string]any{"email": "kari.n@example.test"})
	mergeOK(t, c, survivor.Id, absorbed.Id)

	runAnonymisation(t, h)

	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND name = 'Anonymised person' AND anonymised_at IS NOT NULL`, absorbed.Id); n != 1 {
		t.Error("the merged-away customer was not anonymised")
	}
	got := fetchCustomerJSON(t, c, survivor.Id)
	if got.Name != "Kari N." || str(got.ContactInfo.Email) != "kari.n@example.test" {
		t.Errorf("the survivor = %+v, want it untouched", got)
	}
	entries := timelineOf(t, c, survivor.Id)
	merged := entriesOfType(entries, "customer.merged")
	var payload struct {
		Absorbed json.RawMessage `json:"absorbed"`
		Moved    []mergeMoveJSON `json:"moved"`
	}
	if len(merged) != 1 || json.Unmarshal(merged[0].Payload, &payload) != nil || string(payload.Absorbed) != `"[anonymised]"` ||
		str(merged[0].Summary) != "[anonymised]" || len(payload.Moved) == 0 {
		t.Errorf("customer.merged = %+v, want the snapshot and its summary gone and the counts kept", merged)
	}
	var movedHistory bool
	for _, e := range entriesOfType(entries, "customer.created") {
		movedHistory = movedHistory || str(e.Summary) == "Customer created: Kari Nordmann"
	}
	if !movedHistory {
		t.Error("the history that moved with the merge changed: it is the survivor's now")
	}
}

// A failing module rolls its customer back (design D4): nothing of that
// customer changed, the worker logs it and moves on to the next, and the next
// cycle tries it again.
func TestAnonymisationWorker_AFailingModuleRollsThatCustomerBack(t *testing.T) {
	t.Parallel()
	var failFor atomic.Int32
	fake := &fakePersonalData{during: func(_ context.Context, _ pgx.Tx, id int32) error {
		if id == failFor.Load() {
			return errors.New("the module is down")
		}
		return nil
	}}
	h := newHarness(t, modtest.WithCustomerPersonalData(contracts.CustomerPersonalDataHolder{Module: "fake", Data: fake}))
	c := authenticatedClient(t, h)
	first := archivedPerson(t, c, "Kari Nordmann")
	createAddress(t, c, first.Id, fullAddressBody("postal", nil))
	second := archivedPerson(t, c, "Ola Nordmann")
	scheduleOn(t, h, first.Id, day(h, 0))
	scheduleOn(t, h, second.Id, day(h, 0))
	failFor.Store(first.Id)

	runAnonymisation(t, h)

	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND name = 'Kari Nordmann' AND anonymised_at IS NULL`, first.Id); n != 1 {
		t.Error("the failed customer changed")
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_addresses WHERE customer_id = $1`, first.Id); n != 1 {
		t.Error("the failed customer's address went: its run was not rolled back")
	}
	if n := len(entriesOfType(timelineOf(t, c, first.Id), "customer.anonymised")); n != 0 {
		t.Errorf("the failed customer has %d customer.anonymised events", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND anonymised_at IS NOT NULL`, second.Id); n != 1 {
		t.Error("the next customer was not anonymised: one failure stopped the cycle")
	}
	if logs := h.Logs(); !strings.Contains(logs, "anonymising a customer failed") || !strings.Contains(logs, fmt.Sprintf(`"customerId":%d`, first.Id)) {
		t.Errorf("logs = %s, want the failure logged with its customer", logs)
	}

	failFor.Store(0)
	runAnonymisation(t, h)
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND anonymised_at IS NOT NULL`, first.Id); n != 1 {
		t.Error("the next cycle did not try the failed customer again")
	}
}

// A customer is anonymised once: a second cycle finds nothing, writes nothing
// and asks no module again.
func TestAnonymisationWorker_AnonymisesACustomerOnce(t *testing.T) {
	t.Parallel()
	fake := &fakePersonalData{}
	h := newHarness(t, modtest.WithCustomerPersonalData(contracts.CustomerPersonalDataHolder{Module: "fake", Data: fake}))
	c := authenticatedClient(t, h)
	person := archivedPerson(t, c, "Kari Nordmann")
	scheduleOn(t, h, person.Id, day(h, 0))

	runAnonymisation(t, h)
	revision := fetchCustomerJSON(t, c, person.Id).Revision
	h.Advance(24 * time.Hour)
	// A day is past the 8-hour idle timeout: sign in again.
	c = authenticatedClient(t, h)
	runAnonymisation(t, h)

	if got := fetchCustomerJSON(t, c, person.Id).Revision; got != revision {
		t.Errorf("revision %d after a second cycle, want %d", got, revision)
	}
	if n := len(entriesOfType(timelineOf(t, c, person.Id), "customer.anonymised")); n != 1 {
		t.Errorf("customer.anonymised events = %d, want 1", n)
	}
	if got := fake.erasesSoFar(); len(got) != 1 {
		t.Errorf("erases = %v, want one", got)
	}
}

// Fifty a cycle (design D4): the fifty-first waits for the next.
func TestAnonymisationWorker_TakesAtMostFiftyACycle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for i := 0; i < 51; i++ {
		id := insertCustomer(t, h, fmt.Sprintf("Person %02d", i), "archived")
		h.Exec(t, `UPDATE customers.customers SET type = 'person', anonymise_on = $2 WHERE id = $1`, id, day(h, 0))
	}
	runAnonymisation(t, h)
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE anonymised_at IS NOT NULL`); n != 50 {
		t.Errorf("anonymised after one cycle = %d, want 50", n)
	}
	runAnonymisation(t, h)
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE anonymised_at IS NOT NULL`); n != 51 {
		t.Errorf("anonymised after two = %d, want 51", n)
	}
}

// The registry feed's lease tests, on this worker's key: a second replica skips
// rather than anonymising the same customers, and every cycle lets go.
func TestAnonymisationWorker_SkipsTheCycleWhenTheLeaseIsHeld(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	holder, err := h.Pool().Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the lease holder's connection: %v", err)
	}
	defer holder.Release()
	var locked bool
	if err := holder.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, customers.AnonymisationLeaseKeyForTest).Scan(&locked); err != nil || !locked {
		t.Fatalf("take the lease: %v, %v", locked, err)
	}
	w := customers.NewAnonymisationWorker(h.Deps())
	if ran, err := w.RunCycle(ctx); err != nil || ran {
		t.Errorf("RunCycle with the lease held = %v, %v; want false, nil", ran, err)
	}
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_unlock($1)`, customers.AnonymisationLeaseKeyForTest); err != nil {
		t.Fatalf("release the lease: %v", err)
	}
	if ran, err := w.RunCycle(ctx); err != nil || !ran {
		t.Errorf("RunCycle after release = %v, %v; want true, nil", ran, err)
	}
}

func TestAnonymisationWorker_ReleasesTheLeaseAfterEveryCycle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	w := customers.NewAnonymisationWorker(h.Deps())
	for i := 0; i < 3; i++ {
		if ran, err := w.RunCycle(context.Background()); err != nil || !ran {
			t.Fatalf("RunCycle %d = %v, %v: the previous cycle did not let go", i, ran, err)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`); n != 0 {
		t.Errorf("advisory locks still held on this database = %d, want 0", n)
	}
}
