package communications_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the Suppressions area's tests (EP/SuppressionEndpoints.cs,
// communications inventory §1.5, §2's CreateSuppression bullet, §19.2 items
// 3 and 20, §19.1 item 7, design doc D7) for getCommunicationsSuppressions,
// postCommunicationsSuppressions, getCommunicationsSuppressionsById and
// deleteCommunicationsSuppressionsById.
//
// Dispatch correction 1: all four operations require
// communications:suppressions-manage alone -- including both reads. That is
// the opposite pairing from conversations, where reads take
// conversations-view; every permission test below proves conversations-view
// (and conversations-manage) do NOT substitute for suppressions-manage,
// guarding against pattern-matching across modules.

type suppressionJSON struct {
	Id           string    `json:"id"`
	EmailAddress string    `json:"emailAddress"`
	Reason       *string   `json:"reason"`
	CreatedAt    time.Time `json:"createdAt"`
}

// createSuppression posts body with c and fails t unless the response
// status matches want, returning the decoded suppression.
func createSuppression(t *testing.T, c *modtest.Client, body map[string]any, want int) suppressionJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/communications/suppressions", body)
	if r.Status != want {
		t.Fatalf("create suppression: status %d body %s, want %d", r.Status, r.Body, want)
	}
	var s suppressionJSON
	r.JSON(&s)
	return s
}

// suppressionAddress mints a unique-per-test email address (lowercase, so
// the uppercase-echo tests below prove something) the same way
// channelAddress mints unique channel addresses.
func suppressionAddress(t *testing.T) string {
	t.Helper()
	name := strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	if len(name) > 30 {
		name = name[:30]
	}
	return name + "-" + uuid.NewString() + "@example.test"
}

// ---- GetCommunicationsSuppressions ----

func TestListSuppressions_EmptyBareArray(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:suppressions-manage")

	r := c.Do(http.MethodGet, "/api/v1/communications/suppressions", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got []suppressionJSON
	r.JSON(&got)
	if got == nil {
		t.Error("body decoded to nil, want a bare [] array")
	}
}

// TestListSuppressions_OrderedByCreatedAtDescending pins inventory §1.5 /
// §19.2 item 19: newest first. This is the opposite direction from tags'
// Name ascending order (TestListTags_OrderedByNameAscending) -- the two
// list orderings must not be harmonised. h.Advance guarantees a
// distinguishable CreatedAt between the two inserts rather than relying on
// wall-clock granularity.
func TestListSuppressions_OrderedByCreatedAtDescending(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:suppressions-manage")

	older := createSuppression(t, c, map[string]any{"emailAddress": suppressionAddress(t)}, http.StatusCreated)
	h.Advance(time.Hour)
	newer := createSuppression(t, c, map[string]any{"emailAddress": suppressionAddress(t)}, http.StatusCreated)

	r := c.Do(http.MethodGet, "/api/v1/communications/suppressions", nil)
	var list []suppressionJSON
	r.JSON(&list)
	var olderIdx, newerIdx = -1, -1
	for i, s := range list {
		if s.Id == older.Id {
			olderIdx = i
		}
		if s.Id == newer.Id {
			newerIdx = i
		}
	}
	if olderIdx == -1 || newerIdx == -1 {
		t.Fatalf("both suppressions must be present: older at %d, newer at %d", olderIdx, newerIdx)
	}
	if newerIdx > olderIdx {
		t.Errorf("newer at %d, older at %d, want newer before older (CreatedAt descending)", newerIdx, olderIdx)
	}
}

// TestListSuppressions_RequiresManage pins dispatch correction 1:
// conversations-view (the permission that would gate a read in every other
// area of this module) must NOT suffice; only suppressions-manage does.
func TestListSuppressions_RequiresManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if r := h.SignIn(t, "communications:conversations-view").Do(http.MethodGet, "/api/v1/communications/suppressions", nil); r.Status != http.StatusForbidden {
		t.Errorf("conversations-view: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t).Do(http.MethodGet, "/api/v1/communications/suppressions", nil); r.Status != http.StatusForbidden {
		t.Errorf("no permission: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t, "communications:suppressions-manage").Do(http.MethodGet, "/api/v1/communications/suppressions", nil); r.Status != http.StatusOK {
		t.Errorf("suppressions-manage: status %d, want 200", r.Status)
	}
}

// ---- PostCommunicationsSuppressions ----

// TestCreateSuppression_UppercasesAndEchoesStoredForm is the task's central
// assertion (design doc D7, inventory §19.2 item 3): EmailSuppression.Normalize
// is Trim().ToUpperInvariant(). A mixed-case address in must come back
// uppercased both on the create response and on a subsequent GET by id,
// proving the uppercased form -- not the input -- is what got stored.
func TestCreateSuppression_UppercasesAndEchoesStoredForm(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:suppressions-manage")
	mixedCase := "MiXed-" + uuid.NewString() + "@Example.Test"
	want := strings.ToUpper(mixedCase)

	created := createSuppression(t, c, map[string]any{"emailAddress": mixedCase}, http.StatusCreated)
	if created.EmailAddress != want {
		t.Errorf("create echo = %q, want %q (uppercased)", created.EmailAddress, want)
	}

	r := c.Do(http.MethodGet, "/api/v1/communications/suppressions/"+created.Id, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get: status %d body %s, want 200", r.Status, r.Body)
	}
	var fetched suppressionJSON
	r.JSON(&fetched)
	if fetched.EmailAddress != want {
		t.Errorf("stored/fetched form = %q, want %q (uppercased) -- proves the STORED value is uppercase, not just the response echo", fetched.EmailAddress, want)
	}
}

// TestCreateSuppression_DedupeIsNeverConflict is the brief's correction,
// pinned directly: an address that already exists answers 200, never 409 --
// the unique constraint on normalized_email_address exists to enforce the
// invariant, not to report a conflict. It also pins inventory §19.2 item 20:
// the 200 carries the PRE-EXISTING row (original id, reason, createdAt),
// discarding the second request's new reason entirely. A one-line mutation
// flipping this response to 409, or one that overwrote reason on conflict,
// would fail this test.
func TestCreateSuppression_DedupeIsNeverConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:suppressions-manage")
	address := suppressionAddress(t)
	originalReason := "first reason"

	first := createSuppression(t, c, map[string]any{"emailAddress": address, "reason": originalReason}, http.StatusCreated)
	h.Advance(time.Hour)
	second := createSuppression(t, c, map[string]any{"emailAddress": address, "reason": "second reason, must be discarded"}, http.StatusOK)

	if second.Id != first.Id {
		t.Errorf("second.Id = %q, want %q (same pre-existing row)", second.Id, first.Id)
	}
	if second.Reason == nil || *second.Reason != originalReason {
		t.Errorf("second.Reason = %v, want %q (the original reason, not the discarded new one)", second.Reason, originalReason)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("second.CreatedAt = %v, want %v (the original createdAt)", second.CreatedAt, first.CreatedAt)
	}

	// Also confirm case-insensitivity of the dedupe itself: a differently
	//-cased repeat of the same address must still collapse onto the same row.
	third := createSuppression(t, c, map[string]any{"emailAddress": strings.ToLower(address)}, http.StatusOK)
	if third.Id != first.Id {
		t.Errorf("differently-cased repeat: Id = %q, want %q", third.Id, first.Id)
	}
}

// TestCreateSuppression_InvalidEmailIsFieldError400 pins the field-keyed
// (not flat) shape of ValidateSuppression's failure -- unlike CreateTag's
// flat 400, this one carries fields.emailAddress.
func TestCreateSuppression_InvalidEmailIsFieldError400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:suppressions-manage")

	r := c.Do(http.MethodPost, "/api/v1/communications/suppressions", map[string]any{"emailAddress": "not-an-email"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	if body.Error.Code != "invalid_request" || body.Error.Message != "The request is invalid." {
		t.Errorf("error = %+v, want invalid_request / \"The request is invalid.\"", body.Error)
	}
	if len(body.field("emailAddress")) == 0 {
		t.Errorf("fields = %v, want fields.emailAddress", body.Error.Fields)
	}
}

// TestCreateSuppression_ReasonTooLongIsFieldError400 pins ValidOptional's
// 500-character bound on reason (CommunicationValidation.cs:68).
func TestCreateSuppression_ReasonTooLongIsFieldError400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:suppressions-manage")

	r := c.Do(http.MethodPost, "/api/v1/communications/suppressions", map[string]any{
		"emailAddress": suppressionAddress(t),
		"reason":       strings.Repeat("x", 501),
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	if len(body.field("reason")) == 0 {
		t.Errorf("fields = %v, want fields.reason", body.Error.Fields)
	}
}

// TestCreateSuppression_RequiresManage pins dispatch correction 1 for the
// write side too, mirroring the list test above.
func TestCreateSuppression_RequiresManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := map[string]any{"emailAddress": suppressionAddress(t)}
	if r := h.SignIn(t, "communications:conversations-manage").Do(http.MethodPost, "/api/v1/communications/suppressions", body); r.Status != http.StatusForbidden {
		t.Errorf("conversations-manage: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t, "communications:suppressions-manage").Do(http.MethodPost, "/api/v1/communications/suppressions", body); r.Status != http.StatusCreated {
		t.Errorf("suppressions-manage: status %d, want 201", r.Status)
	}
}

// ---- GetCommunicationsSuppressionsById ----

func TestGetSuppression_NotFoundBare404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:suppressions-manage")

	r := c.Do(http.MethodGet, "/api/v1/communications/suppressions/"+uuid.NewString(), nil)
	if r.Status != http.StatusNotFound {
		t.Fatalf("status %d body %s, want 404", r.Status, r.Body)
	}
	if body := strings.TrimSpace(string(r.Body)); body != "" {
		t.Errorf("body = %q, want empty (bare 404)", body)
	}
}

func TestGetSuppression_RequiresManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin := h.SignIn(t, "communications:suppressions-manage")
	created := createSuppression(t, admin, map[string]any{"emailAddress": suppressionAddress(t)}, http.StatusCreated)

	if r := h.SignIn(t, "communications:conversations-view").Do(http.MethodGet, "/api/v1/communications/suppressions/"+created.Id, nil); r.Status != http.StatusForbidden {
		t.Errorf("conversations-view: status %d, want 403", r.Status)
	}
	if r := admin.Do(http.MethodGet, "/api/v1/communications/suppressions/"+created.Id, nil); r.Status != http.StatusOK {
		t.Errorf("suppressions-manage: status %d, want 200", r.Status)
	}
}

// ---- DeleteCommunicationsSuppressionsById ----

func TestDeleteSuppression_RemovesRowThenNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:suppressions-manage")
	created := createSuppression(t, c, map[string]any{"emailAddress": suppressionAddress(t)}, http.StatusCreated)
	path := "/api/v1/communications/suppressions/" + created.Id

	first := c.Do(http.MethodDelete, path, nil)
	if first.Status != http.StatusNoContent {
		t.Fatalf("first delete: status %d body %s, want 204", first.Status, first.Body)
	}
	if body := strings.TrimSpace(string(first.Body)); body != "" {
		t.Errorf("204 body = %q, want empty", body)
	}

	if r := c.Do(http.MethodGet, path, nil); r.Status != http.StatusNotFound {
		t.Errorf("get after delete: status %d, want 404", r.Status)
	}

	second := c.Do(http.MethodDelete, path, nil)
	if second.Status != http.StatusNotFound {
		t.Fatalf("second delete: status %d body %s, want 404 bare", second.Status, second.Body)
	}
	if body := strings.TrimSpace(string(second.Body)); body != "" {
		t.Errorf("404 body = %q, want empty (bare)", body)
	}
}

func TestDeleteSuppression_NotFoundBare404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:suppressions-manage")

	r := c.Do(http.MethodDelete, "/api/v1/communications/suppressions/"+uuid.NewString(), nil)
	if r.Status != http.StatusNotFound {
		t.Fatalf("status %d body %s, want 404", r.Status, r.Body)
	}
}

func TestDeleteSuppression_RequiresManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin := h.SignIn(t, "communications:suppressions-manage")
	created := createSuppression(t, admin, map[string]any{"emailAddress": suppressionAddress(t)}, http.StatusCreated)
	path := "/api/v1/communications/suppressions/" + created.Id

	if r := h.SignIn(t, "communications:conversations-manage").Do(http.MethodDelete, path, nil); r.Status != http.StatusForbidden {
		t.Errorf("conversations-manage: status %d, want 403", r.Status)
	}
	if r := admin.Do(http.MethodDelete, path, nil); r.Status != http.StatusNoContent {
		t.Errorf("suppressions-manage: status %d, want 204", r.Status)
	}
}

// ---- Fix round 1: the dedupe race ----

// TestCreateSuppression_ConcurrentDedupeAnswers200TwiceNever409 is fix
// round 1's teeth check, on the exact gate technique
// channels_concurrency_test.go's TestCreateChannel_ConcurrentDefaultRaceAnswersModuleShape
// established and this file reuses (race, awaitLockWaiters — unexported in
// this package, not redeclared here): two concurrent POSTs for the SAME
// address, gated so both pass the existing-row lookup (a plain SELECT,
// compatible with the gate's EXCLUSIVE mode) before either reaches the
// INSERT (ROW EXCLUSIVE, which queues behind the gate). Once both are
// confirmed waiting, the gate releases; Postgres's unique index on
// normalized_email_address then serializes the inserts, so exactly one
// request's INSERT succeeds and the other's fails with a 23505 on
// ux_suppressions_normalized_email_address.
//
// Before the fix that loser's unique violation reached httpx.WriteError's
// host-wide fallback: a bare RFC 7807 409 on application/problem+json,
// wrong vocabulary for this module and a status postCommunicationsSuppressions
// never declares (only 200/201/400/401/403) — the contract harness itself
// rejects it ("response 409: status is not supported"). After the fix, the
// loser's insert failure is caught on that specific constraint, re-read,
// and answered with exactly the sequential dedupe path's 200 and the
// winner's row: same id and reason for both responses, and no 409 ever
// leaves the process. Run at -count=5 (per fix round instructions) since a
// race this narrow does not always land the same way twice.
func TestCreateSuppression_ConcurrentDedupeAnswers200TwiceNever409(t *testing.T) {
	h := newHarness(t)
	c := h.SignIn(t, "communications:suppressions-manage")
	address := suppressionAddress(t)

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.suppressions IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock suppressions: %v", err)
	}

	const n = 2
	fns := make([]func() *modtest.Response, n)
	for i := 0; i < n; i++ {
		fns[i] = func() *modtest.Response {
			return c.Do(http.MethodPost, "/api/v1/communications/suppressions", map[string]any{"emailAddress": address})
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(fns...)
		close(finished)
	}()
	awaitLockWaiters(t, h, n, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	var created, deduped int
	var ids, reasons []string
	for _, r := range responses {
		switch r.Status {
		case http.StatusCreated:
			created++
		case http.StatusOK:
			deduped++
		default:
			t.Fatalf("status %d body %s, want 200 or 201 (never 409)", r.Status, r.Body)
		}
		if ct := r.Header("Content-Type"); ct != "application/json" {
			t.Errorf("status %d Content-Type = %q, want application/json (the module's own vocabulary)", r.Status, ct)
		}
		var s suppressionJSON
		r.JSON(&s)
		ids = append(ids, s.Id)
		reason := "<nil>"
		if s.Reason != nil {
			reason = *s.Reason
		}
		reasons = append(reasons, reason)
	}
	if created != 1 {
		t.Errorf("created (201) = %d, want exactly 1", created)
	}
	if deduped != n-1 {
		t.Errorf("deduped (200) = %d, want exactly %d", deduped, n-1)
	}
	if ids[0] != ids[1] {
		t.Errorf("ids = %v, want both responses to carry the same winning row's id", ids)
	}
	if reasons[0] != reasons[1] {
		t.Errorf("reasons = %v, want both responses to carry the same winning row's reason", reasons)
	}

	// Exactly one row exists for the address, whichever request won.
	count := h.Count(t, `SELECT count(*) FROM communications.suppressions WHERE normalized_email_address = $1`, strings.ToUpper(address))
	if count != 1 {
		t.Errorf("rows for address = %d, want exactly 1", count)
	}
}
