package planning_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
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

// TestAttention_RequestsOpen is S22's requestsOpen, planning:manage's: one
// item per open request starting within requestLeadDays (14) of today,
// Saturday 12 September, or already started, dated on its first day and
// titled with the project's code — its id when the directory does not
// resolve it. A request starting after the lead, a filled one and a
// withdrawn one are none; planning:view alone sees none.
func TestAttention_RequestsOpen(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	within := seedRequest(t, h, seedReq{project: projectActive, from: "2026-09-21", to: "2026-10-30", title: "Utvikler"})
	last := seedRequest(t, h, seedReq{project: projectUnknown, from: "2026-09-26", to: "2026-10-30", title: "Arkitekt"})
	started := seedRequest(t, h, seedReq{project: projectPlanned, from: "2026-08-03", to: "2026-10-30", title: "Tester"})
	seedRequest(t, h, seedReq{project: projectActive, from: "2026-09-27", to: "2026-10-30"})
	seedRequest(t, h, seedReq{project: projectActive, from: "2026-09-14", to: "2026-10-30", status: "filled"})
	seedRequest(t, h, seedReq{project: projectActive, from: "2026-09-14", to: "2026-10-30", status: "withdrawn"})

	var got []gen.PlanningStatsAttentionItem
	getJSON(t, h.SignIn(t, "planning:access", "planning:manage"), attentionPath, http.StatusOK, &got)
	want := []gen.PlanningStatsAttentionItem{
		{Id: fmt.Sprintf("requestsOpen/%d", started), Type: gen.PlanningStatsAttentionItemTypeRequestsOpen, Title: "KVEM2000 — Tester",
			EntityId: fmt.Sprint(started), OccurredAt: date("2026-08-03")},
		{Id: fmt.Sprintf("requestsOpen/%d", within), Type: gen.PlanningStatsAttentionItemTypeRequestsOpen, Title: "KVEM1000 — Utvikler",
			EntityId: fmt.Sprint(within), OccurredAt: date("2026-09-21")},
		{Id: fmt.Sprintf("requestsOpen/%d", last), Type: gen.PlanningStatsAttentionItemTypeRequestsOpen, Title: "2999 — Arkitekt",
			EntityId: fmt.Sprint(last), OccurredAt: date("2026-09-26")},
	}
	if len(got) != len(want) {
		t.Fatalf("items = %+v, want %+v", got, want)
	}
	for i, item := range got {
		w := want[i]
		if item.Id != w.Id || item.Type != w.Type || item.Title != w.Title || item.EntityId != w.EntityId || !item.OccurredAt.Equal(w.OccurredAt) {
			t.Errorf("item %d = %+v, want %+v", i, item, w)
		}
	}

	got = nil
	getJSON(t, h.SignIn(t, "planning:access", "planning:view"), attentionPath, http.StatusOK, &got)
	for _, item := range got {
		if item.Type == gen.PlanningStatsAttentionItemTypeRequestsOpen {
			t.Errorf("planning:view alone sees %+v, want no requestsOpen item", item)
		}
	}
}

// TestAttention_WishesUnseen is S22's wishesUnseen and plan reading 9: one
// item to planning:manage while any open wish is unseen, counting them,
// about the oldest unseen and titled with its person's name — none to
// planning:view alone, and none once every wish is seen.
func TestAttention_WishesUnseen(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seen := seedOpenWish(t, h, personOla, "new", nil, "2026-10-01", 0)
	h.Exec(t, `UPDATE planning.wishes SET seen_at = $2 WHERE id = $1`, seen, h.Now())
	oldest := seedOpenWish(t, h, personKari, "new", nil, "2026-10-01", time.Hour)
	newer := seedOpenWish(t, h, personOla, "off", ptr(projectActive), "2026-10-01", 2*time.Hour)

	unseen := func(c *modtest.Client) []gen.PlanningStatsAttentionItem {
		var got []gen.PlanningStatsAttentionItem
		getJSON(t, c, attentionPath, http.StatusOK, &got)
		var out []gen.PlanningStatsAttentionItem
		for _, item := range got {
			if item.Type == gen.PlanningStatsAttentionItemTypeWishesUnseen {
				out = append(out, item)
			}
		}
		return out
	}
	manager := h.SignIn(t, "planning:access", "planning:manage")
	items := unseen(manager)
	if len(items) != 1 {
		t.Fatalf("items = %+v, want one", items)
	}
	item := items[0]
	if item.Id != "wishesUnseen/all" || item.EntityId != fmt.Sprint(oldest) || item.Title != "Kari Nordmann" ||
		item.Count == nil || *item.Count != 2 || !item.OccurredAt.Equal(h.Now().Add(time.Hour)) {
		t.Errorf("item = %+v, want two unseen, about Kari's", item)
	}
	if items := unseen(h.SignIn(t, "planning:access", "planning:view")); len(items) != 0 {
		t.Errorf("planning:view alone sees %+v", items)
	}

	h.workforce.setVisible(false)
	if items := unseen(manager); len(items) != 1 || items[0].Title != fmt.Sprint(personKari) {
		t.Errorf("without the workforce = %+v, want the person's id as the title", items)
	}
	h.Exec(t, `UPDATE planning.wishes SET seen_at = $2 WHERE id IN ($1, $3)`, oldest, h.Now(), newer)
	if items := unseen(manager); len(items) != 0 {
		t.Errorf("all seen = %+v, want no item", items)
	}
}
