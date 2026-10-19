package calendar

import (
	"strings"
	"testing"
	"time"
)

func TestParseRule_EveryRuleInTheSubsetRoundTripsToItsCanonicalForm(t *testing.T) {
	t.Parallel()
	oslo, _ := time.LoadLocation("Europe/Oslo")
	for _, tc := range []struct{ in, want string }{
		{"FREQ=DAILY", "FREQ=DAILY;INTERVAL=1"},
		{"freq=daily;interval=3;count=10", "FREQ=DAILY;INTERVAL=3;COUNT=10"},
		{"RRULE:FREQ=WEEKLY", "FREQ=WEEKLY;INTERVAL=1"},
		{"FREQ=WEEKLY;BYDAY=WE,MO;COUNT=10;INTERVAL=1", "FREQ=WEEKLY;INTERVAL=1;BYDAY=MO,WE;COUNT=10"},
		{"FREQ=WEEKLY;INTERVAL=2;BYDAY=SU,FR,TU;UNTIL=20271231", "FREQ=WEEKLY;INTERVAL=2;BYDAY=TU,FR,SU;UNTIL=20271231"},
		{"FREQ=WEEKLY;UNTIL=20271231T225959Z", "FREQ=WEEKLY;INTERVAL=1;UNTIL=20271231"},
		{"FREQ=WEEKLY;UNTIL=20271231T230000Z", "FREQ=WEEKLY;INTERVAL=1;UNTIL=20280101"},
		{"FREQ=MONTHLY", "FREQ=MONTHLY;INTERVAL=1"},
		{"FREQ=MONTHLY;BYDAY=+2TU", "FREQ=MONTHLY;INTERVAL=1;BYDAY=2TU"},
		{"FREQ=MONTHLY;BYDAY=-1FR;COUNT=12", "FREQ=MONTHLY;INTERVAL=1;BYDAY=-1FR;COUNT=12"},
		{"FREQ=MONTHLY;INTERVAL=99;BYMONTHDAY=15", "FREQ=MONTHLY;INTERVAL=99;BYMONTHDAY=15"},
		{"FREQ=MONTHLY;BYMONTHDAY=-1;COUNT=730", "FREQ=MONTHLY;INTERVAL=1;BYMONTHDAY=-1;COUNT=730"},
	} {
		r, msg := parseRule(tc.in, oslo)
		if msg != "" {
			t.Errorf("parseRule(%q) refused: %s", tc.in, msg)
			continue
		}
		if got := r.String(); got != tc.want {
			t.Errorf("parseRule(%q) = %q, want %q", tc.in, got, tc.want)
		}
		again, msg := parseRule(r.String(), oslo)
		if msg != "" || again.String() != r.String() {
			t.Errorf("the canonical %q does not round-trip: %q %s", r.String(), again.String(), msg)
		}
	}
}

func TestParseRule_RefusesWhatIsOutsideTheSubset(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, in, mention string }{
		{"yearly", "FREQ=YEARLY", "FREQ"},
		{"hourly", "FREQ=HOURLY", "FREQ"},
		{"BYSETPOS", "FREQ=MONTHLY;BYDAY=MO,TU;BYSETPOS=-1", "BYSETPOS"},
		{"a second BYDAY list", "FREQ=WEEKLY;BYDAY=MO;BYDAY=WE", "BYDAY"},
		{"WKST", "FREQ=WEEKLY;WKST=SU", "WKST"},
		{"BYHOUR", "FREQ=DAILY;BYHOUR=9", "BYHOUR"},
		{"BYMONTH", "FREQ=MONTHLY;BYMONTH=3", "BYMONTH"},
		{"COUNT and UNTIL", "FREQ=DAILY;COUNT=3;UNTIL=20271231", "COUNT"},
		{"COUNT over 730", "FREQ=DAILY;COUNT=731", "COUNT"},
		{"COUNT zero", "FREQ=DAILY;COUNT=0", "COUNT"},
		{"INTERVAL 100", "FREQ=DAILY;INTERVAL=100", "INTERVAL"},
		{"INTERVAL 0", "FREQ=DAILY;INTERVAL=0", "INTERVAL"},
		{"BYDAY on a daily rule", "FREQ=DAILY;BYDAY=MO", "BYDAY"},
		{"an ordinal on a weekly rule", "FREQ=WEEKLY;BYDAY=2MO", "BYDAY"},
		{"a weekday twice", "FREQ=WEEKLY;BYDAY=MO,MO", "BYDAY"},
		{"two ordinals on a monthly rule", "FREQ=MONTHLY;BYDAY=1MO,3MO", "BYDAY"},
		{"a plain weekday on a monthly rule", "FREQ=MONTHLY;BYDAY=MO", "BYDAY"},
		{"two month days", "FREQ=MONTHLY;BYMONTHDAY=1,15", "BYMONTHDAY"},
		{"BYMONTHDAY on a weekly rule", "FREQ=WEEKLY;BYMONTHDAY=1", "BYMONTHDAY"},
		{"BYDAY and BYMONTHDAY", "FREQ=MONTHLY;BYDAY=2TU;BYMONTHDAY=1", "BYDAY"},
		{"DTSTART", "DTSTART:20261102T090000Z\nRRULE:FREQ=DAILY", "DTSTART"},
		{"empty", "  ", "required"},
		{"garbage", "FREQ", "rule"},
	} {
		if _, msg := parseRule(tc.in, time.UTC); msg == "" || !strings.Contains(msg, tc.mention) {
			t.Errorf("%s: parseRule(%q) = %q, want a refusal mentioning %q", tc.name, tc.in, msg, tc.mention)
		}
	}
}

func TestSeries_LastDateIsRightForCountUntilAndNone(t *testing.T) {
	t.Parallel()
	oslo, _ := time.LoadLocation("Europe/Oslo")
	start := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC) // Monday 09:00 in Oslo
	s := span{startsAt: start, endsAt: start.Add(time.Hour)}
	for _, tc := range []struct {
		rule    string
		want    string
		bounded bool
	}{
		{"FREQ=WEEKLY;BYDAY=MO,WE;COUNT=4", "2026-11-11", true},
		{"FREQ=DAILY;INTERVAL=2;COUNT=3", "2026-11-06", true},
		{"FREQ=WEEKLY;UNTIL=20261130", "2026-11-30", true},
		{"FREQ=WEEKLY;UNTIL=20261129", "2026-11-23", true},
		{"FREQ=MONTHLY;BYDAY=-1FR;COUNT=2", "2026-12-25", true},
		{"FREQ=WEEKLY", "", false},
	} {
		r, msg := parseRule(tc.rule, oslo)
		if msg != "" {
			t.Fatalf("%s: %s", tc.rule, msg)
		}
		if r.bounded() != tc.bounded {
			t.Errorf("%s: bounded = %v", tc.rule, r.bounded())
		}
		if !tc.bounded {
			continue
		}
		sr, err := newSeries(r, s, oslo)
		if err != nil {
			t.Fatal(err)
		}
		last, ok := sr.lastDate()
		if !ok || last.Format(time.DateOnly) != tc.want {
			t.Errorf("%s: last date %s %v, want %s", tc.rule, last.Format(time.DateOnly), ok, tc.want)
		}
	}
}

func TestSeries_AWeeklyOsloSeriesKeepsItsWallClockAcrossTheOctoberClockChange(t *testing.T) {
	t.Parallel()
	oslo, _ := time.LoadLocation("Europe/Oslo")
	start := time.Date(2026, 10, 19, 7, 0, 0, 0, time.UTC) // Monday 09:00 CEST
	r, _ := parseRule("FREQ=WEEKLY", oslo)
	sr, err := newSeries(r, span{startsAt: start, endsAt: start.Add(30 * time.Minute)}, oslo)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range sr.on(time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) {
		o := sr.occurrence(f)
		got = append(got, o.startsAt.Format(time.RFC3339)+" "+o.startsAt.In(oslo).Format("15:04"))
	}
	want := []string{"2026-10-19T07:00:00Z 09:00", "2026-10-26T08:00:00Z 09:00", "2026-11-02T08:00:00Z 09:00"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("occurrences = %v, want %v", got, want)
	}
	if !sr.produces(time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)) || sr.produces(time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)) {
		t.Error("produces disagrees with the expansion")
	}
}
