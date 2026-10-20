package staff_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// The group sync in the accounts worker's cycle (Entra delivery D, plan
// task 3b): a group section HR turned on is created in the provider's
// directory with its owners and the members who have an enabled account
// there, kept in step by its hashes, retired per deleteExternalAfterDays,
// and left alone while the provider is not connected. The groups are
// written through the store, as HR's endpoints would leave them. The
// harness's today is 2026-09-12.

// recordingProvisioner is the fake provider with the group calls' arguments
// kept: the specs EnsureGroup was given, and the members each SetMembers
// was asked for. onEnsure, when set, runs inside EnsureGroup, before the
// fake answers.
type recordingProvisioner struct {
	*modtest.FakeIdentityProvisioner
	mu       sync.Mutex
	ensured  []contracts.GroupSpec
	members  [][]string
	onEnsure func()
}

func (r *recordingProvisioner) EnsureGroup(ctx context.Context, actor contracts.Actor, spec contracts.GroupSpec) (contracts.ExternalGroup, error) {
	r.mu.Lock()
	r.ensured = append(r.ensured, spec)
	hook := r.onEnsure
	r.mu.Unlock()
	if hook != nil {
		hook()
	}
	return r.FakeIdentityProvisioner.EnsureGroup(ctx, actor, spec)
}

func (r *recordingProvisioner) SetMembers(ctx context.Context, actor contracts.Actor, groupID string, ids []string) (contracts.MembersResult, error) {
	r.mu.Lock()
	r.members = append(r.members, slices.Clone(ids))
	r.mu.Unlock()
	return r.FakeIdentityProvisioner.SetMembers(ctx, actor, groupID, ids)
}

// groupCalls is the fake's group writes so far.
func (r *recordingProvisioner) groupCalls() []string {
	var out []string
	for _, c := range r.Calls() {
		switch c.Method {
		case "EnsureGroup", "SetMembers", "DeleteGroup":
			out = append(out, c.Method)
		}
	}
	return out
}

func (r *recordingProvisioner) lastEnsured(t *testing.T) contracts.GroupSpec {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.ensured) == 0 {
		t.Fatal("no EnsureGroup")
	}
	return r.ensured[len(r.ensured)-1]
}

func (r *recordingProvisioner) lastMembers(t *testing.T) []string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.members) == 0 {
		t.Fatal("no SetMembers")
	}
	return r.members[len(r.members)-1]
}

// groupWorld is a providerWorld whose provider records the group calls.
type groupWorld struct {
	providerWorld
	rec *recordingProvisioner
	q   *store.Queries
}

func newGroupWorld(t *testing.T) groupWorld {
	t.Helper()
	prov := modtest.NewFakeIdentityProvisioner(contracts.AccountKindEntra, "Microsoft Entra ID")
	rec := &recordingProvisioner{FakeIdentityProvisioner: prov}
	w := newAccountsWorld(t, modtest.WithIdentityProvisioners(rec))
	prov.WithClock(w.h.Now)
	return groupWorld{providerWorld: providerWorld{accountsWorld: w, prov: prov}, rec: rec, q: store.New(w.h.Pool())}
}

// later moves the clock past the poll, when a synced section is due again.
func (w groupWorld) later() { w.h.Advance(time.Hour + time.Minute) }

// member is a person with no account flag and, when externalID is not "",
// an account under entra in state (enabled, disabled …) — the worker never
// claims them, so only the group sync reads them.
func (w groupWorld) member(t *testing.T, given, externalID, state string) int64 {
	t.Helper()
	id := seedPerson(t, w.h, given, "Gruppe")
	if externalID != "" {
		w.h.Exec(t, `INSERT INTO staff.identity_links (person_id, provider, external_id, state, created_at, updated_at)
			VALUES ($1, 'entra', $2, $3, $4, $4)`, id, externalID, state, w.h.Now())
	}
	return id
}

// group is a listed group with members and owners, and its entra section
// turned on with values.
func (w groupWorld) group(t *testing.T, key string, members, owners []int64, values map[string]any) int64 {
	t.Helper()
	ctx := context.Background()
	g, err := w.q.InsertGroup(ctx, store.InsertGroupParams{Key: key, Name: "Group " + key, Kind: "listed", Now: w.h.Now()})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range members {
		if err := w.q.InsertGroupMember(ctx, store.InsertGroupMemberParams{
			GroupID: g.ID, PersonID: m, Source: "listed", Since: pgtype.Date{Time: w.h.Now(), Valid: true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.q.ReplaceGroupOwners(ctx, store.ReplaceGroupOwnersParams{GroupID: g.ID, PersonIds: owners}); err != nil {
		t.Fatal(err)
	}
	w.setValues(t, g.ID, values)
	return g.ID
}

// setValues is HR's PUT of the entra section with values.
func (w groupWorld) setValues(t *testing.T, groupID int64, values map[string]any) {
	t.Helper()
	b, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.q.UpsertGroupTrait(context.Background(), store.UpsertGroupTraitParams{
		GroupID: groupID, Provider: "entra", Values: b, Now: w.h.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

func (w groupWorld) section(t *testing.T, groupID int64) store.StaffGroupTrait {
	t.Helper()
	row, err := w.q.GetGroupTrait(context.Background(), store.GetGroupTraitParams{GroupID: groupID, Provider: "entra"})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func (w groupWorld) archive(t *testing.T, groupID int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := w.q.ArchiveGroup(ctx, store.ArchiveGroupParams{Now: w.h.Now(), ID: groupID}); err != nil {
		t.Fatal(err)
	}
	if err := w.q.MakeGroupTraitsDue(ctx, store.MakeGroupTraitsDueParams{GroupID: groupID, Now: w.h.Now()}); err != nil {
		t.Fatal(err)
	}
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

var security = map[string]any{"type": "security"}

// A listed group turned on for the provider is created at the next cycle:
// one EnsureGroup with the caller's id, the key and the owners' external
// ids, then one SetMembers with the members' enabled accounts — a member
// whose account is disabled, or who has none, is not sent, nor is such an
// owner. An unchanged cycle calls nothing; a member leaving makes one
// SetMembers, and a values change one EnsureGroup.
func TestGroupSync_AListedGroupIsCreatedWithItsOwnersAndEnabledMembers(t *testing.T) {
	t.Parallel()
	w := newGroupWorld(t)
	a := w.member(t, "Anne", "ext-a", "enabled")
	b := w.member(t, "Bjørn", "ext-b", "enabled")
	c := w.member(t, "Cato", "ext-c", "disabled")
	d := w.member(t, "Dina", "", "")
	o := w.member(t, "Olav", "ext-o", "enabled")
	g := w.group(t, "sales", []int64{b, a, c, d}, []int64{o, d}, security)

	w.cycle(t)
	if got, want := w.rec.groupCalls(), []string{"EnsureGroup", "SetMembers"}; !slices.Equal(got, want) {
		t.Fatalf("group calls = %v, want %v", got, want)
	}
	spec := w.rec.lastEnsured(t)
	if spec.GroupID != strconv.FormatInt(g, 10) || spec.Key != "sales" || spec.Name != "Group sales" ||
		!slices.Equal(spec.OwnerExternalIDs, []string{"ext-o"}) || spec.Values["type"] != "security" {
		t.Errorf("EnsureGroup's spec = %+v, want the group's id, key and name, the owner ext-o, the values", spec)
	}
	if got := w.prov.Owners(strconv.FormatInt(g, 10)); !slices.Equal(got, []string{"ext-o"}) {
		t.Errorf("the group's owners in the provider = %v, want [ext-o]", got)
	}
	if got := w.rec.lastMembers(t); !slices.Equal(got, []string{"ext-a", "ext-b"}) {
		t.Errorf("SetMembers = %v, want [ext-a ext-b] (the disabled and the unlinked left out)", got)
	}
	row := w.section(t, g)
	if row.State != "synced" || row.ExternalID == nil || row.LastValuesHash == nil || row.LastMembersHash == nil ||
		row.SyncedAt == nil || row.Attempts != 0 || row.LastError != nil || row.LeaseID != nil ||
		row.NextAttemptAt == nil || !row.NextAttemptAt.After(w.h.Now()) {
		t.Errorf("the section = %+v, want synced, the external id, both hashes, no lease, due after the poll", row)
	}

	w.later()
	w.cycle(t)
	if got := w.rec.groupCalls(); len(got) != 2 {
		t.Errorf("group calls after an unchanged cycle = %v, want none more", got)
	}

	if _, err := w.q.DeleteGroupMember(context.Background(), store.DeleteGroupMemberParams{GroupID: g, PersonID: b}); err != nil {
		t.Fatal(err)
	}
	w.later()
	w.cycle(t)
	if got, want := w.rec.groupCalls(), []string{"EnsureGroup", "SetMembers", "SetMembers"}; !slices.Equal(got, want) {
		t.Errorf("group calls after a member left = %v, want %v", got, want)
	}
	if got := w.rec.lastMembers(t); !slices.Equal(got, []string{"ext-a"}) {
		t.Errorf("SetMembers = %v, want [ext-a]", got)
	}

	w.setValues(t, g, map[string]any{"type": "security", "mailNickname": "sales-team"})
	w.cycle(t)
	if got, want := w.rec.groupCalls(), []string{"EnsureGroup", "SetMembers", "SetMembers", "EnsureGroup"}; !slices.Equal(got, want) {
		t.Errorf("group calls after a values change = %v, want %v", got, want)
	}
	if got := w.rec.lastEnsured(t).Values["mailNickname"]; got != "sales-team" {
		t.Errorf("EnsureGroup's mailNickname = %v, want sales-team", got)
	}
	if row := w.section(t, g); row.State != "synced" {
		t.Errorf("the section = %+v, want synced again", row)
	}

	// An owner whose account is enabled since reaches the provider by the
	// one EnsureGroup.
	w.h.Exec(t, `UPDATE staff.identity_links SET state = 'enabled' WHERE person_id = $1`, c)
	if err := w.q.ReplaceGroupOwners(context.Background(), store.ReplaceGroupOwnersParams{GroupID: g, PersonIds: []int64{o, c}}); err != nil {
		t.Fatal(err)
	}
	w.later()
	w.cycle(t)
	if got := w.rec.lastEnsured(t).OwnerExternalIDs; !slices.Equal(got, []string{"ext-c", "ext-o"}) {
		t.Errorf("EnsureGroup's owners = %v, want [ext-c ext-o]", got)
	}
}

// A value the provider refuses lands on the section with its field and
// code, the section failed and not due again: no further call until HR
// changes the values. groupSyncFailed names the group on the dashboard
// meanwhile, and clears once the section syncs. A type changed after the
// group was made is trait_immutable the same way.
func TestGroupSync_ATraitErrorWaitsForTheValuesToChange(t *testing.T) {
	t.Parallel()
	w := newGroupWorld(t)
	a := w.member(t, "Anne", "ext-a", "enabled")
	g := w.group(t, "it", []int64{a}, nil, map[string]any{})

	w.cycle(t)
	row := w.section(t, g)
	if row.State != "failed" || str(row.LastError) != contracts.TraitRequired || str(row.LastErrorField) != "type" ||
		row.NextAttemptAt != nil || row.LastValuesHash != nil || row.ExternalID != nil {
		t.Fatalf("the section = %+v, want failed trait_required on type, not due, no hash", row)
	}
	w.later()
	w.cycle(t)
	if got := w.rec.groupCalls(); !slices.Equal(got, []string{"EnsureGroup"}) {
		t.Errorf("group calls = %v, want the one EnsureGroup (not retried)", got)
	}
	item := groupItem(t, w, g)
	if item == nil || item.Title != "Group it" || item.Id != "groupSyncFailed/"+strconv.FormatInt(g, 10)+"/entra" {
		t.Errorf("the attention item = %+v, want groupSyncFailed naming the group", item)
	}

	w.setValues(t, g, security)
	w.cycle(t)
	if row := w.section(t, g); row.State != "synced" || row.LastError != nil || row.LastErrorField != nil {
		t.Errorf("the section after the values changed = %+v, want synced, no error", row)
	}
	if item := groupItem(t, w, g); item != nil {
		t.Errorf("the attention item after the sync = %+v, want none", item)
	}

	w.setValues(t, g, map[string]any{"type": "microsoft365"})
	w.cycle(t)
	if row := w.section(t, g); row.State != "failed" || str(row.LastError) != contracts.TraitImmutable || str(row.LastErrorField) != "type" {
		t.Errorf("the section after a type change = %+v, want failed trait_immutable on type", row)
	}
}

// groupItem is the groupSyncFailed item for groupID as HR reads it, nil for
// none.
func groupItem(t *testing.T, w groupWorld, groupID int64) *gen.StaffStatsAttentionItem {
	t.Helper()
	var items []gen.StaffStatsAttentionItem
	do(t, w.hr, http.MethodGet, attentionPath, nil, http.StatusOK, &items)
	for _, it := range items {
		if it.Type == gen.GroupSyncFailed && it.EntityId == strconv.FormatInt(groupID, 10) {
			return &it
		}
	}
	return nil
}

// A member the provider could not add leaves the section synced with
// members_failed:<n> and the members' hash unwritten: the next cycle sends
// the members again, and on success the error clears.
func TestGroupSync_AFailedMemberIsSentAgain(t *testing.T) {
	t.Parallel()
	w := newGroupWorld(t)
	a := w.member(t, "Anne", "ext-a", "enabled")
	b := w.member(t, "Bjørn", "ext-b", "enabled")
	g := w.group(t, "ops", []int64{a, b}, nil, security)
	w.prov.FailMembers(strconv.FormatInt(g, 10), "ext-b", contracts.MemberNotFound)

	w.cycle(t)
	row := w.section(t, g)
	if row.State != "synced" || str(row.LastError) != "members_failed:1" || row.LastMembersHash != nil || row.LastValuesHash == nil {
		t.Fatalf("the section = %+v, want synced, members_failed:1, the members' hash unwritten", row)
	}
	w.later()
	w.cycle(t)
	if got, want := w.rec.groupCalls(), []string{"EnsureGroup", "SetMembers", "SetMembers"}; !slices.Equal(got, want) {
		t.Errorf("group calls = %v, want %v", got, want)
	}
	if row := w.section(t, g); row.State != "synced" || row.LastError != nil || row.LastMembersHash == nil {
		t.Errorf("the section after the retry = %+v, want synced, no error, the members' hash", row)
	}
}

// A call that fails is retried after the backoff, counted; the third in a
// row fails the section, which raises its item. A provider not connected
// writes nothing — no call, nothing counted — and the section is created
// once it is connected again.
func TestGroupSync_FailuresAndAProviderNotConnected(t *testing.T) {
	t.Parallel()
	w := newGroupWorld(t)
	a := w.member(t, "Anne", "ext-a", "enabled")
	g := w.group(t, "hr", []int64{a}, nil, security)

	w.prov.SetStatus(contracts.ProviderUnconfigured, "")
	w.cycle(t)
	row := w.section(t, g)
	if got := w.rec.groupCalls(); len(got) != 0 {
		t.Errorf("group calls while unconfigured = %v, want none", got)
	}
	if row.State != "pending" || row.ExternalID != nil || row.Attempts != 0 || row.LastError != nil ||
		row.NextAttemptAt == nil || !row.NextAttemptAt.After(w.h.Now()) || row.LeaseID != nil {
		t.Errorf("the section while unconfigured = %+v, want pending, untouched, due at the next poll", row)
	}

	w.prov.SetStatus(contracts.ProviderConnected, "")
	w.prov.FailWith("EnsureGroup", context.DeadlineExceeded)
	for i := 1; i <= 3; i++ {
		w.later()
		w.cycle(t)
		row := w.section(t, g)
		wantState := "pending"
		if i == 3 {
			wantState = "failed"
		}
		if row.State != wantState || row.Attempts != int32(i) || str(row.LastError) != "call_failed" || row.NextAttemptAt == nil {
			t.Errorf("the section after failure %d = %+v, want %s, %d attempts, call_failed, due again", i, row, wantState, i)
		}
	}
	if item := groupItem(t, w, g); item == nil {
		t.Error("no groupSyncFailed after three failures")
	}
	w.prov.FailWith("EnsureGroup", nil)
	w.later()
	w.cycle(t)
	if row := w.section(t, g); row.State != "synced" || row.Attempts != 0 || row.LastError != nil {
		t.Errorf("the section after the provider answered = %+v, want synced", row)
	}
}

// A section HR changes while the worker's call is under way ends the
// claim: the result, judged on the values read before, is not written, and
// the next cycle sends the new values.
func TestGroupSync_AResultOnAnEndedClaimIsNotWritten(t *testing.T) {
	t.Parallel()
	w := newGroupWorld(t)
	a := w.member(t, "Anne", "ext-a", "enabled")
	g := w.group(t, "lab", []int64{a}, nil, security)
	once := sync.Once{}
	w.rec.onEnsure = func() {
		once.Do(func() { w.setValues(t, g, map[string]any{"type": "security", "mailNickname": "lab"}) })
	}

	w.cycle(t)
	if row := w.section(t, g); row.State != "pending" || row.ExternalID != nil || row.LastValuesHash != nil {
		t.Errorf("the section = %+v, want HR's pending row, the claim's result dropped", row)
	}
	w.cycle(t)
	if row := w.section(t, g); row.State != "synced" || w.rec.lastEnsured(t).Values["mailNickname"] != "lab" {
		t.Errorf("the section = %+v, want synced with HR's new values", row)
	}
}

// An archived group is deleted in the provider deleteExternalAfterDays after
// its archive, not before, and never claimed again.
func TestGroupSync_AnArchivedGroupIsDeletedAfterTheDays(t *testing.T) {
	t.Parallel()
	w := newGroupWorld(t)
	w.h.Exec(t, `UPDATE staff.settings SET delete_external_after_days = 2`)
	a := w.member(t, "Anne", "ext-a", "enabled")
	g := w.group(t, "old", []int64{a}, nil, security)
	w.cycle(t)
	w.archive(t, g)

	w.cycle(t)
	if got := w.rec.groupCalls(); slices.Contains(got, "DeleteGroup") || len(got) != 2 {
		t.Errorf("group calls on the archive's day = %v, want nothing more", got)
	}
	if row := w.section(t, g); row.State != "synced" {
		t.Errorf("the section before the days ran = %+v, want synced still", row)
	}
	w.h.Advance(2 * 24 * time.Hour)
	w.cycle(t)
	if got, want := w.rec.groupCalls(), []string{"EnsureGroup", "SetMembers", "DeleteGroup"}; !slices.Equal(got, want) {
		t.Errorf("group calls two days on = %v, want %v", got, want)
	}
	if row := w.section(t, g); row.State != "deleted" || row.NextAttemptAt != nil {
		t.Errorf("the section = %+v, want deleted, not due", row)
	}
	w.later()
	w.cycle(t)
	if got := w.rec.groupCalls(); len(got) != 3 {
		t.Errorf("group calls after the delete = %v, want none more", got)
	}
}

// With deleteExternalAfterDays never, an archived group is renamed
// "… (archived)" once and kept, the section retired; a section HR turned
// off goes the same way, and with the setting 0 is deleted at once.
func TestGroupSync_ARetiredGroupIsRenamedOrDeleted(t *testing.T) {
	t.Parallel()
	w := newGroupWorld(t)
	a := w.member(t, "Anne", "ext-a", "enabled")
	archived := w.group(t, "archived", []int64{a}, nil, security)
	off := w.group(t, "off", []int64{a}, nil, security)
	gone := w.group(t, "gone", []int64{a}, nil, security)
	never := w.group(t, "never", nil, nil, map[string]any{})
	w.cycle(t)
	calls := len(w.rec.groupCalls())

	w.archive(t, archived)
	w.cycle(t)
	spec := w.rec.lastEnsured(t)
	if got := w.rec.groupCalls()[calls:]; !slices.Equal(got, []string{"EnsureGroup"}) || spec.Name != "Group archived (archived)" ||
		spec.GroupID != strconv.FormatInt(archived, 10) {
		t.Errorf("calls after the archive = %v, spec %+v, want one EnsureGroup renaming the group", got, spec)
	}
	if row := w.section(t, archived); row.State != "retired" || row.RetiredAt == nil || row.NextAttemptAt != nil {
		t.Errorf("the archived group's section = %+v, want retired, not due", row)
	}

	ctx := context.Background()
	disable := func(id int64) {
		if _, err := w.q.DisableGroupTrait(ctx, store.DisableGroupTraitParams{GroupID: id, Provider: "entra", Now: w.h.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	disable(off)
	w.later()
	w.cycle(t)
	if spec := w.rec.lastEnsured(t); spec.Name != "Group off (archived)" {
		t.Errorf("the turned-off group's EnsureGroup = %+v, want the name suffixed", spec)
	}
	if row := w.section(t, off); row.State != "retired" {
		t.Errorf("the turned-off group's section = %+v, want retired", row)
	}
	// A section the provider never made a group for retires with no call.
	disable(never)
	before := len(w.rec.groupCalls())
	w.cycle(t)
	if row := w.section(t, never); row.State != "retired" || len(w.rec.groupCalls()) != before {
		t.Errorf("the never-made section = %+v after %v, want retired with no call", row, w.rec.groupCalls()[before:])
	}

	w.h.Exec(t, `UPDATE staff.settings SET delete_external_after_days = 0`)
	disable(gone)
	before = len(w.rec.groupCalls())
	w.cycle(t)
	if got := w.rec.groupCalls()[before:]; !slices.Equal(got, []string{"DeleteGroup"}) {
		t.Errorf("calls after turning off with 0 days = %v, want one DeleteGroup", got)
	}
	if row := w.section(t, gone); row.State != "deleted" {
		t.Errorf("the section = %+v, want deleted", row)
	}
	w.later()
	before = len(w.rec.groupCalls())
	w.cycle(t)
	if got := w.rec.groupCalls()[before:]; len(got) != 0 {
		t.Errorf("calls after every section retired = %v, want none", got)
	}
}

// An erased person leaves every group and every group's owners, with no
// event; the next sync removes them in the provider.
func TestGroupSync_AnErasedPersonLeavesEveryGroup(t *testing.T) {
	t.Parallel()
	w := newGroupWorld(t)
	a := w.member(t, "Anne", "ext-a", "enabled")
	b := w.member(t, "Bjørn", "ext-b", "enabled")
	end := "2026-01-31"
	seedEmploymentDated(t, w.h, b, 0, "2020-01-01", &end)
	g := w.group(t, "erase", []int64{a, b}, []int64{b}, security)
	w.cycle(t)
	if got := w.rec.lastMembers(t); !slices.Equal(got, []string{"ext-a", "ext-b"}) {
		t.Fatalf("SetMembers = %v, want both", got)
	}

	runCycle(t, w.h)
	if n := w.h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at IS NOT NULL`, b); n != 1 {
		t.Fatal("the leaver was not erased")
	}
	if n := w.h.Count(t, `SELECT (SELECT count(*) FROM staff.group_members WHERE person_id = $1)
		+ (SELECT count(*) FROM staff.group_owners WHERE person_id = $1)`, b); n != 0 {
		t.Errorf("the erased person has %d group rows, want none", n)
	}
	if n := w.h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind LIKE 'group%'`, b); n != 0 {
		t.Errorf("the erase recorded %d group events, want none", n)
	}
	w.later()
	w.cycle(t)
	if got := w.rec.lastMembers(t); !slices.Equal(got, []string{"ext-a"}) {
		t.Errorf("SetMembers after the erase = %v, want [ext-a]", got)
	}
	if got := w.rec.lastEnsured(t).OwnerExternalIDs; len(got) != 0 {
		t.Errorf("EnsureGroup's owners after the erase = %v, want none", got)
	}
	_ = g
}

// A person deleted as created in error takes their group rows with them.
func TestGroupSync_ADeletedPersonLeavesEveryGroup(t *testing.T) {
	t.Parallel()
	w := newGroupWorld(t)
	p := w.member(t, "Feil", "", "")
	w.group(t, "delete", []int64{p}, []int64{p}, security)
	do(t, w.hr, http.MethodDelete, personPath(p), nil, http.StatusNoContent, nil)
	if n := w.h.Count(t, `SELECT (SELECT count(*) FROM staff.group_members WHERE person_id = $1)
		+ (SELECT count(*) FROM staff.group_owners WHERE person_id = $1)`, p); n != 0 {
		t.Errorf("the deleted person has %d group rows, want none", n)
	}
}

// The licences a person gets are the union of their groups' licences
// traits under the provider (D10), in the spec: a group's licences change
// the spec's hash, and the next claim updates the account with them; an
// archived group's licences leave it.
func TestGroupSync_TheLicencesUnionReachesTheSpec(t *testing.T) {
	t.Parallel()
	w := newGroupWorld(t)
	p := w.entraEmployee(t, "Lise", "2026-09-01", nil)
	w.cycle(t)
	if spec, _ := w.prov.LastSpec(p.EmployeeNumber); len(spec.Licences) != 0 {
		t.Fatalf("the spec's licences = %v, want none yet", spec.Licences)
	}
	w.group(t, "e3", []int64{p.Id}, nil, map[string]any{"type": "security", "licences": []string{"SPE_E3", "EMS"}})
	g2 := w.group(t, "e5", []int64{p.Id}, nil, map[string]any{"type": "security", "licences": []string{"EMS", "POWER_BI_PRO"}})
	updates := func() int {
		n := 0
		for _, m := range providerMethods(w.prov) {
			if m == "Update" {
				n++
			}
		}
		return n
	}
	before := updates()
	w.nextDay()
	w.cycle(t)
	spec, _ := w.prov.LastSpec(p.EmployeeNumber)
	if got, want := spec.Licences, []string{"EMS", "POWER_BI_PRO", "SPE_E3"}; !slices.Equal(got, want) {
		t.Errorf("the spec's licences = %v, want %v", got, want)
	}
	if got := updates(); got != before+1 {
		t.Errorf("Updates = %d, want one more than %d", got, before)
	}
	w.nextDay()
	w.cycle(t)
	if got := updates(); got != before+1 {
		t.Errorf("Updates after an unchanged day = %d, want %d", got, before+1)
	}
	w.archive(t, g2)
	w.nextDay()
	w.cycle(t)
	spec, _ = w.prov.LastSpec(p.EmployeeNumber)
	if got, want := spec.Licences, []string{"EMS", "SPE_E3"}; !slices.Equal(got, want) || updates() != before+2 {
		t.Errorf("the spec's licences after the archive = %v (%d updates), want %v in one more Update", got, updates()-before, want)
	}
}

// A group the provider found and adopted and nobody has confirmed there
// (the review's HIGH 1) is no failure: the section waits pending with
// group_unconfirmed, nothing counted, no item raised, asked again at the
// next poll; once confirmed, the next cycle creates it in step and sends the
// members.
func TestGroupSync_AnUnconfirmedGroupWaitsForTheConfirmation(t *testing.T) {
	t.Parallel()
	w := newGroupWorld(t)
	a := w.member(t, "Anne", "ext-a", "enabled")
	g := w.group(t, "found", []int64{a}, nil, security)
	w.prov.SetGroupUnconfirmed(strconv.FormatInt(g, 10), true)

	w.cycle(t)
	row := w.section(t, g)
	if row.State != "pending" || str(row.LastError) != "group_unconfirmed" || row.LastErrorField != nil || row.Attempts != 0 ||
		row.ExternalID != nil || row.LastValuesHash != nil || row.NextAttemptAt == nil || !row.NextAttemptAt.After(w.h.Now()) {
		t.Fatalf("the section = %+v, want pending, group_unconfirmed, nothing counted, due at the next poll", row)
	}
	if item := groupItem(t, w, g); item != nil {
		t.Errorf("an unconfirmed group raised %+v", item)
	}
	w.later()
	w.cycle(t)
	if got := w.rec.groupCalls(); !slices.Equal(got, []string{"EnsureGroup", "EnsureGroup"}) {
		t.Errorf("group calls while unconfirmed = %v, want EnsureGroup each poll and no SetMembers", got)
	}
	if row := w.section(t, g); row.Attempts != 0 || row.State != "pending" {
		t.Errorf("the section after a second poll = %+v, want pending, nothing counted", row)
	}

	w.prov.SetGroupUnconfirmed(strconv.FormatInt(g, 10), false)
	w.later()
	w.cycle(t)
	if got := w.rec.groupCalls(); !slices.Equal(got, []string{"EnsureGroup", "EnsureGroup", "EnsureGroup", "SetMembers"}) {
		t.Errorf("group calls after the confirmation = %v", got)
	}
	if row := w.section(t, g); row.State != "synced" || row.LastError != nil || row.ExternalID == nil {
		t.Errorf("the section after the confirmation = %+v, want synced", row)
	}
}

// A value the provider refuses for a group it holds already does not freeze
// the members (the review's MEDIUM 3): the refusal is kept, failed, with its
// field, and EnsureGroup is not asked again until the values change — but
// SetMembers runs in the same claim, and a member leaving or joining
// afterwards makes the section due at once (writeGroupMembers), the
// refusal still on the row.
func TestGroupSync_ATraitRefusalDoesNotFreezeTheMembers(t *testing.T) {
	t.Parallel()
	w := newGroupWorld(t)
	a := w.member(t, "Anne", "ext-a", "enabled")
	b := w.member(t, "Bjørn", "ext-b", "enabled")
	c := w.member(t, "Cato", "ext-c", "enabled")
	g := w.group(t, "ops", []int64{a, b}, nil, security)
	w.cycle(t)
	if row := w.section(t, g); row.State != "synced" {
		t.Fatalf("the section = %+v, want synced", row)
	}

	// HR changes the type, which the provider refuses, and Bjørn leaves.
	w.setValues(t, g, map[string]any{"type": "microsoft365"})
	if _, err := w.q.DeleteGroupMember(context.Background(), store.DeleteGroupMemberParams{GroupID: g, PersonID: b}); err != nil {
		t.Fatal(err)
	}
	w.cycle(t)
	if got, want := w.rec.groupCalls(), []string{"EnsureGroup", "SetMembers", "EnsureGroup", "SetMembers"}; !slices.Equal(got, want) {
		t.Errorf("group calls = %v, want %v (the members sent despite the refusal)", got, want)
	}
	if got := w.rec.lastMembers(t); !slices.Equal(got, []string{"ext-a"}) {
		t.Errorf("SetMembers = %v, want [ext-a]: Bjørn left", got)
	}
	row := w.section(t, g)
	if row.State != "failed" || str(row.LastError) != contracts.TraitImmutable || str(row.LastErrorField) != "type" ||
		row.LastValuesHash == nil || row.NextAttemptAt == nil || row.ExternalID == nil {
		t.Fatalf("the section = %+v, want failed trait_immutable on type, the values' hash kept, due at the next poll", row)
	}

	// An unchanged poll asks nothing, and the refusal stays.
	w.later()
	w.cycle(t)
	if got := w.rec.groupCalls(); len(got) != 4 {
		t.Errorf("group calls after an unchanged poll = %v, want none more", got)
	}
	if row := w.section(t, g); row.State != "failed" || str(row.LastError) != contracts.TraitImmutable {
		t.Errorf("the section after an unchanged poll = %+v, want the refusal kept", row)
	}

	// Cato joins by HR's PUT: the section is due at once, the members sent,
	// the refusal kept.
	do(t, w.hr, http.MethodPut, groupMembersPath(g), map[string]any{"personIds": []int64{a, c}}, http.StatusOK, nil)
	if row := w.section(t, g); row.NextAttemptAt == nil || row.NextAttemptAt.After(w.h.Now()) {
		t.Errorf("the section after a member joined = %+v, want due now", row)
	}
	w.cycle(t)
	if got, want := w.rec.groupCalls(), []string{"EnsureGroup", "SetMembers", "EnsureGroup", "SetMembers", "SetMembers"}; !slices.Equal(got, want) {
		t.Errorf("group calls after a member joined = %v, want %v", got, want)
	}
	if got := w.rec.lastMembers(t); !slices.Equal(got, []string{"ext-a", "ext-c"}) {
		t.Errorf("SetMembers = %v, want [ext-a ext-c]", got)
	}
	if row := w.section(t, g); row.State != "failed" || str(row.LastError) != contracts.TraitImmutable || str(row.LastErrorField) != "type" {
		t.Errorf("the section = %+v, want the refusal kept", row)
	}

	// HR mends the values: synced.
	w.setValues(t, g, security)
	w.cycle(t)
	if row := w.section(t, g); row.State != "synced" || row.LastError != nil || row.LastErrorField != nil {
		t.Errorf("the section after the values were mended = %+v, want synced", row)
	}
}

// Every group's members are reconciled once a day whatever Staff saw (the
// review's LOW 6): the members' hash covers the civil day, so the first
// cycle of a new day sends them again, and drift in the directory — a
// member added by hand — is undone.
func TestGroupSync_TheMembersAreReconciledDaily(t *testing.T) {
	t.Parallel()
	w := newGroupWorld(t)
	a := w.member(t, "Anne", "ext-a", "enabled")
	g := w.group(t, "daily", []int64{a}, nil, security)
	w.cycle(t)
	w.later()
	w.cycle(t)
	if got := w.rec.groupCalls(); !slices.Equal(got, []string{"EnsureGroup", "SetMembers"}) {
		t.Fatalf("group calls the same day = %v, want the first sync's alone", got)
	}
	w.h.Advance(24 * time.Hour)
	w.cycle(t)
	if got := w.rec.groupCalls(); !slices.Equal(got, []string{"EnsureGroup", "SetMembers", "SetMembers"}) {
		t.Errorf("group calls the next day = %v, want one SetMembers more", got)
	}
	if row := w.section(t, g); row.State != "synced" {
		t.Errorf("the section = %+v, want synced", row)
	}
}

// What a provider's GroupTraitError names is bounded before it is written
// (the review's LOW 9): the field to 40 characters, the code to 200.
func TestGroupSync_AProvidersFieldAndCodeAreBounded(t *testing.T) {
	t.Parallel()
	w := newGroupWorld(t)
	a := w.member(t, "Anne", "ext-a", "enabled")
	g := w.group(t, "long", []int64{a}, nil, security)
	w.prov.FailWith("EnsureGroup", &contracts.GroupTraitError{Field: strings.Repeat("f", 60), Code: strings.Repeat("c", 300)})
	w.cycle(t)
	row := w.section(t, g)
	if row.State != "failed" || str(row.LastErrorField) != strings.Repeat("f", 40) || str(row.LastError) != strings.Repeat("c", 200) {
		t.Errorf("the section = %+v, want the field cut at 40 and the code at 200", row)
	}
}
