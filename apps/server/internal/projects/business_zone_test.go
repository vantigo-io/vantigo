package projects_test

import (
	"context"
	"fmt"
	"testing"
	"time"
	_ "time/tzdata" // America/New_York wherever the tests run

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// Every day this module derives from an instant is a day of the workspace's
// business time zone (time zones design D2, D7), read through the business
// calendar on the request that judges it. A milestone marked ready at 23:30
// UTC on 30 June became ready on 1 July in Oslo, and on 30 June under UTC
// and in New York: the billable read's Until — the day Invoices dates the
// milestone by — follows the zone.
func TestBusinessZone_BillableUntilIsTheDayTheMilestoneBecameReady(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "BZ0001")
	m := readyMilestone(t, c, project.Id, map[string]any{"name": "Klar sent"})
	h.Exec(t, `UPDATE projects.billing_milestones SET ready_at = $2 WHERE id = $1`,
		m.Id, time.Date(2026, time.June, 30, 23, 30, 0, 0, time.UTC))
	read := newBillableMilestones(t, h)

	june30 := time.Date(2026, time.June, 30, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		zone  string
		until time.Time
		want  int
	}{
		{"Europe/Oslo", june30, 0},
		{"Europe/Oslo", june30.AddDate(0, 0, 1), 1},
		{"UTC", june30, 1},
		{"UTC", june30.AddDate(0, 0, -1), 0},
		{"America/New_York", june30, 1},
		{"America/New_York", june30.AddDate(0, 0, -1), 0},
	} {
		h.SetBusinessTimeZone(t, tc.zone)
		for _, req := range []contracts.BillableRequest{
			{ProjectIDs: []int32{project.Id}, Until: tc.until},
			{IDs: []int64{int64(m.Id)}, Until: tc.until},
		} {
			page, err := read.BillableMilestones(context.Background(), req)
			if err != nil {
				t.Fatalf("%s until %s: %v", tc.zone, tc.until.Format(time.DateOnly), err)
			}
			if len(page.Milestones) != tc.want {
				t.Errorf("%s until %s (by ids %v): %d milestones, want %d",
					tc.zone, tc.until.Format(time.DateOnly), len(req.IDs) > 0, len(page.Milestones), tc.want)
			}
		}
	}
}

// The dashboard's "today" and a milestone's overdue flag are the business
// day. At 23:30 UTC on 12 September it is already the 13th in Oslo, so a
// project ending on the 12th and a milestone planned for it are overdue there
// — in the attention list, the invoice plan and the portfolio alike — and
// neither is under UTC or in New York, where it is still the 12th.
func TestBusinessZone_TodayAndOverdueFollowTheWorkspace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	day := h.Now().UTC().Format(time.DateOnly) // 2026-09-12
	project := portfolioProject(t, creator, "BZTODAY1", map[string]any{"endDate": day})
	late := createMilestone(t, creator, project.Id, map[string]any{"name": "Planlagt i dag", "plannedDate": day})

	h.Advance(time.Date(2026, time.September, 12, 23, 30, 0, 0, time.UTC).Sub(h.Now()))
	manager, managerID := signIn(t, h)
	addRole(t, h, project.Id, managerID, "manager")

	for _, tc := range []struct {
		zone    string
		overdue bool
	}{
		{"Europe/Oslo", true},
		{"UTC", false},
		{"America/New_York", false},
	} {
		h.SetBusinessTimeZone(t, tc.zone)
		items := readAttention(t, manager)
		projectItems := attentionOfType(items, "projectOverdue")
		milestoneItems := attentionOfType(items, "milestoneOverdue")
		if got := len(projectItems) == 1 && projectItems[0].EntityId == fmt.Sprint(project.Id); got != tc.overdue || len(projectItems) > 1 {
			t.Errorf("%s: projectOverdue items = %+v, want overdue %v", tc.zone, projectItems, tc.overdue)
		}
		wantID := fmt.Sprintf("%d/%d", project.Id, late.Id)
		if got := len(milestoneItems) == 1 && milestoneItems[0].EntityId == wantID; got != tc.overdue || len(milestoneItems) > 1 {
			t.Errorf("%s: milestoneOverdue items = %+v, want overdue %v", tc.zone, milestoneItems, tc.overdue)
		}
		if got := getMilestone(t, manager, late.Id).Overdue; got != tc.overdue {
			t.Errorf("%s: the milestone's overdue = %v, want %v", tc.zone, got, tc.overdue)
		}
		plan := getMilestones(t, manager, project.Id).Milestones
		if len(plan) != 1 || plan[0].Overdue != tc.overdue {
			t.Errorf("%s: the invoice plan = %+v, want the one milestone with overdue %v", tc.zone, plan, tc.overdue)
		}
		row := portfolioRow(t, getPortfolio(t, manager, ""), "BZTODAY1")
		if row.NextMilestone == nil || row.NextMilestone.Overdue != tc.overdue {
			t.Errorf("%s: the portfolio's next milestone = %+v, want overdue %v", tc.zone, row.NextMilestone, tc.overdue)
		}
	}
}
