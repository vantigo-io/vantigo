package invoices_test

import (
	"slices"
	"testing"
	"time"
	_ "time/tzdata" // America/New_York wherever the tests run
)

// Every day of this module is a day of the workspace's business time zone
// (time zones design D2, D7), read through the business calendar on the
// request that judges it: under America/New_York, an issue at 02:00 UTC on
// 1 October — 22:00 on 30 September there — is dated 30 September and falls
// due 30 days on, the draft is offered 30 September alone, and the pay page
// shows the dates stored. The same installation moved to Europe/Oslo issues
// its next invoice on 1 October, and leaves the first one's dates as they
// were. At 02:00 UTC on 31 October the first invoice, due on Friday the 30th,
// is overdue in Oslo and not yet in New York, where it is still the 30th: the
// state and the reminder engine's overdue list both follow the zone.
func TestBusinessZone_IssueDueAndRemindersFollowTheWorkspace(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	h.SetBusinessTimeZone(t, "America/New_York")
	advanceTo(t, h, time.Date(2026, time.October, 1, 2, 0, 0, 0, time.UTC))

	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 10, 80, vat25)))
	if got := getInvoice(t, h, draft.ID).AllowedIssueDates; !slices.Equal(got, []string{"2026-09-30"}) {
		t.Errorf("allowed issue dates in New York = %v, want [2026-09-30]: it is still 30 September there", got)
	}
	first := issued(t, h, draft.ID)
	if first.IssueDate == nil || *first.IssueDate != "2026-09-30" || first.DueDate == nil || *first.DueDate != "2026-10-30" {
		t.Fatalf("issued in New York = %v due %v, want 2026-09-30 due 2026-10-30", first.IssueDate, first.DueDate)
	}
	page, _ := readPay(t, h.Client(t), payToken(t, h, first.ID))
	if page.IssueDate != "2026-09-30" || page.DueDate == nil || *page.DueDate != "2026-10-30" || !page.Payable {
		t.Errorf("the pay page = issued %s due %v payable %v, want 2026-09-30, 2026-10-30, payable", page.IssueDate, page.DueDate, page.Payable)
	}

	h.SetBusinessTimeZone(t, "Europe/Oslo")
	next := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 10, 80, vat25)))
	if got := getInvoice(t, h, next.ID).AllowedIssueDates; !slices.Equal(got, []string{"2026-09-30", "2026-10-01"}) {
		t.Errorf("allowed issue dates in Oslo = %v, want the last of September and 1 October", got)
	}
	second := issued(t, h, next.ID)
	if second.IssueDate == nil || *second.IssueDate != "2026-10-01" || second.DueDate == nil || *second.DueDate != "2026-10-31" {
		t.Errorf("issued in Oslo = %v due %v, want 2026-10-01 due 2026-10-31", second.IssueDate, second.DueDate)
	}
	if again := getInvoice(t, h, first.ID); again.IssueDate == nil || *again.IssueDate != "2026-09-30" {
		t.Errorf("the first invoice after the zone changed = %v, want 2026-09-30 still: a stored date never moves", again.IssueDate)
	}

	advanceTo(t, h, time.Date(2026, time.October, 31, 2, 0, 0, 0, time.UTC))
	c := reader(t, h)
	h.SetBusinessTimeZone(t, "America/New_York")
	if got := getInvoice(t, h, first.ID).State; got != "open" {
		t.Errorf("in New York on 30 October the state = %s, want open", got)
	}
	if got := overdueOf(t, c, ""); len(got.Items) != 0 {
		t.Errorf("in New York on 30 October the overdue list = %v, want none", overdueIDs(got))
	}
	h.SetBusinessTimeZone(t, "Europe/Oslo")
	if got := getInvoice(t, h, first.ID).State; got != "overdue" {
		t.Errorf("in Oslo on 31 October the state = %s, want overdue", got)
	}
	got := overdueOf(t, c, "")
	if !slices.Equal(overdueIDs(got), []int64{first.ID}) || got.Items[0].DaysOverdue != 1 {
		t.Errorf("in Oslo on 31 October the overdue list = %v, want the first invoice, a day overdue", got.Items)
	}
}
