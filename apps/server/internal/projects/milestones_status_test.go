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

	if got := effectiveAmount(t, invoiced); got != 100000 {
		t.Errorf("EffectiveAmount = %v, want the 100000 frozen at this instant", got)
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
	if got := effectiveAmount(t, getMilestone(t, c, m.Id)); got != 200000 {
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

// The first of the two sequences design §3.3's guards do not cover, because
// they deliberately exempt what cannot bill: a cancelled milestone lets the
// project clear its currency, and reopening it would put a live amount back
// on a project that has no currency to denominate it in. Every move whose
// target is not 'cancelled' therefore re-asks the project's own rules against
// the row the transaction holds, the way a create and an edit do.
func TestPostProjectsMilestonesByMilestoneIdStatus_Reopen_RefusedWithoutTheProjectsCurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MSGUARD10")
	m := createMilestone(t, c, project.Id, map[string]any{"name": "Avlyst"})
	m = movedMilestone(t, c, m, milestoneCancelled, nil)
	project = putProject(t, c, project, map[string]any{"currency": nil})

	r := moveMilestoneStatus(t, c, m, milestonePlanned, nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	msgs := problem.Errors["status"]
	if len(msgs) == 0 {
		t.Fatalf("errors = %v, want a message on 'status'", problem.Errors)
	}
	if !strings.Contains(strings.ToLower(msgs[0]), "currency") {
		t.Errorf("message = %q, want it to point at the project's currency", msgs[0])
	}
	if got := getMilestone(t, c, m.Id); got.Status != milestoneCancelled {
		t.Errorf("Status = %q, want the milestone left cancelled", got.Status)
	}
	if caps := getMilestone(t, c, m.Id).Capabilities; caps.CanReopen {
		t.Errorf("capabilities = %+v, want canReopen false — the server would refuse it", caps)
	}
}

// The same hole seen through the fixed price: a cancelled percent milestone
// lets the project leave fixed-price billing, and reopening it would leave a
// planned milestone whose amount resolves from a price that is gone.
func TestPostProjectsMilestonesByMilestoneIdStatus_Reopen_RefusedWithoutTheFixedPrice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "MSGUARD11", 400000)
	m := createMilestone(t, c, project.Id, map[string]any{"name": "Andel", "amount": nil, "percent": 25})
	m = movedMilestone(t, c, m, milestoneCancelled, nil)
	project = putProject(t, c, project, map[string]any{
		"billingType": "time-and-materials", "fixedPriceAmount": nil,
	})

	r := moveMilestoneStatus(t, c, m, milestonePlanned, nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	msgs := problem.Errors["status"]
	if len(msgs) == 0 {
		t.Fatalf("errors = %v, want a message on 'status'", problem.Errors)
	}
	if !strings.Contains(strings.ToLower(msgs[0]), "fixed price") {
		t.Errorf("message = %q, want it to point at the project's fixed price", msgs[0])
	}
	if caps := getMilestone(t, c, m.Id).Capabilities; caps.CanReopen {
		t.Errorf("capabilities = %+v, want canReopen false", caps)
	}
}

// Marking a milestone ready asks the same question of a milestone that never
// left 'planned': it is a move towards billing, so the project has to be able
// to price it. Design §3.3's guard on the project means an *open* percent
// milestone can never legitimately outlive the fixed price — so this rule is
// defence in depth rather than a path a caller can walk, and the only way to
// reach it is to put the project in that state behind the API's back. The
// price is put back before anything is read, because a planned milestone with
// no resolvable amount is a state the module treats as an error rather than a
// renderable one (only a cancelled one is renderable without an amount).
func TestPostProjectsMilestonesByMilestoneIdStatus_MarkReady_RefusedWithoutTheFixedPrice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "MSGUARD12", 400000)
	m := createMilestone(t, c, project.Id, map[string]any{"name": "Andel", "amount": nil, "percent": 25})
	h.Exec(t, `UPDATE projects.projects SET billing_type = 'time-and-materials', fixed_price_amount = NULL WHERE id = $1`, project.Id)

	r := moveMilestoneStatus(t, c, m, milestoneReady, nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	msgs := problem.Errors["status"]
	if len(msgs) == 0 {
		t.Fatalf("errors = %v, want a message on 'status'", problem.Errors)
	}
	if !strings.Contains(strings.ToLower(msgs[0]), "fixed price") {
		t.Errorf("message = %q, want it to point at the project's fixed price", msgs[0])
	}

	h.Exec(t, `UPDATE projects.projects SET billing_type = 'fixed-price', fixed_price_amount = 400000 WHERE id = $1`, project.Id)
	if got := getMilestone(t, c, m.Id); got.Status != milestonePlanned || got.Revision != 1 {
		t.Errorf("milestone = %+v, want it untouched by the refused move", got)
	}
}

// The one exception, and the reason it is one: crediting an invoice is a real
// thing that happens, so an undo is never refused. A percent milestone whose
// project no longer has a fixed price becomes an amount milestone instead,
// carrying forward the amount that was frozen when it was invoiced — the
// number that was actually billed, which is the only one that means anything
// once the price it was a share of is gone.
func TestPostProjectsMilestonesByMilestoneIdStatus_Undo_ConvertsAPercentWhoseFixedPriceIsGone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "MSGUARD13", 400000)
	m := createMilestone(t, c, project.Id, map[string]any{"name": "Andel", "amount": nil, "percent": 25})
	m = movedMilestone(t, c, m, milestoneReady, nil)
	m = movedMilestone(t, c, m, milestoneInvoiced, nil)
	project = putProject(t, c, project, map[string]any{
		"billingType": "time-and-materials", "fixedPriceAmount": nil,
	})

	if caps := getMilestone(t, c, m.Id).Capabilities; !caps.CanUndoInvoiced {
		t.Fatalf("capabilities = %+v, want canUndoInvoiced true even without a fixed price", caps)
	}
	undone := movedMilestone(t, c, m, milestoneReady, nil)

	if undone.Percent != nil {
		t.Errorf("Percent = %v, want it cleared by the conversion", undone.Percent)
	}
	if undone.Amount == nil || *undone.Amount != 100000 {
		t.Errorf("Amount = %v, want the 100000 that was frozen when it was invoiced", undone.Amount)
	}
	if got := effectiveAmount(t, undone); got != 100000 {
		t.Errorf("EffectiveAmount = %v, want 100000", got)
	}
	stored := modtest.One[string](t, h,
		`SELECT coalesce(amount::text, 'null') || '/' || coalesce(percent::text, 'null')
		 FROM projects.billing_milestones WHERE id = $1`, m.Id)
	if stored != "100000.00/null" {
		t.Errorf("stored amount/percent = %q, want 100000.00/null", stored)
	}

	// Converted means converted: a fixed price set later no longer moves it.
	project = putProject(t, c, project, map[string]any{
		"billingType": "fixed-price", "fixedPriceAmount": 1000000,
	})
	if got := effectiveAmount(t, getMilestone(t, c, m.Id)); got != 100000 {
		t.Errorf("EffectiveAmount = %v, want the converted 100000 to ignore the new price", got)
	}

	// The timeline says it was converted, by a flag and never by a number.
	payload := lastPayloadText(t, h, project.Id, "milestone-invoice-undone")
	if !strings.Contains(payload, "convertedToAmount") {
		t.Errorf("payload %s does not record the conversion", payload)
	}
	if strings.Contains(payload, "100000") {
		t.Errorf("payload %s carries an amount", payload)
	}
}

// An ordinary undo, on a project that still has its fixed price, converts
// nothing: the milestone is still a share of that price and goes back to
// following it. This is the boundary of the case above.
func TestPostProjectsMilestonesByMilestoneIdStatus_Undo_WithTheFixedPriceStillThere_ConvertsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "MSGUARD14", 400000)
	m := createMilestone(t, c, project.Id, map[string]any{"name": "Andel", "amount": nil, "percent": 25})
	m = movedMilestone(t, c, m, milestoneReady, nil)
	m = movedMilestone(t, c, m, milestoneInvoiced, nil)

	undone := movedMilestone(t, c, m, milestoneReady, nil)
	if undone.Amount != nil || undone.Percent == nil || *undone.Percent != 25 {
		t.Errorf("milestone = %+v, want it still a percent", undone)
	}
	if payload := lastPayloadText(t, h, project.Id, "milestone-invoice-undone"); strings.Contains(payload, "convertedToAmount") {
		t.Errorf("payload %s claims a conversion that did not happen", payload)
	}
}

// projects:manage-all is the project manager's rights held globally (design
// §5), so a holder who has no role on the project at all may do every
// manager-only thing with its milestones. Only marking one ready was pinned
// before; the brief's matrix names the whole of it.
func TestMilestones_ManageAll_MayDoEverythingWithoutARoleOnTheProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h, "projects:create")
	project := amountProject(t, owner, "MSGUARD15")

	admin, _ := signIn(t, h, "projects:manage-all")
	created := createMilestone(t, admin, project.Id, map[string]any{"name": "Fra admin"})
	if !created.Capabilities.CanEdit || !created.Capabilities.CanDelete {
		t.Errorf("capabilities = %+v, want a manage-all holder everything a manager has", created.Capabilities)
	}
	changed := changeMilestone(t, admin, created, map[string]any{"name": "Endret av admin"})
	second := createMilestone(t, admin, project.Id, map[string]any{"name": "Andre"})
	if r := moveMilestone(t, admin, second, 1); r.Status != http.StatusOK {
		t.Fatalf("reorder: status %d body %s, want 200", r.Status, r.Body)
	}
	cancelled := movedMilestone(t, admin, changed, milestoneCancelled, nil)
	movedMilestone(t, admin, cancelled, milestonePlanned, nil)
	if r := admin.Do(http.MethodDelete, milestonePath(second.Id), nil); r.Status != http.StatusNoContent {
		t.Errorf("delete: status %d body %s, want 204", r.Status, r.Body)
	}
}

// Whether a reference is allowed at all is decided before whether it is too
// long: a caller who sent one on the wrong move has to be told that, not that
// the thing they should not have sent is also over a hundred characters.
func TestPostProjectsMilestonesByMilestoneIdStatus_ALongReferenceOnAnotherMove_SaysItIsNotAllowed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MSREF1")
	m := createMilestone(t, c, project.Id, map[string]any{"name": "Milepæl"})

	r := moveMilestoneStatus(t, c, m, milestoneReady, map[string]any{
		"invoiceReference": strings.Repeat("x", 101),
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	msgs := problem.Errors["invoiceReference"]
	if len(msgs) == 0 {
		t.Fatalf("errors = %v, want a message on 'invoiceReference'", problem.Errors)
	}
	if !strings.Contains(msgs[0], "only allowed") {
		t.Errorf("message = %q, want it to say the field is not allowed on this move", msgs[0])
	}
}
