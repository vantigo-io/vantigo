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
