package reminderrules

import "testing"

// I5: a letter's deadline is met when the live payments ordered on or before
// it — ordered_on where the bank line has one, else paid_on — cover the
// principal open on its sending.
func TestReminderRules_DeadlineMet(t *testing.T) {
	t.Parallel()
	letter := sentLetter(1, 1, LevelReminder, "2026-07-01", 14, FeeReminder) // deadline 15 Jul
	for _, c := range []struct {
		name     string
		payments []Payment
		credits  []Credit
		met      bool
	}{
		{"by ordered_on, booked three days later", []Payment{{PaidOn: day("2026-07-18"), OrderedOn: dayp("2026-07-15"), Amount: rat("10000")}}, nil, true},
		{"by paid_on without an order date", []Payment{{PaidOn: day("2026-07-15"), Amount: rat("10000")}}, nil, true},
		{"booked after the deadline, no order date", []Payment{{PaidOn: day("2026-07-18"), Amount: rat("10000")}}, nil, false},
		{"ordered the day after", []Payment{{PaidOn: day("2026-07-18"), OrderedOn: dayp("2026-07-16"), Amount: rat("10000")}}, nil, false},
		{"short of the principal", []Payment{
			{PaidOn: day("2026-07-10"), Amount: rat("6000")},
			{PaidOn: day("2026-07-18"), OrderedOn: dayp("2026-07-14"), Amount: rat("3999.99")},
		}, nil, false},
		{"a part paid before the letter, the rest by the deadline", []Payment{
			{PaidOn: day("2026-06-20"), Amount: rat("4000")},
			{PaidOn: day("2026-07-15"), Amount: rat("6000")},
		}, nil, true},
		{"a credit note within the deadline and the rest paid", []Payment{{PaidOn: day("2026-07-12"), Amount: rat("6000")}},
			[]Credit{{IssueDate: day("2026-07-05"), Gross: rat("4000")}}, true},
		{"nothing paid", nil, nil, false},
	} {
		in := baseInput("2026-07-25")
		in.Payments, in.Credits = c.payments, c.credits
		if got := DeadlineMet(in, letter); got != c.met {
			t.Errorf("%s: DeadlineMet = %v, want %v", c.name, got, c.met)
		}
	}
	in := baseInput("2026-07-25")
	in.Payments = []Payment{{PaidOn: day("2026-07-02"), Amount: rat("10000")}}
	if DeadlineMet(in, Letter{ID: 2, Status: StatusQueued}) {
		t.Error("a letter without a deadline has none to meet")
	}
}
