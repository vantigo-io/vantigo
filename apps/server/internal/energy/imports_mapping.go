package energy

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
)

// This file is the mapping (energy import design D3, D5): the JSON document
// PUT /energy/imports/{id}/mapping saves, read against the file's header into
// the importLayout import.go runs rows through, and checked — before anything
// runs — against what its sender could do by hand. energy:import is the door;
// the mapping is where the content is judged: every mode writes metering
// points and needs energy:metering-points-manage, a mode that creates also
// energy:meters-manage (a point is created with its first meter, which the
// create endpoint itself demands), and mapping customerLegalId needs
// energy:supply-periods-manage.

// The keys a mapping may need, named in its refusals.
const (
	meteringPointsManage = "energy:metering-points-manage"
	metersManage         = "energy:meters-manage"
	supplyPeriodsManage  = "energy:supply-periods-manage"
)

// importMappingJSON is the mapping as the import row stores it: the
// contract's own shape, so what was saved is what is answered.
func importMappingJSON(m gen.EnergyImportMapping) ([]byte, error) {
	return json.Marshal(m)
}

// importMappingFromJSON reads a stored mapping; nil for an import that has
// none yet.
func importMappingFromJSON(data []byte) (*gen.EnergyImportMapping, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var m gen.EnergyImportMapping
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("energy: read an import's mapping: %w", err)
	}
	return &m, nil
}

// validateImportMapping is the mapping checked (design D3, D5): its mode, and
// every entry — a known field, exactly one of a column of the file and a
// fixed value, a field at most once, a column at most once, gsrn mapped to a
// column, supplyStart only beside customerLegalId, at least one field besides
// gsrn — then customerLegalId refused when no module provides the customer
// directory, and the permissions the mode and the fields need. The errors are
// the contract's field map, keyed mode or columns; on success the layout the
// rows run through.
func (s *server) validateImportMapping(ctx context.Context, m gen.EnergyImportMapping, header []string) (importLayout, map[string][]string) {
	errs := map[string][]string{}
	fail := func(field, message string) { errs[field] = append(errs[field], message) }

	switch m.Mode {
	case gen.Create, gen.Update, gen.CreateOrUpdate:
	default:
		fail("mode", fmt.Sprintf("A mode must be one of 'create', 'update' or 'createOrUpdate', but was '%s'", m.Mode))
	}

	l := importLayout{column: map[string]int{}, fixed: map[string]string{}, width: len(header), mode: m.Mode}
	columnTaken := map[int]string{}
	for i, entry := range m.Columns {
		at := fmt.Sprintf("columns[%d]", i)
		if !slices.Contains(importFields, entry.Field) {
			fail("columns", fmt.Sprintf("%s names '%s', which is not a field an import writes", at, entry.Field))
			continue
		}
		if l.mapped(entry.Field) {
			fail("columns", fmt.Sprintf("%s maps '%s' a second time", at, entry.Field))
			continue
		}
		switch {
		case entry.Column != nil && entry.Value != nil:
			fail("columns", fmt.Sprintf("%s has both a column and a value; a field is read from one or the other", at))
		case entry.Column == nil && entry.Value == nil:
			fail("columns", fmt.Sprintf("%s has neither a column nor a value", at))
		case entry.Value != nil && entry.Field == fieldGsrn:
			fail("columns", fmt.Sprintf("%s gives gsrn one value for every row; a metering point is matched by a column", at))
		case entry.Value != nil:
			l.fixed[entry.Field] = *entry.Value
		case *entry.Column < 0 || int(*entry.Column) >= len(header):
			fail("columns", fmt.Sprintf("%s names column %d, but the file has columns 0 to %d", at, *entry.Column, len(header)-1))
		case columnTaken[int(*entry.Column)] != "":
			fail("columns", fmt.Sprintf("%s maps column %d a second time; it is already '%s'", at, *entry.Column, columnTaken[int(*entry.Column)]))
		default:
			l.column[entry.Field] = int(*entry.Column)
			columnTaken[int(*entry.Column)] = entry.Field
		}
	}
	if errs["columns"] == nil {
		if _, ok := l.column[fieldGsrn]; !ok {
			fail("columns", "A column must be mapped to 'gsrn': every row is matched by its GSRN")
		}
		if len(l.column)+len(l.fixed) < 2 {
			fail("columns", "No field is mapped besides gsrn; map at least one column to a metering-point field or to customerLegalId")
		}
		if l.mapped(fieldSupplyStart) && !l.mapped(fieldCustomerLegalID) {
			fail("columns", "supplyStart is the start of a customer's supply period and needs customerLegalId mapped beside it")
		}
	}
	if l.mapped(fieldCustomerLegalID) && s.deps.Directory == nil {
		fail("columns", customersModuleOffMessage+"; leave customerLegalId unmapped")
	}

	// The content rule (design D5): the keys the mode and the fields need by
	// hand, each named in its refusal.
	if !s.hasPermission(ctx, meteringPointsManage) {
		fail("mode", importPermissionRefusal("Creating and updating metering points", meteringPointsManage))
	}
	if (m.Mode == gen.Create || m.Mode == gen.CreateOrUpdate) && !s.hasPermission(ctx, metersManage) {
		fail("mode", importPermissionRefusal("Creating metering points, each with its first meter,", metersManage))
	}
	if l.mapped(fieldCustomerLegalID) && !s.hasPermission(ctx, supplyPeriodsManage) {
		fail("columns", importPermissionRefusal("Mapping customerLegalId, which writes supply periods,", supplyPeriodsManage))
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

// hasPermission reports whether the caller holds key, asked of the access
// layer the router itself asks.
func (s *server) hasPermission(ctx context.Context, key string) bool {
	return contracts.HasPermission(ctx, s.deps.Access, key)
}

// layoutFor reads a stored mapping into the layout the rows run through — the
// mapping was validated when it was saved, so nothing is judged here; a column
// past the header is read as unmapped.
func layoutFor(m gen.EnergyImportMapping, width int) importLayout {
	l := importLayout{column: map[string]int{}, fixed: map[string]string{}, width: width, mode: m.Mode}
	for _, entry := range m.Columns {
		if !slices.Contains(importFields, entry.Field) {
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

// keyColumnsOf is where the pass before the first row reads a row's GSRN and
// organisation number from (import_worker.go): the 1-based column of each for
// PostgreSQL's cells[n], 0 — which reads as NULL — for one not mapped to a
// column, and a fixed organisation number beside them.
func (l importLayout) keyColumnsOf() (gsrnColumn, legalIDColumn int32, fixedLegalID string) {
	if i, ok := l.column[fieldGsrn]; ok {
		gsrnColumn = int32(i + 1)
	}
	if i, ok := l.column[fieldCustomerLegalID]; ok {
		legalIDColumn = int32(i + 1)
	}
	return gsrnColumn, legalIDColumn, l.fixed[fieldCustomerLegalID]
}

// importStateIs reports whether state is one of states.
func importStateIs(state string, states ...gen.EnergyImportState) bool {
	return slices.ContainsFunc(states, func(s gen.EnergyImportState) bool { return string(s) == state })
}
