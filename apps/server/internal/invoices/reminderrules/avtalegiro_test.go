package reminderrules

import (
	"slices"
	"testing"
)

// Reading 13 (invoices phase 6 design I33): with an accepted AvtaleGiro
// claim the first letter waits until a business day after the due date —
// a debit due on a Friday is not reminded of on the Saturday, nor one due
// before a holiday on the holiday — and without a claim nothing changes.
func TestReminderRules_AvtaleGiroClaimWaitsABusinessDay(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, due, waitOn, letterOn string
	}{
		{"due on a Friday", "2026-06-12", "2026-06-13", "2026-06-15"},
		{"due before Ascension Day", "2026-05-13", "2026-05-14", "2026-05-15"},
	} {
		in := baseInput(c.waitOn)
		in.Invoice.DueDate = day(c.due)
		in.Invoice.IssueDate = day("2026-05-01")
		in.Deliveries = nil
		in.Settings.FirstReminderDays = 1
		in.AvtaleGiroDue = dayp(c.due)

		out := Next(in)
		if out.Action != ActionWaiting || !slices.Equal(out.Reasons, []string{ReasonAvtaleGiroClaim}) || !out.EarliestOn.Equal(day(c.letterOn)) {
			t.Errorf("%s on %s: %s %v earliest %s, want waiting avtalegiro_claim until %s", c.name, c.waitOn, out.Action, out.Reasons, out.EarliestOn.Format("2006-01-02"), c.letterOn)
		}
		in.L = day(c.letterOn)
		if out := Next(in); out.Action != ActionReminder {
			t.Errorf("%s on %s: %s %v, want a reminder", c.name, c.letterOn, out.Action, out.Reasons)
		}
		in.L, in.AvtaleGiroDue = day(c.waitOn), nil
		if out := Next(in); out.Action == ActionWaiting {
			t.Errorf("%s without a claim on %s: waiting, want the ordinary first letter", c.name, c.waitOn)
		}
	}
}
