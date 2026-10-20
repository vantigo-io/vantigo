package entra

import (
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The address rule (Entra design D13, plan task 4's address.go), by table:
// the work email when its domain is verified and its local part folds into
// the UPN alphabet, else the pattern over the folded names at upn_domain,
// suffixed 2 to 99 on a conflict.

func TestFoldName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"Ørjan", "Orjan"},
		{"Åsmundsen", "Asmundsen"},
		{"ørjan åsmundsen", "orjanasmundsen"},
		{"Ærlig", "AErlig"},
		{"blæbær", "blaebaer"},
		{"Bjørnstjerne", "Bjornstjerne"},
		{"Renée", "Renee"},
		{"Çelik-Øye", "Celik-Oye"},
		{"Zoë", "Zoe"},
		{"Anne Marie", "AnneMarie"},
		{"O'Brien", "O'Brien"},
		{"a+b@c", "abc"},
		{"李", ""},
		{"Łukasz", "Lukasz"},
		{"Straße", "Strasse"},
	} {
		if got := cleanName(tc.in); got != tc.want {
			t.Errorf("cleanName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAddressFor(t *testing.T) {
	t.Parallel()
	verified := []string{"nordlys.no", "Nordlys.onmicrosoft.com"}
	domain := "nordlys.no"
	for _, tc := range []struct {
		name              string
		spec              contracts.ExternalSpec
		pattern           string
		wantUPN, wantNick string
		wantFromEmail     bool
		wantSecond        string // the second attempt, "" when there is none
	}{
		{
			name:    "the Norwegian letters folded",
			spec:    contracts.ExternalSpec{GivenName: "Ørjan", FamilyName: "Åsmundsen"},
			pattern: "given.family", wantUPN: "orjan.asmundsen@nordlys.no", wantNick: "orjan.asmundsen",
			wantSecond: "orjan.asmundsen2@nordlys.no",
		},
		{
			name:    "g.family",
			spec:    contracts.ExternalSpec{GivenName: "Æsa", FamilyName: "Blæstad"},
			pattern: "g.family", wantUPN: "a.blaestad@nordlys.no", wantNick: "a.blaestad",
			wantSecond: "a.blaestad2@nordlys.no",
		},
		{
			name:    "given",
			spec:    contracts.ExternalSpec{GivenName: "Renée", FamilyName: "Lund"},
			pattern: "given", wantUPN: "renee@nordlys.no", wantNick: "renee",
			wantSecond: "renee2@nordlys.no",
		},
		{
			name:    "the work email on a verified domain",
			spec:    contracts.ExternalSpec{GivenName: "Kari", FamilyName: "Nordmann", WorkEmail: "Kari.Nordmann@Nordlys.no"},
			pattern: "given.family", wantUPN: "kari.nordmann@nordlys.no", wantNick: "kari.nordmann", wantFromEmail: true,
		},
		{
			name:    "the work email whose local part folds",
			spec:    contracts.ExternalSpec{GivenName: "Ørjan", FamilyName: "Å", WorkEmail: "ørjan.å@nordlys.no"},
			pattern: "given.family", wantUPN: "orjan.a@nordlys.no", wantNick: "orjan.a", wantFromEmail: true,
		},
		{
			name:    "the work email on another verified domain",
			spec:    contracts.ExternalSpec{GivenName: "Kari", FamilyName: "Nordmann", WorkEmail: "kari@nordlys.onmicrosoft.com"},
			pattern: "given.family", wantUPN: "kari@nordlys.onmicrosoft.com", wantNick: "kari", wantFromEmail: true,
		},
		{
			name:    "the work email on an unverified domain",
			spec:    contracts.ExternalSpec{GivenName: "Kari", FamilyName: "Nordmann", WorkEmail: "kari@nordlys.example"},
			pattern: "given.family", wantUPN: "kari.nordmann@nordlys.no", wantNick: "kari.nordmann",
			wantSecond: "kari.nordmann2@nordlys.no",
		},
		{
			name:    "the work email outside the alphabet",
			spec:    contracts.ExternalSpec{GivenName: "Kari", FamilyName: "Nordmann", WorkEmail: "kari+hr@nordlys.no"},
			pattern: "given.family", wantUPN: "kari.nordmann@nordlys.no", wantNick: "kari.nordmann",
			wantSecond: "kari.nordmann2@nordlys.no",
		},
		{
			name:    "names that fold to nothing",
			spec:    contracts.ExternalSpec{GivenName: "李", FamilyName: "王"},
			pattern: "given.family", wantUPN: "user@nordlys.no", wantNick: "user",
			wantSecond: "user2@nordlys.no",
		},
		{
			name:    "a double name and an apostrophe",
			spec:    contracts.ExternalSpec{GivenName: "Anne-Marie", FamilyName: "O'Brien Dahl"},
			pattern: "given.family", wantUPN: "anne-marie.o'briendahl@nordlys.no", wantNick: "anne-marie.o'briendahl",
			wantSecond: "anne-marie.o'briendahl2@nordlys.no",
		},
		{
			name:    "a long name kept within 64",
			spec:    contracts.ExternalSpec{GivenName: strings.Repeat("a", 50), FamilyName: strings.Repeat("b", 50)},
			pattern: "given.family", wantUPN: strings.Repeat("a", 50) + "." + strings.Repeat("b", 11) + "@nordlys.no",
			wantNick:   strings.Repeat("a", 50) + "." + strings.Repeat("b", 11),
			wantSecond: strings.Repeat("a", 50) + "." + strings.Repeat("b", 11) + "2@nordlys.no",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := addressFor(tc.spec, tc.pattern, &domain, verified)
			if err != nil {
				t.Fatal(err)
			}
			upn, nick, ok := a.attempt(1)
			if !ok || upn != tc.wantUPN || nick != tc.wantNick || a.fromEmail != tc.wantFromEmail {
				t.Errorf("attempt 1 = %q %q %v (from email %v), want %q %q (from email %v)", upn, nick, ok, a.fromEmail, tc.wantUPN, tc.wantNick, tc.wantFromEmail)
			}
			if len(nick) > 64 {
				t.Errorf("the mail nickname is %d characters", len(nick))
			}
			second, _, ok := a.attempt(2)
			if tc.wantSecond == "" {
				if ok {
					t.Errorf("attempt 2 = %q, want none for a work email", second)
				}
				return
			}
			if !ok || second != tc.wantSecond {
				t.Errorf("attempt 2 = %q %v, want %q", second, ok, tc.wantSecond)
			}
			if last, _, ok := a.attempt(99); !ok || !strings.Contains(last, "99@") {
				t.Errorf("attempt 99 = %q %v", last, ok)
			}
			if _, _, ok := a.attempt(100); ok {
				t.Error("attempt 100 answered an address; the suffixes end at 99")
			}
		})
	}
}

func TestAddressFor_TheDomain(t *testing.T) {
	t.Parallel()
	spec := contracts.ExternalSpec{GivenName: "Kari", FamilyName: "Nordmann"}
	// No upn_domain: the first verified domain that is not the initial one.
	a, err := addressFor(spec, "given.family", nil, []string{"nordlys.onmicrosoft.com", "Nordlys.no"})
	if err != nil {
		t.Fatal(err)
	}
	if upn, _, _ := a.attempt(1); upn != "kari.nordmann@nordlys.no" {
		t.Errorf("without upn_domain: %q", upn)
	}
	if _, err := addressFor(spec, "given.family", nil, nil); err == nil {
		t.Error("no domain at all made an address")
	}
}

func TestTemporaryPassword(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for range 50 {
		p, err := temporaryPassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != 32 {
			t.Fatalf("a password of %d characters", len(p))
		}
		var upper, lower, digit, symbol bool
		for _, r := range p {
			switch {
			case r >= 'A' && r <= 'Z':
				upper = true
			case r >= 'a' && r <= 'z':
				lower = true
			case r >= '0' && r <= '9':
				digit = true
			case strings.ContainsRune("!#%+-", r):
				symbol = true
			default:
				t.Fatalf("a character outside the alphabet: %q", r)
			}
		}
		if !upper || !lower || !digit || !symbol {
			t.Fatalf("a password missing a class: %v %v %v %v", upper, lower, digit, symbol)
		}
		if seen[p] {
			t.Fatal("the same password twice")
		}
		seen[p] = true
	}
}
