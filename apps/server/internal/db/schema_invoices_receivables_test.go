package db_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The invoices receivables schema (00041_invoices_receivables.sql; invoices
// payments and reminders design D2, D3, D5–D11): the payments' source and
// bank line, the bank import accounts, files, lines and their events, the
// collection rates with their seeds and the release's seed function, the
// reminder settings and the per-customer policy, manual deliveries, charge
// payments and waivers, reminder runs, letters and print batches, holds and
// hand-offs. Its own file, as 00040's tests are.

// receivablesVersion is 00041's goose version.
const receivablesVersion = 41

// receivablesTables are the tables 00041 creates, in the order it creates
// them.
var receivablesTables = []string{
	"bank_import_accounts", "bank_files", "bank_transactions", "bank_transaction_events",
	"collection_rates", "reminder_settings", "customer_reminder_policies", "manual_deliveries",
	"charge_payments", "reminder_runs", "reminder_print_batches", "reminders",
	"invoice_holds", "collection_handoffs", "charge_waivers",
}

// receivablesObjects is what 00041 adds to the invoices schema, as found: the
// new tables' columns and the payments' three (table.column:type:length or
// precision:nullable:default), the tables, their triggers
// (table:trigger:function), every constraint of the new tables and the
// payments' new ones with its definition, the functions (name:volatility:
// return type) and the indexes as Postgres prints them.
func receivablesObjects(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string][]string {
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
		return got
	}
	return map[string][]string{
		"columns": collect("columns", `
			SELECT table_name || '.' || column_name || ':' || data_type || ':'
			    || coalesce(character_maximum_length::text, CASE WHEN data_type = 'numeric' THEN numeric_precision || ',' || numeric_scale END, '')
			    || ':' || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'invoices'
			  AND (table_name = ANY($1)
			    OR (table_name = 'payments' AND column_name IN ('source', 'bank_transaction_id', 'registered_by_user_id')))
			ORDER BY 1`, receivablesTables),
		"tables": collect("tables", `
			SELECT table_name FROM information_schema.tables
			WHERE table_schema = 'invoices' AND table_name = ANY($1)
			ORDER BY 1`, receivablesTables),
		"triggers": collect("triggers", `
			SELECT c.relname || ':' || t.tgname || ':' || p.proname
			FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
			JOIN pg_proc p ON p.oid = t.tgfoid
			WHERE n.nspname = 'invoices' AND NOT t.tgisinternal AND c.relname = ANY($1)
			ORDER BY 1`, receivablesTables),
		"constraints": collect("constraints", `
			SELECT c.conrelid::regclass::text || ':' || c.conname || ':' || pg_get_constraintdef(c.oid)
			FROM pg_constraint c
			WHERE c.connamespace = 'invoices'::regnamespace AND c.contype IN ('c', 'f', 'u', 'p')
			  AND (c.conrelid::regclass::text = ANY(SELECT 'invoices.' || unnest($1::text[]))
			    OR c.conname IN ('ck_payments_source', 'ck_payments_origin', 'payments_bank_transaction_id_fkey'))
			ORDER BY 1`, receivablesTables),
		"functions": collect("functions", `
			SELECT p.proname || ':' || p.provolatile::text || ':' || pg_catalog.format_type(p.prorettype, NULL)
			FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE n.nspname = 'invoices' AND p.proname IN ('refuse_row_delete', 'refuse_bank_import_account_change',
			    'refuse_bank_file_change', 'refuse_bank_transaction_change', 'refuse_bank_transaction_event_change',
			    'refuse_collection_rate_change', 'seed_collection_rate', 'guard_child_of_issued_insert',
			    'refuse_manual_delivery_change', 'refuse_charge_payment_change', 'refuse_reminder_run_change',
			    'refuse_print_batch_change', 'guard_reminder_insert', 'refuse_reminder_change',
			    'refuse_hold_change', 'refuse_handoff_change', 'refuse_waiver_change')
			ORDER BY 1`),
		"indexes": collect("indexes", `
			SELECT indexname || ':' || indexdef FROM pg_indexes
			WHERE schemaname = 'invoices' AND (tablename = ANY($1) OR indexname = 'ix_payments_bank_transaction')
			ORDER BY 1`, receivablesTables),
		"sequences": collect("sequences", `
			SELECT sequencename || ':' || start_value FROM pg_sequences
			WHERE schemaname = 'invoices' AND sequencename = ANY(SELECT unnest($1::text[]) || '_id_seq')
			ORDER BY 1`, receivablesTables),
	}
}

// TestInvoicesReceivables_AppliesAndIsIdempotent proves
// 00041_invoices_receivables.sql applies, rolls back and re-applies cleanly,
// and pins every object it adds: the fifteen tables with every column, CHECK,
// key and foreign key, the triggers and their functions, the release's seed
// function, and the indexes — the partial ones with their predicates; and the
// payments' source, bank line and nullable user (D2); every identity starting
// at 1001. The Down removes all of it and restores the payments as 00035 made
// them.
func TestInvoicesReceivables_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, receivablesVersion) // 00041_invoices_receivables.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	want := map[string][]string{
		"columns":     receivablesColumns,
		"tables":      {"bank_files", "bank_import_accounts", "bank_transaction_events", "bank_transactions", "charge_payments", "charge_waivers", "collection_handoffs", "collection_rates", "customer_reminder_policies", "invoice_holds", "manual_deliveries", "reminder_print_batches", "reminder_runs", "reminder_settings", "reminders"},
		"constraints": receivablesConstraints,
		"triggers": {
			"bank_files:tr_bank_files_immutable:refuse_bank_file_change",
			"bank_import_accounts:tr_bank_import_accounts_immutable:refuse_bank_import_account_change",
			"bank_transaction_events:tr_bank_transaction_events_immutable:refuse_bank_transaction_event_change",
			"bank_transactions:tr_bank_transactions_immutable:refuse_bank_transaction_change",
			"charge_payments:tr_charge_payments_immutable:refuse_charge_payment_change",
			"charge_payments:tr_charge_payments_parent:guard_child_of_issued_insert",
			"charge_waivers:tr_charge_waivers_immutable:refuse_waiver_change",
			"charge_waivers:tr_charge_waivers_parent:guard_child_of_issued_insert",
			"collection_handoffs:tr_collection_handoffs_immutable:refuse_handoff_change",
			"collection_handoffs:tr_collection_handoffs_parent:guard_child_of_issued_insert",
			"collection_rates:tr_collection_rates_append_only:refuse_collection_rate_change",
			"invoice_holds:tr_invoice_holds_immutable:refuse_hold_change",
			"invoice_holds:tr_invoice_holds_parent:guard_child_of_issued_insert",
			"manual_deliveries:tr_manual_deliveries_immutable:refuse_manual_delivery_change",
			"manual_deliveries:tr_manual_deliveries_parent:guard_child_of_issued_insert",
			"reminder_print_batches:tr_reminder_print_batches_immutable:refuse_print_batch_change",
			"reminder_runs:tr_reminder_runs_immutable:refuse_reminder_run_change",
			"reminder_settings:tr_reminder_settings_kept:refuse_row_delete",
			"reminders:tr_reminders_immutable:refuse_reminder_change",
			"reminders:tr_reminders_parent:guard_reminder_insert",
		},
		"functions": {
			"guard_child_of_issued_insert:v:trigger",
			"guard_reminder_insert:v:trigger",
			"refuse_bank_file_change:v:trigger",
			"refuse_bank_import_account_change:v:trigger",
			"refuse_bank_transaction_change:v:trigger",
			"refuse_bank_transaction_event_change:v:trigger",
			"refuse_charge_payment_change:v:trigger",
			"refuse_collection_rate_change:v:trigger",
			"refuse_handoff_change:v:trigger",
			"refuse_hold_change:v:trigger",
			"refuse_manual_delivery_change:v:trigger",
			"refuse_print_batch_change:v:trigger",
			"refuse_reminder_change:v:trigger",
			"refuse_reminder_run_change:v:trigger",
			"refuse_row_delete:v:trigger",
			"refuse_waiver_change:v:trigger",
			"seed_collection_rate:v:void",
		},
		"indexes": receivablesIndexes,
		// Every identity starts at 1001, as the module's other tables do.
		"sequences": {
			"bank_files_id_seq:1001", "bank_transaction_events_id_seq:1001", "bank_transactions_id_seq:1001",
			"charge_payments_id_seq:1001", "charge_waivers_id_seq:1001", "collection_handoffs_id_seq:1001",
			"collection_rates_id_seq:1001", "invoice_holds_id_seq:1001", "manual_deliveries_id_seq:1001",
			"reminder_print_batches_id_seq:1001", "reminder_runs_id_seq:1001", "reminders_id_seq:1001",
		},
	}
	// After the Down: the payments' user is required again, and nothing else
	// of 00041's is left.
	wantDown := map[string][]string{
		"columns": {"payments.registered_by_user_id:uuid::NO:"},
	}
	expect := func(when string, want map[string][]string) {
		t.Helper()
		got := receivablesObjects(t, ctx, pool)
		for _, what := range []string{"columns", "tables", "triggers", "constraints", "functions", "indexes", "sequences"} {
			if !equalStrings(got[what], want[what]) {
				t.Errorf("%s: %s =\n%s\nwant\n%s", when, what, strings.Join(got[what], "\n"), strings.Join(want[what], "\n"))
			}
		}
	}
	expect("after up", want)

	// The partial indexes' predicates, read on their own: the fingerprint's
	// floor leaves the duplicate rows out (D3, NI2), and the soft key reads
	// only lines with a KID.
	for name, def := range map[string]string{
		"ux_bank_transactions_fingerprint": "CREATE UNIQUE INDEX ux_bank_transactions_fingerprint ON invoices.bank_transactions USING btree (account, fingerprint) WHERE (duplicate_of_id IS NULL)",
		"ix_bank_transactions_soft":        "CREATE INDEX ix_bank_transactions_soft ON invoices.bank_transactions USING btree (account, booked_on, amount, kid) WHERE (kid IS NOT NULL)",
	} {
		if got := indexDefinition(t, ctx, pool, "invoices", name); got != def {
			t.Errorf("%s = %s, want %s", name, got, def)
		}
	}

	migrateTo(t, url, receivablesVersion-1)
	expect("after down", wantDown)

	migrateTo(t, url, receivablesVersion)
	expect("after up again", want)
}

// receivablesColumns is D2, D3, D6, D7, D8, D9, D10 and D11 written out.
var receivablesColumns = []string{
	"bank_files.accounts:ARRAY::NO:",
	"bank_files.byte_size:integer::NO:",
	"bank_files.duplicates:integer::YES:",
	"bank_files.file_identity:character varying:200:NO:",
	"bank_files.first_booked_on:date::YES:",
	"bank_files.format:character varying:10:NO:",
	"bank_files.id:bigint::NO:",
	"bank_files.ignored:integer::NO:",
	"bank_files.ignored_kinds:jsonb::NO:",
	"bank_files.last_booked_on:date::YES:",
	"bank_files.object_key:character varying:300:NO:",
	"bank_files.sha256:character:64:NO:",
	"bank_files.transactions:integer::NO:",
	"bank_files.uploaded_at:timestamp with time zone::NO:",
	"bank_files.uploaded_by_user_id:uuid::NO:",
	"bank_import_accounts.account:character varying:11:NO:",
	"bank_import_accounts.cutover_through:date::YES:",
	"bank_import_accounts.format:character varying:10:NO:",
	"bank_import_accounts.previous_format:character varying:10:YES:",
	"bank_import_accounts.set_at:timestamp with time zone::NO:",
	"bank_import_accounts.set_by_user_id:uuid::NO:",
	"bank_transaction_events.at:timestamp with time zone::NO:",
	"bank_transaction_events.bank_transaction_id:bigint::NO:",
	"bank_transaction_events.by_user_id:uuid::NO:",
	"bank_transaction_events.event:character varying:20:NO:",
	"bank_transaction_events.id:bigint::NO:",
	"bank_transaction_events.note:character varying:500:NO:''::character varying",
	"bank_transaction_events.reason:character varying:30:YES:",
	"bank_transactions.account:character varying:11:NO:",
	"bank_transactions.amount:numeric:14,2:NO:",
	"bank_transactions.archive_ref:character varying:35:NO:''::character varying",
	"bank_transactions.bank_code:character varying:35:NO:''::character varying",
	"bank_transactions.bank_file_id:bigint::NO:",
	"bank_transactions.booked_on:date::NO:",
	"bank_transactions.currency:character:3:NO:",
	"bank_transactions.debtor_account:character varying:34:NO:''::character varying",
	"bank_transactions.debtor_name:character varying:140:NO:''::character varying",
	"bank_transactions.direction:character varying:6:NO:",
	"bank_transactions.duplicate_of_id:bigint::YES:",
	"bank_transactions.fingerprint:character:64:NO:",
	"bank_transactions.format:character varying:10:NO:",
	"bank_transactions.id:bigint::NO:",
	"bank_transactions.kid:character varying:25:YES:",
	"bank_transactions.line_ref:character varying:60:NO:",
	"bank_transactions.negative:boolean::NO:false",
	"bank_transactions.ordered_on:date::YES:",
	"bank_transactions.ordinal:smallint::NO:",
	"bank_transactions.reason:character varying:30:YES:",
	"bank_transactions.remittance_text:character varying:1000:NO:''::character varying",
	"bank_transactions.resolution:character varying:25:YES:",
	"bank_transactions.resolution_note:character varying:500:NO:''::character varying",
	"bank_transactions.resolved_at:timestamp with time zone::YES:",
	"bank_transactions.resolved_by_user_id:uuid::YES:",
	"bank_transactions.status:character varying:10:NO:'pending'::character varying",
	"bank_transactions.suggested_invoice_id:bigint::YES:",
	"bank_transactions.value_on:date::YES:",
	"charge_payments.amount:numeric:14,2:NO:",
	"charge_payments.bank_transaction_id:bigint::YES:",
	"charge_payments.currency:character:3:NO:",
	"charge_payments.id:bigint::NO:",
	"charge_payments.invoice_id:bigint::NO:",
	"charge_payments.note:character varying:500:NO:''::character varying",
	"charge_payments.paid_on:date::NO:",
	"charge_payments.reference:character varying:100:NO:''::character varying",
	"charge_payments.registered_at:timestamp with time zone::NO:",
	"charge_payments.registered_by_user_id:uuid::NO:",
	"charge_payments.removal_reason:character varying:200:YES:",
	"charge_payments.removed_at:timestamp with time zone::YES:",
	"charge_payments.removed_by_user_id:uuid::YES:",
	"charge_payments.source:character varying:10:NO:",
	"charge_waivers.amount:numeric:14,2:NO:",
	"charge_waivers.id:bigint::NO:",
	"charge_waivers.interest_through:date::YES:",
	"charge_waivers.invoice_id:bigint::NO:",
	"charge_waivers.kind:character varying:12:NO:",
	"charge_waivers.note:character varying:500:NO:''::character varying",
	"charge_waivers.reason:character varying:20:NO:",
	"charge_waivers.reminder_id:bigint::NO:",
	"charge_waivers.waived_at:timestamp with time zone::NO:",
	"charge_waivers.waived_by_user_id:uuid::NO:",
	"collection_handoffs.agency:character varying:200:NO:",
	"collection_handoffs.agency_reference:character varying:100:NO:''::character varying",
	"collection_handoffs.created_at:timestamp with time zone::NO:",
	"collection_handoffs.created_by_user_id:uuid::NO:",
	"collection_handoffs.handed_on:date::NO:",
	"collection_handoffs.id:bigint::NO:",
	"collection_handoffs.invoice_id:bigint::NO:",
	"collection_handoffs.note:character varying:500:NO:''::character varying",
	"collection_handoffs.withdrawal_reason:character varying:200:YES:",
	"collection_handoffs.withdrawn_by_user_id:uuid::YES:",
	"collection_handoffs.withdrawn_on:date::YES:",
	"collection_rates.created_at:timestamp with time zone::NO:",
	"collection_rates.created_by_user_id:uuid::YES:",
	"collection_rates.id:bigint::NO:",
	"collection_rates.kind:character varying:30:NO:",
	"collection_rates.release_source_ref:character varying:100:YES:",
	"collection_rates.release_value:numeric:10,2:YES:",
	"collection_rates.source_ref:character varying:100:NO:",
	"collection_rates.valid_from:date::NO:",
	"collection_rates.value:numeric:10,2:NO:",
	"customer_reminder_policies.customer_id:integer::NO:",
	"customer_reminder_policies.mode:character varying:12:NO:",
	"customer_reminder_policies.note:character varying:500:NO:''::character varying",
	"customer_reminder_policies.updated_at:timestamp with time zone::NO:",
	"customer_reminder_policies.updated_by_user_id:uuid::NO:",
	"invoice_holds.charges_allowed:boolean::YES:",
	"invoice_holds.id:bigint::NO:",
	"invoice_holds.invoice_id:bigint::NO:",
	"invoice_holds.kind:character varying:10:NO:",
	"invoice_holds.lift_note:character varying:500:YES:",
	"invoice_holds.lifted_at:timestamp with time zone::YES:",
	"invoice_holds.lifted_by_user_id:uuid::YES:",
	"invoice_holds.note:character varying:500:NO:",
	"invoice_holds.placed_at:timestamp with time zone::NO:",
	"invoice_holds.placed_by_user_id:uuid::NO:",
	"manual_deliveries.delivered_on:date::NO:",
	"manual_deliveries.id:bigint::NO:",
	"manual_deliveries.invoice_id:bigint::NO:",
	"manual_deliveries.kind:character varying:12:NO:",
	"manual_deliveries.note:character varying:500:NO:''::character varying",
	"manual_deliveries.recorded_at:timestamp with time zone::NO:",
	"manual_deliveries.recorded_by_user_id:uuid::NO:",
	"manual_deliveries.removal_reason:character varying:200:YES:",
	"manual_deliveries.removed_at:timestamp with time zone::YES:",
	"manual_deliveries.removed_by_user_id:uuid::YES:",
	"payments.bank_transaction_id:bigint::YES:",
	"payments.registered_by_user_id:uuid::YES:",
	"payments.source:character varying:10:NO:'manual'::character varying",
	"reminder_print_batches.created_at:timestamp with time zone::NO:",
	"reminder_print_batches.created_by_user_id:uuid::NO:",
	"reminder_print_batches.id:bigint::NO:",
	"reminder_print_batches.post_on:date::NO:",
	"reminder_print_batches.posted_at:timestamp with time zone::YES:",
	"reminder_print_batches.posted_by_user_id:uuid::YES:",
	"reminder_print_batches.posted_on:date::YES:",
	"reminder_print_batches.reprinted_at:timestamp with time zone::YES:",
	"reminder_runs.created_at:timestamp with time zone::NO:",
	"reminder_runs.created_by_user_id:uuid::NO:",
	"reminder_runs.id:bigint::NO:",
	"reminder_runs.last_booked_on:date::YES:",
	"reminder_runs.letters:integer::YES:",
	"reminder_runs.run_on:date::NO:",
	"reminder_runs.skipped:integer::YES:",
	"reminder_runs.stale_import_acknowledged:boolean::NO:",
	"reminder_settings.business_charge:character varying:12:NO:'fee'::character varying",
	"reminder_settings.collection_notice:boolean::NO:true",
	"reminder_settings.deadline_days:integer::NO:14",
	"reminder_settings.enabled:boolean::NO:false",
	"reminder_settings.first_reminder_days:integer::NO:14",
	"reminder_settings.grace_days:integer::NO:3",
	"reminder_settings.id:smallint::NO:",
	"reminder_settings.inkassolov_2026_from:date::YES:",
	"reminder_settings.late_interest:boolean::NO:false",
	"reminder_settings.person_charge:character varying:12:NO:'fee'::character varying",
	"reminder_settings.regime_reviewed_at:timestamp with time zone::NO:",
	"reminder_settings.regime_reviewed_by_user_id:uuid::YES:",
	"reminder_settings.regime_reviewed_through:date::NO:",
	"reminder_settings.reminders_before_notice:integer::NO:1",
	"reminder_settings.revision:integer::NO:1",
	"reminder_settings.stale_import_days:integer::NO:3",
	"reminder_settings.updated_at:timestamp with time zone::NO:",
	"reminder_settings.updated_by_user_id:uuid::YES:",
	"reminders.announces_collection:boolean::NO:false",
	"reminders.attempts:integer::NO:0",
	"reminders.channel:character varying:5:NO:",
	"reminders.charge_notes:ARRAY::YES:",
	"reminders.charges_earlier:numeric:14,2:YES:",
	"reminders.compensation:numeric:14,2:YES:",
	"reminders.created_at:timestamp with time zone::NO:",
	"reminders.created_by_user_id:uuid::NO:",
	"reminders.deadline:date::YES:",
	"reminders.failed_at:timestamp with time zone::YES:",
	"reminders.fee:numeric:14,2:YES:",
	"reminders.fee_kind:character varying:15:YES:",
	"reminders.first_attempt_at:timestamp with time zone::YES:",
	"reminders.held_reason:character varying:30:YES:",
	"reminders.id:bigint::NO:",
	"reminders.inkassosats:numeric:10,2:YES:",
	"reminders.interest:numeric:14,2:YES:",
	"reminders.interest_from:date::YES:",
	"reminders.interest_paid:numeric:14,2:YES:",
	"reminders.interest_segments:jsonb::YES:",
	"reminders.interest_waived:numeric:14,2:YES:",
	"reminders.invoice_id:bigint::NO:",
	"reminders.language:character:2:NO:",
	"reminders.last_error:character varying:500:YES:",
	"reminders.lease_id:character varying:100:YES:",
	"reminders.lease_until:timestamp with time zone::YES:",
	"reminders.level:character varying:20:NO:",
	"reminders.message_id:character varying:200:YES:",
	"reminders.next_attempt_at:timestamp with time zone::YES:",
	"reminders.pdf_object_key:character varying:300:YES:",
	"reminders.pdf_sha256:character:64:YES:",
	"reminders.principal_open:numeric:14,2:YES:",
	"reminders.print_batch_id:bigint::YES:",
	"reminders.recipient:character varying:254:NO:''::character varying",
	"reminders.regime:character varying:15:YES:",
	"reminders.run_id:bigint::NO:",
	"reminders.sent_at:timestamp with time zone::YES:",
	"reminders.sent_on:date::YES:",
	"reminders.sequence:smallint::NO:",
	"reminders.status:character varying:15:NO:",
	"reminders.total:numeric:14,2:YES:",
	"reminders.withdrawal_reason:character varying:200:YES:",
	"reminders.withdrawn_at:timestamp with time zone::YES:",
	"reminders.withdrawn_by_user_id:uuid::YES:",
}

// receivablesConstraints is every constraint of the new tables, and the
// payments' new three.
var receivablesConstraints = []string{
	"invoices.bank_files:bank_files_pkey:PRIMARY KEY (id)",
	"invoices.bank_files:ck_bank_files_counts:CHECK (((transactions >= 0) AND (ignored >= 0) AND (duplicates >= 0)))",
	"invoices.bank_files:ck_bank_files_format:CHECK (((format)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying])::text[])))",
	"invoices.bank_files:ck_bank_files_ignored_kinds:CHECK ((jsonb_typeof(ignored_kinds) = 'object'::text))",
	"invoices.bank_files:uq_bank_files_identity:UNIQUE (format, file_identity)",
	"invoices.bank_files:uq_bank_files_sha256:UNIQUE (sha256)",
	"invoices.bank_import_accounts:bank_import_accounts_pkey:PRIMARY KEY (account)",
	"invoices.bank_import_accounts:ck_bank_import_accounts_cutover:CHECK (((previous_format IS NULL) <= (cutover_through IS NULL)))",
	"invoices.bank_import_accounts:ck_bank_import_accounts_format:CHECK (((format)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying])::text[])))",
	"invoices.bank_import_accounts:ck_bank_import_accounts_previous_format:CHECK (((previous_format)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying])::text[])))",
	"invoices.bank_transaction_events:bank_transaction_events_bank_transaction_id_fkey:FOREIGN KEY (bank_transaction_id) REFERENCES invoices.bank_transactions(id) ON DELETE RESTRICT",
	"invoices.bank_transaction_events:bank_transaction_events_pkey:PRIMARY KEY (id)",
	"invoices.bank_transaction_events:ck_bank_transaction_events_event:CHECK (((event)::text = ANY ((ARRAY['matched'::character varying, 'queued'::character varying, 'applied'::character varying, 'dismissed'::character varying, 'reversal_handled'::character varying, 'reopened'::character varying, 'duplicate_confirmed'::character varying, 'treated_as_distinct'::character varying])::text[])))",
	"invoices.bank_transaction_events:ck_bank_transaction_events_reason:CHECK (((reason IS NULL) OR ((reason)::text = ANY ((ARRAY['kid_invalid'::character varying, 'kid_unknown'::character varying, 'invoice_credited'::character varying, 'invoice_settled'::character varying, 'exceeds_open'::character varying, 'no_kid'::character varying, 'negative_amount'::character varying, 'reversal'::character varying, 'vipps_payout'::character varying, 'paid_before_issue'::character varying, 'account_mismatch'::character varying, 'possible_duplicate'::character varying, 'payment_removed'::character varying])::text[]))))",
	"invoices.bank_transactions:bank_transactions_bank_file_id_fkey:FOREIGN KEY (bank_file_id) REFERENCES invoices.bank_files(id) ON DELETE RESTRICT",
	"invoices.bank_transactions:bank_transactions_duplicate_of_id_fkey:FOREIGN KEY (duplicate_of_id) REFERENCES invoices.bank_transactions(id)",
	"invoices.bank_transactions:bank_transactions_pkey:PRIMARY KEY (id)",
	"invoices.bank_transactions:ck_bank_transactions_amount:CHECK ((amount > (0)::numeric))",
	"invoices.bank_transactions:ck_bank_transactions_currency:CHECK ((currency = 'NOK'::bpchar))",
	"invoices.bank_transactions:ck_bank_transactions_direction:CHECK (((direction)::text = ANY ((ARRAY['credit'::character varying, 'debit'::character varying])::text[])))",
	"invoices.bank_transactions:ck_bank_transactions_format:CHECK (((format)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying])::text[])))",
	"invoices.bank_transactions:ck_bank_transactions_ordinal:CHECK ((ordinal >= 1))",
	"invoices.bank_transactions:ck_bank_transactions_reason:CHECK (((reason IS NULL) OR ((reason)::text = ANY ((ARRAY['kid_invalid'::character varying, 'kid_unknown'::character varying, 'invoice_credited'::character varying, 'invoice_settled'::character varying, 'exceeds_open'::character varying, 'no_kid'::character varying, 'negative_amount'::character varying, 'reversal'::character varying, 'vipps_payout'::character varying, 'paid_before_issue'::character varying, 'account_mismatch'::character varying, 'possible_duplicate'::character varying, 'payment_removed'::character varying])::text[]))))",
	"invoices.bank_transactions:ck_bank_transactions_resolution:CHECK (((resolution)::text = ANY ((ARRAY['applied'::character varying, 'not_customer_payment'::character varying, 'reversal_handled'::character varying, 'duplicate_confirmed'::character varying])::text[])))",
	"invoices.bank_transactions:ck_bank_transactions_state:CHECK (((((status)::text = 'exception'::text) <= (reason IS NOT NULL)) AND (((status)::text = 'resolved'::text) = ((resolution IS NOT NULL) AND (resolved_at IS NOT NULL))) AND (((status)::text = 'duplicate'::text) <= (duplicate_of_id IS NOT NULL)) AND ((duplicate_of_id IS NULL) OR ((status)::text = ANY ((ARRAY['duplicate'::character varying, 'exception'::character varying, 'resolved'::character varying])::text[])))))",
	"invoices.bank_transactions:ck_bank_transactions_status:CHECK (((status)::text = ANY ((ARRAY['pending'::character varying, 'matched'::character varying, 'exception'::character varying, 'resolved'::character varying, 'duplicate'::character varying])::text[])))",
	"invoices.charge_payments:charge_payments_bank_transaction_id_fkey:FOREIGN KEY (bank_transaction_id) REFERENCES invoices.bank_transactions(id) ON DELETE RESTRICT",
	"invoices.charge_payments:charge_payments_invoice_id_fkey:FOREIGN KEY (invoice_id) REFERENCES invoices.invoices(id) ON DELETE RESTRICT",
	"invoices.charge_payments:charge_payments_pkey:PRIMARY KEY (id)",
	"invoices.charge_payments:ck_charge_payments_amount:CHECK ((amount > (0)::numeric))",
	"invoices.charge_payments:ck_charge_payments_origin:CHECK ((((source)::text = 'manual'::text) = (bank_transaction_id IS NULL)))",
	"invoices.charge_payments:ck_charge_payments_removal:CHECK ((((removed_at IS NULL) AND (removed_by_user_id IS NULL) AND (removal_reason IS NULL)) OR ((removed_at IS NOT NULL) AND (removed_by_user_id IS NOT NULL) AND (removal_reason IS NOT NULL) AND ((removal_reason)::text <> ''::text))))",
	"invoices.charge_payments:ck_charge_payments_source:CHECK (((source)::text = ANY ((ARRAY['manual'::character varying, 'ocr'::character varying, 'camt054'::character varying])::text[])))",
	"invoices.charge_waivers:charge_waivers_invoice_id_fkey:FOREIGN KEY (invoice_id) REFERENCES invoices.invoices(id) ON DELETE RESTRICT",
	"invoices.charge_waivers:charge_waivers_pkey:PRIMARY KEY (id)",
	"invoices.charge_waivers:ck_charge_waivers_amount:CHECK ((amount > (0)::numeric))",
	"invoices.charge_waivers:ck_charge_waivers_interest_through:CHECK ((((kind)::text = 'interest'::text) = (interest_through IS NOT NULL)))",
	"invoices.charge_waivers:ck_charge_waivers_kind:CHECK (((kind)::text = ANY ((ARRAY['fee'::character varying, 'compensation'::character varying, 'interest'::character varying])::text[])))",
	"invoices.charge_waivers:ck_charge_waivers_reason:CHECK (((reason)::text = ANY ((ARRAY['objection_upheld'::character varying, 'claimed_in_error'::character varying, 'goodwill'::character varying, 'deadline_met'::character varying])::text[])))",
	"invoices.charge_waivers:fk_charge_waivers_reminder:FOREIGN KEY (reminder_id, invoice_id) REFERENCES invoices.reminders(id, invoice_id) ON DELETE RESTRICT",
	"invoices.collection_handoffs:ck_collection_handoffs_withdrawal:CHECK ((((withdrawn_on IS NULL) AND (withdrawn_by_user_id IS NULL) AND (withdrawal_reason IS NULL)) OR ((withdrawn_on IS NOT NULL) AND (withdrawn_by_user_id IS NOT NULL) AND (withdrawal_reason IS NOT NULL) AND ((withdrawal_reason)::text <> ''::text))))",
	"invoices.collection_handoffs:collection_handoffs_invoice_id_fkey:FOREIGN KEY (invoice_id) REFERENCES invoices.invoices(id) ON DELETE RESTRICT",
	"invoices.collection_handoffs:collection_handoffs_pkey:PRIMARY KEY (id)",
	"invoices.collection_rates:ck_collection_rates_half_year:CHECK ((((kind)::text = 'inkassosats'::text) OR ((EXTRACT(day FROM valid_from) = (1)::numeric) AND (EXTRACT(month FROM valid_from) = ANY (ARRAY[(1)::numeric, (7)::numeric])))))",
	"invoices.collection_rates:ck_collection_rates_kind:CHECK (((kind)::text = ANY ((ARRAY['late_interest_percent'::character varying, 'inkassosats'::character varying, 'b2b_compensation_nok'::character varying])::text[])))",
	"invoices.collection_rates:ck_collection_rates_release:CHECK (((release_value > (0)::numeric) AND ((release_value IS NULL) = (release_source_ref IS NULL))))",
	"invoices.collection_rates:ck_collection_rates_value:CHECK ((value > (0)::numeric))",
	"invoices.collection_rates:collection_rates_pkey:PRIMARY KEY (id)",
	"invoices.collection_rates:uq_collection_rates_kind_valid_from:UNIQUE (kind, valid_from)",
	"invoices.customer_reminder_policies:ck_customer_reminder_policies_mode:CHECK (((mode)::text = ANY ((ARRAY['normal'::character varying, 'no_charges'::character varying, 'none'::character varying])::text[])))",
	"invoices.customer_reminder_policies:customer_reminder_policies_pkey:PRIMARY KEY (customer_id)",
	"invoices.invoice_holds:ck_invoice_holds_kind:CHECK (((kind)::text = 'disputed'::text))",
	"invoices.invoice_holds:ck_invoice_holds_lift:CHECK ((((lifted_at IS NULL) = (lifted_by_user_id IS NULL)) AND ((lifted_at IS NULL) = (charges_allowed IS NULL))))",
	"invoices.invoice_holds:invoice_holds_invoice_id_fkey:FOREIGN KEY (invoice_id) REFERENCES invoices.invoices(id) ON DELETE RESTRICT",
	"invoices.invoice_holds:invoice_holds_pkey:PRIMARY KEY (id)",
	"invoices.manual_deliveries:ck_manual_deliveries_kind:CHECK (((kind)::text = ANY ((ARRAY['handed_over'::character varying, 'posted'::character varying])::text[])))",
	"invoices.manual_deliveries:ck_manual_deliveries_removal:CHECK ((((removed_at IS NULL) AND (removed_by_user_id IS NULL) AND (removal_reason IS NULL)) OR ((removed_at IS NOT NULL) AND (removed_by_user_id IS NOT NULL) AND (removal_reason IS NOT NULL) AND ((removal_reason)::text <> ''::text))))",
	"invoices.manual_deliveries:manual_deliveries_invoice_id_fkey:FOREIGN KEY (invoice_id) REFERENCES invoices.invoices(id) ON DELETE RESTRICT",
	"invoices.manual_deliveries:manual_deliveries_pkey:PRIMARY KEY (id)",
	"invoices.payments:ck_payments_origin:CHECK (((((source)::text = 'manual'::text) AND (bank_transaction_id IS NULL) AND (registered_by_user_id IS NOT NULL)) OR (((source)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying])::text[])) AND (bank_transaction_id IS NOT NULL) AND (registered_by_user_id IS NOT NULL))))",
	"invoices.payments:ck_payments_source:CHECK (((source)::text = ANY ((ARRAY['manual'::character varying, 'ocr'::character varying, 'camt054'::character varying])::text[])))",
	"invoices.payments:payments_bank_transaction_id_fkey:FOREIGN KEY (bank_transaction_id) REFERENCES invoices.bank_transactions(id) ON DELETE RESTRICT",
	"invoices.reminder_print_batches:ck_reminder_print_batches_posted:CHECK ((((posted_on IS NULL) = (posted_at IS NULL)) AND ((posted_at IS NULL) = (posted_by_user_id IS NULL))))",
	"invoices.reminder_print_batches:ck_reminder_print_batches_reprinted:CHECK (((posted_on IS NULL) OR (reprinted_at IS NULL)))",
	"invoices.reminder_print_batches:reminder_print_batches_pkey:PRIMARY KEY (id)",
	"invoices.reminder_runs:ck_reminder_runs_counts:CHECK (((letters >= 0) AND (skipped >= 0)))",
	"invoices.reminder_runs:reminder_runs_pkey:PRIMARY KEY (id)",
	"invoices.reminder_settings:ck_reminder_settings_business_charge:CHECK (((business_charge)::text = ANY ((ARRAY['fee'::character varying, 'compensation'::character varying, 'none'::character varying])::text[])))",
	"invoices.reminder_settings:ck_reminder_settings_deadline_days:CHECK (((deadline_days >= 14) AND (deadline_days <= 60)))",
	"invoices.reminder_settings:ck_reminder_settings_first_reminder_days:CHECK (((first_reminder_days >= 1) AND (first_reminder_days <= 60)))",
	"invoices.reminder_settings:ck_reminder_settings_grace_days:CHECK (((grace_days >= 1) AND (grace_days <= 10)))",
	"invoices.reminder_settings:ck_reminder_settings_person_charge:CHECK (((person_charge)::text = ANY ((ARRAY['fee'::character varying, 'none'::character varying])::text[])))",
	"invoices.reminder_settings:ck_reminder_settings_reminders_before_notice:CHECK (((reminders_before_notice >= 0) AND (reminders_before_notice <= 2)))",
	"invoices.reminder_settings:ck_reminder_settings_single_row:CHECK ((id = 1))",
	"invoices.reminder_settings:ck_reminder_settings_stale_import_days:CHECK (((stale_import_days >= 1) AND (stale_import_days <= 30)))",
	"invoices.reminder_settings:reminder_settings_pkey:PRIMARY KEY (id)",
	"invoices.reminders:ck_reminders_channel:CHECK (((channel)::text = ANY ((ARRAY['email'::character varying, 'paper'::character varying])::text[])))",
	"invoices.reminders:ck_reminders_channel_status:CHECK ((((((status)::text = ANY ((ARRAY['awaiting_print'::character varying, 'printed'::character varying])::text[])) OR (print_batch_id IS NOT NULL)) <= ((channel)::text = 'paper'::text)) AND (((status)::text = ANY ((ARRAY['queued'::character varying, 'failed'::character varying])::text[])) <= ((channel)::text = 'email'::text))))",
	"invoices.reminders:ck_reminders_charges:CHECK ((((fee_kind IS NULL) OR ((((fee_kind)::text = 'reminder_fee'::text) = (fee IS NOT NULL)) AND (((fee_kind)::text = 'compensation'::text) = (compensation IS NOT NULL)))) AND (fee > (0)::numeric) AND (compensation > (0)::numeric)))",
	"invoices.reminders:ck_reminders_fee_kind:CHECK (((fee_kind)::text = ANY ((ARRAY['none'::character varying, 'reminder_fee'::character varying, 'compensation'::character varying])::text[])))",
	"invoices.reminders:ck_reminders_held_reason:CHECK (((held_reason)::text = ANY ((ARRAY['collection_rates_outdated'::character varying, 'collection_regime_unreviewed'::character varying])::text[])))",
	"invoices.reminders:ck_reminders_level:CHECK (((level)::text = ANY ((ARRAY['reminder'::character varying, 'collection_notice'::character varying])::text[])))",
	"invoices.reminders:ck_reminders_regime:CHECK (((regime)::text = ANY ((ARRAY['inkassolov_1988'::character varying, 'inkassolov_2026'::character varying])::text[])))",
	"invoices.reminders:ck_reminders_state:CHECK (((((status)::text <> 'sent'::text) OR ((sent_on IS NOT NULL) AND (deadline IS NOT NULL) AND (regime IS NOT NULL) AND (principal_open IS NOT NULL) AND (fee_kind IS NOT NULL) AND (charges_earlier IS NOT NULL) AND (interest IS NOT NULL) AND (interest_waived IS NOT NULL) AND (interest_paid IS NOT NULL) AND (total IS NOT NULL) AND (sent_at IS NOT NULL))) AND (((status)::text <> 'printed'::text) OR ((sent_on IS NOT NULL) AND (deadline IS NOT NULL) AND (regime IS NOT NULL) AND (principal_open IS NOT NULL) AND (fee_kind IS NOT NULL) AND (charges_earlier IS NOT NULL) AND (interest IS NOT NULL) AND (interest_waived IS NOT NULL) AND (interest_paid IS NOT NULL) AND (total IS NOT NULL) AND (print_batch_id IS NOT NULL))) AND (((status)::text <> 'withdrawn'::text) OR ((withdrawn_at IS NOT NULL) AND (withdrawal_reason IS NOT NULL) AND ((withdrawal_reason)::text <> ''::text))) AND (((status)::text <> 'failed'::text) OR (failed_at IS NOT NULL))))",
	"invoices.reminders:ck_reminders_status:CHECK (((status)::text = ANY ((ARRAY['queued'::character varying, 'awaiting_print'::character varying, 'printed'::character varying, 'sent'::character varying, 'withdrawn'::character varying, 'failed'::character varying])::text[])))",
	"invoices.reminders:reminders_invoice_id_fkey:FOREIGN KEY (invoice_id) REFERENCES invoices.invoices(id) ON DELETE RESTRICT",
	"invoices.reminders:reminders_pkey:PRIMARY KEY (id)",
	"invoices.reminders:reminders_print_batch_id_fkey:FOREIGN KEY (print_batch_id) REFERENCES invoices.reminder_print_batches(id)",
	"invoices.reminders:reminders_run_id_fkey:FOREIGN KEY (run_id) REFERENCES invoices.reminder_runs(id)",
	"invoices.reminders:uq_reminders_id_invoice:UNIQUE (id, invoice_id)",
	"invoices.reminders:uq_reminders_invoice_sequence:UNIQUE (invoice_id, sequence)",
}

// receivablesIndexes is every index of the new tables and the payments' new
// one.
var receivablesIndexes = []string{
	"bank_files_pkey:CREATE UNIQUE INDEX bank_files_pkey ON invoices.bank_files USING btree (id)",
	"bank_import_accounts_pkey:CREATE UNIQUE INDEX bank_import_accounts_pkey ON invoices.bank_import_accounts USING btree (account)",
	"bank_transaction_events_pkey:CREATE UNIQUE INDEX bank_transaction_events_pkey ON invoices.bank_transaction_events USING btree (id)",
	"bank_transactions_pkey:CREATE UNIQUE INDEX bank_transactions_pkey ON invoices.bank_transactions USING btree (id)",
	"charge_payments_pkey:CREATE UNIQUE INDEX charge_payments_pkey ON invoices.charge_payments USING btree (id)",
	"charge_waivers_pkey:CREATE UNIQUE INDEX charge_waivers_pkey ON invoices.charge_waivers USING btree (id)",
	"collection_handoffs_pkey:CREATE UNIQUE INDEX collection_handoffs_pkey ON invoices.collection_handoffs USING btree (id)",
	"collection_rates_pkey:CREATE UNIQUE INDEX collection_rates_pkey ON invoices.collection_rates USING btree (id)",
	"customer_reminder_policies_pkey:CREATE UNIQUE INDEX customer_reminder_policies_pkey ON invoices.customer_reminder_policies USING btree (customer_id)",
	"invoice_holds_pkey:CREATE UNIQUE INDEX invoice_holds_pkey ON invoices.invoice_holds USING btree (id)",
	"ix_bank_transaction_events_line:CREATE INDEX ix_bank_transaction_events_line ON invoices.bank_transaction_events USING btree (bank_transaction_id)",
	"ix_bank_transactions_file:CREATE INDEX ix_bank_transactions_file ON invoices.bank_transactions USING btree (bank_file_id)",
	"ix_bank_transactions_open:CREATE INDEX ix_bank_transactions_open ON invoices.bank_transactions USING btree (status) WHERE ((status)::text = ANY ((ARRAY['pending'::character varying, 'exception'::character varying, 'duplicate'::character varying])::text[]))",
	"ix_bank_transactions_soft:CREATE INDEX ix_bank_transactions_soft ON invoices.bank_transactions USING btree (account, booked_on, amount, kid) WHERE (kid IS NOT NULL)",
	"ix_charge_payments_bank_transaction:CREATE INDEX ix_charge_payments_bank_transaction ON invoices.charge_payments USING btree (bank_transaction_id) WHERE (bank_transaction_id IS NOT NULL)",
	"ix_charge_payments_invoice_live:CREATE INDEX ix_charge_payments_invoice_live ON invoices.charge_payments USING btree (invoice_id) WHERE (removed_at IS NULL)",
	"ix_charge_waivers_invoice:CREATE INDEX ix_charge_waivers_invoice ON invoices.charge_waivers USING btree (invoice_id)",
	"ix_manual_deliveries_invoice_live:CREATE INDEX ix_manual_deliveries_invoice_live ON invoices.manual_deliveries USING btree (invoice_id) WHERE (removed_at IS NULL)",
	"ix_payments_bank_transaction:CREATE INDEX ix_payments_bank_transaction ON invoices.payments USING btree (bank_transaction_id) WHERE (bank_transaction_id IS NOT NULL)",
	"ix_reminders_batch:CREATE INDEX ix_reminders_batch ON invoices.reminders USING btree (print_batch_id) WHERE (print_batch_id IS NOT NULL)",
	"ix_reminders_due:CREATE INDEX ix_reminders_due ON invoices.reminders USING btree (next_attempt_at) WHERE ((status)::text = 'queued'::text)",
	"ix_reminders_invoice:CREATE INDEX ix_reminders_invoice ON invoices.reminders USING btree (invoice_id)",
	"ix_reminders_run:CREATE INDEX ix_reminders_run ON invoices.reminders USING btree (run_id)",
	"ix_reminders_waiting:CREATE INDEX ix_reminders_waiting ON invoices.reminders USING btree (status) WHERE ((status)::text = ANY ((ARRAY['awaiting_print'::character varying, 'printed'::character varying, 'failed'::character varying])::text[]))",
	"manual_deliveries_pkey:CREATE UNIQUE INDEX manual_deliveries_pkey ON invoices.manual_deliveries USING btree (id)",
	"reminder_print_batches_pkey:CREATE UNIQUE INDEX reminder_print_batches_pkey ON invoices.reminder_print_batches USING btree (id)",
	"reminder_runs_pkey:CREATE UNIQUE INDEX reminder_runs_pkey ON invoices.reminder_runs USING btree (id)",
	"reminder_settings_pkey:CREATE UNIQUE INDEX reminder_settings_pkey ON invoices.reminder_settings USING btree (id)",
	"reminders_pkey:CREATE UNIQUE INDEX reminders_pkey ON invoices.reminders USING btree (id)",
	"uq_bank_files_identity:CREATE UNIQUE INDEX uq_bank_files_identity ON invoices.bank_files USING btree (format, file_identity)",
	"uq_bank_files_sha256:CREATE UNIQUE INDEX uq_bank_files_sha256 ON invoices.bank_files USING btree (sha256)",
	"uq_collection_rates_kind_valid_from:CREATE UNIQUE INDEX uq_collection_rates_kind_valid_from ON invoices.collection_rates USING btree (kind, valid_from)",
	"uq_reminders_id_invoice:CREATE UNIQUE INDEX uq_reminders_id_invoice ON invoices.reminders USING btree (id, invoice_id)",
	"uq_reminders_invoice_sequence:CREATE UNIQUE INDEX uq_reminders_invoice_sequence ON invoices.reminders USING btree (invoice_id, sequence)",
	"ux_bank_transactions_fingerprint:CREATE UNIQUE INDEX ux_bank_transactions_fingerprint ON invoices.bank_transactions USING btree (account, fingerprint) WHERE (duplicate_of_id IS NULL)",
	"ux_charge_waivers_letter_kind:CREATE UNIQUE INDEX ux_charge_waivers_letter_kind ON invoices.charge_waivers USING btree (reminder_id, kind) WHERE ((kind)::text = ANY ((ARRAY['fee'::character varying, 'compensation'::character varying])::text[]))",
	"ux_collection_handoffs_live:CREATE UNIQUE INDEX ux_collection_handoffs_live ON invoices.collection_handoffs USING btree (invoice_id) WHERE (withdrawn_on IS NULL)",
	"ux_invoice_holds_live:CREATE UNIQUE INDEX ux_invoice_holds_live ON invoices.invoice_holds USING btree (invoice_id) WHERE (lifted_at IS NULL)",
}

// receivablesFixture is a migrated database and the statements the
// behaviour tests build their rows with.
type receivablesFixture struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	next int64 // the next document number to issue
	seq  int   // the next unique suffix for a file's hash and identity
}

func newReceivablesFixture(t *testing.T) *receivablesFixture {
	t.Helper()
	pool, _ := testdb.Migrated(t)
	return &receivablesFixture{t: t, ctx: context.Background(), pool: pool, next: 1}
}

// exec runs sql and answers its error.
func (f *receivablesFixture) exec(sql string, args ...any) error {
	_, err := f.pool.Exec(f.ctx, sql, args...)
	return err
}

// mustExec is exec that must succeed.
func (f *receivablesFixture) mustExec(what, sql string, args ...any) {
	f.t.Helper()
	if err := f.exec(sql, args...); err != nil {
		f.t.Fatalf("%s: %v", what, err)
	}
}

// insert runs an INSERT … RETURNING id and answers the id or the refusal.
func (f *receivablesFixture) insert(sql string, args ...any) (int64, error) {
	var id int64
	err := f.pool.QueryRow(f.ctx, sql, args...).Scan(&id)
	return id, err
}

// mustInsert is insert that must succeed.
func (f *receivablesFixture) mustInsert(what, sql string, args ...any) int64 {
	f.t.Helper()
	id, err := f.insert(sql, args...)
	if err != nil {
		f.t.Fatalf("%s: %v", what, err)
	}
	return id
}

// text answers one text column of one row.
func (f *receivablesFixture) text(sql string, args ...any) string {
	f.t.Helper()
	var s string
	if err := f.pool.QueryRow(f.ctx, sql, args...).Scan(&s); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
	return s
}

// draft makes an invoice draft of customer, or a credit-note draft of
// credits when it is non-zero.
func (f *receivablesFixture) draft(customer int32, credits int64) int64 {
	f.t.Helper()
	kind := "invoice"
	var creditsID *int64
	if credits != 0 {
		kind, creditsID = "credit_note", &credits
	}
	return f.mustInsert("seed a "+kind+" draft", `
		INSERT INTO invoices.invoices (kind, customer_id, credits_invoice_id, created_by_user_id, created_at, updated_at)
		VALUES ($1, $2, $3, gen_random_uuid(), now(), now()) RETURNING id`, kind, customer, creditsID)
}

// issue moves id from draft to issued with the next number, as the issue's
// own update does.
func (f *receivablesFixture) issue(id int64) int64 {
	f.t.Helper()
	f.mustExec(fmt.Sprintf("issue %d", id), `
		UPDATE invoices.invoices SET status = 'issued', number = $2, issue_date = DATE '2026-10-05',
		    due_date = CASE WHEN kind = 'invoice' THEN DATE '2026-10-19' END, exchange_rate_date = DATE '2026-10-05',
		    seller_legal_name = 'Selger AS', buyer_name = 'Kunde AS', issued_at = now(), gross_total = 1250
		WHERE id = $1`, id, f.next)
	f.next++
	return id
}

// issued is an issued invoice of customer.
func (f *receivablesFixture) issued(customer int32) int64 {
	f.t.Helper()
	return f.issue(f.draft(customer, 0))
}

// erase marks customer anonymised, as the erase does.
func (f *receivablesFixture) erase(customer int32) {
	f.t.Helper()
	f.mustExec("mark the customer erased", `INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now())`, customer)
}

// bankFile writes a bank file, each one with its own hash and identity.
func (f *receivablesFixture) bankFile() int64 {
	f.t.Helper()
	f.seq++
	sha := fmt.Sprintf("%064x", f.seq)
	return f.mustInsert("seed a bank file", `
		INSERT INTO invoices.bank_files (format, sha256, file_identity, object_key, byte_size, accounts,
		    first_booked_on, last_booked_on, transactions, ignored, ignored_kinds, uploaded_by_user_id, uploaded_at)
		VALUES ('ocr', $1::text, $2, 'bank-files/' || $1::text || '.ocr', 400, ARRAY['15032080119'],
		    DATE '2026-10-01', DATE '2026-10-02', 2, 0, '{"debit":0,"not_booked":0,"card_information":0,"zero_amount":0}',
		    gen_random_uuid(), now()) RETURNING id`, sha, fmt.Sprintf("00008080:%d:00008080", f.seq))
}

// bankLine writes a line of file with fingerprint, a duplicate of dupOf when
// it is non-nil, in status with reason, and answers its id or the refusal.
func (f *receivablesFixture) bankLine(file int64, fingerprint string, dupOf *int64, status string, reason *string) (int64, error) {
	return f.insert(`
		INSERT INTO invoices.bank_transactions (bank_file_id, line_ref, format, account, direction, booked_on,
		    amount, currency, kid, fingerprint, ordinal, duplicate_of_id, status, reason)
		VALUES ($1, '1/1', 'ocr', '15032080119', 'credit', DATE '2026-10-01', 1250, 'NOK', '0010012', $2, 1, $3, $4, $5)
		RETURNING id`, file, fingerprint, dupOf, status, reason)
}

// mustBankLine is bankLine that must succeed, a live pending line.
func (f *receivablesFixture) mustBankLine(file int64, fingerprint string) int64 {
	f.t.Helper()
	id, err := f.bankLine(file, fingerprint, nil, "pending", nil)
	if err != nil {
		f.t.Fatalf("seed a bank line: %v", err)
	}
	return id
}

// run writes a reminder run.
func (f *receivablesFixture) run() int64 {
	f.t.Helper()
	return f.mustInsert("seed a reminder run", `
		INSERT INTO invoices.reminder_runs (run_on, created_at, created_by_user_id, stale_import_acknowledged)
		VALUES (DATE '2026-11-02', now(), gen_random_uuid(), false) RETURNING id`)
}

// reminder writes the sequence-th letter of invoice in run, queued by
// e-mail, and answers its id or the refusal.
func (f *receivablesFixture) reminder(invoice, run int64, sequence int) (int64, error) {
	return f.insert(`
		INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, recipient, language,
		    created_at, created_by_user_id, status)
		VALUES ($1, $2, $3, 'reminder', 'email', 'kari@example.no', 'nb', now(), gen_random_uuid(), 'queued')
		RETURNING id`, invoice, run, sequence)
}

// paperReminder writes the sequence-th letter of invoice in run, awaiting
// print on paper.
func (f *receivablesFixture) paperReminder(invoice, run int64, sequence int) int64 {
	f.t.Helper()
	return f.mustInsert("seed a paper letter", `
		INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, language,
		    created_at, created_by_user_id, status)
		VALUES ($1, $2, $3, 'reminder', 'paper', 'nb', now(), gen_random_uuid(), 'awaiting_print')
		RETURNING id`, invoice, run, sequence)
}

// mustReminder is reminder that must succeed.
func (f *receivablesFixture) mustReminder(invoice, run int64, sequence int) int64 {
	f.t.Helper()
	id, err := f.reminder(invoice, run, sequence)
	if err != nil {
		f.t.Fatalf("seed a reminder: %v", err)
	}
	return id
}

// The letter's facts as the dispatch writes them, for a status that needs
// them.
const receivablesFacts = receivablesFactsBase + `, fee_kind = 'reminder_fee', fee = 35`

// receivablesFactsBase is the facts but the charge: fee_kind, fee and
// compensation.
const receivablesFactsBase = `sent_on = DATE '2026-11-02', deadline = DATE '2026-11-16', regime = 'inkassolov_1988',
    principal_open = 1250, charges_earlier = 0, interest = 0, interest_waived = 0, interest_paid = 0, total = 1285`

// ptrTo is a pointer to v.
func ptrTo[T any](v T) *T { return &v }

// foreignKeyViolationOf reports whether err is a foreign key violation
// (SQLSTATE 23503) of the constraint named constraint.
func foreignKeyViolationOf(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == constraint
}

// uniqueViolationOf reports whether err is a unique violation (SQLSTATE
// 23505) of the index or constraint named constraint.
func uniqueViolationOf(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

const (
	receivablesPaymentImmutable = "invoices: a payment registration is immutable"
	receivablesPaymentInsert    = `
		INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, registered_by_user_id, registered_at,
		    source, bank_transaction_id)
		VALUES ($1, DATE '2026-10-10', 100, 'NOK', $2, now(), $3, $4) RETURNING id`
)

// TestPayments_SourceCheck pins D2's origin: a manual payment has a user and
// no bank line; an imported one has both; a source outside the three is
// refused. (The Vipps branch is 00042's.)
func TestPayments_SourceCheck(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	invoice := f.issued(7)
	line := f.mustBankLine(f.bankFile(), strings.Repeat("a", 64))
	user := "6f1c7a64-9c4f-4c43-9a51-2a8d4a1f0c11"

	for _, c := range []struct {
		name   string
		source string
		user   *string
		line   *int64
		check  string
	}{
		{"a manual payment with a bank line", "manual", &user, &line, "ck_payments_origin"},
		{"a manual payment without a user", "manual", nil, nil, "ck_payments_origin"},
		{"an OCR payment without a bank line", "ocr", &user, nil, "ck_payments_origin"},
		{"a camt.054 payment without a bank line", "camt054", &user, nil, "ck_payments_origin"},
		{"an OCR payment without a user", "ocr", nil, &line, "ck_payments_origin"},
		{"a Vipps payment (refused by both CHECKs; origin is checked first)", "vipps", &user, nil, "ck_payments_origin"},
	} {
		if _, err := f.insert(receivablesPaymentInsert, invoice, c.user, c.source, c.line); !checkViolationOf(err, c.check) {
			t.Errorf("%s: %v, want %s", c.name, err, c.check)
		}
	}
	for _, c := range []struct {
		name   string
		source string
		line   *int64
	}{
		{"a manual payment", "manual", nil},
		{"an OCR payment", "ocr", &line},
		{"a camt.054 payment", "camt054", &line},
	} {
		if _, err := f.insert(receivablesPaymentInsert, invoice, user, c.source, c.line); err != nil {
			t.Errorf("%s: %v, want it allowed", c.name, err)
		}
	}
	// A line may pay several invoices (D5): bank_transaction_id is not unique.
	if n := f.text(`SELECT count(*)::text FROM invoices.payments WHERE bank_transaction_id = $1`, line); n != "2" {
		t.Errorf("payments of the one line = %s, want 2", n)
	}
}

// TestPayments_ExistingRowsAreManual migrates a 1B payment planted at
// version 40 to 41: it reads manual, with no bank line and its user kept.
func TestPayments_ExistingRowsAreManual(t *testing.T) {
	t.Parallel()
	url := testdb.URL(t)
	migrateTo(t, url, receivablesVersion-1)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	f := &receivablesFixture{t: t, ctx: ctx, pool: pool, next: 1}
	invoice := f.issued(7)
	user := "6f1c7a64-9c4f-4c43-9a51-2a8d4a1f0c11"
	payment := f.mustInsert("plant a 1B payment", `
		INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, registered_by_user_id, registered_at)
		VALUES ($1, DATE '2026-10-10', 100, 'NOK', $2, now()) RETURNING id`, invoice, user)

	migrateTo(t, url, receivablesVersion)
	got := f.text(`SELECT source || ':' || coalesce(bank_transaction_id::text, 'none') || ':' || registered_by_user_id::text
		FROM invoices.payments WHERE id = $1`, payment)
	if want := "manual:none:" + user; got != want {
		t.Errorf("the 1B payment at 41 = %s, want %s", got, want)
	}
}

// TestPayments_NewColumnsFrozen: tr_payments_immutable's jsonb comparison
// covers the new columns without a change to it (R4 §5.1).
func TestPayments_NewColumnsFrozen(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	invoice := f.issued(7)
	file := f.bankFile()
	line, other := f.mustBankLine(file, strings.Repeat("a", 64)), f.mustBankLine(file, strings.Repeat("b", 64))
	user := "6f1c7a64-9c4f-4c43-9a51-2a8d4a1f0c11"
	manual := f.mustInsert("a manual payment", receivablesPaymentInsert, invoice, user, "manual", nil)
	imported := f.mustInsert("an OCR payment", receivablesPaymentInsert, invoice, user, "ocr", line)

	for _, c := range []struct {
		name, sql string
		id        int64
	}{
		{"a manual payment's source", `UPDATE invoices.payments SET source = 'ocr', bank_transaction_id = $2 WHERE id = $1`, manual},
		{"an imported payment's line", `UPDATE invoices.payments SET bank_transaction_id = $2 WHERE id = $1`, imported},
		{"an imported payment's source", `UPDATE invoices.payments SET source = 'camt054' WHERE id = $1 AND $2::bigint IS NOT NULL`, imported},
	} {
		if err := f.exec(c.sql, c.id, other); !refusedWith(err, receivablesPaymentImmutable) {
			t.Errorf("%s changed: %v, want %q", c.name, err, receivablesPaymentImmutable)
		}
	}
	// The removal still passes on an imported payment (D2).
	if err := f.exec(`UPDATE invoices.payments SET removed_at = now(), removed_by_user_id = gen_random_uuid(),
		removal_reason = 'Refunded' WHERE id = $1`, imported); err != nil {
		t.Errorf("removing an imported payment: %v, want it allowed", err)
	}
}

const (
	receivablesAccountImmutable = "invoices: a bank import account changes only its format"
	receivablesFileImmutable    = "invoices: a bank file is immutable"
	receivablesLineImmutable    = "invoices: a bank transaction is immutable"
	receivablesLinePending      = "invoices: a bank transaction never returns to pending"
	receivablesLineReason       = "invoices: a bank transaction keeps its reason"
	receivablesLineMatched      = "invoices: a bank transaction is matched only from pending"
	receivablesEventImmutable   = "invoices: a bank transaction event is immutable"
)

// TestBankFile_Immutable pins D3's triggers: an account changes only through
// the format PUT; a file is never deleted and takes one write, its duplicates
// count set once from NULL; a line is never deleted, changes only its state
// columns, never returns to pending, keeps its reason and its duplicate link;
// an event is never deleted and changes only its note, blanked.
func TestBankFile_Immutable(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)

	// The account.
	f.mustExec("an account", `INSERT INTO invoices.bank_import_accounts (account, format, set_by_user_id, set_at)
		VALUES ('15032080119', 'ocr', gen_random_uuid(), now())`)
	if err := f.exec(`UPDATE invoices.bank_import_accounts SET format = 'camt054', previous_format = 'ocr',
		cutover_through = DATE '2026-10-02', set_by_user_id = gen_random_uuid(), set_at = now()`); err != nil {
		t.Errorf("the format PUT's write: %v, want it allowed", err)
	}
	if err := f.exec(`UPDATE invoices.bank_import_accounts SET account = '15032080120'`); !refusedWith(err, receivablesAccountImmutable) {
		t.Errorf("an account renumbered: %v, want %q", err, receivablesAccountImmutable)
	}
	if err := f.exec(`DELETE FROM invoices.bank_import_accounts`); !refusedWith(err, receivablesAccountImmutable) {
		t.Errorf("an account deleted: %v, want %q", err, receivablesAccountImmutable)
	}
	if err := f.exec(`INSERT INTO invoices.bank_import_accounts (account, format, cutover_through, set_by_user_id, set_at)
		VALUES ('15032080121', 'ocr', DATE '2026-10-02', gen_random_uuid(), now())`); !checkViolationOf(err, "ck_bank_import_accounts_cutover") {
		t.Errorf("a cutover without a previous format: %v, want ck_bank_import_accounts_cutover", err)
	}
	if err := f.exec(`INSERT INTO invoices.bank_import_accounts (account, format, set_by_user_id, set_at)
		VALUES ('15032080122', 'csv', gen_random_uuid(), now())`); !checkViolationOf(err, "ck_bank_import_accounts_format") {
		t.Errorf("an unknown format: %v, want ck_bank_import_accounts_format", err)
	}

	// The file.
	file := f.bankFile()
	if err := f.exec(`UPDATE invoices.bank_files SET object_key = 'elsewhere' WHERE id = $1`, file); !refusedWith(err, receivablesFileImmutable) {
		t.Errorf("a file's key changed: %v, want %q", err, receivablesFileImmutable)
	}
	if err := f.exec(`UPDATE invoices.bank_files SET duplicates = 1, transactions = 3 WHERE id = $1`, file); !refusedWith(err, receivablesFileImmutable) {
		t.Errorf("duplicates set with another column: %v, want %q", err, receivablesFileImmutable)
	}
	if err := f.exec(`UPDATE invoices.bank_files SET duplicates = 1 WHERE id = $1`, file); err != nil {
		t.Errorf("duplicates set from NULL: %v, want it allowed", err)
	}
	if err := f.exec(`UPDATE invoices.bank_files SET duplicates = 2 WHERE id = $1`, file); !refusedWith(err, receivablesFileImmutable) {
		t.Errorf("duplicates set a second time: %v, want %q", err, receivablesFileImmutable)
	}
	if err := f.exec(`UPDATE invoices.bank_files SET duplicates = NULL WHERE id = $1`, file); !refusedWith(err, receivablesFileImmutable) {
		t.Errorf("duplicates cleared: %v, want %q", err, receivablesFileImmutable)
	}
	if err := f.exec(`DELETE FROM invoices.bank_files WHERE id = $1`, file); !refusedWith(err, receivablesFileImmutable) {
		t.Errorf("a file deleted: %v, want %q", err, receivablesFileImmutable)
	}
	fresh := f.bankFile()
	if err := f.exec(`UPDATE invoices.bank_files SET duplicates = -1 WHERE id = $1`, fresh); !checkViolationOf(err, "ck_bank_files_counts") {
		t.Errorf("duplicates of -1: %v, want ck_bank_files_counts", err)
	}
	for _, c := range []struct{ name, transactions, ignored string }{
		{"transactions of -1", "-1", "0"},
		{"ignored of -1", "0", "-1"},
	} {
		if _, err := f.insert(`
			INSERT INTO invoices.bank_files (format, sha256, file_identity, object_key, byte_size, accounts, transactions,
			    ignored, ignored_kinds, uploaded_by_user_id, uploaded_at)
			VALUES ('ocr', repeat('9', 64), 'neg', 'k', 1, ARRAY['15032080119'], $1::integer, $2::integer, '{}', gen_random_uuid(), now())
			RETURNING id`, c.transactions, c.ignored); !checkViolationOf(err, "ck_bank_files_counts") {
			t.Errorf("%s: %v, want ck_bank_files_counts", c.name, err)
		}
	}
	if _, err := f.insert(`
		INSERT INTO invoices.bank_files (format, sha256, file_identity, object_key, byte_size, accounts, transactions,
		    ignored, ignored_kinds, uploaded_by_user_id, uploaded_at)
		VALUES ('ocr', $1, 'x', 'k', 1, ARRAY['15032080119'], 0, 0, '[]', gen_random_uuid(), now()) RETURNING id`,
		strings.Repeat("f", 64)); !checkViolationOf(err, "ck_bank_files_ignored_kinds") {
		t.Errorf("ignored_kinds an array: %v, want ck_bank_files_ignored_kinds", err)
	}
	if _, err := f.insert(`
		INSERT INTO invoices.bank_files (format, sha256, file_identity, object_key, byte_size, accounts, transactions,
		    ignored, ignored_kinds, uploaded_by_user_id, uploaded_at)
		SELECT format, sha256, 'another', object_key, byte_size, accounts, transactions, ignored, ignored_kinds,
		    uploaded_by_user_id, uploaded_at FROM invoices.bank_files WHERE id = $1 RETURNING id`, file); !uniqueViolationOf(err, "uq_bank_files_sha256") {
		t.Errorf("the same bytes twice: %v, want uq_bank_files_sha256", err)
	}
	if _, err := f.insert(`
		INSERT INTO invoices.bank_files (format, sha256, file_identity, object_key, byte_size, accounts, transactions,
		    ignored, ignored_kinds, uploaded_by_user_id, uploaded_at)
		SELECT format, $2, file_identity, object_key, byte_size, accounts, transactions, ignored, ignored_kinds,
		    uploaded_by_user_id, uploaded_at FROM invoices.bank_files WHERE id = $1 RETURNING id`, file, strings.Repeat("e", 64)); !uniqueViolationOf(err, "uq_bank_files_identity") {
		t.Errorf("the same identity twice: %v, want uq_bank_files_identity", err)
	}

	// The lines.
	line := f.mustBankLine(file, strings.Repeat("a", 64))
	dup, err := f.bankLine(file, strings.Repeat("a", 64), &line, "duplicate", nil)
	if err != nil {
		t.Fatalf("a duplicate line: %v", err)
	}
	for _, c := range []struct {
		name, sql, want string
		id              int64
	}{
		{"a line's amount", `UPDATE invoices.bank_transactions SET amount = 1 WHERE id = $1`, receivablesLineImmutable, line},
		{"a line's KID", `UPDATE invoices.bank_transactions SET kid = NULL WHERE id = $1`, receivablesLineImmutable, line},
		{"a duplicate's link", `UPDATE invoices.bank_transactions SET duplicate_of_id = NULL WHERE id = $1`, receivablesLineImmutable, dup},
		{"a line deleted", `DELETE FROM invoices.bank_transactions WHERE id = $1`, receivablesLineImmutable, line},
	} {
		if err := f.exec(c.sql, c.id); !refusedWith(err, c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	if err := f.exec(`UPDATE invoices.bank_transactions SET status = 'exception', reason = 'kid_unknown',
		suggested_invoice_id = 1001 WHERE id = $1`, line); err != nil {
		t.Errorf("a line queued: %v, want it allowed", err)
	}
	if err := f.exec(`UPDATE invoices.bank_transactions SET status = 'pending' WHERE id = $1`, line); !refusedWith(err, receivablesLinePending) {
		t.Errorf("a line back to pending: %v, want %q", err, receivablesLinePending)
	}
	// Matched only from pending (D4): the queue resolves an exception, it
	// never matches it (D5).
	if err := f.exec(`UPDATE invoices.bank_transactions SET status = 'matched' WHERE id = $1`, line); !refusedWith(err, receivablesLineMatched) {
		t.Errorf("an exception matched: %v, want %q", err, receivablesLineMatched)
	}
	matched := f.mustBankLine(file, strings.Repeat("c", 64))
	if err := f.exec(`UPDATE invoices.bank_transactions SET status = 'matched' WHERE id = $1`, matched); err != nil {
		t.Errorf("a pending line matched: %v, want it allowed", err)
	}
	if err := f.exec(`UPDATE invoices.bank_transactions SET resolution_note = '' WHERE id = $1`, matched); err != nil {
		t.Errorf("a matched line's state written again: %v, want it allowed", err)
	}
	if err := f.exec(`UPDATE invoices.bank_transactions SET status = 'resolved', resolution = 'not_customer_payment',
		resolved_by_user_id = gen_random_uuid(), resolved_at = now(), resolution_note = 'Not ours' WHERE id = $1`, line); err != nil {
		t.Errorf("a line dismissed: %v, want it allowed", err)
	}
	if err := f.exec(`UPDATE invoices.bank_transactions SET reason = NULL WHERE id = $1`, line); !refusedWith(err, receivablesLineReason) {
		t.Errorf("a reason cleared: %v, want %q", err, receivablesLineReason)
	}
	if err := f.exec(`UPDATE invoices.bank_transactions SET resolution_note = '' WHERE id = $1`, line); err != nil {
		t.Errorf("a resolution note blanked: %v, want it allowed", err)
	}
	if err := f.exec(`UPDATE invoices.bank_transactions SET status = 'matched' WHERE id = $1`, dup); !refusedWith(err, receivablesLineMatched) {
		t.Errorf("a duplicate matched: %v, want %q", err, receivablesLineMatched)
	}
	if err := f.exec(`UPDATE invoices.bank_transactions SET status = 'resolved', reason = 'possible_duplicate',
		resolution = 'duplicate_confirmed', resolved_at = now() WHERE id = $1`, dup); err != nil {
		t.Errorf("a duplicate confirmed: %v, want it allowed", err)
	}
	if err := f.exec(`UPDATE invoices.bank_transactions SET status = 'matched' WHERE id = $1`, line); !refusedWith(err, receivablesLineMatched) {
		t.Errorf("a resolved line matched: %v, want %q", err, receivablesLineMatched)
	}

	// The events.
	event := f.mustInsert("an event", `
		INSERT INTO invoices.bank_transaction_events (bank_transaction_id, event, reason, note, by_user_id, at)
		VALUES ($1, 'dismissed', 'kid_unknown', 'Not ours', gen_random_uuid(), now()) RETURNING id`, line)
	for _, c := range []struct{ name, sql string }{
		{"an event's note rewritten", `UPDATE invoices.bank_transaction_events SET note = 'Other' WHERE id = $1`},
		{"an event's kind changed", `UPDATE invoices.bank_transaction_events SET event = 'applied' WHERE id = $1`},
		{"an event's note blanked with its reason", `UPDATE invoices.bank_transaction_events SET note = '', reason = NULL WHERE id = $1`},
		{"an event deleted", `DELETE FROM invoices.bank_transaction_events WHERE id = $1`},
	} {
		if err := f.exec(c.sql, event); !refusedWith(err, receivablesEventImmutable) {
			t.Errorf("%s: %v, want %q", c.name, err, receivablesEventImmutable)
		}
	}
	if err := f.exec(`UPDATE invoices.bank_transaction_events SET note = '' WHERE id = $1`, event); err != nil {
		t.Errorf("an event's note blanked: %v, want it allowed (the erase, B1)", err)
	}
	if _, err := f.insert(`
		INSERT INTO invoices.bank_transaction_events (bank_transaction_id, event, by_user_id, at)
		VALUES ($1, 'forgotten', gen_random_uuid(), now()) RETURNING id`, line); !checkViolationOf(err, "ck_bank_transaction_events_event") {
		t.Errorf("an unknown event: %v, want ck_bank_transaction_events_event", err)
	}
	if _, err := f.insert(`
		INSERT INTO invoices.bank_transaction_events (bank_transaction_id, event, reason, by_user_id, at)
		VALUES ($1, 'queued', 'kid_lost', gen_random_uuid(), now()) RETURNING id`, line); !checkViolationOf(err, "ck_bank_transaction_events_reason") {
		t.Errorf("an event with an unknown reason: %v, want ck_bank_transaction_events_reason", err)
	}
}

// TestBankTransactions_TheStateCheck pins ck_bank_transactions_state's
// implications and the line's sets: an exception has a reason; resolved is
// exactly a resolution with its time; a duplicate has its link, which
// outlives the status but never makes a line pending or matched; the reason,
// the format and the ordinal.
func TestBankTransactions_TheStateCheck(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	file := f.bankFile()
	original := f.mustBankLine(file, strings.Repeat("a", 64))

	for _, c := range []struct {
		name   string
		status string
		reason *string
		dupOf  *int64
		check  string
	}{
		{"an exception without a reason", "exception", nil, nil, "ck_bank_transactions_state"},
		{"a duplicate without its link", "duplicate", nil, nil, "ck_bank_transactions_state"},
		{"a resolved line without a resolution", "resolved", ptrTo("kid_unknown"), nil, "ck_bank_transactions_state"},
		{"an unknown reason", "exception", ptrTo("kid_lost"), nil, "ck_bank_transactions_reason"},
		{"an unknown status", "parked", nil, nil, "ck_bank_transactions_status"},
		{"a linked line pending", "pending", nil, &original, "ck_bank_transactions_state"},
		{"a linked line matched", "matched", nil, &original, "ck_bank_transactions_state"},
	} {
		if _, err := f.bankLine(file, strings.Repeat("c", 64), c.dupOf, c.status, c.reason); !checkViolationOf(err, c.check) {
			t.Errorf("%s: %v, want %s", c.name, err, c.check)
		}
	}
	for i, reason := range []string{"kid_invalid", "kid_unknown", "invoice_credited", "invoice_settled", "exceeds_open",
		"no_kid", "negative_amount", "reversal", "vipps_payout", "paid_before_issue", "account_mismatch",
		"possible_duplicate", "payment_removed"} {
		if _, err := f.bankLine(file, fmt.Sprintf("%064d", i+1), nil, "exception", &reason); err != nil {
			t.Errorf("an exception for %s: %v, want it allowed", reason, err)
		}
	}

	for _, c := range []struct{ name, format, ordinal, check string }{
		{"an unknown format", "csv", "1", "ck_bank_transactions_format"},
		{"an ordinal of 0", "ocr", "0", "ck_bank_transactions_ordinal"},
	} {
		if err := f.exec(`
			INSERT INTO invoices.bank_transactions (bank_file_id, line_ref, format, account, direction, booked_on,
			    amount, currency, fingerprint, ordinal)
			VALUES ($1, '9/9', $2, '15032080119', 'credit', DATE '2026-10-01', 1, 'NOK', repeat('e', 64), $3::smallint)`,
			file, c.format, c.ordinal); !checkViolationOf(err, c.check) {
			t.Errorf("%s: %v, want %s", c.name, err, c.check)
		}
	}

	queued := f.mustBankLine(file, strings.Repeat("d", 64))
	if err := f.exec(`UPDATE invoices.bank_transactions SET status = 'exception', reason = 'no_kid',
		resolution = 'applied', resolved_at = now() WHERE id = $1`, queued); !checkViolationOf(err, "ck_bank_transactions_state") {
		t.Errorf("a resolution on an open line: %v, want ck_bank_transactions_state", err)
	}
	if err := f.exec(`UPDATE invoices.bank_transactions SET status = 'resolved', resolution = 'applied' WHERE id = $1`, queued); !checkViolationOf(err, "ck_bank_transactions_state") {
		t.Errorf("resolved without its time: %v, want ck_bank_transactions_state", err)
	}
	if err := f.exec(`UPDATE invoices.bank_transactions SET status = 'resolved', resolution = 'returned', resolved_at = now() WHERE id = $1`, queued); !checkViolationOf(err, "ck_bank_transactions_resolution") {
		t.Errorf("an unknown resolution: %v, want ck_bank_transactions_resolution", err)
	}

	// Treated as distinct (D5): a duplicate becomes an exception and keeps
	// its link, and so stays out of the fingerprint index.
	dup, err := f.bankLine(file, strings.Repeat("a", 64), &original, "duplicate", nil)
	if err != nil {
		t.Fatalf("a duplicate line: %v", err)
	}
	if err := f.exec(`UPDATE invoices.bank_transactions SET status = 'exception', reason = 'possible_duplicate' WHERE id = $1`, dup); err != nil {
		t.Errorf("a duplicate treated as distinct: %v, want it allowed", err)
	}
	if got := f.text(`SELECT status || ':' || duplicate_of_id::text FROM invoices.bank_transactions WHERE id = $1`, dup); got != fmt.Sprintf("exception:%d", original) {
		t.Errorf("the duplicate treated as distinct = %s, want exception and its link kept", got)
	}
}

// TestBankTransactions_TheFingerprintIndexSkipsDuplicates: two live lines of
// one account and fingerprint are refused; a duplicate row beside its live
// twin is not, and neither is the same fingerprint on another account.
func TestBankTransactions_TheFingerprintIndexSkipsDuplicates(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	file := f.bankFile()
	fp := strings.Repeat("a", 64)
	live := f.mustBankLine(file, fp)
	if _, err := f.bankLine(file, fp, nil, "pending", nil); !uniqueViolationOf(err, "ux_bank_transactions_fingerprint") {
		t.Errorf("a second live line: %v, want ux_bank_transactions_fingerprint", err)
	}
	if _, err := f.bankLine(file, fp, &live, "duplicate", nil); err != nil {
		t.Errorf("a duplicate row beside its twin: %v, want it allowed", err)
	}
	if _, err := f.bankLine(file, fp, &live, "duplicate", nil); err != nil {
		t.Errorf("a second duplicate row: %v, want it allowed", err)
	}
	if err := f.exec(`
		INSERT INTO invoices.bank_transactions (bank_file_id, line_ref, format, account, direction, booked_on,
		    amount, currency, fingerprint, ordinal)
		VALUES ($1, '1/2', 'ocr', '15032080120', 'credit', DATE '2026-10-01', 1250, 'NOK', $2, 1)`, file, fp); err != nil {
		t.Errorf("the fingerprint on another account: %v, want it allowed", err)
	}
}

// receivablesSeeds is R4 §2.11's tables, the regulation of each row read on
// Lovdata: kind, valid_from, value, source_ref.
var receivablesSeeds = []string{
	"b2b_compensation_nok:2024-01-01:470.00:FOR-2023-12-14-2043",
	"b2b_compensation_nok:2024-07-01:460.00:FOR-2024-06-26-1320",
	"b2b_compensation_nok:2025-01-01:470.00:FOR-2024-12-19-3279",
	"b2b_compensation_nok:2025-07-01:460.00:FOR-2025-06-23-1321",
	"b2b_compensation_nok:2026-01-01:460.00:FOR-2025-12-18-2658",
	"b2b_compensation_nok:2026-07-01:430.00:FOR-2026-06-25-1372",
	"inkassosats:2019-01-01:700.00:FOR-2018-12-20-2050",
	"inkassosats:2026-01-01:750.00:FOR-2025-12-19-2709",
	"late_interest_percent:2024-01-01:12.50:FOR-2023-12-14-2043",
	"late_interest_percent:2024-07-01:12.50:FOR-2024-06-26-1320",
	"late_interest_percent:2025-01-01:12.50:FOR-2024-12-19-3279",
	"late_interest_percent:2025-07-01:12.25:FOR-2025-06-23-1321",
	"late_interest_percent:2026-01-01:12.00:FOR-2025-12-18-2658",
	"late_interest_percent:2026-07-01:12.25:FOR-2026-06-25-1372",
}

// collectionRates is every rate row as kind:valid_from:value:source_ref,
// each seeded one (no user, no release value) marked so.
func collectionRates(t *testing.T, f *receivablesFixture) []string {
	t.Helper()
	rows, err := f.pool.Query(f.ctx, `
		SELECT kind || ':' || valid_from::text || ':' || value::text || ':' || source_ref
		    || CASE WHEN created_by_user_id IS NULL AND release_value IS NULL THEN '' ELSE ':user or release' END
		FROM invoices.collection_rates ORDER BY kind, valid_from`)
	if err != nil {
		t.Fatalf("read the rates: %v", err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read the rates: %v", err)
	}
	return got
}

// TestCollectionRates_Seeds pins every seeded row against R4 §2.11 (D6):
// kind, date, value and regulation, seeded by no user.
func TestCollectionRates_Seeds(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	if got := collectionRates(t, f); !equalStrings(got, receivablesSeeds) {
		t.Errorf("the seeded rates =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(receivablesSeeds, "\n"))
	}
}

const (
	receivablesRateAppendOnly = "invoices: a collection rate is append-only"
	receivablesRateSeeded     = "invoices: a seeded collection rate is never deleted"
	receivablesRateInsert     = `
		INSERT INTO invoices.collection_rates (kind, valid_from, value, source_ref, created_by_user_id, created_at)
		VALUES ($1, $2::date, $3, 'FOR-2027-01-01-1', gen_random_uuid(), now()) RETURNING id`
)

// TestCollectionRates_AppendOnly: a rate is never updated but for its release
// value, set once from NULL; a seeded row is never deleted, a user's may be;
// the half-yearly kinds start on 1 January or 1 July.
func TestCollectionRates_AppendOnly(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	seeded := f.mustInsert("a seeded row's id", `SELECT id FROM invoices.collection_rates
		WHERE kind = 'late_interest_percent' AND valid_from = DATE '2026-07-01'`)
	for _, c := range []struct{ name, sql string }{
		{"a value changed", `UPDATE invoices.collection_rates SET value = 13 WHERE id = $1`},
		{"a regulation changed", `UPDATE invoices.collection_rates SET source_ref = 'FOR-X' WHERE id = $1`},
		{"a release value set with the value", `UPDATE invoices.collection_rates SET release_value = 13, release_source_ref = 'FOR-R', value = 13 WHERE id = $1`},
		{"a seeded row deleted", `DELETE FROM invoices.collection_rates WHERE id = $1`},
	} {
		want := receivablesRateAppendOnly
		if strings.HasPrefix(c.sql, "DELETE") {
			want = receivablesRateSeeded
		}
		if err := f.exec(c.sql, seeded); !refusedWith(err, want) {
			t.Errorf("%s: %v, want %q", c.name, err, want)
		}
	}
	if err := f.exec(`UPDATE invoices.collection_rates SET release_value = 12.50 WHERE id = $1`, seeded); !checkViolationOf(err, "ck_collection_rates_release") {
		t.Errorf("a release value without its regulation: %v, want ck_collection_rates_release", err)
	}
	if err := f.exec(`UPDATE invoices.collection_rates SET release_value = 0, release_source_ref = 'FOR-R' WHERE id = $1`, seeded); !checkViolationOf(err, "ck_collection_rates_release") {
		t.Errorf("a release value of 0: %v, want ck_collection_rates_release", err)
	}
	if err := f.exec(`UPDATE invoices.collection_rates SET release_value = 12.50, release_source_ref = 'FOR-R' WHERE id = $1`, seeded); err != nil {
		t.Errorf("a release value set from NULL: %v, want it allowed", err)
	}
	if err := f.exec(`UPDATE invoices.collection_rates SET release_value = 12.75 WHERE id = $1`, seeded); !refusedWith(err, receivablesRateAppendOnly) {
		t.Errorf("a release value set twice: %v, want %q", err, receivablesRateAppendOnly)
	}

	user := f.mustInsert("a user's rate", receivablesRateInsert, "late_interest_percent", "2027-01-01", 12.5)
	if err := f.exec(`DELETE FROM invoices.collection_rates WHERE id = $1`, user); err != nil {
		t.Errorf("a user's row deleted: %v, want it allowed", err)
	}

	for _, c := range []struct {
		kind, from string
		check      string
	}{
		{"late_interest_percent", "2027-02-01", "ck_collection_rates_half_year"},
		{"late_interest_percent", "2027-01-02", "ck_collection_rates_half_year"},
		{"b2b_compensation_nok", "2027-07-15", "ck_collection_rates_half_year"},
		{"court_fee", "2027-01-01", "ck_collection_rates_kind"},
	} {
		if _, err := f.insert(receivablesRateInsert, c.kind, c.from, 100); !checkViolationOf(err, c.check) {
			t.Errorf("%s from %s: %v, want %s", c.kind, c.from, err, c.check)
		}
	}
	if _, err := f.insert(receivablesRateInsert, "late_interest_percent", "2027-07-01", 0); !checkViolationOf(err, "ck_collection_rates_value") {
		t.Errorf("a rate of 0: %v, want ck_collection_rates_value", err)
	}
	for _, c := range []struct{ kind, from string }{
		{"b2b_compensation_nok", "2027-07-01"},
		{"inkassosats", "2027-03-15"},
	} {
		if _, err := f.insert(receivablesRateInsert, c.kind, c.from, 500); err != nil {
			t.Errorf("%s from %s: %v, want it allowed", c.kind, c.from, err)
		}
	}
	if _, err := f.insert(receivablesRateInsert, "inkassosats", "2027-03-15", 800); !uniqueViolationOf(err, "uq_collection_rates_kind_valid_from") {
		t.Errorf("a second row of one kind and day: %v, want uq_collection_rates_kind_valid_from", err)
	}
}

// TestCollectionRates_ReleaseSeedOverUserRow runs the statement a later
// release's migration runs (plan reading 4, D6) over a row a user added
// first: it never fails; the release's value and regulation are kept beside
// the user's, once, equal or not (the warning compares them); a day nobody
// had is seeded, and seeding it again changes nothing.
func TestCollectionRates_ReleaseSeedOverUserRow(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	seed := func(kind, from string, value float64) {
		t.Helper()
		if err := f.exec(`SELECT invoices.seed_collection_rate($1, $2::date, $3, 'FOR-2026-12-20-9999')`, kind, from, value); err != nil {
			t.Fatalf("seed %s from %s at %v: %v", kind, from, value, err)
		}
	}
	read := func(kind, from string) string {
		t.Helper()
		return f.text(`SELECT value::text || ':' || coalesce(release_value::text, 'none') || ':' || source_ref || ':'
			|| coalesce(release_source_ref, 'none') || ':' || CASE WHEN created_by_user_id IS NULL THEN 'seeded' ELSE 'user' END
			FROM invoices.collection_rates WHERE kind = $1 AND valid_from = $2::date`, kind, from)
	}

	f.mustInsert("a user's interest rate", receivablesRateInsert, "late_interest_percent", "2027-01-01", 12.75)
	f.mustInsert("a user's compensation", receivablesRateInsert, "b2b_compensation_nok", "2027-01-01", 440)

	seed("late_interest_percent", "2027-01-01", 13)
	if got, want := read("late_interest_percent", "2027-01-01"), "12.75:13.00:FOR-2027-01-01-1:FOR-2026-12-20-9999:user"; got != want {
		t.Errorf("a release seed of another value = %s, want %s", got, want)
	}
	seed("b2b_compensation_nok", "2027-01-01", 440)
	if got, want := read("b2b_compensation_nok", "2027-01-01"), "440.00:440.00:FOR-2027-01-01-1:FOR-2026-12-20-9999:user"; got != want {
		t.Errorf("a release seed of the same value = %s, want %s", got, want)
	}
	seed("late_interest_percent", "2027-01-01", 13.25)
	if got, want := read("late_interest_percent", "2027-01-01"), "12.75:13.00:FOR-2027-01-01-1:FOR-2026-12-20-9999:user"; got != want {
		t.Errorf("a second release seed = %s, want %s: the first release value is kept", got, want)
	}
	seed("inkassosats", "2027-01-01", 800)
	if got, want := read("inkassosats", "2027-01-01"), "800.00:none:FOR-2026-12-20-9999:none:seeded"; got != want {
		t.Errorf("a release seed of a new day = %s, want %s", got, want)
	}
	seed("inkassosats", "2027-01-01", 800)
	if got, want := read("inkassosats", "2027-01-01"), "800.00:none:FOR-2026-12-20-9999:none:seeded"; got != want {
		t.Errorf("the same seed run twice = %s, want %s", got, want)
	}
}

// TestReminderSettings_TheRow pins D7's one row: its defaults, the review
// seeded to 2026-12-31, each bound at its edges, the one row and never
// deleted.
func TestReminderSettings_TheRow(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	got := f.text(`SELECT concat_ws(':', id, enabled, first_reminder_days, deadline_days, grace_days, reminders_before_notice,
		collection_notice, person_charge, business_charge, late_interest, stale_import_days,
		coalesce(inkassolov_2026_from::text, 'none'), regime_reviewed_through, coalesce(regime_reviewed_by_user_id::text, 'none'),
		regime_reviewed_at IS NOT NULL, revision, coalesce(updated_by_user_id::text, 'none'), updated_at IS NOT NULL)
		FROM invoices.reminder_settings`)
	if want := "1:f:14:14:3:1:t:fee:fee:f:3:none:2026-12-31:none:t:1:none:t"; got != want {
		t.Errorf("the settings row = %s, want %s", got, want)
	}

	for _, c := range []struct {
		column string
		lo, hi int
	}{
		{"first_reminder_days", 1, 60},
		{"deadline_days", 14, 60},
		{"grace_days", 1, 10},
		{"reminders_before_notice", 0, 2},
		{"stale_import_days", 1, 30},
	} {
		set := fmt.Sprintf(`UPDATE invoices.reminder_settings SET %s = $1`, c.column)
		check := "ck_reminder_settings_" + c.column
		for _, v := range []int{c.lo - 1, c.hi + 1} {
			if err := f.exec(set, v); !checkViolationOf(err, check) {
				t.Errorf("%s = %d: %v, want %s", c.column, v, err, check)
			}
		}
		for _, v := range []int{c.lo, c.hi} {
			if err := f.exec(set, v); err != nil {
				t.Errorf("%s = %d: %v, want it allowed", c.column, v, err)
			}
		}
	}
	for _, c := range []struct {
		column, value string
		ok            bool
	}{
		{"person_charge", "none", true},
		{"person_charge", "compensation", false},
		{"business_charge", "compensation", true},
		{"business_charge", "none", true},
		{"business_charge", "both", false},
	} {
		err := f.exec(fmt.Sprintf(`UPDATE invoices.reminder_settings SET %s = $1`, c.column), c.value)
		if c.ok && err != nil {
			t.Errorf("%s = %s: %v, want it allowed", c.column, c.value, err)
		}
		if !c.ok && !checkViolationOf(err, "ck_reminder_settings_"+c.column) {
			t.Errorf("%s = %s: %v, want ck_reminder_settings_%s", c.column, c.value, err, c.column)
		}
	}
	if err := f.exec(`INSERT INTO invoices.reminder_settings (id, regime_reviewed_through, regime_reviewed_at, updated_at)
		VALUES (2, DATE '2026-12-31', now(), now())`); !checkViolationOf(err, "ck_reminder_settings_single_row") {
		t.Errorf("a second row: %v, want ck_reminder_settings_single_row", err)
	}
	if err := f.exec(`DELETE FROM invoices.reminder_settings`); !refusedWith(err, "invoices: a row of reminder_settings is never deleted") {
		t.Errorf("the row deleted: %v, want it refused", err)
	}
	if err := f.exec(`INSERT INTO invoices.customer_reminder_policies (customer_id, mode, updated_by_user_id, updated_at)
		VALUES (7, 'lenient', gen_random_uuid(), now())`); !checkViolationOf(err, "ck_customer_reminder_policies_mode") {
		t.Errorf("an unknown policy mode: %v, want ck_customer_reminder_policies_mode", err)
	}
}

const (
	receivablesReminderParent    = "invoices: a reminder needs an issued invoice"
	receivablesReminderDeleted   = "invoices: a reminder is never deleted"
	receivablesReminderIdentity  = "invoices: a reminder keeps its identity"
	receivablesReminderRecipient = "invoices: a reminder's recipient is only ever blanked"
	receivablesReminderMessageID = "invoices: a reminder's message id is set once, while queued"
	receivablesReminderFinal     = "invoices: a reminder is final once sent or withdrawn"
)

// TestReminders_Triggers pins D10's letter: inserted only under an issued
// invoice, its recipient blanked for an erased customer; never deleted; its
// identity frozen; its facts and attempts changed only in flight; once sent
// only its PDF set once; once withdrawn nothing — but the recipient blanked
// in every status (the erase, B1); its message id set once while queued;
// ck_reminders_state for each status; its status tied to its channel; and
// the charge its facts name, and only it.
func TestReminders_Triggers(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	run := f.run()
	invoice := f.issued(7)

	// The parent guard.
	if _, err := f.reminder(f.draft(7, 0), run, 1); !refusedWith(err, receivablesReminderParent) {
		t.Errorf("a letter on a draft: %v, want %q", err, receivablesReminderParent)
	}
	if _, err := f.reminder(f.issue(f.draft(7, invoice)), run, 1); !refusedWith(err, receivablesReminderParent) {
		t.Errorf("a letter on an issued credit note: %v, want %q", err, receivablesReminderParent)
	}
	erased := f.issued(8)
	f.erase(8)
	blank := f.mustReminder(erased, run, 1)
	if got := f.text(`SELECT recipient FROM invoices.reminders WHERE id = $1`, blank); got != "" {
		t.Errorf("an erased customer's letter's recipient = %q, want it blanked", got)
	}

	letter := f.mustReminder(invoice, run, 1)
	if _, err := f.reminder(invoice, run, 1); !uniqueViolationOf(err, "uq_reminders_invoice_sequence") {
		t.Errorf("a second letter of one sequence: %v, want uq_reminders_invoice_sequence", err)
	}
	for _, c := range []struct{ name, set string }{
		{"its invoice", "invoice_id = invoice_id + 1"},
		{"its run", "run_id = run_id + 1"},
		{"its sequence", "sequence = 2"},
		{"its level", "level = 'collection_notice'"},
		{"its channel", "channel = 'paper'"},
		{"its language", "language = 'en'"},
		{"its author", "created_by_user_id = gen_random_uuid()"},
		{"its creation", "created_at = created_at + interval '1 second'"},
	} {
		if err := f.exec(`UPDATE invoices.reminders SET `+c.set+` WHERE id = $1`, letter); !refusedWith(err, receivablesReminderIdentity) {
			t.Errorf("a letter's %s changed: %v, want %q", c.name, err, receivablesReminderIdentity)
		}
	}
	if err := f.exec(`UPDATE invoices.reminders SET recipient = 'other@example.no' WHERE id = $1`, letter); !refusedWith(err, receivablesReminderRecipient) {
		t.Errorf("a recipient rewritten: %v, want %q", err, receivablesReminderRecipient)
	}
	if err := f.exec(`DELETE FROM invoices.reminders WHERE id = $1`, letter); !refusedWith(err, receivablesReminderDeleted) {
		t.Errorf("a letter deleted: %v, want %q", err, receivablesReminderDeleted)
	}

	// In flight: the claim, the uncounted reschedule, the facts written and
	// cleared again, the message id once.
	for _, c := range []struct{ name, set string }{
		{"the claim", "lease_id = 'w1', lease_until = now(), attempts = 1, first_attempt_at = now(), message_id = 'reminder-1@vantigo.invalid'"},
		{"the reschedule", "held_reason = 'collection_rates_outdated', next_attempt_at = now(), lease_id = NULL, lease_until = NULL"},
		{"the facts written", receivablesFacts + ", held_reason = NULL"},
		{"the facts cleared", "sent_on = NULL, deadline = NULL, total = NULL, last_error = 'smtp: 421'"},
	} {
		if err := f.exec(`UPDATE invoices.reminders SET `+c.set+` WHERE id = $1`, letter); err != nil {
			t.Errorf("%s on a queued letter: %v, want it allowed", c.name, err)
		}
	}
	if err := f.exec(`UPDATE invoices.reminders SET message_id = 'reminder-2@vantigo.invalid' WHERE id = $1`, letter); !refusedWith(err, receivablesReminderMessageID) {
		t.Errorf("a message id changed: %v, want %q", err, receivablesReminderMessageID)
	}
	if err := f.exec(`UPDATE invoices.reminders SET message_id = NULL WHERE id = $1`, letter); !refusedWith(err, receivablesReminderMessageID) {
		t.Errorf("a message id cleared: %v, want %q", err, receivablesReminderMessageID)
	}
	if err := f.exec(`UPDATE invoices.reminders SET held_reason = 'rates_late' WHERE id = $1`, letter); !checkViolationOf(err, "ck_reminders_held_reason") {
		t.Errorf("an unknown held reason: %v, want ck_reminders_held_reason", err)
	}

	// ck_reminders_state, each status.
	paper := f.paperReminder(invoice, run, 3)
	for _, c := range []struct {
		name, set string
		id        int64
	}{
		{"sent without its facts", "status = 'sent', sent_at = now()", letter},
		{"sent without sent_at", "status = 'sent', " + receivablesFacts, letter},
		{"printed without its batch", "status = 'printed', " + receivablesFacts, paper},
		{"withdrawn without a reason", "status = 'withdrawn', withdrawn_at = now()", letter},
		{"failed without its time", "status = 'failed'", letter},
	} {
		if err := f.exec(`UPDATE invoices.reminders SET `+c.set+` WHERE id = $1`, c.id); !checkViolationOf(err, "ck_reminders_state") {
			t.Errorf("%s: %v, want ck_reminders_state", c.name, err)
		}
	}
	batch := f.mustInsert("a print batch", `INSERT INTO invoices.reminder_print_batches (post_on, created_at, created_by_user_id)
		VALUES (DATE '2026-11-03', now(), gen_random_uuid()) RETURNING id`)
	if err := f.exec(`UPDATE invoices.reminders SET message_id = 'reminder-4@vantigo.invalid' WHERE id = $1`, paper); !refusedWith(err, receivablesReminderMessageID) {
		t.Errorf("a message id set on a letter awaiting print: %v, want %q", err, receivablesReminderMessageID)
	}
	for _, c := range []struct{ name, set string }{
		{"printed", "status = 'printed', print_batch_id = $2, pdf_object_key = 'k1', pdf_sha256 = 'h1', " + receivablesFacts},
		{"reprinted", "status = 'awaiting_print', print_batch_id = NULL, pdf_object_key = NULL, pdf_sha256 = NULL, sent_on = NULL"},
	} {
		if err := f.exec(`UPDATE invoices.reminders SET `+c.set+` WHERE id = $1 AND $2::bigint IS NOT NULL`, paper, batch); err != nil {
			t.Errorf("a paper letter %s: %v, want it allowed", c.name, err)
		}
	}

	// ck_reminders_channel_status: paper is printed, e-mail dispatched.
	for _, c := range []struct {
		name, set string
		id        int64
	}{
		{"an e-mail awaiting print", "status = 'awaiting_print'", letter},
		{"an e-mail naming a batch", "print_batch_id = $2", letter},
		{"a paper letter queued", "status = 'queued'", paper},
		{"a paper letter failed", "status = 'failed', failed_at = now()", paper},
	} {
		if err := f.exec(`UPDATE invoices.reminders SET `+c.set+` WHERE id = $1 AND $2::bigint IS NOT NULL`, c.id, batch); !checkViolationOf(err, "ck_reminders_channel_status") {
			t.Errorf("%s: %v, want ck_reminders_channel_status", c.name, err)
		}
	}
	if _, err := f.insert(`
		INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, language, created_at, created_by_user_id, status)
		VALUES ($1, $2, 9, 'reminder', 'paper', 'nb', now(), gen_random_uuid(), 'queued') RETURNING id`, invoice, run); !checkViolationOf(err, "ck_reminders_channel_status") {
		t.Errorf("a paper letter inserted queued: %v, want ck_reminders_channel_status", err)
	}

	// ck_reminders_charges: the charge the facts name, and only it.
	for _, c := range []struct{ name, set string }{
		{"a reminder fee without its amount", receivablesFactsBase + ", fee_kind = 'reminder_fee', fee = NULL"},
		{"a reminder fee beside a compensation", receivablesFactsBase + ", fee_kind = 'reminder_fee', fee = 35, compensation = 460"},
		{"a compensation without its amount", receivablesFactsBase + ", fee_kind = 'compensation', fee = NULL, compensation = NULL"},
		{"a compensation carrying a fee", receivablesFactsBase + ", fee_kind = 'compensation', fee = 35, compensation = 460"},
		{"a fee-free letter carrying a fee", receivablesFactsBase + ", fee_kind = 'none', fee = 35"},
		{"a fee of 0", receivablesFactsBase + ", fee_kind = 'reminder_fee', fee = 0"},
		{"a compensation of 0", receivablesFactsBase + ", fee_kind = 'compensation', fee = NULL, compensation = 0"},
	} {
		if err := f.exec(`UPDATE invoices.reminders SET `+c.set+` WHERE id = $1`, letter); !checkViolationOf(err, "ck_reminders_charges") {
			t.Errorf("%s: %v, want ck_reminders_charges", c.name, err)
		}
	}
	for _, c := range []struct{ name, set string }{
		{"a compensation letter", receivablesFactsBase + ", fee_kind = 'compensation', fee = NULL, compensation = 460"},
		{"a fee-free letter", receivablesFactsBase + ", fee_kind = 'none', fee = NULL, compensation = NULL"},
	} {
		if err := f.exec(`UPDATE invoices.reminders SET `+c.set+` WHERE id = $1`, letter); err != nil {
			t.Errorf("%s: %v, want it allowed", c.name, err)
		}
	}

	// Sent: only the PDF, once, and the recipient blanked.
	f.mustExec("the letter sent", `UPDATE invoices.reminders SET status = 'sent', sent_at = now(), `+receivablesFacts+` WHERE id = $1`, letter)
	for _, c := range []struct{ name, set string }{
		{"a sent letter's total", "total = 1"},
		{"a sent letter's status", "status = 'failed', failed_at = now()"},
		{"a sent letter's held reason", "held_reason = 'collection_rates_outdated'"},
	} {
		if err := f.exec(`UPDATE invoices.reminders SET `+c.set+` WHERE id = $1`, letter); !refusedWith(err, receivablesReminderFinal) {
			t.Errorf("%s changed: %v, want %q", c.name, err, receivablesReminderFinal)
		}
	}
	if err := f.exec(`UPDATE invoices.reminders SET pdf_object_key = 'reminders/k', pdf_sha256 = 'h' WHERE id = $1`, letter); err != nil {
		t.Errorf("a sent letter's PDF stored: %v, want it allowed", err)
	}
	if err := f.exec(`UPDATE invoices.reminders SET pdf_object_key = 'reminders/k2' WHERE id = $1`, letter); !refusedWith(err, receivablesReminderFinal) {
		t.Errorf("a sent letter's PDF key changed: %v, want %q", err, receivablesReminderFinal)
	}
	if err := f.exec(`UPDATE invoices.reminders SET recipient = '' WHERE id = $1`, letter); err != nil {
		t.Errorf("a sent letter's recipient blanked: %v, want it allowed (the erase, B1)", err)
	}

	// Withdrawn: nothing but the recipient blanked.
	withdrawn := f.mustReminder(invoice, run, 4)
	f.mustExec("the letter withdrawn", `UPDATE invoices.reminders SET status = 'withdrawn', withdrawn_at = now(),
		withdrawal_reason = 'on_hold' WHERE id = $1`, withdrawn)
	for _, c := range []struct{ name, set string }{
		{"a withdrawn letter back to queued", "status = 'queued', withdrawn_at = NULL, withdrawal_reason = NULL"},
		{"a withdrawn letter's reason", "withdrawal_reason = 'settled'"},
		{"a withdrawn letter's facts", receivablesFacts},
	} {
		if err := f.exec(`UPDATE invoices.reminders SET `+c.set+` WHERE id = $1`, withdrawn); !refusedWith(err, receivablesReminderFinal) {
			t.Errorf("%s: %v, want %q", c.name, err, receivablesReminderFinal)
		}
	}
	if err := f.exec(`UPDATE invoices.reminders SET recipient = '' WHERE id = $1`, withdrawn); err != nil {
		t.Errorf("a withdrawn letter's recipient blanked: %v, want it allowed (the erase, B1)", err)
	}
}

// receivablesChild is one of the tables whose rows hang off an issued
// invoice under guard_child_of_issued_insert, with an INSERT of one row for
// invoice $1 and its permitted and refused changes.
type receivablesChild struct {
	table, insert, immutable string
	refusesNoop              bool     // an UPDATE that changes nothing is refused (00035's rule)
	allowed                  []string // each applied in turn, every one allowed
	refused                  []string // each refused with immutable, after allowed
}

// TestChildrenOfIssued_Triggers pins the parent guard of manual deliveries,
// charge payments, waivers, holds and hand-offs (D8, D9, D11): never under a
// draft or a credit note, the note blanked for an erased customer; each
// table's single permitted change, and nothing else — a no-op refused where
// 00035's rule is copied; never deleted; the anonymised customer's lift
// note; a waiver's letter of its own invoice; and the live unique indexes.
func TestChildrenOfIssued_Triggers(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	run := f.run()
	invoice := f.issued(7)
	letter := f.mustReminder(invoice, run, 1)
	line := f.mustBankLine(f.bankFile(), strings.Repeat("a", 64))

	children := []receivablesChild{
		{
			table: "manual_deliveries", immutable: "invoices: a manual delivery is immutable", refusesNoop: true,
			insert: `INSERT INTO invoices.manual_deliveries (invoice_id, kind, delivered_on, note, recorded_by_user_id, recorded_at)
				VALUES ($1, 'posted', DATE '2026-10-06', 'Posted by hand', gen_random_uuid(), now()) RETURNING id`,
			allowed: []string{
				"note = ''",
				"removed_at = now(), removed_by_user_id = gen_random_uuid(), removal_reason = 'Recorded in error'",
			},
			refused: []string{
				"delivered_on = DATE '2026-10-07'",
				"removal_reason = 'Again'",
				"removed_at = NULL, removed_by_user_id = NULL, removal_reason = NULL",
			},
		},
		{
			table: "charge_payments", immutable: "invoices: a charge payment is immutable", refusesNoop: true,
			insert: `INSERT INTO invoices.charge_payments (invoice_id, paid_on, amount, currency, source, note, registered_by_user_id, registered_at)
				VALUES ($1, DATE '2026-11-10', 35, 'NOK', 'manual', 'Fee paid', gen_random_uuid(), now()) RETURNING id`,
			allowed: []string{
				"note = ''",
				"removed_at = now(), removed_by_user_id = gen_random_uuid(), removal_reason = 'Refunded'",
			},
			refused: []string{"amount = 1", "removal_reason = 'Again'"},
		},
		{
			table: "charge_waivers", immutable: "invoices: a charge waiver is immutable",
			insert: fmt.Sprintf(`INSERT INTO invoices.charge_waivers (invoice_id, reminder_id, kind, amount, reason, note, waived_by_user_id, waived_at)
				VALUES ($1, %d, 'fee', 35, 'goodwill', 'Kind', gen_random_uuid(), now()) RETURNING id`, letter),
			allowed: []string{"note = ''"},
			refused: []string{"amount = 1", "reason = 'claimed_in_error'"},
		},
		{
			table: "invoice_holds", immutable: "invoices: a hold is immutable",
			insert: `INSERT INTO invoices.invoice_holds (invoice_id, kind, note, placed_at, placed_by_user_id)
				VALUES ($1, 'disputed', 'Disputes the hours', now(), gen_random_uuid()) RETURNING id`,
			allowed: []string{
				"note = ''",
				"lifted_at = now(), lifted_by_user_id = gen_random_uuid(), lift_note = 'Agreed', charges_allowed = false",
				"lift_note = ''",
			},
			refused: []string{"charges_allowed = true", "lift_note = 'Again'", "kind = 'disputed', placed_at = now()"},
		},
		{
			table: "collection_handoffs", immutable: "invoices: a hand-off is immutable", refusesNoop: true,
			insert: `INSERT INTO invoices.collection_handoffs (invoice_id, handed_on, agency, agency_reference, note, created_at, created_by_user_id)
				VALUES ($1, DATE '2026-12-01', 'Inkasso AS', 'K-1', 'Sent by mail', now(), gen_random_uuid()) RETURNING id`,
			allowed: []string{
				"note = ''",
				"withdrawn_on = DATE '2026-12-10', withdrawn_by_user_id = gen_random_uuid(), withdrawal_reason = 'Paid'",
			},
			refused: []string{"agency = 'Other AS'", "withdrawal_reason = 'Again'"},
		},
	}
	draft := f.draft(7, 0)
	credit := f.issue(f.draft(7, invoice))
	var erasedHold int64
	erasedInvoice := f.issued(8)
	erasedLetter := f.mustReminder(erasedInvoice, run, 1)
	f.erase(8)
	for _, c := range children {
		parent := "invoices: " + c.table + " needs an issued invoice"
		if _, err := f.insert(c.insert, draft); !refusedWith(err, parent) {
			t.Errorf("%s under a draft: %v, want %q", c.table, err, parent)
		}
		if _, err := f.insert(c.insert, credit); !refusedWith(err, parent) {
			t.Errorf("%s under an issued credit note: %v, want %q", c.table, err, parent)
		}
		insert := c.insert
		if c.table == "charge_waivers" {
			insert = strings.Replace(insert, fmt.Sprint(letter), fmt.Sprint(erasedLetter), 1)
		}
		blanked := f.mustInsert(c.table+" for an erased customer", insert, erasedInvoice)
		if got := f.text(`SELECT note FROM invoices.`+c.table+` WHERE id = $1`, blanked); got != "" {
			t.Errorf("%s for an erased customer: note %q, want it blanked", c.table, got)
		}
		if c.table == "invoice_holds" {
			erasedHold = blanked
		}

		id := f.mustInsert(c.table, c.insert, invoice)
		if c.refusesNoop {
			if err := f.exec(`UPDATE invoices.`+c.table+` SET note = note WHERE id = $1`, id); !refusedWith(err, c.immutable) {
				t.Errorf("%s: a no-op UPDATE: %v, want %q", c.table, err, c.immutable)
			}
		}
		for _, set := range c.allowed {
			if err := f.exec(`UPDATE invoices.`+c.table+` SET `+set+` WHERE id = $1`, id); err != nil {
				t.Errorf("%s: %s: %v, want it allowed", c.table, set, err)
			}
		}
		for _, set := range c.refused {
			if err := f.exec(`UPDATE invoices.`+c.table+` SET `+set+` WHERE id = $1`, id); !refusedWith(err, c.immutable) {
				t.Errorf("%s: %s: %v, want %q", c.table, set, err, c.immutable)
			}
		}
		if err := f.exec(`DELETE FROM invoices.`+c.table+` WHERE id = $1`, id); !refusedWith(err, c.immutable) {
			t.Errorf("%s deleted: %v, want %q", c.table, err, c.immutable)
		}
	}

	// The erase blanking an unlifted hold's note leaves its lift note NULL:
	// only a lift writes it.
	f.mustExec("the erase blanks the unlifted hold's note", `UPDATE invoices.invoice_holds SET note = '' WHERE id = $1`, erasedHold)
	if got := f.text(`SELECT coalesce(lift_note, 'NULL') FROM invoices.invoice_holds WHERE id = $1`, erasedHold); got != "NULL" {
		t.Errorf("an unlifted hold's lift note after the erase = %q, want NULL", got)
	}

	// The lift of an anonymised customer's hold keeps no lift note; any
	// other lift keeps its own.
	f.mustExec("lift the erased customer's hold", `UPDATE invoices.invoice_holds SET lifted_at = now(),
		lifted_by_user_id = gen_random_uuid(), lift_note = 'Agreed with Kari', charges_allowed = false WHERE id = $1`, erasedHold)
	if got := f.text(`SELECT lift_note FROM invoices.invoice_holds WHERE id = $1`, erasedHold); got != "" {
		t.Errorf("the erased customer's lift note = %q, want it blanked", got)
	}
	liveHold := f.mustInsert("a live customer's hold", `INSERT INTO invoices.invoice_holds (invoice_id, kind, note, placed_at,
		placed_by_user_id) VALUES ($1, 'disputed', 'x', now(), gen_random_uuid()) RETURNING id`, f.issued(9))
	f.mustExec("lift a live customer's hold", `UPDATE invoices.invoice_holds SET lifted_at = now(),
		lifted_by_user_id = gen_random_uuid(), lift_note = 'Agreed', charges_allowed = true WHERE id = $1`, liveHold)
	if got := f.text(`SELECT lift_note FROM invoices.invoice_holds WHERE id = $1`, liveHold); got != "Agreed" {
		t.Errorf("a live customer's lift note = %q, want it kept", got)
	}

	// A waiver names a letter of its own invoice.
	if err := f.exec(fmt.Sprintf(`INSERT INTO invoices.charge_waivers (invoice_id, reminder_id, kind, amount, reason,
		waived_by_user_id, waived_at) VALUES ($1, %d, 'compensation', 35, 'goodwill', gen_random_uuid(), now())`, erasedLetter), invoice); !foreignKeyViolationOf(err, "fk_charge_waivers_reminder") {
		t.Errorf("a waiver naming another invoice's letter: %v, want fk_charge_waivers_reminder", err)
	}

	// The CHECKs of the children.
	for _, c := range []struct{ name, sql, check string }{
		{"a charge payment by hand naming a bank line", fmt.Sprintf(`INSERT INTO invoices.charge_payments (invoice_id, paid_on, amount,
			currency, source, bank_transaction_id, registered_by_user_id, registered_at)
			VALUES ($1, DATE '2026-11-10', 35, 'NOK', 'manual', %d, gen_random_uuid(), now())`, line), "ck_charge_payments_origin"},
		{"an imported charge payment without its line", `INSERT INTO invoices.charge_payments (invoice_id, paid_on, amount,
			currency, source, registered_by_user_id, registered_at)
			VALUES ($1, DATE '2026-11-10', 35, 'NOK', 'ocr', gen_random_uuid(), now())`, "ck_charge_payments_origin"},
		{"a charge payment of 0", `INSERT INTO invoices.charge_payments (invoice_id, paid_on, amount, currency, source,
			registered_by_user_id, registered_at) VALUES ($1, DATE '2026-11-10', 0, 'NOK', 'manual', gen_random_uuid(), now())`, "ck_charge_payments_amount"},
		{"a half removal", `INSERT INTO invoices.manual_deliveries (invoice_id, kind, delivered_on, recorded_by_user_id,
			recorded_at, removed_at) VALUES ($1, 'posted', DATE '2026-10-06', gen_random_uuid(), now(), now())`, "ck_manual_deliveries_removal"},
		{"an unknown delivery", `INSERT INTO invoices.manual_deliveries (invoice_id, kind, delivered_on, recorded_by_user_id,
			recorded_at) VALUES ($1, 'faxed', DATE '2026-10-06', gen_random_uuid(), now())`, "ck_manual_deliveries_kind"},
		{"an interest waiver without its day", fmt.Sprintf(`INSERT INTO invoices.charge_waivers (invoice_id, reminder_id, kind,
			amount, reason, waived_by_user_id, waived_at) VALUES ($1, %d, 'interest', 3, 'goodwill', gen_random_uuid(), now())`, letter), "ck_charge_waivers_interest_through"},
		{"a fee waiver with a day", fmt.Sprintf(`INSERT INTO invoices.charge_waivers (invoice_id, reminder_id, kind, amount,
			interest_through, reason, waived_by_user_id, waived_at)
			VALUES ($1, %d, 'compensation', 3, DATE '2026-11-02', 'goodwill', gen_random_uuid(), now())`, letter), "ck_charge_waivers_interest_through"},
		{"an unknown waiver reason", fmt.Sprintf(`INSERT INTO invoices.charge_waivers (invoice_id, reminder_id, kind, amount,
			reason, waived_by_user_id, waived_at) VALUES ($1, %d, 'fee', 3, 'mercy', gen_random_uuid(), now())`, letter), "ck_charge_waivers_reason"},
		{"a lift without its answer", `INSERT INTO invoices.invoice_holds (invoice_id, kind, note, placed_at, placed_by_user_id,
			lifted_at, lifted_by_user_id) VALUES ($1, 'disputed', 'x', now(), gen_random_uuid(), now(), gen_random_uuid())`, "ck_invoice_holds_lift"},
		{"a half withdrawal", `INSERT INTO invoices.collection_handoffs (invoice_id, handed_on, agency, created_at, created_by_user_id,
			withdrawn_on) VALUES ($1, DATE '2026-12-01', 'Inkasso AS', now(), gen_random_uuid(), DATE '2026-12-02')`, "ck_collection_handoffs_withdrawal"},
	} {
		if err := f.exec(c.sql, invoice); !checkViolationOf(err, c.check) {
			t.Errorf("%s: %v, want %s", c.name, err, c.check)
		}
	}

	// The live unique indexes: one live hold and one live hand-off per
	// invoice, a lifted or withdrawn one beside it; one fee and one
	// compensation waiver per letter, interest waivers one after another.
	hold := `INSERT INTO invoices.invoice_holds (invoice_id, kind, note, placed_at, placed_by_user_id)
		VALUES ($1, 'disputed', 'Again', now(), gen_random_uuid())`
	if err := f.exec(hold, invoice); err != nil {
		t.Errorf("a hold beside a lifted one: %v, want it allowed", err)
	}
	if err := f.exec(hold, invoice); !uniqueViolationOf(err, "ux_invoice_holds_live") {
		t.Errorf("a second live hold: %v, want ux_invoice_holds_live", err)
	}
	handoff := `INSERT INTO invoices.collection_handoffs (invoice_id, handed_on, agency, created_at, created_by_user_id)
		VALUES ($1, DATE '2026-12-11', 'Inkasso AS', now(), gen_random_uuid())`
	if err := f.exec(handoff, invoice); err != nil {
		t.Errorf("a hand-off beside a withdrawn one: %v, want it allowed", err)
	}
	if err := f.exec(handoff, invoice); !uniqueViolationOf(err, "ux_collection_handoffs_live") {
		t.Errorf("a second live hand-off: %v, want ux_collection_handoffs_live", err)
	}
	waiver := fmt.Sprintf(`INSERT INTO invoices.charge_waivers (invoice_id, reminder_id, kind, amount, interest_through,
		reason, waived_by_user_id, waived_at) VALUES ($1, %d, $2, 3, $3::date, 'goodwill', gen_random_uuid(), now())`, letter)
	if err := f.exec(waiver, invoice, "fee", nil); !uniqueViolationOf(err, "ux_charge_waivers_letter_kind") {
		t.Errorf("a second fee waiver of one letter: %v, want ux_charge_waivers_letter_kind", err)
	}
	if err := f.exec(waiver, invoice, "compensation", nil); err != nil {
		t.Errorf("a compensation waiver beside the fee's: %v, want it allowed", err)
	}
	if err := f.exec(waiver, invoice, "compensation", nil); !uniqueViolationOf(err, "ux_charge_waivers_letter_kind") {
		t.Errorf("a second compensation waiver of one letter: %v, want ux_charge_waivers_letter_kind", err)
	}
	for i := range 2 {
		if err := f.exec(waiver, invoice, "interest", "2026-11-02"); err != nil {
			t.Errorf("interest waiver %d of one letter: %v, want it allowed", i+1, err)
		}
	}
}

// TestRunsAndBatches_SetOnce pins the two "set once" rows (plan reading 3,
// D10): a run's counts set together once from NULL; a batch posted once — the
// triple together — or reprinted once, never both; neither deleted.
func TestRunsAndBatches_SetOnce(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	const runImmutable, batchImmutable = "invoices: a reminder run is immutable", "invoices: a print batch is immutable"

	run := f.run()
	for _, set := range []string{"letters = 1", "skipped = 0", "run_on = DATE '2026-11-03'"} {
		if err := f.exec(`UPDATE invoices.reminder_runs SET `+set+` WHERE id = $1`, run); !refusedWith(err, runImmutable) {
			t.Errorf("a run's %s: %v, want %q", set, err, runImmutable)
		}
	}
	if err := f.exec(`UPDATE invoices.reminder_runs SET letters = -1, skipped = 0 WHERE id = $1`, run); !checkViolationOf(err, "ck_reminder_runs_counts") {
		t.Errorf("a run's letters of -1: %v, want ck_reminder_runs_counts", err)
	}
	if err := f.exec(`UPDATE invoices.reminder_runs SET letters = 0, skipped = -1 WHERE id = $1`, run); !checkViolationOf(err, "ck_reminder_runs_counts") {
		t.Errorf("a run's skipped of -1: %v, want ck_reminder_runs_counts", err)
	}
	if err := f.exec(`UPDATE invoices.reminder_runs SET letters = 3, skipped = 1 WHERE id = $1`, run); err != nil {
		t.Errorf("a run's counts set: %v, want it allowed", err)
	}
	for _, set := range []string{"letters = 4, skipped = 1", "letters = NULL, skipped = NULL"} {
		if err := f.exec(`UPDATE invoices.reminder_runs SET `+set+` WHERE id = $1`, run); !refusedWith(err, runImmutable) {
			t.Errorf("a run's counts set again (%s): %v, want %q", set, err, runImmutable)
		}
	}
	if err := f.exec(`DELETE FROM invoices.reminder_runs WHERE id = $1`, run); !refusedWith(err, runImmutable) {
		t.Errorf("a run deleted: %v, want %q", err, runImmutable)
	}

	batch := func() int64 {
		return f.mustInsert("a print batch", `INSERT INTO invoices.reminder_print_batches (post_on, created_at, created_by_user_id)
			VALUES (DATE '2026-11-03', now(), gen_random_uuid()) RETURNING id`)
	}
	const posted = "posted_on = DATE '2026-11-03', posted_at = now(), posted_by_user_id = gen_random_uuid()"
	posting, reprinting := batch(), batch()
	if err := f.exec(`UPDATE invoices.reminder_print_batches SET posted_on = DATE '2026-11-03', posted_at = now() WHERE id = $1`, posting); !checkViolationOf(err, "ck_reminder_print_batches_posted") {
		t.Errorf("posted without who: %v, want ck_reminder_print_batches_posted", err)
	}
	if err := f.exec(`UPDATE invoices.reminder_print_batches SET `+posted+`, post_on = DATE '2026-11-04' WHERE id = $1`, posting); !refusedWith(err, batchImmutable) {
		t.Errorf("posted with its day moved: %v, want %q", err, batchImmutable)
	}
	if err := f.exec(`UPDATE invoices.reminder_print_batches SET `+posted+` WHERE id = $1`, posting); err != nil {
		t.Errorf("a batch posted: %v, want it allowed", err)
	}
	if err := f.exec(`UPDATE invoices.reminder_print_batches SET `+posted+` WHERE id = $1`, posting); !refusedWith(err, batchImmutable) {
		t.Errorf("a batch posted twice: %v, want %q", err, batchImmutable)
	}
	if err := f.exec(`UPDATE invoices.reminder_print_batches SET reprinted_at = now() WHERE id = $1`, posting); !checkViolationOf(err, "ck_reminder_print_batches_reprinted") {
		t.Errorf("a posted batch reprinted: %v, want ck_reminder_print_batches_reprinted", err)
	}
	if err := f.exec(`UPDATE invoices.reminder_print_batches SET reprinted_at = now() WHERE id = $1`, reprinting); err != nil {
		t.Errorf("a batch reprinted: %v, want it allowed", err)
	}
	if err := f.exec(`UPDATE invoices.reminder_print_batches SET reprinted_at = now() + interval '1 hour' WHERE id = $1`, reprinting); !refusedWith(err, batchImmutable) {
		t.Errorf("a batch reprinted twice: %v, want %q", err, batchImmutable)
	}
	if err := f.exec(`UPDATE invoices.reminder_print_batches SET `+posted+` WHERE id = $1`, reprinting); !checkViolationOf(err, "ck_reminder_print_batches_reprinted") {
		t.Errorf("a reprinted batch posted: %v, want ck_reminder_print_batches_reprinted", err)
	}
	if err := f.exec(`DELETE FROM invoices.reminder_print_batches WHERE id = $1`, reprinting); !refusedWith(err, batchImmutable) {
		t.Errorf("a batch deleted: %v, want %q", err, batchImmutable)
	}
}
