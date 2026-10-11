package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The staff records schema (00043_staff_records.sql; staff foundation design
// S14, S15, §3.6, delivery A2): the document categories with their six seeds,
// the documents and the object-deletion ledger. Its own file, as 00042's
// tests are.

// staffRecordsVersion is 00043's goose version.
const staffRecordsVersion = 43

// staffRecordsTables are the tables 00043 creates.
var staffRecordsTables = []string{"document_categories", "documents", "object_deletions"}

// TestStaffRecords_AppliesAndIsIdempotent proves 00043_staff_records.sql
// applies, rolls back and re-applies cleanly, and pins what delivery A2's
// documents rest on: every column of the three tables (documents with its
// nullable employment and no deleted_at — rows are deleted, never marked);
// the three unique indexes (a category's key, a document's object key, a
// ledger row's object key), the three foreign keys of documents and the
// indexes beside them; the six seeds with their classes, restricted exactly
// for class E; every identity starting at 1001. The Down removes the three
// tables and leaves 00042's schema as it was.
func TestStaffRecords_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, staffRecordsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := staffCollect(t, ctx, pool, "columns", `
		SELECT table_name || '.' || column_name || ':' || data_type || ':'
		    || coalesce(character_maximum_length::text, '') || ':' || is_nullable || ':' || coalesce(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name = ANY($1)
		ORDER BY 1`, staffRecordsTables)
	wantColumns := []string{
		"document_categories.archived_at:timestamp with time zone::YES:",
		"document_categories.created_at:timestamp with time zone::NO:",
		"document_categories.id:bigint::NO:",
		"document_categories.key:character varying:40:NO:",
		"document_categories.name_en:character varying:100:NO:",
		"document_categories.name_nb:character varying:100:NO:",
		"document_categories.restricted:boolean::NO:false",
		"document_categories.retention_class:character:1:NO:",
		"document_categories.revision:integer::NO:1",
		"document_categories.seeded:boolean::NO:false",
		"document_categories.sort:integer::NO:",
		"document_categories.updated_at:timestamp with time zone::NO:",
		"documents.category_id:bigint::NO:",
		"documents.content_type:character varying:100:NO:",
		"documents.created_at:timestamp with time zone::NO:",
		"documents.employment_id:bigint::YES:",
		"documents.file_name:character varying:255:NO:",
		"documents.id:bigint::NO:",
		"documents.object_key:character varying:200:NO:",
		"documents.person_id:bigint::NO:",
		"documents.revision:integer::NO:1",
		"documents.signed_at:date::YES:",
		"documents.size_bytes:bigint::NO:",
		"documents.updated_at:timestamp with time zone::NO:",
		"documents.uploaded_by_user_id:uuid::NO:",
		"documents.valid_to:date::YES:",
		"object_deletions.attempts:integer::NO:0",
		"object_deletions.claimed_until:timestamp with time zone::YES:",
		"object_deletions.created_at:timestamp with time zone::NO:",
		"object_deletions.id:bigint::NO:",
		"object_deletions.last_error:character varying:200:YES:",
		"object_deletions.next_attempt_at:timestamp with time zone::NO:",
		"object_deletions.object_key:character varying:200:NO:",
	}
	if !equalStrings(columns, wantColumns) {
		t.Errorf("columns =\n%s\nwant\n%s", strings.Join(columns, "\n"), strings.Join(wantColumns, "\n"))
	}

	foreignKeys := staffCollect(t, ctx, pool, "foreign keys", `
		SELECT c.conrelid::regclass::text || ':' || pg_get_constraintdef(c.oid)
		FROM pg_constraint c
		WHERE c.connamespace = 'staff'::regnamespace AND c.contype = 'f'
		  AND c.conrelid::regclass::text = ANY(SELECT 'staff.' || unnest($1::text[]))
		ORDER BY 1`, staffRecordsTables)
	wantForeignKeys := []string{
		"staff.documents:FOREIGN KEY (category_id) REFERENCES staff.document_categories(id)",
		"staff.documents:FOREIGN KEY (employment_id) REFERENCES staff.employments(id)",
		"staff.documents:FOREIGN KEY (person_id) REFERENCES staff.people(id)",
	}
	if !equalStrings(foreignKeys, wantForeignKeys) {
		t.Errorf("foreign keys =\n%s\nwant\n%s", strings.Join(foreignKeys, "\n"), strings.Join(wantForeignKeys, "\n"))
	}

	indexes := staffCollect(t, ctx, pool, "indexes", `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'staff' AND tablename = ANY($1) AND indexname NOT LIKE '%_pkey'
		ORDER BY indexname`, staffRecordsTables)
	wantIndexes := []string{
		"CREATE INDEX ix_documents_category ON staff.documents USING btree (category_id)",
		"CREATE INDEX ix_documents_person ON staff.documents USING btree (person_id)",
		"CREATE INDEX ix_object_deletions_next_attempt ON staff.object_deletions USING btree (next_attempt_at)",
		"CREATE UNIQUE INDEX ux_document_categories_key ON staff.document_categories USING btree (key)",
		"CREATE UNIQUE INDEX ux_documents_object_key ON staff.documents USING btree (object_key)",
		"CREATE UNIQUE INDEX ux_object_deletions_object_key ON staff.object_deletions USING btree (object_key)",
	}
	if !equalStrings(indexes, wantIndexes) {
		t.Errorf("indexes =\n%s\nwant\n%s", strings.Join(indexes, "\n"), strings.Join(wantIndexes, "\n"))
	}

	sequences := staffCollect(t, ctx, pool, "sequences", `
		SELECT sequencename || ':' || start_value FROM pg_sequences
		WHERE schemaname = 'staff' AND sequencename = ANY(SELECT unnest($1::text[]) || '_id_seq')
		ORDER BY 1`, staffRecordsTables)
	if want := []string{"document_categories_id_seq:1001", "documents_id_seq:1001", "object_deletions_id_seq:1001"}; !equalStrings(sequences, want) {
		t.Errorf("sequences = %v, want %v", sequences, want)
	}

	// The six seeds, in their order, each with its class; restricted is the
	// health category's alone, the one of class E.
	seeds := staffCollect(t, ctx, pool, "seeds", `
		SELECT key || ':' || name_en || ':' || name_nb || ':' || retention_class || ':' || restricted::text || ':' || seeded::text
		FROM staff.document_categories ORDER BY sort`)
	wantSeeds := []string{
		"contract:Contract:Arbeidsavtale:A:false:true",
		"attest:Attest:Attest:A:false:true",
		"certificate:Certificate:Sertifikat:D:false:true",
		"correspondence:Correspondence:Korrespondanse:D:false:true",
		"health:Health:Helse:E:true:true",
		"other:Other:Annet:D:false:true",
	}
	if !equalStrings(seeds, wantSeeds) {
		t.Errorf("seeds =\n%s\nwant\n%s", strings.Join(seeds, "\n"), strings.Join(wantSeeds, "\n"))
	}

	// A category's key is unique, and so is an object key, in the archive and
	// in the ledger alike.
	if _, err := pool.Exec(ctx, `
		INSERT INTO staff.document_categories (key, name_en, name_nb, retention_class, sort, created_at, updated_at)
		VALUES ('contract', 'Again', 'Igjen', 'A', 7, now(), now())`); !uniqueViolationOf(err, "ux_document_categories_key") {
		t.Errorf("a second contract category: %v, want the unique index to refuse it", err)
	}
	insertDeletion := func() error {
		_, err := pool.Exec(ctx, `INSERT INTO staff.object_deletions (object_key, next_attempt_at, created_at)
			VALUES ('people/1/same', now(), now())`)
		return err
	}
	if err := insertDeletion(); err != nil {
		t.Fatalf("insert a ledger row: %v", err)
	}
	if err := insertDeletion(); !uniqueViolationOf(err, "ux_object_deletions_object_key") {
		t.Errorf("a second ledger row for one object: %v, want the unique index to refuse it", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM staff.object_deletions`); err != nil {
		t.Fatalf("clear the ledger: %v", err)
	}

	migrateTo(t, url, staffRecordsVersion-1)
	left := staffCollect(t, ctx, pool, "tables after the rollback", `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'staff' AND table_name = ANY($1)`, staffRecordsTables)
	if len(left) != 0 {
		t.Errorf("the rollback left %v", left)
	}
	if tables := staffCollect(t, ctx, pool, "tables", `
		SELECT table_name FROM information_schema.tables WHERE table_schema = 'staff' ORDER BY 1`); !equalStrings(tables, staffTables) {
		t.Errorf("after the rollback staff tables = %v, want 00042's %v", tables, staffTables)
	}
}
