package operations

import (
	"context"
	"mime"
	"net/http"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// Mode is what the assistant may do with an operation (the Assistant design
// S4).
type Mode string

const (
	// ModeAuto is called without asking the person.
	ModeAuto Mode = "auto"
	// ModeConfirm is called only after the person confirmed it.
	ModeConfirm Mode = "confirm"
	// ModeNever is never called, and not shown to the model at all.
	ModeNever Mode = "never"
)

// assistantExtension is the operation extension that states an exception.
// (internal/openapi owns the name for the lint; this package does not import
// the loader.)
const assistantExtension = "x-vantigo-assistant"

// assistantAPIPrefix is the assistant module's own API.
const assistantAPIPrefix = "/api/v1/assistant/"

// refusedPolicies are the policies an assistant grant never satisfies
// (identity.grantRefusedPolicies draws the same line where the rule is
// evaluated).
var refusedPolicies = []string{"Owner", "OwnerManagement", "AuthorizationManagement", "SystemAdmin"}

// contractMode is S4's three steps without the Sensitive clause (the Assistant
// design S16 moved that clause into the workspace's permission levels),
// computed once per operation at Bind. It is the widest an operation can ever
// be; the gate (Dispatcher.SetGate) can only narrow it.
//
//  1. The operation's x-vantigo-assistant, when it is a valid mode. The
//     extension states exceptions only, and the OpenAPI lint checks its
//     values.
//  2. Otherwise never when the access rule is anonymous, scim or session; or
//     a policy naming Owner, OwnerManagement, AuthorizationManagement or
//     SystemAdmin; or a permission it names is not in the catalog; or
//     it is a file upload or download (a multipart or otherwise non-JSON
//     request body, a success response that is not JSON); or it is the
//     assistant's own API.
//  3. Otherwise auto for GET (and HEAD) and confirm for every other method.
//
// A rule that does not parse is never: the router refuses to mount one, so
// this is only a fail-closed default.
func contractMode(method, path string, op *openapi3.Operation, rule contracts.Rule, ruleOK bool, catalog map[string]contracts.Permission) Mode {
	if explicit, ok := op.Extensions[assistantExtension].(string); ok {
		switch Mode(explicit) {
		case ModeAuto, ModeConfirm, ModeNever:
			return Mode(explicit)
		}
	}

	if !ruleOK || strings.HasPrefix(path, assistantAPIPrefix) {
		return ModeNever
	}
	switch rule.Kind {
	case contracts.RuleAnonymous, contracts.RuleSCIM, contracts.RuleSession:
		return ModeNever
	case contracts.RulePolicy:
		if slices.ContainsFunc(rule.Names, func(name string) bool { return slices.Contains(refusedPolicies, name) }) {
			return ModeNever
		}
	case contracts.RulePermission:
		for _, key := range rule.Names {
			// A key missing from the catalog never gets here (the router refuses
			// to mount it); one that does is not trusted. Whether a permission
			// is Sensitive is the gate's concern, not the contract's.
			if _, ok := catalog[key]; !ok {
				return ModeNever
			}
		}
	}
	if !jsonRequest(op) || !jsonResponse(op) {
		return ModeNever
	}

	if method == http.MethodGet || method == http.MethodHead {
		return ModeAuto
	}
	return ModeConfirm
}

// isJSON reports whether a media type is JSON: application/json, or any
// structured-syntax +json type such as application/problem+json.
func isJSON(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(contentType))
	}
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// jsonRequest is false for an operation whose request body is a file upload:
// it takes a body, and none of its media types is JSON.
func jsonRequest(op *openapi3.Operation) bool {
	if op.RequestBody == nil || op.RequestBody.Value == nil || len(op.RequestBody.Value.Content) == 0 {
		return true
	}
	for contentType := range op.RequestBody.Value.Content {
		if isJSON(contentType) {
			return true
		}
	}
	return false
}

// jsonResponse is false for an operation whose success answer is a file or a
// redirect: it has a 2xx with content and none of it is JSON, or it has no 2xx
// content at all but a 3xx.
func jsonResponse(op *openapi3.Operation) bool {
	if op.Responses == nil {
		return true
	}
	sawContent, sawRedirect := false, false
	for code, ref := range op.Responses.Map() {
		if ref == nil || ref.Value == nil {
			continue
		}
		switch {
		case strings.HasPrefix(code, "2"):
			for contentType := range ref.Value.Content {
				if isJSON(contentType) {
					return true
				}
				sawContent = true
			}
		case strings.HasPrefix(code, "3"):
			sawRedirect = true
		}
	}
	return !sawContent && !sawRedirect
}

// Gate narrows an operation's contract mode for the workspace (the Assistant
// design S16). It is given the operation as Bind computed it — Info.Mode is
// the contract's mode, Info.Permissions the keys its access rule names,
// Info.Sensitive whether any of them is Sensitive — and answers the mode the
// assistant may use it in now. A gate may return a mode narrower than
// info.Mode and never a wider one: the dispatcher keeps the narrower of the
// two, so a buggy gate cannot widen the contract. It is called for every
// operation on every List and Search, so it must be cheap (cache by version).
// A gate that cannot decide answers ModeNever.
type Gate func(ctx context.Context, info Info) Mode

// defaultGate is the dispatcher's gate until SetGate: it fails closed. An
// operation whose access rule names a Sensitive permission is never, which is
// delivery 0's behaviour.
func defaultGate(_ context.Context, info Info) Mode {
	if info.Sensitive {
		return ModeNever
	}
	return info.Mode
}

// narrower is the more restrictive of two modes: never over confirm over auto.
func narrower(a, b Mode) Mode {
	rank := func(m Mode) int {
		switch m {
		case ModeAuto:
			return 0
		case ModeConfirm:
			return 1
		}
		return 2 // never, and any mode nobody knows
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}
