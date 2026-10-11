package staff

import (
	"maps"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// The hours for a full position per arbeidstidsordning (plan, Exact values):
// each scheme's default, and the a-melding's rule — exactly 36.5 for
// shift_365, exactly 35.5 for round_the_clock_355, 28 to 33.6 for the two
// 336 schemes, free for not_shift and other_shift.
func TestFullTimeHours_PerScheme(t *testing.T) {
	t.Parallel()
	defaults := map[string]int64{
		"not_shift": 3750, "shift_365": 3650, "offshore_336": 3360,
		"continuous_336": 3360, "round_the_clock_355": 3550, "other_shift": 3750,
	}
	for scheme, want := range defaults {
		if got := defaultFullTimeHours(scheme); got != want {
			t.Errorf("default for %s = %d, want %d", scheme, got, want)
		}
		if fullTimeHoursMismatch(scheme, want) {
			t.Errorf("%s refuses its own default %d", scheme, want)
		}
	}
	for _, c := range []struct {
		scheme   string
		hours    int64
		mismatch bool
	}{
		{"not_shift", 4000, false}, {"not_shift", 3000, false},
		{"shift_365", 3750, true}, {"shift_365", 3649, true},
		{"offshore_336", 2800, false}, {"offshore_336", 2799, true}, {"offshore_336", 3361, true},
		{"continuous_336", 3000, false}, {"continuous_336", 3700, true},
		{"round_the_clock_355", 3540, true}, {"round_the_clock_355", 3550, false},
		{"other_shift", 2000, false}, {"other_shift", 4500, false},
	} {
		if got := fullTimeHoursMismatch(c.scheme, c.hours); got != c.mismatch {
			t.Errorf("%s with %d hundredths: mismatch %v, want %v", c.scheme, c.hours, got, c.mismatch)
		}
	}
}

// The six schemes and the six end reasons are pinned to Skatteetaten's codes,
// which the payroll export will report.
func TestSkatteetatenCodes_Pinned(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		got, want map[string]string
	}{
		"working hours schemes": {workingHoursSchemeCodes, map[string]string{
			"not_shift":           "ikkeSkift",
			"shift_365":           "skift365",
			"offshore_336":        "offshore336",
			"continuous_336":      "helkontinuerligSkiftOgAndreOrdninger336",
			"round_the_clock_355": "doegnkontinuerligSkiftOgTurnus355",
			"other_shift":         "andreSkift",
		}},
		"end reasons": {endReasonCodes, map[string]string{
			"employee_resigned":     "arbeidstakerHarSagtOppSelv",
			"employer_terminated":   "arbeidsgiverHarSagtOppArbeidstaker",
			"contract_ended":        "kontraktEngasjementEllerVikariatErUtloept",
			"wrongly_reported":      "arbeidsforholdetSkulleAldriVaertRapportert",
			"internal_change":       "endringIOrganisasjonsstrukturEllerByttetJobbInternt",
			"payroll_system_change": "byttetLoenssystemEllerRegnskapsfoerer",
		}},
	} {
		if !maps.Equal(c.got, c.want) {
			t.Errorf("%s = %v, want %v", name, c.got, c.want)
		}
	}
	// Every value the contract's enumerations accept has a code.
	for _, scheme := range workingHoursSchemes {
		if _, ok := workingHoursSchemeCodes[scheme]; !ok {
			t.Errorf("scheme %s has no code", scheme)
		}
	}
	for _, reason := range endReasons {
		if _, ok := endReasonCodes[reason]; !ok {
			t.Errorf("end reason %s has no code", reason)
		}
	}
	if len(workingHoursSchemes) != 6 || len(endReasons) != 6 {
		t.Errorf("schemes %v and reasons %v, want six of each", workingHoursSchemes, endReasons)
	}
}

// An appointment form agrees with the employment type: fast and fast_utleid
// with fast, the other three with midlertidig.
func TestAppointmentFormAgrees(t *testing.T) {
	t.Parallel()
	agree := map[string][]string{
		"fast":        {"fast", "fast_utleid"},
		"midlertidig": {"midlertidig", "midlertidig_utleid", "midlertidig_tilkallingsvikar"},
	}
	if len(employmentTypes) != 2 || len(appointmentForms) != 5 {
		t.Fatalf("types %v and forms %v, want two and five", employmentTypes, appointmentForms)
	}
	for _, typ := range employmentTypes {
		for _, form := range appointmentForms {
			if got, want := appointmentFormAgrees(typ, form), slices.Contains(agree[typ], form); got != want {
				t.Errorf("%s with %s: agrees %v, want %v", typ, form, got, want)
			}
		}
	}
}

// The probation (aml. § 15-6): at most six months after the start — the same
// day six months on, or the month's last day when it has no such day — and
// for a temporary employment at most half the expected term; an extension
// needs the agreement, a reason and a probation to extend, lies after it, and
// is no longer than the probation itself, since the absence it answers for
// fell inside it.
func TestProbationTooLong(t *testing.T) {
	t.Parallel()
	date := func(s string) pgtype.Date { return pgDay(t, s) }
	reason := "Sick leave during the probation"
	for _, c := range []struct {
		name  string
		start string
		row   store.StaffEmploymentTerm
		field string
	}{
		{"no probation", "2026-01-15", store.StaffEmploymentTerm{}, ""},
		{"six months to the day", "2026-01-15", store.StaffEmploymentTerm{ProbationEndDate: date("2026-07-15")}, ""},
		{"a day more", "2026-01-15", store.StaffEmploymentTerm{ProbationEndDate: date("2026-07-16")}, "probationEndDate"},
		{"from the 31st to a short month", "2026-08-31", store.StaffEmploymentTerm{ProbationEndDate: date("2027-02-28")}, ""},
		{"past the short month's end", "2026-08-31", store.StaffEmploymentTerm{ProbationEndDate: date("2027-03-01")}, "probationEndDate"},
		{"temporary: half the term", "2026-01-15", store.StaffEmploymentTerm{
			EmploymentType: "midlertidig", ExpectedEndDate: date("2026-07-15"), ProbationEndDate: date("2026-04-15")}, ""},
		{"temporary: past half the term", "2026-01-15", store.StaffEmploymentTerm{
			EmploymentType: "midlertidig", ExpectedEndDate: date("2026-07-15"), ProbationEndDate: date("2026-04-16")}, "probationEndDate"},
		{"temporary without an expected end: six months", "2026-01-15", store.StaffEmploymentTerm{
			EmploymentType: "midlertidig", ProbationEndDate: date("2026-07-15")}, ""},
		{"an agreed extension with a reason", "2026-01-15", store.StaffEmploymentTerm{
			ProbationEndDate: date("2026-07-15"), ProbationExtensionAgreed: true, ProbationExtensionReason: &reason,
			ProbationExtendedTo: date("2026-09-01")}, ""},
		{"an extension not agreed", "2026-01-15", store.StaffEmploymentTerm{
			ProbationEndDate: date("2026-07-15"), ProbationExtensionReason: &reason, ProbationExtendedTo: date("2026-09-01")}, "probationExtendedTo"},
		{"an extension without a reason", "2026-01-15", store.StaffEmploymentTerm{
			ProbationEndDate: date("2026-07-15"), ProbationExtensionAgreed: true, ProbationExtendedTo: date("2026-09-01")}, "probationExtendedTo"},
		{"an extension of no probation", "2026-01-15", store.StaffEmploymentTerm{
			ProbationExtensionAgreed: true, ProbationExtensionReason: &reason, ProbationExtendedTo: date("2026-09-01")}, "probationExtendedTo"},
		{"an extension before the probation's end", "2026-01-15", store.StaffEmploymentTerm{
			ProbationEndDate: date("2026-07-15"), ProbationExtensionAgreed: true, ProbationExtensionReason: &reason,
			ProbationExtendedTo: date("2026-07-15")}, "probationExtendedTo"},
		{"an extension longer than the probation", "2026-01-15", store.StaffEmploymentTerm{
			ProbationEndDate: date("2026-04-15"), ProbationExtensionAgreed: true, ProbationExtensionReason: &reason,
			ProbationExtendedTo: date("2026-07-15")}, "probationExtendedTo"},
	} {
		field, message := probationTooLong(day(t, c.start), c.row)
		if field != c.field || (field != "") != (message != "") {
			t.Errorf("%s: (%q, %q), want field %q", c.name, field, message, c.field)
		}
	}
}
