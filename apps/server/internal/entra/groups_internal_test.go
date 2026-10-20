package entra

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The groups below the harness: the reference paths a member and an owner
// are removed by, the mail nickname's folding, and the trait values checked
// as GroupTraits declares them.

func TestMembersRefPath_EndsInTheReference(t *testing.T) {
	group := uuid.MustParse("6d5f3f0e-1d2c-4b3a-9f8e-7a6b5c4d3e2f")
	user := uuid.MustParse("0f1e2d3c-4b5a-4968-8776-655443322110")
	if got, want := membersRefPath(group, user), "/groups/"+group.String()+"/members/"+user.String()+"/$ref"; got != want {
		t.Errorf("membersRefPath = %q, want %q", got, want)
	}
	if got, want := ownersRefPath(group, user), "/groups/"+group.String()+"/owners/"+user.String()+"/$ref"; got != want {
		t.Errorf("ownersRefPath = %q, want %q", got, want)
	}
	// The path stays on Graph and names the reference, whatever the ids.
	for range 50 {
		path := membersRefPath(uuid.New(), uuid.New())
		u, err := graphURL(path)
		if err != nil || !strings.HasSuffix(u.Path, "/$ref") || strings.Count(u.Path, "/") != 6 {
			t.Fatalf("membersRefPath %q: %v %v", path, u, err)
		}
	}
	if got := userRef(user); got != "https://graph.microsoft.com/v1.0/users/"+user.String() {
		t.Errorf("userRef = %q", got)
	}
	if got := servicePrincipalRef(user); got != "https://graph.microsoft.com/v1.0/servicePrincipals/"+user.String() {
		t.Errorf("servicePrincipalRef = %q", got)
	}
}

func TestMailNicknameOf_FoldsToTheAlphabet(t *testing.T) {
	for in, want := range map[string]string{
		"engineering":           "engineering",
		"sales-north--2":        "sales-north--2",
		"Salg & Marked":         "salg-marked",
		"Økonomi/Regnskap":      "okonomi-regnskap",
		"  Ærlig Åpen Bøtte  ":  "aerlig-apen-botte",
		"José's \"team\"":       "jose-s-team",
		"!!!":                   "",
		strings.Repeat("a", 70): strings.Repeat("a", 64),
	} {
		if got := mailNicknameOf(in); got != want {
			t.Errorf("mailNicknameOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseGroupValues_ByTable(t *testing.T) {
	m365 := func(more map[string]any) map[string]any {
		v := map[string]any{"type": "microsoft365"}
		for k, x := range more {
			v[k] = x
		}
		return v
	}
	for _, c := range []struct {
		name   string
		values map[string]any
		field  string
		code   string
	}{
		{"no type", map[string]any{}, "type", contracts.TraitRequired},
		{"an empty type", map[string]any{"type": " "}, "type", contracts.TraitRequired},
		{"a type not among the choices", map[string]any{"type": "distribution"}, "type", contracts.TraitInvalid},
		{"a type that is not text", map[string]any{"type": true}, "type", contracts.TraitInvalid},
		{"a nickname too long", m365(map[string]any{"mailNickname": strings.Repeat("a", 65)}), "mailNickname", contracts.TraitInvalid},
		{"a nickname with nothing left", m365(map[string]any{"mailNickname": "@@@"}), "mailNickname", contracts.TraitInvalid},
		{"a visibility on a security group", map[string]any{"type": "security", "visibility": "private"}, "visibility", contracts.TraitInvalid},
		{"a visibility not among the choices", m365(map[string]any{"visibility": "hidden"}), "visibility", contracts.TraitInvalid},
		{"a description too long", m365(map[string]any{"description": strings.Repeat("d", 1025)}), "description", contracts.TraitInvalid},
		{"a team on a security group", map[string]any{"type": "security", "team": true}, "team", contracts.TraitInvalid},
		{"a team that is not a switch", m365(map[string]any{"team": "yes"}), "team", contracts.TraitInvalid},
		{"licences that are not a list", m365(map[string]any{"licences": "ENTERPRISEPACK"}), "licences", contracts.TraitInvalid},
		{"a licence that is not a part number", m365(map[string]any{"licences": []any{"E3 plan!"}}), "licences", contracts.TraitInvalid},
	} {
		_, err := parseGroupValues(c.values)
		var te *contracts.GroupTraitError
		if !errors.As(err, &te) || te.Field != c.field || te.Code != c.code {
			t.Errorf("%s: %v, want %s %s", c.name, err, c.field, c.code)
		}
	}

	got, err := parseGroupValues(map[string]any{
		"type": "microsoft365", "mailNickname": "Salg Nord", "visibility": "Public", "description": " Selgerne ",
		"team": true, "licences": []any{"ENTERPRISEPACK", "STANDARDPACK"}, "unknown": 42,
	})
	if err != nil || got.kind != "microsoft365" || got.nickname != "salg-nord" || got.visibility != "public" ||
		got.description != "Selgerne" || !got.team || strings.Join(got.licences, ",") != "ENTERPRISEPACK,STANDARDPACK" {
		t.Errorf("the values of a Microsoft 365 group: %+v %v", got, err)
	}
	// A security group's false team, empty visibility and nulls are no values.
	if got, err := parseGroupValues(map[string]any{"type": "security", "team": false, "visibility": "", "description": nil}); err != nil ||
		got.team || got.visibility != "" || got.description != "" {
		t.Errorf("a security group's empty values: %+v %v", got, err)
	}
}

func TestWantGroup_BoundsWhatReachesGraph(t *testing.T) {
	owner := uuid.New()
	w, err := wantGroup(contracts.GroupSpec{
		GroupID: "17", Key: "engineering", Name: strings.Repeat("N", 300), Description: strings.Repeat("d", 1100),
		Values:           map[string]any{"type": "security"},
		OwnerExternalIDs: []string{owner.String(), "not-an-id", strings.ToUpper(owner.String())},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(w.name)) != 256 || len([]rune(w.description)) != 1024 || w.nickname != "engineering" || len(w.owners) != 1 || w.owners[0] != owner {
		t.Errorf("wantGroup: name %d, description %d, nickname %q, owners %v", len(w.name), len(w.description), w.nickname, w.owners)
	}
	// The trait's description is the tenant's when given.
	w, _ = wantGroup(contracts.GroupSpec{GroupID: "17", Key: "eng", Name: "Eng", Description: "Staff's",
		Values: map[string]any{"type": "security", "description": "The tenant's"}})
	if w.description != "The tenant's" {
		t.Errorf("the description: %q, want the trait's", w.description)
	}
	for _, spec := range []contracts.GroupSpec{
		{Key: "eng", Values: map[string]any{"type": "security"}},
		{GroupID: "17", Values: map[string]any{"type": "security"}},
		{GroupID: strings.Repeat("1", 61), Key: "eng", Values: map[string]any{"type": "security"}},
	} {
		var te *contracts.GroupTraitError
		if _, err := wantGroup(spec); err == nil || errors.As(err, &te) {
			t.Errorf("wantGroup(%+v) = %v, want a plain error", spec, err)
		}
	}
}
