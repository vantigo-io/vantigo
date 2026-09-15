package identity_test

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// bodyWithoutA400 are the identity operations whose contract takes a
// request body but documents no 400. On them every undecodable body —
// malformed, or cut off by the router's cap — answers the decode 400
// (writeDecodeError) off-contract, as it did before the cap existed, and
// as .NET's model binding answered a body it could not bind there. The
// contract is .NET's, so the gap is recorded here rather than papered over
// with a second, equally undocumented status (413) for one kind of
// undecodable body.
var bodyWithoutA400 = []string{
	"deleteIdentityAccessRolesById",
	"postIdentityAccessDelegationsByIdRevoke",
	"postIdentityOidcCallback",
	"postIdentityOwnerInvitationsByIdResend",
}

// TestBodyCap_EveryOperationWithABodyDocumentsA400 is what makes the
// router's request-body cap answer on-contract: a body past the cap fails
// the generated wrapper's decode, which module.DecodeError answers with the
// operation's 400 (writeDecodeError). So every identity operation that
// takes a request body documents a 400, except the four in bodyWithoutA400.
func TestBodyCap_EveryOperationWithABodyDocumentsA400(t *testing.T) {
	t.Parallel()
	doc, err := openapi.Load(context.Background(), "identity")
	if err != nil {
		t.Fatal(err)
	}
	var undocumented []string
	for _, item := range doc.Paths.Map() {
		for _, op := range item.Operations() {
			if op.RequestBody != nil && op.Responses.Status(http.StatusBadRequest) == nil {
				undocumented = append(undocumented, op.OperationID)
			}
		}
	}
	sort.Strings(undocumented)
	if !slices.Equal(undocumented, bodyWithoutA400) {
		t.Errorf("operations with a request body but no documented 400 = %v, want exactly %v", undocumented, bodyWithoutA400)
	}
}

// TestBodyCap_AnOversizedBodyOnAnOperationWithoutA400 pins what one of
// bodyWithoutA400 answers: the OIDC form-post callback, anonymous, given a
// 2 MiB form body, answers the decode 400 invalid_request. Off-contract by
// design (see bodyWithoutA400); the exchange skips validation.
func TestBodyCap_AnOversizedBodyOnAnOperationWithoutA400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := []byte("state=" + strings.Repeat("a", 2<<20))
	r := h.client(t).do(http.MethodPost, "/api/v1/identity/oidc/callback", nil,
		rawBody("application/x-www-form-urlencoded", body),
		skipContract("postIdentityOidcCallback documents only 302; an undecodable form body answers the decode 400"))
	if r.status != http.StatusBadRequest || r.code() != "invalid_request" {
		t.Errorf("2 MiB form body: status %d code %q, want 400 invalid_request", r.status, r.code())
	}
}

// TestBodyCap_AnAnonymousOversizedLoginIsRefused sends POST /login, an
// anonymous operation, a 2 MiB JSON body: the router's 1 MiB default cap
// stops the decode, and the answer is /login's documented 400
// invalid_request. Without the cap the body decodes and the unknown email
// answers 401 invalid_credentials, which is what a body just under the cap
// still gets.
func TestBodyCap_AnAnonymousOversizedLoginIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	login := func(emailBytes int) *resp {
		body := `{"email":"` + strings.Repeat("a", emailBytes) + `@example.test","password":"a-password"}`
		return h.client(t).do(http.MethodPost, loginPath, nil, rawBody("application/json", []byte(body)))
	}

	if r := login(2 << 20); r.status != http.StatusBadRequest || r.code() != "invalid_request" {
		t.Errorf("2 MiB body: status %d code %q, want 400 invalid_request", r.status, r.code())
	}
	if r := login(1<<20 - 1024); r.status != http.StatusUnauthorized || r.code() != "invalid_credentials" {
		t.Errorf("a body just under 1 MiB: status %d code %q, want 401 invalid_credentials", r.status, r.code())
	}
}
