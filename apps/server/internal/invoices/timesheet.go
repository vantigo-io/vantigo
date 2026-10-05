package invoices

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the timesheet (invoices work design D5): an invoice's held
// hours printed inside its PDF, one row per time entry — the date, the
// person, the work type, the task title (else the project's name) and the
// hours. The rows are a snapshot, invoices.timesheet_rows, written when the
// flag is on: by the wizard, by a save that turns the flag on and by a
// refresh, each from the hours as Time's billable read answers them and the
// people as the user directory names them, both read before the writer's
// transaction; every save prunes them to the hours the draft still holds, and
// the issue freezes them with the document. The entry's note is never read —
// contracts.BillableHour does not carry it — and never printed: it is the
// person's own text and may hold health data.

// The person labels (the column's CHECK, ck_settings_timesheet_person_label).
const (
	labelInitials = "initials"
	labelNumber   = "number"
	labelName     = "name"
)

// The timesheet_rows columns' widths: what a snapshot cuts its text to.
const (
	maxPersonLabel   = 200
	maxWorkType      = 100
	maxTimesheetText = 200
)

// unknownPerson is how a person the user directory no longer knows is
// labelled, in every mode.
const unknownPerson = "?"

// cut is s at most n characters, as Postgres counts them.
func cut(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}

// initials is a display name's initials: the first letter of each part,
// upper-cased, the parts split at spaces and hyphens — "Kari Nordmann" is
// KN, "Anne-Lise Berg" ALB.
func initials(name string) string {
	var b strings.Builder
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || r == '-' }) {
		r, _ := utf8.DecodeRuneInString(part)
		b.WriteRune(unicode.ToUpper(r))
	}
	if b.Len() == 0 {
		return unknownPerson
	}
	return b.String()
}

// personLabels names each person of order — the people in order of first
// appearance on the timesheet — by mode (D5): initials (the default, and
// what any other mode falls back to) — "KN", a second KN becoming "KN2";
// number — "Person 1", "Person 2"; or name — the display name, a second
// person of the same name followed by " 2". names is the user directory's
// answer; a person it does not know is "?". No two people ever share a
// label — the PDF totals per label — so a suffixed label that is already
// someone's ("K N 2" is KN2 too) takes the next free number.
func personLabels(mode string, names map[uuid.UUID]string, order []uuid.UUID) map[uuid.UUID]string {
	out := make(map[uuid.UUID]string, len(order))
	taken := map[string]bool{}
	for _, id := range order {
		if _, done := out[id]; done {
			continue
		}
		name, known := names[id]
		if !known || strings.TrimSpace(name) == "" {
			name = unknownPerson
		}
		var base, separator string
		switch mode {
		case labelNumber:
			base = fmt.Sprintf("Person %d", len(out)+1)
		case labelName:
			base, separator = cut(name, maxPersonLabel-8), " "
		default:
			base = initials(name)
		}
		label := base
		for n := 2; taken[label]; n++ {
			label = base + separator + strconv.Itoa(n)
		}
		taken[label] = true
		out[id] = label
	}
	return out
}

// timesheetOrder is hours in the timesheet's order — by date, then by the
// entry's id — and the people in order of first appearance in it.
func timesheetOrder(hours []contracts.BillableHour) ([]contracts.BillableHour, []uuid.UUID) {
	sorted := slices.Clone(hours)
	slices.SortFunc(sorted, func(a, b contracts.BillableHour) int {
		return cmp.Or(utcDay(a.Date).Compare(utcDay(b.Date)), cmp.Compare(a.ID, b.ID))
	})
	var people []uuid.UUID
	for _, h := range sorted {
		if !slices.Contains(people, h.UserID) {
			people = append(people, h.UserID)
		}
	}
	return sorted, people
}

// timesheetRows is hours as the rows InsertTimesheetRows writes, in the
// order given, positions from 1: each person by labels, the work type, and
// as the description the task title, else the project's name (projectNames)
// — the hour's project, else the one its held row took (heldProjects, by
// entry) — else the work type, and only then nothing; never the note, which
// the billable read does not carry.
func timesheetRows(hours []contracts.BillableHour, labels map[uuid.UUID]string, projectNames map[int32]string,
	heldProjects map[int64]int32,
) (store.InsertTimesheetRowsParams, error) {
	var p store.InsertTimesheetRowsParams
	for i, h := range hours {
		description := cut(h.TaskTitle, maxTimesheetText)
		if description == "" {
			description = cut(projectNames[h.ProjectID], maxTimesheetText)
		}
		if held, ok := heldProjects[h.ID]; ok && description == "" {
			description = cut(projectNames[held], maxTimesheetText)
		}
		if description == "" {
			description = cut(h.WorkTypeName, maxTimesheetText)
		}
		label, ok := labels[h.UserID]
		if !ok {
			label = unknownPerson
		}
		hours, err := numericFromRat(big.NewRat(h.HoursHundredths, 100), 2)
		if err != nil {
			return store.InsertTimesheetRowsParams{}, err
		}
		p.Positions = append(p.Positions, int32(i+1))
		p.SourceIds = append(p.SourceIds, h.ID)
		p.PersonLabels = append(p.PersonLabels, label)
		p.EntryDates = append(p.EntryDates, pgDate(utcDay(h.Date)))
		p.Hours = append(p.Hours, hours)
		p.WorkTypes = append(p.WorkTypes, cut(h.WorkTypeName, maxWorkType))
		p.Descriptions = append(p.Descriptions, description)
	}
	return p, nil
}

// timesheetRead is what a writer read before its transaction to write a
// draft's timesheet: the label mode, the held hours as Time answers them now,
// the people's names and the projects' names. A held hour the read did not
// answer — no longer billable — gets no row; the issue refuses it anyway.
type timesheetRead struct {
	mode     string
	hours    map[int64]contracts.BillableHour
	names    map[uuid.UUID]string
	projects map[int32]string
}

// readTimesheet names the people of hours through the user directory and
// the projects through the project directory — each hour's and each held
// hour row's (held) that projectNames, what the caller read already, does
// not name — both on the pool, before the writer's transaction (the lock
// rule). With Projects switched off the projects stay unnamed.
func (s *server) readTimesheet(ctx context.Context, mode string, hours []contracts.BillableHour, held []heldSource,
	projectNames map[int32]string,
) (*timesheetRead, error) {
	read := &timesheetRead{mode: mode, hours: make(map[int64]contracts.BillableHour, len(hours)), names: map[uuid.UUID]string{}, projects: map[int32]string{}}
	maps.Copy(read.projects, projectNames)
	var people []uuid.UUID
	var projects []int32
	unnamed := func(id int32) {
		if _, named := read.projects[id]; !named && !slices.Contains(projects, id) {
			projects = append(projects, id)
		}
	}
	for _, h := range hours {
		read.hours[h.ID] = h
		if !slices.Contains(people, h.UserID) {
			people = append(people, h.UserID)
		}
		unnamed(h.ProjectID)
	}
	for _, h := range held {
		if h.kind == contracts.WorkSourceHours {
			unnamed(h.projectID)
		}
	}
	users, err := s.userEntries(ctx, people)
	if err != nil {
		return nil, fmt.Errorf("invoices: name the people of the timesheet: %w", err)
	}
	for _, u := range users {
		read.names[u.ID] = u.DisplayName
	}
	if s.deps.Projects != nil && len(projects) > 0 {
		slices.Sort(projects)
		entries, err := s.projectEntries(ctx, projects)
		if err != nil {
			return nil, fmt.Errorf("invoices: name the projects of the timesheet: %w", err)
		}
		for _, e := range entries {
			read.projects[e.ID] = e.Name
		}
	}
	return read, nil
}

// heldHours is the hours rows hold as Time's billable read answers them now,
// read by id on the pool — nothing when Time is switched off or the rows
// hold no hours.
func (s *server) heldHours(ctx context.Context, rows []heldSource) ([]contracts.BillableHour, error) {
	var ids []int64
	for _, r := range rows {
		if r.kind == contracts.WorkSourceHours {
			ids = append(ids, r.id)
		}
	}
	if len(ids) == 0 || s.deps.BillableHours == nil {
		return nil, nil
	}
	page, err := s.billableHours(ctx, contracts.BillableRequest{IDs: ids})
	if err != nil {
		return nil, fmt.Errorf("invoices: read the timesheet's hours: %w", err)
	}
	return page.Hours, nil
}

// writeTimesheet writes a draft's timesheet anew, on txq inside the writer's
// transaction: every row deleted, then one row per hour rows hold that read
// answered, in the timesheet's order, every person labelled afresh — so a
// timesheet never carries two numbering schemes.
func writeTimesheet(ctx context.Context, txq *store.Queries, invoiceID int64, read *timesheetRead, rows []heldSource) error {
	if err := txq.DeleteTimesheetRows(ctx, invoiceID); err != nil {
		return fmt.Errorf("invoices: clear draft %d's timesheet: %w", invoiceID, err)
	}
	var hours []contracts.BillableHour
	heldProjects := map[int64]int32{}
	for _, r := range rows {
		if h, ok := read.hours[r.id]; ok && r.kind == contracts.WorkSourceHours {
			hours = append(hours, h)
			heldProjects[r.id] = r.projectID
		}
	}
	if len(hours) == 0 {
		return nil
	}
	sorted, people := timesheetOrder(hours)
	p, err := timesheetRows(sorted, personLabels(read.mode, read.names, people), read.projects, heldProjects)
	if err != nil {
		return err
	}
	p.InvoiceID = invoiceID
	if err := txq.InsertTimesheetRows(ctx, p); err != nil {
		return fmt.Errorf("invoices: write draft %d's timesheet: %w", invoiceID, err)
	}
	return nil
}

// numberedLabel is a label of the number mode.
var numberedLabel = regexp.MustCompile(`^Person [0-9]+$`)

// pruneTimesheet drops a draft's rows of hours rows no longer hold (D5) and,
// when it dropped any, writes the rest again densely: positions from 1 in
// their order and, on a timesheet labelled by number, "Person n" renumbered
// by first appearance among them — no directory read, the snapshot's own
// rows. An initials or name label is kept as written: the snapshot names
// that person so, and a KN2 may stay without a KN.
func pruneTimesheet(ctx context.Context, txq *store.Queries, invoiceID int64, rows []heldSource) error {
	var kept []int64
	for _, r := range rows {
		if r.kind == contracts.WorkSourceHours {
			kept = append(kept, r.id)
		}
	}
	dropped, err := txq.PruneTimesheetRows(ctx, store.PruneTimesheetRowsParams{InvoiceID: invoiceID, Kept: kept})
	if err != nil {
		return fmt.Errorf("invoices: prune draft %d's timesheet: %w", invoiceID, err)
	}
	if dropped == 0 {
		return nil
	}
	left, err := txq.TimesheetRowsOf(ctx, invoiceID)
	if err != nil || len(left) == 0 {
		return err
	}
	numbered := !slices.ContainsFunc(left, func(r store.InvoicesTimesheetRow) bool { return !numberedLabel.MatchString(r.PersonLabel) })
	relabel := map[string]string{}
	p := store.InsertTimesheetRowsParams{InvoiceID: invoiceID}
	for i, r := range left {
		label := r.PersonLabel
		if numbered {
			if _, ok := relabel[label]; !ok {
				relabel[label] = fmt.Sprintf("Person %d", len(relabel)+1)
			}
			label = relabel[label]
		}
		work := ""
		if r.WorkType != nil {
			work = *r.WorkType
		}
		p.Positions, p.SourceIds = append(p.Positions, int32(i+1)), append(p.SourceIds, r.SourceID)
		p.PersonLabels, p.EntryDates = append(p.PersonLabels, label), append(p.EntryDates, r.EntryDate)
		p.Hours, p.WorkTypes = append(p.Hours, r.Hours), append(p.WorkTypes, work)
		p.Descriptions = append(p.Descriptions, r.Description)
	}
	if err := txq.DeleteTimesheetRows(ctx, invoiceID); err != nil {
		return fmt.Errorf("invoices: clear draft %d's timesheet: %w", invoiceID, err)
	}
	if err := txq.InsertTimesheetRows(ctx, p); err != nil {
		return fmt.Errorf("invoices: renumber draft %d's timesheet: %w", invoiceID, err)
	}
	return nil
}

// saveTimesheet is a writer's timesheet step under its lock, after the held
// rows are written: off deletes every row; on writes them anew when the
// writer read them (read non-nil), else prunes them to the hours rows hold.
func saveTimesheet(ctx context.Context, txq *store.Queries, invoiceID int64, on bool, read *timesheetRead, rows []heldSource) error {
	switch {
	case !on:
		if err := txq.DeleteTimesheetRows(ctx, invoiceID); err != nil {
			return fmt.Errorf("invoices: clear draft %d's timesheet: %w", invoiceID, err)
		}
		return nil
	case read != nil:
		return writeTimesheet(ctx, txq, invoiceID, read, rows)
	}
	return pruneTimesheet(ctx, txq, invoiceID, rows)
}

// timesheetResponse is a document's timesheet rows on the wire, [] for none.
func timesheetResponse(rows []store.InvoicesTimesheetRow) ([]gen.InvoicesTimesheetRow, error) {
	out := make([]gen.InvoicesTimesheetRow, 0, len(rows))
	for _, r := range rows {
		hours, err := ratFromNumeric(r.Hours)
		if err != nil {
			return nil, err
		}
		out = append(out, gen.InvoicesTimesheetRow{
			Position: r.Position, PersonLabel: r.PersonLabel, Date: wireDate(r.EntryDate.Time),
			Hours: floatFromRat(hours, 2), WorkType: r.WorkType, Description: r.Description,
		})
	}
	return out, nil
}
