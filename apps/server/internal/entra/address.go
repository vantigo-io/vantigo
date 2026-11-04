package entra

import (
	"errors"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/directoryaddress"
)

// This file is the address rule (Entra design D13, research §3.1): the user
// principal name the module gives a user it creates, and its mailNickname —
// directoryaddress's rule under the UPN's alphabet. The local part is the
// mailNickname, at most 64 characters. What is Entra's own is the domain: the
// connection's upn_domain, else the first verified domain that is not the
// tenant's initial one (*.onmicrosoft.com), else the first.

// errNoDomain: the connection names no UPN domain and the tenant verified
// none, so no address can be made.
var errNoDomain = errors.New("entra: no UPN domain: set one in the account rules, or test the connection")

// fold is directoryaddress.Fold: the table's letters replaced and every
// other letter's combining marks stripped.
func fold(s string) string { return directoryaddress.Fold(s) }

// address is the address a create tries (directoryaddress.Address).
type address struct {
	a directoryaddress.Address
}

// fromEmail reports whether the local part is the person's work email's,
// tried once.
func (a address) fromEmail() bool { return a.a.FromEmail }

// attempt is the n-th address to try, from 1: the UPN and the mailNickname,
// and false when there is no n-th.
func (a address) attempt(n int) (upn, nickname string, ok bool) { return a.a.Attempt(n) }

// addressFor is the address rule for spec: its work email when the domain
// is one of verified and the local part folds into the UPN's alphabet, else
// pattern over the folded names at domain (nil: the first verified domain
// that is not the tenant's initial one, else the first). errNoDomain when
// there is no domain to use.
func addressFor(spec contracts.ExternalSpec, pattern string, domain *string, verified []string) (address, error) {
	at := ""
	if domain != nil {
		at = strings.ToLower(strings.TrimSpace(*domain))
	}
	if at == "" {
		for _, d := range verified {
			if !strings.HasSuffix(strings.ToLower(d), ".onmicrosoft.com") {
				at = strings.ToLower(d)
				break
			}
		}
	}
	if at == "" && len(verified) > 0 {
		at = strings.ToLower(verified[0])
	}
	a, err := directoryaddress.UPN.For(spec, pattern, at, verified)
	if errors.Is(err, directoryaddress.ErrNoDomain) {
		return address{}, errNoDomain
	}
	return address{a: a}, err
}

// temporaryPassword is the create's password (directoryaddress.
// TemporaryPassword): 32 characters, one of each class; Graph asks for one,
// the person never uses it — the pass replaces it — and it is never stored
// or logged.
func temporaryPassword() (string, error) { return directoryaddress.TemporaryPassword() }
