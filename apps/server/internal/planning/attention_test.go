package planning_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

const attentionPath = "/api/v1/planning/stats/attention"

// TestAttention_Audiences is S22's four items of delivery A, each to its
// audience, oldest first: Per unbooked from today; Kari's certificate
// expiring inside her confirmed row; Kari overbooked in the week her two
// rows meet; and, to planning:manage, her row on INTERN ending with no
// follow-on — while her row on KVEM1000 has one (the INTERN row runs past
// it) and Ola's first row is followed within the week. A caller with
// neither role gets an empty list, never a 403.
func TestAttention_Audiences(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setUnbooked(t, h, 4, 50)
	h.workforce.addPerson(personPer, "Per Hansen", "A-1004", date("2026-01-01"), date("2029-12-31"), 37.5)
	certificateEnd := date("2026-09-20")
	h.workforce.setHeld(personKari, contracts.HeldCompetence{CompetenceID: 7001, Key: "hms-kort", Kind: "certificate", Verified: true,
		ValidFrom: date("2025-01-01"), ValidTo: &certificateEnd})
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: week38, to: "2026-09-25", load: 100})
	intern := seedAllocation(t, h, seedRow{person: personKari, project: projectInternal, from: week39, to: "2026-09-27", unit: "hours_per_week", load: 20})
	seedAllocation(t, h, seedRow{person: personOla, project: projectActive, from: week38, to: "2026-09-18", load: 100})
	seedAllocation(t, h, seedRow{person: personOla, project: projectActive, from: "2026-09-22", to: "2026-10-30", load: 100})

	_, week := date(week39).ISOWeek()
	viewItems := []gen.PlanningStatsAttentionItem{
		{Id: "unbooked/1004", Type: gen.PlanningStatsAttentionItemTypeUnbooked, Title: "Per Hansen", EntityId: "1004", OccurredAt: date("2026-09-12")},
		{Id: fmt.Sprintf("certificatesExpiring/%d/7001", intern-1), Type: gen.PlanningStatsAttentionItemTypeCertificatesExpiring,
			Title: "Kari Nordmann, hms-kort", EntityId: "1001", OccurredAt: certificateEnd},
		{Id: "overbooked/1001", Type: gen.PlanningStatsAttentionItemTypeOverbooked, Title: fmt.Sprintf("Kari Nordmann, week %d", week),
			EntityId: "1001", OccurredAt: date(week39)},
	}
	ending := gen.PlanningStatsAttentionItem{
		Id: fmt.Sprintf("endingWithoutFollowOn/%d", intern), Type: gen.PlanningStatsAttentionItemTypeEndingWithoutFollowOn,
		Title: "Kari Nordmann, INTERN", EntityId: fmt.Sprint(intern), OccurredAt: date("2026-09-27"),
	}

	cases := []struct {
		name        string
		permissions []string
		want        []gen.PlanningStatsAttentionItem
	}{
		{name: "planning:view", permissions: []string{"planning:view"}, want: viewItems},
		{name: "planning:manage", permissions: []string{"planning:manage"}, want: []gen.PlanningStatsAttentionItem{ending}},
		{name: "both", permissions: []string{"planning:view", "planning:manage"}, want: append(viewItems[:3:3], ending)},
		{name: "neither", want: []gen.PlanningStatsAttentionItem{}},
	}
	for _, tc := range cases {
		var got []gen.PlanningStatsAttentionItem
		getJSON(t, h.SignIn(t, append([]string{"planning:access"}, tc.permissions...)...), attentionPath, http.StatusOK, &got)
		if len(got) != len(tc.want) {
			t.Errorf("%s: items = %+v, want %+v", tc.name, got, tc.want)
			continue
		}
		for i, item := range got {
			w := tc.want[i]
			if item.Id != w.Id || item.Type != w.Type || item.Title != w.Title || item.EntityId != w.EntityId || !item.OccurredAt.Equal(w.OccurredAt) {
				t.Errorf("%s: item %d = %+v, want %+v", tc.name, i, item, w)
			}
		}
	}
}
