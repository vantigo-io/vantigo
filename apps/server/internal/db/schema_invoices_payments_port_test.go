package db_test

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The invoices payments-port schema (00046_invoices_payments_port.sql;
// invoices payments and reminders design D2's third branch, D14–D17): the
// payment provider's credentials, the payment settings, pay links, payment
// attempts, a Vipps payment's origin on the payments and the day's
// reconciliations. Its own file, as 00041's tests are.

// paymentsPortVersion is 00046's goose version.
const paymentsPortVersion = 46

// paymentsPortTables are the tables 00046 creates, in the order it creates
// them.
var paymentsPortTables = []string{
	"payment_provider_credentials", "payment_settings", "pay_links", "payment_attempts", "vipps_day_reconciliations",
	"quick_requests",
}

// paymentsPortObjects is what 00046 adds to the invoices schema, as found:
// the new tables' columns and the payments' one (table.column:type:length or
// precision:nullable:default), the tables, their triggers
// (table:trigger:function), every constraint of the new tables and the
// payments' replaced and new ones with its definition, the functions
// (name:volatility:return type), the indexes as Postgres prints them and the
// identities' starts. Each list is sorted.
func paymentsPortObjects(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string][]string {
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
			    || coalesce(character_maximum_length::text, CASE WHEN data_type = 'numeric' THEN numeric_precision || ',' || numeric_scale END, '')
			    || ':' || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'invoices'
			  AND (table_name = ANY($1) OR (table_name = 'payments' AND column_name = 'payment_attempt_id')
			    OR (table_name = 'reminders' AND column_name = 'pay_link_id'))`, paymentsPortTables),
		"tables": collect("tables", `
			SELECT table_name FROM information_schema.tables
			WHERE table_schema = 'invoices' AND table_name = ANY($1)`, paymentsPortTables),
		"triggers": collect("triggers", `
			SELECT c.relname || ':' || t.tgname || ':' || p.proname
			FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
			JOIN pg_proc p ON p.oid = t.tgfoid
			WHERE n.nspname = 'invoices' AND NOT t.tgisinternal AND c.relname = ANY($1)`, paymentsPortTables),
		"constraints": collect("constraints", `
			SELECT c.conrelid::regclass::text || ':' || c.conname || ':' || pg_get_constraintdef(c.oid)
			FROM pg_constraint c
			WHERE c.connamespace = 'invoices'::regnamespace AND c.contype IN ('c', 'f', 'u', 'p')
			  AND (c.conrelid::regclass::text = ANY(SELECT 'invoices.' || unnest($1::text[]))
			    OR c.conname IN ('ck_payments_source', 'ck_payments_origin', 'payments_payment_attempt_id_key',
			        'payments_payment_attempt_id_fkey', 'ck_reminders_pay_link', 'reminders_pay_link_id_fkey'))`, paymentsPortTables),
		"functions": collect("functions", `
			SELECT p.proname || ':' || p.provolatile::text || ':' || pg_catalog.format_type(p.prorettype, NULL)
			FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE n.nspname = 'invoices' AND p.proname IN ('refuse_row_change', 'refuse_payment_settings_change',
			    'guard_payment_child_insert', 'refuse_pay_link_change', 'set_payment_attempt_reference',
			    'refuse_payment_attempt_change')`),
		"indexes": collect("indexes", `
			SELECT indexname || ':' || indexdef FROM pg_indexes
			WHERE schemaname = 'invoices' AND (tablename = ANY($1) OR indexname = 'payments_payment_attempt_id_key')`,
			paymentsPortTables),
		"sequences": collect("sequences", `
			SELECT sequencename || ':' || start_value FROM pg_sequences
			WHERE schemaname = 'invoices' AND sequencename = ANY(SELECT unnest($1::text[]) || '_id_seq')`, paymentsPortTables),
	}
}

// TestInvoicesPaymentsPort_AppliesAndIsIdempotent proves
// 00046_invoices_payments_port.sql applies, rolls back and re-applies
// cleanly, and pins every object it adds: the five tables with every column,
// CHECK, key and foreign key, the triggers and their functions, and the
// indexes — the partial ones with their predicates; the payments' attempt and
// both CHECKs replaced (D2's third branch); every identity starting at 1001.
// The Down removes all of it and restores 00041's two CHECKs exactly.
func TestInvoicesPaymentsPort_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, paymentsPortVersion) // 00046_invoices_payments_port.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	want := map[string][]string{
		"columns": paymentsPortColumns,
		"tables": {
			"pay_links", "payment_attempts", "payment_provider_credentials", "payment_settings", "quick_requests",
			"vipps_day_reconciliations",
		},
		"constraints": paymentsPortConstraints,
		"triggers": {
			"pay_links:tr_pay_links_immutable:refuse_pay_link_change",
			"pay_links:tr_pay_links_parent:guard_payment_child_insert",
			"payment_attempts:tr_payment_attempts_immutable:refuse_payment_attempt_change",
			"payment_attempts:tr_payment_attempts_parent:guard_payment_child_insert",
			"payment_attempts:tr_payment_attempts_reference:set_payment_attempt_reference",
			"payment_settings:tr_payment_settings_kept:refuse_payment_settings_change",
			"quick_requests:tr_quick_requests_immutable:refuse_row_change",
			"vipps_day_reconciliations:tr_vipps_day_reconciliations_immutable:refuse_row_change",
		},
		"functions": {
			"guard_payment_child_insert:v:trigger",
			"refuse_pay_link_change:v:trigger",
			"refuse_payment_attempt_change:v:trigger",
			"refuse_payment_settings_change:v:trigger",
			"refuse_row_change:v:trigger",
			"set_payment_attempt_reference:v:trigger",
		},
		"indexes": paymentsPortIndexes,
		// Every identity starts at 1001, as the module's other tables do.
		"sequences": {"pay_links_id_seq:1001", "payment_attempts_id_seq:1001"},
	}
	for _, list := range want {
		slices.Sort(list)
	}
	// After the Down: the payments' CHECKs are 00041's again, and nothing
	// else of 00046's is left.
	wantDown := map[string][]string{
		"constraints": {
			"invoices.payments:ck_payments_origin:CHECK (((((source)::text = 'manual'::text) AND (bank_transaction_id IS NULL) AND (registered_by_user_id IS NOT NULL)) OR (((source)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying])::text[])) AND (bank_transaction_id IS NOT NULL) AND (registered_by_user_id IS NOT NULL))))",
			"invoices.payments:ck_payments_source:CHECK (((source)::text = ANY ((ARRAY['manual'::character varying, 'ocr'::character varying, 'camt054'::character varying])::text[])))",
		},
	}
	expect := func(when string, want map[string][]string) {
		t.Helper()
		got := paymentsPortObjects(t, ctx, pool)
		for _, what := range []string{"columns", "tables", "triggers", "constraints", "functions", "indexes", "sequences"} {
			if !equalStrings(got[what], want[what]) {
				t.Errorf("%s: %s =\n%s\nwant\n%s", when, what, strings.Join(got[what], "\n"), strings.Join(want[what], "\n"))
			}
		}
	}
	expect("after up", want)

	// The partial indexes' predicates, read on their own: one live link per
	// invoice, one live on-site attempt per invoice, and what the worker
	// claims (D15, D16).
	for name, def := range map[string]string{
		"ux_pay_links_live":                "CREATE UNIQUE INDEX ux_pay_links_live ON invoices.pay_links USING btree (invoice_id) WHERE (revoked_at IS NULL)",
		"ux_payment_attempts_on_site_live": "CREATE UNIQUE INDEX ux_payment_attempts_on_site_live ON invoices.payment_attempts USING btree (invoice_id) WHERE (((flow)::text = 'on_site'::text) AND ((state)::text = ANY ((ARRAY['creating'::character varying, 'created'::character varying, 'authorized'::character varying, 'capturing'::character varying, 'capture_unknown'::character varying])::text[])))",
		"ix_payment_attempts_due":          "CREATE INDEX ix_payment_attempts_due ON invoices.payment_attempts USING btree (next_poll_at) WHERE (((state)::text = ANY ((ARRAY['creating'::character varying, 'created'::character varying, 'authorized'::character varying, 'capturing'::character varying, 'capture_unknown'::character varying, 'cancelling'::character varying])::text[])) OR release_pending OR (((state)::text = 'captured'::text) AND (refund_watch_until IS NOT NULL)))",
	} {
		if got := indexDefinition(t, ctx, pool, "invoices", name); got != def {
			t.Errorf("%s = %s, want %s", name, got, def)
		}
	}

	migrateTo(t, url, paymentsPortVersion-1)
	expect("after down", wantDown)
	// The Down restores 00041's refuse_reminder_change, which knows no pay link.
	var body string
	if err := pool.QueryRow(ctx, `SELECT prosrc FROM pg_proc WHERE proname = 'refuse_reminder_change'`).Scan(&body); err != nil ||
		strings.Contains(body, "pay_link") || !strings.Contains(body, "'withdrawal_reason'];") {
		t.Errorf("refuse_reminder_change after the Down = %v %q, want 00041's", err, body)
	}
	receivablesBefore := receivablesObjectsBut00046(t, ctx, pool)

	migrateTo(t, url, paymentsPortVersion)
	expect("after up again", want)

	// 00046 leaves the rest of 00041's objects as they were: only the
	// payments' two CHECKs are replaced.
	receivablesAfter := receivablesObjectsBut00046(t, ctx, pool)
	for what, before := range receivablesBefore {
		if !equalStrings(receivablesAfter[what], before) {
			t.Errorf("00041's %s changed under 00046:\n%s\nwas\n%s", what,
				strings.Join(receivablesAfter[what], "\n"), strings.Join(before, "\n"))
		}
	}
}

// receivablesObjectsBut00046 is receivablesObjects less the payments' two
// CHECKs 00046 replaces and the letters' pay link it adds.
func receivablesObjectsBut00046(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string][]string {
	t.Helper()
	objects := receivablesObjects(t, ctx, pool)
	objects["constraints"] = slices.DeleteFunc(objects["constraints"], func(c string) bool {
		return strings.HasPrefix(c, "invoices.payments:ck_payments_source:") ||
			strings.HasPrefix(c, "invoices.payments:ck_payments_origin:") ||
			strings.HasPrefix(c, "invoices.reminders:ck_reminders_pay_link:") ||
			strings.HasPrefix(c, "invoices.reminders:reminders_pay_link_id_fkey:")
	})
	objects["columns"] = slices.DeleteFunc(objects["columns"], func(c string) bool {
		return strings.HasPrefix(c, "reminders.pay_link_id:")
	})
	return objects
}

// paymentsPortColumns is D14, D15, D16 and D17 written out, with the
// payments' attempt (D2).
var paymentsPortColumns = []string{
	"pay_links.created_at:timestamp with time zone::NO:",
	"pay_links.created_by_user_id:uuid::YES:",
	"pay_links.id:bigint::NO:",
	"pay_links.invoice_id:bigint::NO:",
	"pay_links.revoked_at:timestamp with time zone::YES:",
	"pay_links.revoked_by_user_id:uuid::YES:",
	"pay_links.token_sha256:character:64:NO:",
	"pay_links.url_ciphertext:text::NO:",
	"payment_attempts.abandon_reason:character varying:500:YES:",
	"payment_attempts.abandoned_at:timestamp with time zone::YES:",
	"payment_attempts.abandoned_by_user_id:uuid::YES:",
	"payment_attempts.amount:numeric:14,2:NO:",
	"payment_attempts.authorized_amount:numeric:14,2:YES:",
	"payment_attempts.authorized_at:timestamp with time zone::YES:",
	"payment_attempts.cancel_key:uuid::NO:",
	"payment_attempts.cancelled_amount:numeric:14,2:YES:",
	"payment_attempts.capture_attempts:integer::NO:0",
	"payment_attempts.capture_guaranteed_until:timestamp with time zone::YES:",
	"payment_attempts.capture_key:uuid::YES:",
	"payment_attempts.capture_psp_reference:character varying:100:YES:",
	"payment_attempts.captured_amount:numeric:14,2:YES:",
	"payment_attempts.captured_at:timestamp with time zone::YES:",
	"payment_attempts.create_key:uuid::YES:",
	"payment_attempts.created_at:timestamp with time zone::NO:",
	"payment_attempts.created_by_user_id:uuid::YES:",
	"payment_attempts.expires_at:timestamp with time zone::NO:",
	"payment_attempts.finished_at:timestamp with time zone::YES:",
	"payment_attempts.failures:integer::NO:0",
	"payment_attempts.flow:character varying:10:NO:",
	"payment_attempts.id:bigint::NO:",
	"payment_attempts.insufficient_funds:integer::NO:0",
	"payment_attempts.invoice_id:bigint::NO:",
	"payment_attempts.last_error:character varying:500:YES:",
	"payment_attempts.lease_id:character varying:100:YES:",
	"payment_attempts.lease_until:timestamp with time zone::YES:",
	"payment_attempts.msn:character varying:10:NO:",
	"payment_attempts.next_poll_at:timestamp with time zone::YES:",
	"payment_attempts.pay_link_id:bigint::YES:",
	"payment_attempts.polls:integer::NO:0",
	"payment_attempts.provider:character varying:20:NO:",
	"payment_attempts.psp_reference:character varying:100:YES:",
	"payment_attempts.redirect_url:character varying:2500:YES:",
	"payment_attempts.reference:character varying:64:NO:",
	"payment_attempts.refund_watch_until:timestamp with time zone::YES:",
	"payment_attempts.refunded_amount:numeric:14,2:YES:",
	"payment_attempts.release_pending:boolean::NO:false",
	"payment_attempts.reserved_amount:numeric:14,2:YES:",
	"payment_attempts.return_nonce_sha256:character:64:YES:",
	"payment_attempts.state:character varying:16:NO:",
	"payment_provider_credentials.id:integer::NO:",
	"payment_provider_credentials.provider:character varying:20:NO:",
	"payment_provider_credentials.rejected_at:timestamp with time zone::YES:",
	"payment_provider_credentials.secret_ciphertext:text::NO:",
	"payment_provider_credentials.settings_json:text::NO:'{}'::text",
	"payment_provider_credentials.updated_at:timestamp with time zone::NO:",
	"payment_provider_credentials.webhook_id:character varying:100:YES:",
	"payment_provider_credentials.webhook_secret_ciphertext:text::YES:",
	"payment_settings.id:smallint::NO:",
	"payment_settings.kontantsalg_acknowledged_at:timestamp with time zone::YES:",
	"payment_settings.kontantsalg_acknowledged_by_user_id:uuid::YES:",
	"payment_settings.on_site_payments_enabled:boolean::NO:false",
	"payment_settings.pay_links_enabled:boolean::NO:false",
	"payment_settings.privacy_url:character varying:500:YES:",
	"payment_settings.reference_prefix:character:6:NO:",
	"payment_settings.revision:integer::NO:1",
	"payment_settings.terms_url:character varying:500:YES:",
	"payment_settings.updated_at:timestamp with time zone::NO:",
	"payment_settings.updated_by_user_id:uuid::YES:",
	"payments.payment_attempt_id:bigint::YES:",
	"reminders.pay_link_id:bigint::YES:",
	"quick_requests.body_sha256:character:64:NO:",
	"quick_requests.created_at:timestamp with time zone::NO:",
	"quick_requests.invoice_id:bigint::NO:",
	"quick_requests.request_id:uuid::NO:",
	"vipps_day_reconciliations.day:date::NO:",
	"vipps_day_reconciliations.note:character varying:500:NO:''::character varying",
	"vipps_day_reconciliations.on_site_total:numeric:14,2:NO:",
	"vipps_day_reconciliations.pay_link_total:numeric:14,2:NO:",
	"vipps_day_reconciliations.payments:integer::NO:",
	"vipps_day_reconciliations.reconciled_at:timestamp with time zone::NO:",
	"vipps_day_reconciliations.reconciled_by_user_id:uuid::NO:",
	"vipps_day_reconciliations.total:numeric:14,2:NO:",
}

// paymentsPortConstraints is every key, foreign key and CHECK of the new
// tables, and the payments' two replaced CHECKs with the attempt's key.
var paymentsPortConstraints = []string{
	"invoices.pay_links:ck_pay_links_revocation:CHECK (((revoked_by_user_id IS NULL) OR (revoked_at IS NOT NULL)))",
	"invoices.pay_links:pay_links_invoice_id_fkey:FOREIGN KEY (invoice_id) REFERENCES invoices.invoices(id) ON DELETE RESTRICT",
	"invoices.pay_links:pay_links_pkey:PRIMARY KEY (id)",
	"invoices.pay_links:uq_pay_links_token:UNIQUE (token_sha256)",
	"invoices.payment_attempts:ck_payment_attempts_abandon:CHECK (((((state)::text = 'abandoned'::text) AND (abandoned_by_user_id IS NOT NULL) AND (abandoned_at IS NOT NULL) AND (abandon_reason IS NOT NULL) AND ((abandon_reason)::text <> ''::text)) OR (((state)::text <> 'abandoned'::text) AND (abandoned_by_user_id IS NULL) AND (abandoned_at IS NULL) AND (abandon_reason IS NULL))))",
	"invoices.payment_attempts:ck_payment_attempts_amount:CHECK ((amount >= (1)::numeric))",
	"invoices.payment_attempts:ck_payment_attempts_flow:CHECK ((((flow)::text = ANY ((ARRAY['pay_link'::character varying, 'on_site'::character varying])::text[])) AND (((flow)::text = 'pay_link'::text) = (pay_link_id IS NOT NULL)) AND (((flow)::text = 'pay_link'::text) = (return_nonce_sha256 IS NOT NULL))))",
	"invoices.payment_attempts:ck_payment_attempts_provider:CHECK (((provider)::text = 'vipps'::text))",
	"invoices.payment_attempts:ck_payment_attempts_reference:CHECK (((reference)::text ~ '^[a-zA-Z0-9-]{8,64}$'::text))",
	"invoices.payment_attempts:ck_payment_attempts_release:CHECK (((NOT release_pending) OR ((state)::text = ANY ((ARRAY['captured'::character varying, 'capture_failed'::character varying])::text[]))))",
	"invoices.payment_attempts:ck_payment_attempts_reserved:CHECK ((((reserved_amount IS NOT NULL) = ((state)::text = ANY ((ARRAY['capturing'::character varying, 'capture_unknown'::character varying])::text[]))) AND ((reserved_amount IS NULL) OR ((reserved_amount > (0)::numeric) AND (authorized_amount IS NOT NULL) AND (reserved_amount <= authorized_amount)))))",
	"invoices.payment_attempts:ck_payment_attempts_state:CHECK (((state)::text = ANY ((ARRAY['creating'::character varying, 'created'::character varying, 'authorized'::character varying, 'capturing'::character varying, 'capture_unknown'::character varying, 'captured'::character varying, 'cancelling'::character varying, 'cancelled'::character varying, 'aborted'::character varying, 'expired'::character varying, 'terminated'::character varying, 'capture_failed'::character varying, 'failed'::character varying, 'abandoned'::character varying])::text[])))",
	"invoices.payment_attempts:payment_attempts_invoice_id_fkey:FOREIGN KEY (invoice_id) REFERENCES invoices.invoices(id) ON DELETE RESTRICT",
	"invoices.payment_attempts:payment_attempts_pay_link_id_fkey:FOREIGN KEY (pay_link_id) REFERENCES invoices.pay_links(id) ON DELETE RESTRICT",
	"invoices.payment_attempts:payment_attempts_pkey:PRIMARY KEY (id)",
	"invoices.payment_attempts:uq_payment_attempts_reference:UNIQUE (reference)",
	"invoices.payment_provider_credentials:ck_payment_provider_credentials_provider:CHECK (((provider)::text = 'vipps'::text))",
	"invoices.payment_provider_credentials:ck_payment_provider_credentials_single_row:CHECK ((id = 1))",
	"invoices.payment_provider_credentials:ck_payment_provider_credentials_webhook:CHECK (((webhook_id IS NULL) = (webhook_secret_ciphertext IS NULL)))",
	"invoices.payment_provider_credentials:payment_provider_credentials_pkey:PRIMARY KEY (id)",
	"invoices.payment_settings:ck_payment_settings_acknowledgement:CHECK (((kontantsalg_acknowledged_at IS NULL) = (kontantsalg_acknowledged_by_user_id IS NULL)))",
	"invoices.payment_settings:ck_payment_settings_on_site:CHECK (((NOT on_site_payments_enabled) OR (kontantsalg_acknowledged_at IS NOT NULL)))",
	"invoices.payment_settings:ck_payment_settings_pay_links:CHECK (((NOT pay_links_enabled) OR ((terms_url IS NOT NULL) AND (privacy_url IS NOT NULL))))",
	"invoices.payment_settings:ck_payment_settings_reference_prefix:CHECK ((reference_prefix ~ '^[0-9a-z]{6}$'::text))",
	"invoices.payment_settings:ck_payment_settings_single_row:CHECK ((id = 1))",
	"invoices.payment_settings:payment_settings_pkey:PRIMARY KEY (id)",
	"invoices.payments:ck_payments_origin:CHECK (((((source)::text = 'manual'::text) AND (bank_transaction_id IS NULL) AND (registered_by_user_id IS NOT NULL) AND (payment_attempt_id IS NULL)) OR (((source)::text = ANY ((ARRAY['ocr'::character varying, 'camt054'::character varying])::text[])) AND (bank_transaction_id IS NOT NULL) AND (registered_by_user_id IS NOT NULL) AND (payment_attempt_id IS NULL)) OR (((source)::text = 'vipps'::text) AND (payment_attempt_id IS NOT NULL) AND (bank_transaction_id IS NULL) AND (registered_by_user_id IS NULL))))",
	"invoices.payments:ck_payments_source:CHECK (((source)::text = ANY ((ARRAY['manual'::character varying, 'ocr'::character varying, 'camt054'::character varying, 'vipps'::character varying])::text[])))",
	"invoices.payments:payments_payment_attempt_id_fkey:FOREIGN KEY (payment_attempt_id) REFERENCES invoices.payment_attempts(id) ON DELETE RESTRICT",
	"invoices.payments:payments_payment_attempt_id_key:UNIQUE (payment_attempt_id)",
	"invoices.reminders:ck_reminders_pay_link:CHECK (((pay_link_id IS NULL) OR (sent_on IS NOT NULL)))",
	"invoices.reminders:reminders_pay_link_id_fkey:FOREIGN KEY (pay_link_id) REFERENCES invoices.pay_links(id) ON DELETE RESTRICT",
	"invoices.quick_requests:ck_quick_requests_body:CHECK ((body_sha256 ~ '^[0-9a-f]{64}$'::text))",
	"invoices.quick_requests:quick_requests_invoice_id_fkey:FOREIGN KEY (invoice_id) REFERENCES invoices.invoices(id) ON DELETE RESTRICT",
	"invoices.quick_requests:quick_requests_invoice_id_key:UNIQUE (invoice_id)",
	"invoices.quick_requests:quick_requests_pkey:PRIMARY KEY (request_id)",
	"invoices.vipps_day_reconciliations:vipps_day_reconciliations_pkey:PRIMARY KEY (day)",
}

// paymentsPortIndexes is every index of the new tables, and the payments'
// attempt's.
var paymentsPortIndexes = []string{
	"ix_payment_attempts_captured_on:CREATE INDEX ix_payment_attempts_captured_on ON invoices.payment_attempts USING btree (captured_at)",
	"ix_payment_attempts_due:CREATE INDEX ix_payment_attempts_due ON invoices.payment_attempts USING btree (next_poll_at) WHERE (((state)::text = ANY ((ARRAY['creating'::character varying, 'created'::character varying, 'authorized'::character varying, 'capturing'::character varying, 'capture_unknown'::character varying, 'cancelling'::character varying])::text[])) OR release_pending OR (((state)::text = 'captured'::text) AND (refund_watch_until IS NOT NULL)))",
	"ix_payment_attempts_invoice:CREATE INDEX ix_payment_attempts_invoice ON invoices.payment_attempts USING btree (invoice_id)",
	"pay_links_pkey:CREATE UNIQUE INDEX pay_links_pkey ON invoices.pay_links USING btree (id)",
	"payment_attempts_pkey:CREATE UNIQUE INDEX payment_attempts_pkey ON invoices.payment_attempts USING btree (id)",
	"payment_provider_credentials_pkey:CREATE UNIQUE INDEX payment_provider_credentials_pkey ON invoices.payment_provider_credentials USING btree (id)",
	"payment_settings_pkey:CREATE UNIQUE INDEX payment_settings_pkey ON invoices.payment_settings USING btree (id)",
	"payments_payment_attempt_id_key:CREATE UNIQUE INDEX payments_payment_attempt_id_key ON invoices.payments USING btree (payment_attempt_id)",
	"uq_pay_links_token:CREATE UNIQUE INDEX uq_pay_links_token ON invoices.pay_links USING btree (token_sha256)",
	"uq_payment_attempts_reference:CREATE UNIQUE INDEX uq_payment_attempts_reference ON invoices.payment_attempts USING btree (reference)",
	"ux_pay_links_live:CREATE UNIQUE INDEX ux_pay_links_live ON invoices.pay_links USING btree (invoice_id) WHERE (revoked_at IS NULL)",
	"ux_payment_attempts_on_site_live:CREATE UNIQUE INDEX ux_payment_attempts_on_site_live ON invoices.payment_attempts USING btree (invoice_id) WHERE (((flow)::text = 'on_site'::text) AND ((state)::text = ANY ((ARRAY['creating'::character varying, 'created'::character varying, 'authorized'::character varying, 'capturing'::character varying, 'capture_unknown'::character varying])::text[])))",
	"quick_requests_invoice_id_key:CREATE UNIQUE INDEX quick_requests_invoice_id_key ON invoices.quick_requests USING btree (invoice_id)",
	"quick_requests_pkey:CREATE UNIQUE INDEX quick_requests_pkey ON invoices.quick_requests USING btree (request_id)",
	"vipps_day_reconciliations_pkey:CREATE UNIQUE INDEX vipps_day_reconciliations_pkey ON invoices.vipps_day_reconciliations USING btree (day)",
}

// The triggers' refusals, word for word.
const (
	payLinkImmutable          = "invoices: a pay link is immutable but for its revocation, once"
	attemptNeverDeleted       = "invoices: a payment attempt is never deleted"
	attemptKeepsIdentity      = "invoices: a payment attempt keeps its identity"
	attemptCreateKeyOnce      = "invoices: a payment attempt's create key is set once"
	attemptCaptureKeyReplaced = "invoices: a payment attempt's capture key is replaced only before its first capture"
	paymentSettingsKept       = "invoices: the payment settings row is never deleted, and its reference prefix never changes"
	vippsDayImmutable         = "invoices: a row of vipps_day_reconciliations is never changed"
	quickRequestImmutable     = "invoices: a row of quick_requests is never changed"
)

// attemptFinal is the refusal of a change to a terminal attempt in state.
func attemptFinal(state string) string {
	return "invoices: a payment attempt is final once " + state
}

// childOfIssued is the parent guard's refusal of a row of table.
func childOfIssued(table string) string {
	return "invoices: " + table + " needs an issued invoice"
}

// childOfErased is the parent guard's refusal of a row of table for an
// anonymised customer.
func childOfErased(table string) string {
	return "invoices: the customer is anonymised"
}

// payLink writes a live pay link of invoice with the token hash
// strings.Repeat(token, 64), and answers its id or the refusal.
func (f *receivablesFixture) payLink(invoice int64, token string) (int64, error) {
	return f.insert(`
		INSERT INTO invoices.pay_links (invoice_id, token_sha256, url_ciphertext, created_at)
		VALUES ($1, repeat($2, 64), 'sealed', now()) RETURNING id`, invoice, token)
}

// mustPayLink is payLink that must succeed.
func (f *receivablesFixture) mustPayLink(invoice int64, token string) int64 {
	f.t.Helper()
	id, err := f.payLink(invoice, token)
	if err != nil {
		f.t.Fatalf("seed a pay link: %v", err)
	}
	return id
}

// attempt writes an on-site payment attempt of invoice for 100 in state, with
// extra columns and their values (SQL, each list led by a comma), and
// answers its id or the refusal.
func (f *receivablesFixture) attempt(invoice int64, state, columns, values string) (int64, error) {
	return f.insert(`
		INSERT INTO invoices.payment_attempts (invoice_id, provider, msn, flow, amount, cancel_key, state,
		    created_at, expires_at`+columns+`)
		VALUES ($1, 'vipps', '123456', 'on_site', 100, gen_random_uuid(), $2,
		    now(), now() + interval '10 minutes'`+values+`) RETURNING id`, invoice, state)
}

// mustAttempt is attempt that must succeed.
func (f *receivablesFixture) mustAttempt(invoice int64, state, columns, values string) int64 {
	f.t.Helper()
	id, err := f.attempt(invoice, state, columns, values)
	if err != nil {
		f.t.Fatalf("seed a %s attempt: %v", state, err)
	}
	return id
}

// linkAttempt writes a pay-link attempt of invoice through link, created.
func (f *receivablesFixture) linkAttempt(invoice, link int64) (int64, error) {
	return f.insert(`
		INSERT INTO invoices.payment_attempts (invoice_id, provider, msn, flow, pay_link_id, return_nonce_sha256,
		    amount, cancel_key, state, created_at, expires_at)
		VALUES ($1, 'vipps', '123456', 'pay_link', $2, repeat('c', 64), 100, gen_random_uuid(), 'created',
		    now(), now() + interval '10 minutes') RETURNING id`, invoice, link)
}

// bareAttempt inserts an on-site attempt in state creating with provider,
// flow and amount as given, and answers the refusal.
func (f *receivablesFixture) bareAttempt(invoice int64, provider, flow, amount string) error {
	_, err := f.insert(`
		INSERT INTO invoices.payment_attempts (invoice_id, provider, msn, flow, amount, cancel_key, state, created_at, expires_at)
		VALUES ($1, $2, '123456', $3, $4::numeric, gen_random_uuid(), 'creating', now(), now()) RETURNING id`,
		invoice, provider, flow, amount)
	return err
}

const paymentsPortPaymentInsert = `
	INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, registered_by_user_id, registered_at,
	    source, bank_transaction_id, payment_attempt_id)
	VALUES ($1, DATE '2026-10-10', 100, 'NOK', $2, now(), $3, $4, $5) RETURNING id`

// TestPayments_VippsSourceCheck completes D2's origin with its third branch:
// a Vipps payment names its attempt and has neither a user nor a bank line,
// and no other source names an attempt. (00041's TestPayments_SourceCheck
// keeps the first two branches.)
func TestPayments_VippsSourceCheck(t *testing.T) {
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
		{"a Vipps payment with a user", "vipps", &user, nil, &attempt},
		{"a Vipps payment without an attempt", "vipps", nil, nil, nil},
		{"a Vipps payment with a bank line", "vipps", nil, &line, &attempt},
		{"a manual payment with an attempt", "manual", &user, nil, &attempt},
		{"an OCR payment with an attempt", "ocr", &user, &line, &attempt},
		{"a camt.054 payment with an attempt", "camt054", &user, &line, &attempt},
	} {
		if _, err := f.insert(paymentsPortPaymentInsert, invoice, c.user, c.source, c.line, c.attempt); !checkViolationOf(err, "ck_payments_origin") {
			t.Errorf("%s: %v, want ck_payments_origin", c.name, err)
		}
	}
	if _, err := f.insert(paymentsPortPaymentInsert, invoice, nil, "vipps", nil, attempt); err != nil {
		t.Errorf("a Vipps payment naming its attempt, without a user or a bank line: %v, want it allowed", err)
	}
	if _, err := f.insert(paymentsPortPaymentInsert, invoice, user, "manual", nil, nil); err != nil {
		t.Errorf("a manual payment: %v, want it allowed still", err)
	}
	if _, err := f.insert(paymentsPortPaymentInsert, invoice, user, "ocr", line, nil); err != nil {
		t.Errorf("an OCR payment: %v, want it allowed still", err)
	}
}

// TestPayments_AttemptIsUnique: one capture is one payment — a second
// payment naming the same attempt is refused (the exactly-once guard, D16).
func TestPayments_AttemptIsUnique(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	invoice := f.issued(7)
	attempt := f.mustAttempt(invoice, "captured", "", "")
	if _, err := f.insert(paymentsPortPaymentInsert, invoice, nil, "vipps", nil, attempt); err != nil {
		t.Fatalf("the first payment of the attempt: %v", err)
	}
	if _, err := f.insert(paymentsPortPaymentInsert, invoice, nil, "vipps", nil, attempt); !uniqueViolationOf(err, "payments_payment_attempt_id_key") {
		t.Errorf("a second payment of the attempt: %v, want payments_payment_attempt_id_key", err)
	}
}

// TestPaymentAttempts_Triggers pins D16's immutability: the identity never
// changes; the create key is set once; no attempt is deleted; a terminal
// attempt changes only its lease, its next poll, its release (true to false,
// with the cancelled amount) and, captured, its refund watch; and an attempt
// belongs to an issued invoice of a customer not anonymised.
func TestPaymentAttempts_Triggers(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	invoice := f.issued(7)
	other := f.issued(7)
	live := f.mustAttempt(invoice, "creating", "", "")

	// The identity, column by column.
	for _, set := range []string{
		"invoice_id = " + fmt.Sprint(other), "provider = 'vipps2'", "msn = '654321'", "flow = 'pay_link'",
		"return_nonce_sha256 = repeat('d', 64)", "reference = 'abcdef-1-99999'", "amount = 101",
		"cancel_key = gen_random_uuid()", "created_at = created_at - interval '1 second'",
		"created_by_user_id = gen_random_uuid()",
	} {
		if err := f.exec(`UPDATE invoices.payment_attempts SET `+set+` WHERE id = $1`, live); !refusedWith(err, attemptKeepsIdentity) {
			t.Errorf("SET %s: %v, want %q", set, err, attemptKeepsIdentity)
		}
	}

	// The create key: set once from NULL.
	f.mustExec("set the create key", `UPDATE invoices.payment_attempts SET create_key = gen_random_uuid() WHERE id = $1`, live)
	if err := f.exec(`UPDATE invoices.payment_attempts SET create_key = gen_random_uuid() WHERE id = $1`, live); !refusedWith(err, attemptCreateKeyOnce) {
		t.Errorf("a second create key: %v, want %q", err, attemptCreateKeyOnce)
	}
	if err := f.exec(`UPDATE invoices.payment_attempts SET create_key = NULL WHERE id = $1`, live); !refusedWith(err, attemptCreateKeyOnce) {
		t.Errorf("the create key cleared: %v, want %q", err, attemptCreateKeyOnce)
	}

	// A live attempt moves through its states.
	for _, set := range []string{
		"state = 'created', redirect_url = 'https://pay.vipps.no/x', psp_reference = 'p', next_poll_at = now(), polls = 1",
		"state = 'authorized', authorized_amount = 100, authorized_at = now(), last_error = 'slow'",
		"state = 'capturing', reserved_amount = 100, capture_key = gen_random_uuid(), lease_id = 'l', lease_until = now()",
		"state = 'capture_unknown', capture_attempts = 3",
		"failures = 2, insufficient_funds = 1",
	} {
		if err := f.exec(`UPDATE invoices.payment_attempts SET `+set+` WHERE id = $1`, live); err != nil {
			t.Errorf("a live attempt, SET %s: %v, want it allowed", set, err)
		}
	}

	if err := f.exec(`DELETE FROM invoices.payment_attempts WHERE id = $1`, live); !refusedWith(err, attemptNeverDeleted) {
		t.Errorf("DELETE a live attempt: %v, want %q", err, attemptNeverDeleted)
	}

	// Each terminal state: what may change and nothing else.
	for _, state := range []string{"captured", "cancelled", "aborted", "expired", "terminated", "capture_failed", "failed", "abandoned"} {
		releasable := state == "captured" || state == "capture_failed"
		columns, values := ", release_pending, authorized_amount", fmt.Sprintf(", %t, 100", releasable)
		if state == "abandoned" {
			columns += ", abandoned_by_user_id, abandoned_at, abandon_reason"
			values += ", gen_random_uuid(), now(), 'checked the portal'"
		}
		id := f.mustAttempt(other, state, columns, values)
		// The worker's bookkeeping: the release and the refund watch keep
		// polling a finished attempt.
		allowed := []string{
			"lease_id = 'lease', lease_until = now() + interval '1 minute'",
			"lease_id = NULL, lease_until = NULL",
			"next_poll_at = now() + interval '1 day'",
			"polls = polls + 1, last_error = 'x'",
			"failures = failures + 1",
			"last_error = NULL",
			"finished_at = now()",
		}
		refused := []string{
			"state = 'creating'", "captured_amount = 1",
			"authorized_amount = 99", "capture_attempts = capture_attempts + 1",
			"cancelled_amount = 5", "insufficient_funds = 1",
		}
		if state != "capture_failed" {
			refused = append(refused, "state = 'captured', captured_amount = 50, captured_at = now()")
		}
		if releasable {
			refused = append(refused, "release_pending = false")
			allowed = append(allowed, "release_pending = false, cancelled_amount = 20")
		}
		refused = append(refused, "release_pending = true, cancelled_amount = 30")
		if state == "captured" {
			allowed = append(allowed, "refunded_amount = 50, refund_watch_until = now() + interval '30 days'", "refund_watch_until = NULL")
		} else {
			refused = append(refused, "refunded_amount = 50", "refund_watch_until = now()")
		}
		// The refusals first, while a releasable row's release is still
		// pending; then what is allowed, the release last.
		for _, set := range refused {
			if err := f.exec(`UPDATE invoices.payment_attempts SET `+set+` WHERE id = $1`, id); !refusedWith(err, attemptFinal(state)) {
				t.Errorf("%s: SET %s: %v, want %q", state, set, err, attemptFinal(state))
			}
		}
		for _, set := range allowed {
			if err := f.exec(`UPDATE invoices.payment_attempts SET `+set+` WHERE id = $1`, id); err != nil {
				t.Errorf("%s: SET %s: %v, want it allowed", state, set, err)
			}
		}
		// Its finish is set once.
		for _, set := range []string{"finished_at = finished_at + interval '1 second'", "finished_at = NULL"} {
			if err := f.exec(`UPDATE invoices.payment_attempts SET `+set+` WHERE id = $1`, id); !refusedWith(err, attemptFinal(state)) {
				t.Errorf("%s, finished: SET %s: %v, want %q", state, set, err, attemptFinal(state))
			}
		}
		if err := f.exec(`DELETE FROM invoices.payment_attempts WHERE id = $1`, id); !refusedWith(err, attemptNeverDeleted) {
			t.Errorf("DELETE a %s attempt: %v, want %q", state, err, attemptNeverDeleted)
		}
	}
	// Once released, a captured attempt's cancelled amount stays, and its
	// release is never pending again.
	released := f.mustAttempt(other, "captured", ", release_pending, authorized_amount", ", true, 100")
	f.mustExec("release", `UPDATE invoices.payment_attempts SET release_pending = false, cancelled_amount = 20 WHERE id = $1`, released)
	for _, set := range []string{"cancelled_amount = 30", "release_pending = true"} {
		if err := f.exec(`UPDATE invoices.payment_attempts SET `+set+` WHERE id = $1`, released); !refusedWith(err, attemptFinal("captured")) {
			t.Errorf("SET %s after the release: %v, want %q", set, err, attemptFinal("captured"))
		}
	}

	// Captured money is always registered: a failed capture whose money Vipps
	// then shows captured becomes captured — with what was captured, never
	// without — released or not; no other terminal state does.
	for _, released := range []bool{false, true} {
		failed := f.mustAttempt(other, "capture_failed", ", release_pending, authorized_amount", ", true, 100")
		if released {
			f.mustExec("release", `UPDATE invoices.payment_attempts SET release_pending = false, cancelled_amount = 20 WHERE id = $1`, failed)
		}
		if err := f.exec(`UPDATE invoices.payment_attempts SET state = 'captured' WHERE id = $1`, failed); !refusedWith(err, attemptFinal("capture_failed")) {
			t.Errorf("capture_failed to captured with nothing captured: %v, want %q", err, attemptFinal("capture_failed"))
		}
		if err := f.exec(`UPDATE invoices.payment_attempts SET state = 'captured', captured_amount = 80, captured_at = now(),
			release_pending = false, refund_watch_until = now() + interval '30 days' WHERE id = $1`, failed); err != nil {
			t.Errorf("capture_failed (released %v) to captured with 80 captured: %v, want it allowed", released, err)
		}
	}

	// The parent guard: never under a draft or a credit note, never for an
	// anonymised customer.
	draft := f.draft(7, 0)
	credit := f.issue(f.draft(7, invoice))
	erased := f.issued(9)
	f.erase(9)
	for _, c := range []struct {
		name    string
		invoice int64
		want    string
	}{
		{"under a draft", draft, childOfIssued("payment_attempts")},
		{"under a credit note", credit, childOfIssued("payment_attempts")},
		{"for an anonymised customer", erased, childOfErased("payment_attempts")},
	} {
		if _, err := f.attempt(c.invoice, "creating", "", ""); !refusedWith(err, c.want) {
			t.Errorf("an attempt %s: %v, want %q", c.name, err, c.want)
		}
	}
}

// TestPaymentAttempts_Checks pins each CHECK of D16 at its edges, the
// on-site attempts' live index and the reference's pattern.
func TestPaymentAttempts_Checks(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	invoice := f.issued(7)
	link := f.mustPayLink(invoice, "a")

	for _, c := range []struct {
		name, state, columns, values, check string
	}{
		{"an unknown state", "pending", "", "", "ck_payment_attempts_state"},
		{"an on-site attempt naming a pay link", "creating", ", pay_link_id", fmt.Sprintf(", %d", link), "ck_payment_attempts_flow"},
		{"an on-site attempt with a return nonce", "creating", ", return_nonce_sha256", ", repeat('c', 64)", "ck_payment_attempts_flow"},
		{"capturing without a reservation", "capturing", ", authorized_amount", ", 100", "ck_payment_attempts_reserved"},
		{"capture_unknown without a reservation", "capture_unknown", ", authorized_amount", ", 100", "ck_payment_attempts_reserved"},
		{"a reservation while authorized", "authorized", ", authorized_amount, reserved_amount", ", 100, 100", "ck_payment_attempts_reserved"},
		{"a reservation once captured", "captured", ", authorized_amount, reserved_amount", ", 100, 100", "ck_payment_attempts_reserved"},
		{"a reservation of zero", "capturing", ", authorized_amount, reserved_amount", ", 100, 0", "ck_payment_attempts_reserved"},
		{"a reservation above the authorized amount", "capturing", ", authorized_amount, reserved_amount", ", 100, 100.01", "ck_payment_attempts_reserved"},
		{"a reservation without an authorized amount", "capturing", ", reserved_amount", ", 50", "ck_payment_attempts_reserved"},
		{"a release pending while authorized", "authorized", ", release_pending", ", true", "ck_payment_attempts_release"},
		{"a release pending once cancelled", "cancelled", ", release_pending", ", true", "ck_payment_attempts_release"},
		{"abandoned without who, when and why", "abandoned", "", "", "ck_payment_attempts_abandon"},
		{"abandoned with an empty reason", "abandoned", ", abandoned_by_user_id, abandoned_at, abandon_reason", ", gen_random_uuid(), now(), ''", "ck_payment_attempts_abandon"},
		{"abandoned without a user", "abandoned", ", abandoned_at, abandon_reason", ", now(), 'why'", "ck_payment_attempts_abandon"},
		{"a reason on a live attempt", "creating", ", abandon_reason", ", 'why'", "ck_payment_attempts_abandon"},
	} {
		if _, err := f.attempt(invoice, c.state, c.columns, c.values); !checkViolationOf(err, c.check) {
			t.Errorf("%s: %v, want %s", c.name, err, c.check)
		}
	}
	for _, c := range []struct{ name, provider, flow, amount, check string }{
		{"another provider", "stripe", "on_site", "100", "ck_payment_attempts_provider"},
		{"an amount of 0.99", "vipps", "on_site", "0.99", "ck_payment_attempts_amount"},
		{"an unknown flow", "vipps", "card", "100", "ck_payment_attempts_flow"},
		{"a pay-link attempt without its link and nonce", "vipps", "pay_link", "100", "ck_payment_attempts_flow"},
	} {
		if err := f.bareAttempt(invoice, c.provider, c.flow, c.amount); !checkViolationOf(err, c.check) {
			t.Errorf("%s: %v, want %s", c.name, err, c.check)
		}
	}

	// The edges that pass: an amount of exactly 1, a reservation of the
	// whole authorized amount, a release pending on a failed capture, an
	// abandonment with who, when and why, a pay-link attempt with both.
	for _, c := range []struct{ name, state, columns, values string }{
		{"a reservation of the whole authorized amount", "capturing", ", authorized_amount, reserved_amount", ", 100, 100"},
		{"a release pending on a failed capture", "capture_failed", ", release_pending", ", true"},
		{"an abandonment with who, when and why", "abandoned", ", abandoned_by_user_id, abandoned_at, abandon_reason", ", gen_random_uuid(), now(), 'why'"},
	} {
		if _, err := f.attempt(f.issued(8), c.state, c.columns, c.values); err != nil {
			t.Errorf("%s: %v, want it allowed", c.name, err)
		}
	}
	if err := f.bareAttempt(f.issued(8), "vipps", "on_site", "1"); err != nil {
		t.Errorf("an amount of exactly 1: %v, want it allowed", err)
	}
	if _, err := f.linkAttempt(invoice, link); err != nil {
		t.Errorf("a pay-link attempt with its link and nonce: %v, want it allowed", err)
	}

	// One live on-site attempt per invoice; a finished one, or a pay-link
	// one, does not count.
	onSite := f.issued(8)
	first := f.mustAttempt(onSite, "created", "", "")
	for _, state := range []string{"creating", "created", "authorized"} {
		if _, err := f.attempt(onSite, state, "", ""); !uniqueViolationOf(err, "ux_payment_attempts_on_site_live") {
			t.Errorf("a second live on-site attempt, %s: %v, want ux_payment_attempts_on_site_live", state, err)
		}
	}
	onSiteLink := f.mustPayLink(onSite, "b")
	if _, err := f.linkAttempt(onSite, onSiteLink); err != nil {
		t.Errorf("a pay-link attempt beside a live on-site one: %v, want it allowed", err)
	}
	f.mustExec("expire the first", `UPDATE invoices.payment_attempts SET state = 'expired' WHERE id = $1`, first)
	if _, err := f.attempt(onSite, "creating", "", ""); err != nil {
		t.Errorf("an on-site attempt after the first expired: %v, want it allowed", err)
	}

	// The reference's pattern, with the trigger that sets it switched off on
	// this test's own database.
	f.mustExec("switch the reference trigger off", `ALTER TABLE invoices.payment_attempts DISABLE TRIGGER tr_payment_attempts_reference`)
	for _, ref := range []string{"short-1", "with space-123", "under_score-1", "ends-in-dot."} {
		if _, err := f.attempt(invoice, "failed", ", reference", ", '"+ref+"'"); !checkViolationOf(err, "ck_payment_attempts_reference") {
			t.Errorf("the reference %q: %v, want ck_payment_attempts_reference", ref, err)
		}
	}
	if _, err := f.attempt(invoice, "failed", ", reference", ", 'abc123-7-1001x'"); err != nil {
		t.Errorf("a reference of the pattern: %v, want it allowed", err)
	}
}

// TestPaymentAttempts_ReferenceEndsInItsID pins I1: an attempt inserted
// without a reference gets <prefix>-<invoice number>-<its own id> from its
// BEFORE INSERT trigger, a supplied one is overwritten; and the capture key
// is replaced only while no capture was attempted (m3).
func TestPaymentAttempts_ReferenceEndsInItsID(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	invoice := f.issued(7)
	prefix := f.text(`SELECT reference_prefix FROM invoices.payment_settings WHERE id = 1`)
	number := f.text(`SELECT number::text FROM invoices.invoices WHERE id = $1`, invoice)

	id := f.mustAttempt(invoice, "creating", "", "")
	if got, want := f.text(`SELECT reference FROM invoices.payment_attempts WHERE id = $1`, id), fmt.Sprintf("%s-%s-%d", prefix, number, id); got != want {
		t.Errorf("the reference = %q, want %q: the prefix, the invoice's number and the row's own id", got, want)
	}
	supplied := f.mustAttempt(invoice, "failed", ", reference", ", 'zzzzzz-1-1'")
	if got, want := f.text(`SELECT reference FROM invoices.payment_attempts WHERE id = $1`, supplied), fmt.Sprintf("%s-%s-%d", prefix, number, supplied); got != want {
		t.Errorf("a supplied reference became %q, want it overwritten with %q", got, want)
	}

	f.mustExec("authorize", `UPDATE invoices.payment_attempts SET state = 'authorized', authorized_amount = 100 WHERE id = $1`, id)
	f.mustExec("decide", `UPDATE invoices.payment_attempts SET state = 'capturing', reserved_amount = 100, capture_key = gen_random_uuid() WHERE id = $1`, id)
	if err := f.exec(`UPDATE invoices.payment_attempts SET capture_key = gen_random_uuid(), reserved_amount = 90 WHERE id = $1`, id); err != nil {
		t.Errorf("the capture key replaced before the first capture (the re-decision): %v, want it allowed", err)
	}
	f.mustExec("the first capture", `UPDATE invoices.payment_attempts SET capture_attempts = 1 WHERE id = $1`, id)
	for _, set := range []string{"capture_key = gen_random_uuid()", "capture_key = NULL"} {
		if err := f.exec(`UPDATE invoices.payment_attempts SET `+set+` WHERE id = $1`, id); !refusedWith(err, attemptCaptureKeyReplaced) {
			t.Errorf("SET %s after the first capture: %v, want %q", set, err, attemptCaptureKeyReplaced)
		}
	}
}

// TestPayLinks_Triggers pins D15's link: one live per invoice and a token
// hash seen once; the revocation once — with or without a user, an erase's
// having none — and nothing else; never deleted; only under an issued
// invoice of a customer not anonymised (plan reading 19).
func TestPayLinks_Triggers(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	invoice := f.issued(7)
	link := f.mustPayLink(invoice, "a")

	if _, err := f.payLink(invoice, "b"); !uniqueViolationOf(err, "ux_pay_links_live") {
		t.Errorf("a second live link: %v, want ux_pay_links_live", err)
	}
	if _, err := f.payLink(f.issued(7), "a"); !uniqueViolationOf(err, "uq_pay_links_token") {
		t.Errorf("a token hash seen before: %v, want uq_pay_links_token", err)
	}
	for _, set := range []string{
		"url_ciphertext = 'other'", "token_sha256 = repeat('e', 64)", "created_at = now() - interval '1 day'",
		"created_by_user_id = gen_random_uuid()", "invoice_id = invoice_id",
	} {
		if err := f.exec(`UPDATE invoices.pay_links SET `+set+` WHERE id = $1`, link); !refusedWith(err, payLinkImmutable) {
			t.Errorf("a live link, SET %s: %v, want %q", set, err, payLinkImmutable)
		}
	}
	if _, err := f.insert(`INSERT INTO invoices.pay_links (invoice_id, token_sha256, url_ciphertext, created_at, revoked_by_user_id)
		VALUES ($1, repeat('f', 64), 'sealed', now(), gen_random_uuid()) RETURNING id`, f.issued(7)); !checkViolationOf(err, "ck_pay_links_revocation") {
		t.Errorf("a revoking user without a revocation: %v, want ck_pay_links_revocation", err)
	}
	f.mustExec("revoke", `UPDATE invoices.pay_links SET revoked_at = now(), revoked_by_user_id = gen_random_uuid() WHERE id = $1`, link)
	for _, set := range []string{"revoked_at = now() + interval '1 second'", "revoked_at = NULL, revoked_by_user_id = NULL", "revoked_by_user_id = gen_random_uuid()"} {
		if err := f.exec(`UPDATE invoices.pay_links SET `+set+` WHERE id = $1`, link); !refusedWith(err, payLinkImmutable) {
			t.Errorf("a revoked link, SET %s: %v, want %q", set, err, payLinkImmutable)
		}
	}
	if err := f.exec(`DELETE FROM invoices.pay_links WHERE id = $1`, link); !refusedWith(err, payLinkImmutable) {
		t.Errorf("DELETE a link: %v, want %q", err, payLinkImmutable)
	}
	// Revoked, the invoice may have a live link again; an erase revokes it
	// with no user.
	again := f.mustPayLink(invoice, "c")
	if err := f.exec(`UPDATE invoices.pay_links SET revoked_at = now() WHERE id = $1`, again); err != nil {
		t.Errorf("a revocation without a user (the erase's): %v, want it allowed", err)
	}

	draft := f.draft(7, 0)
	credit := f.issue(f.draft(7, invoice))
	erased := f.issued(9)
	f.erase(9)
	for _, c := range []struct {
		name    string
		invoice int64
		want    string
	}{
		{"under a draft", draft, childOfIssued("pay_links")},
		{"under a credit note", credit, childOfIssued("pay_links")},
		{"for an anonymised customer", erased, childOfErased("pay_links")},
	} {
		if _, err := f.payLink(c.invoice, "d"); !refusedWith(err, c.want) {
			t.Errorf("a link %s: %v, want %q", c.name, err, c.want)
		}
	}
}

// TestPaymentSettings_TheRow pins D14's one row: made by the migration with
// its reference prefix, which never changes; never deleted; the
// acknowledgement whole or absent, required by on-site payment; pay links
// requiring both URLs.
func TestPaymentSettings_TheRow(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	if n := f.text(`SELECT count(*)::text FROM invoices.payment_settings`); n != "1" {
		t.Fatalf("payment settings rows = %s, want the one row", n)
	}
	row := f.text(`SELECT reference_prefix || ':' || pay_links_enabled || ':' || on_site_payments_enabled || ':'
		|| coalesce(terms_url, '-') || ':' || coalesce(privacy_url, '-') || ':' || revision
		|| ':' || (kontantsalg_acknowledged_at IS NULL) FROM invoices.payment_settings`)
	if !regexp.MustCompile(`^[0-9a-z]{6}:false:false:-:-:1:true$`).MatchString(row) {
		t.Errorf("the row = %s, want six base36 characters, everything off, revision 1, nothing acknowledged", row)
	}

	if err := f.exec(`INSERT INTO invoices.payment_settings (id, reference_prefix, updated_at) VALUES (2, 'abcdef', now())`); !checkViolationOf(err, "ck_payment_settings_single_row") {
		t.Errorf("a second row: %v, want ck_payment_settings_single_row", err)
	}
	for _, set := range []string{"reference_prefix = 'zzzzzz'", "reference_prefix = '000000'"} {
		if err := f.exec(`UPDATE invoices.payment_settings SET ` + set); !refusedWith(err, paymentSettingsKept) {
			t.Errorf("SET %s: %v, want %q", set, err, paymentSettingsKept)
		}
	}
	if err := f.exec(`DELETE FROM invoices.payment_settings`); !refusedWith(err, paymentSettingsKept) {
		t.Errorf("DELETE the row: %v, want %q", err, paymentSettingsKept)
	}

	for _, c := range []struct{ set, check string }{
		{"on_site_payments_enabled = true", "ck_payment_settings_on_site"},
		{"kontantsalg_acknowledged_at = now()", "ck_payment_settings_acknowledgement"},
		{"kontantsalg_acknowledged_by_user_id = gen_random_uuid()", "ck_payment_settings_acknowledgement"},
		{"pay_links_enabled = true", "ck_payment_settings_pay_links"},
		{"pay_links_enabled = true, terms_url = 'https://example.no/terms'", "ck_payment_settings_pay_links"},
		{"pay_links_enabled = true, privacy_url = 'https://example.no/privacy'", "ck_payment_settings_pay_links"},
	} {
		if err := f.exec(`UPDATE invoices.payment_settings SET ` + c.set); !checkViolationOf(err, c.check) {
			t.Errorf("SET %s: %v, want %s", c.set, err, c.check)
		}
	}
	for _, set := range []string{
		"on_site_payments_enabled = true, kontantsalg_acknowledged_at = now(), kontantsalg_acknowledged_by_user_id = gen_random_uuid(), revision = revision + 1",
		"pay_links_enabled = true, terms_url = 'https://example.no/terms', privacy_url = 'https://example.no/privacy', updated_by_user_id = gen_random_uuid(), updated_at = now()",
		"on_site_payments_enabled = false, kontantsalg_acknowledged_at = NULL, kontantsalg_acknowledged_by_user_id = NULL",
	} {
		if err := f.exec(`UPDATE invoices.payment_settings SET ` + set); err != nil {
			t.Errorf("SET %s: %v, want it allowed", set, err)
		}
	}
}

// TestPaymentProviderCredentials_TheRow pins D14's credentials: one row, of
// Vipps, whose webhook id and sealed secret come together; a DELETE is the
// API's to make.
func TestPaymentProviderCredentials_TheRow(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	insert := `INSERT INTO invoices.payment_provider_credentials (id, provider, secret_ciphertext, webhook_id, webhook_secret_ciphertext, updated_at)
		VALUES ($1, $2, 'sealed', $3, $4, now())`
	hook := "wh-1"
	for _, c := range []struct {
		name          string
		id            int
		provider      string
		webhookID     *string
		webhookSecret *string
		check         string
	}{
		{"a second row", 2, "vipps", nil, nil, "ck_payment_provider_credentials_single_row"},
		{"another provider", 1, "stripe", nil, nil, "ck_payment_provider_credentials_provider"},
		{"a webhook id without its secret", 1, "vipps", &hook, nil, "ck_payment_provider_credentials_webhook"},
		{"a webhook secret without its id", 1, "vipps", nil, &hook, "ck_payment_provider_credentials_webhook"},
	} {
		if err := f.exec(insert, c.id, c.provider, c.webhookID, c.webhookSecret); !checkViolationOf(err, c.check) {
			t.Errorf("%s: %v, want %s", c.name, err, c.check)
		}
	}
	f.mustExec("the credentials", insert, 1, "vipps", &hook, &hook)
	if settings := f.text(`SELECT settings_json FROM invoices.payment_provider_credentials`); settings != "{}" {
		t.Errorf("settings_json = %q, want '{}' by default", settings)
	}
	f.mustExec("delete the credentials", `DELETE FROM invoices.payment_provider_credentials WHERE id = 1`)
}

// TestVippsDayReconciliations_Immutable pins D17's record: one per day,
// never changed and never deleted.
func TestVippsDayReconciliations_Immutable(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	insert := `INSERT INTO invoices.vipps_day_reconciliations (day, payments, total, on_site_total, pay_link_total,
		reconciled_by_user_id, reconciled_at) VALUES (DATE '2026-10-12', 2, 300, 100, 200, gen_random_uuid(), now())`
	f.mustExec("reconcile the day", insert)
	if note := f.text(`SELECT note FROM invoices.vipps_day_reconciliations`); note != "" {
		t.Errorf("the note = %q, want it empty by default", note)
	}
	if err := f.exec(insert); !uniqueViolationOf(err, "vipps_day_reconciliations_pkey") {
		t.Errorf("the day again: %v, want vipps_day_reconciliations_pkey", err)
	}
	for _, set := range []string{"note = 'x'", "total = 301", "day = day"} {
		if err := f.exec(`UPDATE invoices.vipps_day_reconciliations SET ` + set); !refusedWith(err, vippsDayImmutable) {
			t.Errorf("SET %s: %v, want %q", set, err, vippsDayImmutable)
		}
	}
	if err := f.exec(`DELETE FROM invoices.vipps_day_reconciliations`); !refusedWith(err, vippsDayImmutable) {
		t.Errorf("DELETE: %v, want %q", err, vippsDayImmutable)
	}
}

// A quick invoice's request id (D17, after review): one row per id and one
// per invoice, a body hash of 64 lowercase hex digits, under an existing
// invoice; never changed or deleted.
func TestQuickRequests_TheRow(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	invoice := f.issued(7)
	other := f.issued(7)
	insert := func(request string, invoiceID int64, sha string) error {
		return f.exec(`INSERT INTO invoices.quick_requests (request_id, invoice_id, body_sha256, created_at)
			VALUES ($1::uuid, $2, $3, now())`, request, invoiceID, sha)
	}
	sha := strings.Repeat("a", 64)
	first := "00000000-0000-4000-8000-000000000001"
	if err := insert(first, invoice, sha); err != nil {
		t.Fatalf("the first request: %v", err)
	}
	if err := insert(first, other, sha); !uniqueViolationOf(err, "quick_requests_pkey") {
		t.Errorf("the id again: %v, want quick_requests_pkey", err)
	}
	if err := insert("00000000-0000-4000-8000-000000000002", invoice, sha); !uniqueViolationOf(err, "quick_requests_invoice_id_key") {
		t.Errorf("a second id for the invoice: %v, want quick_requests_invoice_id_key", err)
	}
	if err := insert("00000000-0000-4000-8000-000000000003", other, strings.Repeat("A", 64)); !checkViolationOf(err, "ck_quick_requests_body") {
		t.Errorf("an uppercase hash: %v, want ck_quick_requests_body", err)
	}
	if err := insert("00000000-0000-4000-8000-000000000004", 999999, sha); err == nil {
		t.Error("a request of no invoice was accepted")
	}
	for _, sql := range []string{`UPDATE invoices.quick_requests SET created_at = created_at`, `DELETE FROM invoices.quick_requests`} {
		if err := f.exec(sql); !refusedWith(err, quickRequestImmutable) {
			t.Errorf("%s: %v, want %q", sql, err, quickRequestImmutable)
		}
	}
}

// reminderPayLinkForeign is refuse_reminder_change's refusal of another
// invoice's link.
const reminderPayLinkForeign = "invoices: a reminder's pay link is its own invoice's"

// TestReminders_PayLink pins a letter's pay link (00046, after review): one
// of its facts — written with them, never without them (ck_reminders_pay_link),
// cleared with them while the letter is in flight — its own invoice's link
// only, and frozen once the letter is sent, a revoked link kept.
func TestReminders_PayLink(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	run := f.run()
	invoice := f.issued(7)
	link := f.mustPayLink(invoice, "a")
	other := f.mustPayLink(f.issued(7), "b")
	letter := f.mustReminder(invoice, run, 1)

	if err := f.exec(`UPDATE invoices.reminders SET pay_link_id = $2 WHERE id = $1`, letter, link); !checkViolationOf(err, "ck_reminders_pay_link") {
		t.Errorf("a link without the facts: %v, want ck_reminders_pay_link", err)
	}
	if err := f.exec(`UPDATE invoices.reminders SET `+receivablesFacts+`, pay_link_id = $2 WHERE id = $1`, letter, other); !refusedWith(err, reminderPayLinkForeign) {
		t.Errorf("another invoice's link: %v, want %q", err, reminderPayLinkForeign)
	}
	for _, c := range []struct{ name, set string }{
		{"written with the facts", receivablesFacts + ", pay_link_id = $2"},
		{"cleared with the facts", "sent_on = NULL, deadline = NULL, credited = NULL, total = NULL, pay_link_id = NULL"},
		{"written again", receivablesFacts + ", pay_link_id = $2"},
	} {
		if err := f.exec(`UPDATE invoices.reminders SET `+c.set+` WHERE id = $1 AND $2::bigint IS NOT NULL`, letter, link); err != nil {
			t.Errorf("a queued letter's link %s: %v, want it allowed", c.name, err)
		}
	}
	f.mustExec("the letter sent", `UPDATE invoices.reminders SET status = 'sent', sent_at = now() WHERE id = $1`, letter)
	f.mustExec("the link revoked", `UPDATE invoices.pay_links SET revoked_at = now() WHERE id = $1`, link)
	if got := f.text(`SELECT pay_link_id::text FROM invoices.reminders WHERE id = $1`, letter); got != fmt.Sprint(link) {
		t.Errorf("a sent letter's link after its revocation = %s, want %d kept", got, link)
	}
	for _, set := range []string{"pay_link_id = NULL", "pay_link_id = pay_link_id + 0, sent_on = sent_on + 1"} {
		if err := f.exec(`UPDATE invoices.reminders SET `+set+` WHERE id = $1`, letter); !refusedWith(err, receivablesReminderFinal) {
			t.Errorf("a sent letter, SET %s: %v, want %q", set, err, receivablesReminderFinal)
		}
	}
	if err := f.exec(`DELETE FROM invoices.pay_links WHERE id = $1`, link); err == nil {
		t.Error("a letter's link deleted, want it refused")
	}
}
