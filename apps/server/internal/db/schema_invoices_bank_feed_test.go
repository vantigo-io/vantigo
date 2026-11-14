package db_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The invoices bank feed schema (00178_invoices_bank_feed.sql; invoices bank
// feed design F3, F4, F10, plan findings 3, 4, 9): the connection and its
// accounts, and the third line format, feed, whose batches no person uploaded
// and whose payments no person need register. Its own file, as 00041's and
// 00046's tests are.

// bankFeedVersion is 00178's goose version.
const bankFeedVersion = 178

// bankFeedTables are the tables 00178 creates.
var bankFeedTables = []string{"bank_feed_connections", "bank_feed_accounts"}

// bankFeedChecks are the CHECKs 00178 replaces or adds on 00041's tables.
var bankFeedChecks = []string{
	"ck_bank_files_format", "ck_bank_files_origin", "ck_bank_transactions_format", "ck_bank_import_accounts_format",
	"ck_bank_import_accounts_previous_format", "ck_payments_source", "ck_payments_origin", "ck_charge_payments_source",
	"ck_charge_payments_origin", "ck_charge_waivers_by", "ck_bank_transaction_events_by",
}

// bankFeedObjects is what 00178 adds or changes, as found: the new tables'
// columns and the five it adds or makes nullable on 00041's tables
// (table.column:type:length:nullable:default), the tables, every constraint
// of the new tables and the CHECKs it replaces or adds with their
// definitions, and the new tables' indexes as Postgres prints them. Each list
// is sorted.
func bankFeedObjects(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string][]string {
	t.Helper()
	collect := func(what, sql string, args ...any) []string {
		t.Helper()
		rows, err := pool.Query(ctx, sql, args...)
		if err != nil {
			t.Fatalf("query %s: %v", what, err)
		}
		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("collect %s: %v", what, err)
		}
		slices.Sort(got)
		return got
	}
	return map[string][]string{
		"columns": collect("columns", `
			SELECT table_name || '.' || column_name || ':' || data_type || ':'
			    || coalesce(character_maximum_length::text, '') || ':' || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'invoices'
			  AND (table_name = ANY($1)
			    OR (table_name = 'bank_files' AND column_name IN ('provider', 'uploaded_by_user_id'))
			    OR (table_name = 'charge_payments' AND column_name = 'registered_by_user_id')
			    OR (table_name = 'charge_waivers' AND column_name = 'waived_by_user_id')
			    OR (table_name = 'bank_transaction_events' AND column_name = 'by_user_id'))`, bankFeedTables),
		"tables": collect("tables", `
			SELECT table_name FROM information_schema.tables
			WHERE table_schema = 'invoices' AND table_name = ANY($1)`, bankFeedTables),
		"constraints": collect("constraints", `
			SELECT c.conrelid::regclass::text || ':' || c.conname || ':' || pg_get_constraintdef(c.oid)
			FROM pg_constraint c
			WHERE c.connamespace = 'invoices'::regnamespace AND c.contype IN ('c', 'f', 'u', 'p')
			  AND (c.conrelid::regclass::text = ANY(SELECT 'invoices.' || unnest($1::text[])) OR c.conname = ANY($2))`,
			bankFeedTables, bankFeedChecks),
		"triggers": collect("triggers", `
			SELECT c.relname || ':' || t.tgname
			FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'invoices' AND NOT t.tgisinternal AND c.relname = ANY($1)`, bankFeedTables),
		"indexes": collect("indexes", `
			SELECT indexname || ':' || indexdef FROM pg_indexes
			WHERE schemaname = 'invoices' AND tablename = ANY($1)`, bankFeedTables),
	}
}

// TestInvoicesBankFeed_AppliesAndIsIdempotent proves
// 00178_invoices_bank_feed.sql applies, rolls back and re-applies cleanly,
// and pins every object it adds or changes: the two tables with every column,
// CHECK, key and foreign key and their index, no trigger on either; the bank
// files' provider and nullable uploader; the users of charge payments,
// waivers and line events nullable; and the eleven CHECKs. The Down restores
// 00041's and 00046's CHECKs and NOT NULLs exactly, and leaves every other
// object of 00041 and 00046 as it was.
func TestInvoicesBankFeed_AppliesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	url := testdb.MigratedURL(t)
	applyUpDownUp(t, url, bankFeedVersion) // 00178_invoices_bank_feed.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	want := map[string][]string{
		"columns":     bankFeedColumns,
		"tables":      {"bank_feed_accounts", "bank_feed_connections"},
		"constraints": bankFeedConstraints,
		"triggers":    nil,
		"indexes": {
			"bank_feed_accounts_pkey:CREATE UNIQUE INDEX bank_feed_accounts_pkey ON invoices.bank_feed_accounts USING btree (account)",
			"bank_feed_connections_pkey:CREATE UNIQUE INDEX bank_feed_connections_pkey ON invoices.bank_feed_connections USING btree (id)",
			"ix_bank_feed_accounts_due:CREATE INDEX ix_bank_feed_accounts_due ON invoices.bank_feed_accounts USING btree (next_sync_at) WHERE enabled",
		},
	}
	// After the Down: the five columns as 00041 made them, the CHECKs as
	// 00041 and 00046 left them, and no table, provider or origin.
	wantDown := map[string][]string{
		"columns": {
			"bank_files.uploaded_by_user_id:uuid::NO:",
			"bank_transaction_events.by_user_id:uuid::NO:",
			"charge_payments.registered_by_user_id:uuid::NO:",
			"charge_waivers.waived_by_user_id:uuid::NO:",
		},
		"constraints": bankFeedConstraintsBefore,
	}
	expect := func(when string, want map[string][]string) {
		t.Helper()
		got := bankFeedObjects(t, ctx, pool)
		for _, what := range []string{"columns", "tables", "constraints", "triggers", "indexes"} {
			if !equalStrings(got[what], want[what]) {
				t.Errorf("%s: %s =\n%s\nwant\n%s", when, what, strings.Join(got[what], "\n"), strings.Join(want[what], "\n"))
			}
		}
	}
	expect("after up", want)

	migrateTo(t, url, bankFeedVersion-1)
	expect("after down", wantDown)
	receivablesBefore := receivablesObjectsBut00178(t, ctx, pool)
	portBefore := paymentsPortObjects(t, ctx, pool)

	migrateTo(t, url, bankFeedVersion)
	expect("after up again", want)

	// 00178 leaves the rest of 00041's and 00046's objects as they were.
	receivablesAfter := receivablesObjectsBut00178(t, ctx, pool)
	for what, before := range receivablesBefore {
		if !equalStrings(receivablesAfter[what], before) {
			t.Errorf("00041's %s changed under 00178:\n%s\nwas\n%s", what,
				strings.Join(receivablesAfter[what], "\n"), strings.Join(before, "\n"))
		}
	}
	portAfter := paymentsPortObjects(t, ctx, pool)
	for what, before := range portBefore {
		if what == "constraints" {
			before = withoutPrefixes(before, "invoices.payments:ck_payments_")
			portAfter[what] = withoutPrefixes(portAfter[what], "invoices.payments:ck_payments_")
		}
		if !equalStrings(portAfter[what], before) {
			t.Errorf("00046's %s changed under 00178:\n%s\nwas\n%s", what,
				strings.Join(portAfter[what], "\n"), strings.Join(before, "\n"))
		}
	}
}

// receivablesObjectsBut00178 is receivablesObjects less what 00178 changes:
// the CHECKs it replaces and adds, and the columns it adds or makes nullable.
func receivablesObjectsBut00178(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string][]string {
	t.Helper()
	objects := receivablesObjects(t, ctx, pool)
	objects["constraints"] = slices.DeleteFunc(objects["constraints"], func(c string) bool {
		return slices.ContainsFunc(bankFeedChecks, func(name string) bool { return strings.Contains(c, ":"+name+":") })
	})
	objects["columns"] = withoutPrefixes(objects["columns"], "bank_files.provider:", "bank_files.uploaded_by_user_id:",
		"charge_payments.registered_by_user_id:", "charge_waivers.waived_by_user_id:", "bank_transaction_events.by_user_id:")
	return objects
}

// withoutPrefixes is list less the entries starting with any of prefixes.
func withoutPrefixes(list []string, prefixes ...string) []string {
	return slices.DeleteFunc(slices.Clone(list), func(s string) bool {
		return slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(s, p) })
	})
}

// bankFeedColumns is F3, F4 and F10 written out.
var bankFeedColumns = []string{
	"bank_feed_accounts.account:character varying:11:NO:",
	"bank_feed_accounts.detail:character varying:500:NO:''::character varying",
	"bank_feed_accounts.enabled:boolean::NO:",
	"bank_feed_accounts.enabled_by_user_id:uuid::NO:",
	"bank_feed_accounts.failures:integer::NO:0",
	"bank_feed_accounts.last_bank_file_id:bigint::YES:",
	"bank_feed_accounts.last_synced_at:timestamp with time zone::YES:",
	"bank_feed_accounts.lease_id:character varying:32:YES:",
	"bank_feed_accounts.lease_until:timestamp with time zone::YES:",
	"bank_feed_accounts.next_sync_at:timestamp with time zone::YES:",
	"bank_feed_accounts.reason:character varying:40:YES:",
	"bank_feed_accounts.start_day:date::NO:",
	"bank_feed_accounts.status:character varying:12:NO:'ok'::character varying",
	"bank_feed_accounts.updated_at:timestamp with time zone::NO:",
	"bank_feed_connections.connected_at:timestamp with time zone::NO:",
	"bank_feed_connections.connected_by_user_id:uuid::NO:",
	"bank_feed_connections.id:integer::NO:",
	"bank_feed_connections.key_last4:character varying:4:NO:",
	"bank_feed_connections.provider:character varying:20:NO:",
	"bank_feed_connections.rejected_at:timestamp with time zone::YES:",
	"bank_feed_connections.secret_ciphertext:text::NO:",
	"bank_feed_connections.status:character varying:12:NO:'ok'::character varying",
	"bank_feed_connections.updated_at:timestamp with time zone::NO:",
	"bank_files.provider:character varying:20:YES:",
	"bank_files.uploaded_by_user_id:uuid::YES:",
	"bank_transaction_events.by_user_id:uuid::YES:",
	"charge_payments.registered_by_user_id:uuid::YES:",
	"charge_waivers.waived_by_user_id:uuid::YES:",
}

// bankFeedConstraints is every constraint of the two tables and the eleven
// CHECKs, as Postgres prints them after 00178.
var bankFeedConstraints = []string{
	"invoices.bank_feed_accounts:bank_feed_accounts_last_bank_file_id_fkey:FOREIGN KEY (last_bank_file_id) REFERENCES invoices.bank_files(id)",
	"invoices.bank_feed_accounts:bank_feed_accounts_pkey:PRIMARY KEY (account)",
	"invoices.bank_feed_accounts:ck_bank_feed_accounts_lease:CHECK (((lease_id IS NULL) = (lease_until IS NULL)))",
	"invoices.bank_feed_accounts:ck_bank_feed_accounts_status:CHECK (((status)::text = ANY ((ARRAY['ok'::character varying, 'failing'::character varying, 'refused'::character varying, 'unauthorized'::character varying])::text[])))",
	"invoices.bank_feed_connections:bank_feed_connections_pkey:PRIMARY KEY (id)",
	"invoices.bank_feed_connections:ck_bank_feed_connections_provider:CHECK (((provider)::text = 'folio'::text))",
	"invoices.bank_feed_connections:ck_bank_feed_connections_single_row:CHECK ((id = 1))",
	"invoices.bank_feed_connections:ck_bank_feed_connections_status:CHECK (((status)::text = ANY ((ARRAY['ok'::character varying, 'unauthorized'::character varying])::text[])))",
	"invoices.bank_files:ck_bank_files_format:CHECK (((format)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying, 'feed'::character varying])::text[])))",
	"invoices.bank_files:ck_bank_files_origin:CHECK (((((format)::text = 'feed'::text) = (uploaded_by_user_id IS NULL)) AND (((format)::text = 'feed'::text) = (provider IS NOT NULL))))",
	"invoices.bank_import_accounts:ck_bank_import_accounts_format:CHECK (((format)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying, 'feed'::character varying])::text[])))",
	"invoices.bank_import_accounts:ck_bank_import_accounts_previous_format:CHECK (((previous_format)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying, 'feed'::character varying])::text[])))",
	"invoices.bank_transaction_events:ck_bank_transaction_events_by:CHECK (((by_user_id IS NOT NULL) OR ((event)::text = ANY ((ARRAY['matched'::character varying, 'queued'::character varying])::text[]))))",
	"invoices.bank_transactions:ck_bank_transactions_format:CHECK (((format)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying, 'feed'::character varying])::text[])))",
	"invoices.charge_payments:ck_charge_payments_origin:CHECK (((((source)::text = 'manual'::text) = (bank_transaction_id IS NULL)) AND ((registered_by_user_id IS NOT NULL) OR ((source)::text = 'bank_feed'::text))))",
	"invoices.charge_payments:ck_charge_payments_source:CHECK (((source)::text = ANY ((ARRAY['manual'::character varying, 'ocr'::character varying, 'camt054'::character varying, 'bank_feed'::character varying])::text[])))",
	"invoices.charge_waivers:ck_charge_waivers_by:CHECK (((waived_by_user_id IS NOT NULL) OR ((reason)::text = 'deadline_met'::text)))",
	"invoices.payments:ck_payments_origin:CHECK (((((source)::text = 'manual'::text) AND (bank_transaction_id IS NULL) AND (registered_by_user_id IS NOT NULL) AND (payment_attempt_id IS NULL)) OR (((source)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying])::text[])) AND (bank_transaction_id IS NOT NULL) AND (registered_by_user_id IS NOT NULL) AND (payment_attempt_id IS NULL)) OR (((source)::text = 'vipps'::text) AND (payment_attempt_id IS NOT NULL) AND (bank_transaction_id IS NULL) AND (registered_by_user_id IS NULL)) OR (((source)::text = 'bank_feed'::text) AND (bank_transaction_id IS NOT NULL) AND (payment_attempt_id IS NULL))))",
	"invoices.payments:ck_payments_source:CHECK (((source)::text = ANY ((ARRAY['manual'::character varying, 'ocr'::character varying, 'camt054'::character varying, 'vipps'::character varying, 'bank_feed'::character varying])::text[])))",
}

// bankFeedConstraintsBefore is the eight CHECKs 00178 replaces, as 00041 and
// 00046 left them: the Down restores them exactly.
var bankFeedConstraintsBefore = []string{
	"invoices.bank_files:ck_bank_files_format:CHECK (((format)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying])::text[])))",
	"invoices.bank_import_accounts:ck_bank_import_accounts_format:CHECK (((format)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying])::text[])))",
	"invoices.bank_import_accounts:ck_bank_import_accounts_previous_format:CHECK (((previous_format)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying])::text[])))",
	"invoices.bank_transactions:ck_bank_transactions_format:CHECK (((format)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying])::text[])))",
	"invoices.charge_payments:ck_charge_payments_origin:CHECK ((((source)::text = 'manual'::text) = (bank_transaction_id IS NULL)))",
	"invoices.charge_payments:ck_charge_payments_source:CHECK (((source)::text = ANY ((ARRAY['manual'::character varying, 'ocr'::character varying, 'camt054'::character varying])::text[])))",
	"invoices.payments:ck_payments_origin:CHECK (((((source)::text = 'manual'::text) AND (bank_transaction_id IS NULL) AND (registered_by_user_id IS NOT NULL) AND (payment_attempt_id IS NULL)) OR (((source)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying])::text[])) AND (bank_transaction_id IS NOT NULL) AND (registered_by_user_id IS NOT NULL) AND (payment_attempt_id IS NULL)) OR (((source)::text = 'vipps'::text) AND (payment_attempt_id IS NOT NULL) AND (bank_transaction_id IS NULL) AND (registered_by_user_id IS NULL))))",
	"invoices.payments:ck_payments_source:CHECK (((source)::text = ANY ((ARRAY['manual'::character varying, 'ocr'::character varying, 'camt054'::character varying, 'vipps'::character varying])::text[])))",
}

// bankFeedFile inserts a bank file of format by uploader with provider and
// answers its id or the refusal.
func (f *receivablesFixture) bankFeedFile(format string, uploader, provider *string) (int64, error) {
	f.seq++
	sha := fmt.Sprintf("%064x", 1<<40+f.seq)
	return f.insert(`
		INSERT INTO invoices.bank_files (format, sha256, file_identity, object_key, byte_size, accounts,
		    transactions, ignored, ignored_kinds, uploaded_by_user_id, uploaded_at, provider)
		VALUES ($1, $2::text, $2::text, 'bank-files/' || $2::text, 2, ARRAY['15032080119'], 0, 0, '{}', $3, now(), $4)
		RETURNING id`, format, sha, uploader, provider)
}

// TestBankFiles_FeedOrigin: a feed batch has a provider and no uploader, and a
// file has an uploader and no provider (ck_bank_files_origin); feed is a
// format of files, lines and accounts alike.
func TestBankFiles_FeedOrigin(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	user, folio := "6f1c7a64-9c4f-4c43-9a51-2a8d4a1f0c11", "folio"

	for _, c := range []struct {
		name             string
		format           string
		uploader, source *string
	}{
		{"a feed batch with an uploader", "feed", &user, &folio},
		{"a feed batch without a provider", "feed", nil, nil},
		{"an OCR file without an uploader", "ocr", nil, nil},
		{"an OCR file with a provider", "ocr", &user, &folio},
	} {
		if _, err := f.bankFeedFile(c.format, c.uploader, c.source); !checkViolationOf(err, "ck_bank_files_origin") {
			t.Errorf("%s: %v, want ck_bank_files_origin", c.name, err)
		}
	}
	if _, err := f.bankFeedFile("folio", nil, &folio); !checkViolationOf(err, "ck_bank_files_format") {
		t.Errorf("an unknown format: %v, want ck_bank_files_format", err)
	}
	feed, err := f.bankFeedFile("feed", nil, &folio)
	if err != nil {
		t.Fatalf("a feed batch by no one from folio: %v, want it allowed", err)
	}
	if _, err := f.bankFeedFile("camt054", &user, nil); err != nil {
		t.Errorf("a camt.054 file by a user: %v, want it allowed still", err)
	}
	// The file stays frozen: its provider is no exception.
	if err := f.exec(`UPDATE invoices.bank_files SET provider = NULL, uploaded_by_user_id = gen_random_uuid() WHERE id = $1`, feed); !refusedWith(err, "invoices: a bank file is immutable") {
		t.Errorf("a feed batch changed: %v, want it refused", err)
	}

	if _, err := f.insert(`
		INSERT INTO invoices.bank_transactions (bank_file_id, line_ref, format, account, direction, booked_on,
		    amount, currency, fingerprint, ordinal)
		VALUES ($1, '8d9b0e8f-1c2a-4f7e-9a3b-5c6d7e8f9a0b', 'feed', '15032080119', 'credit', DATE '2026-10-01', 1250, 'NOK',
		    repeat('f', 64), 1) RETURNING id`, feed); err != nil {
		t.Errorf("a feed line: %v, want it allowed", err)
	}
	f.mustExec("an account in format feed", `INSERT INTO invoices.bank_import_accounts (account, format, previous_format,
		cutover_through, set_by_user_id, set_at) VALUES ('12345678903', 'feed', 'camt054', DATE '2026-10-01', gen_random_uuid(), now())`)
	f.mustExec("an account from feed back to files", `UPDATE invoices.bank_import_accounts SET format = 'ocr',
		previous_format = 'feed' WHERE account = '12345678903'`)
	if err := f.exec(`INSERT INTO invoices.bank_import_accounts (account, format, set_by_user_id, set_at)
		VALUES ('86011117947', 'api', gen_random_uuid(), now())`); !checkViolationOf(err, "ck_bank_import_accounts_format") {
		t.Errorf("an unknown account format: %v, want ck_bank_import_accounts_format", err)
	}
}

// TestPayments_BankFeedSourceCheck: a bank_feed payment names its line and no
// attempt, and has a user or none (plan finding 3); a file's payment still
// needs its user. (00046's TestPayments_VippsSourceCheck keeps the rest.)
func TestPayments_BankFeedSourceCheck(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	invoice := f.issued(7)
	line := f.mustBankLine(f.bankFile(), strings.Repeat("a", 64))
	attempt := f.mustAttempt(invoice, "captured", "", "")
	user := "6f1c7a64-9c4f-4c43-9a51-2a8d4a1f0c11"

	for _, c := range []struct {
		name    string
		source  string
		user    *string
		line    *int64
		attempt *int64
	}{
		{"a bank_feed payment without a bank line", "bank_feed", nil, nil, nil},
		{"a bank_feed payment with an attempt", "bank_feed", nil, &line, &attempt},
		{"an OCR payment without a user", "ocr", nil, &line, nil},
		{"a camt.054 payment without a user", "camt054", nil, &line, nil},
	} {
		if _, err := f.insert(paymentsPortPaymentInsert, invoice, c.user, c.source, c.line, c.attempt); !checkViolationOf(err, "ck_payments_origin") {
			t.Errorf("%s: %v, want ck_payments_origin", c.name, err)
		}
	}
	// An unknown source breaks both CHECKs; Postgres names either.
	if _, err := f.insert(paymentsPortPaymentInsert, invoice, user, "bank", line, nil); !checkViolationOf(err, "ck_payments_source") && !checkViolationOf(err, "ck_payments_origin") {
		t.Errorf("an unknown source: %v, want ck_payments_source", err)
	}
	if _, err := f.insert(paymentsPortPaymentInsert, invoice, nil, "bank_feed", line, nil); err != nil {
		t.Errorf("a bank_feed payment by no user: %v, want it allowed", err)
	}
	if _, err := f.insert(paymentsPortPaymentInsert, invoice, user, "bank_feed", line, nil); err != nil {
		t.Errorf("a bank_feed payment a person applied: %v, want it allowed", err)
	}
	if _, err := f.insert(paymentsPortPaymentInsert, invoice, user, "ocr", line, nil); err != nil {
		t.Errorf("an OCR payment: %v, want it allowed still", err)
	}
}

// TestChargePayments_BankFeedOrigin: a charge payment has a bank line unless
// manual, and a user unless from the feed (plan finding 4).
func TestChargePayments_BankFeedOrigin(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	invoice := f.issued(7)
	line := f.mustBankLine(f.bankFile(), strings.Repeat("b", 64))
	insert := `INSERT INTO invoices.charge_payments (invoice_id, paid_on, amount, currency, source, bank_transaction_id,
		registered_by_user_id, registered_at) VALUES ($1, DATE '2026-11-10', 35, 'NOK', $2, $3, $4, now()) RETURNING id`
	user := "6f1c7a64-9c4f-4c43-9a51-2a8d4a1f0c11"

	for _, c := range []struct {
		name   string
		source string
		line   *int64
		user   *string
	}{
		{"a bank_feed charge payment without a bank line", "bank_feed", nil, nil},
		{"an OCR charge payment without a user", "ocr", &line, nil},
		{"a manual charge payment without a user", "manual", nil, nil},
	} {
		if _, err := f.insert(insert, invoice, c.source, c.line, c.user); !checkViolationOf(err, "ck_charge_payments_origin") {
			t.Errorf("%s: %v, want ck_charge_payments_origin", c.name, err)
		}
	}
	for _, c := range []struct {
		name string
		user *string
	}{{"by no user", nil}, {"by a person", &user}} {
		if _, err := f.insert(insert, invoice, "bank_feed", line, c.user); err != nil {
			t.Errorf("a bank_feed charge payment %s: %v, want it allowed", c.name, err)
		}
	}
	if _, err := f.insert(insert, invoice, "vipps", line, user); !checkViolationOf(err, "ck_charge_payments_source") {
		t.Errorf("a Vipps charge payment: %v, want ck_charge_payments_source", err)
	}
}

// TestChargeWaivers_ByNoUserOnlyForADeadlineMet: a waiver is a person's, but
// for deadline_met, which matching writes (plan finding 4).
func TestChargeWaivers_ByNoUserOnlyForADeadlineMet(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	invoice := f.issued(7)
	letter := f.mustReminder(invoice, f.run(), 1)
	insert := fmt.Sprintf(`INSERT INTO invoices.charge_waivers (invoice_id, reminder_id, kind, amount, reason,
		waived_by_user_id, waived_at) VALUES ($1, %d, $2, 35, $3, $4, now()) RETURNING id`, letter)
	user := "6f1c7a64-9c4f-4c43-9a51-2a8d4a1f0c11"

	for _, reason := range []string{"objection_upheld", "claimed_in_error", "goodwill"} {
		if _, err := f.insert(insert, invoice, "fee", reason, nil); !checkViolationOf(err, "ck_charge_waivers_by") {
			t.Errorf("a %s waiver by no user: %v, want ck_charge_waivers_by", reason, err)
		}
	}
	if _, err := f.insert(insert, invoice, "fee", "deadline_met", nil); err != nil {
		t.Errorf("a deadline_met waiver by no user: %v, want it allowed", err)
	}
	if _, err := f.insert(insert, invoice, "compensation", "goodwill", user); err != nil {
		t.Errorf("a goodwill waiver by a person: %v, want it allowed still", err)
	}
}

// TestBankTransactionEvents_ByNoUser: a line's matched and queued events may
// be by no user — the feed's — and every other event, a person's queue
// action, keeps its user (ck_bank_transaction_events_by).
func TestBankTransactionEvents_ByNoUser(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	line := f.mustBankLine(f.bankFile(), strings.Repeat("c", 64))
	insert := `INSERT INTO invoices.bank_transaction_events (bank_transaction_id, event, reason, by_user_id, at)
		VALUES ($1, $2, $3, $4, now())`
	reason := "no_kid"
	for _, c := range []struct {
		event  string
		reason *string
	}{{"matched", nil}, {"queued", &reason}} {
		if err := f.exec(insert, line, c.event, c.reason, nil); err != nil {
			t.Errorf("a %s event by no user: %v, want it allowed", c.event, err)
		}
	}
	for _, event := range []string{"applied", "dismissed", "reversal_handled", "reopened", "duplicate_confirmed",
		"treated_as_distinct", "reversed"} {
		if err := f.exec(insert, line, event, nil, nil); !checkViolationOf(err, "ck_bank_transaction_events_by") {
			t.Errorf("a %s event by no user: %v, want ck_bank_transaction_events_by", event, err)
		}
	}
	if err := f.exec(insert, line, "applied", nil, "6f1c7a64-9c4f-4c43-9a51-2a8d4a1f0c11"); err != nil {
		t.Errorf("an applied event by a person: %v, want it allowed still", err)
	}
}

// TestBankFeed_TheTwoTables: one connection row, of folio, ok or
// unauthorized; an account's status and its lease's two columns together.
// Neither table is frozen.
func TestBankFeed_TheTwoTables(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	connection := `INSERT INTO invoices.bank_feed_connections (id, provider, secret_ciphertext, key_last4, status,
		connected_by_user_id, connected_at, updated_at) VALUES ($1, $2, 'sealed', '1234', $3, gen_random_uuid(), now(), now())`
	for _, c := range []struct {
		name, check string
		args        []any
	}{
		{"a second row", "ck_bank_feed_connections_single_row", []any{2, "folio", "ok"}},
		{"another bank", "ck_bank_feed_connections_provider", []any{1, "dnb", "ok"}},
		{"an unknown status", "ck_bank_feed_connections_status", []any{1, "folio", "failing"}},
	} {
		if err := f.exec(connection, c.args...); !checkViolationOf(err, c.check) {
			t.Errorf("%s: %v, want %s", c.name, err, c.check)
		}
	}
	f.mustExec("the connection", connection, 1, "folio", "ok")
	f.mustExec("the connection refused", `UPDATE invoices.bank_feed_connections SET status = 'unauthorized', rejected_at = now()`)

	account := `INSERT INTO invoices.bank_feed_accounts (account, enabled, start_day, status, lease_id, lease_until,
		enabled_by_user_id, updated_at) VALUES ($1, true, DATE '2026-10-01', $2, $3, $4::timestamptz, gen_random_uuid(), now())`
	if err := f.exec(account, "86011117947", "stale", nil, nil); !checkViolationOf(err, "ck_bank_feed_accounts_status") {
		t.Errorf("an unknown account status: %v, want ck_bank_feed_accounts_status", err)
	}
	if err := f.exec(account, "86011117947", "ok", "lease", nil); !checkViolationOf(err, "ck_bank_feed_accounts_lease") {
		t.Errorf("a lease without its end: %v, want ck_bank_feed_accounts_lease", err)
	}
	f.mustExec("an account", account, "86011117947", "ok", nil, nil)
	f.mustExec("an account refused", `UPDATE invoices.bank_feed_accounts SET status = 'refused', reason = 'direction_unknown'`)
	f.mustExec("an account removed", `DELETE FROM invoices.bank_feed_accounts`)
	f.mustExec("the connection removed", `DELETE FROM invoices.bank_feed_connections`)
}
