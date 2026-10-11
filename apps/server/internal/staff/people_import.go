package staff

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/secrets"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/ident"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is POST /people/import (design S19; plan Task 3, reading 9): the
// register's file (csvfile.go) read back into people. Customers' import
// (internal/customers/import.go) is its precedent, and four things about it
// are worth saying once, because every function below follows from them:
//
//   - A file may only say what its sender could say by hand. The router
//     admits staff:manage; the header is then checked against the caller
//     before a row is read, and a sealed column from a caller without
//     staff:personal-data refuses the file whole (import_sealed_columns)
//     rather than being skipped — a skipped column is a change the sender
//     believes was made.
//   - Each row is written through the functions the endpoints themselves call
//     (writes.go) behind the same validators and followed by the same events,
//     the importer as actor, under the same guard: an update takes the
//     person's lock (withPersonLock) and an erased person's row is
//     person_erased when it would change anything, so a row that succeeds is
//     indistinguishable from the same edits made by hand, and one that fails
//     leaves nothing half-written. A create is the person, then — when the
//     row has a startDate — the first employment and its terms, in the row's
//     one transaction; an update is the person's own columns and never an
//     employment (reading 9).
//   - A dry run is the real run minus the commit: each row in its own
//     transaction, rolled back when the row has run — no person, no employee
//     number, no event, no mail. What it cannot see is an earlier row's effect
//     on a later one; the cases a file makes likely are checked in memory
//     before any row runs (inFileDuplicates), and a manager an earlier row
//     creates is taken as found.
//   - Nothing leaves the database while a row's transaction is open: the
//     actor and the structure's names are read once, before the first row,
//     the sealed values are sealed before their row's transaction, and the
//     bank-change mail goes after its row has committed.

// The upload's bounds (plan, Exact values). maxImportRequestBytes is the
// router's cap for the operation (bodyLimits): the file plus room for the
// multipart framing, the receipts' margin.
const (
	maxImportFileBytes    = 5 * 1024 * 1024
	maxImportRequestBytes = maxImportFileBytes + 64*1024
	// importErrorColumn is the column a failed-rows file adds, ignored so
	// that file imports as it is.
	importErrorColumn = "error"
	// defaultImportMaxRows is STAFF_IMPORT_MAX_ROWS' default, for a server
	// whose Deps carry no configuration (the white-box tests').
	defaultImportMaxRows = 5000
)

// importMaxRows is how many people one import takes and one export writes:
// STAFF_IMPORT_MAX_ROWS, one number for both so a file the export wrote
// always fits.
func (s *server) importMaxRows() int {
	if s.deps.Config != nil && s.deps.Config.StaffImportMaxRows > 0 {
		return s.deps.Config.StaffImportMaxRows
	}
	return defaultImportMaxRows
}

// The file's refusals that are sentences of their own.
const (
	importUploadMessage   = "An import is one CSV file of at most 5 MiB, sent as the multipart part named 'file'"
	importTooLargeMessage = "An import file is at most 5 MiB; import it in slices"
)

// readImportPart reads the one multipart part named "file". It answers the
// bytes and a refusal: import_too_large for a part past 5 MiB or a body the
// router's own cap stopped (http.MaxBytesError), a plain one for a part that
// is missing, repeated, empty or unreadable.
func readImportPart(mr *multipart.Reader) ([]byte, fieldErrors) {
	var f fieldErrors
	if mr == nil {
		f.add(importFileField, importUploadMessage)
		return nil, f
	}
	fail := func(err error) ([]byte, fieldErrors) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			f.addCoded(importFileField, codeImportTooLarge, importTooLargeMessage)
		} else {
			f.add(importFileField, importUploadMessage)
		}
		return nil, f
	}
	var data []byte
	found := false
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fail(err)
		}
		if part.FormName() != importFileField {
			_ = part.Close()
			continue
		}
		if found {
			_ = part.Close()
			f.add(importFileField, importUploadMessage)
			return nil, f
		}
		found = true
		data, err = io.ReadAll(io.LimitReader(part, maxImportFileBytes+1))
		_ = part.Close()
		if err != nil {
			return fail(err)
		}
		if len(data) > maxImportFileBytes {
			f.addCoded(importFileField, codeImportTooLarge, importTooLargeMessage)
			return nil, f
		}
	}
	if !found || len(data) == 0 {
		f.add(importFileField, importUploadMessage)
		return nil, f
	}
	return data, f
}

// importLayout is a header read against the column table: where each column
// a row is read from sits, and how the file spells it.
type importLayout struct {
	index    map[string]int
	spelling map[string]string
	width    int
}

func (l importLayout) has(column string) bool {
	_, ok := l.index[column]
	return ok
}

// cell is rec's value in column, "" for a column the file does not carry.
func (l importLayout) cell(rec csvRecord, column string) string {
	if i, ok := l.index[column]; ok {
		return rec.Cells[i]
	}
	return ""
}

// trimmed is cell without the spaces around it.
func (l importLayout) trimmed(rec csvRecord, column string) string {
	return strings.TrimSpace(l.cell(rec, column))
}

// spelt is column as the header spells it — headers match without regard to
// case, and an error names the cell the person will look for.
func (l importLayout) spelt(column string) string {
	if s, ok := l.spelling[column]; ok {
		return s
	}
	return column
}

// position orders a row's errors by where their column sits, the row's own
// first.
func (l importLayout) position(column string) int {
	if i, ok := l.index[column]; ok {
		return i
	}
	return -1
}

// maxImportHeaderWidth is the widest header the table can describe: every
// column once and the error column. A wider one is one refusal rather than a
// refusal per cell.
var maxImportHeaderWidth = len(registerColumns) + 1

// The sealed columns' pairs: a value and its kind come together, and a
// foreign id's country only with them.
var sealedPairs = [][2]string{{"nationalId", "nationalIdKind"}, {"bankAccount", "bankAccountKind"}}

// importLayoutFor reads the header (plan Task 3): every column one the table
// names, the export-only ones or the error column, each at most once, matched
// without regard to case; the employeeNumber column there; a sealed value
// with its kind; and no sealed column from a caller without
// staff:personal-data, which is import_sealed_columns. Every refusal is
// answered at once, so a file is fixed in one pass.
func importLayoutFor(header []string, personalData bool) (importLayout, fieldErrors) {
	var f fieldErrors
	if len(header) > maxImportHeaderWidth {
		f.add(importFileField, fmt.Sprintf("The header has %d columns, but the file describes at most %d", len(header), maxImportHeaderWidth))
		return importLayout{}, f
	}
	known := make(map[string]csvColumn, len(registerColumns))
	for _, c := range registerColumns {
		known[strings.ToLower(c.Name)] = c
	}
	l := importLayout{index: map[string]int{}, spelling: map[string]string{}, width: len(header)}
	var unknown, nameless, sealed []string
	for i, raw := range header {
		name := strings.TrimSpace(raw)
		if name == "" {
			nameless = append(nameless, strconv.Itoa(i+1))
			continue
		}
		if strings.EqualFold(name, importErrorColumn) {
			continue
		}
		c, ok := known[strings.ToLower(name)]
		switch {
		case !ok:
			unknown = append(unknown, "'"+name+"'")
			continue
		case c.Group == csvGroupExportOnly:
			continue
		}
		if _, repeated := l.index[c.Name]; repeated {
			f.add(importFileField, fmt.Sprintf("The column %s appears more than once", c.Name))
			continue
		}
		l.index[c.Name] = i
		l.spelling[c.Name] = name
		if c.Group == csvGroupNationalID || c.Group == csvGroupBank {
			sealed = append(sealed, c.Name)
		}
	}
	if len(nameless) > 0 {
		f.add(importFileField, fmt.Sprintf("Columns without a name: %s", strings.Join(nameless, ", ")))
	}
	if len(unknown) > 0 {
		f.add(importFileField, fmt.Sprintf("Unknown columns: %s. A column is one the export and the template carry, and a misspelt one is refused rather than ignored", strings.Join(unknown, ", ")))
	}
	if !l.has("employeeNumber") {
		f.add(importFileField, "The file has no employeeNumber column; every row is keyed on its employee number")
	}
	for _, pair := range sealedPairs {
		if l.has(pair[0]) != l.has(pair[1]) {
			f.add(importFileField, fmt.Sprintf("The columns %s and %s come together, both or neither", pair[0], pair[1]))
		}
	}
	if l.has("nationalIdCountry") && !l.has("nationalId") {
		f.add(importFileField, "The column nationalIdCountry comes only with nationalId and nationalIdKind")
	}
	if len(sealed) > 0 && !personalData {
		f.addCoded(importFileField, codeImportSealedColumns, fmt.Sprintf(
			"The columns %s need the staff:personal-data permission, which you do not have: remove them from the file, or ask for the permission",
			strings.Join(sealed, ", ")))
	}
	return l, f
}

// importError is one entry of StaffImportResult.errors: Column "" is a
// problem with the row as a whole, a column its canonical name until the
// tally spells it as the file does.
type importError struct {
	Row     int
	Column  string
	Message string
}

// importRowRefusal is a row's errors travelling out of its transaction as
// the error that rolls it back.
type importRowRefusal struct {
	errs []importError
}

func (r *importRowRefusal) Error() string {
	return fmt.Sprintf("staff: import row %d refused", r.errs[0].Row)
}

// errImportDryRun rolls a dry-run row's transaction back once the row ran.
var errImportDryRun = errors.New("staff: import dry run rolled back")

// rowFields maps a refused request's field names to the file's columns: a
// person's fields are their own columns, an employment's and its first
// row's ("terms.<field>") the first-employment columns that fed them.
var rowFields = map[string]string{
	"businessUnitId":        "businessUnit",
	"terms.departmentId":    "department",
	"terms.locationId":      "location",
	"terms.managerPersonId": "managerEmployeeNumber",
}

// columnOfField is the column a refused field came from, "" when the file
// has no such column (a rule about the row as a whole).
func columnOfField(field string) string {
	if c, ok := rowFields[field]; ok {
		return c
	}
	name := strings.TrimPrefix(field, "terms.")
	for _, c := range registerColumns {
		if c.Name == name && c.Group != csvGroupExportOnly {
			return name
		}
	}
	return ""
}

// columnOfCode is the column a coded conflict is about, when it names one.
var columnOfCode = map[string]string{
	codeSelfManager:  "managerEmployeeNumber",
	codeManagerCycle: "managerEmployeeNumber",
}

// rowRefusal is err as row's errors when it is one of the module's refusals
// — every field error on the column it came from (sealed: "value" is the
// sealed column the path wrote), a conflict's detail on the column its code
// names or on the row — and err itself otherwise.
func rowRefusal(row int, err error, sealedColumn string) error {
	if errors.Is(err, errPersonNotFound) {
		return &importRowRefusal{errs: []importError{{Row: row, Message: "The person this employee number named is gone"}}}
	}
	r, ok := asRefusal(err)
	if !ok {
		return err
	}
	var errs []importError
	if r.problem.Errors != nil {
		fields := make([]string, 0, len(*r.problem.Errors))
		for field := range *r.problem.Errors {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		for _, field := range fields {
			column := columnOfField(field)
			if field == "value" {
				column = sealedColumn
			}
			for _, m := range (*r.problem.Errors)[field] {
				errs = append(errs, importError{Row: row, Column: column, Message: m})
			}
		}
	}
	if len(errs) == 0 {
		message := "This row was refused"
		switch {
		case r.problem.Detail != nil:
			message = *r.problem.Detail
		case r.status == http.StatusNotFound:
			message = "Something this row names does not exist"
		}
		column := ""
		if r.problem.Code != nil {
			column = columnOfCode[*r.problem.Code]
		}
		errs = append(errs, importError{Row: row, Column: column, Message: message})
	}
	return &importRowRefusal{errs: errs}
}

// importPlan is one row as read before any transaction: its key, the errors
// its cells already have, and its sealed values checked and sealed.
type importPlan struct {
	rec            csvRecord
	employeeNumber string
	errs           []importError
	nationalID     *sealedNationalID
	bankAccount    *sealedBankAccount
}

// planImportRow reads what of rec needs no database: the width, the key and the
// sealed cells, each checked by staff/ident and sealed — so a sealed value
// is never carried past this function in clear. Its messages never echo a
// sealed cell.
func (s *server) planImportRow(l importLayout, rec csvRecord) (importPlan, error) {
	p := importPlan{rec: rec}
	fail := func(column, message string) {
		p.errs = append(p.errs, importError{Row: rec.Row, Column: column, Message: message})
	}
	if len(rec.Cells) != l.width {
		fail("", fmt.Sprintf("This row has %d cells, but the header has %d", len(rec.Cells), l.width))
		return p, nil
	}
	p.employeeNumber = l.trimmed(rec, "employeeNumber")
	if p.employeeNumber == "" {
		fail("employeeNumber", "Every row needs its employee number; it is how the import finds the person, or names the new one")
	} else if _, _, msg := parseEmployeeNumber(p.employeeNumber); msg != "" {
		fail("employeeNumber", msg)
	}
	if value := l.trimmed(rec, "nationalId"); value != "" {
		kind := l.trimmed(rec, "nationalIdKind")
		n, err := ident.ParseNationalID(ident.NationalIDKind(kind), l.trimmed(rec, "nationalIdCountry"), value)
		switch {
		case kind == "":
			fail("nationalIdKind", "A national identity number needs its kind: fodselsnummer, d_nummer or foreign")
		case err != nil:
			fail("nationalId", identMessage(err, "Not a valid national identity number of this kind"))
		default:
			sealed, err := s.sealNationalID(n)
			if err != nil {
				return p, fmt.Errorf("staff: import row %d: %w", rec.Row, err)
			}
			p.nationalID = &sealed
		}
	}
	if value := l.trimmed(rec, "bankAccount"); value != "" {
		kind := l.trimmed(rec, "bankAccountKind")
		normalised, err := ident.ValidateBankAccount(ident.AccountKind(kind), value)
		switch {
		case kind == "":
			fail("bankAccountKind", "A bank account needs its kind: bban or iban")
		case err != nil:
			fail("bankAccount", identMessage(err, "Not a valid bank account number of this kind"))
		default:
			sealed, err := s.sealBankAccount(ident.AccountKind(kind), normalised)
			if err != nil {
				return p, fmt.Errorf("staff: import row %d: %w", rec.Row, err)
			}
			p.bankAccount = &sealed
		}
	}
	return p, nil
}

// inFileDuplicates refuses the second of two rows with the same employee
// number (however cased, as the unique index compares), the same work email
// (lower-cased, as it is stored) or the same national identity number (by
// its MAC, as the unique index compares, and never naming a digit of it), in
// both runs alike: a dry run's rows each roll back before the next, so the
// database could not tell it. A row whose cells already failed claims
// nothing.
func inFileDuplicates(l importLayout, plans []importPlan) {
	numbers, emails, nationalIDs := map[string]int{}, map[string]int{}, map[string]int{}
	for i := range plans {
		p := &plans[i]
		if len(p.errs) > 0 {
			continue
		}
		number := strings.ToUpper(p.employeeNumber)
		if row, seen := numbers[number]; seen {
			p.errs = append(p.errs, importError{Row: p.rec.Row, Column: "employeeNumber",
				Message: fmt.Sprintf("Row %d of this file already has this employee number", row)})
		}
		email := strings.ToLower(l.trimmed(p.rec, "workEmail"))
		if row, seen := emails[email]; seen && email != "" {
			p.errs = append(p.errs, importError{Row: p.rec.Row, Column: "workEmail",
				Message: fmt.Sprintf("Row %d of this file already has this work email", row)})
		}
		mac := ""
		if p.nationalID != nil {
			mac = string(p.nationalID.mac)
			if row, seen := nationalIDs[mac]; seen {
				p.errs = append(p.errs, importError{Row: p.rec.Row, Column: "nationalId",
					Message: fmt.Sprintf("Row %d of this file already has this national identity number", row)})
			}
		}
		if len(p.errs) > 0 {
			continue
		}
		numbers[number] = p.rec.Row
		if email != "" {
			emails[email] = p.rec.Row
		}
		if mac != "" {
			nationalIDs[mac] = p.rec.Row
		}
	}
}

// importVocabulary is the structure a first employment names, read once per
// file: the business units by name and by org number, the departments by
// code, the locations by name — each name matched without regard to case and
// mapping to every row that bears it, so a name two rows share is refused as
// ambiguous rather than guessed at. Whether the row named is archived is the
// write path's own check.
type importVocabulary struct {
	unitsByName, unitsByOrg, departments, locations map[string][]int64
}

func importVocabularyFor(ctx context.Context, q *store.Queries, l importLayout) (importVocabulary, error) {
	v := importVocabulary{
		unitsByName: map[string][]int64{}, unitsByOrg: map[string][]int64{},
		departments: map[string][]int64{}, locations: map[string][]int64{},
	}
	if !l.has("startDate") {
		return v, nil
	}
	units, err := q.ListBusinessUnits(ctx)
	if err != nil {
		return v, fmt.Errorf("staff: read the business units: %w", err)
	}
	for _, u := range units {
		key := strings.ToLower(u.Name)
		v.unitsByName[key] = append(v.unitsByName[key], u.ID)
		if u.OrgNumber != nil {
			v.unitsByOrg[*u.OrgNumber] = append(v.unitsByOrg[*u.OrgNumber], u.ID)
		}
	}
	departments, err := q.ListDepartments(ctx)
	if err != nil {
		return v, fmt.Errorf("staff: read the departments: %w", err)
	}
	for _, d := range departments {
		key := strings.ToUpper(d.Code)
		v.departments[key] = append(v.departments[key], d.ID)
	}
	locations, err := q.ListLocations(ctx)
	if err != nil {
		return v, fmt.Errorf("staff: read the locations: %w", err)
	}
	for _, loc := range locations {
		key := strings.ToLower(loc.Name)
		v.locations[key] = append(v.locations[key], loc.ID)
	}
	return v, nil
}

// one is the single id a name found, or the row's error: none is missing,
// many is ambiguous.
func one(ids []int64, missing, ambiguous string) (*int64, string) {
	switch len(ids) {
	case 0:
		return nil, missing
	case 1:
		return &ids[0], ""
	}
	return nil, ambiguous
}

// cellReader reads one row's cells into a request's shapes, collecting every
// cell that does not read as its type.
type cellReader struct {
	l    importLayout
	rec  csvRecord
	errs []importError
}

func (r *cellReader) fail(column, message string) {
	r.errs = append(r.errs, importError{Row: r.rec.Row, Column: column, Message: message})
}

// text is a column's trimmed cell, nil when blank.
func (r *cellReader) text(column string) *string {
	if v := r.l.trimmed(r.rec, column); v != "" {
		return &v
	}
	return nil
}

// date is a column's cell as a civil date, nil when blank.
func (r *cellReader) date(column, label string) *openapi_types.Date {
	v := r.text(column)
	if v == nil {
		return nil
	}
	d, err := time.Parse(time.DateOnly, *v)
	if err != nil {
		r.fail(column, fmt.Sprintf("%s is a date such as 2026-01-31, but was '%s'", label, *v))
		return nil
	}
	return &openapi_types.Date{Time: d}
}

// decimal is a column's cell as a number, either decimal mark, nil when
// blank.
func (r *cellReader) decimal(column, label string) *float64 {
	v := r.text(column)
	if v == nil {
		return nil
	}
	n, ok := parseCSVDecimal(*v)
	if !ok {
		r.fail(column, fmt.Sprintf("%s must be a number such as 37,5, but was '%s'", label, *v))
		return nil
	}
	return &n
}

// personRequest is base with the person's columns the file carries laid over
// it: a blank cell clears an optional field (and leaves the display name and
// the defaults to the person's own rules), a column not in the file keeps
// base's value.
func (r *cellReader) personRequest(base gen.StaffPersonRequest) gen.StaffPersonRequest {
	out := base
	has := r.l.has
	textInto := func(column string, into **string) {
		if has(column) {
			*into = r.text(column)
		}
	}
	if has("givenName") {
		out.GivenName = r.l.trimmed(r.rec, "givenName")
	}
	if has("familyName") {
		out.FamilyName = r.l.trimmed(r.rec, "familyName")
	}
	textInto("displayName", &out.DisplayName)
	textInto("workEmail", &out.WorkEmail)
	textInto("phoneWork", &out.PhoneWork)
	textInto("workCountry", &out.WorkCountry)
	textInto("phoneMobile", &out.PhoneMobile)
	textInto("phonePrivate", &out.PhonePrivate)
	textInto("privateEmail", &out.PrivateEmail)
	textInto("street", &out.Street)
	textInto("postalCode", &out.PostalCode)
	textInto("city", &out.City)
	textInto("country", &out.Country)
	if has("preferredLanguage") {
		out.PreferredLanguage = (*gen.StaffLanguage)(r.text("preferredLanguage"))
	}
	if has("gender") {
		out.Gender = (*gen.StaffGender)(r.text("gender"))
	}
	if has("birthDate") {
		out.BirthDate = r.date("birthDate", "A birth date")
	}
	return out
}

// personRequestOf is a stored person as the request that would write them
// again: the base an update's cells are laid over, so a column the file does
// not carry keeps its value. The employee number is left out — it never
// changes.
func personRequestOf(row store.GetPersonRow) gen.StaffPersonRequest {
	displayName, workCountry := row.DisplayName, row.WorkCountry
	language := gen.StaffLanguage(row.PreferredLanguage)
	return gen.StaffPersonRequest{
		GivenName: row.GivenName, FamilyName: row.FamilyName, DisplayName: &displayName,
		BirthDate: dateOf(row.BirthDate), Gender: (*gen.StaffGender)(row.Gender),
		WorkEmail: row.WorkEmail, PhoneWork: row.PhoneWork, PhoneMobile: row.PhoneMobile,
		PhonePrivate: row.PhonePrivate, PrivateEmail: row.PrivateEmail, Street: row.Street,
		PostalCode: row.PostalCode, City: row.City, Country: row.Country, WorkCountry: &workCountry,
		PreferredLanguage: &language, Notes: row.Notes,
	}
}

// employmentRequest is a create's first employment from its columns, the
// structure looked up in vocab and the manager already resolved; nil when
// the row has no startDate — and then every other first-employment cell must
// be blank, since a value nobody would read is a change the sender believes
// was made.
func (r *cellReader) employmentRequest(vocab importVocabulary, manager *int64) *gen.StaffEmploymentRequest {
	start := r.date("startDate", "A start date")
	if start == nil {
		if r.l.trimmed(r.rec, "startDate") == "" {
			for _, c := range csvColumnsWhere(func(g csvGroup) bool { return g == csvGroupEmployment }) {
				if r.l.trimmed(r.rec, c.Name) != "" {
					r.fail("startDate", "The first employment's columns need its startDate; leave them blank to add the person alone")
					break
				}
			}
		}
		return nil
	}
	body := gen.StaffEmploymentRequest{StartDate: *start}
	if v := r.text("businessUnit"); v != nil {
		ids, ok := vocab.unitsByOrg[strings.Join(strings.Fields(*v), "")]
		if !ok {
			ids = vocab.unitsByName[strings.ToLower(*v)]
		}
		id, msg := one(ids, fmt.Sprintf("No business unit is named or numbered '%s'", *v),
			fmt.Sprintf("More than one business unit is named '%s'; name it by its org number", *v))
		if msg != "" {
			r.fail("businessUnit", msg)
		}
		body.BusinessUnitId = id
	}
	t := &body.Terms
	t.EmploymentType = (*gen.StaffEmploymentType)(r.text("employmentType"))
	t.TemporaryGround = (*gen.StaffTemporaryGround)(r.text("temporaryGround"))
	t.ExpectedEndDate = r.date("expectedEndDate", "An expected end date")
	t.FtePercent = r.decimal("ftePercent", "A percentage")
	t.AppointmentForm = (*gen.StaffAppointmentForm)(r.text("appointmentForm"))
	t.WorkingHoursScheme = (*gen.StaffWorkingHoursScheme)(r.text("workingHoursScheme"))
	t.FullTimeHoursPerWeek = r.decimal("fullTimeHoursPerWeek", "Hours per week")
	t.OccupationCode = r.text("occupationCode")
	t.Title = r.text("title")
	if v := r.text("department"); v != nil {
		id, msg := one(vocab.departments[strings.ToUpper(*v)], fmt.Sprintf("No department has the code '%s'", *v), "")
		if msg != "" {
			r.fail("department", msg)
		}
		t.DepartmentId = id
	}
	if v := r.text("location"); v != nil {
		id, msg := one(vocab.locations[strings.ToLower(*v)], fmt.Sprintf("No location is named '%s'", *v),
			fmt.Sprintf("More than one location is named '%s'; rename one of them", *v))
		if msg != "" {
			r.fail("location", msg)
		}
		t.LocationId = id
	}
	t.ManagerPersonId = manager
	return &body
}

// importOptions is what every row of one request shares, resolved before the
// first row's transaction opens.
type importOptions struct {
	who    actor
	dryRun bool
	vocab  importVocabulary
	// numbers is the first row of the file that carries each employee number
	// (upper-cased), for a dry run's manager an earlier row creates.
	numbers map[string]int
}

// importOutcome is what a row that succeeded did.
type importOutcome int

const (
	importCreated importOutcome = iota + 1
	importUpdated
)

// importRow writes one planned row in its own transaction — under the
// person's lock for a number a person has, a locked transaction creating
// them otherwise — rolled back on a dry run. A refusal is an
// importRowRefusal; any other error is the request's. A bank account the row
// changed mails the person once the row has committed.
func (s *server) importRow(ctx context.Context, l importLayout, p importPlan, o importOptions) (importOutcome, error) {
	id, err := store.New(s.deps.Pool).PersonIDByEmployeeNumber(ctx, p.employeeNumber)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("staff: find employee number of import row %d: %w", p.rec.Row, err)
	}
	var (
		outcome = importUpdated
		mailTo  *store.GetPersonRow
	)
	write := func(ctx context.Context, txq *store.Queries, personID int64) error {
		if p.nationalID != nil {
			if err := s.setNationalID(ctx, txq, personID, *p.nationalID, o.who); err != nil {
				return rowRefusal(p.rec.Row, err, "nationalId")
			}
		}
		if p.bankAccount != nil {
			changed, err := s.setBankAccount(ctx, txq, personID, *p.bankAccount, o.who)
			if err != nil {
				return rowRefusal(p.rec.Row, err, "bankAccount")
			}
			if changed {
				saved, err := readPerson(ctx, txq, personID)
				if err != nil {
					return err
				}
				mailTo = &saved
			}
		}
		if o.dryRun {
			return errImportDryRun
		}
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		outcome = importCreated
		err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
			personID, err := s.importCreate(ctx, txq, l, p, o)
			if err != nil {
				return err
			}
			return write(ctx, txq, personID)
		})
	} else {
		// allowErased: the export carries erased people, so their unchanged
		// rows come back; importUpdate refuses only a row that would change
		// one (person_erased), judged on the row the lock holds.
		err = s.withPersonLock(ctx, id, allowErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
			if err := s.importUpdate(ctx, txq, l, p, o, id); err != nil {
				return err
			}
			return write(ctx, txq, id)
		})
	}
	err = rowRefusal(p.rec.Row, err, "")
	if errors.Is(err, errImportDryRun) {
		return outcome, nil
	}
	if err != nil {
		return 0, err
	}
	if mailTo != nil {
		s.mailBankAccountChanged(ctx, *mailTo)
	}
	return outcome, nil
}

// importUpdate is PUT /people/{id} for a row, under the person's lock: the
// person as stored, the row's person columns laid over them, the person's
// own rules, and updatePerson. An update never touches employments. An
// erased person is read-only (erased.go): their row is person_erased when it
// would change a person column or a sealed value, and otherwise — the row
// the export wrote for them — no write, as any unchanged row.
func (s *server) importUpdate(ctx context.Context, txq *store.Queries, l importLayout, p importPlan, o importOptions, personID int64) error {
	current, err := readPerson(ctx, txq, personID)
	if err != nil {
		return err
	}
	r := &cellReader{l: l, rec: p.rec}
	body := r.personRequest(personRequestOf(current))
	parsed, f := parsePerson(body, false)
	if err := refuseRowFields(r, f); err != nil {
		return err
	}
	if current.ErasedAt != nil {
		changed, err := s.importChanges(ctx, txq, current, parsed, p)
		if err != nil {
			return err
		}
		if changed {
			return rowRefusal(p.rec.Row, personErased(), "")
		}
	}
	if _, err := s.updatePerson(ctx, txq, current, parsed, o.who); err != nil {
		return rowRefusal(p.rec.Row, err, "")
	}
	return nil
}

// importChanges reports whether an update row would change current: a person
// column, or a sealed cell that is not the value on file (the national
// identity number by its MAC, the bank account opened to compare — local
// work, no call out).
func (s *server) importChanges(ctx context.Context, txq *store.Queries, current store.GetPersonRow, parsed parsedPerson, p importPlan) (bool, error) {
	if len(diffPerson(&current, parsed.applyTo(current))) > 0 {
		return true, nil
	}
	if p.nationalID != nil {
		before, err := txq.PersonNationalID(ctx, current.ID)
		if err != nil {
			return false, fmt.Errorf("staff: read person %d's national identity number: %w", current.ID, err)
		}
		if len(before.NationalIDMac) == 0 || !secrets.MACEqual(before.NationalIDMac, p.nationalID.mac) {
			return true, nil
		}
	}
	if p.bankAccount != nil {
		same, err := s.sameBankAccount(ctx, txq, current.ID, *p.bankAccount)
		if err != nil {
			return false, err
		}
		if !same {
			return true, nil
		}
	}
	return false, nil
}

// importCreate is POST /people for a row and, when it has a startDate, POST
// /people/{id}/employments for its first employment, in the row's one
// transaction: every cell read and judged before anything is written, so a
// row's every problem is reported at once. It answers the new person's id.
func (s *server) importCreate(ctx context.Context, txq *store.Queries, l importLayout, p importPlan, o importOptions) (int64, error) {
	r := &cellReader{l: l, rec: p.rec}
	number := p.employeeNumber
	body := r.personRequest(gen.StaffPersonRequest{EmployeeNumber: &number})
	parsed, f := parsePerson(body, true)

	manager, err := s.importManager(ctx, txq, r, p, o)
	if err != nil {
		return 0, err
	}
	var employment *newEmployment
	if request := r.employmentRequest(o.vocab, manager); request != nil {
		e, ef := parseNewEmployment(*request)
		for field, messages := range ef.errs {
			for _, m := range messages {
				f.add(field, m)
			}
		}
		employment = &e
	}
	if err := refuseRowFields(r, f); err != nil {
		return 0, err
	}
	created, err := s.createPerson(ctx, txq, parsed, o.who)
	if err != nil {
		return 0, rowRefusal(p.rec.Row, err, "")
	}
	if employment != nil {
		if _, err := s.addEmployment(ctx, txq, created.ID, *employment, o.who); err != nil {
			return 0, rowRefusal(p.rec.Row, err, "")
		}
	}
	return created.ID, nil
}

// importManager is the person a create's managerEmployeeNumber names, read
// in the row's transaction: nil when the cell is blank, and for a manager no
// person is the row's error — but in a dry run, a manager an earlier row of
// the file creates is taken as found (its row rolled back), and the row is
// judged without one.
func (s *server) importManager(ctx context.Context, txq *store.Queries, r *cellReader, p importPlan, o importOptions) (*int64, error) {
	number := r.text("managerEmployeeNumber")
	if number == nil || r.l.trimmed(r.rec, "startDate") == "" {
		return nil, nil
	}
	if strings.EqualFold(*number, p.employeeNumber) {
		r.fail("managerEmployeeNumber", "A person is never their own manager")
		return nil, nil
	}
	id, err := txq.PersonIDByEmployeeNumber(ctx, *number)
	switch {
	case err == nil:
		return &id, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("staff: find the manager of import row %d: %w", p.rec.Row, err)
	}
	if first, ok := o.numbers[strings.ToUpper(*number)]; ok && first < p.rec.Row && o.dryRun {
		return nil, nil
	}
	r.fail("managerEmployeeNumber", fmt.Sprintf("No person has the employee number '%s'; a manager the file adds comes on an earlier row", *number))
	return nil, nil
}

// refuseRowFields is the row's refusal when its cells or its requests'
// rules failed: the cells' own errors and every field error on its column.
func refuseRowFields(r *cellReader, f fieldErrors) error {
	errs := r.errs
	fields := make([]string, 0, len(f.errs))
	for field := range f.errs {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		for _, m := range f.errs[field] {
			errs = append(errs, importError{Row: r.rec.Row, Column: columnOfField(field), Message: m})
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return &importRowRefusal{errs: errs}
}

// importTally is what the rows did.
type importTally struct {
	rows, created, updated, failed int
	errs                           []importError
}

func (t importTally) result(dryRun bool) gen.StaffImportResult {
	errs := make([]gen.StaffImportError, 0, len(t.errs))
	for _, e := range t.errs {
		item := gen.StaffImportError{Row: int32(e.Row), Message: e.Message}
		if e.Column != "" {
			column := e.Column
			item.Column = &column
		}
		errs = append(errs, item)
	}
	return gen.StaffImportResult{
		DryRun: dryRun, Rows: int32(t.rows), Created: int32(t.created), Updated: int32(t.updated),
		Failed: int32(t.failed), Errors: errs,
	}
}

// importFile runs every row in file order. Every row is planned first — its
// key and sealed cells read, no database — then the file's rows are checked
// against each other (inFileDuplicates) before any row is applied. A refusal
// is that row's failure and the next row runs; any other error ends the
// request, and so does a request whose caller has gone, checked before each
// row's transaction opens (the rows already run stay as they are). An error
// comes with the tally so far, which the caller records.
func (s *server) importFile(ctx context.Context, file csvFile, l importLayout, o importOptions) (importTally, error) {
	tally := importTally{rows: len(file.Rows), errs: []importError{}}
	plans := make([]importPlan, len(file.Rows))
	o.numbers = map[string]int{}
	for i, rec := range file.Rows {
		p, err := s.planImportRow(l, rec)
		if err != nil {
			return tally, err
		}
		plans[i] = p
	}
	inFileDuplicates(l, plans)
	for _, p := range plans {
		if number := strings.ToUpper(p.employeeNumber); len(p.errs) == 0 && number != "" {
			if _, seen := o.numbers[number]; !seen {
				o.numbers[number] = p.rec.Row
			}
		}
	}
	for _, p := range plans {
		errs := p.errs
		if len(errs) == 0 {
			if err := ctx.Err(); err != nil {
				return tally, err
			}
			outcome, err := s.importRow(ctx, l, p, o)
			var refused *importRowRefusal
			switch {
			case errors.As(err, &refused):
				errs = refused.errs
			case err != nil:
				return tally, err
			case outcome == importCreated:
				tally.created++
			default:
				tally.updated++
			}
		}
		if len(errs) > 0 {
			tally.failed++
			sort.SliceStable(errs, func(i, j int) bool { return l.position(errs[i].Column) < l.position(errs[j].Column) })
			for _, e := range errs {
				if e.Column != "" {
					e.Column = l.spelt(e.Column)
				}
				tally.errs = append(tally.errs, e)
			}
		}
	}
	return tally, nil
}

// PostStaffPeopleImport Import people from CSV
// (POST /api/v1/staff/people/import)
//
// The order of refusals is the order that costs least: the flag, the part,
// the file's bytes, then its header against the table and the caller —
// every one a 400 before a single row is read — and only then the rows,
// whose problems are the result rather than a refusal. A real run is
// recorded as the installation's people_imported event (reading 11) — also
// one a caller who went away cut short, with the counts so far
// (recordPeopleImported).
func (s *server) PostStaffPeopleImport(ctx context.Context, req gen.PostStaffPeopleImportRequestObject) (gen.PostStaffPeopleImportResponseObject, error) {
	dryRun := true
	if v := req.Params.DryRun; v != nil {
		switch *v {
		case "true":
		case "false":
			dryRun = false
		default:
			var f fieldErrors
			f.add("dryRun", fmt.Sprintf("'dryRun' must be one of 'true' or 'false', but was '%s'", *v))
			return gen.PostStaffPeopleImport400ApplicationProblemPlusJSONResponse(invalid(invalidImportFileTitle, f)), nil
		}
	}
	data, f := readImportPart(req.Body)
	if f.failed() {
		return gen.PostStaffPeopleImport400ApplicationProblemPlusJSONResponse(invalid(invalidImportFileTitle, f)), nil
	}
	file, f := readCSVFile(data, s.importMaxRows())
	if f.failed() {
		return gen.PostStaffPeopleImport400ApplicationProblemPlusJSONResponse(invalid(invalidImportFileTitle, f)), nil
	}
	layout, f := importLayoutFor(file.Header, s.has(ctx, "staff:personal-data"))
	if f.failed() {
		return gen.PostStaffPeopleImport400ApplicationProblemPlusJSONResponse(invalid(invalidImportFileTitle, f)), nil
	}

	vocab, err := importVocabularyFor(ctx, store.New(s.deps.Pool), layout)
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	tally, err := s.importFile(ctx, file, layout, importOptions{who: who, dryRun: dryRun, vocab: vocab})
	if !dryRun {
		err = errors.Join(err, s.recordPeopleImported(ctx, who, tally))
	}
	if err != nil {
		return nil, fmt.Errorf("staff: import people: %w", err)
	}
	return gen.PostStaffPeopleImport200JSONResponse(tally.result(dryRun)), nil
}

// importEventTimeout bounds the people_imported event's write, which runs on
// a context detached from the request's.
const importEventTimeout = 10 * time.Second

// recordPeopleImported records a real run's people_imported event with
// tally's counts. The rows a run committed stay committed whatever ends it,
// so the event is written on a context the request's end does not cancel —
// a caller who went away mid-file still leaves the history saying what the
// file did — and bounded, as the module's other writes after the fact are.
func (s *server) recordPeopleImported(ctx context.Context, who actor, tally importTally) error {
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), importEventTimeout)
	defer cancel()
	return s.withLockedTx(detached, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		return s.recordEvent(ctx, txq, nil, eventPeopleImported, who, map[string]any{
			"rows": tally.rows, "created": tally.created, "updated": tally.updated, "failed": tally.failed,
		}, nil)
	})
}
