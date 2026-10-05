package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The project dimension (invoices work design D9): the one project all of a
// draft's held work belongs to, derived by every save from its own line
// sources — never written by a request — with the project's code as a
// snapshot, read through the project directory before the save's
// transaction; frozen at issue; copied by a credit note. As the sources tests
// do, the held rows are planted by SQL on drafts made through the API.

// projectOf is a document's derived project as "<id> <reference>", "-" for
// none (and "!" when the two disagree, which ck_invoices_project forbids).
func projectOf(d invoiceJSON) string {
	switch {
	case d.ProjectID == nil && d.ProjectReference == nil:
		return "-"
	case d.ProjectID == nil || d.ProjectReference == nil:
		return "!"
	}
	return fmt.Sprintf("%d %s", *d.ProjectID, *d.ProjectReference)
}

// putDoc replaces a draft as c and answers it, failing unless it was saved.
func putDoc(t *testing.T, c *modtest.Client, id int64, body map[string]any) invoiceJSON {
	t.Helper()
	res := c.Do(http.MethodPut, invoicePath(id), body)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT /invoices/%d = %d %s, want 200", id, res.Status, res.Body)
	}
	var doc invoiceJSON
	res.JSON(&doc)
	return doc
}

// acrossTwo is theFour under other ids (each 1000 on) with the milestone on
// project 42: work that spans two projects, held beside theFour's.
func acrossTwo() []planted {
	rows := theFour()
	for i := range rows {
		rows[i].id += 1000
	}
	rows[3].project = project42
	return rows
}

// linesNaming is the sourced draft's three lines, each naming the rows
// planted on it when its position is in keep, and [] otherwise.
func linesNaming(rows []planted, keep ...int32) []map[string]any {
	named := map[int32][]refJSON{1: {}, 2: {}, 3: {}}
	for _, r := range rows {
		if slices.Contains(keep, r.position) {
			named[r.position] = append(named[r.position], ref(r.kind, r.id))
		}
	}
	return []map[string]any{
		sourcedLine(line("Konsulenttimer", 7.5, 1200, vat25), named[1]),
		sourcedLine(line("Kjøregodtgjørelse", 1, 450, vat25), named[2]),
		sourcedLine(line("Milepæl", 1, 10000, vat25), named[3]),
	}
}

// projectCalls is how many project-directory reads userID's requests made,
// and whether any was made under a lock.
func projectCalls(userID uuid.UUID) (n int, locked bool) {
	for _, c := range contractCalls.by(userID) {
		if c.method == "Projects.Projects" {
			n++
			locked = locked || c.locked
		}
	}
	return n, locked
}

// A save sets the project when all the work it keeps belongs to one, with
// the project's code; none when the work spans two, and none when it holds
// no work — and a save that drops the second project's work leaves the
// first. GET answers what the save stored.
func TestProject_DerivedOneTwoNone(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithProjects(newFakeProjects()))
	c := creator(t, h)

	one := sourcedDraft(t, h)
	if got := projectOf(one); got != "-" {
		t.Fatalf("a created draft's project = %s, want none: nothing derived it yet", got)
	}
	saved := putDoc(t, c, one.ID, sourcedBody(one, customerAcme, theSameLines()...))
	if got := projectOf(saved); got != "41 P-41" {
		t.Errorf("one project: %s, want 41 P-41", got)
	}
	if got := projectOf(getInvoice(t, h, one.ID)); got != "41 P-41" {
		t.Errorf("GET after the save: %s, want 41 P-41", got)
	}

	two := plantedDraft(t, h, acrossTwo()...)
	saved = putDoc(t, c, two.ID, sourcedBody(two, customerAcme, linesNaming(acrossTwo(), 1, 2, 3)...))
	if got := projectOf(saved); got != "-" {
		t.Errorf("two projects: %s, want none", got)
	}
	// The milestone's line names no work any more: what is left is project
	// 41's alone.
	saved = putDoc(t, c, two.ID, sourcedBody(saved, customerAcme, linesNaming(acrossTwo(), 1, 2)...))
	if got := projectOf(saved); got != "41 P-41" {
		t.Errorf("the other project's work dropped: %s, want 41 P-41", got)
	}
	saved = putDoc(t, c, two.ID, sourcedBody(saved, customerAcme, linesNaming(acrossTwo())...))
	if got := projectOf(saved); got != "-" {
		t.Errorf("every source dropped: %s, want none", got)
	}

	plain := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	body := sourcedBody(plain, customerAcme, line("A", 1, 100, vat25))
	if got := projectOf(putDoc(t, c, plain.ID, body)); got != "-" {
		t.Errorf("a draft without work: %s, want none", got)
	}
}

// Work from another project arriving on a draft that had one — as a later
// append from the uninvoiced view brings it — clears the project at the next
// save.
func TestProject_ALaterLineFromAnotherProjectClearsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithProjects(newFakeProjects()))
	c := creator(t, h)
	d := sourcedDraft(t, h)
	saved := putDoc(t, c, d.ID, sourcedBody(d, customerAcme, theSameLines()...))
	if got := projectOf(saved); got != "41 P-41" {
		t.Fatalf("before: %s, want 41 P-41", got)
	}

	const otherMilestone = 702
	plantSource(t, h, d.ID, planted{position: 3, kind: "projects.milestone", id: otherMilestone, revision: 1,
		quantity: "1", amount: "500", date: "2026-09-06", project: project42})
	saved = putDoc(t, c, d.ID, sourcedBody(saved, customerAcme,
		hoursLine(7.5, 1200, refHourOne, refHourTwo),
		sourcedLine(line("Kjøregodtgjørelse", 1, 450, vat25), []refJSON{refMileage}),
		sourcedLine(line("Milepæl", 1, 10000, vat25), []refJSON{refMilestone, ref("projects.milestone", otherMilestone)})))
	if got := projectOf(saved); got != "-" {
		t.Errorf("after work from project 42 arrived: %s, want none", got)
	}
}

// The code is read through the project directory before the save's
// transaction — a contract call, never made under the lock — and only when
// the work the save keeps belongs to one project the draft does not already
// name: a later save of the same work keeps the stored snapshot and reads
// nothing, even when the project's code has changed since.
func TestProject_TheReferenceIsReadBeforeTheLockAndOnlyWhenNeeded(t *testing.T) {
	t.Parallel()
	projects := newFakeProjects()
	h := newHarness(t, modtest.WithProjects(projects))
	c, userID := h.SignInUser(t, "invoices:access", "invoices:create")
	d := sourcedDraft(t, h)

	saved := putDoc(t, c, d.ID, sourcedBody(d, customerAcme, theSameLines()...))
	if n, locked := projectCalls(userID); n != 1 || locked {
		t.Errorf("first save: %d project reads, locked %v; want one, before the lock", n, locked)
	}
	projects.edit(project41, func(p *contracts.ProjectEntry) { p.Code = "P-41-NY" })
	saved = putDoc(t, c, d.ID, sourcedBody(saved, customerAcme, theSameLines()...))
	if n, _ := projectCalls(userID); n != 1 {
		t.Errorf("second save of the same work: %d project reads in all, want still one", n)
	}
	if got := projectOf(saved); got != "41 P-41" {
		t.Errorf("second save: %s, want the snapshot 41 P-41 kept", got)
	}

	// A draft without work reads no project at all.
	plain := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	putDoc(t, c, plain.ID, sourcedBody(plain, customerAcme, line("A", 1, 100, vat25)))
	if n, _ := projectCalls(userID); n != 1 {
		t.Errorf("a draft without work: %d project reads in all, want still one", n)
	}
}

// With Projects switched off, or the project gone from the directory, there
// is no code to snapshot and the document names no project (ck_invoices_project
// keeps the two together).
func TestProject_WithoutTheDirectoryOrTheProjectNone(t *testing.T) {
	t.Parallel()
	off := newHarness(t)
	d := sourcedDraft(t, off)
	if got := projectOf(putDoc(t, creator(t, off), d.ID, sourcedBody(d, customerAcme, theSameLines()...))); got != "-" {
		t.Errorf("Projects switched off: %s, want none", got)
	}

	projects := newFakeProjects()
	projects.drop(project41)
	gone := newHarness(t, modtest.WithProjects(projects))
	d = sourcedDraft(t, gone)
	if got := projectOf(putDoc(t, creator(t, gone), d.ID, sourcedBody(d, customerAcme, theSameLines()...))); got != "-" {
		t.Errorf("the project gone: %s, want none", got)
	}
}

// A request never writes the project: the fields are answers, and one sent
// in a body is ignored on a create and on a replace.
func TestProject_NeverWrittenByARequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithProjects(newFakeProjects()))
	c := creator(t, h)
	body := draftBody(customerAcme, line("A", 1, 100, vat25))
	body["projectId"], body["projectReference"] = project42, "P-42"
	res := c.Do(http.MethodPost, invoicesPath, body)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s, want 201", res.Status, res.Body)
	}
	var plain invoiceJSON
	res.JSON(&plain)
	if got := projectOf(plain); got != "-" {
		t.Errorf("create: %s, want none", got)
	}

	d := sourcedDraft(t, h)
	put := sourcedBody(d, customerAcme, theSameLines()...)
	put["projectId"], put["projectReference"] = project42, "P-42"
	if got := projectOf(putDoc(t, c, d.ID, put)); got != "41 P-41" {
		t.Errorf("replace: %s, want the derived 41 P-41", got)
	}
}

// The issue freezes the project with the rest of the row: what the draft
// took is what the invoice keeps, whatever the directory says since, and
// the trigger refuses a change in SQL.
func TestProject_SnapshotFrozenAtIssue(t *testing.T) {
	t.Parallel()
	projects := newFakeProjects()
	h := workReady(t, newFakeHolders(), projects)
	d := sourcedDraft(t, h)
	putDoc(t, creator(t, h), d.ID, sourcedBody(d, customerAcme, theSameLines()...))
	projects.edit(project41, func(p *contracts.ProjectEntry) { p.Code = "P-41-NY" })

	inv := issued(t, h, d.ID)
	if got := projectOf(inv); got != "41 P-41" {
		t.Errorf("issued: %s, want 41 P-41", got)
	}
	for _, sql := range []string{
		`UPDATE invoices.invoices SET project_reference = 'P-41-NY' WHERE id = $1`,
		`UPDATE invoices.invoices SET project_id = NULL, project_reference = NULL WHERE id = $1`,
	} {
		_, err := h.Pool().Exec(context.Background(), sql, inv.ID)
		if err == nil || !strings.Contains(err.Error(), "invoices: issued document is immutable") {
			t.Errorf("%s: %v, want the immutability refusal", sql, err)
		}
	}
}

// A credit note copies its original's project and keeps it through its own
// saves — it holds no work to derive one from — and its issue.
func TestProject_ACreditNoteCopiesItsOriginals(t *testing.T) {
	t.Parallel()
	h := workReady(t, newFakeHolders(), newFakeProjects())
	d := sourcedDraft(t, h)
	putDoc(t, creator(t, h), d.ID, sourcedBody(d, customerAcme, theSameLines()...))
	original := issued(t, h, d.ID)

	cn := creditDraft(t, h, original.ID)
	if got := projectOf(cn); got != "41 P-41" {
		t.Errorf("credit-note draft: %s, want its original's 41 P-41", got)
	}
	lowered := creditLine(cn.Lines[0])
	lowered["quantity"] = 1
	cn = saveCredit(t, h, cn, creditBody(cn, lowered))
	if got := projectOf(cn); got != "41 P-41" {
		t.Errorf("credit-note draft after a save: %s, want 41 P-41 kept", got)
	}
	if got := projectOf(issued(t, h, cn.ID)); got != "41 P-41" {
		t.Errorf("issued credit note: %s, want 41 P-41", got)
	}

	plain := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	if got := projectOf(creditDraft(t, h, plain.ID)); got != "-" {
		t.Errorf("a credit note of an invoice without a project: %s, want none", got)
	}
}
