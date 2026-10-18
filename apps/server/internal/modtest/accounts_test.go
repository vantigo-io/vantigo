package modtest

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// fakeAccounts is a contracts.AccountProvisioning nobody calls: the test is
// about which provider the composition hands a module.
type fakeAccounts struct{ contracts.AccountProvisioning }

// Identity always provides Accounts, so a module composed beside it reads
// identity's provider — WithAccounts' provider instead when a test gives
// one, which Compose would otherwise overwrite with identity's, and none
// under WithoutAccounts.
func TestNew_WithAccountsReplacesIdentitysProvider(t *testing.T) {
	t.Parallel()
	compose := func(opts ...Option) contracts.AccountProvisioning {
		var got contracts.AccountProvisioning
		capture := module.Module{
			Name: "customers",
			Mount: func(d module.Deps) (http.Handler, error) {
				got = d.Accounts
				return http.NotFoundHandler(), nil
			},
		}
		New(t, append([]Option{WithRecorder(contracttest.NewForModule("customers")), WithModule(capture)}, opts...)...)
		return got
	}

	if got := compose(); got == nil {
		t.Error("without WithAccounts Deps.Accounts = nil, want identity's provider")
	} else if _, fake := got.(*fakeAccounts); fake {
		t.Error("without WithAccounts Deps.Accounts is a fake, want identity's provider")
	}
	fake := &fakeAccounts{}
	if got := compose(WithAccounts(fake)); got != fake {
		t.Errorf("with WithAccounts Deps.Accounts = %T, want the given provider", got)
	}
	if got := compose(WithAccounts(fake), WithoutAccounts()); got != nil {
		t.Errorf("with WithoutAccounts Deps.Accounts = %T, want nil", got)
	}
}

// Workers builds the composed modules' workers as worker mode does, the
// single-provider slots resolved onto the Deps it is given: a module's
// worker reads WithAccounts' provider, though Deps() itself carries none.
func TestHarness_WorkersResolveTheAccountsSlot(t *testing.T) {
	t.Parallel()
	var got contracts.AccountProvisioning
	capture := module.Module{
		Name:  "customers",
		Mount: func(module.Deps) (http.Handler, error) { return http.NotFoundHandler(), nil },
		Workers: func(d module.Deps) []worker.Worker {
			got = d.Accounts
			return nil
		},
	}
	fake := &fakeAccounts{}
	h := New(t, WithRecorder(contracttest.NewForModule("customers")), WithModule(capture), WithAccounts(fake))
	if h.Deps().Accounts != nil {
		t.Fatalf("Deps().Accounts = %T, want none (Compose resolves it per module)", h.Deps().Accounts)
	}
	h.Workers(h.Deps())
	if got != fake {
		t.Errorf("a worker's Deps.Accounts = %T, want the given provider", got)
	}
}
