package entra

import (
	"context"
	"errors"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// A call to identity from inside one of the module's transactions is
// refused before it is made; outside one, a missing port answers as
// identity absent would.
func TestContractCalls_RefuseInsideATransaction(t *testing.T) {
	s := &server{deps: module.Deps{}}
	locked := context.WithValue(context.Background(), lockedTxKey{}, true)
	if _, err := s.unconnected(locked, 1); !errors.Is(err, errCallInsideTransaction) {
		t.Errorf("unconnected inside a transaction: %v, want errCallInsideTransaction", err)
	}
	keys := []contracts.DirectoryKey{{ObjectID: "x"}}
	if _, err := s.keyHolders(locked, keys); !errors.Is(err, errCallInsideTransaction) {
		t.Errorf("keyHolders inside a transaction: %v, want errCallInsideTransaction", err)
	}
	if _, err := s.unconnected(context.Background(), 1); !errors.Is(err, contracts.ErrSSONotConfigured) {
		t.Errorf("unconnected without identity: %v, want ErrSSONotConfigured", err)
	}
	if got, err := s.keyHolders(context.Background(), keys); err != nil || got != nil {
		t.Errorf("keyHolders without identity: %v, %v, want nothing", got, err)
	}
}
