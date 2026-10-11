package staff

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

func states(items []contractItem) map[string]string {
	out := map[string]string{}
	for _, i := range items {
		out[i.Item] = i.State
	}
	return out
}

// The seventeen items of aml. § 14-6 (1), each present, missing or
// not_applicable by the plan's table: a permanent employee without a
// probation, a special arrangement, a tariff or a hirer has e, f, l, n and o
// not applicable; a bare row is missing everything else but the parties and
// the start; a full row has everything; a temporary or hired-out employment
// makes its items apply.
func TestContractCheck_PerItem(t *testing.T) {
	t.Parallel()
	text := func(s string) *string { return &s }
	small := func(v int16) *int16 { return &v }
	start := pgDay(t, "2026-01-01")
	employment := store.StaffEmployment{ID: 1, PersonID: 2, BusinessUnitID: 3, StartDate: start}
	bare := store.StaffEmploymentTerm{EmploymentType: "fast", AppointmentForm: "fast", WorkingHoursScheme: "not_shift"}
	full := store.StaffEmploymentTerm{
		EmploymentType: "fast", AppointmentForm: "fast", WorkingHoursScheme: "shift_365",
		LocationID: id(5), Title: text("Developer"), ProbationEndDate: pgDay(t, "2026-06-30"),
		HolidayTermsText: text("Ferieloven"), NoticeEmployee: small(1), NoticeEmployer: small(1), NoticeUnit: text("months"),
		PayComponents: []byte(`[{"name":"Base","amount":50000,"currency":"NOK","period":"monthly"}]`),
		PayMethod:     text("Bank transfer"), PaydayText: text("The 20th"), WorkingTimeText: text("08–16"),
		BreakMinutes: small(30), SpecialHoursText: text("Rotating shifts"), OvertimeRuleText: text("Per aml. § 10-6"),
		TariffReference: text("HK"), CompetenceDevelopmentText: text("Courses"), SocialSecurityText: text("OTP"),
	}
	all := "abcdefghijklmnopq"
	want := func(present, missing, na string) map[string]string {
		out := map[string]string{}
		for _, set := range []struct{ items, state string }{{present, "present"}, {missing, "missing"}, {na, "not_applicable"}} {
			for _, r := range set.items {
				out[string(r)] = set.state
			}
		}
		if len(out) != len(all) {
			t.Fatalf("the expectation names %d items, want 17", len(out))
		}
		return out
	}
	temporary := bare
	temporary.EmploymentType, temporary.AppointmentForm = "midlertidig", "midlertidig"
	temporary.TemporaryGround = text("a")
	hiredOut := bare
	hiredOut.AppointmentForm = "fast_utleid"
	pension := text("Storebrand")

	for _, c := range []struct {
		name    string
		terms   *store.StaffEmploymentTerm
		pension *string
		want    map[string]string
	}{
		{"a bare permanent row", &bare, nil, want("ad", "bcghijkmpq", "eflno")},
		{"a full row", &full, nil, want("abcdfghijklmnpq", "", "eo")},
		{"no terms at all", nil, nil, want("ad", "bcghijkmpq", "eflno")},
		{"the settings' pension provider answers q", &bare, pension, want("adq", "bcghijkmp", "eflno")},
		{"temporary without an expected end", &temporary, nil, want("ad", "bceghijkmpq", "flno")},
		{"hired out without a tariff or a hirer", &hiredOut, nil, want("ad", "bcghijkmnopq", "efl")},
	} {
		got := states(contractCheck(employment, c.terms, c.pension))
		for _, item := range all {
			if got[string(item)] != c.want[string(item)] {
				t.Errorf("%s: item %c = %q, want %q", c.name, item, got[string(item)], c.want[string(item)])
			}
		}
	}
	for _, i := range contractCheck(employment, &full, nil) {
		if i.Source == "" {
			t.Errorf("item %s names no source", i.Item)
		}
	}
	partial := full
	partial.PaydayText = nil
	if got := states(contractCheck(employment, &partial, nil))["i"]; got != "missing" {
		t.Errorf("pay without a payday: i = %s, want missing (all three are needed)", got)
	}
}
