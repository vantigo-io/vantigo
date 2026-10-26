package staff

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// Every reason the accounts worker writes as a link's blocked_reason — each
// reason* constant of accounts_decide.go and accounts_worker.go, walked from
// the source so a reason added later cannot be forgotten — maps to an
// attention item, or is named as judged by the person query instead.
func TestAccountAttention_EveryWorkerReasonIsMapped(t *testing.T) {
	fset := token.NewFileSet()
	seen := 0
	for _, file := range []string{"accounts_decide.go", "accounts_worker.go", "accounts_provider.go"} {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, name := range spec.Names {
				if !strings.HasPrefix(name.Name, "reason") || i >= len(spec.Values) {
					continue
				}
				lit, ok := spec.Values[i].(*ast.BasicLit)
				if !ok {
					t.Errorf("%s: %s is not a string literal", file, name.Name)
					continue
				}
				reason, _ := strconv.Unquote(lit.Value)
				seen++
				if _, mapped := accountReasonItems[reason]; !mapped {
					t.Errorf("%s: %s (%q) maps to no attention item", file, name.Name, reason)
				}
			}
			return true
		})
	}
	if seen < 25 {
		t.Errorf("walked %d reasons, want every one (at least twenty-five)", seen)
	}
}

// Every code a provider may report on its link (contracts.Reported*) and
// Staff's own manager_not_in_entra raises its item — a licence's whatever
// its SKU — but pass_not_allowed, which the Account tab says beside the
// pass; and every status the worker records raises its installation item.
func TestAccountAttention_EveryProviderCodeIsMapped(t *testing.T) {
	for code, want := range map[string]gen.StaffStatsAttentionItemType{
		contracts.ReportedHireDateNotWritten:                 gen.HireDateNotWritten,
		contracts.ReportedPrivileged:                         gen.AccountPrivilegedInEntra,
		contracts.ReportedLicenceUnavailable:                 gen.LicenceUnavailable,
		contracts.ReportedLicenceUnavailable + ":ENTERPRISE": gen.LicenceUnavailable,
		contracts.ReportedManagerNotFound:                    gen.ManagerNotInEntra,
		contracts.ReportedPhotoNotWritten:                    gen.PhotoNotWritten,
		codeManagerNotInEntra:                                gen.ManagerNotInEntra,
	} {
		if got, ok := providerCodeItem(code); !ok || got != want {
			t.Errorf("code %q raises %q (%v), want %q", code, got, ok, want)
		}
	}
	if got, ok := providerCodeItem(contracts.ReportedPassNotAllowed); ok {
		t.Errorf("pass_not_allowed raises %q, want none", got)
	}
	for _, c := range []struct {
		status, reason string
		want           gen.StaffStatsAttentionItemType
	}{
		{contracts.ProviderUnconfigured, "", gen.EntraNotConnected},
		{contracts.ProviderUnconfigured, "provider_missing", gen.EntraNotConnected},
		{contracts.ProviderFailing, "credential_invalid", gen.EntraFailing},
		{contracts.ProviderFailing, "unreachable", gen.EntraFailing},
		{contracts.ProviderFailing, "secret_expired", gen.SecretExpired},
		{contracts.ProviderConnected, "secret_expiring", gen.SecretExpiring},
	} {
		if got, ok := providerStatusItem(c.status, c.reason); !ok || got != c.want {
			t.Errorf("status %s/%s raises %q (%v), want %q", c.status, c.reason, got, ok, c.want)
		}
		if _, ok := accountInstallationItems[c.want]; !ok {
			t.Errorf("%q has no installation sentence", c.want)
		}
	}
	if got, ok := providerStatusItem(contracts.ProviderConnected, ""); ok {
		t.Errorf("a connected provider raises %q, want none", got)
	}
}
