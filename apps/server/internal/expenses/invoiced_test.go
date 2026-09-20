package expenses_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is design decision X5's second track: what has been billed on to a
// customer. It is per billable line on a project, and it belongs to whoever
// may see the project's money — not to expenses:manage, who pays the employee
// rather than invoicing the customer.

// billableOutlay records an approved, billable outlay on the ordinary customer
// project and answers it as its owner last saw it.
func billableOutlay(t *testing.T, owner, approver *modtest.Client, overrides map[string]any) entryJSON {
	t.Helper()
	entry := createEntry(t, owner, outlayBody(bodyWith(map[string]any{
		"projectId": projectKraftVerket, "billable": true,
	}, overrides)))
	approvedBy(t, owner, approver, entry.Id)
	return getEntry(t, owner, entry.Id)
}

// invoicedBody is the body POST /entries/{id}/invoiced takes.
func invoicedBody(revision int32, overrides map[string]any) map[string]any {
	return bodyWith(map[string]any{"revision": revision}, overrides)
}

// TestExpensesInvoiced_MarksAndUndoesWithTheReference is the move itself, and
// the way back from it.
func TestExpensesInvoiced_MarksAndUndoesWithTheReference(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, managerID := signInAs(t, h, projectKraftVerket, roleManager, "expenses:approve")
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	entry := billableOutlay(t, owner, manager, nil)

	before := getEntry(t, manager, entry.Id)
	if !before.Capabilities.CanMarkInvoiced || before.Capabilities.CanUndoInvoiced {
		t.Errorf("capabilities before = %+v, want canMarkInvoiced alone", before.Capabilities)
	}
	billed := markInvoiced(t, manager, entry.Id, invoicedBody(before.Revision, map[string]any{"reference": "F-2026-118"}))
	switch {
	case billed.Billing == nil || billed.Billing.Invoice == nil:
		t.Fatalf("the invoiced line carries %+v, want an invoice", billed.Billing)
	case billed.Billing.Invoice.Reference == nil || *billed.Billing.Invoice.Reference != "F-2026-118":
		t.Errorf("invoice reference = %v, want the one given", billed.Billing.Invoice.Reference)
	case billed.Billing.Invoice.By.UserId != managerID:
		t.Errorf("invoiced by = %v, want the caller", billed.Billing.Invoice.By.UserId)
	case billed.Revision != before.Revision+1:
		t.Errorf("revision = %d, want %d", billed.Revision, before.Revision+1)
	case billed.Status != "approved":
		t.Errorf("status = %q, want it still approved", billed.Status)
	}
	if billed.Capabilities.CanMarkInvoiced || !billed.Capabilities.CanUndoInvoiced {
		t.Errorf("capabilities after = %+v, want only the undo", billed.Capabilities)
	}
	// Marking a line invoiced records that it went out; it reprices nothing.
	if billed.GrossAmount != before.GrossAmount || billed.Billing.BillAmount != before.Billing.BillAmount {
		t.Errorf("figures after the invoicing = %v/%v, want the frozen %v/%v",
			billed.GrossAmount, billed.Billing.BillAmount, before.GrossAmount, before.Billing.BillAmount)
	}

	// The owner sees their own gross and what they are owed, and nothing of
	// what the customer was charged for it.
	if mine := getEntry(t, owner, entry.Id); mine.Billing != nil {
		t.Errorf("the owner's copy carries billing %+v, want none", mine.Billing)
	}

	back := undoInvoiced(t, manager, entry.Id, invoicedBody(billed.Revision, nil))
	if back.Billing == nil || back.Billing.Invoice != nil {
		t.Errorf("after the undo the line carries %+v, want no invoice", back.Billing)
	}
}

// TestExpensesInvoiced_NeedsFinancialRightsOnTheProject: the three refusals
// are told apart exactly as the pricing door tells them apart.
func TestExpensesInvoiced_NeedsFinancialRightsOnTheProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager, "expenses:approve")
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	entry := billableOutlay(t, owner, manager, nil)
	revision := getEntry(t, manager, entry.Id).Revision

	outsider, _ := signIn(t, h)
	if r := outsider.Do(http.MethodPost, entryInvoicedPath(entry.Id), invoicedBody(revision, nil)); r.Status != http.StatusNotFound {
		t.Errorf("an outsider: status %d body %s, want 404", r.Status, r.Body)
	}
	seer, _ := signIn(t, h, "expenses:view-all", "expenses:manage")
	forbidden(t, seer, http.MethodPost, entryInvoicedPath(entry.Id), invoicedBody(revision, nil))
	forbidden(t, seer, http.MethodPost, entryInvoicedUndoPath(entry.Id), invoicedBody(revision, nil))
	if caps := getEntry(t, seer, entry.Id).Capabilities; caps.CanMarkInvoiced {
		t.Error("canMarkInvoiced is true for a caller with no financial rights on the project")
	}

	// projects:view-financials on a project they can see is enough, as it is
	// everywhere else here — beside whatever lets them see the expense at all,
	// which a project role short of manager does not.
	financial, _ := signInAs(t, h, projectKraftVerket, roleViewer, "expenses:view-all", "projects:view-financials")
	markInvoiced(t, financial, entry.Id, invoicedBody(revision, nil))
}

// TestExpensesInvoiced_OnlyAnApprovedBillableLineWithAnAmount: every state
// refusal is a 400 naming the field it is about.
func TestExpensesInvoiced_OnlyAnApprovedBillableLineWithAnAmount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager, "expenses:approve")
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)

	draft := createEntry(t, owner, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true,
	}))
	errs := refusedEntry(t, manager, http.MethodPost, entryInvoicedPath(draft.Id), invoicedBody(draft.Revision, nil))
	if len(errs["status"]) == 0 {
		t.Errorf("a draft answered %v, want a refusal on status", errs)
	}

	notBillable := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	approvedBy(t, owner, manager, notBillable.Id)
	onIt := getEntry(t, manager, notBillable.Id)
	errs = refusedEntry(t, manager, http.MethodPost, entryInvoicedPath(notBillable.Id), invoicedBody(onIt.Revision, nil))
	if len(errs["billable"]) == 0 {
		t.Errorf("a line nobody bills answered %v, want a refusal on billable", errs)
	}

	// Billable mileage recorded while no customer rate was in force carries no
	// bill amount at all: it must be priced before it can be invoiced.
	unpriced := createEntry(t, owner, mileageBody(map[string]any{
		"projectId": projectKraftVerket, "billable": true,
	}))
	approvedBy(t, owner, manager, unpriced.Id)
	priced := getEntry(t, manager, unpriced.Id)
	errs = refusedEntry(t, manager, http.MethodPost, entryInvoicedPath(unpriced.Id), invoicedBody(priced.Revision, nil))
	if len(errs["billAmount"]) == 0 {
		t.Errorf("an unpriced line answered %v, want a refusal on billAmount", errs)
	}
	if caps := priced.Capabilities; caps.CanMarkInvoiced {
		t.Error("canMarkInvoiced is true on a line with nothing to bill")
	}

	// And a second invoicing, and an undo of something never invoiced.
	done := billableOutlay(t, owner, manager, map[string]any{"description": "Stillas"})
	current := getEntry(t, manager, done.Id)
	billed := markInvoiced(t, manager, done.Id, invoicedBody(current.Revision, nil))
	errs = refusedEntry(t, manager, http.MethodPost, entryInvoicedPath(done.Id), invoicedBody(billed.Revision, nil))
	if len(errs["status"]) == 0 {
		t.Errorf("a second invoicing answered %v, want a refusal on status", errs)
	}
	errs = refusedEntry(t, manager, http.MethodPost, entryInvoicedUndoPath(notBillable.Id), invoicedBody(onIt.Revision, nil))
	if len(errs["status"]) == 0 {
		t.Errorf("an undo of something never invoiced answered %v, want a refusal on status", errs)
	}
}

// TestExpensesInvoiced_IsGuardedByTheRevision: nobody invoices a line they
// have not read.
func TestExpensesInvoiced_IsGuardedByTheRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager, "expenses:approve")
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	entry := billableOutlay(t, owner, manager, nil)

	r := manager.Do(http.MethodPost, entryInvoicedPath(entry.Id), invoicedBody(1, nil))
	if r.Status != http.StatusConflict {
		t.Fatalf("a stale revision: status %d body %s, want 409", r.Status, r.Body)
	}
	long := strings.Repeat("f", 101)
	current := getEntry(t, manager, entry.Id)
	errs := refusedEntry(t, manager, http.MethodPost, entryInvoicedPath(entry.Id),
		invoicedBody(current.Revision, map[string]any{"reference": long}))
	if len(errs["reference"]) == 0 {
		t.Errorf("errors = %v, want one on reference", errs)
	}
}

// TestExpensesInvoiced_IsNotHeldBackByThePeriodLock. The lock protects what an
// employee submitted and what was approved — dates, amounts, status moves.
// Invoicing is bookkeeping done after the period closes, by somebody who is
// not expenses:manage and would otherwise be locked out of their own books.
func TestExpensesInvoiced_IsNotHeldBackByThePeriodLock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:manage")
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager, "expenses:approve")
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	entry := billableOutlay(t, owner, manager, nil)
	putSettings(t, boss, settingsBody(map[string]any{"lockedBefore": "2026-04-01"}))

	current := getEntry(t, manager, entry.Id)
	if !current.Capabilities.CanMarkInvoiced {
		t.Error("canMarkInvoiced is false inside a closed period")
	}
	billed := markInvoiced(t, manager, entry.Id, invoicedBody(current.Revision, nil))
	if billed.Billing == nil || billed.Billing.Invoice == nil {
		t.Fatal("a line dated inside a closed period could not be invoiced")
	}
	if !billed.Capabilities.CanUndoInvoiced {
		t.Error("canUndoInvoiced is false inside a closed period")
	}
	undoInvoiced(t, manager, entry.Id, invoicedBody(billed.Revision, nil))
}

// TestExpensesInvoiced_ClosesTheOtherTwoDoors: what has been sent to a
// customer is not repriced behind their back, and is not unapproved either.
func TestExpensesInvoiced_ClosesTheOtherTwoDoors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager, "expenses:approve", "expenses:manage")
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	entry := billableOutlay(t, owner, manager, nil)
	current := getEntry(t, manager, entry.Id)
	billed := markInvoiced(t, manager, entry.Id, invoicedBody(current.Revision, nil))

	errs := refusedEntry(t, manager, http.MethodPut, entryBillingPath(entry.Id), map[string]any{
		"billable": true, "markupPercent": 25.0, "revision": billed.Revision,
	})
	if len(errs["status"]) == 0 {
		t.Errorf("pricing an invoiced line answered %v, want a refusal on status", errs)
	}
	if caps := billed.Capabilities; caps.CanSetBilling {
		t.Error("canSetBilling is true on an invoiced line")
	}

	flow := refusedFlow(t, manager, unapprovePath, flowBody([]int64{entry.Id}, nil))
	if !mentions(flow["entryIds"], fmt.Sprintf("Expense %d has been invoiced", entry.Id)) {
		t.Errorf("errors %v do not refuse unapproving an invoiced expense", flow["entryIds"])
	}
	if billed.Capabilities.CanUnapprove {
		t.Error("canUnapprove is true on an invoiced expense")
	}
}

// TestExpensesInvoiced_AReimbursedLineIsStillInvoiceable: the two tracks are
// independent — one pays the employee, the other charges the customer.
func TestExpensesInvoiced_AReimbursedLineIsStillInvoiceable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager, "expenses:approve", "expenses:manage")
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	entry := billableOutlay(t, owner, manager, nil)

	markReimbursed(t, manager, reimbursedBody([]int64{entry.Id}, nil))
	current := getEntry(t, manager, entry.Id)
	billed := markInvoiced(t, manager, entry.Id, invoicedBody(current.Revision, nil))
	if billed.Reimbursement == nil || billed.Billing.Invoice == nil {
		t.Errorf("the line carries reimbursement %+v and billing %+v, want both",
			billed.Reimbursement, billed.Billing)
	}
}

// TestExpensesInvoiced_WithoutProjects_IsA400OnProjectId: with no projects
// module there is no customer to invoice, and the operation says so on the
// field rather than pretending the expense does not exist.
func TestExpensesInvoiced_WithoutProjects_IsA400OnProjectId(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)
	entry := createEntry(t, owner, outlayBody(nil))
	approvedBy(t, owner, boss, entry.Id)
	current := getEntry(t, boss, entry.Id)

	errs := refusedEntry(t, boss, http.MethodPost, entryInvoicedPath(entry.Id), invoicedBody(current.Revision, nil))
	if len(errs["projectId"]) == 0 {
		t.Errorf("errors = %v, want one on projectId", errs)
	}
	if caps := current.Capabilities; caps.CanMarkInvoiced || caps.CanUndoInvoiced {
		t.Errorf("capabilities = %+v, want neither invoicing capability", caps)
	}
}
