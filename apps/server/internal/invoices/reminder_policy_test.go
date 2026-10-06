package invoices_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func reminderPolicyPath(customerID int32) string {
	return fmt.Sprintf("/api/v1/invoices/customers/%d/reminder-policy", customerID)
}

// reminderPolicyJSON is a customer's reminder policy as a client reads it.
type reminderPolicyJSON struct {
	CustomerID int32      `json:"customerId"`
	Mode       string     `json:"mode"`
	Note       string     `json:"note"`
	UpdatedAt  *time.Time `json:"updatedAt"`
	UpdatedBy  *string    `json:"updatedBy"`
}

// readPolicy reads customer's policy as an invoices:access holder.
func readPolicy(t *testing.T, h *harness, customer int32) reminderPolicyJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, reminderPolicyPath(customer), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET %s = %d %s", reminderPolicyPath(customer), res.Status, res.Body)
	}
	var p reminderPolicyJSON
	res.JSON(&p)
	return p
}

// putPolicy sets customer's policy as an invoices:payments holder.
func putPolicy(t *testing.T, h *harness, customer int32, mode, note string) *modtest.Response {
	t.Helper()
	return payer(t, h).Do(http.MethodPut, reminderPolicyPath(customer), map[string]any{"mode": mode, "note": note})
}

// policySet puts a policy and answers it, failing unless it was set.
func policySet(t *testing.T, h *harness, customer int32, mode, note string) reminderPolicyJSON {
	t.Helper()
	res := putPolicy(t, h, customer, mode, note)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT %s %s %q = %d %s, want 200", reminderPolicyPath(customer), mode, note, res.Status, res.Body)
	}
	var p reminderPolicyJSON
	res.JSON(&p)
	return p
}

// plantPolicy writes a policy row directly — the merge slot's tests need
// customers this installation has no documents for.
func plantPolicy(t *testing.T, h *harness, customer int32, mode, note string) {
	t.Helper()
	h.Exec(t, `INSERT INTO invoices.customer_reminder_policies (customer_id, mode, note, updated_by_user_id, updated_at)
		VALUES ($1, $2, $3, gen_random_uuid(), now())`, customer, mode, note)
}

// plantPolicyBy writes a policy row set by by at at.
func plantPolicyBy(t *testing.T, h *harness, customer int32, mode, note string, by uuid.UUID, at time.Time) {
	t.Helper()
	h.Exec(t, `INSERT INTO invoices.customer_reminder_policies (customer_id, mode, note, updated_by_user_id, updated_at)
		VALUES ($1, $2, $3, $4, $5)`, customer, mode, note, by, at)
}

// policyRow is customer's row as "mode|note", or "" without one.
func policyRow(t *testing.T, h *harness, customer int32) string {
	t.Helper()
	var row string
	err := h.Pool().QueryRow(context.Background(),
		`SELECT mode || '|' || note FROM invoices.customer_reminder_policies WHERE customer_id = $1`, customer).Scan(&row)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("read customer %d's policy: %v", customer, err)
	}
	return row
}

// A policy (D7): normal without a row; each mode set and read back with its
// note trimmed, who and when; 400 on the fields; invoices:payments sets it,
// invoices:access reads it, invoices:manage alone does not set it.
func TestPolicy_CRUD(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))

	if p := readPolicy(t, h, customerAcme); p.CustomerID != customerAcme || p.Mode != "normal" || p.Note != "" || p.UpdatedAt != nil || p.UpdatedBy != nil {
		t.Errorf("without a row = %+v, want normal, no note, nobody's", p)
	}
	h.Advance(time.Hour)
	for _, c := range []struct{ mode, note, want string }{
		{"none", "  Tvist om leveransen  ", "Tvist om leveransen"},
		{"no_charges", "Fast kunde", "Fast kunde"},
		{"normal", "Ringer før purring", "Ringer før purring"},
	} {
		client, who := h.SignInUser(t, "invoices:access", "invoices:payments")
		res := client.Do(http.MethodPut, reminderPolicyPath(customerAcme), map[string]any{"mode": c.mode, "note": c.note})
		var p reminderPolicyJSON
		res.JSON(&p)
		if res.Status != http.StatusOK || p.Mode != c.mode || p.Note != c.want || p.UpdatedBy == nil || *p.UpdatedBy != who.String() ||
			p.UpdatedAt == nil || !p.UpdatedAt.Equal(h.Now()) {
			t.Errorf("PUT %s = %d %+v, want it stored with its note %q, by %s now", c.mode, res.Status, p, c.want, who)
		}
		if got := readPolicy(t, h, customerAcme); got.Mode != c.mode || got.Note != c.want {
			t.Errorf("GET after PUT %s = %+v", c.mode, got)
		}
	}

	for _, c := range []struct {
		body  map[string]any
		field string
	}{
		{map[string]any{"mode": "sometimes", "note": ""}, "mode"},
		{map[string]any{"mode": "", "note": ""}, "mode"},
		{map[string]any{"mode": "none", "note": strings.Repeat("ø", 501)}, "note"},
	} {
		res := payer(t, h).Do(http.MethodPut, reminderPolicyPath(customerAcme), c.body)
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[c.field]) == 0 {
			t.Errorf("PUT %v = %d %s, want 400 on %s", c.body, res.Status, res.Body, c.field)
		}
	}
	if res := payer(t, h).Do(http.MethodPut, reminderPolicyPath(customerAcme), map[string]any{"mode": "none", "note": strings.Repeat("ø", 500)}); res.Status != http.StatusOK {
		t.Errorf("a note of 500 characters = %d %s, want 200", res.Status, res.Body)
	}
	for _, perms := range [][]string{{"invoices:access"}, {"invoices:access", "invoices:manage"}} {
		res := h.SignIn(t, perms...).Do(http.MethodPut, reminderPolicyPath(customerAcme), map[string]any{"mode": "normal", "note": ""})
		if res.Status != http.StatusForbidden {
			t.Errorf("PUT as %v = %d, want 403", perms, res.Status)
		}
	}
	if got := policyRow(t, h, customerAcme); got != "none|"+strings.Repeat("ø", 500) {
		t.Errorf("the row after the refusals = %.20s…, want the last one set", got)
	}
}

// A customer with no document here — never invoiced, or unknown — has no
// policy to set: 404, nothing written.
func TestPolicy_404WithoutDocuments(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	for _, customer := range []int32{customerNoTerms, customerUnknown} {
		if res := putPolicy(t, h, customer, "none", "Tvist"); res.Status != http.StatusNotFound {
			t.Errorf("PUT for customer %d without documents = %d %s, want 404", customer, res.Status, res.Body)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.customer_reminder_policies`); n != 0 {
		t.Errorf("%d policies written, want none", n)
	}
}

// An anonymised customer keeps its issued documents, but gets no fresh note
// about them (D7, plan reading 11): 404, nothing written.
func TestPolicy_404ForAnErasedCustomer(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	issuedFor(t, h, customerPerson, line("Konsultasjon", 1, 1000, vat25))
	inTx(t, h, true, func(tx pgx.Tx) {
		if _, err := invoices.Module().CustomerPersonalData(disabledDeps(h)).EraseCustomerData(context.Background(), tx, customerPerson); err != nil {
			t.Fatalf("EraseCustomerData: %v", err)
		}
	})
	if res := putPolicy(t, h, customerPerson, "none", "Ringte og sa at hun flytter"); res.Status != http.StatusNotFound {
		t.Errorf("PUT for an anonymised customer = %d %s, want 404", res.Status, res.Body)
	}
	if got := policyRow(t, h, customerPerson); got != "" {
		t.Errorf("the anonymised customer's policy = %q, want none", got)
	}
}

// No row is normal (D7): a PUT of normal with an empty note deletes the row
// and answers normal; normal with a note keeps one.
func TestPolicy_NormalWithEmptyNoteDeletes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	policySet(t, h, customerAcme, "no_charges", "Fast kunde")
	if p := policySet(t, h, customerAcme, "normal", "   "); p.Mode != "normal" || p.Note != "" || p.UpdatedAt != nil {
		t.Errorf("normal with an empty note = %+v, want normal with nothing recorded", p)
	}
	if got := policyRow(t, h, customerAcme); got != "" {
		t.Errorf("the row after normal with an empty note = %q, want it deleted", got)
	}
	policySet(t, h, customerAcme, "normal", "Ringer før purring")
	if got := policyRow(t, h, customerAcme); got != "normal|Ringer før purring" {
		t.Errorf("normal with a note = %q, want the row kept", got)
	}
}

// merged runs the merge slot from → into in a committed transaction and
// answers what it reported.
func merged(t *testing.T, h *harness, from, into int32) []contracts.RepointedReferences {
	t.Helper()
	var moved []contracts.RepointedReferences
	inTx(t, h, true, func(tx pgx.Tx) {
		var err error
		if moved, err = invoices.Module().CustomerReferences(disabledDeps(h)).RepointCustomer(context.Background(), tx, from, into); err != nil {
			t.Fatalf("RepointCustomer(%d, %d): %v", from, into, err)
		}
	})
	return moved
}

// stricterMode is the test's own reading of D7's order: none, then
// no_charges, then normal.
func stricterMode(a, b string) string {
	for _, m := range []string{"none", "no_charges"} {
		if a == m || b == m {
			return m
		}
	}
	return "normal"
}

// policiesReported is the merge's invoices.customerReminderPolicies count.
func policiesReported(t *testing.T, moved []contracts.RepointedReferences) int64 {
	t.Helper()
	for _, m := range moved {
		if m.Kind == "invoices.customerReminderPolicies" {
			return m.Count
		}
	}
	t.Fatalf("the merge reported %+v, without invoices.customerReminderPolicies", moved)
	return 0
}

// The merge (D7): with both rows the stricter mode wins — none over
// no_charges over normal, whichever side holds it — and the notes are joined,
// the survivor's first, " / ", cut to 500 characters; the absorbed row goes.
// The merged row is the stricter row's author's — the survivor's on a tie —
// at the merge's clock (the Task 6 review's M7). With only the absorbed
// customer's row it moves; with none nothing moves. Each reported as
// invoices.customerReminderPolicies.
func TestPolicy_MergeStricterWins(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	planted := h.Now().Add(-48 * time.Hour)
	h.Advance(time.Hour)
	for _, c := range []struct {
		from, into                       int32
		fromMode, fromNote, intoMode, in string
		want                             string
	}{
		{9116, 9117, "none", "Tvist", "none", "Konkurs", "none|Konkurs / Tvist"},
		{9101, 9102, "none", "Tvist", "no_charges", "Fast kunde", "none|Fast kunde / Tvist"},
		{9103, 9104, "no_charges", "Fast kunde", "none", "Tvist", "none|Tvist / Fast kunde"},
		{9105, 9106, "no_charges", "Avtale", "normal", "Ring først", "no_charges|Ring først / Avtale"},
		{9107, 9108, "normal", "Ring først", "no_charges", "", "no_charges|Ring først"},
		{9110, 9109, "none", strings.Repeat("b", 300), "normal", strings.Repeat("a", 300),
			"none|" + (strings.Repeat("a", 300) + " / " + strings.Repeat("b", 300))[:500]},
	} {
		fromBy, intoBy := uuid.New(), uuid.New()
		plantPolicyBy(t, h, c.from, c.fromMode, c.fromNote, fromBy, planted)
		plantPolicyBy(t, h, c.into, c.intoMode, c.in, intoBy, planted)
		if n := policiesReported(t, merged(t, h, c.from, c.into)); n != 1 {
			t.Errorf("merge %d → %d reported %d policies, want 1", c.from, c.into, n)
		}
		if got := policyRow(t, h, c.into); got != c.want {
			t.Errorf("merge %d (%s) → %d (%s) = %q, want %q", c.from, c.fromMode, c.into, c.intoMode, got, c.want)
		}
		if got := policyRow(t, h, c.from); got != "" {
			t.Errorf("merge %d → %d left the absorbed row %q", c.from, c.into, got)
		}
		author := intoBy
		if c.fromMode == stricterMode(c.fromMode, c.intoMode) && c.fromMode != c.intoMode {
			author = fromBy
		}
		var by uuid.UUID
		var at time.Time
		if err := h.Pool().QueryRow(context.Background(), `SELECT updated_by_user_id, updated_at
			FROM invoices.customer_reminder_policies WHERE customer_id = $1`, c.into).Scan(&by, &at); err != nil {
			t.Fatalf("read the merged row: %v", err)
		}
		if by != author || !at.Equal(h.Now()) {
			t.Errorf("merge %d (%s) → %d (%s) = by %s at %s, want the stricter row's author %s at the merge's %s",
				c.from, c.fromMode, c.into, c.intoMode, by, at, author, h.Now())
		}
	}
	long := policyRow(t, h, 9109)
	if n := utf8.RuneCountInString(strings.TrimPrefix(long, "none|")); n != 500 {
		t.Errorf("the joined note = %d characters, want 500", n)
	}

	plantPolicy(t, h, 9111, "none", "Konkurs")
	if n := policiesReported(t, merged(t, h, 9111, 9112)); n != 1 {
		t.Errorf("moving a lone row reported %d, want 1", n)
	}
	if from, into := policyRow(t, h, 9111), policyRow(t, h, 9112); from != "" || into != "none|Konkurs" {
		t.Errorf("a lone row = from %q into %q, want it moved", from, into)
	}
	plantPolicy(t, h, 9114, "no_charges", "Fast kunde")
	if n := policiesReported(t, merged(t, h, 9113, 9114)); n != 0 {
		t.Errorf("a merge whose absorbed customer has no row reported %d, want 0", n)
	}
	if got := policyRow(t, h, 9114); got != "no_charges|Fast kunde" {
		t.Errorf("the survivor's own row = %q, want it untouched", got)
	}
	if n := policiesReported(t, merged(t, h, 9115, 9115)); n != 0 {
		t.Errorf("from == into reported %d, want 0", n)
	}
}

// exportedPolicy is the section's reminderPolicy as a client reads it.
type exportedPolicy struct {
	ReminderPolicy *struct {
		Mode      string    `json:"mode"`
		Note      string    `json:"note"`
		UpdatedAt time.Time `json:"updatedAt"`
	} `json:"reminderPolicy"`
	Documents []json.RawMessage `json:"documents"`
}

// policyExportOf is the invoices section of customer's export, as JSON.
func policyExportOf(t *testing.T, h *harness, customer int32) (exportedPolicy, bool) {
	t.Helper()
	section, err := invoices.Module().CustomerPersonalData(disabledDeps(h)).ExportCustomerData(context.Background(), customer)
	if err != nil {
		t.Fatalf("ExportCustomerData(%d): %v", customer, err)
	}
	if section == nil {
		return exportedPolicy{}, false
	}
	b, err := json.Marshal(section)
	if err != nil {
		t.Fatalf("encode the section: %v", err)
	}
	var e exportedPolicy
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatalf("decode the section: %v", err)
	}
	return e, true
}

// A person's export carries the policy — its mode, note and time — and a
// customer with only a policy here is not "nothing held"; the erase deletes
// it and reports it, and a second erase reports zero (D7, D19).
func TestPolicy_ExportAndErase(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	issuedFor(t, h, customerPerson, line("Konsultasjon", 1, 1000, vat25))
	h.Advance(time.Hour)
	set := policySet(t, h, customerPerson, "none", "Betalingsavtale til mars")

	e, ok := policyExportOf(t, h, customerPerson)
	if !ok || e.ReminderPolicy == nil || e.ReminderPolicy.Mode != "none" || e.ReminderPolicy.Note != "Betalingsavtale til mars" ||
		!e.ReminderPolicy.UpdatedAt.Equal(*set.UpdatedAt) {
		t.Errorf("the export's policy = %+v, want none, its note and %s", e.ReminderPolicy, set.UpdatedAt)
	}
	if e, ok := policyExportOf(t, h, customerAcme); ok && e.ReminderPolicy != nil {
		t.Errorf("another customer's export carries a policy: %+v", e.ReminderPolicy)
	}
	plantPolicy(t, h, 9201, "no_charges", "Bare en merknad")
	if e, ok := policyExportOf(t, h, 9201); !ok || e.ReminderPolicy == nil || e.ReminderPolicy.Note != "Bare en merknad" || len(e.Documents) != 0 {
		t.Errorf("a customer with only a policy = %v %+v, want a section with the policy and no documents", ok, e)
	}

	plantPolicy(t, h, customerAcme, "no_charges", "Acme")
	erase := func() int64 {
		var erased []contracts.ErasedData
		inTx(t, h, true, func(tx pgx.Tx) {
			var err error
			if erased, err = invoices.Module().CustomerPersonalData(disabledDeps(h)).EraseCustomerData(context.Background(), tx, customerPerson); err != nil {
				t.Fatalf("EraseCustomerData: %v", err)
			}
		})
		if last := erased[len(erased)-1]; last.Kind == "invoices.customerReminderPolicies" {
			return last.Count
		}
		t.Fatalf("erased = %+v, want invoices.customerReminderPolicies last", erased)
		return 0
	}
	if n := erase(); n != 1 {
		t.Errorf("the erase reported %d policies, want 1", n)
	}
	if got := policyRow(t, h, customerPerson); got != "" {
		t.Errorf("the person's policy after the erase = %q, want it deleted", got)
	}
	if got := policyRow(t, h, customerAcme); got != "no_charges|Acme" {
		t.Errorf("another customer's policy = %q, want it kept", got)
	}
	if n := erase(); n != 0 {
		t.Errorf("a second erase reported %d policies, want 0", n)
	}
	if e, _ := policyExportOf(t, h, customerPerson); e.ReminderPolicy != nil {
		t.Errorf("the export after the erase carries %+v, want no policy", e.ReminderPolicy)
	}
}

// The PUT's lock order (D18, plan reading 11): the customer's documents FOR
// SHARE, newest first, then the policy row once there is one; the merge
// slot's policy rows by customer id ascending, after the documents. Not
// parallel: the seam is the package's.
func TestPolicy_LockOrder(t *testing.T) {
	h := newHarness(t)
	var docs []int64
	for _, d := range []string{"A", "B", "C"} {
		docs = append(docs, createDraft(t, h, draftBody(customerAcme, line(d, 1, 100, vat25))).ID)
	}
	createDraft(t, h, draftBody(customerNoTerms, line("D", 1, 100, vat25)))
	seen := &lockSeen{}
	restore := invoices.SetLockTaken(seen.note)
	defer restore()

	documents := []string{"document " + idKey(docs[2]), "document " + idKey(docs[1]), "document " + idKey(docs[0])}
	policySet(t, h, customerAcme, "none", "Tvist")
	if got := seen.take(); !slices.Equal(got, documents) {
		t.Errorf("the first PUT locked %v, want %v: no row to lock yet", got, documents)
	}
	policySet(t, h, customerAcme, "no_charges", "")
	if got, want := seen.take(), append(slices.Clone(documents), "policy "+idKey(customerAcme)); !slices.Equal(got, want) {
		t.Errorf("the second PUT locked %v, want %v", got, want)
	}

	plantPolicy(t, h, customerNoTerms, "none", "")
	merged(t, h, customerNoTerms, customerAcme)
	if got, want := seen.take(), []string{"policy " + idKey(customerAcme), "policy " + idKey(customerNoTerms)}; !slices.Equal(got, want) {
		t.Errorf("the merge locked %v, want %v: by customer id ascending", got, want)
	}
}
