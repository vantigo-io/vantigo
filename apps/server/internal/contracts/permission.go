// Package contracts holds the runtime types every module and the router
// share to enforce the API contract's x-vantigo-access rules: the permission
// catalog, the parsed access rule, the authenticated principal, and the
// Access interface identity implements and every module's router calls.
package contracts

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Permission is one catalog entry, "module:verb". Category groups entries
// for display (.NET's PermissionDescriptor.Category); the module a key
// belongs to is its prefix, which ValidatePermission ties to its module.
type Permission struct {
	Key         string
	Display     string
	Description string
	Category    string
	Sensitive   bool
	Delegable   bool
}

// permissionKey is the contract's permission grammar (see
// internal/openapi.AccessRule's permission: clause), not the broader
// [a-z0-9_-] the brief sketches: every real key already fits this, and a key
// the contract cannot express in x-vantigo-access could never be required by
// a route.
var permissionKey = regexp.MustCompile(`^[a-z]+:[a-z-]+$`)

// ValidatePermission checks the key grammar, a display, description and
// category that are not blank (empty or whitespace only), and that the
// key's module prefix (before ":") equals module, as .NET's
// PermissionCatalog.Create validated each descriptor with IsNullOrWhiteSpace
// (packages/contracts/Vantigo.Contracts/Authorization/PermissionCatalog.cs:79-93).
func ValidatePermission(module string, p Permission) error {
	if !permissionKey.MatchString(p.Key) {
		return fmt.Errorf("contracts: permission key %q is not of the form module:verb", p.Key)
	}
	prefix, _, _ := strings.Cut(p.Key, ":")
	if prefix != module {
		return fmt.Errorf("contracts: permission key %q does not start with module %q", p.Key, module)
	}
	if strings.TrimSpace(p.Display) == "" {
		return fmt.Errorf("contracts: permission %q needs a non-blank display", p.Key)
	}
	if strings.TrimSpace(p.Description) == "" {
		return fmt.Errorf("contracts: permission %q needs a non-blank description", p.Key)
	}
	if strings.TrimSpace(p.Category) == "" {
		return fmt.Errorf("contracts: permission %q needs a non-blank category", p.Key)
	}
	return nil
}

// RuleKind is the shape one x-vantigo-access value takes.
type RuleKind uint8

const (
	RuleAnonymous RuleKind = iota
	RuleSession
	RuleSCIM
	RulePolicy
	RulePermission
)

// policyNames are the policy names x-vantigo-access's policy: clause may
// list, sorted — the same set internal/openapi.AccessRule accepts.
var policyNames = []string{"ActiveAccount", "AuthorizationManagement", "Business", "Owner", "OwnerManagement", "SystemAdmin"}

// ruleGrammar duplicates internal/openapi.AccessRule's grammar rather than
// importing it: contracts is a leaf package other modules and the router
// depend on, and this pins the two together instead (see
// TestParseRule_AgreesWithOpenAPIAccessRule).
var ruleGrammar = regexp.MustCompile(`^(anonymous|session|scim|permission:[a-z]+:[a-z-]+(\+[a-z]+:[a-z-]+)*|policy:(` +
	strings.Join(policyNames, "|") + `)(\+(` + strings.Join(policyNames, "|") + `))*)$`)

// Rule is a parsed x-vantigo-access value. Names are the policies or
// permission keys, in the contract's (sorted) order.
type Rule struct {
	Kind  RuleKind
	Names []string
	// Station says whether Check may answer a shared shop-floor station's
	// operator (manufacturing phase 2 M134, M135). Only the router's wrap sets
	// it, from the operation's x-vantigo-station; the zero value refuses
	// station credentials as unauthenticated, so a handler's own Check, a
	// platform endpoint or the docs handler never admits a station.
	Station StationMode
	// Agent says whether an agent grant may meet a session rule (purchasing
	// delivery 0): the operation is marked x-vantigo-agent: allowed, and its
	// handler checks the permissions itself. Only the router's wrap sets it;
	// the zero value refuses an agent every session rule.
	Agent bool
	// API says whether an integration's API key may meet the rule (API access
	// design K5): the operation's x-vantigo-api mark. Only the router's wrap
	// sets it from the contract, and HasPermissions for a key's handler-side
	// question; the zero value refuses a key, so a platform endpoint or the
	// docs handler never admits one unless it says so.
	API APIMode
}

// APIMode is Rule.API.
type APIMode uint8

const (
	// APIRefused: an API key may not meet the rule (403 api_not_available).
	APIRefused APIMode = iota
	// APIAllowed: an API key may meet the rule, but a read-only key may not
	// (403 api_key_read_only).
	APIAllowed
	// APIRead: an API key may meet the rule, a read-only key included.
	APIRead
)

// APIExtension is the operation extension that says whether an integration's
// API key may call the operation (API access design K5): allowed, read (a
// read-only key may call it too; every GET and HEAD is one without the mark),
// or never. Absent means allowed, or read for a GET or HEAD.
const (
	APIExtension        = "x-vantigo-api"
	APIExtensionAllowed = "allowed"
	APIExtensionRead    = "read"
	APIExtensionNever   = "never"
)

// APINever is the permissions an API key never holds, whatever its
// integration's roles grant (API access design K7): every identity: and
// agents: key, and every permission that cannot be delegated, which is the
// Owner tier. It is computed from the composed catalog, so a new
// non-delegable permission joins it without an edit. The answer is sorted.
func APINever(catalog []Permission) []string {
	var never []string
	for _, p := range catalog {
		if !p.Delegable || strings.HasPrefix(p.Key, "identity:") || strings.HasPrefix(p.Key, "agents:") {
			never = append(never, p.Key)
		}
	}
	slices.Sort(never)
	return slices.Compact(never)
}

// StationMode is Rule.Station.
type StationMode uint8

const (
	// StationRefused: station credentials are not a caller at all (401).
	StationRefused StationMode = iota
	// StationOutOfScope: the router's operation is not open to stations; a
	// valid station's operator is ErrStationScope (403 station_scope).
	StationOutOfScope
	// StationAllowed: the operation is marked x-vantigo-station: allowed; a
	// station's operator is answered with the permissions intersected.
	StationAllowed
)

// StationExtension is the operation extension that opens an operation to a
// shared station's operators; its one value is StationExtensionAllowed.
const (
	StationExtension        = "x-vantigo-station"
	StationExtensionAllowed = "allowed"
)

// AgentExtension is the operation extension that opens an operation whose
// rule is session to an agent grant, because its handler checks the
// permissions an agent's roles must hold; its one value is
// AgentExtensionAllowed.
const (
	AgentExtension        = "x-vantigo-agent"
	AgentExtensionAllowed = "allowed"
)

// ParseRule parses s. It accepts exactly what internal/openapi.AccessRule
// accepts; it does not itself require Names to be sorted or unique, since
// that is a contract-authoring rule (internal/openapi.Lint), not a runtime
// one.
func ParseRule(s string) (Rule, error) {
	if !ruleGrammar.MatchString(s) {
		return Rule{}, fmt.Errorf("contracts: %q is not a valid x-vantigo-access value", s)
	}
	switch s {
	case "anonymous":
		return Rule{Kind: RuleAnonymous}, nil
	case "session":
		return Rule{Kind: RuleSession}, nil
	case "scim":
		return Rule{Kind: RuleSCIM}, nil
	}
	kind, list, _ := strings.Cut(s, ":")
	names := strings.Split(list, "+")
	if kind == "policy" {
		return Rule{Kind: RulePolicy, Names: names}, nil
	}
	return Rule{Kind: RulePermission, Names: names}, nil // "permission": the only other grammar branch
}

// String reconstructs the x-vantigo-access value r was parsed from (or an
// equivalent one, for a Rule built directly rather than parsed).
func (r Rule) String() string {
	switch r.Kind {
	case RuleAnonymous:
		return "anonymous"
	case RuleSession:
		return "session"
	case RuleSCIM:
		return "scim"
	case RulePolicy:
		return "policy:" + strings.Join(r.Names, "+")
	case RulePermission:
		return "permission:" + strings.Join(r.Names, "+")
	default:
		return ""
	}
}
