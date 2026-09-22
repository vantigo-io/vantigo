package customers

import (
	"strings"
	"testing"
	"time"
)

// This file tests registry_diff.go, the pure half of a refresh (Brreg in
// full design D4): what one fetch found different from the record on file,
// and how each difference is rendered for the timeline. It is package
// customers (the brreg_entity_test.go convention) because everything here is
// unexported and needs neither a harness nor a database — a table test per
// rule is both faster and sharper than driving the same case through HTTP.

func registryDate(t *testing.T, s string) *time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("registryDate(%q): %v", s, err)
	}
	return &d
}

func registryEmployees(v int32) *int32 { return &v }

// equinorRecord is a full stored record to diff against, so each case below
// only has to say what it changes.
func equinorRecord(t *testing.T) registryRecord {
	t.Helper()
	return registryRecord{
		OrganisationNumber:   "923609016",
		Name:                 "EQUINOR ASA",
		OrganisationFormCode: "ASA",
		OrganisationForm:     "Allmennaksjeselskap",
		IndustryCode:         "06.100",
		Industry:             "Utvinning av råolje",
		Employees:            registryEmployees(21272),
		VATRegistered:        true,
		FoundedOn:            registryDate(t, "1972-06-14"),
		Website:              "www.equinor.com",
		BusinessAddress: &registryAddress{
			Lines: []string{"Forusbeen 50"}, PostalCode: "4035", City: "STAVANGER",
			Municipality: "STAVANGER", CountryCode: "NO",
		},
		FetchedAt: time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC),
	}
}

// TestDiffRegistryRecords_FirstFetchComparesOnlyTheLegalName pins the
// first-fetch rule (design D4): with no record on file there is nothing to
// compare a record against, so the only comparison is the registry's name
// against the name the legal identity asserted — and an equal pair records
// nothing at all, so a create's own fetch is silent for the overwhelmingly
// common case of a name that came from the very same registry.
func TestDiffRegistryRecords_FirstFetchComparesOnlyTheLegalName(t *testing.T) {
	t.Parallel()
	after := equinorRecord(t)

	if changes := diffRegistryRecords(nil, "EQUINOR ASA", after); len(changes) != 0 {
		t.Errorf("changes = %+v, want none (the registry's name is the legal name)", changes)
	}

	changes := diffRegistryRecords(nil, "Equinor", after)
	want := []registryChange{{Field: "name", From: "Equinor", To: "EQUINOR ASA"}}
	if !sameRegistryChanges(changes, want) {
		t.Errorf("changes = %+v, want %+v", changes, want)
	}
}

// TestDiffRegistryRecords_FirstFetchReportsADeletedEntity pins the second
// half of the first-fetch comparison: a company that is already struck from
// the register when it is first read is news, whether or not its name
// matches, and the two are reported in the full diff's own field order.
func TestDiffRegistryRecords_FirstFetchReportsADeletedEntity(t *testing.T) {
	t.Parallel()
	after := equinorRecord(t)
	after.DeletedOn = registryDate(t, "2026-09-21")

	changes := diffRegistryRecords(nil, "EQUINOR ASA", after)
	want := []registryChange{{Field: "deletedOn", To: "2026-09-21"}}
	if !sameRegistryChanges(changes, want) {
		t.Errorf("changes = %+v, want %+v", changes, want)
	}

	changes = diffRegistryRecords(nil, "Equinor", after)
	want = []registryChange{
		{Field: "name", From: "Equinor", To: "EQUINOR ASA"},
		{Field: "deletedOn", To: "2026-09-21"},
	}
	if !sameRegistryChanges(changes, want) {
		t.Errorf("changes = %+v, want %+v (name first, then deletedOn)", changes, want)
	}
}

// TestDiffRegistryRecords_FirstFetchIgnoresEveryOtherField proves the
// first-fetch comparison really is those two fields and no more: a record
// full of values has nothing to have changed *from*, so reporting them all
// as changes would bury the ones a person cares about under sixteen lines of
// noise.
func TestDiffRegistryRecords_FirstFetchIgnoresEveryOtherField(t *testing.T) {
	t.Parallel()
	after := equinorRecord(t)
	after.Bankrupt = true
	after.Employees = registryEmployees(3)
	after.Email = "post@equinor.test"

	if changes := diffRegistryRecords(nil, "EQUINOR ASA", after); len(changes) != 0 {
		t.Errorf("changes = %+v, want none — a first fetch compares the name and nothing else", changes)
	}
}

// TestDiffRegistryRecords_EveryComparedField walks the whole list of fields
// design D4 names, one case each, so a field dropped from the comparison (or
// rendered differently) breaks exactly one case. The legal name is passed
// equal to the stored name throughout: once a record is on file the legal
// name plays no part at all.
func TestDiffRegistryRecords_EveryComparedField(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*registryRecord)
		want   registryChange
	}{
		{"name", func(r *registryRecord) { r.Name = "EQUINOR ENERGY AS" },
			registryChange{Field: "name", From: "EQUINOR ASA", To: "EQUINOR ENERGY AS"}},
		{"organisationForm", func(r *registryRecord) { r.OrganisationForm = "Aksjeselskap" },
			registryChange{Field: "organisationForm", From: "Allmennaksjeselskap", To: "Aksjeselskap"}},
		{"industryCode", func(r *registryRecord) { r.IndustryCode = "06.200" },
			registryChange{Field: "industryCode", From: "06.100", To: "06.200"}},
		{"employees", func(r *registryRecord) { r.Employees = registryEmployees(21273) },
			registryChange{Field: "employees", From: "21272", To: "21273"}},
		{"employees unregistered", func(r *registryRecord) { r.Employees = nil },
			registryChange{Field: "employees", From: "21272", To: ""}},
		{"vatRegistered", func(r *registryRecord) { r.VATRegistered = false },
			registryChange{Field: "vatRegistered", From: "true", To: "false"}},
		{"bankrupt", func(r *registryRecord) { r.Bankrupt = true },
			registryChange{Field: "bankrupt", From: "false", To: "true"}},
		{"underLiquidation", func(r *registryRecord) { r.UnderLiquidation = true },
			registryChange{Field: "underLiquidation", From: "false", To: "true"}},
		{"underForcedLiquidation", func(r *registryRecord) { r.UnderForcedLiquidation = true },
			registryChange{Field: "underForcedLiquidation", From: "false", To: "true"}},
		{"deletedOn", func(r *registryRecord) { r.DeletedOn = registryDate(t, "2026-09-21") },
			registryChange{Field: "deletedOn", From: "", To: "2026-09-21"}},
		{"website", func(r *registryRecord) { r.Website = "www.equinor.test" },
			registryChange{Field: "website", From: "www.equinor.com", To: "www.equinor.test"}},
		{"email", func(r *registryRecord) { r.Email = "post@equinor.test" },
			registryChange{Field: "email", From: "", To: "post@equinor.test"}},
		{"phone", func(r *registryRecord) { r.Phone = "51990000" },
			registryChange{Field: "phone", From: "", To: "51990000"}},
		{"mobile", func(r *registryRecord) { r.Mobile = "90000000" },
			registryChange{Field: "mobile", From: "", To: "90000000"}},
		{"parentOrganisationNumber", func(r *registryRecord) { r.ParentOrganisationNumber = "974760673" },
			registryChange{Field: "parentOrganisationNumber", From: "", To: "974760673"}},
		{"businessAddress", func(r *registryRecord) { r.BusinessAddress.Lines = []string{"Forusbeen 51"} },
			registryChange{Field: "businessAddress", From: "Forusbeen 50, 4035 STAVANGER, NO", To: "Forusbeen 51, 4035 STAVANGER, NO"}},
		{"postalAddress", func(r *registryRecord) {
			r.PostalAddress = &registryAddress{Lines: []string{"Postboks 8500"}, PostalCode: "4035", City: "STAVANGER", CountryCode: "NO"}
		},
			registryChange{Field: "postalAddress", From: "", To: "Postboks 8500, 4035 STAVANGER, NO"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before := equinorRecord(t)
			after := equinorRecord(t)
			tc.mutate(&after)

			changes := diffRegistryRecords(&before, before.Name, after)
			if !sameRegistryChanges(changes, []registryChange{tc.want}) {
				t.Errorf("changes = %+v, want exactly %+v", changes, tc.want)
			}
		})
	}
}

// TestDiffRegistryRecords_UnchangedRecordIsSilent pins the no-op: the same
// record fetched again — at a later fetchedAt, which is deliberately not a
// compared field (design D4: "fetched_at moving is not a change") — reports
// nothing, so a quiet refresh writes no timeline event.
func TestDiffRegistryRecords_UnchangedRecordIsSilent(t *testing.T) {
	t.Parallel()
	before := equinorRecord(t)
	after := equinorRecord(t)
	after.FetchedAt = before.FetchedAt.Add(24 * time.Hour)

	if changes := diffRegistryRecords(&before, before.Name, after); len(changes) != 0 {
		t.Errorf("changes = %+v, want none (only fetchedAt moved)", changes)
	}
}

// TestDiffRegistryRecords_FieldsNotComparedAreIgnored pins the other half of
// the field list: organisationFormCode, industry (the description beside the
// code), foundedOn and organisationNumber are stored but never diffed —
// design D4 names the compared fields, and a code's description changing
// wording is not news.
func TestDiffRegistryRecords_FieldsNotComparedAreIgnored(t *testing.T) {
	t.Parallel()
	before := equinorRecord(t)
	after := equinorRecord(t)
	after.OrganisationFormCode = "AS"
	after.Industry = "Utvinning av råolje og naturgass"
	after.FoundedOn = registryDate(t, "1972-06-15")
	after.OrganisationNumber = "974760673"

	if changes := diffRegistryRecords(&before, before.Name, after); len(changes) != 0 {
		t.Errorf("changes = %+v, want none (none of these fields is compared)", changes)
	}
}

// TestDiffRegistryRecords_ReportsEveryChangedFieldInOrder proves a refresh
// that found several differences reports them all, in the fixed order the
// summary then reads off — not one event per field, and not an order that
// depends on map iteration.
func TestDiffRegistryRecords_ReportsEveryChangedFieldInOrder(t *testing.T) {
	t.Parallel()
	before := equinorRecord(t)
	after := equinorRecord(t)
	after.Employees = registryEmployees(21000)
	after.Name = "EQUINOR ENERGY AS"
	after.BusinessAddress = &registryAddress{Lines: []string{"Havnegata 48"}, PostalCode: "8910", City: "BRØNNØYSUND", CountryCode: "NO"}

	changes := diffRegistryRecords(&before, before.Name, after)
	want := []registryChange{
		{Field: "name", From: "EQUINOR ASA", To: "EQUINOR ENERGY AS"},
		{Field: "employees", From: "21272", To: "21000"},
		{Field: "businessAddress", From: "Forusbeen 50, 4035 STAVANGER, NO", To: "Havnegata 48, 8910 BRØNNØYSUND, NO"},
	}
	if !sameRegistryChanges(changes, want) {
		t.Errorf("changes = %+v, want %+v (record order, every field)", changes, want)
	}
}

// TestRegistryAddressDisplay pins the one-line rendering both the diff and
// the timeline read an address as: lines first, then the post code and city
// as one part, then the country code — and an absent address renders empty
// rather than as a string of separators.
func TestRegistryAddressDisplay(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		addr *registryAddress
		want string
	}{
		{"nil", nil, ""},
		{"domestic", &registryAddress{Lines: []string{"Forusbeen 50"}, PostalCode: "4035", City: "STAVANGER", Municipality: "STAVANGER", CountryCode: "NO"},
			"Forusbeen 50, 4035 STAVANGER, NO"},
		{"two lines", &registryAddress{Lines: []string{"c/o Someone", "Storgata 1"}, PostalCode: "0155", City: "OSLO", CountryCode: "NO"},
			"c/o Someone, Storgata 1, 0155 OSLO, NO"},
		{"foreign, no post code", &registryAddress{Lines: []string{"ul. Budowniczych 12"}, City: "81-336 GDYNIA", CountryCode: "PL"},
			"ul. Budowniczych 12, 81-336 GDYNIA, PL"},
		{"nothing but a country", &registryAddress{Lines: []string{}, CountryCode: "NO"}, "NO"},
		{"entirely empty", &registryAddress{Lines: []string{}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := registryAddressDisplay(tc.addr); got != tc.want {
				t.Errorf("registryAddressDisplay = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRegistryChangeSummary pins the summary a registry.change event
// carries: the fixed prefix and the changed fields' own names, joined — and
// truncated to summary's varchar(500), which sixteen fields cannot reach on
// their own but which is the column's limit all the same (addressSummary's
// reasoning, timeline_events.go).
func TestRegistryChangeSummary(t *testing.T) {
	t.Parallel()
	got := registryChangeSummary([]registryChange{
		{Field: "name", To: "EQUINOR ENERGY AS"},
		{Field: "employees", To: "21000"},
	})
	want := "Registry record updated: name, employees"
	if got != want {
		t.Errorf("registryChangeSummary = %q, want %q", got, want)
	}

	long := make([]registryChange, 0, 200)
	for i := 0; i < 200; i++ {
		long = append(long, registryChange{Field: strings.Repeat("x", 20)})
	}
	if n := len([]rune(registryChangeSummary(long))); n != 500 {
		t.Errorf("summary length = %d runes, want 500 (truncated to the column)", n)
	}
}

// sameRegistryChanges compares two change lists field by field, in order —
// registryChange is comparable, but a failure message naming the first
// disagreement is more useful than a bare "not equal".
func sameRegistryChanges(got, want []registryChange) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
