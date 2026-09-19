package projects_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Design §3.2's status flow and §2 E5's manual "mark as invoiced": a
// milestone goes planned → ready → invoiced, may be cancelled from either
// open status and reopened from cancelled, and the invoicing can be undone.
// Anything else is refused by naming the move.
//
// Two rules make this its own file. The move table has an access column of
// its own — invoicing and undoing it need financial rights and nothing more,
// while every other move is the project's manager — and every move writes its
// own timeline entry, which is the record a member reads even though the
// amounts are not theirs to see.

// milestoneIn builds one milestone already in the status a case starts from,
// through the same API a caller would: there is no other way to reach 'ready'
// or 'invoiced', and a row inserted behind the API would not carry the stamps
// the moves out of those statuses are decided against.
func milestoneIn(t *testing.T, c *modtest.Client, projectID int32, name, status string) milestoneJSON {
	t.Helper()
	m := createMilestone(t, c, projectID, map[string]any{"name": name})
	switch status {
	case milestonePlanned:
		return m
	case milestoneReady:
		return movedMilestone(t, c, m, milestoneReady, nil)
	case milestoneInvoiced:
		return movedMilestone(t, c, movedMilestone(t, c, m, milestoneReady, nil), milestoneInvoiced, nil)
	case milestoneCancelled:
		return movedMilestone(t, c, m, milestoneCancelled, nil)
	default:
		t.Fatalf("milestoneIn: no such status %q", status)
		return milestoneJSON{}
	}
}

// Every move the table allows, each with the entry it writes and the status
// it lands in. They are one test because the table is one rule: a case
// missing from here is a move nobody checked.
func TestPostProjectsMilestonesByMilestoneIdStatus_TheAllowedMoves(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		code  string
		from  string
		to    string
		event string
	}{
		{"planned to ready", "MSMV01", milestonePlanned, milestoneReady, "milestone-ready"},
		{"ready back to planned", "MSMV02", milestoneReady, milestonePlanned, "milestone-planned"},
		{"ready to invoiced", "MSMV03", milestoneReady, milestoneInvoiced, "milestone-invoiced"},
		{"invoiced back to ready", "MSMV04", milestoneInvoiced, milestoneReady, "milestone-invoice-undone"},
		{"planned to cancelled", "MSMV05", milestonePlanned, milestoneCancelled, "milestone-cancelled"},
		{"ready to cancelled", "MSMV06", milestoneReady, milestoneCancelled, "milestone-cancelled"},
		{"cancelled back to planned", "MSMV07", milestoneCancelled, milestonePlanned, "milestone-reopened"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			c, _ := signIn(t, h, "projects:create")
			project := amountProject(t, c, tc.code)
			m := milestoneIn(t, c, project.Id, "Milepæl", tc.from)

			moved := movedMilestone(t, c, m, tc.to, nil)
			if moved.Status != tc.to {
				t.Errorf("Status = %q, want %q", moved.Status, tc.to)
			}
			if moved.Revision != m.Revision+1 {
				t.Errorf("Revision = %d, want %d", moved.Revision, m.Revision+1)
			}
			if got := eventTypes(t, h, project.Id); got[len(got)-1] != tc.event {
				t.Errorf("timeline = %v, want it to end with %s", got, tc.event)
			}
			payload := lastPayloadText(t, h, project.Id, tc.event)
			if !strings.Contains(payload, "Milepæl") || !strings.Contains(payload, fmt.Sprintf("%d", m.Id)) {
				t.Errorf("payload %s does not name the milestone and its id", payload)
			}
			if strings.Contains(payload, "100000") {
				t.Errorf("payload %s carries an amount", payload)
			}
		})
	}
}

// Every pair the table does not have, refused by naming both statuses. The
// same-status "moves" are in here too: pressing a button that changes nothing
// is not a move, and answering 200 to it would write a timeline entry saying
// something happened.
func TestPostProjectsMilestonesByMilestoneIdStatus_TheRefusedMoves(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, code, from, to string }{
		{"planned straight to invoiced", "MSMR01", milestonePlanned, milestoneInvoiced},
		{"planned to planned", "MSMR02", milestonePlanned, milestonePlanned},
		{"ready to ready", "MSMR03", milestoneReady, milestoneReady},
		{"invoiced to planned", "MSMR04", milestoneInvoiced, milestonePlanned},
		{"invoiced to cancelled", "MSMR05", milestoneInvoiced, milestoneCancelled},
		{"invoiced to invoiced", "MSMR06", milestoneInvoiced, milestoneInvoiced},
		{"cancelled to ready", "MSMR07", milestoneCancelled, milestoneReady},
		{"cancelled to invoiced", "MSMR08", milestoneCancelled, milestoneInvoiced},
		{"cancelled to cancelled", "MSMR09", milestoneCancelled, milestoneCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			c, _ := signIn(t, h, "projects:create")
			project := amountProject(t, c, tc.code)
			m := milestoneIn(t, c, project.Id, "Milepæl", tc.from)
			before := eventTypes(t, h, project.Id)

			r := moveMilestoneStatus(t, c, m, tc.to, nil)
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem validationProblemJSON
			r.JSON(&problem)
			msgs := problem.Errors["status"]
			if len(msgs) == 0 {
				t.Fatalf("errors = %v, want a message on 'status'", problem.Errors)
			}
			if !strings.Contains(msgs[0], tc.from) || !strings.Contains(msgs[0], tc.to) {
				t.Errorf("message = %q, want it to name both %q and %q", msgs[0], tc.from, tc.to)
			}
			if got := eventTypes(t, h, project.Id); len(got) != len(before) {
				t.Errorf("timeline = %v, want the refused move to have written nothing", got)
			}
			if getMilestone(t, c, m.Id).Status != tc.from {
				t.Errorf("the milestone moved anyway, want it left in %q", tc.from)
			}
		})
	}
}

// A status the contract does not have is a bad body, not a move: it is
// refused on the same field, naming the four that exist.
func TestPostProjectsMilestonesByMilestoneIdStatus_UnknownStatus_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MSMR10")
	m := createMilestone(t, c, project.Id, nil)

	for _, status := range []string{"", "Ready", "billed"} {
		r := moveMilestoneStatus(t, c, m, status, nil)
		if r.Status != http.StatusBadRequest {
			t.Fatalf("%q: status %d body %s, want 400", status, r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if len(problem.Errors["status"]) == 0 {
			t.Errorf("%q: errors = %v, want a message on 'status'", status, problem.Errors)
		}
	}
}

// → ready stamps who said so and when; ready → planned takes the stamp off
// again, because a milestone that is back to being planned was never marked
// ready by anybody.
func TestPostProjectsMilestonesByMilestoneIdStatus_ReadyStampsAndPlannedClears(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := signIn(t, h, "projects:create")
	setDisplayName(t, h, userID, "Marte Leder")
	project := amountProject(t, c, "MSST01")
	m := createMilestone(t, c, project.Id, nil)

	ready := movedMilestone(t, c, m, milestoneReady, nil)
	if ready.ReadyAt == nil || !ready.ReadyAt.Equal(h.Now()) {
		t.Errorf("ReadyAt = %v, want the server clock %v", ready.ReadyAt, h.Now())
	}
	if ready.ReadyBy == nil || ready.ReadyBy.UserId != userID || ready.ReadyBy.DisplayName != "Marte Leder" {
		t.Errorf("ReadyBy = %+v, want the caller", ready.ReadyBy)
	}
	if !ready.ReadyBy.Active {
		t.Errorf("ReadyBy.Active = false, want true for a live account")
	}

	back := movedMilestone(t, c, ready, milestonePlanned, nil)
	if back.ReadyAt != nil || back.ReadyBy != nil {
		t.Errorf("milestone = %+v, want the ready stamps cleared", back)
	}
}

// E5's manual invoicing: the move takes an optional reference and date,
// freezes the amount as it stood at that instant, and records who did it.
// Freezing is what makes an invoiced milestone stop following the fixed price
// (see milestones_test.go), so it is asserted on the stored column too.
func TestPostProjectsMilestonesByMilestoneIdStatus_Invoiced_FreezesTheAmountAndStamps(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := signIn(t, h, "projects:create")
	setDisplayName(t, h, userID, "Marte Leder")
	project := fixedPriceProject(t, c, "MSST02", 400000)
	m := createMilestone(t, c, project.Id, map[string]any{"name": "Andel", "amount": nil, "percent": 25})
	m = movedMilestone(t, c, m, milestoneReady, nil)

	invoiced := movedMilestone(t, c, m, milestoneInvoiced, map[string]any{
		"invoiceReference": "F-2026-0042", "invoiceDate": "2026-09-30",
	})

	if invoiced.EffectiveAmount != 100000 {
		t.Errorf("EffectiveAmount = %v, want the 100000 frozen at this instant", invoiced.EffectiveAmount)
	}
	if invoiced.InvoicedAt == nil || !invoiced.InvoicedAt.Equal(h.Now()) {
		t.Errorf("InvoicedAt = %v, want the server clock", invoiced.InvoicedAt)
	}
	if invoiced.InvoicedBy == nil || invoiced.InvoicedBy.UserId != userID {
		t.Errorf("InvoicedBy = %+v, want the caller", invoiced.InvoicedBy)
	}
	if invoiced.InvoiceReference == nil || *invoiced.InvoiceReference != "F-2026-0042" {
		t.Errorf("InvoiceReference = %v, want the one sent", invoiced.InvoiceReference)
	}
	if invoiced.InvoiceDate == nil || *invoiced.InvoiceDate != "2026-09-30" {
		t.Errorf("InvoiceDate = %v, want 2026-09-30", invoiced.InvoiceDate)
	}
	if invoiced.Percent == nil || *invoiced.Percent != 25 {
		t.Errorf("Percent = %v, want the milestone to keep saying what it was", invoiced.Percent)
	}
	stored := modtest.One[string](t, h,
		`SELECT invoiced_amount::text FROM projects.billing_milestones WHERE id = $1`, m.Id)
	if stored != "100000.00" {
		t.Errorf("invoiced_amount = %q, want 100000.00 stored, not recomputed on read", stored)
	}
	want := milestoneCapabilitiesJSON{CanUndoInvoiced: true}
	if invoiced.Capabilities != want {
		t.Errorf("Capabilities = %+v, want only canUndoInvoiced", invoiced.Capabilities)
	}
}

// Undoing the invoicing puts the milestone back to ready and clears all four
// invoice fields: the reference, the date, the stamp and the frozen amount,
// which then starts following the fixed price again.
func TestPostProjectsMilestonesByMilestoneIdStatus_Undo_ClearsAllFourInvoiceFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "MSST03", 400000)
	m := createMilestone(t, c, project.Id, map[string]any{"name": "Andel", "amount": nil, "percent": 25})
	m = movedMilestone(t, c, m, milestoneReady, nil)
	m = movedMilestone(t, c, m, milestoneInvoiced, map[string]any{
		"invoiceReference": "F-2026-0042", "invoiceDate": "2026-09-30",
	})

	undone := movedMilestone(t, c, m, milestoneReady, nil)
	if undone.InvoicedAt != nil || undone.InvoicedBy != nil ||
		undone.InvoiceReference != nil || undone.InvoiceDate != nil {
		t.Errorf("milestone = %+v, want every invoice field cleared", undone)
	}
	if n := h.Count(t, `SELECT count(*) FROM projects.billing_milestones
	                    WHERE id = $1 AND invoiced_amount IS NOT NULL`, m.Id); n != 0 {
		t.Errorf("the frozen amount survived the undo")
	}
	putProject(t, c, project, map[string]any{"fixedPriceAmount": 800000})
	if got := getMilestone(t, c, m.Id).EffectiveAmount; got != 200000 {
		t.Errorf("EffectiveAmount = %v, want 200000 — an un-invoiced percent follows the price again", got)
	}
}

// A reference and a date belong to the invoicing and to nothing else, so
// sending either on any other move is a bad body rather than something
// silently ignored.
func TestPostProjectsMilestonesByMilestoneIdStatus_InvoiceFieldsOnAnotherMove_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MSST04")

	for _, tc := range []struct {
		field string
		value any
	}{
		{"invoiceReference", "F-1"},
		{"invoiceDate", "2026-09-30"},
	} {
		m := createMilestone(t, c, project.Id, map[string]any{"name": "M-" + tc.field})
		r := moveMilestoneStatus(t, c, m, milestoneReady, map[string]any{tc.field: tc.value})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("%s: status %d body %s, want 400", tc.field, r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if len(problem.Errors[tc.field]) == 0 {
			t.Errorf("%s: errors = %v, want a message on that field", tc.field, problem.Errors)
		}
	}

	// The reference has a length of its own, checked on the move that does
	// accept it.
	m := createMilestone(t, c, project.Id, map[string]any{"name": "For lang"})
	m = movedMilestone(t, c, m, milestoneReady, nil)
	r := moveMilestoneStatus(t, c, m, milestoneInvoiced, map[string]any{
		"invoiceReference": strings.Repeat("x", 101),
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("a 101-character reference: status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["invoiceReference"]) == 0 {
		t.Errorf("errors = %v, want a message on 'invoiceReference'", problem.Errors)
	}
}

// The move is revision guarded like every other milestone write: two people
// looking at the same plan cannot both mark the same milestone.
func TestPostProjectsMilestonesByMilestoneIdStatus_StaleRevision_Returns409(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MSST05")
	m := createMilestone(t, c, project.Id, nil)
	movedMilestone(t, c, m, milestoneReady, nil)

	r := moveMilestoneStatus(t, c, m, milestoneCancelled, nil)
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if !strings.Contains(problem.Detail, "revision 2") {
		t.Errorf("detail = %q, want it to name the revision the milestone now carries", problem.Detail)
	}
}

// The move table's access column (§3.2): invoicing and undoing it need
// financial rights and nothing more — a projects:view-financials holder who
// is nobody's manager may bill — while marking ready, cancelling and
// reopening are the project's manager's.
func TestPostProjectsMilestonesByMilestoneIdStatus_FinancialViewer_MayInvoiceAndUndoOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h, "projects:create")
	project := amountProject(t, owner, "MSST06")

	viewer, viewerID := signIn(t, h, "projects:view-financials")
	addRole(t, h, project.Id, viewerID, "viewer")

	// Reads are theirs.
	plan := getMilestones(t, viewer, project.Id)
	if len(plan.Milestones) != 0 {
		t.Errorf("plan = %+v, want an empty one", plan)
	}

	m := createMilestone(t, owner, project.Id, map[string]any{"name": "Milepæl"})
	// Marking it ready is not.
	if r := moveMilestoneStatus(t, viewer, m, milestoneReady, nil); r.Status != http.StatusForbidden {
		t.Fatalf("a viewer marking ready: status %d body %s, want 403", r.Status, r.Body)
	}
	m = movedMilestone(t, owner, m, milestoneReady, nil)

	// Invoicing it is, and so is undoing it.
	invoiced := movedMilestone(t, viewer, m, milestoneInvoiced, map[string]any{"invoiceReference": "F-9"})
	if invoiced.InvoicedBy == nil || invoiced.InvoicedBy.UserId != viewerID {
		t.Errorf("InvoicedBy = %+v, want the viewer", invoiced.InvoicedBy)
	}
	undone := movedMilestone(t, viewer, invoiced, milestoneReady, nil)

	// Cancelling and reopening are not.
	if r := moveMilestoneStatus(t, viewer, undone, milestoneCancelled, nil); r.Status != http.StatusForbidden {
		t.Errorf("a viewer cancelling: status %d body %s, want 403", r.Status, r.Body)
	}
	// Nor is anything that edits the milestone.
	if r := putMilestone(t, viewer, undone, map[string]any{"name": "Nytt"}); r.Status != http.StatusForbidden {
		t.Errorf("a viewer editing: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := postMilestone(t, viewer, project.Id, nil); r.Status != http.StatusForbidden {
		t.Errorf("a viewer creating: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := viewer.Do(http.MethodDelete, milestonePath(undone.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("a viewer deleting: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := moveMilestone(t, viewer, undone, 1); r.Status != http.StatusForbidden {
		t.Errorf("a viewer reordering: status %d body %s, want 403", r.Status, r.Body)
	}

	// And the capabilities say exactly that before they try.
	caps := getMilestone(t, viewer, undone.Id).Capabilities
	want := milestoneCapabilitiesJSON{CanMarkInvoiced: true}
	if caps != want {
		t.Errorf("Capabilities = %+v, want only canMarkInvoiced", caps)
	}
}

// A stamp outlives the account that made it: the milestone keeps saying who
// marked it, and the directory's answer is what says they are gone.
func TestMilestones_ReadyBy_SurvivesTheAccountAsInactive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h, "projects:create", "projects:manage-all")
	project := amountProject(t, owner, "MSST07")

	second, secondID := signIn(t, h, "projects:manage-all")
	setDisplayName(t, h, secondID, "Sluttet Ansatt")
	m := createMilestone(t, owner, project.Id, nil)
	movedMilestone(t, second, m, milestoneReady, nil)
	disableUser(t, h, secondID)

	got := getMilestone(t, owner, m.Id)
	if got.ReadyBy == nil || got.ReadyBy.UserId != secondID {
		t.Fatalf("ReadyBy = %+v, want the disabled account", got.ReadyBy)
	}
	if got.ReadyBy.Active {
		t.Errorf("ReadyBy.Active = true, want false for a disabled account")
	}
	if got.ReadyBy.DisplayName != "Sluttet Ansatt" {
		t.Errorf("ReadyBy.DisplayName = %q, want the name they had", got.ReadyBy.DisplayName)
	}
}

// A manager's capabilities in each status, which is the move table read from
// the other side — what the frontend renders buttons from.
func TestMilestones_Capabilities_AreTheMoveTablePerStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MSST08")

	cases := []struct {
		status string
		want   milestoneCapabilitiesJSON
	}{
		{milestonePlanned, milestoneCapabilitiesJSON{CanEdit: true, CanDelete: true, CanMarkReady: true, CanCancel: true}},
		{milestoneReady, milestoneCapabilitiesJSON{CanEdit: true, CanMarkPlanned: true, CanMarkInvoiced: true, CanCancel: true}},
		{milestoneInvoiced, milestoneCapabilitiesJSON{CanUndoInvoiced: true}},
		{milestoneCancelled, milestoneCapabilitiesJSON{CanReopen: true}},
	}
	for _, tc := range cases {
		m := milestoneIn(t, c, project.Id, "M-"+tc.status, tc.status)
		if m.Capabilities != tc.want {
			t.Errorf("%s: Capabilities = %+v, want %+v", tc.status, m.Capabilities, tc.want)
		}
	}
}
