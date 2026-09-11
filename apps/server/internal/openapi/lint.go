package openapi

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// AccessRule is the grammar of x-vantigo-access.
var AccessRule = regexp.MustCompile(`^(anonymous|session|scim|permission:[a-z]+:[a-z-]+(\+[a-z]+:[a-z-]+)*|policy:(ActiveAccount|SystemAdmin|Owner|OwnerManagement|Business|AuthorizationManagement)(\+(ActiveAccount|SystemAdmin|Owner|OwnerManagement|Business|AuthorizationManagement))*)$`)

// Problem is one structural rule an operation breaks.
type Problem struct {
	OperationID string
	Message     string
}

// Lint checks the rules every operation must meet: an operationId, a valid
// x-vantigo-access, and a documented success response (see
// hasSuccessResponse).
func Lint(doc *openapi3.T) []Problem {
	var problems []Problem
	for _, op := range operations(doc) {
		where := strings.ToUpper(op.Method) + " " + op.Path
		if op.OperationID == "" {
			problems = append(problems, Problem{"", where + ": no operationId"})
		}
		access, _ := op.Op.Extensions["x-vantigo-access"].(string)
		if !AccessRule.MatchString(access) {
			problems = append(problems, Problem{op.OperationID, fmt.Sprintf("%s: x-vantigo-access %q is not valid", where, access)})
		} else if !sortedAndUnique(access) {
			problems = append(problems, Problem{op.OperationID, fmt.Sprintf("%s: x-vantigo-access %q must list its names sorted and without duplicates", where, access)})
		}
		if !hasSuccessResponse(op.Op) {
			problems = append(problems, Problem{op.OperationID, where + ": no 2xx response with a body schema, 204, or 3xx with a Location header"})
		}
	}
	return problems
}

// sortedAndUnique reports whether a permission: or policy: access value lists
// its names in strictly ascending order — sorted, no duplicates — which the
// grammar regex cannot say. Other values have no list and always pass.
func sortedAndUnique(access string) bool {
	kind, list, _ := strings.Cut(access, ":")
	if kind != "permission" && kind != "policy" {
		return true
	}
	names := strings.Split(list, "+")
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			return false
		}
	}
	return true
}

// hasSuccessResponse reports whether op documents how it succeeds: a 2xx with
// a body schema, a 204, a 2xx explicitly marked `x-vantigo-empty-body: true`
// (the handler returns Ok() with no value — marked, so an uncurated bare
// "200 OK" still fails), or — for redirect endpoints such as the OIDC flow —
// a 3xx that declares its Location header.
func hasSuccessResponse(op *openapi3.Operation) bool {
	if op.Responses == nil {
		return false
	}
	for code, ref := range op.Responses.Map() {
		if ref == nil || ref.Value == nil {
			continue
		}
		switch {
		case code == "204":
			return true
		case strings.HasPrefix(code, "2"):
			if len(ref.Value.Content) == 0 && ref.Value.Extensions["x-vantigo-empty-body"] == true {
				return true
			}
			for _, media := range ref.Value.Content {
				if media != nil && media.Schema != nil {
					return true
				}
			}
		case strings.HasPrefix(code, "3"):
			if _, ok := ref.Value.Headers["Location"]; ok {
				return true
			}
		}
	}
	return false
}
