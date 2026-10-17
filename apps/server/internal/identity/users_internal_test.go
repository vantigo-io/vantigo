package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// TestSetPassword_AVanishedUserIsNotFound covers the window between the
// owner-set password's lookup of its target and its transaction: a user
// deleted in between is the bare 404, not a 200 for nobody, and nothing is
// written.
func TestSetPassword_AVanishedUserIsNotFound(t *testing.T) {
	t.Parallel()
	srv, pool := newInternalServer(t)
	gone := uuid.New() // looked up a moment ago, deleted since

	answer, err := refusalOr[gen.PostIdentityOwnerUsersByIdPasswordResponseObject](
		srv.setPassword(context.Background(), gone, "AnyPassword123", nil))
	r, ok := answer.(refusal)
	if err != nil || !ok || r.status != http.StatusNotFound || r.body != nil {
		t.Fatalf("setPassword for a vanished user = %v, %v; want the bare 404", answer, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM identity.users`); n != 0 {
		t.Errorf("%d users, want none", n)
	}
}

// The deletion backstops match a foreign key refusing the deletion by its
// constraint, whichever code PostgreSQL raises: restrict_violation (23001)
// for ON DELETE RESTRICT, which both keys are, or foreign_key_violation
// (23503) for NO ACTION. Nothing else matches, wrapped or not.
func TestDeletionBackstopsMatchTheirConstraintUnderEitherCode(t *testing.T) {
	scim := func(code string) error {
		return fmt.Errorf("delete: %w", &pgconn.PgError{Code: code, TableName: "scim_user_mappings", ConstraintName: "scim_user_mappings_user_id_fkey"})
	}
	creator := func(code string) error {
		return fmt.Errorf("delete: %w", &pgconn.PgError{Code: code, TableName: "authorization_delegations", ConstraintName: "authorization_delegations_created_by_user_id_fkey"})
	}
	for _, code := range []string{"23001", "23503"} {
		if !isProvenanceViolation(scim(code)) || isDelegationCreatorViolation(scim(code)) {
			t.Errorf("%s on the SCIM mapping: provenance %t, delegation creator %t, want only provenance", code, isProvenanceViolation(scim(code)), isDelegationCreatorViolation(scim(code)))
		}
		if !isDelegationCreatorViolation(creator(code)) || isProvenanceViolation(creator(code)) {
			t.Errorf("%s on the delegation creator: delegation creator %t, provenance %t, want only delegation creator", code, isDelegationCreatorViolation(creator(code)), isProvenanceViolation(creator(code)))
		}
	}
	for _, err := range []error{scim("23505"), creator("23505"), errors.New("23001 created_by_user_id scim"), nil} {
		if isProvenanceViolation(err) || isDelegationCreatorViolation(err) {
			t.Errorf("%v matched a deletion backstop", err)
		}
	}
}

// TestIsLastActiveOwner_APendingOwnerDoesNotCount proves a pending user
// holding Owner is no active Owner: beside them, the one real Owner is the
// last, which the last-Owner refusals (an Owner's disable or demotion,
// Staff's Deactivate) rest on. A second real Owner makes neither the last.
func TestIsLastActiveOwner_APendingOwnerDoesNotCount(t *testing.T) {
	t.Parallel()
	srv, pool := newInternalServer(t)
	ctx := context.Background()
	owner := func(email string) uuid.UUID {
		id := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO identity.users (id, email, normalized_email, display_name, version, created_at, updated_at)
			VALUES ($1, $2, upper($2), $2, gen_random_uuid(), now(), now())`, id, email); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, RoleOwnerID); err != nil {
			t.Fatal(err)
		}
		return id
	}
	realOwner := owner("real-owner@example.test")
	pending := owner("pending-owner@example.test")
	if _, err := pool.Exec(ctx, `UPDATE identity.users SET managed_by = 'staff', employee_number = 'P-1', employee_number_key = 'P-1', pending_since = now() WHERE id = $1`, pending); err != nil {
		t.Fatal(err)
	}
	last := func(id uuid.UUID) bool {
		got, err := srv.q.IsLastActiveOwner(ctx, store.IsLastActiveOwnerParams{UserID: id, Now: internalNow})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if !last(realOwner) {
		t.Errorf("the real Owner beside a pending one: last = false, want true")
	}
	if last(pending) {
		t.Errorf("the pending Owner: last = true, want false (no active Owner)")
	}
	owner("second-owner@example.test")
	if last(realOwner) {
		t.Errorf("the real Owner beside a second real one: last = true, want false")
	}
}
