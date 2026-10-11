package staff_test

import (
	"bytes"
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The person's field groups (design §5), as the response names their fields.
var (
	workFields    = []string{"id", "employeeNumber", "givenName", "familyName", "displayName", "workEmail", "phoneWork", "workCountry", "preferredLanguage", "capabilities", "revision"}
	privateFields = []string{"birthDate", "gender", "phoneMobile", "phonePrivate", "privateEmail", "street", "postalCode", "city", "country"}
	sealedFields  = []string{"nationalId", "bankAccount"}
	linkFields    = []string{"user"}
	hrFields      = []string{"notes"}
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
// fields' clear columns seeded directly, as Task 3b's endpoints will write
// them — linked to a user of their own, with a next of kin and a role.
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
	seedEmployment(t, h, p.Id, 0) // active today, so the colleague audience holds
	self, user := signInNamed(t, h, "Kari the user")
	link(t, h, p.Id, user)
	return shapedPerson{id: p.Id, self: self, user: user}
}

// The audience × field matrix for GET /people/{id} (design §5, S21 as
// amended: shaping by field group, per field): each audience of this task
// sees exactly its groups' fields — a group it may not read is absent, never
// null — and an outsider gets the 404 an unknown id gets, byte for byte.
// The Manager audience joins in Task 4.
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
		{"self", p.self, fields(workFields, privateFields, sealedFields, linkFields), caps{pay: true}},
		{"colleague", colleague, fields(workFields), caps{}},
		{"staff:manage", manage, fields(workFields, privateFields, sealedFields, linkFields, hrFields), caps{edit: true, pay: true, account: true}},
		{"staff:personal-data", personalData, fields(workFields, privateFields, sealedFields, linkFields, hrFields), caps{reveal: true, pay: true}},
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
// history is the person's, HR's and — in Task 4 — the manager's, never a
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
