package staff_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

const meExportPath = "/api/v1/staff/me/export"

func personalDataPath(id int64) string { return personPath(id) + "/personal-data" }

// exportedPerson is a person with something in every part of the file: every
// field group, the sealed values written through their endpoints, HR's
// notes and a terms row's note, a next of kin, a role, an employment with a
// pay term, a document and a health document filed through the archive, and
// a self-service link to a user of their own.
type exportedPerson struct {
	id         int64
	self       *modtest.Client
	selfID     uuid.UUID
	nationalID string
}

const exportedBankAccount = "12345678903"

func seedExportedPerson(t *testing.T, h *harness, hr, pd *modtest.Client) exportedPerson {
	t.Helper()
	company(t, hr)
	p := createPerson(t, hr, map[string]any{
		"givenName": "Kari", "familyName": "Nordmann", "employeeNumber": "PD-1", "birthDate": "1990-01-01",
		"gender": "female", "workEmail": "kari@example.no", "phoneWork": "22 00 00 00",
		"phoneMobile": "900 00 000", "privateEmail": "kari@privat.no", "street": "Storgata 1",
		"postalCode": "0155", "city": "Oslo", "country": "NO", "notes": "HR's own",
	})
	number := testNationalID("018190", 347)
	do(t, pd, http.MethodPut, nationalIDPath(p.Id), map[string]any{"kind": "fodselsnummer", "value": number}, http.StatusOK, nil)
	do(t, pd, http.MethodPut, bankAccountPath(p.Id), map[string]any{"kind": "bban", "value": exportedBankAccount}, http.StatusOK, nil)
	putNextOfKin(t, h, hr, p.Id, map[string]any{"nextOfKin": []any{kin("Ola", "spouse")}})
	putRoles(t, h, hr, p.Id, map[string]any{"roles": []any{map[string]any{"role": "verneombud"}}})
	createEmployment(t, hr, p.Id, newEmployment("2026-01-01", terms("note", "Agreed at the interview", "remunerationType", "monthly")))
	uploadDocument(t, hr, p.Id, pdfUpload(documentCategoryID(t, h, "contract")))
	uploadDocument(t, pd, p.Id, pdfUpload(documentCategoryID(t, h, "health")))
	// HR's notes change: an event whose one field the person never reads.
	do(t, hr, http.MethodPut, personPath(p.Id), with(newPerson("Kari", "Nordmann"), "employeeNumber", "PD-1",
		"birthDate", "1990-01-01", "gender", "female", "workEmail", "kari@example.no", "phoneWork", "22 00 00 00",
		"phoneMobile", "900 00 000", "privateEmail", "kari@privat.no", "street", "Storgata 1",
		"postalCode", "0155", "city", "Oslo", "country", "NO", "notes", "HR's own, revised"),
		http.StatusOK, nil, ifMatch(personRevision(t, h, p.Id)))
	self, selfID := signInNamed(t, h, "Kari the user")
	link(t, h, p.Id, selfID)
	return exportedPerson{id: p.Id, self: self, selfID: selfID, nationalID: number}
}

// exportOf decodes a personal-data file, failing the test unless it was
// answered 200.
func exportOf(t *testing.T, r *modtest.Response) gen.StaffPersonalData {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("export: status %d body %s, want 200", r.Status, r.Body)
	}
	var out gen.StaffPersonalData
	r.JSON(&out)
	return out
}

// eventOfKind is the first event of kind in events, failing the test when
// there is none.
func eventOfKind(t *testing.T, events []gen.StaffEvent, kind string) gen.StaffEvent {
	t.Helper()
	for _, e := range events {
		if e.Kind == kind {
			return e
		}
	}
	t.Fatalf("no %s event among %v", kind, kinds(events))
	return gen.StaffEvent{}
}

// The HR-issued file is the complete answer to an innsyn request (design
// S16, reading 35): every field group of the record with the national
// identity number and the bank account opened, HR's notes and the hold
// fields, the next of kin, the roles, the employment with its terms — pay
// and note included — the link with the linked user, every document's
// metadata, health ones included, and every event whole, oldest first.
func TestPersonalData_TheCompleteFile(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")
	p := seedExportedPerson(t, h, hr, pd)

	file := exportOf(t, pd.Do(http.MethodGet, personalDataPath(p.id), nil))
	if !file.ExportedAt.Equal(h.Now()) {
		t.Errorf("exportedAt = %s, want %s", file.ExportedAt, h.Now())
	}
	person := file.Person
	if person.EmployeeNumber != "PD-1" || person.PrivateEmail == nil || *person.PrivateEmail != "kari@privat.no" ||
		person.Street == nil || person.Notes == nil || *person.Notes != "HR's own, revised" {
		t.Errorf("person = %+v, want every group, HR's notes included", person)
	}
	if id := person.NationalId; !id.OnFile || id.Kind == nil || *id.Kind != "fodselsnummer" || id.Value == nil || *id.Value != p.nationalID {
		t.Errorf("nationalId = %+v, want the number opened", id)
	}
	if b := person.BankAccount; b == nil || b.Last4 != "8903" || b.Value == nil || *b.Value != exportedBankAccount {
		t.Errorf("bankAccount = %+v, want the account opened", b)
	}
	if person.LegalHold == nil || person.LegalHold.Held {
		t.Errorf("legalHold = %+v, want present and not held", person.LegalHold)
	}
	if len(file.NextOfKin) != 1 || file.NextOfKin[0].Name != "Ola" || len(file.Roles) != 1 {
		t.Errorf("nextOfKin %+v, roles %+v, want one of each", file.NextOfKin, file.Roles)
	}
	if len(file.Employments) != 1 || len(file.Employments[0].Terms) != 1 {
		t.Fatalf("employments = %+v, want one with its one terms row", file.Employments)
	}
	first := file.Employments[0].Terms[0]
	if first.Note == nil || *first.Note != "Agreed at the interview" || first.RemunerationType == nil {
		t.Errorf("terms = %+v, want the note and the pay", first)
	}
	if !file.Link.Linked || file.Link.User == nil || file.Link.User.DisplayName != "Kari the user" {
		t.Errorf("link = %+v, want the linked user named", file.Link)
	}
	var categories []string
	for _, d := range file.Documents {
		categories = append(categories, d.Category.Key)
	}
	slices.Sort(categories)
	if !slices.Equal(categories, []string{"contract", "health"}) {
		t.Errorf("documents = %v, want the contract and the health document", categories)
	}
	if len(file.Events) == 0 || file.Events[0].Kind != "person_created" {
		t.Fatalf("events = %v, want the history oldest first", kinds(file.Events))
	}
	notes := fieldsOf(eventOfKind(t, file.Events, "person_updated"))["notes"]
	if change, _ := notes.(map[string]any); change["to"] != "HR's own, revised" {
		t.Errorf("person_updated notes = %v, want the values whole", notes)
	}
	health := eventOfKind(t, file.Events, "document_added")
	if fields := fieldsOf(health); fields["fileName"] == "changed" {
		t.Errorf("document_added = %v, want it whole", health.Payload)
	}
}

// The person's own copy (design S16, readings 31 and 35) has the same shape
// without the sealed values — the national id its kind and whether one is on
// file, the bank account its last four digits — without HR's notes, the
// terms' note and the hold fields, and with the history as the person reads
// it on My page: a change to HR's notes is left out of its event, the health
// document's event is there. No digit of either sealed value is anywhere in
// the file, and the copy records no event.
func TestPersonalData_MyOwnCopy(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")
	p := seedExportedPerson(t, h, hr, pd)
	before := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1`, p.id)

	r := p.self.Do(http.MethodGet, meExportPath, nil)
	file := exportOf(t, r)
	// The whole numbers, not leaks' four-digit runs: the file carries random
	// user ids, whose digits could match a run by chance.
	if strings.Contains(string(r.Body), p.nationalID) || strings.Contains(string(r.Body), exportedBankAccount) {
		t.Fatalf("the person's own copy carries a sealed value: %s", r.Body)
	}
	body := raw(t, r)
	person, _ := body["person"].(map[string]any)
	for _, absent := range []string{"notes", "legalHold", "erasedAt", "reassessedAt"} {
		if _, ok := person[absent]; ok {
			t.Errorf("person carries %s: %v", absent, person[absent])
		}
	}
	if id, _ := person["nationalId"].(map[string]any); len(id) != 2 || id["onFile"] != true || id["kind"] != "fodselsnummer" {
		t.Errorf("nationalId = %v, want kind and onFile only", person["nationalId"])
	}
	if b, _ := person["bankAccount"].(map[string]any); len(b) != 2 || b["last4"] != "8903" {
		t.Errorf("bankAccount = %v, want kind and last4 only", person["bankAccount"])
	}
	employments, _ := body["employments"].([]any)
	if len(employments) != 1 {
		t.Fatalf("employments = %v, want one", body["employments"])
	}
	termsRows, _ := employments[0].(map[string]any)["terms"].([]any)
	if row, _ := termsRows[0].(map[string]any); row["note"] != nil || row["remunerationType"] != "monthly" {
		t.Errorf("terms = %v, want the pay without HR's note", row)
	}
	if file.Person.PrivateEmail == nil || len(file.Documents) != 2 || !file.Link.Linked {
		t.Errorf("file = %+v, want the private group, both documents and the link", file)
	}
	if _, ok := fieldsOf(eventOfKind(t, file.Events, "person_updated"))["notes"]; ok {
		t.Errorf("the notes change is in the person's own history: %v", eventOfKind(t, file.Events, "person_updated").Payload)
	}
	var health map[string]any
	for _, e := range file.Events {
		if e.Kind == "document_added" && fieldsOf(e)["category"] != nil {
			if change, _ := fieldsOf(e)["category"].(map[string]any); change["to"] == "health" {
				health = fieldsOf(e)
			}
		}
	}
	if health == nil || health["fileName"] == "changed" {
		t.Errorf("the health document's event = %v, want it as the person reads it", health)
	}
	if after := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1`, p.id); after != before {
		t.Errorf("the person's own copy recorded %d events, want none", after-before)
	}

	unlinked, _ := signIn(t, h)
	refused(t, unlinked.Do(http.MethodGet, meExportPath, nil), http.StatusNotFound, "no_person")
}

// Both files are downloads (customers' visitor): JSON, an attachment named by
// the employee number, never cached, its length declared.
func TestPersonalData_IsADownload(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")
	p := seedExportedPerson(t, h, hr, pd)

	for name, r := range map[string]*modtest.Response{
		"HR's":         pd.Do(http.MethodGet, personalDataPath(p.id), nil),
		"the person's": p.self.Do(http.MethodGet, meExportPath, nil),
	} {
		exportOf(t, r)
		for header, want := range map[string]string{
			"Content-Type":        "application/json",
			"Content-Disposition": `attachment; filename="staff-PD-1-personal-data.json"`,
			"Cache-Control":       "private, no-store",
			"Content-Length":      strconv.Itoa(len(r.Body)),
		} {
			if got := r.Header(header); got != want {
				t.Errorf("%s file: %s = %q, want %q", name, header, got, want)
			}
		}
	}

	if r := pd.Do(http.MethodGet, personalDataPath(p.id+1000), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("unknown person: %d %q, want the bare 404", r.Status, r.Body)
	}
	if r := hr.Do(http.MethodGet, personalDataPath(p.id), nil); r.Status != http.StatusForbidden {
		t.Errorf("staff:manage alone: %d, want 403", r.Status)
	}
}

// The HR export is an audited reveal (design S16, plan reading 15): once the
// file has been read, a personal_data_exported event naming the viewer is
// recorded, with nothing in its payload — and not in the file it records.
func TestPersonalData_TheExportIsRecorded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, pdID := signInNamed(t, h, "Pia Personvern", "staff:personal-data")
	p := seedExportedPerson(t, h, hr, pd)

	file := exportOf(t, pd.Do(http.MethodGet, personalDataPath(p.id), nil))
	for _, e := range file.Events {
		if e.Kind == "personal_data_exported" {
			t.Errorf("the file records its own export: %v", kinds(file.Events))
		}
	}
	events := listEvents(t, hr, p.id)
	latest := events[0]
	if latest.Kind != "personal_data_exported" {
		t.Fatalf("latest event = %s, want personal_data_exported", latest.Kind)
	}
	if latest.Actor.DisplayName != "Pia Personvern" || latest.Actor.UserId == nil || *latest.Actor.UserId != pdID {
		t.Errorf("actor = %+v, want the viewer", latest.Actor)
	}
	if len(latest.Payload) != 0 {
		t.Errorf("payload = %v, want empty", latest.Payload)
	}
	if !latest.At.Equal(h.Now()) {
		t.Errorf("at = %s, want %s", latest.At, h.Now())
	}
}

// The file is read in one snapshot, and the directory is asked only after it
// (design §4, Locking): a change made while the export reaches the directory
// — the person renamed, an event added — is not in the file, and no call out
// was made inside a transaction.
func TestPersonalData_OneSnapshotThenTheDirectory(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, pdID := signIn(t, h, "staff:personal-data")
	p := seedExportedPerson(t, h, hr, pd)
	events := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1`, p.id)

	changed := false
	onContractCall(t, pdID, func(method string) {
		if changed || method != "Users.User" {
			return
		}
		changed = true
		h.Exec(t, `UPDATE staff.people SET given_name = 'Later' WHERE id = $1`, p.id)
		h.Exec(t, `INSERT INTO staff.events (person_id, kind, actor_kind, at, payload, payload_classes)
			VALUES ($1, 'person_updated', 'system', $2, '{"fields":{}}', '{}')`, p.id, h.Now())
	})
	file := exportOf(t, pd.Do(http.MethodGet, personalDataPath(p.id), nil))
	if !changed {
		t.Fatal("the export never asked the directory")
	}
	if file.Person.GivenName != "Kari" || len(file.Events) != events {
		t.Errorf("file has %q and %d events, want Kari and %d: a change after the snapshot reached it", file.Person.GivenName, len(file.Events), events)
	}
	var directory int
	for _, c := range contractCalls.by(pdID) {
		if c.method == "Users.User" {
			directory++
		}
		if c.inTx {
			t.Errorf("%s was called inside a transaction", c.method)
		}
	}
	if directory == 0 {
		t.Error("the linked user was never read from the directory")
	}
}

// One person's file is built in memory and never refused for size (plan
// reading 14): a few thousand events fit.
func TestPersonalData_AFewThousandEventsFit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")
	p := createPerson(t, hr, with(newPerson("Mange", "Hendelser"), "employeeNumber", "PD-BIG"))
	const many = 4000
	h.Exec(t, `
		INSERT INTO staff.events (person_id, kind, actor_kind, actor_display, at, payload, payload_classes)
		SELECT $1, 'person_updated', 'user', 'Hanne HR', $2::timestamptz + n * interval '1 second',
			jsonb_build_object('fields', jsonb_build_object('phoneWork', jsonb_build_object('from', 'n' || n, 'to', 'n' || (n + 1)))),
			'{D}'
		FROM generate_series(1, $3::int) AS n`, p.Id, h.Now(), many)

	file := exportOf(t, pd.Do(http.MethodGet, personalDataPath(p.Id), nil))
	if len(file.Events) != many+1 {
		t.Fatalf("events = %d, want %d", len(file.Events), many+1)
	}
	last := fieldsOf(file.Events[many])["phoneWork"]
	if change, _ := last.(map[string]any); change["to"] != fmt.Sprintf("n%d", many+1) {
		t.Errorf("the last event = %v, want the latest, oldest first", last)
	}
	encoded, err := json.Marshal(file)
	if err != nil || len(encoded) < many*50 {
		t.Errorf("the file is %d bytes (%v), want every event in it", len(encoded), err)
	}
}
