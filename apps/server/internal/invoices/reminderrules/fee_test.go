package reminderrules

import (
	"slices"
	"testing"
	"time"
)

// R8: 1/20 of the inkassosats, rounded to the nearest krone, .50 up (INKF
// § 1-2 fourth paragraph; Finanstilsynet confirms 750 → 38).
func TestReminderRules_FeeRounding(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ sats, want string }{
		{"700", "35"},
		{"750", "38"}, // 37.50
		{"725", "36"}, // 36.25
		{"770", "39"}, // 38.50
		{"710", "36"}, // 35.50
		{"749", "37"}, // 37.45
	} {
		if got := ReminderFee(rat(c.sats)); got.Cmp(rat(c.want)) != 0 {
			t.Errorf("ReminderFee(%s) = %s, want %s", c.sats, got.FloatString(2), c.want)
		}
	}
}

// feeLetters is the sent fee-bearing letters on the given days, oldest first.
func feeLetters(days ...string) []Letter {
	letters := make([]Letter, 0, len(days))
	for i, d := range days {
		letters = append(letters, sentLetter(int64(i+1), i+1, LevelReminder, d, 14, FeeReminder))
	}
	return letters
}

// D8's reset table, every row with its years, pinned as the count (reading
// 49): the anniversary itself is still inside the six months (`>`), the
// months are added clamped, and the chain stops at the first gap of more than
// six months.
func TestFeeChainCount_TheResetTable(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		letters []Letter
		l       string
		want    int
	}{
		{feeLetters("2026-01-01", "2026-02-01"), "2026-07-02", 2},
		{feeLetters("2026-01-01", "2026-02-01"), "2026-08-01", 2}, // the anniversary is inside
		{feeLetters("2026-01-01", "2026-02-01"), "2026-08-02", 0},
		{feeLetters("2026-08-31"), "2027-02-28", 1},
		{feeLetters("2026-08-15", "2026-08-31"), "2027-03-01", 0}, // 31 Aug + 6 clamps to 28 Feb
		{feeLetters("2026-08-31", "2026-09-15"), "2027-03-15", 2},
		{feeLetters("2026-08-31", "2026-09-15"), "2027-03-16", 0},
		{feeLetters("2026-01-01", "2026-07-02", "2026-07-15"), "2026-08-01", 2}, // 2 Jul → 1 Jan breaks the chain
		{feeLetters("2026-01-01", "2026-07-02"), "2026-07-03", 1},
		{feeLetters("2026-01-01", "2026-07-01"), "2026-07-02", 2}, // exactly six months does not break it
		// The letters in any order; fee-free, withdrawn and unsent letters are
		// not fee letters.
		{reversed(feeLetters("2026-08-31", "2026-09-15")), "2027-03-15", 2},
		{append(feeLetters("2026-01-01", "2026-02-01"), sentLetter(3, 3, LevelReminder, "2026-03-01", 14, FeeNone)), "2026-08-02", 0},
		{append(feeLetters("2026-01-01"), withStatus(sentLetter(2, 2, LevelReminder, "2026-02-01", 14, FeeReminder), StatusWithdrawn)), "2026-07-02", 0},
		{append(feeLetters("2026-01-01"), withStatus(sentLetter(2, 2, LevelReminder, "2026-02-01", 14, FeeReminder), StatusPrinted)), "2026-06-30", 1},
		{nil, "2026-07-02", 0},
	} {
		if got := feeChainCount(c.letters, day(c.l)); got != c.want {
			t.Errorf("fee letters %v at %s: count %d, want %d", sentDays(c.letters), c.l, got, c.want)
		}
	}
}

// The table's rows where R10 can hold — the previous fee letter's 14-day
// deadline passed by L — through Next: the fee allowed, or refused
// fee_cap_reached. A waived fee still counts: it was claimed.
func TestReminderRules_TheResetTableThroughNext(t *testing.T) {
	t.Parallel()
	waivedFeb := []Waiver{{ID: 1, ReminderID: 2, Kind: WaiverFee, Amount: rat("38")}}
	for _, c := range []struct {
		name    string
		letters []Letter
		waivers []Waiver
		l       string
		allowed bool
	}{
		{"1 Jan, 1 Feb at 2 Jul", feeLetters("2026-01-01", "2026-02-01"), nil, "2026-07-02", false},
		{"1 Jan, 1 Feb at 1 Aug", feeLetters("2026-01-01", "2026-02-01"), nil, "2026-08-01", false},
		{"1 Jan, 1 Feb at 2 Aug", feeLetters("2026-01-01", "2026-02-01"), nil, "2026-08-02", true},
		{"31 Aug at 28 Feb", feeLetters("2026-08-31"), nil, "2027-02-28", true},
		{"15 Aug, 31 Aug at 1 Mar", feeLetters("2026-08-15", "2026-08-31"), nil, "2027-03-01", true},
		{"31 Aug, 15 Sep at 15 Mar", feeLetters("2026-08-31", "2026-09-15"), nil, "2027-03-15", false},
		{"31 Aug, 15 Sep at 16 Mar", feeLetters("2026-08-31", "2026-09-15"), nil, "2027-03-16", true},
		{"1 Jan, 2 Jul, 15 Jul at 1 Aug", feeLetters("2026-01-01", "2026-07-02", "2026-07-15"), nil, "2026-08-01", false},
		{"1 Jan, 1 Feb (waived) at 1 Aug", feeLetters("2026-01-01", "2026-02-01"), waivedFeb, "2026-08-01", false},
	} {
		in := baseInput(c.l)
		in.Invoice.DueDate = day("2025-12-01")
		in.Deliveries = []time.Time{day("2025-11-20")}
		in.Settings.RemindersBeforeNotice = 2
		in.Settings.GraceDays = 1
		in.Letters, in.Waivers = c.letters, c.waivers
		out := Next(in)
		if out.Letter == nil {
			t.Errorf("%s: %s %v, want a letter", c.name, out.Action, out.Reasons)
			continue
		}
		gotFee := out.Letter.FeeKind == FeeReminder && out.Letter.Fee.Cmp(rat("38")) == 0
		capped := slices.Contains(out.ChargeNotes, NoteFeeCapReached)
		if gotFee != c.allowed || capped == c.allowed {
			t.Errorf("%s: fee %s %s, notes %v; want allowed=%v", c.name, out.Letter.FeeKind, out.Letter.Fee.FloatString(2), out.ChargeNotes, c.allowed)
		}
	}
}

// R7: no fee before the effective due date + 14 days — the letter goes, fee
// free, noted fee_before_14_days; a Saturday due date counts from the Monday.
func TestReminderRules_FourteenDays(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, due, l string
		fee          bool
	}{
		{"E + 13", "2026-06-15", "2026-06-28", false},
		{"E + 14", "2026-06-15", "2026-06-29", true},
		{"a Saturday due date + 14", "2026-06-13", "2026-06-27", false},
		{"a Saturday due date + 15, the Monday's + 13", "2026-06-13", "2026-06-28", false},
		{"the Monday after a Saturday + 14", "2026-06-13", "2026-06-29", true},
	} {
		in := baseInput(c.l)
		in.Invoice.DueDate = day(c.due)
		in.Settings.FirstReminderDays = 1
		out := Next(in)
		if out.Letter == nil {
			t.Fatalf("%s: %s %v, want a letter", c.name, out.Action, out.Reasons)
		}
		noted := slices.Contains(out.ChargeNotes, NoteFeeBefore14Days)
		if (out.Letter.FeeKind == FeeReminder) != c.fee || noted == c.fee {
			t.Errorf("%s: fee %s, notes %v; want fee=%v", c.name, out.Letter.FeeKind, out.ChargeNotes, c.fee)
		}
		if c.fee && out.Letter.Fee.Cmp(rat("38")) != 0 {
			t.Errorf("%s: fee %s, want 38", c.name, out.Letter.Fee.FloatString(2))
		}
	}
}

// R10: a second fee only when the previous fee letter's deadline was at least
// 14 days after its sending, has passed, and was missed.
func TestReminderRules_SecondFeeNeedsAMissedDeadline(t *testing.T) {
	t.Parallel()
	first := sentLetter(1, 1, LevelReminder, "2026-07-01", 14, FeeReminder) // deadline 15 Jul
	paidLate := Payment{PaidOn: day("2026-07-18"), OrderedOn: dayp("2026-07-15"), Amount: rat("10000")}
	for _, c := range []struct {
		name     string
		letter   Letter
		payments []Payment
		l        string
		action   Action
		fee      bool
	}{
		{"missed", first, nil, "2026-07-20", ActionReminder, true},
		{"met, the payment not yet booked", first, nil, "2026-07-19", ActionReminder, true},
		{"met by a payment ordered on the deadline, booked three days later", first, []Payment{paidLate}, "2026-07-20", ActionNone, false},
		{"a deadline of 13 days", sentLetter(1, 1, LevelReminder, "2026-07-01", 13, FeeReminder), nil, "2026-07-20", ActionReminder, false},
	} {
		in := baseInput(c.l)
		in.Settings.RemindersBeforeNotice = 2
		in.Letters, in.Payments = []Letter{c.letter}, c.payments
		out := Next(in)
		if out.Action != c.action {
			t.Errorf("%s: %s %v, want %s", c.name, out.Action, out.Reasons, c.action)
			continue
		}
		if c.action == ActionNone {
			if out.Letter != nil {
				t.Errorf("%s: a letter %+v, want none", c.name, *out.Letter)
			}
			if !DeadlineMet(in, c.letter) {
				t.Errorf("%s: the deadline is not met", c.name)
			}
			continue
		}
		if out.Letter == nil || (out.Letter.FeeKind == FeeReminder) != c.fee {
			t.Errorf("%s: letter %+v, want fee=%v", c.name, out.Letter, c.fee)
		}
	}
}
