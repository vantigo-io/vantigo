package invoices

import (
	"fmt"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
)

// This module's refusals follow the codebase's one rule: 404 for what does
// not exist (a bare body), 403 for what is not theirs to do (the
// access layer's own body), 400 naming the field (apicommon.ValidationProblem)
// and 409 for a rule about the state of things — the settings, the series, a
// document — carrying the rule's code (InvoicesConflictProblem), the way the
// customers module's conflicts carry theirs. A client keys off the code, never
// the words.

// The titles this module's validation problems carry.
const (
	invalidSettingsTitle = "Invalid invoice settings"
	invalidVatCodeTitle  = "Invalid VAT code"
	invalidRateTitle     = "Invalid VAT rate"
)

// withFieldError adds one message to a map another rule may already have put
// something in. A nil map is the "nothing failed yet" case, so it is grown
// rather than written to.
func withFieldError(errs map[string][]string, field, message string) map[string][]string {
	if errs == nil {
		errs = map[string][]string{}
	}
	errs[field] = append(errs[field], message)
	return errs
}

// fieldError is the one-field error map, for a rule decided on its own.
func fieldError(field, message string) map[string][]string {
	return map[string][]string{field: {message}}
}

// conflict is a 409 carrying the rule's code and a sentence for a person.
func conflict(code, title, detail string) gen.InvoicesConflictProblem {
	status := int32(http.StatusConflict)
	return gen.InvoicesConflictProblem{Code: &code, Title: &title, Detail: &detail, Status: &status}
}

// revisionConflict is the 409 an update carrying a stale revision answers. It
// names both revisions, so a client can tell "somebody else saved" from "I
// sent the wrong number", and carries no code: it is not a rule of this
// module's but the codebase's (docs/src/content/docs/en/reference/expenses.md).
func revisionConflict(what string, current, supplied int32) gen.InvoicesConflictProblem {
	title := what + " revision conflict"
	detail := fmt.Sprintf("The %s has revision %d; the supplied revision was %d.", what, current, supplied)
	status := int32(http.StatusConflict)
	return gen.InvoicesConflictProblem{Title: &title, Detail: &detail, Status: &status}
}

// invalid is the 400 body for a request whose fields did not pass.
func invalid(title string, errs map[string][]string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem(title, errs)
}
