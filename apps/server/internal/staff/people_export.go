package staff

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is GET /people/export.csv and GET /people/import-template.csv
// (design S19; plan Task 3, readings 10 and 11): the register's file
// (csvfile.go) on its way out. The export is HR's whole register — the work
// and private groups, where each person stands today and whether a national
// identity number is on file — and never a sealed value: not a digit of the
// national identity number, nothing of the bank account. Everything it
// writes is read in one snapshot, the file is built in memory (a few
// megabytes at the bound), and the export is recorded as the installation's
// people_exported event before a byte is answered. The template is shaped by
// what its caller's import may write.

// csvDownload writes a register file: the generated response types
// hard-code a bare text/csv and name no file, and this answer has to say
// which encoding it is in, what to call it and that nobody may cache it —
// customers' csvDownload, answering both of this file's operations.
type csvDownload struct {
	body     []byte
	fileName string
}

func (d csvDownload) write(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.fileName))
	// The register names everyone who works here, where they live and how to
	// reach them: in nobody's cache, least of all a shared one.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(d.body)))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(d.body)
	return err
}

func (d csvDownload) VisitGetStaffPeopleExportCsvResponse(w http.ResponseWriter) error {
	return d.write(w)
}

func (d csvDownload) VisitGetStaffPeopleImportTemplateCsvResponse(w http.ResponseWriter) error {
	return d.write(w)
}

// exportColumns is the export's header: the key, the person's columns and the
// export-only ones — never a sealed column, never the first employment's.
var exportColumns = csvColumnsWhere(func(g csvGroup) bool {
	return g == csvGroupKey || g == csvGroupPerson || g == csvGroupExportOnly
})

// importTemplateColumns is every column its caller's import can write: the
// key, the person's, the sealed ones for a staff:personal-data holder — the
// very columns importLayoutFor would refuse the file for otherwise — and the
// first employment's.
func importTemplateColumns(personalData bool) []csvColumn {
	return csvColumnsWhere(func(g csvGroup) bool {
		switch g {
		case csvGroupKey, csvGroupPerson, csvGroupEmployment:
			return true
		case csvGroupNationalID, csvGroupBank:
			return personalData
		}
		return false
	})
}

// exportSnapshot is what one export reads in its snapshot.
type exportSnapshot struct {
	people     []store.GetPersonRow
	placements map[int64]placement
	// fte is the hundredths of a position each person's employments in
	// force today add up to; absent for someone with none.
	fte map[int64]int64
}

// readExport reads the register on q, a snapshot's: at most limit people,
// where each stands today, and what their employments in force today add up
// to.
func readExport(ctx context.Context, q *store.Queries, limit int, today time.Time) (exportSnapshot, error) {
	out := exportSnapshot{fte: map[int64]int64{}}
	rows, err := q.ListPeopleForExport(ctx, int32(limit))
	if err != nil {
		return out, fmt.Errorf("staff: list the people for export: %w", err)
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		out.people = append(out.people, store.GetPersonRow(r))
		ids = append(ids, r.ID)
	}
	if out.placements, err = placementsOf(ctx, q, ids, today); err != nil {
		return out, err
	}
	employments, err := q.ListAllEmployments(ctx)
	if err != nil {
		return out, fmt.Errorf("staff: list the employments for export: %w", err)
	}
	employmentIDs := make([]int64, 0, len(employments))
	for _, e := range employments {
		employmentIDs = append(employmentIDs, e.ID)
	}
	terms, err := q.ListTermsOfEmployments(ctx, employmentIDs)
	if err != nil {
		return out, fmt.Errorf("staff: read the terms for export: %w", err)
	}
	chains := map[int64][]store.StaffEmploymentTerm{}
	for _, t := range terms {
		chains[t.EmploymentID] = append(chains[t.EmploymentID], t)
	}
	for _, e := range employments {
		if t := termsInForce(e, chains[e.ID], today); t != nil {
			out.fte[e.PersonID] += hundredthsOfNumeric(t.FtePercent)
		}
	}
	return out, nil
}

// exportValues is one person's cells by column name: a value there is none
// of is the empty cell; dates are ISO, the percentage carries the decimal
// comma.
func exportValues(row store.GetPersonRow, p placement, fte int64, hasFTE bool) map[string]string {
	values := map[string]string{}
	for name, v := range personValues(row) {
		if v != nil {
			values[name] = *v
		}
	}
	values["status"] = string(p.status.State)
	if p.title != nil {
		values["currentTitle"] = *p.title
	}
	if p.department != nil {
		values["currentDepartment"] = p.department.Code
	}
	if p.businessUnit != nil {
		values["currentBusinessUnit"] = p.businessUnit.Name
	}
	if p.location != nil {
		values["currentLocation"] = p.location.Name
	}
	if p.manager != nil {
		values["currentManagerEmployeeNumber"] = p.manager.EmployeeNumber
	}
	if hasFTE {
		percent := float64(fte) / 100
		values["currentFtePercent"] = formatCSVDecimal(&percent)
	}
	values["nationalIdOnFile"] = "no"
	if row.NationalIDOnFile {
		values["nationalIdOnFile"] = "yes"
	}
	return values
}

// registerCSV is the file: the byte order mark, the header, then one row per
// person with its cells in the columns' order.
func registerCSV(snap exportSnapshot) []byte {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, csvColumnNames(exportColumns))
	cells := make([]string, len(exportColumns))
	for _, row := range snap.people {
		fte, hasFTE := snap.fte[row.ID]
		values := exportValues(row, snap.placements[row.ID], fte, hasFTE)
		for i, c := range exportColumns {
			cells[i] = values[c.Name]
		}
		writeCSVRow(&b, cells)
	}
	return b.Bytes()
}

// exportTooMany is the bound's refusal: a bare problem with its code.
func exportTooMany(limit int) gen.StaffProblem {
	status := int32(http.StatusBadRequest)
	code, title := codeExportTooMany, "Too many people to export"
	detail := fmt.Sprintf("The register holds more than %d people, which is more than one file holds (STAFF_IMPORT_MAX_ROWS).", limit)
	return gen.StaffProblem{Code: &code, Title: &title, Detail: &detail, Status: &status}
}

// GetStaffPeopleExportCsv Export the register as CSV
// (GET /api/v1/staff/people/export.csv)
//
// One person more than the bound is read, so "the whole register" and "more
// than a file may hold" are told apart without a count of their own. The
// actor is resolved before the snapshot, and the event is recorded after it
// in its own transaction: a file whose export could not be recorded is not
// answered.
func (s *server) GetStaffPeopleExportCsv(ctx context.Context, _ gen.GetStaffPeopleExportCsvRequestObject) (gen.GetStaffPeopleExportCsvResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	limit := s.importMaxRows()
	var snap exportSnapshot
	if err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		snap, err = readExport(ctx, q, limit+1, caller.Today)
		return err
	}); err != nil {
		return nil, err
	}
	if len(snap.people) > limit {
		return gen.GetStaffPeopleExportCsv400ApplicationProblemPlusJSONResponse(exportTooMany(limit)), nil
	}
	if err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		return s.recordEvent(ctx, txq, nil, eventPeopleExported, who, map[string]any{"rows": len(snap.people)}, nil)
	}); err != nil {
		return nil, err
	}
	return csvDownload{
		body:     registerCSV(snap),
		fileName: "staff-people-" + caller.Today.Format(time.DateOnly) + ".csv",
	}, nil
}

// GetStaffPeopleImportTemplateCsv Download the register's import template
// (GET /api/v1/staff/people/import-template.csv)
func (s *server) GetStaffPeopleImportTemplateCsv(ctx context.Context, _ gen.GetStaffPeopleImportTemplateCsvRequestObject) (gen.GetStaffPeopleImportTemplateCsvResponseObject, error) {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, csvColumnNames(importTemplateColumns(s.has(ctx, "staff:personal-data"))))
	return csvDownload{body: b.Bytes(), fileName: "staff-people-import-template.csv"}, nil
}
