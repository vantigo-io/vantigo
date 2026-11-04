package entra

import (
	"errors"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The address rule's domain, Entra's own (Entra design D13): the rule itself
// is directoryaddress's, tested there.

func TestAddressFor_TheDomain(t *testing.T) {
	t.Parallel()
	spec := contracts.ExternalSpec{GivenName: "Kari", FamilyName: "Nordmann"}
	// No upn_domain: the first verified domain that is not the initial one.
	a, err := addressFor(spec, "given.family", nil, []string{"nordlys.onmicrosoft.com", "Nordlys.no"})
	if err != nil {
		t.Fatal(err)
	}
	if upn, _, _ := a.attempt(1); upn != "kari.nordmann@nordlys.no" {
		t.Errorf("without upn_domain: %q", upn)
	}
	// Only the initial domain: it is used.
	a, err = addressFor(spec, "given.family", nil, []string{"nordlys.onmicrosoft.com"})
	if err != nil {
		t.Fatal(err)
	}
	if upn, _, _ := a.attempt(1); upn != "kari.nordmann@nordlys.onmicrosoft.com" {
		t.Errorf("only the initial domain: %q", upn)
	}
	// upn_domain wins over the verified ones.
	domain := " Nordlys.Example "
	a, err = addressFor(spec, "g.family", &domain, []string{"nordlys.no"})
	if err != nil {
		t.Fatal(err)
	}
	if upn, _, _ := a.attempt(2); upn != "k.nordmann2@nordlys.example" {
		t.Errorf("with upn_domain: %q", upn)
	}
	if _, err := addressFor(spec, "given.family", nil, nil); !errors.Is(err, errNoDomain) {
		t.Errorf("no domain at all: %v, want errNoDomain", err)
	}
}
