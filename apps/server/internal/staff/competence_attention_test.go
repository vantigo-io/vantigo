package staff_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The competence attention items (staff competence design §4's table; plan
// Task 3b): computed on read, whatever the worker stamped. A declared row is
// staff:manage's, staff:approve's and the person's managers'; an expiring or
// expired one staff:manage's and the managers'. Only active, unerased
// people's rows raise items, and a caller never sees their own.

// competenceItemIDs is the competence items' ids among items, sorted.
func competenceItemIDs(items []gen.StaffStatsAttentionItem) []string {
	var out []string
	for _, it := range items {
		if strings.HasPrefix(string(it.Type), "competence") {
			out = append(out, it.Id)
		}
	}
	slices.Sort(out)
	return out
}

// itemID is the id of the item of kind about row.
func itemID(kind gen.StaffStatsAttentionItemType, row int64) string {
	return string(kind) + "/" + idText(row)
}

func sortedItemIDs(ids ...string) []string {
	slices.Sort(ids)
	return ids
}

func TestCompetenceAttention_PerAudience(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorld(t)
	h := w.h
	fse, hms, truck := catalogueID(t, h, "fse"), catalogueID(t, h, "hms-kort"), catalogueID(t, h, "truck")
	skill := companySkill(t, h, "sveising", "Sveising")
	two := int16(2)

	// Kari, Bjørn's report: a declaration (FSE, its end beyond its lead of
	// 30 days), HMS-kort within its lead of 60 days, a truck certificate
	// ended on 1 September.
	kariDeclared := rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: fse, from: "2026-09-01", to: strPtr("2027-09-01")})
	kariExpiring := rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: hms, from: "2024-10-13", to: strPtr("2026-10-12"), verified: true})
	kariExpired := rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: truck, from: "2021-09-02", to: strPtr("2026-09-01"), verified: true})
	// Bjørn's own declaration.
	bossDeclared := rawPersonCompetence(t, h, competenceRow{person: w.boss, competence: hms, from: "2026-09-01"})
	// Ola has no manager: a declared skill and an ended FSE.
	ola := seedPerson(t, h, "Ola", "Nordmann")
	seedEmployment(t, h, ola, 0)
	olaDeclared := rawPersonCompetence(t, h, competenceRow{person: ola, competence: skill, level: &two, from: "2026-09-01"})
	olaExpired := rawPersonCompetence(t, h, competenceRow{person: ola, competence: fse, from: "2025-09-02", to: strPtr("2026-09-01"), verified: true})
	// A leaver, an erased person and a hire not yet started raise nothing.
	leaver := seedPerson(t, h, "Per", "Sluttet")
	seedEmploymentDated(t, h, leaver, w.boss, "2024-01-01", dayPtr("2026-08-31"))
	rawPersonCompetence(t, h, competenceRow{person: leaver, competence: hms, from: "2026-01-01"})
	rawPersonCompetence(t, h, competenceRow{person: leaver, competence: fse, from: "2025-09-02", to: strPtr("2026-09-01"), verified: true})
	erased := seedPerson(t, h, "Eva", "Glemt")
	seedEmployment(t, h, erased, w.boss)
	h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, erased, h.Now())
	rawPersonCompetence(t, h, competenceRow{person: erased, competence: hms, from: "2026-01-01"})
	hire := seedPerson(t, h, "Nina", "Ny")
	seedEmploymentDated(t, h, hire, w.boss, "2026-10-01", nil)
	rawPersonCompetence(t, h, competenceRow{person: hire, competence: hms, from: "2026-01-01"})

	hr, _ := signIn(t, h, "staff:manage")
	approver, _ := signIn(t, h, "staff:approve")
	olaApprover, olaUser := signIn(t, h, "staff:approve")
	link(t, h, ola, olaUser)
	kariC, kariUser := signIn(t, h)
	link(t, h, w.kari, kariUser)
	viewer, _ := signIn(t, h, "staff:view")

	kariItems := []string{
		itemID(gen.CompetenceDeclared, kariDeclared), itemID(gen.CompetenceExpiring, kariExpiring), itemID(gen.CompetenceExpired, kariExpired),
	}
	for _, tc := range []struct {
		who    string
		caller *modtest.Client
		want   []string
	}{
		{"staff:manage", hr, sortedItemIDs(append(slices.Clone(kariItems), itemID(gen.CompetenceDeclared, bossDeclared),
			itemID(gen.CompetenceDeclared, olaDeclared), itemID(gen.CompetenceExpired, olaExpired))...)},
		{"staff:approve", approver, sortedItemIDs(itemID(gen.CompetenceDeclared, kariDeclared), itemID(gen.CompetenceDeclared, bossDeclared),
			itemID(gen.CompetenceDeclared, olaDeclared))},
		{"staff:approve, never their own", olaApprover, sortedItemIDs(itemID(gen.CompetenceDeclared, kariDeclared), itemID(gen.CompetenceDeclared, bossDeclared))},
		{"the manager, their report's only", w.bossC, sortedItemIDs(kariItems...)},
		{"the person", kariC, nil},
		{"staff:view", viewer, nil},
	} {
		if got := competenceItemIDs(attentionItems(t, tc.caller)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: items = %v, want %v", tc.who, got, tc.want)
		}
	}

	byID := map[string]gen.StaffStatsAttentionItem{}
	for _, it := range attentionItems(t, hr) {
		byID[it.Id] = it
	}
	declared, expiring := byID[itemID(gen.CompetenceDeclared, kariDeclared)], byID[itemID(gen.CompetenceExpiring, kariExpiring)]
	if declared.Title != "Kari Nordmann" || declared.EntityId != idText(w.kari) || !declared.OccurredAt.Equal(h.Now()) {
		t.Errorf("the declaration's item = %+v", declared)
	}
	if expiring.Title != "Kari Nordmann" || expiring.EntityId != idText(w.kari) ||
		!expiring.OccurredAt.Equal(time.Date(2026, time.October, 12, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("the expiring item = %+v", expiring)
	}

	// The items do not wait for the worker, nor go when it has stamped.
	runCompetence(t, h)
	if got := competenceItemIDs(attentionItems(t, w.bossC)); !slices.Equal(got, sortedItemIDs(kariItems...)) {
		t.Errorf("after the worker's cycle the manager's items = %v", got)
	}
}
