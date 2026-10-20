package staff_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// groupProviderHarness is an installation with the fake provider in the
// slot under entra — the fake declares type (a required choice, security by
// default) and mailNickname (a string of at most 64) — and an HR caller.
func groupProviderHarness(t *testing.T) (*harness, *modtest.FakeIdentityProvisioner, *modtest.Client) {
	t.Helper()
	fake := entraProvider()
	h := newHarness(t, modtest.WithIdentityProvisioners(fake))
	hr, _ := signIn(t, h, "staff:manage")
	return h, fake, hr
}

// getTraits reads a group's sections and fails the test unless it answered
// 200.
func getTraits(t *testing.T, c *modtest.Client, id int64) gen.StaffGroupTraits {
	t.Helper()
	var out gen.StaffGroupTraits
	do(t, c, http.MethodGet, groupTraitsPath(id), nil, http.StatusOK, &out)
	return out
}

// A group's sections are one per provider in the slot, turned on or not:
// the provider's name and status, its declaration as the contract gives it,
// and the values — an empty object when it stores none.
func TestGroupTraits_TheDeclarationPerProvider(t *testing.T) {
	t.Parallel()
	h, fake, hr := groupProviderHarness(t)
	g := createGroup(t, hr, listedGroup("declared"))

	got := getTraits(t, hr, g.Id)
	if len(got.Providers) != 1 {
		t.Fatalf("providers = %+v, want one", got.Providers)
	}
	p := got.Providers[0]
	if p.Key != "entra" || p.Name != "Microsoft Entra ID" || p.Status != "connected" || p.Enabled || p.State != nil || p.Values == nil || len(p.Values) != 0 {
		t.Errorf("section = %+v, want entra, connected, off, no state, no values", p)
	}
	if len(p.Traits) != 2 {
		t.Fatalf("traits = %+v, want type and mailNickname", p.Traits)
	}
	kind, nick := p.Traits[0], p.Traits[1]
	if kind.Key != "type" || kind.Type != gen.Choice || !kind.Required || kind.Default != "security" || kind.Choices == nil || len(*kind.Choices) != 2 ||
		kind.Label.En != "Type" || (*kind.Choices)[1].Label.Nb == nil {
		t.Errorf("type = %+v, want a required choice of two, security by default", kind)
	}
	if nick.Key != "mailNickname" || nick.Type != gen.String || nick.Required || nick.MaxLength == nil || *nick.MaxLength != 64 || nick.Choices != nil {
		t.Errorf("mailNickname = %+v, want a string of at most 64", nick)
	}
	if r := hr.Do(http.MethodGet, groupTraitsPath(g.Id), nil); !strings.Contains(string(r.Body), `"values":{}`) {
		t.Errorf("the values are not an empty object: %s", r.Body)
	}

	fake.FailWith("GroupTraits", errors.New("boom"))
	p = getTraits(t, hr, g.Id).Providers[0]
	if p.Status != "failing" || len(p.Traits) != 0 {
		t.Errorf("a provider that cannot declare: %+v, want failing with no traits", p)
	}
	// Turning it on then cannot check the values: 503 provider_unavailable,
	// nothing written.
	refused(t, hr.Do(http.MethodPut, groupTraitPath(g.Id, "entra"), map[string]any{"enabled": true, "values": map[string]any{"type": "security"}}),
		http.StatusServiceUnavailable, "provider_unavailable")
	if n := h.Count(t, `SELECT count(*) FROM staff.group_traits WHERE group_id = $1`, g.Id); n != 0 {
		t.Errorf("%d sections written while the provider could not declare", n)
	}
	if r := hr.Do(http.MethodGet, groupTraitsPath(999999), nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown group: status %d, want 404", r.Status)
	}
}

// With no provider in the slot a group has no sections, and turning one on
// is the unknown provider's 404.
func TestGroupTraits_NoProvider(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	g := createGroup(t, hr, listedGroup("alone"))
	if got := getTraits(t, hr, g.Id); len(got.Providers) != 0 {
		t.Errorf("providers = %+v, want none", got.Providers)
	}
	if r := hr.Do(http.MethodPut, groupTraitPath(g.Id, "entra"), map[string]any{"enabled": true, "values": map[string]any{"type": "security"}}); r.Status != http.StatusNotFound {
		t.Errorf("a provider the slot lacks: status %d, want 404", r.Status)
	}
}

// The values are checked against the declaration when the provider is
// turned on: a key it does not declare, a value of the wrong type, a choice
// it does not admit or a string past its length is trait_invalid on
// values.<key>, a required trait left out trait_required — and nothing is
// written.
func TestGroupTraits_ValidatedAgainstTheDeclaration(t *testing.T) {
	t.Parallel()
	h, _, hr := groupProviderHarness(t)
	g := createGroup(t, hr, listedGroup("checked"))
	cases := []struct {
		name   string
		values map[string]any
		field  string
		code   string
	}{
		{"none", nil, "values.type", "trait_required"},
		{"type left out", map[string]any{"mailNickname": "checked"}, "values.type", "trait_required"},
		{"type empty", map[string]any{"type": ""}, "values.type", "trait_required"},
		{"type null", map[string]any{"type": nil}, "values.type", "trait_required"},
		{"a choice not admitted", map[string]any{"type": "distribution"}, "values.type", "trait_invalid"},
		{"a choice of the wrong type", map[string]any{"type": true}, "values.type", "trait_invalid"},
		{"a string too long", map[string]any{"type": "security", "mailNickname": strings.Repeat("n", 65)}, "values.mailNickname", "trait_invalid"},
		{"a string of the wrong type", map[string]any{"type": "security", "mailNickname": 12}, "values.mailNickname", "trait_invalid"},
		{"a key not declared", map[string]any{"type": "security", "team": true}, "values.team", "trait_invalid"},
	}
	for _, c := range cases {
		body := map[string]any{"enabled": true}
		if c.values != nil {
			body["values"] = c.values
		}
		p := refused(t, hr.Do(http.MethodPut, groupTraitPath(g.Id, "entra"), body), http.StatusBadRequest, c.code)
		if msgs := p.Errors[c.field]; len(msgs) != 1 || msgs[0] != c.code || len(p.Errors) != 1 {
			t.Errorf("%s: errors %v, want %s on %s alone", c.name, p.Errors, c.code, c.field)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.group_traits WHERE group_id = $1`, g.Id); n != 0 {
		t.Errorf("a refused section was written")
	}
	// The longest string passes.
	do(t, hr, http.MethodPut, groupTraitPath(g.Id, "entra"),
		map[string]any{"enabled": true, "values": map[string]any{"type": "security", "mailNickname": strings.Repeat("n", 64)}}, http.StatusOK, nil)
}

// Turning a provider on stores the values, pending and due now — so the
// worker's next cycle sends them — and again resets a failed section; turning
// it off keeps the values and the state, stamps disabled_at once, and on a
// provider never turned on writes nothing. An archived group refuses both.
func TestGroupTraits_OnAndOff(t *testing.T) {
	t.Parallel()
	h, _, hr := groupProviderHarness(t)
	g := createGroup(t, hr, listedGroup("switched"))

	var off gen.StaffGroupProvider
	do(t, hr, http.MethodPut, groupTraitPath(g.Id, "entra"), map[string]any{"enabled": false}, http.StatusOK, &off)
	if off.Enabled || off.State != nil || len(off.Traits) != 2 {
		t.Errorf("off before on: %+v, want off, no state, the declaration", off)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.group_traits WHERE group_id = $1`, g.Id); n != 0 {
		t.Fatalf("turning off a provider never on wrote a row")
	}

	var on gen.StaffGroupProvider
	do(t, hr, http.MethodPut, groupTraitPath(g.Id, "entra"),
		map[string]any{"enabled": true, "values": map[string]any{"type": "microsoft365", "mailNickname": "switched", "nothing": nil}}, http.StatusOK, &on)
	if !on.Enabled || on.State == nil || *on.State != gen.Pending || on.Values["type"] != "microsoft365" || on.Values["mailNickname"] != "switched" || len(on.Values) != 2 {
		t.Fatalf("on = %+v, want enabled, pending, the two values", on)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.group_traits WHERE group_id = $1 AND next_attempt_at <= $2 AND attempts = 0 AND last_values_hash IS NULL AND disabled_at IS NULL`, g.Id, h.Now()); n != 1 {
		t.Errorf("the section is not due now")
	}
	if list := getGroup(t, hr, g.Id).Providers; len(list) != 1 || list[0].Key != "entra" || !list[0].Enabled || list[0].State != gen.Pending {
		t.Errorf("the group's providers = %+v, want entra on and pending", list)
	}

	// A failed section, turned on again with new values, is pending and due.
	h.Exec(t, `UPDATE staff.group_traits SET state = 'failed', last_error = 'trait_immutable', last_error_field = 'type', next_attempt_at = NULL, external_id = 'ext-1' WHERE group_id = $1`, g.Id)
	got := getTraits(t, hr, g.Id).Providers[0]
	if got.State == nil || *got.State != gen.Failed || got.LastError == nil || *got.LastError != "trait_immutable" ||
		got.LastErrorField == nil || *got.LastErrorField != "type" || got.ExternalId == nil || *got.ExternalId != "ext-1" {
		t.Errorf("the failed section reads %+v", got)
	}
	do(t, hr, http.MethodPut, groupTraitPath(g.Id, "entra"), map[string]any{"enabled": true, "values": map[string]any{"type": "security"}}, http.StatusOK, &on)
	if *on.State != gen.Pending || on.LastError != nil || on.LastErrorField != nil || on.ExternalId == nil {
		t.Errorf("turned on again: %+v, want pending, no error, the external id kept", on)
	}

	h.Exec(t, `UPDATE staff.group_traits SET state = 'synced' WHERE group_id = $1`, g.Id)
	do(t, hr, http.MethodPut, groupTraitPath(g.Id, "entra"), map[string]any{"enabled": false, "values": map[string]any{"type": "bogus"}}, http.StatusOK, &off)
	if off.Enabled || off.State == nil || *off.State != gen.Synced || off.Values["type"] != "security" {
		t.Errorf("off = %+v, want off, still synced, the values kept", off)
	}
	first := modtest.One[string](t, h.Harness, `SELECT disabled_at::text FROM staff.group_traits WHERE group_id = $1`, g.Id)
	h.Advance(time.Minute)
	do(t, hr, http.MethodPut, groupTraitPath(g.Id, "entra"), map[string]any{"enabled": false}, http.StatusOK, nil)
	if again := modtest.One[string](t, h.Harness, `SELECT disabled_at::text FROM staff.group_traits WHERE group_id = $1`, g.Id); again != first {
		t.Errorf("disabled_at moved from %s to %s on a second off", first, again)
	}

	do(t, hr, http.MethodPost, archivePath(groupsPath, g.Id), nil, http.StatusOK, nil)
	refused(t, hr.Do(http.MethodPut, groupTraitPath(g.Id, "entra"), map[string]any{"enabled": true, "values": map[string]any{"type": "security"}}), http.StatusConflict, "group_archived")
	refused(t, hr.Do(http.MethodPut, groupTraitPath(g.Id, "entra"), map[string]any{"enabled": false}), http.StatusConflict, "group_archived")
	if r := hr.Do(http.MethodPut, groupTraitPath(999999, "entra"), map[string]any{"enabled": false}); r.Status != http.StatusNotFound {
		t.Errorf("an unknown group: status %d, want 404", r.Status)
	}
	if r := hr.Do(http.MethodPut, groupTraitPath(g.Id, "okta"), map[string]any{"enabled": false}); r.Status != http.StatusNotFound {
		t.Errorf("an unknown provider: status %d, want 404", r.Status)
	}
}
