package inventory

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// This module's refusals follow the codebase's rule: 404 for what does not
// exist (a bare body), 403 for what is not the caller's to do (the access
// layer's own body), 400 naming the field or the line, and 409 for a rule
// about the state of things — every one an RFC 9457 problem in one shape,
// gen.InventoryProblem, carrying the rule's code. A client keys off the code,
// never the words. The design's §4 gave its field rules 422 and a stale
// If-Match 412; the house answers them 400 and 409, and so does this module.

// The refusal codes (design §4).
const (
	codeSiteCodeTaken       = "site_code_taken"
	codeLocationCodeTaken   = "location_code_taken"
	codeFullCodeTaken       = "full_code_taken"
	codeCodeInvalid         = "code_invalid"
	codeDepthExceeded       = "depth_exceeded"
	codeParentInvalid       = "parent_invalid"
	codeBulkTooLarge        = "bulk_too_large"
	codeLocationHasStock    = "location_has_stock"
	codeLocationHasHistory  = "location_has_history"
	codeRootLocation        = "root_location"
	codeLocationArchived    = "location_archived"
	codeLocationNotFound    = "location_not_found"
	codeVariantNotFound     = "variant_not_found"
	codeVariantNotStockable = "variant_not_stockable"
	codeVariantDiscontinued = "variant_discontinued"
	codeMoveCrossesSites    = "move_crosses_sites"
	codeSameLocation        = "same_location"
	codeInsufficientStock   = "insufficient_stock"
	codeQuantityInvalid     = "quantity_invalid"
	codeNothingToPost       = "nothing_to_post"
	codeTooManyLines        = "too_many_lines"
	codeClientRequestReused = "client_request_reused"
	codeRevisionConflict    = "revision_conflict"
	codeDependencyUnavail   = "dependency_unavailable"
)

// The titles this module's validation problems carry, one per thing a
// request writes.
const (
	invalidSiteTitle     = "Invalid site"
	invalidLocationTitle = "Invalid location"
	invalidBulkTitle     = "Invalid bulk create"
	invalidPostingTitle  = "Invalid movement"
	invalidQueryTitle    = "Invalid query"
)

// errDependencyUnavailable is every accessor's answer when the contract it
// reads is not composed: config makes that impossible in a running
// installation, and a handler answers it as the 503 dependency_unavailable.
var errDependencyUnavailable = errors.New("inventory: a module this one reads is not composed")

// fieldErrors is a validation pass's outcome: the field errors and, when one
// of them is a rule the design names, its code. The first coded rule to fail
// gives the code; the field errors carry every failure.
type fieldErrors struct {
	errs map[string][]string
	code string
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

// addCoded records message on field as the coded rule code.
func (f *fieldErrors) addCoded(field, code, message string) {
	if message == "" {
		return
	}
	f.add(field, message)
	if f.code == "" {
		f.code = code
	}
}

// merge adds every failure of o to f, o's code if f has none.
func (f *fieldErrors) merge(o fieldErrors) {
	for field, messages := range o.errs {
		for _, m := range messages {
			f.add(field, m)
		}
	}
	if f.code == "" {
		f.code = o.code
	}
}

// failed reports whether any rule failed.
func (f *fieldErrors) failed() bool { return len(f.errs) > 0 }

// problem is a problem body with a status, a code, a title and a detail; an
// empty code or detail is left out.
func problem(status int, code, title, detail string) gen.InventoryProblem {
	s := int32(status)
	p := gen.InventoryProblem{Title: &title, Status: &s}
	if code != "" {
		p.Code = &code
	}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}

// invalid is the 400 body for a request whose fields did not pass.
func invalid(title string, f fieldErrors) gen.InventoryProblem {
	p := problem(http.StatusBadRequest, f.code, title, "")
	p.Errors = &f.errs
	return p
}

// lineFailure is one failing line of a posting (§4: all of them at once).
type lineFailure struct {
	Index  int
	Code   string
	Detail string
}

// linesProblem is a posting's refusal naming every failing line: 409 when
// any line is insufficient_stock — the state of things — and 400 otherwise.
// The problem's own code is the first line's.
func linesProblem(failures []lineFailure) (int, gen.InventoryProblem) {
	status := http.StatusBadRequest
	lines := make([]gen.InventoryProblemLine, 0, len(failures))
	for _, l := range failures {
		if l.Code == codeInsufficientStock {
			status = http.StatusConflict
		}
		lines = append(lines, gen.InventoryProblemLine{Index: int32(l.Index), Code: l.Code, Detail: l.Detail})
	}
	code := ""
	if len(failures) > 0 {
		code = failures[0].Code
		if status == http.StatusConflict {
			code = codeInsufficientStock
		}
	}
	p := problem(status, code, "The movement was not posted", fmt.Sprintf("%d line(s) failed; none was posted.", len(failures)))
	p.Lines = &lines
	return status, p
}

// revisionConflict is the 409 a change carrying a stale If-Match answers.
func revisionConflict(what string, current, supplied int32) gen.InventoryProblem {
	return problem(http.StatusConflict, codeRevisionConflict, "Revision conflict",
		fmt.Sprintf("The %s has revision %d; the supplied revision was %d.", what, current, supplied))
}

// dependencyUnavailable is the 503 an operation answers when the catalog is
// not composed.
func dependencyUnavailable() gen.InventoryProblem {
	return problem(http.StatusServiceUnavailable, codeDependencyUnavail, "Products is unavailable",
		"Inventory reads the product catalog, which is not running in this installation. Nothing was changed.")
}

// refusal is a write's answer that is not a success, returned from inside its
// transaction so everything the write did rolls back, and turned into the
// operation's own typed response by the handler (answerOf).
type refusal struct {
	status  int
	problem gen.InventoryProblem
}

func (r *refusal) Error() string { return fmt.Sprintf("inventory: refused with %d", r.status) }

// notFound is the bare 404 of an unknown id.
func notFound() error { return &refusal{status: http.StatusNotFound} }

// refuse is a refusal with p's status.
func refuse(p gen.InventoryProblem) error {
	status := http.StatusBadRequest
	if p.Status != nil {
		status = int(*p.Status)
	}
	return &refusal{status: status, problem: p}
}

// refuseInvalid is a 400 carrying the field errors under title.
func refuseInvalid(title string, f fieldErrors) error {
	return &refusal{status: http.StatusBadRequest, problem: invalid(title, f)}
}

// refuseLines is a posting's refusal naming every failing line.
func refuseLines(failures []lineFailure) error {
	status, p := linesProblem(failures)
	return &refusal{status: status, problem: p}
}

// answerOf is how a handler reads an error: a refusal's status and body,
// errDependencyUnavailable as the 503, and ok false for anything else, which
// the handler returns as the 500 it is.
func answerOf(err error) (int, gen.InventoryProblem, bool) {
	var r *refusal
	if errors.As(err, &r) {
		return r.status, r.problem, true
	}
	if errors.Is(err, errDependencyUnavailable) {
		return http.StatusServiceUnavailable, dependencyUnavailable(), true
	}
	return 0, gen.InventoryProblem{}, false
}
