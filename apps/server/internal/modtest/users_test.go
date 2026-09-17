package modtest

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// TestNew_ComposesIdentityAsTheUserDirectoryProvider proves what every real
// composition now guarantees: identity is always mounted (enabledModules
// never filters it out) and always declares Module.Users, so Deps.Users is
// never nil once New has composed, unlike Deps.Directory or Deps.Products,
// which stay nil until some enabled module opts in. capture's Mount is
// handed "customers" as its Name only to reuse a real embedded contract
// (openapi.Load needs one for every composed module's Name); it never
// mounts a router or serves customers itself, the same trick identity's own
// harness_test.go catalogModule plays.
func TestNew_ComposesIdentityAsTheUserDirectoryProvider(t *testing.T) {
	t.Parallel()
	var got contracts.UserDirectory
	capture := module.Module{
		Name: "customers",
		Mount: func(d module.Deps) (http.Handler, error) {
			got = d.Users
			return http.NotFoundHandler(), nil
		},
	}

	New(t, WithRecorder(contracttest.NewForModule("customers")), WithModule(capture))

	if got == nil {
		t.Error("Deps.Users = nil, want identity's user directory: identity is always mounted and always provides one")
	}
}
