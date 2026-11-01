package openapi

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// AccessRule is the grammar of x-vantigo-access.
var AccessRule = regexp.MustCompile(`^(anonymous|session|scim|permission:[a-z]+:[a-z-]+(\+[a-z]+:[a-z-]+)*|policy:(ActiveAccount|SystemAdmin|Owner|OwnerManagement|Business|AuthorizationManagement)(\+(ActiveAccount|SystemAdmin|Owner|OwnerManagement|Business|AuthorizationManagement))*)$`)

// AssistantExtension is the operation extension that states an exception to
// the mode the assistant would otherwise derive for the operation (the
// Assistant design S4). It is optional.
const AssistantExtension = "x-vantigo-assistant"

// AssistantModes are the values of AssistantExtension: call without asking,
// call only after the person confirms, or never.
var AssistantModes = []string{"auto", "confirm", "never"}

// assistantRefusedPolicies are the policies an assistant grant never
// satisfies (identity refuses them where the rule is evaluated), so an
// operation behind one cannot be marked callable.
var assistantRefusedPolicies = []string{"Owner", "OwnerManagement", "AuthorizationManagement", "SystemAdmin"}

// Problem is one structural rule an operation breaks.
type Problem struct {
	OperationID string
	Message     string
}

// Lint checks the rules every operation must meet: an operationId, a summary
// (the assistant searches by it, the Assistant design S15), a valid
// x-vantigo-access, a valid x-vantigo-assistant when present, and a documented
// success response (see hasSuccessResponse).
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
		if strings.TrimSpace(op.Op.Summary) == "" {
			problems = append(problems, Problem{op.OperationID, where + ": no summary"})
		}
		if value, present := op.Op.Extensions[AssistantExtension]; present {
			mode, _ := value.(string)
			switch {
			case !slices.Contains(AssistantModes, mode):
				problems = append(problems, Problem{op.OperationID, fmt.Sprintf("%s: %s %v must be one of %s", where, AssistantExtension, value, strings.Join(AssistantModes, ", "))})
			case mode != "never" && assistantNeverCallable(access):
				problems = append(problems, Problem{op.OperationID, fmt.Sprintf("%s: %s %s is not possible behind x-vantigo-access %q: an assistant is never admitted to it", where, AssistantExtension, mode, access)})
			}
		}
		if !hasSuccessResponse(op.Op) {
			problems = append(problems, Problem{op.OperationID, where + ": no 2xx response with a body schema, 204, or 3xx with a Location header"})
		}
	}
	return problems
}

// assistantNeverCallable reports whether an assistant grant is refused the
// access rule whatever the person holds: a scim rule, or a policy naming one of
// assistantRefusedPolicies.
func assistantNeverCallable(access string) bool {
	kind, list, _ := strings.Cut(access, ":")
	switch kind {
	case "scim":
		return true
	case "policy":
		return slices.ContainsFunc(strings.Split(list, "+"), func(name string) bool { return slices.Contains(assistantRefusedPolicies, name) })
	}
	return false
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
