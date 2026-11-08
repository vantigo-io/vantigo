package agents_test

import (
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/agents"
)

func oslo(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Skipf("no tz database: %v", err)
	}
	return loc
}

// TestLastOccurrence_IsTheLatestMomentAScheduleWokeInTheBusinessZone is V29's time
// zone edges: the weekday and the date are the business zone's, not UTC's, and a
// local time a clock jumps over or repeats still has one occurrence.
func TestLastOccurrence_IsTheLatestMomentAScheduleWokeInTheBusinessZone(t *testing.T) {
	t.Parallel()
	loc := oslo(t)
	utc := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tests := []struct {
		name string
		now  time.Time
		kind string
		day  int
		at   string
		want string // the occurrence, in UTC
	}{
		{"weekly, the minute before", utc("2026-11-09T06:59:00Z"), "weekly", 1, "08:00", "2026-11-02T07:00:00Z"},
		{"weekly, the minute after", utc("2026-11-09T07:01:00Z"), "weekly", 1, "08:00", "2026-11-09T07:00:00Z"},
		// 23:30 UTC on Sunday is 00:30 Monday in Oslo: Monday 00:15 has already passed there.
		{"weekly, the zone's weekday is not UTC's", utc("2026-11-08T23:30:00Z"), "weekly", 1, "00:15", "2026-11-08T23:15:00Z"},
		{"weekly, Sunday before the zone's Monday", utc("2026-11-08T22:30:00Z"), "weekly", 1, "00:15", "2026-11-01T23:15:00Z"},
		{"weekly, Sunday is 7", utc("2026-11-08T12:00:00Z"), "weekly", 7, "12:00", "2026-11-08T11:00:00Z"},
		// The clocks jump 02:00 to 03:00 on Sunday 2027-03-28: 02:30 does not exist, and the occurrence is 03:30 CEST.
		{"weekly, the hour the clocks skip", utc("2027-03-28T01:45:00Z"), "weekly", 7, "02:30", "2027-03-28T01:30:00Z"},
		{"weekly, the week before the skipped hour", utc("2027-03-28T01:15:00Z"), "weekly", 7, "02:30", "2027-03-21T01:30:00Z"},
		// The clocks fall back 03:00 to 02:00 on Sunday 2026-10-25: 02:30 happens twice and the later is the
		// occurrence, so the role wakes once, after the second pass.
		{"weekly, the hour the clocks repeat, after both", utc("2026-10-25T01:45:00Z"), "weekly", 7, "02:30", "2026-10-25T01:30:00Z"},
		{"weekly, the hour the clocks repeat, between", utc("2026-10-25T00:45:00Z"), "weekly", 7, "02:30", "2026-10-18T00:30:00Z"},
		{"monthly, before the day", utc("2026-11-14T10:00:00Z"), "monthly", 15, "09:00", "2026-10-15T07:00:00Z"},
		{"monthly, on the day", utc("2026-11-15T08:01:00Z"), "monthly", 15, "09:00", "2026-11-15T08:00:00Z"},
		{"monthly, across the new year", utc("2027-01-03T10:00:00Z"), "monthly", 28, "09:00", "2026-12-28T08:00:00Z"},
		{"monthly, the last day every month has", utc("2027-03-01T10:00:00Z"), "monthly", 28, "09:00", "2027-02-28T08:00:00Z"},
	}
	for _, tt := range tests {
		got, ok := agents.LastOccurrence(tt.now, loc, tt.kind, tt.day, tt.at)
		if !ok {
			t.Errorf("%s: no occurrence", tt.name)
			continue
		}
		if want := utc(tt.want); !got.Equal(want) {
			t.Errorf("%s: occurrence = %s, want %s", tt.name, got.UTC().Format(time.RFC3339), want.Format(time.RFC3339))
		}
		if got.After(tt.now) {
			t.Errorf("%s: the occurrence %s is after now", tt.name, got)
		}
	}
}

func TestLastOccurrence_RefusesWhatIsNotASchedule(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		kind, at string
		day      int
	}{
		{"weekly", "08:00", 0}, {"weekly", "08:00", 8}, {"monthly", "08:00", 29}, {"monthly", "08:00", 0},
		{"weekly", "24:00", 1}, {"weekly", "8", 1}, {"daily", "08:00", 1}, {"weekly", "08:60", 1},
	} {
		if _, ok := agents.LastOccurrence(time.Now(), time.UTC, tt.kind, tt.day, tt.at); ok {
			t.Errorf("%s day %d at %q was accepted", tt.kind, tt.day, tt.at)
		}
	}
}

// TestDecideChange is the mode of a change a template marked safe (V29): only S3
// applies, Auto makes it, Auto above X % makes it from the threshold.
func TestDecideChange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mode      string
		threshold int
		score     int
		send      bool
		held      []string
	}{
		{"review", 85, 99, false, nil},
		{"auto", 85, 0, true, nil},
		{"auto", 85, 40, true, nil},
		{"auto_above", 85, 85, true, nil},
		{"auto_above", 85, 84, false, nil},
		{"auto_above", 85, 0, false, []string{"S3"}},
	}
	for _, tt := range tests {
		send, held := agents.DecideChange(tt.mode, tt.threshold, tt.score)
		if send != tt.send || !slices.Equal(held, tt.held) {
			t.Errorf("%s at %d, score %d: send %v held %v, want %v %v", tt.mode, tt.threshold, tt.score, send, held, tt.send, tt.held)
		}
	}
}

// Only the front desk and the reminder replies mark an operation safe, and only
// the one whose written fields the run's tools pin to the status.
func TestSafeOperations_AreMarkedByTheTemplateAndNoOtherChangeIsSafe(t *testing.T) {
	t.Parallel()
	for _, key := range agents.TemplateKeys() {
		safe := agents.SafeOperations(key)
		switch key {
		case "front_desk", "reminder_replies":
			if !slices.Equal(safe, []string{"patchCommunicationsConversationsById"}) {
				t.Errorf("%s safe operations = %v, want the status change alone", key, safe)
			}
		default:
			if len(safe) != 0 {
				t.Errorf("%s marks %v safe, want none", key, safe)
			}
		}
	}
}
