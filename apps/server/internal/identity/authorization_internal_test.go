package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestRoleMutationLockKeyIsDotNets pins the role lock key to .NET's
// RoleMutationLockKey (AZ/AuthorizationMutationService.cs:27-31). The
// expected keys were computed by that very code, SHA256.HashData over
// Guid.ToByteArray() read with BinaryPrimitives.ReadInt64BigEndian, run on
// this host's .NET 10 SDK, and agree with Python's
// struct.unpack(">q", sha256(uuid.bytes_le).digest()[:8]).
func TestRoleMutationLockKeyIsDotNets(t *testing.T) {
	for id, want := range map[string]int64{
		"00000000-0000-4000-8000-000000000003": -5557501446355267480, // the built-in User role
		"0f1e2d3c-4b5a-4978-8695-a4b3c2d1e0ff": 2525443575146273166,
	} {
		if got := roleMutationLockKey(uuid.MustParse(id)); got != want {
			t.Errorf("roleMutationLockKey(%s) = %d, want %d", id, got, want)
		}
	}
}

// TestAccessConflictFilter proves the /access route group's filter
// (EA/AuthorizationManagementEndpoints.cs:20-34) on what an operation
// returns: every lost race AuthorizationConflict.IsExpected names becomes
// the flat 409 authorization_conflict; a refusal, any other error and a
// response pass through untouched; and the operations outside that group
// are not filtered at all.
func TestAccessConflictFilter(t *testing.T) {
	returning := func(response any, err error) func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
		return func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) { return response, err }
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/identity/access/roles/x", nil)

	for name, err := range map[string]error{
		"serialization failure":     &pgconn.PgError{Code: "40001"},
		"deadlock":                  &pgconn.PgError{Code: "40P01"},
		"unique violation":          fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23505"}),
		"concurrent version change": fmt.Errorf("update: %w", errConcurrentChange),
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			response, got := accessConflictFilter(returning(nil, err), "PutIdentityAccessRolesById")(context.Background(), w, req, nil)
			if response != nil || got != nil {
				t.Fatalf("filter returned (%v, %v), want the answer written and nothing returned", response, got)
			}
			if w.Code != http.StatusConflict || w.Header().Get("Content-Type") != "application/json" ||
				w.Body.String() != `{"code":"authorization_conflict","message":"The authorization state changed concurrently."}`+"\n" {
				t.Errorf("answer %d %q %s", w.Code, w.Header().Get("Content-Type"), w.Body)
			}
		})
	}

	t.Run("anything else passes through", func(t *testing.T) {
		other := errors.New("boom")
		for _, c := range []struct {
			response any
			err      error
		}{{nil, roleExists}, {nil, other}, {"a response", nil}} {
			w := httptest.NewRecorder()
			response, err := accessConflictFilter(returning(c.response, c.err), "PostIdentityAccessRoles")(context.Background(), w, req, nil)
			if response != c.response || !errors.Is(err, c.err) || w.Body.Len() != 0 {
				t.Errorf("filter turned (%v, %v) into (%v, %v) and wrote %q", c.response, c.err, response, err, w.Body)
			}
		}
	})

	t.Run("outside the /access route group", func(t *testing.T) {
		conflict := &pgconn.PgError{Code: "40001"}
		for _, op := range []string{"GetIdentityAccessMe", "PostIdentityAccessGroups", "PutIdentityAccessGroupsByGroupIdMembersByUserId", "PutIdentityOwnerUsersById", "GetIdentityAccount"} {
			w := httptest.NewRecorder()
			if _, err := accessConflictFilter(returning(nil, conflict), op)(context.Background(), w, req, nil); !errors.Is(err, conflict) || w.Body.Len() != 0 {
				t.Errorf("%s: the filter answered (%v, %q), want the error untouched", op, err, w.Body)
			}
		}
	})
}
