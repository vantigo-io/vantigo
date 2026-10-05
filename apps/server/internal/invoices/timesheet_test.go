package invoices_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The timesheet (invoices work design D5): the held hours of an invoice
// printed inside its PDF — written by the wizard, by a save that turns it on
// and by a refresh, pruned by every save, deleted when turned off, frozen at
// the issue; each person labelled by the settings' label; never the time
// entry's note. Built on the work fixture (work_test.go): Kari's 4 h on
// 2026-09-01 and Ola's 3.5 h on 2026-09-02 on project 41, Kari's 2 h on
// 2026-09-03 on project 42.

// tsDocJSON is the part of a document the timesheet tests read.
type tsDocJSON struct {
	ID            int64       `json:"id"`
	Status        string      `json:"status"`
	Revision      int32       `json:"revision"`
	Timesheet     bool        `json:"timesheet"`
	TimesheetRows []tsRowJSON `json:"timesheetRows"`
	Warnings      []string    `json:"warnings"`
	Lines         []struct {
		Description string       `json:"description"`
		Quantity    float64      `json:"quantity"`
		Unit        string       `json:"unit"`
		UnitPrice   float64      `json:"unitPrice"`
		VatCodeID   int32        `json:"vatCodeId"`
		Sources     []sourceJSON `json:"sources"`
	} `json:"lines"`
}

// tsRowJSON is one timesheet row on the wire.
type tsRowJSON struct {
	Position    int32   `json:"position"`
	PersonLabel string  `json:"personLabel"`
	Date        string  `json:"date"`
	Hours       float64 `json:"hours"`
	WorkType    *string `json:"workType"`
	Description string  `json:"description"`
}

// rowText is a row as "position label date hours work type | description".
func rowText(r tsRowJSON) string {
	work := "-"
	if r.WorkType != nil {
		work = *r.WorkType
	}
	return fmt.Sprintf("%d %s %s %g %s | %s", r.Position, r.PersonLabel, r.Date, r.Hours, work, r.Description)
}

func rowTexts(rows []tsRowJSON) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, rowText(r))
	}
	return out
}

// storedRows is a document's timesheet rows as stored, in the same text.
func storedRows(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return modtest.One[[]string](t, h.Harness, `
		SELECT coalesce(array_agg(format('%s %s %s %s %s | %s', position, person_label, entry_date, hours::float8,
			coalesce(work_type, '-'), description) ORDER BY position), '{}')
		FROM invoices.timesheet_rows WHERE invoice_id = $1`, id)
}

func tsDoc(t *testing.T, res *modtest.Response, want int) tsDocJSON {
	t.Helper()
	if res.Status != want {
		t.Fatalf("%d %s, want %d", res.Status, res.Body, want)
	}
	var d tsDocJSON
	res.JSON(&d)
	return d
}

// tsFromWork makes a draft of sources through the wizard, with timesheet as
// given (nil: left out).
func tsFromWork(t *testing.T, h *harness, timesheet *bool, sources ...map[string]any) tsDocJSON {
	t.Helper()
	body := fromWorkBody(customerAcme, sources...)
	if timesheet != nil {
		body["timesheet"] = *timesheet
	}
	return tsDoc(t, creator(t, h).Do(http.MethodPost, fromWorkPath, body), http.StatusCreated)
}

// keepBody is a replace of d keeping every line and its sources as read.
func keepBody(d tsDocJSON) map[string]any {
	lines := []map[string]any{}
	for _, l := range d.Lines {
		refs := []refJSON{}
		for _, s := range l.Sources {
			refs = append(refs, ref(s.Kind, s.ID))
		}
		lines = append(lines, map[string]any{"description": l.Description, "quantity": l.Quantity, "unit": l.Unit,
			"unitPrice": l.UnitPrice, "vatCodeId": l.VatCodeID, "sources": refs})
	}
	return map[string]any{"customerId": customerAcme, "deliveryDate": "2026-09-10", "paymentTermsDays": 30,
		"revision": d.Revision, "lines": lines}
}

func tsPut(t *testing.T, h *harness, d tsDocJSON, body map[string]any) tsDocJSON {
	t.Helper()
	return tsDoc(t, creator(t, h).Do(http.MethodPut, invoicePath(d.ID), body), http.StatusOK)
}

// timesheetSettings saves the complete seller with the timesheet's default
// and label.
func timesheetSettings(t *testing.T, h *harness, on bool, label string) {
	t.Helper()
	var current settingsJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&current)
	body := completeSeller(current.Revision)
	body["timesheetDefault"], body["timesheetPersonLabel"] = on, label
	saveSeller(t, h, body)
}

var (
	withSheet    = ptrTo(true)
	withoutSheet = ptrTo(false)
)

// The two hours of project 41 and its outlay.
func project41Work() []map[string]any {
	return []map[string]any{
		workSrc("time.entry", workHourKari, 2), workSrc("time.entry", workHourOla, 1), workSrc("expenses.entry", workOutlay, 4),
	}
}

// Rows exist only while the flag is on: the wizard writes none without it —
// the settings' default off — and one per held hour with it, never one for an
// expense; the settings' default turns it on when the request leaves it out,
// and the request's false wins over the default; a draft created by hand
// takes the default too.
func TestTimesheet_RowsOnlyWithTheFlag(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	h := f.h

	without := tsFromWork(t, h, nil, project41Work()...)
	if without.Timesheet || len(without.TimesheetRows) != 0 || len(storedRows(t, h, without.ID)) != 0 {
		t.Errorf("without the flag: timesheet %v rows %v, want off and none", without.Timesheet, without.TimesheetRows)
	}
	h.Exec(t, `DELETE FROM invoices.invoices WHERE id = $1`, without.ID)

	with := tsFromWork(t, h, withSheet, project41Work()...)
	want := []string{"1 KN 2026-09-01 4 - | Project 41", "2 OH 2026-09-02 3.5 - | Project 41"}
	if !with.Timesheet || !slices.Equal(rowTexts(with.TimesheetRows), want) {
		t.Errorf("with the flag: timesheet %v rows %v, want on and %v", with.Timesheet, rowTexts(with.TimesheetRows), want)
	}
	if got := storedRows(t, h, with.ID); !slices.Equal(got, want) {
		t.Errorf("stored = %v, want %v", got, want)
	}
	h.Exec(t, `DELETE FROM invoices.invoices WHERE id = $1`, with.ID)

	timesheetSettings(t, h, true, "initials")
	byDefault := tsFromWork(t, h, nil, project41Work()...)
	if !byDefault.Timesheet || len(byDefault.TimesheetRows) != 2 {
		t.Errorf("the default on: timesheet %v rows %v, want on and two", byDefault.Timesheet, byDefault.TimesheetRows)
	}
	h.Exec(t, `DELETE FROM invoices.invoices WHERE id = $1`, byDefault.ID)
	refused := tsFromWork(t, h, withoutSheet, project41Work()...)
	if refused.Timesheet || len(storedRows(t, h, refused.ID)) != 0 {
		t.Errorf("false over the default on: timesheet %v rows %v, want off and none", refused.Timesheet, storedRows(t, h, refused.ID))
	}
	byHand := tsDoc(t, creator(t, h).Do(http.MethodPost, invoicesPath, draftBody(customerAcme, line("A", 1, 100, vat25))), http.StatusCreated)
	if !byHand.Timesheet || len(byHand.TimesheetRows) != 0 {
		t.Errorf("a draft by hand with the default on: timesheet %v rows %v, want on and none", byHand.Timesheet, byHand.TimesheetRows)
	}

	// An append keeps the target's flag unless it says; turning it on there
	// writes the rows of every hour the draft then holds, its own and the new.
	appendTo := func(d tsDocJSON, timesheet *bool, sources ...map[string]any) tsDocJSON {
		t.Helper()
		body := fromWorkBody(customerAcme, sources...)
		body["invoiceId"], body["revision"] = d.ID, d.Revision
		if timesheet != nil {
			body["timesheet"] = *timesheet
		}
		return tsDoc(t, creator(t, h).Do(http.MethodPost, fromWorkPath, body), http.StatusOK)
	}
	added := appendTo(refused, nil, workSrc("expenses.entry", workMileage, 1))
	if added.Timesheet || len(added.TimesheetRows) != 0 {
		t.Errorf("an append leaving the flag out: timesheet %v rows %v, want the target's off", added.Timesheet, added.TimesheetRows)
	}
	added = appendTo(added, withSheet, workSrc("time.entry", workHour42, 1))
	if got, want := rowTexts(added.TimesheetRows), []string{
		"1 KN 2026-09-01 4 - | Project 41", "2 OH 2026-09-02 3.5 - | Project 41", "3 KN 2026-09-03 2 - | Project 42",
	}; !added.Timesheet || !slices.Equal(got, want) {
		t.Errorf("an append turning it on: timesheet %v rows %v, want on and %v", added.Timesheet, got, want)
	}
}

// Every save prunes the rows to the hours the draft still holds: an hour
// dropped from its line loses its row, the others keep theirs — labels and
// positions as written — and a customer change, which drops every hold,
// empties the timesheet.
func TestTimesheet_PrunedOnSave(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	h := f.h
	d := tsFromWork(t, h, withSheet, project41Work()...)

	same := tsPut(t, h, d, keepBody(d))
	if got := rowTexts(same.TimesheetRows); !slices.Equal(got, rowTexts(d.TimesheetRows)) {
		t.Errorf("a save keeping the work = %v, want %v", got, rowTexts(d.TimesheetRows))
	}
	body := keepBody(same)
	for _, l := range body["lines"].([]map[string]any) {
		var kept []refJSON
		for _, r := range l["sources"].([]refJSON) {
			if r.ID != workHourKari {
				kept = append(kept, r)
			}
		}
		l["sources"] = append([]refJSON{}, kept...)
	}
	pruned := tsPut(t, h, same, body)
	if got, want := rowTexts(pruned.TimesheetRows), []string{"2 OH 2026-09-02 3.5 - | Project 41"}; !pruned.Timesheet || !slices.Equal(got, want) {
		t.Errorf("Kari's hour dropped: rows %v, want %v", got, want)
	}

	moved := keepBody(pruned)
	moved["customerId"] = customerNoTerms
	for _, l := range moved["lines"].([]map[string]any) {
		l["sources"] = []refJSON{}
	}
	if gone := tsPut(t, h, pruned, moved); len(gone.TimesheetRows) != 0 || len(storedRows(t, h, d.ID)) != 0 {
		t.Errorf("after a customer change: rows %v, want none", storedRows(t, h, d.ID))
	}
}

// A save turning the flag on reads the held hours through BillableHours by
// id and their people through the user directory — both on the pool, before
// the save's transaction — and writes the rows; one leaving the field out
// keeps the flag and the rows; one turning it off deletes them. A
// credit-note draft takes no timesheet.
func TestTimesheet_WrittenWhenTurnedOnAndDeletedWhenOff(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	h := f.h
	d := tsFromWork(t, h, nil, project41Work()...)
	before := len(f.billable.reads())

	c, caller := h.SignInUser(t, "invoices:access", "invoices:create")
	body := keepBody(d)
	body["timesheet"] = true
	turned := tsDoc(t, c.Do(http.MethodPut, invoicePath(d.ID), body), http.StatusOK)
	want := []string{"1 KN 2026-09-01 4 - | Project 41", "2 OH 2026-09-02 3.5 - | Project 41"}
	if !turned.Timesheet || !slices.Equal(rowTexts(turned.TimesheetRows), want) {
		t.Errorf("turned on: timesheet %v rows %v, want on and %v", turned.Timesheet, rowTexts(turned.TimesheetRows), want)
	}
	reads := f.billable.reads()[before:]
	if len(reads) != 1 || reads[0].method != "BillableHours" || !slices.Equal(reads[0].ids, []int64{workHourKari, workHourOla}) || reads[0].locked {
		t.Errorf("reads = %+v, want BillableHours of the two held hours, by id, off the lock", reads)
	}
	var named bool
	for _, call := range contractCalls.by(caller) {
		if call.locked {
			t.Errorf("%s made under the save's lock", call.method)
		}
		named = named || call.method == "Users.Users"
	}
	if !named {
		t.Error("the people were not named through the user directory")
	}

	kept := tsPut(t, h, turned, keepBody(turned))
	if !kept.Timesheet || !slices.Equal(rowTexts(kept.TimesheetRows), want) {
		t.Errorf("the field left out: timesheet %v rows %v, want both kept", kept.Timesheet, rowTexts(kept.TimesheetRows))
	}
	if n := len(f.billable.reads()) - before; n != 1 {
		t.Errorf("%d billable reads since the flag was turned on, want only that save's one", n)
	}

	body = keepBody(kept)
	body["timesheet"] = false
	if gone := tsPut(t, h, kept, body); gone.Timesheet || len(gone.TimesheetRows) != 0 || len(storedRows(t, h, d.ID)) != 0 {
		t.Errorf("turned off: timesheet %v rows %v, want off and none", gone.Timesheet, storedRows(t, h, d.ID))
	}

	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	credit := creditDraft(t, h, original.ID)
	res := creator(t, h).Do(http.MethodPut, invoicePath(credit.ID), map[string]any{
		"customerId": customerAcme, "revision": credit.Revision, "timesheet": true,
		"lines": []map[string]any{{"description": "A", "quantity": 1, "unit": "timer", "unitPrice": 100, "vatCodeId": vat25,
			"creditsLineId": credit.Lines[0].CreditsLineID}},
	})
	if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["timesheet"]) == 0 {
		t.Errorf("a credit note's timesheet = %d %s, want 400 on timesheet", res.Status, res.Body)
	}
}

// A refresh writes the rows of the refreshed hours from their current facts
// and labels every row afresh with the settings' label as it is now: a
// timesheet never carries two numbering schemes.
func TestTimesheet_RegeneratedByRefreshSources(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	h := f.h
	timesheetSettings(t, h, false, "number")
	d := tsFromWork(t, h, withSheet, project41Work()...)
	if got := rowTexts(d.TimesheetRows); !slices.Equal(got, []string{
		"1 Person 1 2026-09-01 4 - | Project 41", "2 Person 2 2026-09-02 3.5 - | Project 41",
	}) {
		t.Fatalf("numbered = %v", got)
	}

	timesheetSettings(t, h, false, "initials")
	hour := f.billable.hours[workHourKari]
	hour.Revision, hour.HoursHundredths, hour.Amount, hour.TaskTitle = 3, 425, "5100.00000000", "Workshop"
	f.billable.putHour(hour)
	body := keepBody(d)
	body["refreshSources"] = true
	refreshed := tsPut(t, h, d, body)
	want := []string{"1 KN 2026-09-01 4.25 - | Workshop", "2 OH 2026-09-02 3.5 - | Project 41"}
	if got := rowTexts(refreshed.TimesheetRows); !slices.Equal(got, want) {
		t.Errorf("after the refresh = %v, want %v", got, want)
	}
	if got := storedRows(t, h, d.ID); strings.Contains(strings.Join(got, " "), "Person") {
		t.Errorf("stored = %v: a row kept the old numbering", got)
	}
}

// Each label: initials by default — Kari Nordmann KN, Knut Nilsen a second KN
// and so KN2, Ola Hansen OH — number in order of first appearance on the
// timesheet, and name. A person the user directory does not know is "?".
func TestTimesheet_EachPersonLabelAndTheInitialsCollision(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	h := f.h
	knut := namedUser(t, h, "Knut Nilsen")
	f.billable.putHour(contracts.BillableHour{ID: 804, Revision: 1, ProjectID: project41, UserID: knut, Date: wDay("2026-09-01"),
		HoursHundredths: 150, BillRate: "1200.00", Currency: "NOK", Amount: "1800.00000000", WorkTypeName: "Rådgivning"})
	f.billable.putHour(contracts.BillableHour{ID: 805, Revision: 1, ProjectID: project41, UserID: uuid.New(), Date: wDay("2026-09-04"),
		HoursHundredths: 100, BillRate: "1200.00", Currency: "NOK", Amount: "1200.00000000"})
	sources := append(project41Work(), workSrc("time.entry", 804, 1), workSrc("time.entry", 805, 1))

	for _, c := range []struct {
		label string
		want  []string
	}{
		{"initials", []string{"KN", "KN2", "OH", "?"}},
		{"number", []string{"Person 1", "Person 2", "Person 3", "Person 4"}},
		{"name", []string{"Kari Nordmann", "Knut Nilsen", "Ola Hansen", "?"}},
	} {
		timesheetSettings(t, h, true, c.label)
		d := tsFromWork(t, h, nil, sources...)
		var got []string
		for _, r := range d.TimesheetRows {
			got = append(got, r.PersonLabel)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: labels %v, want %v (rows %v)", c.label, got, c.want, rowTexts(d.TimesheetRows))
		}
		if w := d.TimesheetRows[1].WorkType; w == nil || *w != "Rådgivning" {
			t.Errorf("%s: Knut's work type = %v, want Rådgivning", c.label, d.TimesheetRows[1].WorkType)
		}
		h.Exec(t, `DELETE FROM invoices.invoices WHERE id = $1`, d.ID)
	}
}

// The note never reaches a timesheet: the billable read carries no field
// for it — contracts.BillableHour is the only road an hour takes into this
// module — and a row's description is the task title, else the project's
// name, nothing else, in the rows, the PDF's model and the export.
func TestTimesheet_NeverTheNote(t *testing.T) {
	ty := reflect.TypeFor[contracts.BillableHour]()
	for i := range ty.NumField() {
		if name := strings.ToLower(ty.Field(i).Name); strings.Contains(name, "note") || strings.Contains(name, "comment") {
			t.Fatalf("contracts.BillableHour carries %s: a time entry's note must never reach the timesheet", ty.Field(i).Name)
		}
	}
	f := newWorkFixture(t)
	h := f.h
	hour := f.billable.hours[workHourOla]
	hour.TaskTitle, hour.WorkTypeName = "Workshop", "Rådgivning"
	f.billable.putHour(hour)
	d := tsFromWork(t, h, withSheet, project41Work()...)
	want := []string{"1 KN 2026-09-01 4 - | Project 41", "2 OH 2026-09-02 3.5 Rådgivning | Workshop"}
	if got := rowTexts(d.TimesheetRows); !slices.Equal(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
	var mu sync.Mutex
	var sheet *invoices.PDFTimesheet
	defer invoices.SetPDFModelBuilt(func(id int64, m invoices.PDFModel) {
		if id == d.ID {
			mu.Lock()
			defer mu.Unlock()
			sheet = m.Timesheet
		}
	})()
	if res := creator(t, h).Do(http.MethodGet, previewPath(d.ID), nil); res.Status != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Status, res.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if sheet == nil || len(sheet.Rows) != 2 || sheet.Rows[0][3] != "Project 41" || sheet.Rows[1][3] != "Workshop" {
		t.Errorf("the PDF's timesheet = %+v, want the task title, else the project's name", sheet)
	}
	raw := exportOf(t, h, customerAcme)
	if !strings.Contains(raw, `"description":"Project 41"`) || !strings.Contains(raw, `"description":"Workshop"`) {
		t.Errorf("export = %s, want the two descriptions as printed", raw)
	}
}

// exportOf is the customer's personal-data export as JSON.
func exportOf(t *testing.T, h *harness, customer int32) string {
	t.Helper()
	section, err := invoices.Module().CustomerPersonalData(disabledDeps(h)).ExportCustomerData(context.Background(), customer)
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}
	raw, _ := json.Marshal(section)
	return string(raw)
}

// The rows are frozen with the document at its issue: the trigger refuses any
// write of them and of the flag; a save is invoice_issued; and nothing the
// user directory or the settings later say changes them — identity renaming
// and disabling a user never touches the snapshot.
func TestTimesheet_FrozenAtIssue(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t, newFakeHolders().options()...)
	h := f.h
	d := tsFromWork(t, h, withSheet, project41Work()...)
	inv := issued(t, h, d.ID)
	want := []string{"1 KN 2026-09-01 4 - | Project 41", "2 OH 2026-09-02 3.5 - | Project 41"}
	if got := storedRows(t, h, inv.ID); !slices.Equal(got, want) {
		t.Fatalf("issued with %v, want %v", got, want)
	}

	ctx := context.Background()
	for _, sql := range []string{
		`UPDATE invoices.timesheet_rows SET person_label = 'XX' WHERE invoice_id = $1`,
		`DELETE FROM invoices.timesheet_rows WHERE invoice_id = $1`,
		`INSERT INTO invoices.timesheet_rows (invoice_id, position, source_id, person_label, entry_date, hours, description)
			VALUES ($1, 9, 1, 'XX', '2026-09-01', 1, 'x')`,
		`UPDATE invoices.invoices SET timesheet = false WHERE id = $1`,
	} {
		if _, err := h.Pool().Exec(ctx, sql, inv.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Errorf("%s = %v, want refused as immutable", strings.Fields(sql)[0], err)
		}
	}
	body := keepBody(d)
	body["revision"], body["timesheet"] = inv.Revision, false
	if res := creator(t, h).Do(http.MethodPut, invoicePath(inv.ID), body); res.Status != http.StatusConflict {
		t.Errorf("a save of the issued invoice = %d %s, want 409", res.Status, res.Body)
	}

	h.Exec(t, `UPDATE identity.users SET display_name = 'Omdøpt Person', is_disabled = true WHERE id = $1`, f.kari)
	timesheetSettings(t, h, false, "name")
	var after tsDocJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicePath(inv.ID), nil).JSON(&after)
	if got := rowTexts(after.TimesheetRows); !after.Timesheet || !slices.Equal(got, want) {
		t.Errorf("after identity and the settings changed: %v, want %v", got, want)
	}
}

// pdfTimesheets records the timesheet block of every model built for id.
func pdfTimesheets(id int64) (restore func(), seen func() []*invoices.PDFTimesheet) {
	var mu sync.Mutex
	var all []*invoices.PDFTimesheet
	restore = invoices.SetPDFModelBuilt(func(got int64, m invoices.PDFModel) {
		if got != id {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		all = append(all, m.Timesheet)
	})
	return restore, func() []*invoices.PDFTimesheet {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(all)
	}
}

// The PDF's timesheet block: "Timeliste" on a Norwegian document and
// "Timesheet" on an English one, the columns date, person, work type,
// description and hours, one row per timesheet row, a total per person in
// order of first appearance and the whole. Not parallel: the hook is the
// package's.
func TestPDF_TheTimesheetBlock(t *testing.T) {
	f := newWorkFixture(t)
	h := f.h
	hour := f.billable.hours[workHourOla]
	hour.WorkTypeName = "Rådgivning"
	f.billable.putHour(hour)
	d := tsFromWork(t, h, withSheet, workSrc("time.entry", workHourKari, 2), workSrc("time.entry", workHourOla, 1),
		workSrc("time.entry", workHour42, 1))
	restore, seen := pdfTimesheets(d.ID)
	defer restore()

	if res := creator(t, h).Do(http.MethodGet, previewPath(d.ID), nil); res.Status != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Status, res.Body)
	}
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.Language = "en" })
	if res := creator(t, h).Do(http.MethodGet, previewPath(d.ID), nil); res.Status != http.StatusOK {
		t.Fatalf("preview in English = %d %s", res.Status, res.Body)
	}
	sheets := seen()
	if len(sheets) != 2 || sheets[0] == nil || sheets[1] == nil {
		t.Fatalf("timesheets = %+v, want one per preview", sheets)
	}
	nb, en := sheets[0], sheets[1]
	if nb.Title != "Timeliste" || !slices.Equal(nb.Header, []string{"Dato", "Person", "Arbeidstype", "Beskrivelse", "Timer"}) {
		t.Errorf("nb = %q %v", nb.Title, nb.Header)
	}
	wantRows := [][]string{
		{"01.09.2026", "KN", "", "Project 41", "4,00"},
		{"02.09.2026", "OH", "Rådgivning", "Project 41", "3,50"},
		{"03.09.2026", "KN", "", "Project 42", "2,00"},
	}
	if !slices.EqualFunc(nb.Rows, wantRows, slices.Equal) {
		t.Errorf("nb rows = %v, want %v", nb.Rows, wantRows)
	}
	if want := [][2]string{{"Sum KN", "6,00"}, {"Sum OH", "3,50"}, {"Sum timer", "9,50"}}; !slices.Equal(nb.Totals, want) {
		t.Errorf("nb totals = %v, want %v", nb.Totals, want)
	}
	if en.Title != "Timesheet" || !slices.Equal(en.Header, []string{"Date", "Person", "Work type", "Description", "Hours"}) ||
		en.Rows[0][0] != "2026-09-01" || en.Rows[0][4] != "4.00" {
		t.Errorf("en = %q %v %v", en.Title, en.Header, en.Rows)
	}
	if want := [][2]string{{"Total KN", "6.00"}, {"Total OH", "3.50"}, {"Total hours", "9.50"}}; !slices.Equal(en.Totals, want) {
		t.Errorf("en totals = %v, want %v", en.Totals, want)
	}
}

// The preview renders the draft's timesheet as it stands, and no block once
// the flag is off. Not parallel: the hook is the package's.
func TestPDF_ThePreviewCarriesIt(t *testing.T) {
	f := newWorkFixture(t)
	h := f.h
	d := tsFromWork(t, h, withSheet, project41Work()...)
	restore, seen := pdfTimesheets(d.ID)
	defer restore()
	preview := func() {
		t.Helper()
		if res := creator(t, h).Do(http.MethodGet, previewPath(d.ID), nil); res.Status != http.StatusOK || res.Header("Content-Type") != "application/pdf" {
			t.Fatalf("preview = %d %s", res.Status, res.Body)
		}
	}
	preview()
	body := keepBody(d)
	body["timesheet"] = false
	tsPut(t, h, d, body)
	preview()
	sheets := seen()
	if len(sheets) != 2 || sheets[0] == nil || len(sheets[0].Rows) != 2 || sheets[1] != nil {
		t.Errorf("previews' timesheets = %+v, want the two rows, then none", sheets)
	}
}

// The store-once rule is unchanged by the timesheet: the issue renders the
// model once, timesheet and all, keys the object by the bytes' own hash, and
// a download streams those bytes without rendering again. Not parallel: the
// hook is the package's.
func TestPDFStore_StoreOnceUnchanged(t *testing.T) {
	f := newWorkFixture(t, newFakeHolders().options()...)
	h := f.h
	d := tsFromWork(t, h, withSheet, project41Work()...)
	restore, seen := pdfTimesheets(d.ID)
	defer restore()
	inv := issued(t, h, d.ID)

	keys, puts, _ := h.objects.stored()
	if len(keys) != 1 || puts != 1 || !regexp.MustCompile(fmt.Sprintf(`^documents/%d/%d-[0-9a-f]{64}\.pdf$`, inv.ID, *inv.Number)).MatchString(keys[0]) {
		t.Fatalf("stored = %v after %d puts, want one documents/<id>/<number>-<sha256>.pdf", keys, puts)
	}
	hash := modtest.One[string](t, h.Harness, `SELECT pdf_sha256 FROM invoices.invoices WHERE id = $1`, inv.ID)
	if body := h.objects.object(keys[0]); sha(body) != hash || !strings.HasSuffix(keys[0], "-"+hash+".pdf") {
		t.Errorf("the object's hash %s, the row's %s, the key %s: want one hash", sha(body), hash, keys[0])
	}
	for range 2 {
		if res := download(t, h, inv.ID); res.Status != http.StatusOK || sha([]byte(res.Body)) != hash {
			t.Errorf("download = %d, hash %s; want the stored bytes", res.Status, sha([]byte(res.Body)))
		}
	}
	if sheets := seen(); len(sheets) != 1 || sheets[0] == nil || len(sheets[0].Rows) != 2 {
		t.Errorf("models built = %+v, want the issue's one, with its timesheet", sheets)
	}
}

// The customer's export carries each document's timesheet as printed —
// issued and draft alike, since the customer received or would receive it —
// and leaves the key out of a document without one.
func TestExport_TheTimesheet(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t, newFakeHolders().options()...)
	h := f.h
	issued(t, h, tsFromWork(t, h, withSheet, project41Work()...).ID)
	tsFromWork(t, h, withSheet, workSrc("time.entry", workHour42, 1))
	createDraft(t, h, draftBody(customerAcme, line("Uten timeliste", 1, 100, vat25)))

	raw := exportOf(t, h, customerAcme)
	for _, want := range []string{
		`"timesheet":[{"position":1,"personLabel":"KN","date":"2026-09-01","hours":"4.00","description":"Project 41"},` +
			`{"position":2,"personLabel":"OH","date":"2026-09-02","hours":"3.50","description":"Project 41"}]`,
		`"timesheet":[{"position":1,"personLabel":"KN","date":"2026-09-03","hours":"2.00","description":"Project 42"}]`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("export %s has no %s", raw, want)
		}
	}
	if n := strings.Count(raw, `"timesheet"`); n != 2 {
		t.Errorf("export has %d timesheets, want the two documents' that carry one", n)
	}
}

// The erase deletes a draft's timesheet with the draft — the cascade — and
// keeps an issued document's, part of the sales document (art. 17(3)(b)).
func TestErase_ADraftsTimesheetGoesAndAnIssuedOneStays(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t, newFakeHolders().options()...)
	h := f.h
	inv := issued(t, h, tsFromWork(t, h, withSheet, project41Work()...).ID)
	draft := tsFromWork(t, h, withSheet, workSrc("time.entry", workHour42, 1))
	data := invoices.Module().CustomerPersonalData(disabledDeps(h))
	inTx(t, h, true, func(tx pgx.Tx) {
		if _, err := data.EraseCustomerData(context.Background(), tx, customerAcme); err != nil {
			t.Fatalf("EraseCustomerData: %v", err)
		}
	})
	if n := h.Count(t, `SELECT count(*) FROM invoices.timesheet_rows WHERE invoice_id = $1`, draft.ID); n != 0 {
		t.Errorf("the draft's timesheet = %d rows after the erase, want none", n)
	}
	if got := storedRows(t, h, inv.ID); len(got) != 2 {
		t.Errorf("the issued invoice's timesheet = %v after the erase, want both rows kept", got)
	}
}

// The timesheet's two settings: false and initials until changed; both
// required (absent or null a 400 on the field); a label other than
// initials, number or name a 400 on timesheetPersonLabel; a change moves the
// revision on; invoices:manage only.
func TestSettings_TheTimesheetFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	var read settingsJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&read)
	if read.TimesheetDefault || read.TimesheetPersonLabel != "initials" {
		t.Errorf("defaults = %v %q, want false and initials", read.TimesheetDefault, read.TimesheetPersonLabel)
	}
	manager := h.SignIn(t, "invoices:access", "invoices:manage")
	for _, c := range []struct {
		field string
		value any
	}{
		{"timesheetDefault", nil}, {"timesheetPersonLabel", nil}, {"timesheetDefault", "yes"},
		{"timesheetPersonLabel", "nickname"}, {"timesheetPersonLabel", ""}, {"timesheetPersonLabel", 3},
	} {
		body := completeSeller(read.Revision)
		body[c.field] = c.value
		res := manager.Do(http.MethodPut, settingsPath, body)
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[c.field]) == 0 {
			t.Errorf("%s = %v: %d %s, want 400 on %s", c.field, c.value, res.Status, res.Body, c.field)
		}
		delete(body, c.field)
		if res := manager.Do(http.MethodPut, settingsPath, body); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[c.field]) == 0 {
			t.Errorf("%s left out: %d %s, want 400 on it", c.field, res.Status, res.Body)
		}
	}
	body := completeSeller(read.Revision)
	body["timesheetDefault"], body["timesheetPersonLabel"] = true, "number"
	if res := h.SignIn(t, "invoices:access").Do(http.MethodPut, settingsPath, body); res.Status != http.StatusForbidden {
		t.Errorf("without invoices:manage = %d, want 403", res.Status)
	}
	saved := saveSeller(t, h, body)
	if !saved.TimesheetDefault || saved.TimesheetPersonLabel != "number" || saved.Revision != read.Revision+1 {
		t.Errorf("saved = %v %q at %d, want true, number at %d", saved.TimesheetDefault, saved.TimesheetPersonLabel, saved.Revision, read.Revision+1)
	}
	if res := manager.Do(http.MethodPut, settingsPath, body); res.Status != http.StatusConflict {
		t.Errorf("a stale revision = %d, want 409", res.Status)
	}
	for _, label := range []string{"initials", "name"} {
		body := completeSeller(saved.Revision)
		body["timesheetPersonLabel"] = label
		saved = saveSeller(t, h, body)
		if saved.TimesheetPersonLabel != label || saved.TimesheetDefault {
			t.Errorf("saved = %v %q, want false and %s", saved.TimesheetDefault, saved.TimesheetPersonLabel, label)
		}
	}
}
