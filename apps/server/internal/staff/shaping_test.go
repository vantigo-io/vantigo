package staff_test

import (
	"bytes"
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The person's field groups (design §5), as the response names their fields.
var (
	workFields    = []string{"id", "employeeNumber", "givenName", "familyName", "displayName", "workEmail", "phoneWork", "workCountry", "preferredLanguage", "capabilities", "revision", "status"}
	privateFields = []string{"birthDate", "gender", "phoneMobile", "phonePrivate", "privateEmail", "street", "postalCode", "city", "country"}
	sealedFields  = []string{"nationalId", "bankAccount"}
	linkFields    = []string{"user"}
	hrFields      = []string{"notes"}
	// holdFields is the hold group of a person neither erased nor
	// reassessed: the legal hold, not held (erasedAt and reassessedAt are
	// absent until set).
	holdFields = []string{"legalHold"}
	// placedFields is the work group's place today, from the terms in force
	// and their employment (design §5's Work row): the title, the business
	// unit and the location, read by every audience that sees the person.
	placedFields = []string{"title", "businessUnit", "location"}
)

func fields(groups ...[]string) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g...)
	}
	slices.Sort(out)
	return out
}

// shapedPerson is a person with every field of every group set — the sealed
// fields' clear columns seeded directly, in the shape PUT /national-id and
// /bank-account write them, since the matrix is about who reads them —
// linked to a user of their own, with a next of kin and a role.
type shapedPerson struct {
	id   int64
	self *modtest.Client
	user uuid.UUID
}

func seedShapedPerson(t *testing.T, h *harness, hr *modtest.Client) shapedPerson {
	t.Helper()
	p := createPerson(t, hr, map[string]any{
		"givenName": "Kari", "familyName": "Nordmann", "employeeNumber": "S-1",
		"birthDate": "1990-02-28", "gender": "female",
		"workEmail": "kari@example.no", "phoneWork": "22 00 00 00",
		"phoneMobile": "900 00 000", "phonePrivate": "55 00 00 00", "privateEmail": "kari@privat.no",
		"street": "Storgata 1", "postalCode": "0155", "city": "Oslo", "country": "NO",
		"workCountry": "NO", "preferredLanguage": "en", "notes": "HR's own",
	})
	h.Exec(t, `UPDATE staff.people SET national_id_kind = 'fodselsnummer', national_id_mac = '\x01'::bytea,
		national_id_sealed = '\x02'::bytea, bank_account_kind = 'bban', bank_account_last4 = '1234',
		bank_account_sealed = '\x03'::bytea WHERE id = $1`, p.Id)
	putNextOfKin(t, h, hr, p.Id, map[string]any{"nextOfKin": []any{kin("Ola", "spouse")}})
	putRoles(t, h, hr, p.Id, map[string]any{"roles": []any{map[string]any{"role": "verneombud"}}})
	employment := seedEmployment(t, h, p.Id, 0) // active today, so the colleague audience holds
	location := modtest.One[int64](t, h.Harness, `INSERT INTO staff.locations (name, created_at, updated_at)
		VALUES ('Oslo HQ', $1, $1) RETURNING id`, h.Now())
	h.Exec(t, `UPDATE staff.employment_terms SET title = 'Developer', location_id = $2 WHERE employment_id = $1`, employment, location)
	self, user := signInNamed(t, h, "Kari the user")
	link(t, h, p.Id, user)
	return shapedPerson{id: p.Id, self: self, user: user}
}

// The audience × field matrix for GET /people/{id} (design §5, S21 as
// amended: shaping by field group, per field): each audience of this task
// sees exactly its groups' fields — a group it may not read is absent, never
// null — and an outsider gets the 404 an unknown id gets, byte for byte.
// The Manager audience is TestShaping_ManagerAudience's.
func TestShaping_PersonPerAudience(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := seedShapedPerson(t, h, hr)
	colleague, _ := signIn(t, h, "staff:view")
	manage, _ := signIn(t, h, "staff:manage")
	personalData, _ := signIn(t, h, "staff:personal-data")
	outsider, _ := signIn(t, h)

	type caps struct{ edit, reveal, pay, account bool }
	for _, c := range []struct {
		name   string
		client *modtest.Client
		want   []string
		caps   caps
	}{
		{"self", p.self, fields(workFields, placedFields, privateFields, sealedFields, linkFields), caps{pay: true}},
		{"colleague", colleague, fields(workFields, placedFields), caps{}},
		{"staff:manage", manage, fields(workFields, placedFields, privateFields, sealedFields, linkFields, hrFields, holdFields), caps{edit: true, pay: true, account: true}},
		{"staff:personal-data", personalData, fields(workFields, placedFields, privateFields, sealedFields, linkFields, hrFields, holdFields), caps{reveal: true, pay: true}},
	} {
		body := raw(t, c.client.Do(http.MethodGet, personPath(p.id), nil))
		got := slices.Sorted(maps.Keys(body))
		if !slices.Equal(got, c.want) {
			t.Errorf("%s sees %v, want %v", c.name, got, c.want)
		}
		capabilities, _ := body["capabilities"].(map[string]any)
		gotCaps := caps{capabilities["canEdit"] == true, capabilities["canRevealIdentifiers"] == true, capabilities["canSeePay"] == true, capabilities["canManageAccount"] == true}
		if gotCaps != c.caps {
			t.Errorf("%s capabilities = %v, want %+v", c.name, capabilities, c.caps)
		}
		if id, ok := body["nationalId"].(map[string]any); ok && (len(id) != 2 || id["kind"] != "fodselsnummer" || id["onFile"] != true) {
			t.Errorf("%s nationalId = %v, want kind and onFile only", c.name, id)
		}
		if account, ok := body["bankAccount"].(map[string]any); ok && (len(account) != 2 || account["kind"] != "bban" || account["last4"] != "1234") {
			t.Errorf("%s bankAccount = %v, want kind and last4 only", c.name, account)
		}
		if body["title"] != "Developer" {
			t.Errorf("%s title = %v, want the terms' in force today", c.name, body["title"])
		}
		if unit, _ := body["businessUnit"].(map[string]any); unit["name"] != "Oslo" || len(unit) != 2 {
			t.Errorf("%s businessUnit = %v, want the employment's, id and name", c.name, body["businessUnit"])
		}
		if location, _ := body["location"].(map[string]any); location["name"] != "Oslo HQ" || len(location) != 2 {
			t.Errorf("%s location = %v, want the terms', id and name", c.name, body["location"])
		}
		if user, ok := body["user"].(map[string]any); ok && (user["id"] != p.user.String() || user["displayName"] != "Kari the user" || user["active"] != true) {
			t.Errorf("%s user = %v, want the linked user", c.name, user)
		}
	}

	unknown := outsider.Do(http.MethodGet, personPath(p.id+1000), nil)
	for _, path := range []string{personPath(p.id), nextOfKinPath(p.id), rolesPath(p.id), eventsPath(p.id)} {
		r := outsider.Do(http.MethodGet, path, nil)
		if r.Status != http.StatusNotFound || !bytes.Equal(r.Body, unknown.Body) {
			t.Errorf("outsider GET %s: %d %q, want the unknown id's 404 %q", path, r.Status, r.Body, unknown.Body)
		}
	}
}

// The person's sub-resources follow their groups: next of kin are private
// (the person and HR), roles are work (everyone who sees the person), and the
// history is the person's, HR's and the manager's, never a
// colleague's. A group the audience may not read is the unknown id's 404.
func TestShaping_SubResourcesPerAudience(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := seedShapedPerson(t, h, hr)
	colleague, _ := signIn(t, h, "staff:view")
	personalData, _ := signIn(t, h, "staff:personal-data")

	for _, c := range []struct {
		name                string
		client              *modtest.Client
		kin, roles, history int
	}{
		{"self", p.self, http.StatusOK, http.StatusOK, http.StatusOK},
		{"colleague", colleague, http.StatusNotFound, http.StatusOK, http.StatusNotFound},
		{"staff:manage", hr, http.StatusOK, http.StatusOK, http.StatusOK},
		{"staff:personal-data", personalData, http.StatusOK, http.StatusOK, http.StatusOK},
	} {
		for path, want := range map[string]int{nextOfKinPath(p.id): c.kin, rolesPath(p.id): c.roles, eventsPath(p.id): c.history} {
			r := c.client.Do(http.MethodGet, path, nil)
			if r.Status != want {
				t.Errorf("%s GET %s: %d, want %d", c.name, path, r.Status, want)
			}
			if want == http.StatusNotFound && len(r.Body) != 0 {
				t.Errorf("%s GET %s: body %q, want the bare 404", c.name, path, r.Body)
			}
		}
	}
}

// keysOf is a JSON object's keys, sorted.
func keysOf(m map[string]any) []string { return slices.Sorted(maps.Keys(m)) }

// rawList decodes a response body as a list of JSON objects.
func rawList(t *testing.T, r *modtest.Response) []map[string]any {
	t.Helper()
	var out []map[string]any
	r.JSON(&out)
	return out
}

// The terms' and the employment's fields by group (design §5): what a
// colleague reads, what else a manager reads, and the pay and HR's note.
var (
	termsDateFields       = []string{"id", "employmentId", "validFrom", "employmentType", "ftePercent", "revision"}
	termsNonPayFields     = []string{"appointmentForm", "fullTimeHoursPerWeek", "title", "workingHoursScheme", "managerPersonId", "probationExtensionAgreed"}
	termsPayFields        = []string{"payComponents", "payMethod", "remunerationType"}
	employmentDateFields  = []string{"id", "personId", "startDate", "revision", "fteChangedAt"}
	employmentOtherFields = []string{"businessUnitId", "employmentNumber", "relationshipType"}
)

// The Manager audience (design §5, plan reading 6): a person linked to the
// caller who manages someone today — named on their terms in force, or the
// head of their department — reads that person's work group, employments and
// every non-pay term, their roles and their history shaped (a private
// field's change is "changed", a work field's shows its values; the sealed
// fields' and the link's are "changed"), and the contract check; not the
// pay, not HR's notes, not the private group, the next of kin or the link.
// A colleague reads an employment's dates and the terms' dates, type and
// percentage only.
func TestShaping_ManagerAudience(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	start := shiftDay(t, today(t, h), -30)

	boss := createPerson(t, hr, newPerson("Berit", "Boss"))
	createEmployment(t, hr, boss.Id, newEmployment(start, terms()))
	manager, managerUser := signIn(t, h)
	link(t, h, boss.Id, managerUser)

	p := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "birthDate", "1990-02-28", "workEmail", "kari@example.no"))
	office := createLocation(t, hr, "Oslo HQ").Id
	e := createEmployment(t, hr, p.Id, newEmployment(start, terms("title", "Developer", "managerPersonId", boss.Id, "locationId", office,
		"payComponents", []any{map[string]any{"name": "Base", "amount": 50000, "currency": "NOK", "period": "monthly"}},
		"payMethod", "Bank transfer", "remunerationType", "monthly", "note", "HR only")))
	putNextOfKin(t, h, hr, p.Id, map[string]any{"nextOfKin": []any{kin("Ola", "spouse")}})
	do(t, hr, http.MethodPut, personPath(p.Id), with(newPerson("Kari", "Nordmann"), "birthDate", "1991-03-01", "workEmail", "kari.n@example.no"),
		http.StatusOK, nil, ifMatch(personRevision(t, h, p.Id)))
	h.Exec(t, `UPDATE staff.people SET national_id_kind = 'fodselsnummer', national_id_mac = '\x01'::bytea, national_id_sealed = '\x02'::bytea WHERE id = $1`, p.Id)
	h.Exec(t, `INSERT INTO staff.events (person_id, kind, actor_kind, actor_display, at, payload, payload_classes)
		VALUES ($1, 'national_id_set', 'user', 'HR', $2, '{"fields":{"nationalId":{"to":{"kind":"fodselsnummer"}}}}', '{D}')`, p.Id, h.Now())

	person := raw(t, manager.Do(http.MethodGet, personPath(p.Id), nil))
	// No work phone was set, so it is absent for everyone.
	unset := func(name string) bool { return name == "phoneWork" }
	if got, want := keysOf(person), slices.DeleteFunc(fields(workFields, placedFields, []string{"manager"}), unset); !slices.Equal(got, want) {
		t.Errorf("manager reads the person as %v, want %v", got, want)
	}
	if location, _ := person["location"].(map[string]any); person["title"] != "Developer" || location["name"] != "Oslo HQ" {
		t.Errorf("manager reads title %v and location %v, want the terms' in force today", person["title"], person["location"])
	}
	if m, _ := person["manager"].(map[string]any); m["id"] != float64(boss.Id) {
		t.Errorf("manager = %v, want Berit", person["manager"])
	}

	employment := rawList(t, manager.Do(http.MethodGet, employmentsPath(p.Id), nil))
	if len(employment) != 1 {
		t.Fatalf("manager reads %d employments, want 1", len(employment))
	}
	for _, f := range append(slices.Clone(employmentDateFields), employmentOtherFields...) {
		if _, ok := employment[0][f]; !ok {
			t.Errorf("manager: employment lacks %s: %v", f, employment[0])
		}
	}
	if _, ok := employment[0]["salaryChangedAt"]; ok {
		t.Errorf("manager reads salaryChangedAt, which is the pay group's: %v", employment[0])
	}
	row := rawList(t, manager.Do(http.MethodGet, termsPath(e.Id), nil))[0]
	for _, f := range append(slices.Clone(termsDateFields), termsNonPayFields...) {
		if _, ok := row[f]; !ok {
			t.Errorf("manager: terms lack %s: %v", f, row)
		}
	}
	for _, f := range append(slices.Clone(termsPayFields), "note") {
		if _, ok := row[f]; ok {
			t.Errorf("manager reads %s: %v", f, row)
		}
	}
	hrRow := rawList(t, hr.Do(http.MethodGet, termsPath(e.Id), nil))[0]
	for _, f := range append(slices.Clone(termsPayFields), "note") {
		if _, ok := hrRow[f]; !ok {
			t.Errorf("HR does not read %s: %v", f, hrRow)
		}
	}

	for path, want := range map[string]int{
		nextOfKinPath(p.Id): http.StatusNotFound, userPath(p.Id): http.StatusNotFound, rolesPath(p.Id): http.StatusOK,
		eventsPath(p.Id): http.StatusOK, contractCheckPath(e.Id): http.StatusOK, orgPath(p.Id): http.StatusOK,
		employmentPath(e.Id): http.StatusOK,
	} {
		if r := manager.Do(http.MethodGet, path, nil); r.Status != want {
			t.Errorf("manager GET %s: %d, want %d", path, r.Status, want)
		}
	}
	var events gen.PaginatedResponseOfStaffEvent
	do(t, manager, http.MethodGet, eventsPath(p.Id), nil, http.StatusOK, &events)
	seen := map[string]bool{}
	for _, ev := range events.Data {
		f := fieldsOf(ev)
		switch ev.Kind {
		case "person_updated":
			seen[ev.Kind] = true
			if f["birthDate"] != "changed" {
				t.Errorf("manager reads birthDate as %v, want changed", f["birthDate"])
			}
			if w, ok := f["workEmail"].(map[string]any); !ok || w["to"] != "kari.n@example.no" {
				t.Errorf("manager reads workEmail as %v, want its values", f["workEmail"])
			}
		case "national_id_set":
			seen[ev.Kind] = true
			if f["nationalId"] != "changed" {
				t.Errorf("manager reads nationalId as %v, want changed", f["nationalId"])
			}
		case "next_of_kin_updated":
			seen[ev.Kind] = true
			if f["nextOfKin"] != "changed" {
				t.Errorf("manager reads nextOfKin as %v, want changed", f["nextOfKin"])
			}
		case "terms_added":
			seen[ev.Kind] = true
			for _, name := range []string{"payComponents", "payMethod", "remunerationType", "note"} {
				if v, ok := f[name]; ok {
					t.Errorf("manager reads %s of terms_added as %v, want it left out", name, v)
				}
			}
			if title, ok := f["title"].(map[string]any); !ok || title["to"] != "Developer" {
				t.Errorf("manager reads title as %v, want its value", f["title"])
			}
		}
	}
	if len(seen) != 4 {
		t.Errorf("manager saw %v of the four event kinds", seen)
	}
	// A correction of the pay alone tells a manager nothing — not even that
	// the pay changed, or on which day, which the employment's
	// salaryChangedAt keeps from them.
	first := rawList(t, hr.Do(http.MethodGet, termsPath(e.Id), nil))[0]
	do(t, hr, http.MethodPut, termsRowPath(int64(first["id"].(float64))), terms("validFrom", start, "title", "Developer", "managerPersonId", boss.Id, "locationId", office,
		"payComponents", []any{map[string]any{"name": "Base", "amount": 55000, "currency": "NOK", "period": "monthly"}},
		"payMethod", "Bank transfer", "remunerationType", "monthly", "note", "HR only", "correction", true),
		http.StatusOK, nil, ifMatch(int32(first["revision"].(float64))))
	do(t, manager, http.MethodGet, eventsPath(p.Id), nil, http.StatusOK, &events)
	if latest := events.Data[0]; latest.Kind != "terms_corrected" || len(fieldsOf(latest)) != 0 {
		t.Errorf("manager reads the pay correction as %s %v, want terms_corrected with no field", latest.Kind, fieldsOf(latest))
	}
	do(t, hr, http.MethodGet, eventsPath(p.Id), nil, http.StatusOK, &events)
	if _, ok := fieldsOf(events.Data[0])["payComponents"].(map[string]any); !ok {
		t.Errorf("HR reads the pay correction as %v, want its values", fieldsOf(events.Data[0]))
	}

	// The head of a department manages its people who name no manager.
	head := createPerson(t, hr, newPerson("Hilde", "Leder"))
	createEmployment(t, hr, head.Id, newEmployment(start, terms()))
	headClient, headUser := signIn(t, h)
	link(t, h, head.Id, headUser)
	dept := createDepartment(t, hr, map[string]any{"code": "OPS", "name": "Operations", "managerPersonId": head.Id})
	q := createPerson(t, hr, newPerson("Quentin", "Ops"))
	createEmployment(t, hr, q.Id, newEmployment(start, terms("departmentId", dept.Id)))
	if r := headClient.Do(http.MethodGet, eventsPath(q.Id), nil); r.Status != http.StatusOK {
		t.Errorf("the department's head reads their report's history: %d, want 200", r.Status)
	}
	if r := headClient.Do(http.MethodGet, eventsPath(p.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("the head reads someone else's report's history: %d, want 404", r.Status)
	}

	colleague, _ := signIn(t, h, "staff:view")
	crow := rawList(t, colleague.Do(http.MethodGet, termsPath(e.Id), nil))[0]
	if got, want := keysOf(crow), fields(termsDateFields); !slices.Equal(got, want) {
		t.Errorf("colleague reads the terms as %v, want %v", got, want)
	}
	cemp := rawList(t, colleague.Do(http.MethodGet, employmentsPath(p.Id), nil))[0]
	if got, want := keysOf(cemp), fields(employmentDateFields); !slices.Equal(got, want) {
		t.Errorf("colleague reads the employment as %v, want %v", got, want)
	}
}
