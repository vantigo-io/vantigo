package staff

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// gapToday is the day the gap rule's tables judge on.
var gapToday = time.Date(2026, time.October, 12, 0, 0, 0, 0, time.UTC)

// gapDay is a day relative to gapToday as a date column.
func gapDay(days int) pgtype.Date { return dayParam(gapToday.AddDate(0, 0, days)) }

// gapItem is a profile item on a competence of kind, with its lead.
func gapItem(kind string, minLevel *int16, mustBeValid bool, lead *int16) store.ListCompetenceProfileItemsRow {
	return store.ListCompetenceProfileItemsRow{
		StaffCompetenceProfileItem: store.StaffCompetenceProfileItem{
			ProfileID: 1, CompetenceID: 10, MinLevel: minLevel, MustBeValid: mustBeValid,
		},
		CompetenceKey: "c", CompetenceKind: kind, CompetenceHasLevel: kind == "skill", CompetenceReminderLeadDays: lead,
	}
}

// gapRow is a current row valid from from (days from gapToday) to to (nil:
// without end), verified or declared, at level.
func gapRow(level *int16, verified bool, from int, to *int) *store.ListCurrentCompetenceRowsOfRow {
	r := &store.ListCurrentCompetenceRowsOfRow{PersonID: 7, CompetenceID: 10, Level: level, ValidFrom: gapDay(from)}
	if verified {
		at := gapToday
		r.VerifiedAt = &at
	}
	if to != nil {
		r.ValidTo = gapDay(*to)
	}
	return r
}

func gapLevel(n int16) *int16 { return &n }
func gapDays(n int) *int      { return &n }

// The gap rule (staff competence design S13; plan readings 23 and 24), in
// its order: missing, declared_only, below_level, the validity when the
// item asks a valid row (expired, not yet valid as missing, expiring within
// the competence's lead), met. An item that does not ask a valid row never
// judges the validity.
func TestGapStatus_TheRuleInItsOrder(t *testing.T) {
	t.Parallel()
	lead30 := gapLevel(30)
	for _, tc := range []struct {
		name         string
		item         store.ListCompetenceProfileItemsRow
		row          *store.ListCurrentCompetenceRowsOfRow
		want         gen.StaffCompetenceGapStatus
		wantDeclared bool
	}{
		{"no row", gapItem("certificate", nil, true, lead30), nil, gen.GapMissing, false},
		{"no row of a skill with a level asked", gapItem("skill", gapLevel(2), true, nil), nil, gen.GapMissing, false},
		{"a skill declared at the level", gapItem("skill", gapLevel(2), true, nil), gapRow(gapLevel(2), false, -10, nil), gen.GapMet, true},
		{"a skill declared above the level", gapItem("skill", gapLevel(2), true, nil), gapRow(gapLevel(4), false, -10, nil), gen.GapMet, true},
		{"a skill verified below the level", gapItem("skill", gapLevel(3), true, nil), gapRow(gapLevel(2), true, -10, nil), gen.GapBelowLevel, false},
		{"a skill declared below the level", gapItem("skill", gapLevel(3), true, nil), gapRow(gapLevel(2), false, -10, nil), gen.GapBelowLevel, true},
		{"a skill at level 0 when any level does", gapItem("skill", nil, true, nil), gapRow(gapLevel(0), true, -10, nil), gen.GapMet, false},
		{"a certificate declared", gapItem("certificate", nil, true, lead30), gapRow(nil, false, -10, gapDays(300)), gen.GapDeclaredOnly, true},
		{"a certificate declared and expired", gapItem("certificate", nil, true, lead30), gapRow(nil, false, -400, gapDays(-1)), gen.GapDeclaredOnly, true},
		{"a certificate declared, validity not asked", gapItem("certificate", nil, false, lead30), gapRow(nil, false, -10, nil), gen.GapDeclaredOnly, true},
		{"a certificate verified and valid", gapItem("certificate", nil, true, lead30), gapRow(nil, true, -10, gapDays(300)), gen.GapMet, false},
		{"its end passed yesterday", gapItem("certificate", nil, true, lead30), gapRow(nil, true, -400, gapDays(-1)), gen.GapExpired, false},
		{"its end passed, validity not asked", gapItem("certificate", nil, false, lead30), gapRow(nil, true, -400, gapDays(-1)), gen.GapMet, false},
		{"valid from tomorrow", gapItem("certificate", nil, true, lead30), gapRow(nil, true, 1, gapDays(300)), gen.GapMissing, false},
		{"valid from tomorrow, validity not asked", gapItem("certificate", nil, false, lead30), gapRow(nil, true, 1, gapDays(300)), gen.GapMet, false},
		{"its end today", gapItem("certificate", nil, true, lead30), gapRow(nil, true, -300, gapDays(0)), gen.GapExpiring, false},
		{"its end on the lead's last day", gapItem("certificate", nil, true, lead30), gapRow(nil, true, -300, gapDays(30)), gen.GapExpiring, false},
		{"its end the day after the lead", gapItem("certificate", nil, true, lead30), gapRow(nil, true, -300, gapDays(31)), gen.GapMet, false},
		{"within the lead, validity not asked", gapItem("certificate", nil, false, lead30), gapRow(nil, true, -300, gapDays(5)), gen.GapMet, false},
		{"its end near, a competence without a lead", gapItem("certificate", nil, true, nil), gapRow(nil, true, -300, gapDays(0)), gen.GapMet, false},
		{"a competence without validity, must be valid", gapItem("certificate", nil, true, nil), gapRow(nil, true, -3000, nil), gen.GapMet, false},
		{"below the level and expired", gapItem("skill", gapLevel(3), true, lead30), gapRow(gapLevel(1), true, -400, gapDays(-1)), gen.GapBelowLevel, false},
	} {
		got, declared := gapStatus(gapInput{item: tc.item, row: tc.row, today: gapToday})
		if got != tc.want || declared != tc.wantDeclared {
			t.Errorf("%s: %s (declared %v), want %s (declared %v)", tc.name, got, declared, tc.want, tc.wantDeclared)
		}
	}
}

// Not met is every status but met and expiring (reading 23).
func TestNotMet_AllButMetAndExpiring(t *testing.T) {
	t.Parallel()
	for status, want := range map[gen.StaffCompetenceGapStatus]bool{
		gen.GapMet: false, gen.GapExpiring: false, gen.GapDeclaredOnly: true, gen.GapBelowLevel: true,
		gen.GapExpired: true, gen.GapMissing: true,
	} {
		if got := notMet(status); got != want {
			t.Errorf("notMet(%s) = %v, want %v", status, got, want)
		}
	}
}

// The gap list: an archived profile reaches nobody (reading 26); an
// archived competence's item stays, reported and counted (§9); the row's
// level, end and verification are carried; the items not met are counted
// per profile and in all.
func TestGapsOf_ProfilesItemsAndCounts(t *testing.T) {
	t.Parallel()
	archivedAt := gapToday
	department := &gen.StaffDepartmentSummary{Id: 5, Code: "OPS", Name: "Operations"}
	profiles := []effectiveProfile{
		{Profile: store.StaffCompetenceProfile{ID: 1, Key: "electrician", NameEn: "Electrician", NameNb: "Elektriker"}, Assigned: true},
		{Profile: store.StaffCompetenceProfile{ID: 2, Key: "old", ArchivedAt: &archivedAt}, Assigned: true},
		{Profile: store.StaffCompetenceProfile{ID: 3, Key: "operator"}, Department: department},
	}
	item := func(profile, competence int64, kind string, minLevel *int16, archived bool) store.ListCompetenceProfileItemsRow {
		it := gapItem(kind, minLevel, true, gapLevel(30))
		it.StaffCompetenceProfileItem.ProfileID, it.StaffCompetenceProfileItem.CompetenceID = profile, competence
		if archived {
			it.CompetenceArchivedAt = &archivedAt
		}
		return it
	}
	items := map[int64][]store.ListCompetenceProfileItemsRow{
		1: {item(1, 10, "certificate", nil, false), item(1, 11, "skill", gapLevel(3), false), item(1, 12, "certificate", nil, true)},
		2: {item(2, 13, "certificate", nil, false)},
		3: {item(3, 11, "skill", gapLevel(2), false)},
	}
	fse := gapRow(nil, true, -300, gapDays(10))
	golang := gapRow(gapLevel(2), false, -30, nil)
	golang.CompetenceID = 11
	rows := map[int64]store.ListCurrentCompetenceRowsOfRow{10: *fse, 11: *golang}

	got := gapsOf(profiles, items, rows, gapToday)
	if len(got.Profiles) != 2 || got.Profiles[0].Profile.Key != "electrician" || got.Profiles[1].Profile.Key != "operator" {
		t.Fatalf("profiles = %+v, want electrician and operator, not the archived one", got.Profiles)
	}
	electrician, operator := got.Profiles[0], got.Profiles[1]
	if !electrician.Assigned || electrician.Department != nil || operator.Assigned || operator.Department != department {
		t.Errorf("how each reaches the person = %+v / %+v", electrician, operator)
	}
	if electrician.Profile.Names.Nb != "Elektriker" || electrician.Profile.Archived {
		t.Errorf("the summary = %+v", electrician.Profile)
	}
	want := []struct {
		status   gen.StaffCompetenceGapStatus
		declared bool
		archived bool
		verified bool
	}{
		{gen.GapExpiring, false, false, true},
		{gen.GapBelowLevel, true, false, false},
		{gen.GapMissing, false, true, false},
	}
	if len(electrician.Items) != len(want) {
		t.Fatalf("items = %+v", electrician.Items)
	}
	for i, w := range want {
		it := electrician.Items[i]
		if it.Status != w.status || it.Declared != w.declared || it.Archived != w.archived || it.Verified != w.verified || !it.MustBeValid {
			t.Errorf("item %d = %+v, want %+v", i, it, w)
		}
	}
	if it := electrician.Items[0]; it.ValidTo == nil || !it.ValidTo.Equal(gapToday.AddDate(0, 0, 10)) || it.Level != nil {
		t.Errorf("the FSE item carries %+v, want its end and no level", it)
	}
	if it := electrician.Items[1]; it.Level == nil || *it.Level != 2 || it.MinLevel == nil || *it.MinLevel != 3 || it.ValidTo != nil ||
		it.Competence.Id != 11 || !it.Competence.HasLevel {
		t.Errorf("the skill item carries %+v, want level 2 against 3", it)
	}
	if it := electrician.Items[2]; it.Level != nil || it.ValidTo != nil || !it.Competence.Archived {
		t.Errorf("the archived item carries %+v", it)
	}
	if electrician.NotMet != 2 || operator.NotMet != 0 || got.NotMet != 2 {
		t.Errorf("not met = %d, %d, %d in all; want 2, 0, 2", electrician.NotMet, operator.NotMet, got.NotMet)
	}
	if operator.Items[0].Status != gen.GapMet || !operator.Items[0].Declared {
		t.Errorf("the operator's item = %+v, want met, declared", operator.Items[0])
	}

	empty := gapsOf(nil, nil, nil, gapToday)
	if empty.Profiles == nil || len(empty.Profiles) != 0 || empty.NotMet != 0 {
		t.Errorf("no profile = %+v, want an empty list", empty)
	}
}
