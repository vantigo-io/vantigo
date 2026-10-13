package staff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the retention run's erase (design S15 as amended by the A2
// pre-flight; plan Task 5, readings 3, 4, 19-22): every column of the
// register classed, the three schedules as three due queries over what
// remains, and one person's erases in one transaction on the lease's
// connection. The worker and the endpoint that run it are
// retention_worker.go's; the ledger that finishes the objects is
// object_deletions.go's.
//
// The classes are S15's: A pay and contract documentation, kept five years
// after the year the employment ended; C who was employed when, kept; D
// personnel administration, erased a grace period after the last employment
// ended; E health, erased healthRetentionDays after it (until delivery B
// gives health documents a sick case's schedule); F the hold, which stops
// every erase and is erased by none. columnClasses is the erase's one source:
// the statements that null a class's columns are built from it, a test walks
// the database's columns against it, and another holds fieldClasses — what
// an event's payload_classes say — to it.
//
// An erase never calls out of the module: a document's row is deleted and its
// object's key queued in the ledger in the same transaction, and the ledger
// removes the object afterwards, holding no connection.

// classOfCategory is a documents column's class: the category's. A document
// is erased whole — its row deleted, its object queued — by the erase of its
// category's class, so its columns share that class row by row.
const classOfCategory = "category"

// columnClasses is the retention class of every column of the tables the
// erase reaches, keyed table.column (design §3.1-§3.3, §3.6, S15). Ids,
// foreign keys, revisions and timestamps are C: they say that a row existed
// and when, which is who-was-employed-when.
var columnClasses = map[string]string{
	// The person (§3.1). The national identity number's kind stays, so the
	// record still says what kind of number was on file; the number, its MAC
	// and a foreign id's country go. The self-certification withdrawal is
	// class E on delivery B's schedule — nothing writes it before B, and no
	// A2 erase reaches it. The legal hold is F.
	"people.id": "C", "people.employee_number": "C", "people.given_name": "C", "people.family_name": "C",
	"people.display_name": "C", "people.birth_date": "C", "people.gender": "C", "people.national_id_kind": "C",
	"people.national_id_sealed": "D", "people.national_id_mac": "D", "people.national_id_country": "D",
	"people.work_email": "D", "people.phone_work": "D", "people.phone_mobile": "D", "people.phone_private": "D",
	"people.private_email": "D", "people.street": "D", "people.postal_code": "D", "people.city": "D", "people.country": "D",
	"people.work_country": "C", "people.preferred_language": "C",
	"people.bank_account_sealed": "D", "people.bank_account_last4": "D", "people.bank_account_kind": "D",
	"people.children_under_12": "D", "people.sole_carer": "D", "people.chronically_ill_children": "D",
	"people.self_cert_withdrawn_until": "E", "people.self_cert_withdrawn_reason": "E",
	"people.provision_account": "C", "people.provision_from": "C", "people.account_email_kind": "C",
	"people.legal_hold": "F", "people.legal_hold_reason": "F", "people.legal_hold_by_user_id": "F", "people.legal_hold_at": "F",
	"people.erased_at": "C", "people.reassessed_at": "C", "people.reassessed_by_user_id": "C",
	"people.notes": "D", "people.revision": "C", "people.created_at": "C", "people.updated_at": "C",

	// The next of kin are class D whole (§3.1): every column, so the D erase
	// deletes the rows.
	"next_of_kin.id": "D", "next_of_kin.person_id": "D", "next_of_kin.position": "D", "next_of_kin.name": "D",
	"next_of_kin.relationship": "D", "next_of_kin.phone": "D", "next_of_kin.note": "D",
	"next_of_kin.created_at": "D", "next_of_kin.updated_at": "D",

	// The roles, the employments and the link are class C whole (§3.1,
	// §3.2, §3.6).
	"person_roles.id": "C", "person_roles.person_id": "C", "person_roles.role": "C", "person_roles.from_date": "C",
	"person_roles.to_date": "C", "person_roles.created_at": "C", "person_roles.updated_at": "C",

	"employments.id": "C", "employments.person_id": "C", "employments.employment_number": "C",
	"employments.relationship_type": "C", "employments.business_unit_id": "C", "employments.start_date": "C",
	"employments.end_date": "C", "employments.end_reason": "C", "employments.contract_signed_at": "C",
	"employments.holiday_scheme_id": "C", // delivery B: which scheme the holiday followed
	"employments.revision":          "C", "employments.created_at": "C", "employments.updated_at": "C",

	"identity_links.id": "C", "identity_links.person_id": "C", "identity_links.provider": "C",
	"identity_links.external_id": "C", "identity_links.state": "C", "identity_links.managed": "C",
	"identity_links.blocked_reason": "C", "identity_links.last_error": "C", "identity_links.attempts": "C",
	"identity_links.next_attempt_at": "C", "identity_links.lease_id": "C", "identity_links.lease_until": "C",
	"identity_links.last_synced_at": "C", "identity_links.last_spec_hash": "C",
	"identity_links.created_at": "C", "identity_links.updated_at": "C",

	// The terms (§3.3): what the job was is C; the pay, the seniority date
	// and the pay grade are A, kept five years past the end year; the note is
	// HR's, D.
	"employment_terms.id": "C", "employment_terms.employment_id": "C", "employment_terms.valid_from": "C",
	"employment_terms.employment_type": "C", "employment_terms.temporary_ground": "C",
	"employment_terms.expected_end_date": "C", "employment_terms.appointment_form": "C",
	"employment_terms.hired_out_to": "C", "employment_terms.fte_percent": "C",
	"employment_terms.working_hours_scheme": "C", "employment_terms.full_time_hours_per_week": "C",
	"employment_terms.occupation_code": "C", "employment_terms.title": "C", "employment_terms.employee_type": "C",
	"employment_terms.remuneration_type": "A", "employment_terms.pay_components": "A",
	"employment_terms.pay_method": "A", "employment_terms.payday_text": "A",
	"employment_terms.department_id": "C", "employment_terms.location_id": "C",
	"employment_terms.manager_person_id": "C", "employment_terms.cost_centre": "C", "employment_terms.division": "C",
	"employment_terms.working_time_text": "C", "employment_terms.break_minutes": "C",
	"employment_terms.special_hours_text": "C", "employment_terms.overtime_rule_text": "C",
	"employment_terms.probation_end_date": "C", "employment_terms.probation_extension_agreed": "C",
	"employment_terms.probation_extended_to": "C", "employment_terms.probation_extension_reason": "C",
	"employment_terms.notice_employee": "C", "employment_terms.notice_employer": "C",
	"employment_terms.notice_unit": "C", "employment_terms.tariff_reference": "C",
	"employment_terms.holiday_terms_text": "C", "employment_terms.competence_development_text": "C",
	"employment_terms.social_security_text": "C",
	"employment_terms.seniority_date":       "A", "employment_terms.pay_grade": "A",
	"employment_terms.note":     "D",
	"employment_terms.revision": "C", "employment_terms.created_at": "C", "employment_terms.updated_at": "C",

	// A document is its category's class, row by row (§3.6).
	"documents.id": classOfCategory, "documents.person_id": classOfCategory, "documents.category_id": classOfCategory,
	"documents.employment_id": classOfCategory, "documents.object_key": classOfCategory,
	"documents.file_name": classOfCategory, "documents.content_type": classOfCategory,
	"documents.size_bytes": classOfCategory, "documents.valid_to": classOfCategory,
	"documents.signed_at": classOfCategory, "documents.uploaded_by_user_id": classOfCategory,
	"documents.revision": classOfCategory, "documents.created_at": classOfCategory,
	"documents.updated_at": classOfCategory,
	// The case a health document belongs to (delivery B, S15): the case's
	// schedule reaches the document through it; still the category's class.
	"documents.case_id": classOfCategory,
}

// The kinds an erase reports, in the erased event and the run's answer.
const (
	eraseKindD      = "D"
	eraseKindHealth = "health"
	eraseKindA      = "A"
)

// eraseKinds is the order the kinds are reported in.
var eraseKinds = []string{eraseKindD, eraseKindHealth, eraseKindA}

// documentEventKinds are the kinds of event about one document: the A erase
// of a person's last employment finds those of a document that is gone by
// them.
var documentEventKinds = []string{eventDocumentAdded, eventDocumentUpdated, eventDocumentRemoved}

// The schedules' constants (S15): the health class, and the years an
// employment's pay documentation outlives the year it ended.
const (
	healthClass  = "E"
	payYearsKept = 5
	// everyRow is the limit a due query asked about one person runs with.
	everyRow = math.MaxInt32
)

// erasedKind is one kind of erase and how many items it removed: each column
// value nulled, each row deleted (a next of kin, a document) and each event
// payload rewritten.
type erasedKind struct {
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
}

// columnsOf is table's columns of class, sorted.
func columnsOf(table, class string) []string {
	var out []string
	for key, c := range columnClasses {
		if t, column, _ := strings.Cut(key, "."); t == table && c == class {
			out = append(out, column)
		}
	}
	slices.Sort(out)
	return out
}

// wholeTable reports whether every column of table is of class: a table whose
// rows that class's erase deletes rather than nulls — the next of kin.
func wholeTable(table, class string) bool {
	found := false
	for key, c := range columnClasses {
		if t, _, _ := strings.Cut(key, "."); t == table {
			if c != class {
				return false
			}
			found = true
		}
	}
	return found
}

// tablesWith is the tables, but documents, that hold a column of class,
// sorted: what a class's erase nulls or deletes.
func tablesWith(class string) []string {
	var out []string
	for key, c := range columnClasses {
		if t, _, _ := strings.Cut(key, "."); c == class && t != "documents" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	slices.Sort(out)
	return out
}

// hasColumn reports whether table has column.
func hasColumn(table, column string) bool {
	_, ok := columnClasses[table+"."+column]
	return ok
}

// fieldsOfClass is the payload fields of class, sorted.
func fieldsOfClass(class string) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(fieldClasses)) {
		if fieldClasses[name] == class {
			out = append(out, name)
		}
	}
	return out
}

// personScope is the rows of table that are one person's ($1), for the D erase.
func personScope(table string) string {
	switch table {
	case "people":
		return "id = $1"
	case "employment_terms":
		return "employment_id IN (SELECT id FROM staff.employments WHERE person_id = $1)"
	}
	return "person_id = $1"
}

// employmentScope is the rows of table that are one employment's ($1), for
// the A erase; the A columns live on the terms alone (the column test holds
// that).
func employmentScope(table string) (string, error) {
	switch table {
	case "employment_terms":
		return "employment_id = $1", nil
	case "employments":
		return "id = $1", nil
	}
	return "", fmt.Errorf("staff: the A erase has no employment scope for %s", table)
}

// eraseColumns erases class in table's rows that scope selects (its $1 is
// arg): a table of that class whole has those rows deleted; otherwise the
// class's columns are nulled where any holds a value, and the row's revision
// and updated_at move on. The statements are built from columnClasses — no
// request reaches them, and each identifier is quoted all the same — and
// answer the items removed: rows deleted, or values nulled.
func eraseColumns(ctx context.Context, tx pgx.Tx, table, class, scope string, arg int64, now time.Time) (int64, error) {
	relation := pgx.Identifier{"staff", table}.Sanitize()
	if wholeTable(table, class) {
		tag, err := tx.Exec(ctx, "DELETE FROM "+relation+" WHERE "+scope, arg)
		if err != nil {
			return 0, fmt.Errorf("staff: erase %s's rows of class %s: %w", table, class, err)
		}
		return tag.RowsAffected(), nil
	}
	columns := columnsOf(table, class)
	if len(columns) == 0 {
		return 0, nil
	}
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = pgx.Identifier{column}.Sanitize()
	}
	list := strings.Join(quoted, ", ")
	var values int64
	if err := tx.QueryRow(ctx, "SELECT coalesce(sum(num_nonnulls("+list+")), 0) FROM "+relation+" WHERE "+scope, arg).Scan(&values); err != nil {
		return 0, fmt.Errorf("staff: count %s's values of class %s: %w", table, class, err)
	}
	if values == 0 {
		return 0, nil
	}
	sets := make([]string, 0, len(columns)+2)
	for _, column := range quoted {
		sets = append(sets, column+" = NULL")
	}
	if hasColumn(table, "revision") {
		sets = append(sets, "revision = revision + 1")
	}
	args := []any{arg}
	if hasColumn(table, "updated_at") {
		sets = append(sets, "updated_at = $2::timestamptz")
		args = append(args, now)
	}
	statement := "UPDATE " + relation + " SET " + strings.Join(sets, ", ") + " WHERE (" + scope + ") AND num_nonnulls(" + list + ") > 0"
	if _, err := tx.Exec(ctx, statement, args...); err != nil {
		return 0, fmt.Errorf("staff: erase %s's values of class %s: %w", table, class, err)
	}
	return values, nil
}

// retentionPlan is what one cycle's due queries are asked with, from the
// settings and the clock: "today" the settings zone's civil day.
type retentionPlan struct {
	dCutoff      time.Time
	healthCutoff time.Time
	thisYear     int32
	aColumns     []string
	aFields      []string
}

// planRetention reads the settings on q and derives the cycle's dates.
func (s *server) planRetention(ctx context.Context, q *store.Queries) (retentionPlan, error) {
	row, err := settings(ctx, q)
	if err != nil {
		return retentionPlan{}, err
	}
	today := civilToday(s.deps.Clock(), zoneOf(row))
	return retentionPlan{
		dCutoff:      today.AddDate(0, 0, -int(row.RetentionGraceDays)),
		healthCutoff: today.AddDate(0, 0, -int(row.HealthRetentionDays)),
		thisYear:     int32(today.Year()),
		aColumns:     columnsOf("employment_terms", "A"),
		aFields:      fieldsOfClass("A"),
	}, nil
}

func (p retentionPlan) dueD(person *int64, limit int32) store.DuePeopleDParams {
	return store.DuePeopleDParams{PersonID: person, Cutoff: dayParam(p.dCutoff), RowLimit: limit}
}

func (p retentionPlan) dueHealth(person *int64, limit int32) store.DuePeopleHealthParams {
	return store.DuePeopleHealthParams{PersonID: person, Cutoff: dayParam(p.healthCutoff), Blanked: changed, RowLimit: limit}
}

func (p retentionPlan) dueA(person *int64, limit int32) store.DueEmploymentsAParams {
	return store.DueEmploymentsAParams{
		BeforeYear: p.thisYear - payYearsKept, PersonID: person,
		AColumns: p.aColumns, AFields: p.aFields, Blanked: changed, DocumentKinds: documentEventKinds, RowLimit: limit,
	}
}

// eraseCycle is one erase cycle on conn, the lease's connection: the people
// something is due for — each schedule's oldest first, at most
// retentionBatch people — and each one's erases in one transaction of its
// own, on conn. One person failing is logged by id and the cycle moves on;
// they are due again next cycle. It answers what was erased, per kind,
// summed.
func (s *server) eraseCycle(ctx context.Context, conn *pgxpool.Conn, who actor) ([]erasedKind, error) {
	q := store.New(conn)
	plan, err := s.planRetention(ctx, q)
	if err != nil {
		return nil, err
	}
	d, err := q.DuePeopleD(ctx, plan.dueD(nil, retentionBatch))
	if err != nil {
		return nil, fmt.Errorf("staff: select the people due for the D erase: %w", err)
	}
	health, err := q.DuePeopleHealth(ctx, plan.dueHealth(nil, retentionBatch))
	if err != nil {
		return nil, fmt.Errorf("staff: select the people due for the health erase: %w", err)
	}
	a, err := q.DueEmploymentsA(ctx, plan.dueA(nil, retentionBatch))
	if err != nil {
		return nil, fmt.Errorf("staff: select the employments due for the A erase: %w", err)
	}
	people := slices.Concat(d, health)
	for _, row := range a {
		people = append(people, row.PersonID)
	}
	var due []int64
	for _, id := range people {
		if !slices.Contains(due, id) && len(due) < retentionBatch {
			due = append(due, id)
		}
	}

	totals := map[string]int64{}
	erased, failed := 0, 0
	for _, id := range due {
		if ctx.Err() != nil {
			break
		}
		kinds, err := s.erasePersonRecovering(ctx, conn, id, plan, who)
		if err != nil {
			if ctx.Err() == nil {
				s.logger().ErrorContext(ctx, "staff: erasing a person failed; they are due again next cycle",
					"worker", retentionWorkerName, "person_id", id, "error", err.Error())
			}
			failed++
			continue
		}
		if len(kinds) > 0 {
			erased++
		}
		for _, k := range kinds {
			totals[k.Kind] += k.Count
		}
	}
	s.logger().InfoContext(ctx, "staff: retention erase cycle finished", "worker", retentionWorkerName, "erased", erased, "failed", failed)
	out := []erasedKind{}
	for _, kind := range eraseKinds {
		if count, ok := totals[kind]; ok {
			out = append(out, erasedKind{Kind: kind, Count: count})
		}
	}
	return out, nil
}

// erasePersonRecovering is erasePerson with a panic turned into that person's
// error: the transaction's deferred rollback has run, nothing of the person
// changed, and the cycle moves on — one bug must not stop the worker for
// good, since the runner never restarts it.
func (s *server) erasePersonRecovering(ctx context.Context, conn *pgxpool.Conn, personID int64, plan retentionPlan, who actor) (kinds []erasedKind, err error) {
	defer func() {
		if r := recover(); r != nil {
			kinds, err = nil, fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	return s.erasePerson(ctx, conn, personID, plan, who)
}

// erasePerson runs every erase due for one person in one transaction on conn
// under the person's lock, each schedule judged again on the row the lock
// holds — a hold set, an employment reopened or a rehire since the cycle's
// selection is seen here — and records one erased event naming the kinds
// when anything was erased. The lock allows an erased person (allowErased):
// the health and the A erases run on people the D erase marked, and the D
// erase's own due query is what refuses a person already erased.
func (s *server) erasePerson(ctx context.Context, conn *pgxpool.Conn, personID int64, plan retentionPlan, who actor) ([]erasedKind, error) {
	var kinds []erasedKind
	err := s.withPersonLockOn(ctx, conn, personID, allowErased, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		kinds = nil
		now := s.deps.Clock()
		d, err := txq.DuePeopleD(ctx, plan.dueD(&personID, 1))
		if err != nil {
			return fmt.Errorf("staff: judge person %d's D erase: %w", personID, err)
		}
		if len(d) == 1 {
			n, err := s.eraseD(ctx, tx, txq, personID, now)
			if err != nil {
				return err
			}
			kinds = append(kinds, erasedKind{Kind: eraseKindD, Count: n})
		}
		health, err := txq.DuePeopleHealth(ctx, plan.dueHealth(&personID, 1))
		if err != nil {
			return fmt.Errorf("staff: judge person %d's health erase: %w", personID, err)
		}
		if len(health) == 1 {
			n, err := s.eraseHealth(ctx, txq, personID, now)
			if err != nil {
				return err
			}
			if n > 0 {
				kinds = append(kinds, erasedKind{Kind: eraseKindHealth, Count: n})
			}
		}
		employments, err := txq.DueEmploymentsA(ctx, plan.dueA(&personID, everyRow))
		if err != nil {
			return fmt.Errorf("staff: judge person %d's A erase: %w", personID, err)
		}
		var a int64
		for _, e := range employments {
			n, err := s.eraseA(ctx, tx, txq, personID, e.ID, now)
			if err != nil {
				return err
			}
			a += n
		}
		if a > 0 {
			kinds = append(kinds, erasedKind{Kind: eraseKindA, Count: a})
		}
		if len(kinds) == 0 {
			return nil
		}
		return s.recordEvent(ctx, txq, &personID, eventErased, who, map[string]any{"kinds": kinds}, nil)
	})
	if errors.Is(err, errPersonNotFound) {
		// Deleted since the cycle chose them: nothing left to erase.
		return nil, nil
	}
	return kinds, err
}

// eraseD is the D erase (S15): every D column of the person and their terms
// nulled, their next of kin deleted, their documents of class D deleted and
// queued, every value of class D in their history replaced, and erased_at
// set. Class C is untouched.
func (s *server) eraseD(ctx context.Context, tx pgx.Tx, txq *store.Queries, personID int64, now time.Time) (int64, error) {
	var count int64
	for _, table := range tablesWith("D") {
		n, err := eraseColumns(ctx, tx, table, "D", personScope(table), personID, now)
		if err != nil {
			return 0, err
		}
		count += n
	}
	docs, err := txq.PersonDocumentsOfClass(ctx, store.PersonDocumentsOfClassParams{PersonID: personID, Class: "D"})
	if err != nil {
		return 0, fmt.Errorf("staff: read person %d's documents of class D: %w", personID, err)
	}
	removed, err := s.eraseDocuments(ctx, txq, mapRows(docs, func(r store.PersonDocumentsOfClassRow) erasedDocument {
		return erasedDocument{id: r.ID, key: r.ObjectKey}
	}), now)
	if err != nil {
		return 0, err
	}
	count += int64(len(removed))
	n, err := s.blankEvents(ctx, txq, personID, "D", func(eventIDs) bool { return true })
	if err != nil {
		return 0, err
	}
	count += n
	if err := txq.MarkPersonErased(ctx, store.MarkPersonErasedParams{ID: personID, Now: now}); err != nil {
		return 0, fmt.Errorf("staff: mark person %d erased: %w", personID, err)
	}
	return count, nil
}

// eraseHealth is the health erase (plan reading 3): the person's documents
// in restricted categories — class E — deleted and queued, and every value
// of class E in their history replaced.
func (s *server) eraseHealth(ctx context.Context, txq *store.Queries, personID int64, now time.Time) (int64, error) {
	docs, err := txq.PersonDocumentsOfClass(ctx, store.PersonDocumentsOfClassParams{PersonID: personID, Class: healthClass})
	if err != nil {
		return 0, fmt.Errorf("staff: read person %d's health documents: %w", personID, err)
	}
	removed, err := s.eraseDocuments(ctx, txq, mapRows(docs, func(r store.PersonDocumentsOfClassRow) erasedDocument {
		return erasedDocument{id: r.ID, key: r.ObjectKey}
	}), now)
	if err != nil {
		return 0, err
	}
	n, err := s.blankEvents(ctx, txq, personID, healthClass, func(eventIDs) bool { return true })
	if err != nil {
		return 0, err
	}
	return int64(len(removed)) + n, nil
}

// eraseA is one employment's A erase (plan reading 4): its pay columns
// nulled on every terms row, its documents of class A deleted and queued —
// filed against it, or against no employment while it is the person's last
// — and the values of class A in its events replaced, with every value of
// the deleted documents' events. While it is the last, the events of a
// document of class A that is already gone — a contract or an attest deleted
// by hand, which no longer says which employment it was filed against — are
// replaced too. The person's other employments keep theirs.
func (s *server) eraseA(ctx context.Context, tx pgx.Tx, txq *store.Queries, personID, employmentID int64, now time.Time) (int64, error) {
	var count int64
	for _, table := range tablesWith("A") {
		scope, err := employmentScope(table)
		if err != nil {
			return 0, err
		}
		n, err := eraseColumns(ctx, tx, table, "A", scope, employmentID, now)
		if err != nil {
			return 0, err
		}
		count += n
	}
	last, err := txq.LastEmploymentID(ctx, personID)
	if err != nil {
		return 0, fmt.Errorf("staff: read person %d's last employment: %w", personID, err)
	}
	docs, err := txq.EmploymentDocumentsA(ctx, store.EmploymentDocumentsAParams{
		PersonID: personID, EmploymentID: &employmentID, IsLast: last == employmentID,
	})
	if err != nil {
		return 0, fmt.Errorf("staff: read employment %d's documents of class A: %w", employmentID, err)
	}
	removed, err := s.eraseDocuments(ctx, txq, mapRows(docs, func(r store.EmploymentDocumentsARow) erasedDocument {
		return erasedDocument{id: r.ID, key: r.ObjectKey}
	}), now)
	if err != nil {
		return 0, err
	}
	count += int64(len(removed))
	var gone []int64
	if last == employmentID {
		if gone, err = txq.GoneDocumentsWithEventsA(ctx, store.GoneDocumentsWithEventsAParams{
			PersonID: &personID, DocumentKinds: documentEventKinds,
		}); err != nil {
			return 0, fmt.Errorf("staff: read person %d's events of documents gone: %w", personID, err)
		}
	}
	n, err := s.blankEvents(ctx, txq, personID, "A", func(ids eventIDs) bool {
		if ids.DocumentID != nil {
			return slices.Contains(removed, *ids.DocumentID) || slices.Contains(gone, *ids.DocumentID)
		}
		return ids.EmploymentID != nil && *ids.EmploymentID == employmentID
	})
	if err != nil {
		return 0, err
	}
	return count + n, nil
}

// erasedDocument is a document an erase deletes: its row and its object's key.
type erasedDocument struct {
	id  int64
	key string
}

// eraseDocuments deletes each document's row and queues its object in the
// ledger, in the caller's transaction (plan reading 21) — the hand delete's
// two writes, without its attempt: the ledger finishes the object once the
// transaction has committed. It answers the ids deleted.
func (s *server) eraseDocuments(ctx context.Context, txq *store.Queries, docs []erasedDocument, now time.Time) ([]int64, error) {
	out := make([]int64, 0, len(docs))
	for _, d := range docs {
		if err := txq.DeleteDocument(ctx, d.id); err != nil {
			return nil, fmt.Errorf("staff: erase document %d: %w", d.id, err)
		}
		if err := txq.InsertObjectDeletion(ctx, store.InsertObjectDeletionParams{ObjectKey: d.key, Now: now}); err != nil {
			return nil, fmt.Errorf("staff: queue erased document %d's object: %w", d.id, err)
		}
		out = append(out, d.id)
	}
	return out, nil
}

// eventIDs is what an event's payload says it is about.
type eventIDs struct {
	EmploymentID *int64 `json:"employmentId"`
	DocumentID   *int64 `json:"documentId"`
}

// blankEvents replaces class's values in the person's events whose
// payload_classes hold it and that include accepts, answering the payloads
// rewritten. An event keeps its kind, actor, instant and classes — the
// history still says what happened when, and readers still know what kind of
// data it was about.
func (s *server) blankEvents(ctx context.Context, txq *store.Queries, personID int64, class string, include func(eventIDs) bool) (int64, error) {
	rows, err := txq.PersonEventsOfClass(ctx, store.PersonEventsOfClassParams{PersonID: &personID, Class: class})
	if err != nil {
		return 0, fmt.Errorf("staff: read person %d's events of class %s: %w", personID, class, err)
	}
	var count int64
	for _, row := range rows {
		var ids eventIDs
		if err := json.Unmarshal(row.Payload, &ids); err != nil || !include(ids) {
			continue
		}
		payload, rewritten, err := blankPayload(row.Payload, class)
		if err != nil {
			return 0, fmt.Errorf("staff: erase event %d's values of class %s: %w", row.ID, class, err)
		}
		if !rewritten {
			continue
		}
		if err := txq.SetEventPayload(ctx, store.SetEventPayloadParams{ID: row.ID, Payload: payload}); err != nil {
			return 0, fmt.Errorf("staff: rewrite event %d: %w", row.ID, err)
		}
		count++
	}
	return count, nil
}

// blankPayload is a stored payload with class's values replaced by
// "changed" — the field's name kept, its value gone: what shapePayload shows
// a reader who may not read the field, now shown to everyone. A document's
// event is replaced whole, since its fields take its category's class; any
// other event field by field, by fieldClasses. It answers whether anything
// changed: a value already replaced is left, so an erase run twice writes
// nothing the second time.
func blankPayload(stored []byte, class string) ([]byte, bool, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(stored, &payload); err != nil {
		return nil, false, err
	}
	var fields map[string]json.RawMessage
	if raw, ok := payload["fields"]; ok {
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, false, err
		}
	}
	blank, err := json.Marshal(changed)
	if err != nil {
		return nil, false, err
	}
	_, document := payload["documentId"]
	// An absence, adjustment or case event (delivery B) carries each
	// field's class in its own map, written when it was recorded: it is
	// blanked by that map, a field it does not class counting as D.
	var own map[string]string
	_, absence := payload["absenceId"]
	_, adjustment := payload["adjustmentId"]
	_, sickCase := payload["caseId"]
	classed := absence || adjustment || sickCase
	if raw, ok := payload["classes"]; ok && classed {
		if err := json.Unmarshal(raw, &own); err != nil {
			return nil, false, err
		}
	}
	classOf := func(name string) string {
		if !classed {
			return classOfField(name)
		}
		if c, ok := own[name]; ok {
			return c
		}
		return "D"
	}
	rewritten := false
	for name, value := range fields {
		if (document || classOf(name) == class) && !bytes.Equal(value, blank) {
			fields[name] = blank
			rewritten = true
		}
	}
	if !rewritten {
		return stored, false, nil
	}
	if payload["fields"], err = json.Marshal(fields); err != nil {
		return nil, false, err
	}
	out, err := json.Marshal(payload)
	return out, true, err
}

// erasedKindsResponse is the erase cycle's kinds as the contract answers them.
func erasedKindsResponse(kinds []erasedKind) []gen.StaffErasedKind {
	return mapRows(kinds, func(k erasedKind) gen.StaffErasedKind {
		return gen.StaffErasedKind{Kind: gen.StaffErasedKindKind(k.Kind), Count: k.Count}
	})
}
