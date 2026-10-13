package customers

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
)

// This file is the mapping (customers import wizard design D3, D4): the JSON
// document PUT /customers/imports/{id}/mapping saves, read against the file's
// header into the importLayout import.go runs rows through, and checked —
// before anything runs — against what its sender could do by hand. The
// import key is the door; the mapping is where the content is judged: a mode
// that creates needs customers:create, one that updates customers:update, a
// legal-identity field customers:legal-identity-manage, a billing field
// customers:billing-manage.

// importPermission is the key a write needs by hand, named in the refusal.
const (
	customersCreate = "customers:create"
	customersUpdate = "customers:update"
)

// importFields are the fields a mapping may name: the file format's import
// columns (csvfile.go's table less the export-only four), customerNumber
// among them as the match key it now is.
var importFields = func() map[string]csvColumn {
	fields := map[string]csvColumn{}
	for _, c := range customerCSVColumns {
		if c.Group != csvGroupExportOnly {
			fields[c.Name] = c
		}
	}
	return fields
}()

// importMappingJSON is the mapping as the import row stores it: the contract's
// own shape, so what was saved is what is answered.
func importMappingJSON(m gen.CustomerImportMapping) ([]byte, error) {
	return json.Marshal(m)
}

// importMappingFromJSON reads a stored mapping; nil for an import that has
// none yet.
func importMappingFromJSON(data []byte) (*gen.CustomerImportMapping, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var m gen.CustomerImportMapping
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("customers: read an import's mapping: %w", err)
	}
	return &m, nil
}

// validateImportMapping is the mapping checked (design D3, D4): its mode and
// match key, every entry — a known field, exactly one of a column of the file
// and a fixed value, a field at most once, a column at most once, the match
// column mapped for a mode that matches, a fixed value never on
// customerNumber, at least one field written — and the permissions the mode
// and the fields need. The errors are the contract's field map, keyed mode,
// matchBy or columns; on success the layout the rows run through.
func (s *server) validateImportMapping(ctx context.Context, m gen.CustomerImportMapping, header []string) (importLayout, map[string][]string) {
	errs := map[string][]string{}
	fail := func(field, message string) { errs[field] = append(errs[field], message) }

	switch m.Mode {
	case gen.Create, gen.Update, gen.CreateOrUpdate:
	default:
		fail("mode", fmt.Sprintf("A mode must be one of 'create', 'update' or 'createOrUpdate', but was '%s'", m.Mode))
	}
	matches := m.Mode == gen.Update || m.Mode == gen.CreateOrUpdate
	var matchBy gen.CustomerImportMappingMatchBy
	if matches {
		switch {
		case m.MatchBy == nil:
			fail("matchBy", "A mode that updates customers needs 'matchBy': 'customerNumber' or 'legalId'")
		case *m.MatchBy == gen.CustomerNumber, *m.MatchBy == gen.LegalId:
			matchBy = *m.MatchBy
		default:
			fail("matchBy", fmt.Sprintf("'matchBy' must be one of 'customerNumber' or 'legalId', but was '%s'", *m.MatchBy))
		}
	}

	l := importLayout{column: map[string]int{}, fixed: map[string]string{}, width: len(header), mode: m.Mode, matchBy: matchBy, allowDuplicateIdentity: m.AllowDuplicateIdentity}
	columnTaken := map[int]string{}
	written := 0
	for i, entry := range m.Columns {
		at := fmt.Sprintf("columns[%d]", i)
		field, known := importFields[entry.Field]
		if !known {
			fail("columns", fmt.Sprintf("%s names '%s', which is not a field an import writes", at, entry.Field))
			continue
		}
		if l.mapped(field.Name) {
			fail("columns", fmt.Sprintf("%s maps '%s' a second time", at, field.Name))
			continue
		}
		switch {
		case entry.Column != nil && entry.Value != nil:
			fail("columns", fmt.Sprintf("%s has both a column and a value; a field is read from one or the other", at))
		case entry.Column == nil && entry.Value == nil:
			fail("columns", fmt.Sprintf("%s has neither a column nor a value", at))
		case entry.Value != nil && field.Name == "customerNumber":
			fail("columns", fmt.Sprintf("%s gives customerNumber one value for every row; a match key is read from a column", at))
		case entry.Value != nil:
			l.fixed[field.Name] = *entry.Value
		case *entry.Column < 0 || int(*entry.Column) >= len(header):
			fail("columns", fmt.Sprintf("%s names column %d, but the file has columns 0 to %d", at, *entry.Column, len(header)-1))
		case columnTaken[int(*entry.Column)] != "":
			fail("columns", fmt.Sprintf("%s maps column %d a second time; it is already '%s'", at, *entry.Column, columnTaken[int(*entry.Column)]))
		default:
			l.column[field.Name] = int(*entry.Column)
			columnTaken[int(*entry.Column)] = field.Name
		}
		if field.Name != "customerNumber" {
			written++
		}
	}
	if written == 0 && errs["columns"] == nil {
		fail("columns", "No field is mapped; map at least one column to a customer field")
	}
	if matches && matchBy != "" {
		key := "customerNumber"
		if matchBy == gen.LegalId {
			key = "legalId"
		}
		if _, ok := l.column[key]; !ok {
			fail("columns", fmt.Sprintf("Matching by %s needs a column mapped to '%s'", key, key))
		}
	}

	// The content rule (design D4): the keys the mode and the fields need by
	// hand, each named in its refusal.
	if m.Mode == gen.Create || m.Mode == gen.CreateOrUpdate {
		if !s.hasPermission(ctx, customersCreate) {
			fail("mode", importPermissionRefusal("Creating customers", customersCreate))
		}
	}
	if matches && !s.hasPermission(ctx, customersUpdate) {
		fail("mode", importPermissionRefusal("Updating customers", customersUpdate))
	}
	if l.has(csvGroupIdentity) && !s.hasPermission(ctx, legalIdentityManage) {
		fail("columns", importPermissionRefusal("Mapping the legal identity's fields ("+strings.Join(csvColumnsOf(csvGroupIdentity), ", ")+")", legalIdentityManage))
	}
	if l.has(csvGroupBilling) && !s.hasPermission(ctx, billingManage) {
		fail("columns", importPermissionRefusal("Mapping the billing profile's fields ("+strings.Join(csvColumnsOf(csvGroupBilling), ", ")+")", billingManage))
	}
	if len(errs) > 0 {
		return importLayout{}, errs
	}
	return l, nil
}

// importPermissionRefusal names what the mapping does and the key it needs.
func importPermissionRefusal(what, key string) string {
	return fmt.Sprintf("%s needs the %s permission, which you do not have", what, key)
}

// layoutFor reads a stored mapping into the layout the rows run through —
// the mapping was validated when it was saved, so nothing is judged here; a
// row it cannot read (a column past a header that cannot have changed) is
// read as blank.
func layoutFor(m gen.CustomerImportMapping, width int) importLayout {
	l := importLayout{column: map[string]int{}, fixed: map[string]string{}, width: width, mode: m.Mode, allowDuplicateIdentity: m.AllowDuplicateIdentity}
	if m.MatchBy != nil {
		l.matchBy = *m.MatchBy
	}
	for _, entry := range m.Columns {
		if _, known := importFields[entry.Field]; !known {
			continue
		}
		switch {
		case entry.Value != nil:
			l.fixed[entry.Field] = *entry.Value
		case entry.Column != nil && *entry.Column >= 0 && int(*entry.Column) < width:
			l.column[entry.Field] = int(*entry.Column)
		}
	}
	return l
}

// identityColumnsOf is where the duplicate pass reads a row's legal id and
// country from (import_worker.go): the 1-based column of each for
// PostgreSQL's cells[n], 0 — which reads as NULL — for a field that is fixed
// or not mapped, and the fixed values beside them.
func (l importLayout) identityColumnsOf() (idColumn, countryColumn int32, fixedID, fixedCountry string) {
	if i, ok := l.column["legalId"]; ok {
		idColumn = int32(i + 1)
	}
	if i, ok := l.column["legalCountry"]; ok {
		countryColumn = int32(i + 1)
	}
	return idColumn, countryColumn, l.fixed["legalId"], l.fixed["legalCountry"]
}

// importStateIs reports whether state is one of states.
func importStateIs(state string, states ...gen.CustomerImportState) bool {
	return slices.ContainsFunc(states, func(s gen.CustomerImportState) bool { return string(s) == state })
}
