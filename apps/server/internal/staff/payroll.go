package staff

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is GET /payroll/export (design S18; plan Task 4, readings 12 and
// 13): a month's employments handed to payroll through a port. The rows are
// payroll_rows.go's; what file they become is a PayrollExporter's, chosen by
// the request's format — csv alone today, so a payroll system that wants its
// own file is one implementation more and not a change to the rows. The
// order is the register export's: the caller, one snapshot, the file built in
// memory, the export recorded as the installation's payroll_exported event
// with the file's hash, then the download.

// PayrollExporter turns a month's rows into a file: its media type and its
// bytes. month is YYYY-MM.
type PayrollExporter interface {
	Export(ctx context.Context, month string, rows []payrollRow) (contentType string, body []byte, err error)
}

// payrollFormatCSV is the format a request that names none gets.
const payrollFormatCSV = "csv"

// csvExporter is the payroll file in the Expenses form
// (expenses/reimbursements.go): a byte order mark, semicolons, CRLF, the
// decimal comma, each cell through the formula guard (csvfile.go).
type csvExporter struct{}

func (csvExporter) Export(_ context.Context, _ string, rows []payrollRow) (string, []byte, error) {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, payrollColumns)
	for _, r := range rows {
		writeCSVRow(&b, r.cells())
	}
	return "text/csv; charset=utf-8", b.Bytes(), nil
}

// payrollDownload writes the file: the generated 200 hard-codes a bare
// text/csv and names no file, and this answer has to carry the exporter's
// media type, the file's name and that nobody may cache it — the register's
// csvDownload, with the type the port chose.
type payrollDownload struct {
	contentType, fileName string
	body                  []byte
}

func (d payrollDownload) VisitGetStaffPayrollExportResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", d.contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.fileName))
	// Everyone's pay terms for a month: in nobody's cache, least of all a
	// shared one, and never sniffed into something else.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(d.body)))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(d.body)
	return err
}

// readPayroll reads what a month's file is built from on q, a snapshot's:
// every person's number and names, every employment with its terms chain,
// the business units and the departments. It reads no sealed column. The
// terms come as the store's full rows, the class D note and pay_components
// with them, because the chain helpers (termsInForce, fteChangedAt,
// salaryChangedAt) take that row; neither is written to the file, so no D
// value — and no pay amount — reaches it.
func readPayroll(ctx context.Context, q *store.Queries) (payrollInput, error) {
	in := payrollInput{
		People:      map[int64]payrollPerson{},
		Terms:       map[int64][]store.StaffEmploymentTerm{},
		Units:       map[int64]store.StaffBusinessUnit{},
		Departments: map[int64]store.StaffDepartment{},
	}
	people, err := q.ListPeopleForPayroll(ctx)
	if err != nil {
		return in, fmt.Errorf("staff: list the people for payroll: %w", err)
	}
	for _, p := range people {
		person := payrollPerson{EmployeeNumber: p.EmployeeNumber, GivenName: p.GivenName, FamilyName: p.FamilyName}
		if p.ChildrenUnder12 != nil || p.ChronicallyIllChildren != nil || p.SoleCarer != nil {
			days := careDayEntitlement(int16Value(p.ChildrenUnder12), int16Value(p.ChronicallyIllChildren), p.SoleCarer != nil && *p.SoleCarer)
			person.CareDays = &days
		}
		in.People[p.ID] = person
	}
	if in.Employments, err = q.ListAllEmployments(ctx); err != nil {
		return in, fmt.Errorf("staff: list the employments for payroll: %w", err)
	}
	ids := make([]int64, 0, len(in.Employments))
	for _, e := range in.Employments {
		ids = append(ids, e.ID)
	}
	terms, err := q.ListTermsOfEmployments(ctx, ids)
	if err != nil {
		return in, fmt.Errorf("staff: read the terms for payroll: %w", err)
	}
	for _, t := range terms {
		in.Terms[t.EmploymentID] = append(in.Terms[t.EmploymentID], t)
	}
	units, err := q.ListBusinessUnits(ctx)
	if err != nil {
		return in, fmt.Errorf("staff: list the business units for payroll: %w", err)
	}
	for _, u := range units {
		in.Units[u.ID] = u
	}
	departments, err := q.ListDepartments(ctx)
	if err != nil {
		return in, fmt.Errorf("staff: list the departments for payroll: %w", err)
	}
	for _, d := range departments {
		in.Departments[d.ID] = d
	}
	// Every approved and registered absence (delivery B, Task 6): a leave
	// reported in the month is named by its whole days, however long ago it
	// began or however far ahead it is known to end.
	if in.Absences, err = q.AbsencesBetween(ctx, store.AbsencesBetweenParams{FromDay: dayParam(allDaysFrom), ToDay: dayParam(allDaysTo)}); err != nil {
		return in, fmt.Errorf("staff: read the absences for payroll: %w", err)
	}
	return in, nil
}

// allDaysFrom and allDaysTo bound a read of every absence there is.
var (
	allDaysFrom = time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)
	allDaysTo   = time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC)
)

// purposeLeaveID is the secrets box's purpose for a leave's Leave ID.
const purposeLeaveID = "staff/leave-id"

// leaveID is the Leave ID of the leave chain whose first row is id: "L-"
// and a keyed MAC of the id under the installation's secret, so a leave
// keeps one id in every month's file and the file tells no id of the
// register (whole-branch review). The box's only error is its key
// derivation's; the cell is then blank.
func (s *server) leaveID(id int64) string {
	mac, err := s.deps.Secrets.MAC(purposeLeaveID, []byte(fmt.Sprintf("leave:%d", id)))
	if err != nil {
		return ""
	}
	return "L-" + strings.ToUpper(hex.EncodeToString(mac[:6]))
}

// GetStaffPayrollExport Export a month for payroll
// (GET /api/v1/staff/payroll/export)
//
// The contract asks staff:manage with staff:personal-data (whole-branch
// review, open question 10's interim): the leave rows name HR-only leave by
// its a-melding description. Order: (1) the parameters and the actor,
// before anything is read; (2) one REPEATABLE READ snapshot; (3) the rows and
// the file, built in memory; (4) the export recorded after the snapshot in a
// transaction of its own, with the hash of the bytes about to be answered — a
// file whose export could not be recorded is not answered; (5) the download.
// The event carries no personal data, so it has no payload class, as
// people_exported has none: no erase has anything to reach in it.
func (s *server) GetStaffPayrollExport(ctx context.Context, req gen.GetStaffPayrollExportRequestObject) (gen.GetStaffPayrollExportResponseObject, error) {
	var f fieldErrors
	// The contract leaves month optional so that a request without one
	// reaches this check and is answered with its code, as an empty one is.
	var rawMonth string
	if req.Params.Month != nil {
		rawMonth = *req.Params.Month
	}
	month, ok := parsePayrollMonth(rawMonth)
	if !ok {
		f.addCoded("month", codePayrollMonthInvalid, fmt.Sprintf("A month is YYYY-MM with a month from 01 to 12, but was '%s'", rawMonth))
	}
	format := payrollFormatCSV
	if req.Params.Format != nil {
		format = *req.Params.Format
	}
	exporter, known := s.payroll[format]
	if !known {
		f.addCoded("format", codePayrollFormatUnsupported, fmt.Sprintf("The format must be csv, but was '%s'", format))
	}
	if f.failed() {
		return gen.GetStaffPayrollExport400ApplicationProblemPlusJSONResponse(invalid(invalidPayrollTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var in payrollInput
	if err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		in, err = readPayroll(ctx, q)
		return err
	}); err != nil {
		return nil, err
	}
	in.LeaveID = s.leaveID
	rows := payrollRows(in, month)
	contentType, body, err := exporter.Export(ctx, month.Name, rows)
	if err != nil {
		return nil, fmt.Errorf("staff: write the payroll file for %s as %s: %w", month.Name, format, err)
	}
	sum := sha256.Sum256(body)
	payload := map[string]any{"month": month.Name, "format": format, "rows": len(rows), "sha256": hex.EncodeToString(sum[:])}
	if err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		return s.recordEvent(ctx, txq, nil, eventPayrollExported, who, payload, nil)
	}); err != nil {
		return nil, err
	}
	return payrollDownload{
		contentType: contentType,
		fileName:    "staff-payroll-" + month.Name + "." + format,
		body:        body,
	}, nil
}
