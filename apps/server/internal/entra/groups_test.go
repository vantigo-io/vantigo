package entra_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The groups (plan task 4, Entra design D10, D14): GroupTraits from the
// connection's row, EnsureGroup's create, adoption and updates with the
// service principal among the owners, the team, SetMembers' batches by
// $ref, DeleteGroup, the leaver's managed step, the licences on Update, and
// GET /groups — against the fake tenant through the harness.

// person adds a user the tenant holds, as an owner or a member.
func (l *lifecycle) person(t *testing.T, nickname string) string {
	t.Helper()
	return l.g.AddUser(graphtest.User{DisplayName: nickname, MailNickname: nickname, UserPrincipalName: nickname + "@nordlys.no"}).ID
}

// people adds n users and answers their ids.
func (l *lifecycle) people(t *testing.T, prefix string, n int) []string {
	t.Helper()
	out := make([]string, n)
	for i := range out {
		out[i] = l.person(t, prefix+strings.Repeat("x", i%3)+uuid.NewString()[:8])
	}
	return out
}

// groupSpec is Staff's group 17, engineering, with values and owners.
func groupSpec(values map[string]any, owners ...string) contracts.GroupSpec {
	return contracts.GroupSpec{
		GroupID: "17", Key: "engineering", Name: "Engineering", Description: "The engineers",
		Values: values, OwnerExternalIDs: owners,
	}
}

func security() map[string]any { return map[string]any{"type": "security"} }

func (l *lifecycle) ensure(t *testing.T, spec contracts.GroupSpec) contracts.ExternalGroup {
	t.Helper()
	out, err := l.p.EnsureGroup(context.Background(), staffActor, spec)
	if err != nil {
		t.Fatalf("EnsureGroup: %v", err)
	}
	return out
}

func (l *lifecycle) group(t *testing.T, id string) graphtest.Group {
	t.Helper()
	g, ok := l.g.Group(id)
	if !ok {
		t.Fatalf("the tenant holds no group %s", id)
	}
	return g
}

// sameSet reports whether a and b hold the same ids, in any order.
func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// noTrap fails the test when a call removed a member or an owner by
// anything but its reference, or when a user was deleted.
func (l *lifecycle) noTrap(t *testing.T) {
	t.Helper()
	for _, c := range l.g.Calls() {
		if c.Method == http.MethodDelete && (strings.Contains(c.Path, "/members/") || strings.Contains(c.Path, "/owners/")) && !strings.HasSuffix(c.Path, "/$ref") {
			t.Errorf("a removal without /$ref: %s %s", c.Method, c.Path)
		}
	}
	if d := l.g.Deleted(); len(d) != 0 {
		t.Errorf("%d users deleted: the trap was reached", len(d))
	}
}

func TestGroupTraits_TheSixFromTheRowWithoutTheNetwork(t *testing.T) {
	t.Parallel()
	h, g := newTenantHarness(t)
	p := provider(h)
	ctx := context.Background()

	traits, err := p.GroupTraits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, len(traits))
	for i, tr := range traits {
		keys[i] = tr.Key + ":" + tr.Type
		if tr.Label["en"] == "" || tr.Label["nb"] == "" || tr.Description["en"] == "" || tr.Description["nb"] == "" ||
			!strings.HasPrefix(tr.DocURL, "https://learn.microsoft.com/") {
			t.Errorf("the trait %s lacks a label, a description or its page: %+v", tr.Key, tr)
		}
		for _, c := range tr.Choices {
			if c.Value == "" || c.Label["en"] == "" || c.Label["nb"] == "" {
				t.Errorf("a choice of %s without its labels: %+v", tr.Key, c)
			}
		}
	}
	if want := []string{"type:choice", "mailNickname:string", "visibility:choice", "description:string", "team:bool", "licences:list"}; !slices.Equal(keys, want) {
		t.Fatalf("the traits: %v, want %v", keys, want)
	}
	kind, nickname, visibility, description, team, licences := traits[0], traits[1], traits[2], traits[3], traits[4], traits[5]
	if !kind.Required || kind.Default != "security" || len(kind.Choices) != 2 || kind.Choices[0].Value != "security" || kind.Choices[1].Value != "microsoft365" {
		t.Errorf("the type: %+v", kind)
	}
	if nickname.MaxLength != 64 || nickname.Required || description.MaxLength != 1024 || team.Default != false ||
		len(visibility.Choices) != 2 || visibility.Choices[0].Value != "private" || visibility.Choices[1].Value != "public" || visibility.Default != nil {
		t.Errorf("the nickname %+v, the description %+v, the team %+v, the visibility %+v", nickname, description, team, visibility)
	}
	if len(licences.Choices) != 0 {
		t.Errorf("the licences before any test: %+v, want no choice", licences.Choices)
	}
	if n := len(g.Calls()); n != 0 {
		t.Errorf("GroupTraits made %d calls", n)
	}

	// The connection test caches the tenant's licences, by part number.
	connected(t, h)
	mark := len(g.Calls())
	traits, err = p.GroupTraits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := traits[5].Choices
	if len(got) != 2 || got[0].Value != "ENTERPRISEPACK" || got[0].Label["en"] != "ENTERPRISEPACK (5 of 25 available)" ||
		got[0].Label["nb"] != "ENTERPRISEPACK (5 av 25 ledige)" || got[1].Value != "STANDARDPACK" || got[1].Label["en"] != "STANDARDPACK (10 of 10 available)" {
		t.Errorf("the licences after the test: %+v", got)
	}
	if n := len(g.Calls()) - mark; n != 0 {
		t.Errorf("GroupTraits made %d calls", n)
	}

	// The licences' read refreshes the cache.
	g.Configure(func(g *graphtest.FakeGraph) { g.Skus[0].Consumed = 25 })
	m, _ := manager(t, h)
	if r := m.Do(http.MethodGet, skusPath, nil); r.Status != http.StatusOK {
		t.Fatalf("GET /connection/skus: %d %s", r.Status, r.Body)
	}
	traits, _ = p.GroupTraits(ctx)
	if l := traits[5].Choices[0].Label["en"]; l != "ENTERPRISEPACK (0 of 25 available)" {
		t.Errorf("the licence after the read: %q", l)
	}
}

func TestConnectionTest_WritesTheServicePrincipal(t *testing.T) {
	t.Parallel()
	h, g := newTenantHarness(t)
	if n := h.Count(t, `SELECT count(*) FROM entra.connection WHERE service_principal_id IS NULL AND skus = '[]'::jsonb`); n != 1 {
		t.Fatal("a fresh connection names a service principal or licences")
	}
	connected(t, h)
	if n := h.Count(t, `SELECT count(*) FROM entra.connection WHERE service_principal_id = $1::uuid AND jsonb_array_length(skus) = 2`, g.ServicePrincipalID); n != 1 {
		t.Error("the test did not write the token's oid and the licences")
	}
}

func TestEnsureGroup_CreatesWithTheServicePrincipalAndTheOwners(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	kari, ola := l.person(t, "kari"), l.person(t, "ola")

	mark := len(l.g.Calls())
	out := l.ensure(t, groupSpec(security(), kari, ola))
	if !out.Created || out.ExternalID == "" || len(out.Pending) != 0 {
		t.Fatalf("EnsureGroup answered %+v", out)
	}
	g := l.group(t, out.ExternalID)
	if g.DisplayName != "Engineering" || g.MailNickname != "engineering" || g.Description != "The engineers" || g.MailEnabled ||
		!g.SecurityEnabled || len(g.GroupTypes) != 0 || !sameSet(g.Owners, []string{l.g.ServicePrincipalID, kari, ola}) {
		t.Errorf("the group made: %+v", g)
	}
	if got := writes(l.graphCalls(mark)); !slices.Equal(got, []string{"POST groups"}) {
		t.Errorf("the create's writes: %q, want the one POST", got)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.groups WHERE object_id = $1::uuid AND group_id = '17' AND key = 'engineering'
	    AND display_name = 'Engineering' AND mail_nickname = 'engineering' AND type = 'security' AND created_by_us AND state = 'present'
	    AND team_created_at IS NULL`, out.ExternalID); n != 1 {
		t.Error("the group's row is not written")
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.group.created' AND object_id = $1::uuid AND upn IS NULL
	    AND actor_system = $2 AND codes = ARRAY['type:security', 'owners:2']::text[]`, out.ExternalID, contracts.SystemActorStaffAccounts); n != 1 {
		t.Errorf("the created event: %v", l.events(t, "entra.group.created"))
	}

	// Twenty-two owners: the service principal and nineteen in the create,
	// the other three by $ref.
	many := l.people(t, "owner", 22)
	spec := groupSpec(security(), many...)
	spec.GroupID, spec.Key = "18", "many-owners"
	mark = len(l.g.Calls())
	out = l.ensure(t, spec)
	if g := l.group(t, out.ExternalID); len(g.Owners) != 23 || !slices.Contains(g.Owners, l.g.ServicePrincipalID) {
		t.Errorf("%d owners, want the service principal and 22", len(g.Owners))
	}
	if n := countCalls(l.graphCalls(mark), http.MethodPost, "/owners/$ref"); n != 3 {
		t.Errorf("%d owners by $ref, want 3", n)
	}

	// An owner the tenant does not hold: made with the service principal,
	// the people by $ref, the stranger skipped.
	spec = groupSpec(security(), kari, uuid.NewString())
	spec.GroupID, spec.Key = "19", "with-a-stranger"
	out = l.ensure(t, spec)
	if g := l.group(t, out.ExternalID); !sameSet(g.Owners, []string{l.g.ServicePrincipalID, kari}) {
		t.Errorf("the owners with a stranger named: %v", g.Owners)
	}
	if ev := l.events(t, "entra.group.created"); !hasCode(ev[len(ev)-1], "owners:1") {
		t.Errorf("the created event: %v", ev[len(ev)-1])
	}
}

func TestEnsureGroup_LaterCallsWriteWhatDiffersAndReconcileTheOwners(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	kari, ola, per := l.person(t, "kari"), l.person(t, "ola"), l.person(t, "per")
	id := l.ensure(t, groupSpec(security(), kari, ola)).ExternalID

	// The same spec again: read, nothing written.
	mark := len(l.g.Calls())
	if out := l.ensure(t, groupSpec(security(), kari, ola)); out.Created || out.ExternalID != id {
		t.Errorf("the second EnsureGroup answered %+v", out)
	}
	if got := writes(l.graphCalls(mark)); len(got) != 0 {
		t.Errorf("an unchanged group's writes: %q", got)
	}

	// The name alone: one PATCH.
	renamed := groupSpec(security(), kari, ola)
	renamed.Name = "Engineering (archived)"
	mark = len(l.g.Calls())
	l.ensure(t, renamed)
	if got := writes(l.graphCalls(mark)); !slices.Equal(got, []string{"PATCH " + id}) {
		t.Errorf("a rename's writes: %q, want one PATCH", got)
	}
	if g := l.group(t, id); g.DisplayName != "Engineering (archived)" {
		t.Errorf("the name: %q", g.DisplayName)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.groups WHERE object_id = $1::uuid AND display_name = 'Engineering (archived)'`, id); n != 1 {
		t.Error("the row keeps the old name")
	}

	// The owners: per added, ola removed by $ref in a batch, the service
	// principal kept.
	moved := groupSpec(security(), kari, per)
	moved.Name = renamed.Name
	l.ensure(t, moved)
	if g := l.group(t, id); !sameSet(g.Owners, []string{l.g.ServicePrincipalID, kari, per}) {
		t.Errorf("the owners: %v", g.Owners)
	}
	ev := l.events(t, "entra.group.updated")
	if last := ev[len(ev)-1]; !slices.Equal(last, []string{"owners_added:1", "owners_removed:1"}) {
		t.Errorf("the updated event: %v", last)
	}

	// No owner named: every person removed, the service principal never —
	// and one an administrator removed is put back.
	l.g.EditGroup(id, func(g *graphtest.Group) {
		g.Owners = slices.DeleteFunc(g.Owners, func(o string) bool { return o == l.g.ServicePrincipalID })
	})
	ownerless := groupSpec(security())
	ownerless.Name = renamed.Name
	l.ensure(t, ownerless)
	if g := l.group(t, id); !slices.Equal(g.Owners, []string{l.g.ServicePrincipalID}) {
		t.Errorf("the owners of an ownerless group: %v, want the service principal alone", g.Owners)
	}
	l.noTrap(t)
}

func TestEnsureGroup_TheTypeAndTheNicknameAreImmutable(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	id := l.ensure(t, groupSpec(security())).ExternalID
	ctx := context.Background()
	for field, values := range map[string]map[string]any{
		"type":         {"type": "microsoft365"},
		"mailNickname": {"type": "security", "mailNickname": "engineers"},
	} {
		mark := len(l.g.Calls())
		_, err := l.p.EnsureGroup(ctx, staffActor, groupSpec(values))
		var te *contracts.GroupTraitError
		if !errors.As(err, &te) || te.Field != field || te.Code != contracts.TraitImmutable {
			t.Errorf("%s changed: %v, want %s trait_immutable", field, err, field)
		}
		if n := len(l.graphCalls(mark)); n != 0 {
			t.Errorf("%s changed: %d calls to the tenant", field, n)
		}
	}
	if g := l.group(t, id); g.MailNickname != "engineering" || g.MailEnabled {
		t.Errorf("the group changed: %+v", g)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.group.updated' AND object_id = $1::uuid
	    AND codes = ARRAY['refused:trait_immutable']::text[]`, id); n != 2 {
		t.Errorf("the refusals' events: %d", n)
	}

	// A value the declaration refuses: the trait's error, nothing asked.
	mark := len(l.g.Calls())
	_, err := l.p.EnsureGroup(ctx, staffActor, groupSpec(map[string]any{"type": "security", "team": true}))
	var te *contracts.GroupTraitError
	if !errors.As(err, &te) || te.Field != "team" || te.Code != contracts.TraitInvalid {
		t.Errorf("a team on a security group: %v, want team trait_invalid", err)
	}
	if n := len(l.graphCalls(mark)); n != 0 {
		t.Errorf("a refused value made %d calls", n)
	}
}

func TestEnsureGroup_RefusesAGroupItMayNotWrite(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	l.g.AddGroup(graphtest.Group{DisplayName: "Sales", MailNickname: "sales", SecurityEnabled: true,
		GroupTypes: []string{"DynamicMembership"}, MembershipRule: `user.department -eq "Sales"`})
	l.g.AddGroup(graphtest.Group{DisplayName: "Admins", MailNickname: "admins", SecurityEnabled: true, IsAssignableToRole: true})
	l.g.AddGroup(graphtest.Group{DisplayName: "List", MailNickname: "list", MailEnabled: true})
	l.g.AddGroup(graphtest.Group{DisplayName: "Twin", MailNickname: "twin", SecurityEnabled: true})
	l.g.AddGroup(graphtest.Group{DisplayName: "Twin", MailNickname: "twin", SecurityEnabled: true})
	ops := l.ensure(t, contracts.GroupSpec{GroupID: "1", Key: "ops", Name: "Ops", Values: security()})

	for _, spec := range []contracts.GroupSpec{
		{GroupID: "2", Key: "sales", Name: "Sales", Values: security()},
		{GroupID: "3", Key: "admins", Name: "Admins", Values: security()},
		{GroupID: "4", Key: "list", Name: "List", Values: security()},
		{GroupID: "5", Key: "twin", Name: "Twin", Values: security()},
		{GroupID: "6", Key: "ops-again", Name: "Ops", Values: map[string]any{"type": "security", "mailNickname": "ops"}},
	} {
		mark := len(l.g.Calls())
		if _, err := l.p.EnsureGroup(ctx, staffActor, spec); !errors.Is(err, contracts.ErrGroupNotManageable) {
			t.Errorf("%s: %v, want ErrGroupNotManageable", spec.Key, err)
		}
		if got := writes(l.graphCalls(mark)); len(got) != 0 {
			t.Errorf("%s: writes %q", spec.Key, got)
		}
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.groups`); n != 1 {
		t.Errorf("%d rows, want ops's alone", n)
	}
	if g := l.group(t, ops.ExternalID); g.DisplayName != "Ops" {
		t.Errorf("another Staff group's group was written: %+v", g)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.events WHERE codes = ARRAY['refused:group_not_manageable']::text[]`); n != 5 {
		t.Errorf("%d refusals recorded, want 5", n)
	}

	// The module's own group made dynamic in the portal: refused too.
	l.g.EditGroup(ops.ExternalID, func(g *graphtest.Group) { g.MembershipRule = "user.accountEnabled -eq true" })
	if _, err := l.p.EnsureGroup(ctx, staffActor, contracts.GroupSpec{GroupID: "1", Key: "ops", Name: "Ops", Values: security()}); !errors.Is(err, contracts.ErrGroupNotManageable) {
		t.Errorf("the module's group made dynamic: %v", err)
	}
}

// A group found holding the nickname is adopted untrusted (the review's
// HIGH 1): its row recorded, nothing written to the tenant — no name, no
// owner, no member — and every write answers ErrGroupUnconfirmed until a
// holder of entra:manage confirms it; then EnsureGroup does what adoption
// did. The module never deletes a group it did not make.
func TestEnsureGroup_AnAdoptedGroupIsUntrustedUntilConfirmed(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	kari, ola, per := l.person(t, "kari"), l.person(t, "ola"), l.person(t, "per")
	found := l.g.AddGroup(graphtest.Group{DisplayName: "Engineers (old)", MailNickname: "engineering", SecurityEnabled: true,
		Owners: []string{kari}, Members: []string{per}})

	mark := len(l.g.Calls())
	if out, err := l.p.EnsureGroup(ctx, staffActor, groupSpec(security(), kari, ola)); !errors.Is(err, contracts.ErrGroupUnconfirmed) || out.ExternalID != "" {
		t.Fatalf("EnsureGroup of a found group: %+v %v, want ErrGroupUnconfirmed", out, err)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.groups WHERE object_id = $1::uuid AND group_id = '17' AND NOT created_by_us
	    AND adoption_confirmed_at IS NULL AND state = 'present'`, found.ID); n != 1 {
		t.Error("the adopted group's row")
	}
	if ev := l.events(t, "entra.group.adopted"); len(ev) != 1 || !slices.Equal(ev[0], []string{"type:security", "refused:adoption_unconfirmed"}) {
		t.Errorf("the adopted event: %v", ev)
	}
	if _, err := l.p.EnsureGroup(ctx, staffActor, groupSpec(security(), kari, ola)); !errors.Is(err, contracts.ErrGroupUnconfirmed) {
		t.Errorf("EnsureGroup again: %v, want ErrGroupUnconfirmed", err)
	}
	if _, err := l.p.SetMembers(ctx, staffActor, "17", []string{ola}); !errors.Is(err, contracts.ErrGroupUnconfirmed) {
		t.Errorf("SetMembers of an unconfirmed group: %v, want ErrGroupUnconfirmed", err)
	}
	for _, action := range []string{"entra.group.updated", "entra.group.members_set"} {
		if ev := l.events(t, action); len(ev) != 1 || !hasCode(ev[0], "refused:adoption_unconfirmed") {
			t.Errorf("the %s events: %v, want refused:adoption_unconfirmed", action, ev)
		}
	}
	if got := writes(l.graphCalls(mark)); len(got) != 0 {
		t.Errorf("writes to an unconfirmed group: %q", got)
	}
	if g := l.group(t, found.ID); g.DisplayName != "Engineers (old)" || !sameSet(g.Owners, []string{kari}) || !sameSet(g.Members, []string{per}) {
		t.Errorf("the unconfirmed group was changed: %+v", g)
	}

	// Confirmed: 403 without entra:manage, 404 unknown, then 200 and
	// audited once; a second confirmation is answered as it is.
	confirm := groupsPath + "/" + found.ID + "/confirm"
	if r := l.h.SignIn(t).Do(http.MethodPost, confirm, nil); r.Status != http.StatusForbidden {
		t.Errorf("confirm without entra:manage: %d, want 403", r.Status)
	}
	m, managerID := manager(t, l.h)
	if r := m.Do(http.MethodPost, groupsPath+"/"+uuid.NewString()+"/confirm", nil); r.Status != http.StatusNotFound {
		t.Errorf("confirm an unknown group: %d, want 404", r.Status)
	}
	for range 2 {
		r := m.Do(http.MethodPost, confirm, nil)
		if r.Status != http.StatusOK {
			t.Fatalf("confirm: %d %s", r.Status, r.Body)
		}
		var g gen.EntraGroup
		r.JSON(&g)
		if g.ObjectId.String() != found.ID || g.CreatedByUs || g.AdoptionConfirmedAt == nil {
			t.Errorf("the confirmed group: %+v", g)
		}
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.group.adoption_confirmed' AND actor_user_id = $1
	    AND object_id = $2::uuid`, managerID, found.ID); n != 1 {
		t.Errorf("%d adoption_confirmed events, want one", n)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.groups WHERE object_id = $1::uuid AND adoption_confirmed_by_user_id = $2`, found.ID, managerID); n != 1 {
		t.Error("the confirmation is not kept on the row")
	}

	out := l.ensure(t, groupSpec(security(), kari, ola))
	if out.Created || out.ExternalID != found.ID {
		t.Fatalf("EnsureGroup after the confirmation answered %+v", out)
	}
	if g := l.group(t, found.ID); g.DisplayName != "Engineering" || !sameSet(g.Owners, []string{kari, ola, l.g.ServicePrincipalID}) {
		t.Errorf("the confirmed group: %+v", g)
	}
	if result, err := l.p.SetMembers(ctx, staffActor, "17", []string{ola}); err != nil || result.Added != 1 || result.Removed != 1 {
		t.Errorf("SetMembers after the confirmation: %+v %v", result, err)
	}

	// Let go: the tenant keeps a group the module did not make.
	mark = len(l.g.Calls())
	if err := l.p.DeleteGroup(ctx, staffActor, "17"); err != nil {
		t.Fatal(err)
	}
	if got := writes(l.graphCalls(mark)); len(got) != 0 {
		t.Errorf("DeleteGroup of an adopted group wrote %q", got)
	}
	if g := l.group(t, found.ID); g.Deleted {
		t.Error("the adopted group was deleted in the tenant")
	}
	if ev := l.events(t, "entra.group.deleted"); len(ev) != 1 || !slices.Equal(ev[0], []string{"kept_not_ours"}) {
		t.Errorf("the deleted events: %v", ev)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.groups WHERE object_id = $1::uuid AND state = 'deleted'`, found.ID); n != 1 {
		t.Error("the adopted group's row is not marked deleted")
	}

	// Adopted with another type: the type cannot change, and nothing is
	// recorded.
	l.g.AddGroup(graphtest.Group{DisplayName: "All", MailNickname: "all", MailEnabled: true, SecurityEnabled: true, GroupTypes: []string{"Unified"}})
	_, err := l.p.EnsureGroup(ctx, staffActor, contracts.GroupSpec{GroupID: "2", Key: "all", Name: "All", Values: security()})
	var te *contracts.GroupTraitError
	if !errors.As(err, &te) || te.Field != "type" || te.Code != contracts.TraitImmutable {
		t.Errorf("a Microsoft 365 group found for a security one: %v", err)
	}
}

// An unconfirmed adoption deleted by Staff is left to the tenant too, with
// no call (kept_not_ours).
func TestDeleteGroup_AnUnconfirmedAdoptionIsLeftToTheTenant(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	found := l.g.AddGroup(graphtest.Group{DisplayName: "Engineers", MailNickname: "engineering", SecurityEnabled: true})
	if _, err := l.p.EnsureGroup(ctx, staffActor, groupSpec(security())); !errors.Is(err, contracts.ErrGroupUnconfirmed) {
		t.Fatalf("EnsureGroup: %v", err)
	}
	mark := len(l.g.Calls())
	if err := l.p.DeleteGroup(ctx, staffActor, "17"); err != nil || len(l.g.Calls()) != mark {
		t.Errorf("DeleteGroup of an unconfirmed adoption: %v, %d calls", err, len(l.g.Calls())-mark)
	}
	if g := l.group(t, found.ID); g.Deleted {
		t.Error("deleted in the tenant")
	}
}

// Two Staff groups cannot hold one tenant group (the review's LOW 7): the
// second Staff group naming the first's nickname is refused, and
// InsertGroup never re-points another Staff group's row.
func TestEnsureGroup_TwoStaffGroupsAndOneNickname(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	first := l.ensure(t, groupSpec(security())).ExternalID

	other := contracts.GroupSpec{GroupID: "18", Key: "engineers", Name: "Engineers", Values: map[string]any{"type": "security", "mailNickname": "engineering"}}
	if _, err := l.p.EnsureGroup(ctx, staffActor, other); !errors.Is(err, contracts.ErrGroupNotManageable) {
		t.Errorf("a second Staff group on the nickname: %v, want ErrGroupNotManageable", err)
	}
	_, err := store.New(l.h.Pool()).InsertGroup(ctx, store.InsertGroupParams{
		ObjectID: uuid.MustParse(first), GroupID: "18", Key: "engineers", DisplayName: "Engineers", MailNickname: "engineering",
		Type: "security", CreatedByUs: false, Now: l.h.Now(),
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("InsertGroup of another Staff group's object: %v, want no row", err)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.groups WHERE object_id = $1::uuid AND group_id = '17' AND key = 'engineering'`, first); n != 1 {
		t.Error("the first Staff group's row was re-pointed")
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.groups WHERE group_id = '18'`); n != 0 {
		t.Error("a row was recorded for the second Staff group")
	}
}

func TestEnsureGroup_TheTeam(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	kari := l.person(t, "kari")
	values := map[string]any{"type": "microsoft365", "visibility": "private", "team": true}

	// Young: made, the team pending.
	out := l.ensure(t, groupSpec(values, kari))
	if !out.Created || !slices.Equal(out.Pending, []string{"team"}) {
		t.Fatalf("a young group's EnsureGroup: %+v", out)
	}
	g := l.group(t, out.ExternalID)
	if !g.MailEnabled || !g.SecurityEnabled || !slices.Equal(g.GroupTypes, []string{"Unified"}) || g.Visibility != "Private" || g.Team {
		t.Errorf("the Microsoft 365 group: %+v", g)
	}
	if n := countCalls(l.g.Calls(), http.MethodPut, "/team"); n != 0 {
		t.Errorf("%d teams asked of a young group", n)
	}
	if ev := l.events(t, "entra.group.created"); len(ev) != 1 || !hasCode(ev[0], "team_pending") {
		t.Errorf("the created event: %v", ev)
	}

	// Fifteen minutes on, Teams not ready twice: the third try makes it.
	l.h.Advance(16 * time.Minute)
	l.g.Configure(func(g *graphtest.FakeGraph) { g.TeamNotFound = 2 })
	mark := len(l.g.Calls())
	out = l.ensure(t, groupSpec(values, kari))
	if len(out.Pending) != 0 || out.Created {
		t.Errorf("the old group's EnsureGroup: %+v", out)
	}
	if n := countCalls(l.graphCalls(mark), http.MethodPut, "/team"); n != 3 {
		t.Errorf("%d PUT /team, want three tries", n)
	}
	if !l.group(t, out.ExternalID).Team {
		t.Error("no team")
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.groups WHERE object_id = $1::uuid AND team_created_at IS NOT NULL`, out.ExternalID); n != 1 {
		t.Error("team_created_at is not recorded")
	}
	if ev := l.events(t, "entra.group.team_created"); len(ev) != 1 {
		t.Errorf("the team_created events: %v", ev)
	}
	// A team made is not asked for again.
	mark = len(l.g.Calls())
	l.ensure(t, groupSpec(values, kari))
	if n := countCalls(l.graphCalls(mark), http.MethodPut, "/team"); n != 0 {
		t.Errorf("%d PUT /team on a team", n)
	}

	// Without a person owner: trait_needs_owner, no team asked.
	ownerless := contracts.GroupSpec{GroupID: "2", Key: "ownerless", Name: "Ownerless", Values: values}
	if out := l.ensure(t, ownerless); !slices.Equal(out.Pending, []string{"team"}) {
		t.Errorf("the ownerless group made: %+v", out)
	}
	l.h.Advance(16 * time.Minute)
	mark = len(l.g.Calls())
	_, err := l.p.EnsureGroup(ctx, staffActor, ownerless)
	var te *contracts.GroupTraitError
	if !errors.As(err, &te) || te.Field != "team" || te.Code != contracts.TraitNeedsOwner {
		t.Errorf("a team without a person owner: %v, want team trait_needs_owner", err)
	}
	if n := countCalls(l.graphCalls(mark), http.MethodPut, "/team"); n != 0 {
		t.Errorf("%d PUT /team without an owner", n)
	}

	// Teams still not ready after three tries: pending, for the next cycle.
	kept := contracts.GroupSpec{GroupID: "3", Key: "kept", Name: "Kept", Values: values, OwnerExternalIDs: []string{kari}}
	l.ensure(t, kept)
	l.h.Advance(16 * time.Minute)
	l.g.Configure(func(g *graphtest.FakeGraph) { g.TeamNotFound = 3 })
	mark = len(l.g.Calls())
	if out := l.ensure(t, kept); !slices.Equal(out.Pending, []string{"team"}) {
		t.Errorf("three 404s: %+v, want the team pending", out)
	}
	if n := countCalls(l.graphCalls(mark), http.MethodPut, "/team"); n != 3 {
		t.Errorf("%d PUT /team, want three", n)
	}
}

func TestSetMembers_AddsInPatchesAndRemovesByRefInBatches(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	id := l.ensure(t, groupSpec(security())).ExternalID
	stay := l.people(t, "stay", 3)
	leave := l.people(t, "leave", 24)
	stale := uuid.NewString()
	l.g.EditGroup(id, func(g *graphtest.Group) {
		g.Members = slices.Concat(stay, leave)
		g.StaleMembers = []string{stale}
	})
	join := l.people(t, "join", 45)

	mark := len(l.g.Calls())
	result, err := l.p.SetMembers(ctx, staffActor, "17", slices.Concat(stay, join, join[:2]))
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 45 || result.Removed != 25 || len(result.Failed) != 0 {
		t.Errorf("SetMembers answered %+v, want 45 added and 25 removed", result)
	}
	if g := l.group(t, id); !sameSet(g.Members, slices.Concat(stay, join)) {
		t.Errorf("%d members, want the 48 named", len(g.Members))
	}
	calls := l.graphCalls(mark)
	if n := countCalls(calls, http.MethodPatch, "/groups/"+id); n != 3 {
		t.Errorf("%d PATCHes, want three of at most twenty", n)
	}
	if n := countCalls(calls, http.MethodPost, "/$batch"); n != 2 {
		t.Errorf("%d $batch, want two of at most twenty", n)
	}
	deletes := 0
	for _, c := range calls {
		if c.Method == http.MethodDelete {
			deletes++
			if !c.Batch || !strings.HasPrefix(c.Path, "/v1.0/groups/"+id+"/members/") || !strings.HasSuffix(c.Path, "/$ref") {
				t.Errorf("a removal: %+v", c)
			}
		}
	}
	if deletes != 25 {
		t.Errorf("%d removals, want 25", deletes)
	}
	l.noTrap(t)
	for _, u := range slices.Concat(leave, join) {
		if _, ok := l.g.User(u); !ok {
			t.Fatalf("user %s is gone", u)
		}
	}
	if logs := l.h.Logs(); slices.ContainsFunc(slices.Concat(stay, leave, join), func(u string) bool { return strings.Contains(logs, u) }) {
		t.Error("a member's id is in the log")
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.groups WHERE object_id = $1::uuid AND last_members_at IS NOT NULL`, id); n != 1 {
		t.Error("last_members_at is not recorded")
	}
	ev := l.events(t, "entra.group.members_set")
	if len(ev) != 1 || !slices.Equal(ev[0], []string{"added:45", "removed:25", "failed:0"}) {
		t.Errorf("the members_set events: %v", ev)
	}

	// Unchanged: read, nothing written.
	mark = len(l.g.Calls())
	if result, err := l.p.SetMembers(ctx, staffActor, "17", slices.Concat(stay, join)); err != nil || result.Added+result.Removed+len(result.Failed) != 0 {
		t.Errorf("an unchanged SetMembers: %+v %v", result, err)
	}
	if got := writes(l.graphCalls(mark)); len(got) != 0 {
		t.Errorf("an unchanged SetMembers wrote %q", got)
	}
}

func TestSetMembers_OneByOneWhenAPatchIsRefused(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	id := l.ensure(t, groupSpec(security())).ExternalID
	kari, ola := l.person(t, "kari"), l.person(t, "ola")
	ghost := uuid.NewString()
	// kari joins in the portal between the read and the write: the PATCH
	// answers "already exist", and kari is no failure.
	l.w.afterOn("GET /members", func() {
		l.g.EditGroup(id, func(g *graphtest.Group) {
			if !slices.Contains(g.Members, kari) {
				g.Members = append(g.Members, kari)
			}
		})
	})
	result, err := l.p.SetMembers(ctx, staffActor, "17", []string{kari, ola, ghost, "not-an-id"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 1 || result.Removed != 0 || len(result.Failed) != 2 ||
		result.Failed[0] != (contracts.MemberFailure{ID: "not-an-id", Code: contracts.MemberNotFound}) ||
		result.Failed[1] != (contracts.MemberFailure{ID: ghost, Code: contracts.MemberNotFound}) {
		t.Errorf("SetMembers answered %+v", result)
	}
	if g := l.group(t, id); !sameSet(g.Members, []string{kari, ola}) {
		t.Errorf("the members: %v", g.Members)
	}
	if n := countCalls(l.g.Calls(), http.MethodPost, "/members/$ref"); n != 3 {
		t.Errorf("%d members by $ref, want the three of the refused PATCH", n)
	}
	if strings.Contains(l.h.Logs(), ghost) {
		t.Error("the stranger's id is in the log")
	}
}

func TestSetMembers_ReadsEveryPage(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	id := l.ensure(t, groupSpec(security())).ExternalID
	members := make([]string, 1001)
	for i := range members {
		members[i] = uuid.NewString()
	}
	l.g.EditGroup(id, func(g *graphtest.Group) { g.Members = slices.Clone(members) })
	mark := len(l.g.Calls())
	result, err := l.p.SetMembers(context.Background(), staffActor, "17", members)
	if err != nil || result.Added+result.Removed+len(result.Failed) != 0 {
		t.Errorf("SetMembers: %+v %v, want nothing to change", result, err)
	}
	pages := 0
	for _, c := range l.graphCalls(mark) {
		if c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/members") {
			pages++
			if !strings.Contains(c.Query, "top=999") {
				t.Errorf("a page without $top=999: %s", c.Query)
			}
		}
	}
	if pages != 2 {
		t.Errorf("%d pages read, want two", pages)
	}
}

func TestSetMembers_TheGroupsItMayNotWrite(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	if _, err := l.p.SetMembers(ctx, staffActor, "17", nil); !errors.Is(err, contracts.ErrGroupNotManageable) {
		t.Errorf("a group never made: %v, want ErrGroupNotManageable", err)
	}
	id := l.ensure(t, groupSpec(security())).ExternalID
	kari, ola := l.person(t, "kari"), l.person(t, "ola")
	l.g.EditGroup(id, func(g *graphtest.Group) {
		g.MembershipRule = `user.department -eq "Engineering"`
		g.Members = []string{kari}
	})
	mark := len(l.g.Calls())
	result, err := l.p.SetMembers(ctx, staffActor, "17", []string{ola})
	if err != nil || result.Added+result.Removed != 0 || len(result.Failed) != 2 ||
		result.Failed[0].Code != contracts.MemberDynamic || result.Failed[1].Code != contracts.MemberDynamic {
		t.Errorf("a dynamic group: %+v %v", result, err)
	}
	if got := writes(l.graphCalls(mark)); len(got) != 0 {
		t.Errorf("a dynamic group's writes: %q", got)
	}
}

func TestDeleteGroup_SoftDeletesOnceAndAGroupIsMadeAnew(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	mark := len(l.g.Calls())
	if err := l.p.DeleteGroup(ctx, staffActor, "17"); err != nil || len(l.g.Calls()) != mark {
		t.Errorf("a group never made: %v, %d calls", err, len(l.g.Calls())-mark)
	}
	m365 := func() map[string]any { return map[string]any{"type": "microsoft365"} }
	id := l.ensure(t, groupSpec(m365())).ExternalID
	if err := l.p.DeleteGroup(ctx, staffActor, "17"); err != nil {
		t.Fatal(err)
	}
	if g := l.group(t, id); !g.Deleted {
		t.Error("the group is not deleted")
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.groups WHERE object_id = $1::uuid AND state = 'deleted'`, id); n != 1 {
		t.Error("the row is not marked deleted")
	}
	mark = len(l.g.Calls())
	if err := l.p.DeleteGroup(ctx, staffActor, "17"); err != nil || len(l.g.Calls()) != mark {
		t.Errorf("a second DeleteGroup: %v, %d calls", err, len(l.g.Calls())-mark)
	}
	if ev := l.events(t, "entra.group.deleted"); len(ev) != 1 {
		t.Errorf("the deleted events: %v", ev)
	}
	if _, err := l.p.SetMembers(ctx, staffActor, "17", nil); !errors.Is(err, contracts.ErrGroupNotManageable) {
		t.Errorf("SetMembers on a deleted group: %v", err)
	}

	// Turned on again: a new group takes the row's place.
	again := l.ensure(t, groupSpec(m365()))
	if !again.Created || again.ExternalID == id {
		t.Errorf("EnsureGroup after the delete: %+v", again)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.groups WHERE group_id = '17' AND object_id = $1::uuid AND state = 'present'`, again.ExternalID); n != 1 {
		t.Error("the new group's row")
	}

	// Deleted in the portal: the next EnsureGroup makes it anew too.
	l.g.EditGroup(again.ExternalID, func(g *graphtest.Group) { g.Deleted = true })
	third := l.ensure(t, groupSpec(m365()))
	if !third.Created || third.ExternalID == again.ExternalID {
		t.Errorf("EnsureGroup after a delete in the portal: %+v", third)
	}
}

// A security group the module made is never deleted in the tenant — Graph
// deletes one for good — but emptied of its user members and its person
// owners by reference, the service principal kept an owner, renamed
// "… (archived)" once and kept, its row retired (the review's MEDIUM 4).
// Turned on again, the group is found by its nickname and is the module's
// own still: no confirmation asked.
func TestDeleteGroup_ASecurityGroupIsEmptiedAndKept(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	kari, ola := l.person(t, "kari"), l.person(t, "ola")
	members := l.people(t, "member", 23)
	id := l.ensure(t, groupSpec(security(), kari, ola)).ExternalID
	if _, err := l.p.SetMembers(ctx, staffActor, "17", members); err != nil {
		t.Fatal(err)
	}

	mark := len(l.g.Calls())
	if err := l.p.DeleteGroup(ctx, staffActor, "17"); err != nil {
		t.Fatal(err)
	}
	g := l.group(t, id)
	if g.Deleted || len(g.Members) != 0 || g.DisplayName != "Engineering (archived)" || slices.Contains(g.Owners, kari) ||
		slices.Contains(g.Owners, ola) || !slices.Contains(g.Owners, l.g.ServicePrincipalID) {
		t.Errorf("the retired group: %+v, want it kept, empty, renamed, the service principal its owner", g)
	}
	if n := countCalls(l.graphCalls(mark), http.MethodDelete, "/groups/"+id); n != 0 {
		t.Error("the security group was deleted")
	}
	l.noTrap(t)
	for _, u := range slices.Concat(members, []string{kari, ola}) {
		if _, ok := l.g.User(u); !ok {
			t.Fatalf("user %s is gone", u)
		}
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.groups WHERE object_id = $1::uuid AND state = 'retired' AND display_name = 'Engineering (archived)'`, id); n != 1 {
		t.Error("the row is not retired")
	}
	ev := l.events(t, "entra.group.retired")
	if len(ev) != 1 || !slices.Equal(ev[0], []string{"security_kept", "removed:23", "owners_removed:2", "changed:displayName"}) {
		t.Errorf("the retired events: %v", ev)
	}
	mark = len(l.g.Calls())
	if err := l.p.DeleteGroup(ctx, staffActor, "17"); err != nil || len(l.g.Calls()) != mark {
		t.Errorf("a second DeleteGroup: %v, %d calls", err, len(l.g.Calls())-mark)
	}
	if conn := l.h.Count(t, `SELECT count(*) FROM entra.groups WHERE state = 'present'`); conn != 0 {
		t.Error("a retired group counts as present")
	}

	// Turned on again: the kept group is found by its nickname, the
	// module's own, and brought back without a confirmation.
	out := l.ensure(t, groupSpec(security(), kari))
	if out.Created || out.ExternalID != id {
		t.Errorf("EnsureGroup after the retirement: %+v, want the kept group", out)
	}
	if g := l.group(t, id); g.DisplayName != "Engineering" || !slices.Contains(g.Owners, kari) {
		t.Errorf("the group brought back: %+v", g)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.groups WHERE object_id = $1::uuid AND state = 'present' AND created_by_us`, id); n != 1 {
		t.Error("the row brought back is not the module's, present")
	}
}

func TestGroups_TheActorAndTheConnection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, g := newTenantHarness(t)
	p := provider(h)
	_, ensure := p.EnsureGroup(ctx, staffActor, groupSpec(security()))
	_, members := p.SetMembers(ctx, staffActor, "17", nil)
	for name, err := range map[string]error{"EnsureGroup": ensure, "SetMembers": members} {
		if !errors.Is(err, contracts.ErrProviderUnavailable) {
			t.Errorf("unconfigured %s: %v, want ErrProviderUnavailable", name, err)
		}
	}
	if err := p.DeleteGroup(ctx, staffActor, "17"); err != nil {
		t.Errorf("unconfigured DeleteGroup of a group never made: %v", err)
	}
	if n := len(g.Calls()); n != 0 {
		t.Errorf("an unconfigured provider made %d calls", n)
	}

	l := newLifecycle(t)
	l.ensure(t, groupSpec(security()))
	mark := len(l.g.Calls())
	nobody := uuid.Nil
	for _, actor := range []contracts.Actor{{}, {System: "system:someone"}, {UserID: &nobody}} {
		_, ensure := l.p.EnsureGroup(ctx, actor, groupSpec(security()))
		_, members := l.p.SetMembers(ctx, actor, "17", nil)
		for name, err := range map[string]error{"EnsureGroup": ensure, "SetMembers": members, "DeleteGroup": l.p.DeleteGroup(ctx, actor, "17")} {
			if !errors.Is(err, contracts.ErrInvalidActor) {
				t.Errorf("%s by %+v: %v, want ErrInvalidActor", name, actor, err)
			}
		}
	}
	if n := len(l.g.Calls()) - mark; n != 0 {
		t.Errorf("an invalid actor's writes made %d calls", n)
	}
}

func TestDeactivate_ManagedRemovesFromTheModulesGroupsAlone(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	l.h.Exec(t, `UPDATE entra.connection SET leaver_remove_groups = 'managed'`)
	spec := joiner("E-1")
	id := l.prepare(t, spec).ExternalID
	l.activate(t, id, spec)
	ours := l.ensure(t, groupSpec(security())).ExternalID
	if _, err := l.p.SetMembers(ctx, staffActor, "17", []string{id}); err != nil {
		t.Fatal(err)
	}
	others, _ := l.leaverGroups(id)

	mark := len(l.g.Calls())
	if acc := l.deactivate(t, id, leaver(spec)); acc.State != "disabled" || len(acc.Reported) != 0 {
		t.Errorf("Deactivate answered %+v", acc)
	}
	if l.member(t, ours, id) {
		t.Error("still a member of the module's group")
	}
	for _, g := range others {
		if !l.member(t, g, id) {
			t.Errorf("removed from %s, a group not the module's", g)
		}
	}
	if n := countCalls(l.graphCalls(mark), http.MethodDelete, "/$ref"); n != 1 {
		t.Errorf("%d removals, want the module's group's alone", n)
	}
	ev := l.events(t, "entra.user.deactivated")
	if len(ev) != 1 || !hasCode(ev[0], "groups_removed") {
		t.Errorf("the deactivated events: %v", ev)
	}
	l.noTrap(t)
}

func TestUpdate_ReconcilesTheDirectLicences(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	const skuE5 = "06ebc4ee-1bb5-47dd-8120-11324bc54e06"
	l.g.Configure(func(g *graphtest.FakeGraph) {
		g.Skus = append(g.Skus, graphtest.Sku{SkuID: skuE5, PartNumber: "SPE_E5", Enabled: 5, Consumed: 0})
	})
	spec := joiner("E-1") // ENTERPRISEPACK
	id := l.prepare(t, spec).ExternalID
	l.activate(t, id, spec)
	group := uuid.NewString()
	l.g.EditUser(id, func(u *graphtest.User) {
		u.Licences = append(u.Licences, graphtest.LicenceState{SkuID: skuE1, AssignedByGroup: group, State: "Active", Error: "None"})
	})

	moved := spec
	moved.Licences = []string{"SPE_E5"}
	if _, err := l.p.Update(context.Background(), staffActor, id, moved); err != nil {
		t.Fatal(err)
	}
	var held []string
	for _, lic := range l.user(t, id).Licences {
		held = append(held, lic.SkuID+"/"+lic.AssignedByGroup)
	}
	if !sameSet(held, []string{skuE5 + "/", skuE1 + "/" + group}) {
		t.Errorf("the licences after Update: %v, want SPE_E5 direct and the group's kept", held)
	}
	ev := l.events(t, "entra.user.updated")
	if last := ev[len(ev)-1]; !hasCode(last, "licences_assigned:1") || !hasCode(last, "licences_removed:1") {
		t.Errorf("the updated event: %v", last)
	}

	// A licence with no seat left is reported, as Activate reports it.
	l.g.Configure(func(g *graphtest.FakeGraph) { g.Skus[0].Consumed = g.Skus[0].Enabled })
	moved.Licences = []string{"SPE_E5", "ENTERPRISEPACK"}
	acc, err := l.p.Update(context.Background(), staffActor, id, moved)
	if err != nil || !hasCode(acc.Reported, "licence_unavailable:ENTERPRISEPACK") {
		t.Errorf("Update with no seat left: %+v %v", acc, err)
	}

	// A disabled user's licences are the leaver's: Update leaves them.
	l.h.Exec(t, `UPDATE entra.connection SET leaver_strip_licences = false`)
	l.deactivate(t, id, leaver(moved))
	before := len(l.user(t, id).Licences)
	moved.Licences = nil
	if _, err := l.p.Update(context.Background(), staffActor, id, moved); err != nil {
		t.Fatal(err)
	}
	if after := len(l.user(t, id).Licences); after != before {
		t.Errorf("Update on a disabled user changed its licences: %d to %d", before, after)
	}
}

// Update removes only the direct licences the module itself assigned (the
// review's HIGH 2, design reading 9): one an administrator assigned
// directly survives an Update with no licence named; one the module
// assigned leaves when the person's groups' licences trait no longer names
// it; the leaver's strip forgets what the module assigned.
func TestUpdate_RemovesOnlyTheLicencesTheModuleAssigned(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	const skuE5 = "06ebc4ee-1bb5-47dd-8120-11324bc54e06"
	const skuVisio = "c5928f49-12ba-48f7-ada3-0d743a3601d5"
	l.g.Configure(func(g *graphtest.FakeGraph) {
		g.Skus = append(g.Skus,
			graphtest.Sku{SkuID: skuE5, PartNumber: "SPE_E5", Enabled: 5, Consumed: 0},
			graphtest.Sku{SkuID: skuVisio, PartNumber: "VISIOCLIENT", Enabled: 5, Consumed: 0})
	})
	spec := joiner("E-1")
	spec.Licences = nil
	id := l.prepare(t, spec).ExternalID
	l.activate(t, id, spec)
	// An administrator's direct licence, assigned in the portal.
	l.g.EditUser(id, func(u *graphtest.User) {
		u.Licences = append(u.Licences, graphtest.LicenceState{SkuID: skuVisio, State: "Active", Error: "None"})
	})
	direct := func() []string {
		var out []string
		for _, lic := range l.user(t, id).Licences {
			if lic.AssignedByGroup == "" {
				out = append(out, lic.SkuID)
			}
		}
		return out
	}
	assigned := func() string {
		return modtest.One[string](t, l.h.Harness, `SELECT array_to_string(licences_assigned, ',') FROM entra.accounts WHERE object_id = $1::uuid`, id)
	}

	if _, err := l.p.Update(context.Background(), staffActor, id, spec); err != nil {
		t.Fatal(err)
	}
	if got := direct(); !sameSet(got, []string{skuVisio}) {
		t.Errorf("the direct licences after an Update with none named: %v, want the administrator's kept", got)
	}

	// A group's licences trait names SPE_E5: the module assigns it.
	withE5 := spec
	withE5.Licences = []string{"SPE_E5"}
	if _, err := l.p.Update(context.Background(), staffActor, id, withE5); err != nil {
		t.Fatal(err)
	}
	if got := direct(); !sameSet(got, []string{skuVisio, skuE5}) {
		t.Errorf("the direct licences with SPE_E5 named: %v", got)
	}
	if got := assigned(); got != skuE5 {
		t.Errorf("licences_assigned = %q, want the module's SPE_E5 alone", got)
	}

	// The trait drops it: the module's licence leaves, the administrator's
	// stays.
	if _, err := l.p.Update(context.Background(), staffActor, id, spec); err != nil {
		t.Fatal(err)
	}
	if got := direct(); !sameSet(got, []string{skuVisio}) {
		t.Errorf("the direct licences after the trait dropped SPE_E5: %v, want the administrator's alone", got)
	}
	if got := assigned(); got != "" {
		t.Errorf("licences_assigned = %q, want none", got)
	}
	ev := l.events(t, "entra.user.updated")
	if last := ev[len(ev)-1]; !hasCode(last, "licences_removed:1") {
		t.Errorf("the updated event: %v", last)
	}

	// The leaver's strip (C's rule) takes every direct licence, and the
	// module forgets the ones it assigned.
	if _, err := l.p.Update(context.Background(), staffActor, id, withE5); err != nil {
		t.Fatal(err)
	}
	l.deactivate(t, id, leaver(withE5))
	if got := direct(); len(got) != 0 {
		t.Errorf("the direct licences after the leaver's strip: %v", got)
	}
	if got := assigned(); got != "" {
		t.Errorf("licences_assigned after the strip = %q, want none", got)
	}
}

func TestGroupsEndpoint_TheModulesGroupsAndTheirCount(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	kari := l.person(t, "kari")
	first := l.ensure(t, groupSpec(map[string]any{"type": "microsoft365", "team": true}, kari)).ExternalID
	l.h.Advance(16 * time.Minute)
	l.ensure(t, groupSpec(map[string]any{"type": "microsoft365", "team": true}, kari))
	second := l.ensure(t, contracts.GroupSpec{GroupID: "18", Key: "accounts", Name: "Accounts", Values: security()}).ExternalID
	if _, err := l.p.SetMembers(context.Background(), staffActor, "18", nil); err != nil {
		t.Fatal(err)
	}
	if err := l.p.DeleteGroup(context.Background(), staffActor, "18"); err != nil {
		t.Fatal(err)
	}

	if r := l.h.SignIn(t).Do(http.MethodGet, groupsPath, nil); r.Status != http.StatusForbidden {
		t.Errorf("GET /groups without entra:manage: %d, want 403", r.Status)
	}
	m, _ := manager(t, l.h)
	r := m.Do(http.MethodGet, groupsPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /groups: %d %s", r.Status, r.Body)
	}
	var groups []gen.EntraGroup
	r.JSON(&groups)
	if len(groups) != 2 || groups[0].ObjectId.String() != second || groups[1].ObjectId.String() != first {
		t.Fatalf("GET /groups: %+v, want Accounts (archived) then Engineering", groups)
	}
	acc, eng := groups[0], groups[1]
	if acc.State != gen.EntraGroupStateRetired || acc.Type != gen.Security || acc.Team || acc.LastMembersAt == nil || acc.GroupId != "18" ||
		acc.DisplayName != "Accounts (archived)" || acc.AdoptionConfirmedAt != nil {
		t.Errorf("the retired group: %+v", acc)
	}
	if eng.State != gen.EntraGroupStatePresent || eng.Type != gen.Microsoft365 || !eng.Team || !eng.CreatedByUs || eng.Key != "engineering" ||
		eng.MailNickname != "engineering" || eng.DisplayName != "Engineering" || eng.LastMembersAt != nil {
		t.Errorf("the team's group: %+v", eng)
	}
	if conn := getConnection(t, m); conn.Groups.Count != 1 {
		t.Errorf("groups.count = %d, want the one present", conn.Groups.Count)
	}
}
