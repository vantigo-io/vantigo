package modtest

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
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
