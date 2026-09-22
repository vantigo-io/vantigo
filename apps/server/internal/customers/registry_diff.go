package customers

import (
	"fmt"
	"strings"
	"time"
)

// This file is the pure half of a registry refresh (Brreg in full design
// D4): what one freshly fetched record differs from the record on file in,
// and how each difference reads. Nothing here touches the database, the
// network or a request — registry.go owns all three — so every rule below
// is a table test away in registry_diff_test.go.
//
// Why a diff at all: the registry's record is a fact about the world, kept
// verbatim and overwritten on every fetch (design D1), so the row itself
// remembers nothing about what it used to say. The timeline does, as one
// registry.change event per refresh that found something — which is also
// why a refresh that found nothing writes no event: a person scrolling a
// customer's history should see the day it went bankrupt, not the ninety
// days it did not.

// registryChange is one field a refresh found different. From and To are
// already rendered as text (an employee count as a number, a flag as
// "true"/"false", a date as ISO, an address as its one-line form), because
// the wire carries them as strings and the timeline payload embeds exactly
// what the response shows. Either side is "" when that side was empty — a
// field not set before, or not set now — and the wire then omits it.
type registryChange struct {
	Field string
	From  string
	To    string
}

// registryAddressDisplay is the one-line rendering an address is both
// compared and reported as ("Forusbeen 50, 4035 STAVANGER, NO"), shaped like
// addresses.go's own addressDisplay for a customer address: the street lines
// first, the post code and city as one part, then the country code. The
// municipality is deliberately left out — it repeats the city on nearly
// every Norwegian address and is absent on every foreign one, so including
// it would make most addresses read "…, STAVANGER, STAVANGER, NO".
//
// Comparing the rendering rather than the struct is design D4's own choice:
// a person reading the timeline needs to see what the address became, and
// two addresses that render identically differ in nothing a person would
// call a change of address.
func registryAddressDisplay(a *registryAddress) string {
	if a == nil {
		return ""
	}
	parts := make([]string, 0, len(a.Lines)+2)
	parts = append(parts, a.Lines...)
	cityLine := a.PostalCode
	if a.City != "" {
		if cityLine != "" {
			cityLine += " "
		}
		cityLine += a.City
	}
	if cityLine != "" {
		parts = append(parts, cityLine)
	}
	if a.CountryCode != "" {
		parts = append(parts, a.CountryCode)
	}
	return strings.Join(parts, ", ")
}

// registryEmployeesDisplay renders a headcount: the number itself, or ""
// when the registry has not registered one at all (brreg_entity.go's
// Employees is nil for exactly that, distinct from a registered zero).
func registryEmployeesDisplay(employees *int32) string {
	if employees == nil {
		return ""
	}
	return fmt.Sprintf("%d", *employees)
}

// registryBoolDisplay renders a status flag as the literal the wire and the
// timeline both carry.
func registryBoolDisplay(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// registryDateDisplay renders one of the registry's dates as ISO, "" when
// absent.
func registryDateDisplay(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(civilDateLayout)
}

// diffRegistryRecords is what a refresh reports (design D4). before is the
// record on file, nil when this is the very first fetch for the customer;
// legalName is the legal identity's own name, used only in that first-fetch
// case.
//
// The first fetch is deliberately not a sixteen-field diff against zero
// values: there is nothing to have changed *from*, so almost nothing is
// worth reporting. Two things are. The registry's name against the name the
// user asserted when they picked the company — and when those agree, which
// they do for every ordinary Brreg pick, that half is silent, which is why a
// create's own after-commit fetch normally writes no timeline event at all.
// And the deletion date: picking a company that has already been struck from
// the register is exactly the kind of thing the person doing the picking
// needs told, and storing that silently would leave the timeline claiming
// nothing happened on the day it was found out.
//
// The order is this function's own, fixed — and the first-fetch pair follows
// the same order the full comparison below puts them in (name, then
// deletedOn), so the summary reads the same way whichever branch built it.
func diffRegistryRecords(before *registryRecord, legalName string, after registryRecord) []registryChange {
	if before == nil {
		var changes []registryChange
		// Trimmed on both sides, matching stats.go's own rename rule (fix round
		// 2, minors): the attention list and the timeline must agree about what
		// counts as a different name, and a legal name stored with a stray
		// trailing space is not a rename by anybody's reading.
		if strings.TrimSpace(after.Name) != strings.TrimSpace(legalName) {
			changes = append(changes, registryChange{Field: "name", From: legalName, To: after.Name})
		}
		if deletedOn := registryDateDisplay(after.DeletedOn); deletedOn != "" {
			changes = append(changes, registryChange{Field: "deletedOn", To: deletedOn})
		}
		return changes
	}

	var changes []registryChange
	add := func(field, from, to string) {
		if from != to {
			changes = append(changes, registryChange{Field: field, From: from, To: to})
		}
	}
	add("name", before.Name, after.Name)
	add("organisationForm", before.OrganisationForm, after.OrganisationForm)
	add("industryCode", before.IndustryCode, after.IndustryCode)
	add("employees", registryEmployeesDisplay(before.Employees), registryEmployeesDisplay(after.Employees))
	add("vatRegistered", registryBoolDisplay(before.VATRegistered), registryBoolDisplay(after.VATRegistered))
	add("bankrupt", registryBoolDisplay(before.Bankrupt), registryBoolDisplay(after.Bankrupt))
	add("underLiquidation", registryBoolDisplay(before.UnderLiquidation), registryBoolDisplay(after.UnderLiquidation))
	add("underForcedLiquidation", registryBoolDisplay(before.UnderForcedLiquidation), registryBoolDisplay(after.UnderForcedLiquidation))
	add("deletedOn", registryDateDisplay(before.DeletedOn), registryDateDisplay(after.DeletedOn))
	add("website", before.Website, after.Website)
	add("email", before.Email, after.Email)
	add("phone", before.Phone, after.Phone)
	add("mobile", before.Mobile, after.Mobile)
	add("parentOrganisationNumber", before.ParentOrganisationNumber, after.ParentOrganisationNumber)
	add("businessAddress", registryAddressDisplay(before.BusinessAddress), registryAddressDisplay(after.BusinessAddress))
	add("postalAddress", registryAddressDisplay(before.PostalAddress), registryAddressDisplay(after.PostalAddress))
	return changes
}

// registryChangeSummary is the sentence a registry.change event is listed
// under: the changed fields' own names, in diffRegistryRecords's order. It
// lives here rather than beside timeline_events.go's other summary builders
// because it reads the diff's field names, which are this file's to choose.
//
// Truncated to customers_timeline_entries.summary's varchar(500) for the
// same reason addressSummary is (timeline_events.go): sixteen field names
// cannot reach 500 UTF-16 units today, but the column's limit is the
// column's limit, and a summary that outgrew it would fail the insert on an
// otherwise perfectly good refresh.
func registryChangeSummary(changes []registryChange) string {
	fields := make([]string, 0, len(changes))
	for _, c := range changes {
		fields = append(fields, c.Field)
	}
	return truncateUTF16("Registry record updated: "+strings.Join(fields, ", "), 500)
}
