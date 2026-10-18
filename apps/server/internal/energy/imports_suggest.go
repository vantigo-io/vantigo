package energy

import (
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/fileimport"
)

// This file is the suggested mapping (energy import design D3): the customers
// import's three tiers — the mapping last saved on an import with the same
// header, a field's own JSON name, the alias table below — with one rule of
// its own. A supplier's export carries the customer's postal address
// (POSTNR, POSTSTED) beside the installation's (POSTNR ANLEGG, POSTSTED
// ANLEGG), and the point's address is the installation's: where a field has an
// installation alias and a plain one, the installation alias wins whatever
// the column order. A suggestion is only a suggestion: the mapping the person
// saves is the mapping.

// importAliases are the header words that mean a field, matched after
// fileimport.HeaderKey (lowercased, NFC, nothing but letters and digits, so
// "ORG. NR." is orgnr and "POSTNR ANLEGG" postnranlegg). installation are the
// words that name the installation's own value and win over plain.
var importAliases = map[string]struct{ installation, plain []string }{
	fieldGsrn:            {plain: []string{"målepunkt", "målepunktid", "meteringpointid", "gsrn"}},
	fieldMeterNumber:     {plain: []string{"målernummer", "målernr", "meterno"}},
	fieldStreetAddress:   {installation: []string{"anleggsadresse"}, plain: []string{"adresse"}},
	fieldPostalCode:      {installation: []string{"postnranlegg", "postnummeranlegg"}, plain: []string{"postnr", "postnummer"}},
	fieldCity:            {installation: []string{"poststedanlegg"}, plain: []string{"poststed"}},
	fieldPriceArea:       {plain: []string{"prisområde", "elspotområde"}},
	fieldGridArea:        {plain: []string{"netteier", "nettområde", "nettselskap"}},
	fieldExpectedAnnual:  {plain: []string{"årsforbruk", "forventetårsforbruk", "forventetårsforbrukkwh"}},
	fieldCustomerLegalID: {plain: []string{"orgnr", "organisasjonsnummer", "kundeorgnr"}},
	fieldSupplyStart:     {plain: []string{"oppstart", "startdato", "startdatokunde", "leveringsstart"}},
}

// The ranks a header can match a field at: a plain alias, an installation
// alias, the field's own JSON name. A field is suggested from the column of
// the highest rank, the first such column on a tie.
const (
	rankPlain = iota + 1
	rankInstallation
	rankJSONName
)

type aliasMatch struct {
	field string
	rank  int
}

// aliasIndex is importAliases inverted, plus every field's own JSON name, each
// under its fileimport.HeaderKey.
var aliasIndex = func() map[string]aliasMatch {
	index := map[string]aliasMatch{}
	for field, aliases := range importAliases {
		for _, alias := range aliases.plain {
			index[fileimport.HeaderKey(alias)] = aliasMatch{field: field, rank: rankPlain}
		}
		for _, alias := range aliases.installation {
			index[fileimport.HeaderKey(alias)] = aliasMatch{field: field, rank: rankInstallation}
		}
	}
	for _, field := range importFields {
		index[fileimport.HeaderKey(field)] = aliasMatch{field: field, rank: rankJSONName}
	}
	return index
}()

// suggestMapping is the mapping the page starts from (design D3): previous is
// the mapping last saved on an import with this very header, nil when none;
// otherwise each field from the best-ranked column that names it. The mode is
// createOrUpdate: every row is matched by its GSRN, and a file of points is
// as often the first as the next.
func suggestMapping(header []string, previous *gen.EnergyImportMapping) gen.EnergyImportMapping {
	if previous != nil {
		return *previous
	}
	best := map[string]int{}
	rank := map[string]int{}
	for i, h := range header {
		m, ok := aliasIndex[fileimport.HeaderKey(h)]
		if !ok || rank[m.field] >= m.rank {
			continue
		}
		best[m.field], rank[m.field] = i, m.rank
	}
	columns := []gen.EnergyImportMappingColumn{}
	for i := range header {
		for _, field := range importFields {
			if c, ok := best[field]; ok && c == i {
				column := int32(i)
				columns = append(columns, gen.EnergyImportMappingColumn{Field: field, Column: &column})
			}
		}
	}
	return gen.EnergyImportMapping{Mode: gen.CreateOrUpdate, Columns: columns}
}
