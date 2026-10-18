package staff

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// Every key Staff asks identity for the holders of is a key of Staff's own
// catalog (Task 2a review LOW-3): a misspelt key would name no holder, so
// the fallback mails nobody and the sole-holder rule finds nobody sole —
// silently, which is what this test is for.
func TestHolders_TheKeysAskedAboutAreInTheCatalog(t *testing.T) {
	t.Parallel()
	catalog := map[string]bool{}
	for _, p := range permissions {
		catalog[p.Key] = true
	}
	asked := append(append([]string{}, byRightKeys...), permPersonalData)
	if len(asked) != 3 {
		t.Errorf("the keys asked about = %v, want staff:approve, staff:manage and staff:personal-data", asked)
	}
	for _, key := range asked {
		if !catalog[key] {
			t.Errorf("Staff asks for the holders of %q, which its catalog does not name", key)
		}
	}
}

// countingUsers is a directory counting PermissionHolders calls, answering
// one holder or err.
type countingUsers struct {
	silentUsers
	calls int
	err   error
}

func (u *countingUsers) PermissionHolders(context.Context, string) ([]contracts.Holder, error) {
	u.calls++
	if u.err != nil {
		return nil, u.err
	}
	return []contracts.Holder{{ID: uuid.New()}}, nil
}

// The holders are read once per request and key, an error included: the
// memo lives on the request's caller.
func TestHolders_MemoisedPerRequest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	users := &countingUsers{}
	s := &server{deps: module.Deps{Users: users}}
	caller := &callerContext{}
	for range 3 {
		if hs, err := s.holders(ctx, caller, permManage); err != nil || len(hs) != 1 {
			t.Fatalf("holders = %v, %v", hs, err)
		}
	}
	if users.calls != 1 {
		t.Errorf("one key in one request read the directory %d times, want once", users.calls)
	}
	_, _ = s.holders(ctx, caller, permApprove)
	_, _ = s.holders(ctx, &callerContext{}, permManage)
	if users.calls != 3 {
		t.Errorf("another key and another request read it %d times in all, want 3", users.calls)
	}
	failing := &countingUsers{err: errors.New("down")}
	s = &server{deps: module.Deps{Users: failing}}
	caller = &callerContext{}
	for range 2 {
		if _, err := s.holders(ctx, caller, permPersonalData); err == nil {
			t.Error("a failed read answered no error")
		}
	}
	if failing.calls != 1 {
		t.Errorf("a failed read was asked %d times in one request, want once", failing.calls)
	}
}
