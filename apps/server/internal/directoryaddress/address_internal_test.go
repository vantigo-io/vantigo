package directoryaddress

import (
	"errors"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The address rule (Entra design D13, Google Workspace design G18), by
// table: the work email when its domain is verified and its local part folds
// into the alphabet, else the pattern over the folded names at the domain,
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
		if got := UPN.CleanName(tc.in); got != tc.want {
			t.Errorf("CleanName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFor(t *testing.T) {
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
			a, err := UPN.For(tc.spec, tc.pattern, domain, verified)
			if err != nil {
				t.Fatal(err)
			}
			upn, nick, ok := a.Attempt(1)
			if !ok || upn != tc.wantUPN || nick != tc.wantNick || a.FromEmail != tc.wantFromEmail {
				t.Errorf("attempt 1 = %q %q %v (from email %v), want %q %q (from email %v)", upn, nick, ok, a.FromEmail, tc.wantUPN, tc.wantNick, tc.wantFromEmail)
			}
			if len(nick) > 64 {
				t.Errorf("the mail nickname is %d characters", len(nick))
			}
			second, _, ok := a.Attempt(2)
			if tc.wantSecond == "" {
				if ok {
					t.Errorf("attempt 2 = %q, want none for a work email", second)
				}
				return
			}
			if !ok || second != tc.wantSecond {
				t.Errorf("attempt 2 = %q %v, want %q", second, ok, tc.wantSecond)
			}
			if last, _, ok := a.Attempt(99); !ok || !strings.Contains(last, "99@") {
				t.Errorf("attempt 99 = %q %v", last, ok)
			}
			if _, _, ok := a.Attempt(100); ok {
				t.Error("attempt 100 answered an address; the suffixes end at 99")
			}
		})
	}
}

func TestFor_NoDomain(t *testing.T) {
	t.Parallel()
	spec := contracts.ExternalSpec{GivenName: "Kari", FamilyName: "Nordmann"}
	if _, err := UPN.For(spec, PatternGivenFamily, "", nil); !errors.Is(err, ErrNoDomain) {
		t.Errorf("no domain at all: %v, want ErrNoDomain", err)
	}
	// A work email on a verified domain needs no domain of the provider's.
	spec.WorkEmail = "kari@nordlys.no"
	a, err := UPN.For(spec, PatternGivenFamily, "", []string{"nordlys.no"})
	if err != nil || !a.FromEmail {
		t.Errorf("the work email without a domain: %+v %v", a, err)
	}
}

// The mailbox alphabet keeps what Google admits and drops what a UPN alone
// may hold.
func TestMailbox(t *testing.T) {
	t.Parallel()
	if got := Mailbox.CleanName("O'Brien!#^~"); got != "O'Brien" {
		t.Errorf("Mailbox.CleanName = %q", got)
	}
	spec := contracts.ExternalSpec{GivenName: "Kari", FamilyName: "Nordmann", WorkEmail: "kari!hr@nordlys.no"}
	a, err := Mailbox.For(spec, PatternGivenFamily, "nordlys.no", []string{"nordlys.no"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := a.Attempt(1); got != "kari.nordmann@nordlys.no" || a.FromEmail {
		t.Errorf("a work email outside the mailbox alphabet: %q (from email %v)", got, a.FromEmail)
	}
	if got, _, _ := mustFor(t, UPN, spec).Attempt(1); got != "kari!hr@nordlys.no" {
		t.Errorf("the same work email under the UPN alphabet: %q", got)
	}
}

func mustFor(t *testing.T, r Rules, spec contracts.ExternalSpec) Address {
	t.Helper()
	a, err := r.For(spec, PatternGivenFamily, "nordlys.no", []string{"nordlys.no"})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestTemporaryPassword(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for range 50 {
		p, err := TemporaryPassword()
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
