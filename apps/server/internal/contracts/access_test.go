package contracts_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// validRules are examples drawn from internal/openapi.AccessRule's grammar:
// the three bare keywords, permission: with one or several module:verb
// clauses, and policy: with one or several of the six policy names.
var validRules = []string{
	"anonymous",
	"session",
	"scim",
	"permission:identity:manage-access",
	"permission:identity:manage-access+identity:view-audit",
	"permission:customers:create",
	"policy:ActiveAccount",
	"policy:SystemAdmin",
	"policy:ActiveAccount+SystemAdmin",
	"policy:AuthorizationManagement+Business+Owner+OwnerManagement+SystemAdmin",
}

// invalidRules are near misses: wrong case, a missing clause, an unknown
// policy name, a trailing separator, and the empty string.
var invalidRules = []string{
	"",
	"Anonymous",
	"anonymous ",
	"permission:Identity:manage-access",
	"permission:identity",
	"permission:identity:manage-access+",
	"policy:Nonexistent",
	"policy:ActiveAccount+",
	"policy:activeaccount",
	"identity:manage-access",
}

// ParseRule round-trips every example from internal/openapi.AccessRule's
// grammar and rejects the invalid ones.
func TestParseRule(t *testing.T) {
	for _, s := range validRules {
		t.Run(s, func(t *testing.T) {
			rule, err := contracts.ParseRule(s)
			if err != nil {
				t.Fatalf("ParseRule(%q): %v", s, err)
			}
			if got := rule.String(); got != s {
				t.Errorf("round trip: ParseRule(%q).String() = %q", s, got)
			}
		})
	}
	for _, s := range invalidRules {
		t.Run(s, func(t *testing.T) {
			if _, err := contracts.ParseRule(s); err == nil {
				t.Errorf("ParseRule(%q): want an error, got nil", s)
			}
		})
	}
}

// contracts duplicates internal/openapi.AccessRule's grammar (see the
// comment on ruleGrammar) rather than importing it, so this test pins the
// two together on the same table of examples: ParseRule must agree with
// openapi.AccessRule on every one, valid or not.
func TestParseRule_AgreesWithOpenAPIAccessRule(t *testing.T) {
	for _, s := range append(append([]string{}, validRules...), invalidRules...) {
		wantValid := openapi.AccessRule.MatchString(s)
		_, err := contracts.ParseRule(s)
		gotValid := err == nil
		if gotValid != wantValid {
			t.Errorf("%q: openapi.AccessRule accepts=%v, contracts.ParseRule accepts=%v", s, wantValid, gotValid)
		}
	}
}

func TestValidatePermission(t *testing.T) {
	valid := contracts.Permission{Key: "identity:manage-access", Display: "Manage access", Description: "Grant and revoke roles."}
	if err := contracts.ValidatePermission("identity", valid); err != nil {
		t.Fatalf("valid permission rejected: %v", err)
	}

	cases := []struct {
		name   string
		module string
		perm   contracts.Permission
	}{
		{"empty key", "identity", contracts.Permission{Key: "", Display: "d", Description: "d"}},
		{"uppercase in key", "identity", contracts.Permission{Key: "identity:Manage", Display: "d", Description: "d"}},
		{"underscore in key", "identity", contracts.Permission{Key: "identity:manage_access", Display: "d", Description: "d"}},
		{"digit in key", "identity", contracts.Permission{Key: "identity:manage2", Display: "d", Description: "d"}},
		{"no verb", "identity", contracts.Permission{Key: "identity", Display: "d", Description: "d"}},
		{"wrong prefix", "customers", contracts.Permission{Key: "identity:manage-access", Display: "d", Description: "d"}},
		{"empty display", "identity", contracts.Permission{Key: "identity:manage-access", Display: "", Description: "d"}},
		{"empty description", "identity", contracts.Permission{Key: "identity:manage-access", Display: "d", Description: ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := contracts.ValidatePermission(c.module, c.perm); err == nil {
				t.Errorf("ValidatePermission(%q, %+v): want an error, got nil", c.module, c.perm)
			}
		})
	}
}
