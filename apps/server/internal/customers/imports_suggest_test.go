package customers

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
)

// This file is the suggested mapping (customers import wizard design D3),
// without a server: the alias table, the JSON names, the previous mapping and
// the one-column-per-field rule.

// fieldsOf is a suggestion as "field=column" pairs, in column order.
func fieldsOf(m gen.CustomerImportMapping) map[string]int32 {
	out := map[string]int32{}
	for _, c := range m.Columns {
		out[c.Field] = *c.Column
	}
	return out
}

func TestSuggestMapping_ReadsTheWordsNorwegianFilesUse(t *testing.T) {
	t.Parallel()
	header := []string{"Kundenr", "Firmanavn", "Org.nr", "E-post", "Tlf", "Adresse", "Postnr", "Poststed", "Land", "Fakturaepost", "Betalingsbetingelser", "Valuta", "Kundegruppe", "Stikkord", "Noe annet"}
	m := suggestMapping(header, nil)
	want := map[string]int32{
		"customerNumber": 0, "name": 1, "legalId": 2, "email": 3, "phone": 4, "postalLine1": 5, "postalPostalCode": 6,
		"postalCity": 7, "postalCountry": 8, "invoiceEmail": 9, "paymentTermsDays": 10, "currency": 11, "group": 12, "tags": 13,
	}
	got := fieldsOf(m)
	if len(got) != len(want) {
		t.Errorf("suggested %d fields %v, want %d %v", len(got), got, len(want), want)
	}
	for field, column := range want {
		if got[field] != column {
			t.Errorf("%s suggested from column %d, want %d", field, got[field], column)
		}
	}
	if m.Mode != gen.CreateOrUpdate || m.MatchBy == nil || *m.MatchBy != gen.LegalId {
		t.Errorf("mode = %s matchBy %v, want createOrUpdate by legalId when an organisation-number column is there, a customer number too", m.Mode, m.MatchBy)
	}
}

// TestSuggestMapping_TheDefaultMatch is the default mode and match key: the
// organisation number when there is one — another system's customer number
// is that system's, which no customer here has — else the customer number,
// else a create.
func TestSuggestMapping_TheDefaultMatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		header  []string
		mode    gen.CustomerImportMappingMode
		matchBy *gen.CustomerImportMappingMatchBy
	}{
		{"organisation number alone", []string{"Navn", "Orgnr"}, gen.CreateOrUpdate, ptrMatchBy(gen.LegalId)},
		{"customer number alone", []string{"Kundenr", "Navn"}, gen.CreateOrUpdate, ptrMatchBy(gen.CustomerNumber)},
		{"both", []string{"KUNDENR", "Navn", "Org.nr"}, gen.CreateOrUpdate, ptrMatchBy(gen.LegalId)},
		{"neither", []string{"Navn", "E-post"}, gen.Create, nil},
	} {
		m := suggestMapping(tc.header, nil)
		if m.Mode != tc.mode {
			t.Errorf("%s: mode = %s, want %s", tc.name, m.Mode, tc.mode)
		}
		switch {
		case tc.matchBy == nil && m.MatchBy != nil:
			t.Errorf("%s: matchBy = %s, want none", tc.name, *m.MatchBy)
		case tc.matchBy != nil && (m.MatchBy == nil || *m.MatchBy != *tc.matchBy):
			t.Errorf("%s: matchBy = %v, want %s", tc.name, m.MatchBy, *tc.matchBy)
		}
	}
}

func TestSuggestMapping_TheJSONNamesMapThemselves_AndAFieldIsClaimedOnce(t *testing.T) {
	t.Parallel()
	header := []string{"name", "EMAIL", "e-post", "legalId", "orgnr", "", "unknown"}
	m := suggestMapping(header, nil)
	got := fieldsOf(m)
	if got["name"] != 0 || got["email"] != 1 || got["legalId"] != 3 || len(got) != 3 {
		t.Errorf("suggested %v, want name=0 email=1 legalId=3 and the second claims of email and legalId left unmapped", got)
	}
	if m.Mode != gen.CreateOrUpdate || m.MatchBy == nil || *m.MatchBy != gen.LegalId {
		t.Errorf("mode = %s matchBy %v, want createOrUpdate by legalId when a legal-id column is there", m.Mode, m.MatchBy)
	}
}

func TestSuggestMapping_APreviousMappingForTheSameHeaderWins(t *testing.T) {
	t.Parallel()
	column := int32(1)
	value := "NOK"
	previous := gen.CustomerImportMapping{Mode: gen.Update, MatchBy: ptrMatchBy(gen.LegalId), Columns: []gen.CustomerImportMappingColumn{
		{Field: "legalId", Column: &column}, {Field: "currency", Value: &value},
	}}
	m := suggestMapping([]string{"Firmanavn", "Org.nr"}, &previous)
	if m.Mode != gen.Update || len(m.Columns) != 2 || m.Columns[1].Value == nil || *m.Columns[1].Value != "NOK" {
		t.Errorf("suggested %+v, want the previous mapping as it was", m)
	}
}

func ptrMatchBy(m gen.CustomerImportMappingMatchBy) *gen.CustomerImportMappingMatchBy { return &m }
