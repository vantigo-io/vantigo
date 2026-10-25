package staff

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// classedTables are the tables columnClasses covers (plan, Global
// Constraints "Classes on every column"): the person, their next of kin and
// roles, their employments and terms, their link and their documents — and
// delivery B's absence tables (Task 6): the absences, the holiday
// adjustments, the sick-leave cases and their milestones, and the types and
// schemes they name — and Entra delivery D's groups, their members, owners
// and sections per provider — and the person's competences (staff
// competence design S17) — and what the person wants and their competence
// reviews (delivery B, §3.4–3.7).
var classedTables = []string{
	"people", "next_of_kin", "person_roles", "employments", "employment_terms", "identity_links", "documents",
	"absences", "holiday_adjustments", "sick_cases", "sick_case_milestones", "absence_types", "holiday_schemes",
	"groups", "group_members", "group_owners", "group_traits",
	"person_competences", "person_interests", "person_preferences", "availability_declarations", "competence_reviews",
}

// Every column of the tables the erase reaches has a class, and every class
// names a column that exists: a migration that adds a column without saying
// when it is erased fails here, not in an audit. The classes are the five of
// S15 the register holds (B, hours, is Time's) — and a document's columns,
// which take their category's. A column the erase nulls (class D or A outside
// a table whose rows it deletes whole) must be nullable, or the erase would
// fail on it rather than erase it.
func TestColumnClasses_EveryColumnIsClassed(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	rows, err := pool.Query(context.Background(), `
		SELECT table_name || '.' || column_name, is_nullable = 'YES'
		FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name = ANY($1)`, classedTables)
	if err != nil {
		t.Fatalf("read the columns: %v", err)
	}
	defer rows.Close()
	nullable := map[string]bool{}
	for rows.Next() {
		var column string
		var null bool
		if err := rows.Scan(&column, &null); err != nil {
			t.Fatalf("scan a column: %v", err)
		}
		nullable[column] = null
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the columns: %v", err)
	}
	if len(nullable) == 0 {
		t.Fatal("no columns read: the tables are not where this test looks")
	}

	for _, column := range slices.Sorted(maps.Keys(nullable)) {
		class, ok := columnClasses[column]
		if !ok {
			t.Errorf("column %s has no retention class in columnClasses", column)
			continue
		}
		table, _, _ := strings.Cut(column, ".")
		switch {
		case table == "documents":
			if class != classOfCategory {
				t.Errorf("column %s is class %q, want %q: a document is erased whole, by its category's class", column, class, classOfCategory)
			}
		case !slices.Contains([]string{"A", "C", "D", "E", "F"}, class):
			t.Errorf("column %s is class %q, want one of A, C, D, E, F", column, class)
		case (class == "D" || class == "A" || class == "E") && !wholeTable(table, class) && !nullable[column] && erasedTo[column] == "":
			t.Errorf("column %s is class %s and NOT NULL with no erased value: the erase could not null it", column, class)
		}
	}
	// The A erase is per employment (plan reading 4), so a column of class A
	// may live only where an employment scopes it: the terms.
	if tables := tablesWith("A"); !slices.Equal(tables, []string{"employment_terms"}) {
		t.Errorf("class A columns live in %v, want the terms alone — the A erase scopes by employment", tables)
	}
	for _, column := range slices.Sorted(maps.Keys(columnClasses)) {
		if _, ok := nullable[column]; !ok {
			t.Errorf("columnClasses names %s, which is no column of %v", column, classedTables)
		}
	}
}

// Every field a payload can name (fieldClasses) has the class of the column
// it records, so an event's payload_classes say what the erase of that
// column must also reach in the history. A document's fields take their
// class from the category, per event, and are not in fieldClasses; the
// fields that record a row rather than a column map to the row's table.
func TestFieldClasses_AgreeWithTheirColumns(t *testing.T) {
	t.Parallel()
	rows := map[string]string{
		"nextOfKin":   "next_of_kin.name",
		"roles":       "person_roles.role",
		"user":        "identity_links.external_id",
		"nationalId":  "people.national_id_sealed",
		"bankAccount": "people.bank_account_sealed",
		// snake() reads "Under12" as one word; the column has the digits apart.
		"childrenUnder12": "people.children_under_12",
		// The one-to-one's (delivery B): a list's count or kinds record its
		// table's rows, the Seen its stamps, the review its day.
		"interests":    "person_interests.competence_id",
		"preferences":  "person_preferences.kind",
		"declarations": "availability_declarations.kind",
		"seen":         "person_interests.seen_at",
		"reviewedOn":   "competence_reviews.reviewed_on",
	}
	// A field is its column in the first of these tables that has it.
	searched := []string{"people", "employments", "employment_terms"}
	for _, field := range slices.Sorted(maps.Keys(fieldClasses)) {
		column, ok := rows[field]
		if !ok {
			for _, table := range searched {
				if _, found := columnClasses[table+"."+snake(field)]; found {
					column = table + "." + snake(field)
					break
				}
			}
		}
		if column == "" {
			t.Errorf("field %s names no column of %v", field, searched)
			continue
		}
		if got, want := fieldClasses[field], columnClasses[column]; got != want {
			t.Errorf("field %s is class %q, its column %s class %q", field, got, column, want)
		}
	}
	for _, field := range []string{"category", "fileName", "validTo", "signedAt", "documentId"} {
		if _, ok := fieldClasses[field]; ok {
			t.Errorf("fieldClasses names the document field %s, whose class is its category's per event", field)
		}
	}
}

// snake is a contract field name as its column is spelled.
func snake(field string) string {
	var b strings.Builder
	for i, r := range field {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// The reference's table of classes (docs/…/en/reference/staff.md, "The
// classes, column by column") is columnClasses as an operator reads it: each
// row a table, a class and its columns, every column once. A class changed
// here without the page, or the page without here, fails.
func TestColumnClasses_TheReferenceAgrees(t *testing.T) {
	t.Parallel()
	page, err := os.ReadFile("../../../../docs/src/content/docs/en/reference/staff.md")
	if err != nil {
		t.Fatalf("read the reference: %v", err)
	}
	_, section, ok := strings.Cut(string(page), "### The classes, column by column")
	if !ok {
		t.Fatal("the reference has no section \"The classes, column by column\"")
	}
	section, _, _ = strings.Cut(section, "\n### ")
	row := regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\| ([A-F]|the category's) \\| (.*) \\|$")
	column := regexp.MustCompile("`([a-z_0-9]+)`")
	documented := map[string]string{}
	for _, m := range row.FindAllStringSubmatch(section, -1) {
		class := m[2]
		if class == "the category's" {
			class = classOfCategory
		}
		cells, _, _ := strings.Cut(m[3], " — ")
		for _, c := range column.FindAllStringSubmatch(cells, -1) {
			key := m[1] + "." + c[1]
			if _, twice := documented[key]; twice {
				t.Errorf("the reference lists %s twice", key)
			}
			documented[key] = class
		}
	}
	if !maps.Equal(documented, columnClasses) {
		for key, class := range columnClasses {
			if documented[key] != class {
				t.Errorf("%s is class %q here, %q in the reference", key, class, documented[key])
			}
		}
		for key := range documented {
			if _, ok := columnClasses[key]; !ok {
				t.Errorf("the reference lists %s, which columnClasses does not", key)
			}
		}
	}
}

// A competence event's fields are classed D in its own map (staff competence
// design S17) — validFrom among them, which fieldClasses holds C for a terms
// row — so the D erase blanks every one and leaves the row's id.
func TestBlankPayload_TheCompetenceEvents(t *testing.T) {
	t.Parallel()
	stored := []byte(`{"competenceId":1001,"fields":{"competenceKey":{"to":"fse"},"validFrom":{"to":"2026-01-01"}},"classes":{"competenceKey":"D","validFrom":"D"}}`)
	out, rewritten, err := blankPayload(stored, "D", 0)
	if err != nil || !rewritten {
		t.Fatalf("blankPayload = %s, %v, %v; want rewritten", out, rewritten, err)
	}
	var payload struct {
		CompetenceID int64          `json:"competenceId"`
		Fields       map[string]any `json:"fields"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CompetenceID != 1001 || payload.Fields["competenceKey"] != changed || payload.Fields["validFrom"] != changed {
		t.Errorf("the erased payload = %s, want both fields changed and the row's id kept", out)
	}
	if _, rewritten, _ := blankPayload(stored, "C", 0); rewritten {
		t.Error("the C erase rewrote a competence event, whose fields are D")
	}
}
