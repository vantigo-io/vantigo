package expenses_test

import (
	"net/http"
	"strconv"
	"testing"
)

// This file is the dashboard's four reads (design §6's Dashboard row). The
// shapes are the host's, shared with every other module — the same from/to
// envelope on the summary, the same daily buckets, the same attention item —
// so the dashboard's own code is reused rather than special-cased per module.

// The attention types this module raises. The host builds its links from
// these strings and from entityId, so they are part of the contract.
const (
	attentionApprovalWaiting      = "approvalWaiting"
	attentionExpenseRejected      = "expenseRejected"
	attentionReimbursementWaiting = "reimbursementWaiting"
)

// marchPeriod is a period around the entry dates every fixture here uses; the
// harness clock is in September, so the default 30 days would hold none of
// them.
const marchPeriod = "?from=2026-03-01T00:00:00Z&to=2026-03-31T00:00:00Z"

// TestExpensesStats_AreTheCallersOwnKeyFigures is the strip the My expenses
// page shows: how many of the caller's own expenses stand in each status, what
// they are still owed, and how much is waiting for them to approve.
func TestExpensesStats_AreTheCallersOwnKeyFigures(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)

	createEntry(t, owner, outlayBody(nil))
	submitted := createEntry(t, owner, outlayBody(map[string]any{"description": "Skruer"}))
	approved := createEntry(t, owner, outlayBody(map[string]any{"description": "Stillas"}))
	rejected := createEntry(t, owner, outlayBody(map[string]any{"description": "Lunsj"}))
	submitEntries(t, owner, submitted.Id, approved.Id, rejected.Id)
	approveEntries(t, boss, approved.Id)
	rejectEntries(t, boss, "Mangler kvittering", rejected.Id)

	mine := getStats(t, owner)
	if mine.Draft != 1 || mine.Submitted != 1 || mine.Approved != 1 || mine.Rejected != 1 {
		t.Errorf("the owner's counts = %+v, want one of each status", mine)
	}
	if len(mine.Unreimbursed) != 1 || mine.Unreimbursed[0].Currency != "NOK" || mine.Unreimbursed[0].Amount != 1250 {
		t.Errorf("unreimbursed = %+v, want one NOK line of 1250", mine.Unreimbursed)
	}
	if mine.AwaitingMyApproval != 0 {
		t.Errorf("awaitingMyApproval = %d for somebody who approves nothing, want 0", mine.AwaitingMyApproval)
	}

	theirs := getStats(t, boss)
	if theirs.Draft != 0 || theirs.Approved != 0 || len(theirs.Unreimbursed) != 0 {
		t.Errorf("the approver's own counts = %+v, want nothing of their own", theirs)
	}
	if theirs.AwaitingMyApproval != 1 {
		t.Errorf("awaitingMyApproval = %d, want the one submitted expense", theirs.AwaitingMyApproval)
	}

	markReimbursed(t, boss, reimbursedBody([]int64{approved.Id}, nil))
	if paid := getStats(t, owner); len(paid.Unreimbursed) != 0 {
		t.Errorf("unreimbursed after the payout = %+v, want nothing", paid.Unreimbursed)
	}
}

// TestExpensesStatsSummary_IsTheDashboardCard: the host's envelope, with the
// one figure that has a delta and the two that cannot have one.
func TestExpensesStatsSummary_IsTheDashboardCard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)

	createEntry(t, owner, outlayBody(nil))
	waiting := createEntry(t, owner, outlayBody(map[string]any{"description": "Skruer"}))
	owed := createEntry(t, owner, outlayBody(map[string]any{"description": "Stillas", "currency": "EUR"}))
	submitEntries(t, owner, waiting.Id, owed.Id)
	approveEntries(t, boss, owed.Id)

	mine := getStatsSummary(t, owner, "")
	switch {
	case mine.From == "" || mine.To == "":
		t.Errorf("summary = %+v, want the period echoed", mine)
	case mine.MyDrafts != 1:
		t.Errorf("myDrafts = %d, want 1", mine.MyDrafts)
	case len(mine.MyUnreimbursed) != 1 || mine.MyUnreimbursed[0].Currency != "EUR" || mine.MyUnreimbursed[0].Amount != 1250:
		t.Errorf("myUnreimbursed = %+v, want one EUR line of 1250", mine.MyUnreimbursed)
	case mine.AwaitingMyApproval != 0:
		t.Errorf("awaitingMyApproval = %d, want 0", mine.AwaitingMyApproval)
	}

	theirs := getStatsSummary(t, boss, "")
	if theirs.AwaitingMyApproval != 1 || theirs.AwaitingMyApprovalDelta != 1 {
		t.Errorf("the approver's figures = %d (delta %d), want 1 and 1 — it was submitted inside the period",
			theirs.AwaitingMyApproval, theirs.AwaitingMyApprovalDelta)
	}
	// A period that began after the submission leaves the delta at zero: the
	// same expense was already waiting when it started.
	old := getStatsSummary(t, boss, "?from=2026-09-12T13:00:00Z&to=2026-09-20T00:00:00Z")
	if old.AwaitingMyApproval != 1 || old.AwaitingMyApprovalDelta != 0 {
		t.Errorf("over a later period = %d (delta %d), want 1 and 0",
			old.AwaitingMyApproval, old.AwaitingMyApprovalDelta)
	}

	r := boss.Do(http.MethodGet, statsSummaryPath+"?from=2026-09-20T00:00:00Z&to=2026-09-01T00:00:00Z", nil)
	if r.Status != http.StatusBadRequest {
		t.Errorf("a period that ends before it begins: status %d, want 400", r.Status)
	}
}

// TestExpensesStatsTimeseries_IsTheApprovedNetPerDay: the caller's own
// approved expenses, in the installation's own currency, one point per day
// that has something on it.
func TestExpensesStatsTimeseries_IsTheApprovedNetPerDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)

	first := createEntry(t, owner, outlayBody(map[string]any{"vatAmount": 250.00}))
	second := createEntry(t, owner, outlayBody(map[string]any{
		"entryDate": "2026-03-12", "description": "Skruer", "grossAmount": 100.00,
	}))
	euro := createEntry(t, owner, outlayBody(map[string]any{
		"entryDate": "2026-03-12", "description": "Ferge", "currency": "EUR", "grossAmount": 40.00,
	}))
	draft := createEntry(t, owner, outlayBody(map[string]any{"description": "Ikke sendt"}))
	approvedBy(t, owner, boss, first.Id, second.Id, euro.Id)

	buckets := getTimeseries(t, owner, marchPeriod+"&metric=netAmount")
	want := []bucketJSON{{Date: "2026-03-10", Value: 1000}, {Date: "2026-03-12", Value: 100}}
	if len(buckets) != len(want) {
		t.Fatalf("buckets = %+v, want %+v — draft %d and the EUR line %d are not in it",
			buckets, want, draft.Id, euro.Id)
	}
	for i, w := range want {
		if buckets[i] != w {
			t.Errorf("bucket %d = %+v, want %+v", i, buckets[i], w)
		}
	}

	// The metric is checked, and after the period, as every other module's is.
	for name, query := range map[string]string{
		"an unknown metric":  marchPeriod + "&metric=kroner",
		"no metric at all":   marchPeriod,
		"an upside-down one": "?from=2026-09-20T00:00:00Z&to=2026-09-01T00:00:00Z&metric=netAmount",
	} {
		t.Run(name, func(t *testing.T) {
			if r := owner.Do(http.MethodGet, statsTimeseriesPath+query, nil); r.Status != http.StatusBadRequest {
				t.Errorf("status %d body %s, want 400", r.Status, r.Body)
			}
		})
	}
}

// TestExpensesStatsAttention_IsWhatEachCallerHasToDo: the three types, each
// with its own recipients and its own entityId.
func TestExpensesStatsAttention_IsWhatEachCallerHasToDo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	anna, annaID := signIn(t, h)
	bjorn, bjornID := signIn(t, h)

	rejected := createEntry(t, anna, outlayBody(map[string]any{"description": "Lunsj med kunden"}))
	waiting := createEntry(t, anna, outlayBody(map[string]any{"description": "Skruer"}))
	owed := createEntry(t, anna, outlayBody(map[string]any{"description": "Stillas"}))
	trip := createEntry(t, bjorn, mileageBody(nil))
	submitEntries(t, anna, rejected.Id, waiting.Id, owed.Id)
	submitEntries(t, bjorn, trip.Id)
	rejectEntries(t, boss, "Mangler kvittering", rejected.Id)
	approveEntries(t, boss, owed.Id)

	// The owner is told about their own rejection and about nothing else.
	mine := getAttention(t, anna)
	if len(mine) != 1 {
		t.Fatalf("the owner's items = %+v, want the rejection alone", mine)
	}
	switch item := mine[0]; {
	case item.Type != attentionExpenseRejected:
		t.Errorf("type = %q, want %q", item.Type, attentionExpenseRejected)
	case item.EntityId != strconv.FormatInt(rejected.Id, 10):
		t.Errorf("entityId = %q, want the expense id %d", item.EntityId, rejected.Id)
	case item.Title != "Lunsj med kunden":
		t.Errorf("title = %q, want the expense's own description", item.Title)
	case item.OccurredAt == "":
		t.Error("occurredAt is empty, want when it was decided")
	}

	// The approver is told per person, and the payroll clerk once.
	theirs := getAttention(t, boss)
	approvals := attentionOfType(theirs, attentionApprovalWaiting)
	if len(approvals) != 2 {
		t.Fatalf("approvalWaiting items = %+v, want one per person", approvals)
	}
	seen := map[string]bool{}
	for _, item := range approvals {
		seen[item.EntityId] = true
		if item.Count == nil || *item.Count < 1 {
			t.Errorf("%+v carries no count", item)
		}
	}
	if !seen[annaID.String()] || !seen[bjornID.String()] {
		t.Errorf("approvalWaiting entityIds = %v, want the two owners' user ids", seen)
	}

	payroll := attentionOfType(theirs, attentionReimbursementWaiting)
	if len(payroll) != 1 {
		t.Fatalf("reimbursementWaiting items = %+v, want exactly one", payroll)
	}
	if payroll[0].EntityId != "reimbursements" {
		t.Errorf("entityId = %q, want %q", payroll[0].EntityId, "reimbursements")
	}
	if payroll[0].Count == nil || *payroll[0].Count != 1 {
		t.Errorf("count = %v, want the one waiting expense", payroll[0].Count)
	}

	// Somebody who approves nothing and pays nobody is told about neither.
	stranger, _ := signIn(t, h, "expenses:view-all")
	if items := getAttention(t, stranger); len(items) != 0 {
		t.Errorf("a bystander's items = %+v, want none", items)
	}

	markReimbursed(t, boss, reimbursedBody([]int64{owed.Id}, nil))
	if items := attentionOfType(getAttention(t, boss), attentionReimbursementWaiting); len(items) != 0 {
		t.Errorf("reimbursementWaiting after the payout = %+v, want none", items)
	}
}

// TestExpensesStats_WithoutProjects_AreTheSameFourReads: nothing on the
// dashboard needs a project, so an installation running MODULES=customers,
// expenses gets the whole card.
func TestExpensesStats_WithoutProjects_AreTheSameFourReads(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)

	rejected := createEntry(t, owner, outlayBody(map[string]any{"description": "Lunsj"}))
	approved := createEntry(t, owner, outlayBody(nil))
	submitEntries(t, owner, rejected.Id, approved.Id)
	rejectEntries(t, boss, "Mangler kvittering", rejected.Id)
	approveEntries(t, boss, approved.Id)

	if stats := getStats(t, owner); stats.Approved != 1 || len(stats.Unreimbursed) != 1 {
		t.Errorf("stats = %+v, want one approved expense and one unreimbursed line", stats)
	}
	if summary := getStatsSummary(t, boss, ""); summary.AwaitingMyApproval != 0 {
		t.Errorf("summary = %+v, want nothing awaiting approval", summary)
	}
	if buckets := getTimeseries(t, owner, marchPeriod+"&metric=netAmount"); len(buckets) != 1 {
		t.Errorf("buckets = %+v, want the one approved day", buckets)
	}
	items := getAttention(t, boss)
	if len(attentionOfType(items, attentionReimbursementWaiting)) != 1 {
		t.Errorf("items = %+v, want the payroll one", items)
	}
	if mine := getAttention(t, owner); len(mine) != 1 || mine[0].Type != attentionExpenseRejected {
		t.Errorf("the owner's items = %+v, want the rejection", mine)
	}
}

// TestExpensesStats_AttentionNamesThePersonRatherThanASentence: the host
// translates from type, so the title is only the name of the thing.
func TestExpensesStats_AttentionNamesThePersonRatherThanASentence(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve")
	owner, ownerID := signIn(t, h)
	nameUser(t, h, ownerID, "Anna Ås")
	entry := createEntry(t, owner, outlayBody(nil))
	submitEntries(t, owner, entry.Id)

	items := attentionOfType(getAttention(t, boss), attentionApprovalWaiting)
	if len(items) != 1 {
		t.Fatalf("items = %+v, want one", items)
	}
	if items[0].Title != "Anna Ås" {
		t.Errorf("title = %q, want the person's display name", items[0].Title)
	}
	if items[0].Id != items[0].EntityId {
		t.Errorf("id = %q, entityId = %q; want them the same", items[0].Id, items[0].EntityId)
	}
}
