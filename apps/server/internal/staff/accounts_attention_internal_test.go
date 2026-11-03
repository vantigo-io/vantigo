package staff

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
	"time"

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
// Staff's own manager_not_in_directory raises its item — a licence's whatever
// its SKU — but pass_not_allowed, which the Account tab says beside the
// pass; and every status the worker records raises its installation item.
func TestAccountAttention_EveryProviderCodeIsMapped(t *testing.T) {
	for code, want := range map[string]gen.StaffStatsAttentionItemType{
		contracts.ReportedHireDateNotWritten:                 gen.HireDateNotWritten,
		contracts.ReportedPrivileged:                         gen.AccountPrivilegedInDirectory,
		contracts.ReportedLicenceUnavailable:                 gen.LicenceUnavailable,
		contracts.ReportedLicenceUnavailable + ":ENTERPRISE": gen.LicenceUnavailable,
		contracts.ReportedManagerNotFound:                    gen.ManagerNotInDirectory,
		contracts.ReportedPhotoNotWritten:                    gen.PhotoNotWritten,
		codeManagerNotInDirectory:                            gen.ManagerNotInDirectory,
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
		{contracts.ProviderUnconfigured, "", gen.ProviderNotConnected},
		{contracts.ProviderUnconfigured, "provider_missing", gen.ProviderNotConnected},
		{contracts.ProviderFailing, "credential_invalid", gen.ProviderFailing},
		{contracts.ProviderFailing, "unreachable", gen.ProviderFailing},
		{contracts.ProviderFailing, "secret_expired", gen.SecretExpired},
		{contracts.ProviderConnected, "secret_expiring", gen.SecretExpiring},
	} {
		if got, ok := providerStatusItem(c.status, c.reason); !ok || got != c.want {
			t.Errorf("status %s/%s raises %q (%v), want %q", c.status, c.reason, got, ok, c.want)
		}
	}
	if got, ok := providerStatusItem(contracts.ProviderConnected, ""); ok {
		t.Errorf("a connected provider raises %q, want none", got)
	}
}

// The provider items about the installation are one per provider (Google
// design G6): id <type>/<provider key>, titled with the provider's recorded
// name — its key when none is recorded — and raised only while people whose
// flag is on have the provider's kind, counting them.
func TestAccountAttention_ProviderItemsArePerProvider(t *testing.T) {
	checked := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	a := accountAttention{
		providers: map[string]providerStatusSeen{
			"entra":  {Name: "Microsoft Entra ID", Status: contracts.ProviderFailing, Reason: "credential_invalid", CheckedAt: checked},
			"google": {Name: "Google Workspace", Status: contracts.ProviderFailing, Reason: "unreachable", CheckedAt: checked},
			"okta":   {Status: contracts.ProviderUnconfigured, Reason: "provider_missing", CheckedAt: checked},
			"quiet":  {Name: "Quiet", Status: contracts.ProviderUnconfigured, CheckedAt: checked},
		},
		kinds: map[string]int64{"entra": 2, "google": 1, "okta": 3},
	}
	got := a.providerItems()
	want := []struct {
		id, title string
		kind      gen.StaffStatsAttentionItemType
		count     int32
	}{
		{"providerFailing/entra", "Microsoft Entra ID", gen.ProviderFailing, 2},
		{"providerFailing/google", "Google Workspace", gen.ProviderFailing, 1},
		{"providerNotConnected/okta", "okta", gen.ProviderNotConnected, 3},
	}
	if len(got) != len(want) {
		t.Fatalf("provider items = %+v, want %d", got, len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Id != w.id || g.Title != w.title || g.Type != w.kind || g.Count == nil || *g.Count != w.count ||
			g.EntityId != settingsEntity || !g.OccurredAt.Equal(checked) {
			t.Errorf("item %d = %+v (count %v), want %+v", i, g, g.Count, w)
		}
	}
}
