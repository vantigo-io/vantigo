package google

import (
	"fmt"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/google/gen"
)

// This module's refusals (Google Workspace design G13) are RFC 9457 problems
// in one shape, gen.GoogleProblem: field errors on the body (400), keyed by
// the request's camelCase field; a bare 404 for an unknown credential; a 412
// or a 422 carrying one of the codes below. The 401, the 403 and the 429 are
// the router's own. A client keys off the status and the code, never the
// words.

// The refusal codes.
const (
	// codeVersionMismatch is a PUT whose If-Match version has moved on (412).
	codeVersionMismatch = "version_mismatch"
	// codeClientMismatch is a key for another service account while one of
	// the current account's is live (422): delegation is granted to a client
	// id, so changing account is revoking every key first.
	codeClientMismatch = "client_mismatch"
	// codeCredentialKindUnsupported is a credential kind this version does
	// not implement — vantigo_hosted (422).
	codeCredentialKindUnsupported = "credential_kind_unsupported"
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
func invalid(title string, f fieldErrors) gen.GoogleProblem {
	status := int32(http.StatusBadRequest)
	errs := f.errs
	return gen.GoogleProblem{Title: &title, Status: &status, Errors: &errs}
}

// problem is a refusal of status carrying the rule's code and a sentence for
// a person.
func problem(status int, code, title, detail string) gen.GoogleProblem {
	s := int32(status)
	return gen.GoogleProblem{Code: &code, Title: &title, Detail: &detail, Status: &s}
}

// versionMismatch is the 412 a PUT carrying a stale If-Match answers, naming
// both versions.
func versionMismatch(current, supplied int32) gen.GoogleProblem {
	return problem(http.StatusPreconditionFailed, codeVersionMismatch, "Version mismatch",
		fmt.Sprintf("The connection has version %d; the supplied version was %d.", current, supplied))
}

// clientMismatch is the 422 a key for another service account answers.
func clientMismatch() gen.GoogleProblem {
	return problem(http.StatusUnprocessableEntity, codeClientMismatch, "Another service account",
		"This key belongs to another service account than the keys already added. Domain-wide delegation is granted to one service account's client id: "+
			"to change the service account, revoke every key of the current one first.")
}

// credentialKindUnsupported is the 422 a credential kind this version does
// not implement answers.
func credentialKindUnsupported() gen.GoogleProblem {
	return problem(http.StatusUnprocessableEntity, codeCredentialKindUnsupported, "Credential kind unsupported",
		"This version of Vantigo signs in to Google with a service account key only.")
}

// refusal carries a problem out of a transaction, which it rolls back; the
// handler answers the problem with the status it names.
type refusal struct {
	problem gen.GoogleProblem
}

func (r refusal) Error() string {
	code := ""
	if r.problem.Code != nil {
		code = *r.problem.Code
	}
	return fmt.Sprintf("google: refused %d %s", r.status(), code)
}

// status is r's HTTP status.
func (r refusal) status() int {
	if r.problem.Status == nil {
		return http.StatusBadRequest
	}
	return int(*r.problem.Status)
}
