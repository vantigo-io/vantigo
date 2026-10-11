package staff

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// silentUsers is contracts.UserDirectory answering nothing, and silentMail an
// SMTP seam that delivers nowhere: this test is about where a call is made
// from, not what it answers.
type silentUsers struct{}

var _ contracts.UserDirectory = silentUsers{}

func (silentUsers) User(context.Context, uuid.UUID) (*contracts.UserEntry, error) { return nil, nil }

func (silentUsers) Users(context.Context, []uuid.UUID) ([]contracts.UserEntry, error) {
	return nil, nil
}

func (silentUsers) SearchUsers(context.Context, string, int) ([]contracts.UserEntry, error) {
	return nil, nil
}

func silentMail(context.Context, config.MailConfig, mail.Outbound) error { return nil }

// The whole point of routing every call out of the module through
// contractscalls.go is that the suite can prove none is made inside one of
// the module's transactions. That check is only worth having if it fires, so
// this drives every accessor from outside any transaction, from inside a
// read-only snapshot and from inside a locked transaction, and holds the
// hook's record and each accessor's answer against what was called where:
// outside, every call goes through; inside either kind, every call is
// reported and refused with errCallInsideTransaction.
//
// It is deliberately not parallel: it swaps the package-level hook TestMain
// installed and puts it back, and Go pauses every parallel test while a
// sequential one runs.
func TestContractCalls_AreReportedAndRefusedInsideATransaction(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	s := &server{deps: module.Deps{Pool: pool, Users: silentUsers{}, SMTPSend: silentMail}}

	var outside, inside []string
	previous := contractCallHook
	t.Cleanup(func() { contractCallHook = previous })
	contractCallHook = func(ctx context.Context, method string) {
		if inOpenTx(ctx) {
			inside = append(inside, method)
			return
		}
		outside = append(outside, method)
	}

	call := func(ctx context.Context) []error {
		user := uuid.New()
		_, e1 := s.usersUser(ctx, user)
		_, e2 := s.usersUsers(ctx, []uuid.UUID{user})
		_, e3 := s.usersSearch(ctx, "kari", 20)
		e4 := s.smtpSend(ctx, config.MailConfig{}, mail.Outbound{})
		return []error{e1, e2, e3, e4}
	}
	want := []string{"Users.User", "Users.Users", "Users.SearchUsers", "SMTPSend"}

	for _, err := range call(ctx) {
		if err != nil {
			t.Errorf("outside a transaction a call failed: %v", err)
		}
	}
	if !slices.Equal(outside, want) || len(inside) != 0 {
		t.Errorf("outside a transaction the hook saw %v outside and %v inside, want %v outside", outside, inside, want)
	}

	for name, run := range map[string]func(func(context.Context) error) error{
		"withReadTx": func(fn func(context.Context) error) error {
			return s.withReadTx(ctx, func(ctx context.Context, _ *store.Queries) error { return fn(ctx) })
		},
		"withLockedTx": func(fn func(context.Context) error) error {
			return s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, _ *store.Queries) error { return fn(ctx) })
		},
	} {
		inside = nil
		if err := run(func(ctx context.Context) error {
			for _, err := range call(ctx) {
				if !errors.Is(err, errCallInsideTransaction) {
					t.Errorf("%s: a call returned %v, want errCallInsideTransaction", name, err)
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !slices.Equal(inside, want) {
			t.Errorf("%s: the hook saw %v inside, want every call reported: %v", name, inside, want)
		}
	}

	// A batch of no names is not a call: there is nothing to ask.
	outside = nil
	if users, err := s.usersUsers(ctx, nil); err != nil || len(users) != 0 {
		t.Errorf("usersUsers(nil) = %v, %v, want no users and no error", users, err)
	}
	if len(outside) != 0 {
		t.Errorf("an empty batch of names reported %v, want no call at all", outside)
	}
}
