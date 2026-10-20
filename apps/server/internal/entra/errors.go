package entra

import (
	"fmt"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
)

// This module's refusals (Entra design D15) are RFC 9457 problems in one
// shape, gen.EntraProblem: field errors on the body (400), keyed by the
// request's camelCase field; a bare 404 for an unknown secret; a 409 or a
// 412 carrying one of the codes below; and a 503 when the tenant could not
// be asked. The 401, the 403 and the 429 are the router's own. A client keys
// off the status and the code, never the words.

// The refusal codes.
const (
	// codeConnectionInUse is a change of the tenant or the client while the
	// module keeps accounts, without confirmChange (409).
	codeConnectionInUse = "connection_in_use"
	// codeCredentialInUse is the revoke of the only valid secret while the
	// module keeps accounts (409).
	codeCredentialInUse = "credential_in_use"
	// codeAdoptionConfirmed is the confirmation of an adoption confirmed
	// already, or of an account the module created (409).
	codeAdoptionConfirmed = "adoption_confirmed"
	// codeVersionMismatch is a PUT whose If-Match version has moved on (412).
	codeVersionMismatch = "version_mismatch"
	// codeCredentialInvalid is a call to the tenant no secret could fetch a
	// token for (503).
	codeCredentialInvalid = "credential_invalid"
	// codeTenantUnreachable is a call to the tenant that was not answered
	// (503).
	codeTenantUnreachable = "tenant_unreachable"
)

// fieldErrors is a validation pass's outcome: every failure, by field.
type fieldErrors struct {
	errs map[string][]string
}

// add records message on field; "" records nothing.
func (f *fieldErrors) add(field, message string) {
	if message == "" {
		return
	}
	if f.errs == nil {
		f.errs = map[string][]string{}
	}
	f.errs[field] = append(f.errs[field], message)
}

// failed reports whether any rule failed.
func (f *fieldErrors) failed() bool { return len(f.errs) > 0 }

// invalid is the 400 body for a request whose fields did not pass.
func invalid(title string, f fieldErrors) gen.EntraProblem {
	status := int32(http.StatusBadRequest)
	errs := f.errs
	return gen.EntraProblem{Title: &title, Status: &status, Errors: &errs}
}

// problem is a refusal of status carrying the rule's code and a sentence for
// a person.
func problem(status int, code, title, detail string) gen.EntraProblem {
	s := int32(status)
	return gen.EntraProblem{Code: &code, Title: &title, Detail: &detail, Status: &s}
}

// connectionInUse is the 409 a change of the tenant or the client answers
// while the module keeps accounts and confirmChange is not true. It says
// what confirming costs: the accounts stay in the old tenant, and Vantigo no
// longer disables or deletes them when their people leave.
func connectionInUse(accounts int32) gen.EntraProblem {
	return problem(http.StatusConflict, codeConnectionInUse, "Connection in use",
		fmt.Sprintf("The module keeps %d accounts in the current tenant. Confirm the change to proceed: nothing in the tenant is deleted, "+
			"but the accounts of the current tenant will no longer be disabled or deleted by Vantigo when their people leave.", accounts))
}

// adoptionConfirmed is the 409 the confirmation of an account answers when
// there is nothing to confirm: confirmed already, or created by the module.
func adoptionConfirmed() gen.EntraProblem {
	return problem(http.StatusConflict, codeAdoptionConfirmed, "Adoption confirmed",
		"The account is the module's already: its adoption was confirmed, or the module created it.")
}

// credentialInUse is the 409 the revoke of the only valid secret answers
// while the module keeps accounts.
func credentialInUse() gen.EntraProblem {
	return problem(http.StatusConflict, codeCredentialInUse, "Secret in use",
		"This is the only valid secret and the module keeps accounts in the tenant. Add a new secret before revoking this one.")
}

// versionMismatch is the 412 a PUT carrying a stale If-Match answers. It
// names both versions, so a client can tell "somebody else saved" from "I
// sent the wrong number".
func versionMismatch(current, supplied int32) gen.EntraProblem {
	return problem(http.StatusPreconditionFailed, codeVersionMismatch, "Version mismatch",
		fmt.Sprintf("The connection has version %d; the supplied version was %d.", current, supplied))
}

// tenantUnavailable is the 503 a read from the tenant answers when no
// secret fetched a token (codeCredentialInvalid) or the tenant did not answer
// (codeTenantUnreachable). It carries the code alone: the directory's own
// words are the log's, never the caller's.
func tenantUnavailable(code string) gen.EntraProblem {
	return problem(http.StatusServiceUnavailable, code, "Tenant unavailable",
		"Microsoft Entra ID could not be asked. Test the connection for the reason.")
}
