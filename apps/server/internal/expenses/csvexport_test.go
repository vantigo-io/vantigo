package expenses_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/expenses"
)

// This file is the payroll export: the one answer this module gives that is
// not JSON. Its bytes are a contract with a spreadsheet rather than with a
// client, so they are pinned exactly — separator, decimal comma, line ends,
// the byte order mark and the guard against a description that a spreadsheet
// would otherwise run as a formula.

// csvBOM is the UTF-8 byte order mark the file opens with — the one thing
// that makes a Norwegian Excel read "Anna Ås" rather than "Anna Ã…s".
const csvBOM = "\xef\xbb\xbf"

// nameUser gives a seeded user a display name of its own, so the export's
// bytes can be pinned: modtest names a user after its generated email.
func nameUser(t *testing.T, h *harness, id uuid.UUID, name string) {
	t.Helper()
	h.Exec(t, `UPDATE identity.users SET display_name = $1 WHERE id = $2`, name, id)
}

// TestExpensesReimbursementsExport_IsExactlyTheseBytes is the golden sample:
// two expenses of one person, one of them with a description a spreadsheet
// would treat as a formula and which carries the separator, a quote and a line
// break as well.
func TestExpensesReimbursementsExport_IsExactlyTheseBytes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	anna, annaID := signInAs(t, h, projectKraftVerket, roleMember)
	nameUser(t, h, annaID, "Anna Ås")

	trip := createEntry(t, anna, mileageBody(map[string]any{
		"entryDate": "2026-03-01", "description": "Til anlegget",
	}))
	receipt := createEntry(t, anna, outlayBody(map[string]any{
		"entryDate":   "2026-03-10",
		"description": "=SUM(A1);\"farlig\"\nlinje to",
		"vatAmount":   250.00,
		"projectId":   projectKraftVerket,
	}))
	approvedBy(t, anna, boss, trip.Id, receipt.Id)

	want := csvBOM + strings.Join([]string{
		"Employee;User id;Date;Kind;Description;Category;Currency;Gross;VAT;Owed;Project code",
		fmt.Sprintf("Anna Ås;%s;2026-03-01;mileage;Til anlegget;;NOK;636,00;;636,00;", annaID),
		fmt.Sprintf("Anna Ås;%s;2026-03-10;outlay;\"'=SUM(A1);\"\"farlig\"\"\nlinje to\";Materials;NOK;1250,00;250,00;1250,00;KVEM1000", annaID),
		"",
	}, "\r\n")

	got := string(exportCSV(t, boss, "").Body)
	if got != want {
		t.Errorf("the export is\n%q\nwant\n%q", got, want)
	}
}

// TestExpensesReimbursementsExport_IsServedAsAFileNobodyCaches pins the three
// headers a browser needs to save the file rather than show it.
func TestExpensesReimbursementsExport_IsServedAsAFileNobodyCaches(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)
	entry := createEntry(t, owner, outlayBody(nil))
	approvedBy(t, owner, boss, entry.Id)

	r := exportCSV(t, boss, "")
	for header, want := range map[string]string{
		"Content-Type":        "text/csv; charset=utf-8",
		"Content-Disposition": `attachment; filename="expenses-reimbursements-2026-09-12.csv"`,
		"Cache-Control":       "private, no-store",
	} {
		if got := r.Header(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

// TestExpensesReimbursementsExport_TakesTheListsFiltersOrExplicitIds: the file
// is whatever the clerk is looking at, or exactly the expenses they picked.
func TestExpensesReimbursementsExport_TakesTheListsFiltersOrExplicitIds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	anna, annaID := signIn(t, h)
	bjorn, _ := signIn(t, h)
	nameUser(t, h, annaID, "Anna Ås")

	annas := createEntry(t, anna, outlayBody(map[string]any{"description": "Anna sin"}))
	bjorns := createEntry(t, bjorn, outlayBody(map[string]any{"description": "Bjørn sin"}))
	approvedBy(t, anna, boss, annas.Id)
	approvedBy(t, bjorn, boss, bjorns.Id)

	all := string(exportCSV(t, boss, "").Body)
	if !strings.Contains(all, "Anna sin") || !strings.Contains(all, "Bjørn sin") {
		t.Errorf("the whole export = %q, want both expenses", all)
	}
	one := string(exportCSV(t, boss, "?userId="+annaID.String()).Body)
	if !strings.Contains(one, "Anna sin") || strings.Contains(one, "Bjørn sin") {
		t.Errorf("the filtered export = %q, want Anna's alone", one)
	}
	picked := string(exportCSV(t, boss, fmt.Sprintf("?entryIds=%d", bjorns.Id)).Body)
	if !strings.Contains(picked, "Bjørn sin") || strings.Contains(picked, "Anna sin") {
		t.Errorf("the picked export = %q, want the one id alone", picked)
	}

	// An export of what has already been paid, so a clerk can reproduce the
	// file they sent to payroll.
	markReimbursed(t, boss, reimbursedBody([]int64{annas.Id}, nil))
	done := string(exportCSV(t, boss, "?state=reimbursed").Body)
	if !strings.Contains(done, "Anna sin") || strings.Contains(done, "Bjørn sin") {
		t.Errorf("the reimbursed export = %q, want the paid one alone", done)
	}
}

// TestExpensesReimbursementsExport_RefusesWhatItCannotExport: an id that is
// not in the export's own set is named rather than quietly left out — a
// payroll file that is missing a line nobody was told about is worse than no
// file.
func TestExpensesReimbursementsExport_RefusesWhatItCannotExport(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)
	draft := createEntry(t, owner, outlayBody(nil))
	company := createEntry(t, owner, companyPaid(nil))
	approvedBy(t, owner, boss, company.Id)

	errs := refusedExport(t, boss, fmt.Sprintf("?entryIds=%d&entryIds=%d&entryIds=90210",
		draft.Id, company.Id))
	for _, want := range []string{
		fmt.Sprintf("Expense %d cannot be exported", draft.Id),
		fmt.Sprintf("Expense %d cannot be exported", company.Id),
		"Expense 90210 was not found",
	} {
		if !mentions(errs["entryIds"], want) {
			t.Errorf("errors %v do not say %q", errs["entryIds"], want)
		}
	}

	bad := refusedExport(t, boss, "?state=paid")
	if len(bad["state"]) == 0 {
		t.Errorf("errors = %v, want one on state", bad)
	}
}

// TestExpensesReimbursementsExport_IsBoundedAndSaysSo: a payroll file is read
// by a person, and an unbounded one is a way to pull the whole table through
// one request. It is deliberately not paged — half a payroll file is worse
// than none — so the bound is a refusal that asks for a narrower filter.
//
// It is not parallel: it moves the package's own cap for the length of the
// test rather than recording five thousand expenses.
func TestExpensesReimbursementsExport_IsBoundedAndSaysSo(t *testing.T) {
	restore := expenses.SetExportMaxRows(1)
	t.Cleanup(restore)

	h := newHarness(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, _ := signIn(t, h)
	first := createEntry(t, owner, outlayBody(nil))
	second := createEntry(t, owner, outlayBody(map[string]any{"description": "Skruer"}))
	approvedBy(t, owner, boss, first.Id, second.Id)

	r := boss.Do(http.MethodGet, reimbursementsExportPath, nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("an export over the cap: status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if problem.Title != "Too many rows to export" {
		t.Errorf("problem title = %q, want the row cap's", problem.Title)
	}
	// One row still fits.
	if body := string(exportCSV(t, boss, fmt.Sprintf("?entryIds=%d", first.Id)).Body); !strings.Contains(body, "Kabel") {
		t.Errorf("an export inside the cap = %q, want the expense", body)
	}
}

// TestExpensesReimbursementsExport_WithoutProjects_LeavesTheProjectColumnEmpty:
// the column stays, because a payroll system reading the file must not have to
// know which modules an installation runs.
func TestExpensesReimbursementsExport_WithoutProjects_LeavesTheProjectColumnEmpty(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	boss, _ := signIn(t, h, "expenses:approve", "expenses:manage")
	owner, ownerID := signIn(t, h)
	nameUser(t, h, ownerID, "Ola Nordmann")
	entry := createEntry(t, owner, outlayBody(nil))
	approvedBy(t, owner, boss, entry.Id)

	got := string(exportCSV(t, boss, "").Body)
	want := csvBOM + strings.Join([]string{
		"Employee;User id;Date;Kind;Description;Category;Currency;Gross;VAT;Owed;Project code",
		fmt.Sprintf("Ola Nordmann;%s;2026-03-10;outlay;Kabel og kontakter;Materials;NOK;1250,00;;1250,00;", ownerID),
		"",
	}, "\r\n")
	if got != want {
		t.Errorf("the export is\n%q\nwant\n%q", got, want)
	}
}
