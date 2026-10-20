package staff_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The provider column (Entra design D7, plan Task 4a): a person whose
// account kind is a provider's (entra) is worked provider first, identity
// second, under the claim the cycle holds on the identity link, against
// modtest's fake provider and the fake accounts. The harness's today is
// 2026-09-12.

// providerWorld is an accountsWorld with a fake identity provider "entra"
// in the slot, on the harness's clock.
type providerWorld struct {
	accountsWorld
	prov *modtest.FakeIdentityProvisioner
}

func newProviderWorld(t *testing.T, opts ...modtest.Option) providerWorld {
	t.Helper()
	prov := modtest.NewFakeIdentityProvisioner(contracts.AccountKindEntra, "Microsoft Entra ID")
	w := newAccountsWorld(t, append([]modtest.Option{modtest.WithIdentityProvisioners(prov)}, opts...)...)
	prov.WithClock(w.h.Now)
	return providerWorld{accountsWorld: w, prov: prov}
}

// entraEmployee is accountEmployee of kind entra.
func (w providerWorld) entraEmployee(t *testing.T, given, start string, end *string) gen.StaffPerson {
	t.Helper()
	p := w.employee(t, given, start, end)
	w.h.Exec(t, `UPDATE staff.people SET account_kind = 'entra' WHERE id = $1`, p.Id)
	return p
}

// extLink is a person's provider link's columns as a test reads them.
type extLink struct {
	State, ExternalID, Reason, LastError, UPN, Email string
	Announced, PassIssued                            *time.Time
	Codes                                            []string
	SpecHash                                         bool
}

func extLinkOf(t *testing.T, w providerWorld, personID int64) (extLink, bool) {
	t.Helper()
	var l extLink
	err := w.h.Pool().QueryRow(context.Background(), `
		SELECT state, coalesce(external_id, ''), coalesce(blocked_reason, ''), coalesce(last_error, ''),
			coalesce(external_upn, ''), coalesce(external_email, ''), announced_at, pass_issued_at, reported_codes,
			last_spec_hash IS NOT NULL
		FROM staff.identity_links WHERE person_id = $1 AND provider = 'entra'`, personID).
		Scan(&l.State, &l.ExternalID, &l.Reason, &l.LastError, &l.UPN, &l.Email, &l.Announced, &l.PassIssued, &l.Codes, &l.SpecHash)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return extLink{}, false
		}
		t.Fatalf("read person %d's provider link: %v", personID, err)
	}
	return l, true
}

func mustExtLink(t *testing.T, w providerWorld, personID int64) extLink {
	t.Helper()
	l, ok := extLinkOf(t, w, personID)
	if !ok {
		t.Fatalf("person %d has no provider link", personID)
	}
	return l
}

// providerMethods is the methods of the fake provider's calls so far.
func providerMethods(p *modtest.FakeIdentityProvisioner) []string {
	var out []string
	for _, c := range p.Calls() {
		out = append(out, c.Method)
	}
	return out
}

// eventKinds is personID's event kinds, in order.
func eventKinds(t *testing.T, w providerWorld, personID int64) []string {
	t.Helper()
	rows, err := w.h.Pool().Query(context.Background(), `SELECT kind FROM staff.events WHERE person_id = $1 ORDER BY id`, personID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		out = append(out, k)
	}
	return out
}

// nextDay moves the harness clock a day on, past every link's next attempt.
func (w providerWorld) nextDay() { w.h.Advance(24 * time.Hour) }

// A due joiner of kind entra: the provider's Prepare, the announcement and
// the provider's Activate — three provider calls, the announcement among
// them — all before identity is asked anything; identity's Prepare named the
// kind and found nobody, which is no refusal while the account has not
// arrived. The provider link records the account, its UPN, the announcement,
// the pass and the spec; the events name each step with codes only.
func TestAccountsWorker_TheProviderChainComesFirst(t *testing.T) {
	t.Parallel()
	w := newProviderWorld(t)
	p := w.entraEmployee(t, "Åse", "2026-09-01", nil)
	var providerCallsAtIdentity = -1
	w.fake.whenWriting(func(method string, _ uuid.UUID) {
		if providerCallsAtIdentity < 0 {
			providerCallsAtIdentity = len(w.prov.Calls())
		}
	})
	w.cycle(t)

	if got, want := providerMethods(w.prov), []string{"Prepare", "Activate"}; !slices.Equal(got, want) {
		t.Errorf("provider calls = %v, want %v", got, want)
	}
	if got, want := w.fake.writeCalls(), []string{"Expect:" + p.EmployeeNumber, "Prepare:" + p.EmployeeNumber}; !slices.Equal(got, want) {
		t.Errorf("identity writes = %v, want %v", got, want)
	}
	if providerCallsAtIdentity != 1 {
		t.Errorf("provider calls made before identity's first write = %d, want 1 (Prepare, then Expect)", providerCallsAtIdentity)
	}
	acc, ok := w.prov.ByNumber(p.EmployeeNumber)
	if !ok || acc.State != "enabled" || !acc.HasPass {
		t.Fatalf("the external account = %+v (%v), want enabled with a pass", acc, ok)
	}
	ann, ok := w.fake.announcements()[contracts.NormalizeEmployeeNumber(p.EmployeeNumber)]
	if !ok || ann.ObjectID != acc.ExternalID || ann.UserPrincipalName != acc.UserPrincipalName || ann.ReplaceUserID != nil || ann.Issuer != "" {
		t.Errorf("the announcement = %+v (%v), want the object id %s and the UPN %s, no issuer, no user replaced", ann, ok, acc.ExternalID, acc.UserPrincipalName)
	}
	if got := w.fake.callKinds(); !slices.Equal(got, []string{"Prepare:entra"}) {
		t.Errorf("identity calls' kinds = %v, want [Prepare:entra]", got)
	}
	l := mustExtLink(t, w, p.Id)
	if l.State != "enabled" || l.ExternalID != acc.ExternalID || l.UPN != acc.UserPrincipalName || l.Announced == nil ||
		l.PassIssued == nil || !l.SpecHash || l.Reason != "" || l.LastError != "" {
		t.Errorf("the provider link = %+v, want enabled, the account, its UPN, announced, a pass issued, the spec", l)
	}
	if id := mustLink(t, w.h, p.Id); id.State != "preparing" || id.Reason != "" || id.User != "" {
		t.Errorf("the identity link = %+v, want preparing with no reason and no user (the account has not arrived)", id)
	}
	kinds := eventKinds(t, w, p.Id)
	for _, want := range []string{"account_external_prepared", "account_announced", "account_external_activated"} {
		if !slices.Contains(kinds, want) {
			t.Errorf("events = %v, want %s", kinds, want)
		}
	}
	spec, _ := w.prov.LastSpec(p.EmployeeNumber)
	if spec.WorkEmail != *p.WorkEmail || spec.EmployeeNumber != p.EmployeeNumber || len(spec.Licences) != 0 || spec.Flags.Fresh {
		t.Errorf("the spec = %+v, want the work email, the number, no licences, not fresh", spec)
	}
	if _, ok := spec.Fields["jobTitle"]; !ok {
		t.Errorf("the spec's fields = %v, want the switched-on jobTitle", spec.Fields)
	}
	if _, ok := spec.Fields["businessPhone"]; ok {
		t.Errorf("the spec's fields = %v, want no phones (switched off by default)", spec.Fields)
	}

	// The account arrives (the provider's SCIM push): identity's Prepare
	// finds it, and the one Activate gives the roles, with the kind.
	user := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: user, State: contracts.AccountStateActive, Managed: true,
		EmployeeNumber: p.EmployeeNumber, HasCredential: true})
	w.h.Advance(time.Hour + time.Minute)
	w.cycle(t)
	if id := mustLink(t, w.h, p.Id); id.User != user.String() || id.State != "active" {
		t.Errorf("the identity link after the arrival = %+v, want the user, active", id)
	}
	if got := w.fake.callKinds(); !slices.Contains(got, "Activate:entra") {
		t.Errorf("identity calls' kinds = %v, want Activate:entra (the roles, once)", got)
	}
	activations := countWrites(w.fake, "Activate")
	w.nextDay()
	w.cycle(t)
	if got := countWrites(w.fake, "Activate"); got != activations {
		t.Errorf("Activate calls after another day = %d, want %d (the roles are given once)", got, activations)
	}
	if got := providerMethods(w.prov); slices.Contains(got[2:], "Prepare") || slices.Contains(got[2:], "Activate") {
		t.Errorf("provider calls after the first cycle = %v, want neither a Prepare nor an Activate", got[2:])
	}
}

// A person whose identity user is pending and credential-less — prepared
// while their kind was local — announced with ReplaceUserID naming that
// user once HR makes their kind entra; the identity link reports no
// mode_changed: the arrival completes the user.
func TestAccountsWorker_TheAnnouncementReplacesAPendingLocalUser(t *testing.T) {
	t.Parallel()
	w := newProviderWorld(t)
	p := w.employee(t, "Lokal", "2026-09-01", nil)
	w.cycle(t)
	id := mustLink(t, w.h, p.Id)
	if id.User == "" || id.State != "pending" {
		t.Fatalf("the local user = %+v, want a pending user", id)
	}
	w.h.Exec(t, `UPDATE staff.people SET account_kind = 'entra' WHERE id = $1`, p.Id)
	w.nextDay()
	w.cycle(t)
	ann, ok := w.fake.announcements()[contracts.NormalizeEmployeeNumber(p.EmployeeNumber)]
	if !ok || ann.ReplaceUserID == nil || ann.ReplaceUserID.String() != id.User {
		t.Errorf("the announcement = %+v (%v), want ReplaceUserID %s", ann, ok, id.User)
	}
	if after := mustLink(t, w.h, p.Id); after.Reason != "" {
		t.Errorf("the identity link = %+v, want no reason (not mode_changed)", after)
	}
}

// The provider unconfigured: nothing is prepared, made or announced, the
// identity link waits too, the status is recorded and raises
// entraNotConnected counting the people of the kind; a leaver's accounts are
// still deactivated on both links.
func TestAccountsWorker_AProviderNotConnectedReportsAndDeactivatesOnly(t *testing.T) {
	t.Parallel()
	w := newProviderWorld(t)
	p := w.entraEmployee(t, "Venter", "2026-09-01", nil)
	w.cycle(t) // enabled while connected
	user := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: user, State: contracts.AccountStateActive, Managed: true,
		EmployeeNumber: p.EmployeeNumber, HasCredential: true})
	w.nextDay()
	w.cycle(t) // the identity link names the user

	w.prov.SetStatus(contracts.ProviderUnconfigured, "")
	joiner := w.entraEmployee(t, "Ny", "2026-09-01", nil)
	before := len(w.prov.Calls())
	writes := len(w.fake.writeCalls())
	w.nextDay()
	w.cycle(t)
	if got := w.prov.Calls()[before:]; len(got) != 0 {
		t.Errorf("provider calls while unconfigured = %v, want none", got)
	}
	if got := w.fake.writeCalls()[writes:]; len(got) != 0 {
		t.Errorf("identity writes while unconfigured = %v, want none", got)
	}
	if _, ok := extLinkOf(t, w, joiner.Id); ok {
		t.Error("the joiner has a provider link, want none while the provider is unconfigured")
	}
	if l := mustExtLink(t, w, p.Id); l.Reason != "provider_unavailable" || l.State != "enabled" {
		t.Errorf("the employee's provider link = %+v, want enabled, provider_unavailable", l)
	}
	status := modtest.One[string](t, w.h.Harness, `SELECT provider_status->'entra'->>'status' FROM staff.settings`)
	if status != contracts.ProviderUnconfigured {
		t.Errorf("the recorded status = %q, want unconfigured", status)
	}
	items := attentionItems(t, w.hr)
	i := slices.IndexFunc(items, func(i gen.StaffStatsAttentionItem) bool { return i.Type == gen.EntraNotConnected })
	if i < 0 || items[i].Count == nil || *items[i].Count != 2 || items[i].EntityId != "settings" {
		t.Errorf("attention = %+v, want entraNotConnected counting the two people of the kind", items)
	}

	// The employee leaves: both links deactivated whatever the status.
	w.h.Exec(t, `UPDATE staff.employments SET end_date = '2026-09-13', end_reason = 'contract_ended' WHERE person_id = $1`, p.Id)
	before = len(w.prov.Calls())
	w.nextDay()
	w.nextDay()
	w.cycle(t)
	if got := providerMethods(w.prov)[before:]; !slices.Equal(got, []string{"Deactivate"}) {
		t.Errorf("provider calls for the leaver = %v, want [Deactivate]", got)
	}
	if !w.fake.staffDisabledUser(user) {
		t.Error("the leaver's identity user is not disabled")
	}
}

// Disable now deactivates the provider's account first, then the identity
// user; a provider's refusal answers 409 provider_refused with the code.
func TestAccountsRun_DisableNowDeactivatesBothLinks(t *testing.T) {
	t.Parallel()
	w := newProviderWorld(t)
	p := w.entraEmployee(t, "Slutt", "2026-09-01", nil)
	w.cycle(t)
	user := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: user, State: contracts.AccountStateActive, Managed: true,
		EmployeeNumber: p.EmployeeNumber, HasCredential: true})
	w.nextDay()
	w.cycle(t)

	do(t, w.hr, http.MethodPost, accountDisablePath(p.Id), nil, http.StatusOK, nil)
	if l := mustExtLink(t, w, p.Id); l.State != "disabled" || l.PassIssued != nil {
		t.Errorf("the provider link = %+v, want disabled with no pass", l)
	}
	if acc, _ := w.prov.ByNumber(p.EmployeeNumber); acc.State != "disabled" {
		t.Errorf("the external account = %+v, want disabled", acc)
	}
	if !w.fake.staffDisabledUser(user) {
		t.Error("the identity user is not disabled")
	}

	q := w.entraEmployee(t, "Nekt", "2026-09-01", nil)
	w.cycle(t)
	w.prov.FailWith("Deactivate", contracts.ErrExternalPrivileged)
	r := w.hr.Do(http.MethodPost, accountDisablePath(q.Id), nil)
	if r.Status != http.StatusConflict || !strings.Contains(string(r.Body), "provider_refused") || !strings.Contains(string(r.Body), "external_privileged") {
		t.Errorf("Disable now refused by the provider = %d %s, want 409 provider_refused naming external_privileged", r.Status, r.Body)
	}
}

// A leaver's account is deactivated, then deleted once
// delete_external_after_days have passed since the last end — never under a
// legal hold; a rehire is activated again with a new pass.
func TestAccountsWorker_TheLeaverTheDeleteAndTheRehire(t *testing.T) {
	t.Parallel()
	w := newProviderWorld(t)
	w.h.Exec(t, `UPDATE staff.settings SET delete_external_after_days = 30`)
	gone := w.entraEmployee(t, "Borte", "2026-01-01", nil)
	held := w.entraEmployee(t, "Holdt", "2026-01-01", nil)
	rehired := w.entraEmployee(t, "Igjen", "2026-01-01", nil)
	w.cycle(t)
	for _, p := range []gen.StaffPerson{gone, held} {
		w.h.Exec(t, `UPDATE staff.employments SET end_date = '2026-08-01', end_reason = 'contract_ended' WHERE person_id = $1`, p.Id)
	}
	// Ended within the thirty days: deactivated, not deleted.
	w.h.Exec(t, `UPDATE staff.employments SET end_date = '2026-09-10', end_reason = 'contract_ended' WHERE person_id = $1`, rehired.Id)
	w.h.Exec(t, `UPDATE staff.people SET legal_hold = true, legal_hold_reason = 'tvist', legal_hold_at = now() WHERE id = $1`, held.Id)
	w.nextDay()
	w.cycle(t)
	if l := mustExtLink(t, w, gone.Id); l.State != "deleted" {
		t.Errorf("the leaver's provider link = %+v, want deleted (deactivated, then deleted past the setting)", l)
	}
	if l := mustExtLink(t, w, held.Id); l.State != "disabled" {
		t.Errorf("the held leaver's provider link = %+v, want disabled and never deleted", l)
	}
	if acc, _ := w.prov.ByNumber(held.EmployeeNumber); acc.State != "disabled" {
		t.Errorf("the held leaver's account = %+v, want disabled", acc)
	}

	l := mustExtLink(t, w, rehired.Id)
	if acc, _ := w.prov.ByNumber(rehired.EmployeeNumber); l.State != "disabled" || l.PassIssued != nil || acc.HasPass {
		t.Fatalf("the recent leaver's provider link = %+v, want disabled with no pass", l)
	}
	// The rehire: the disabled account activated again, with a new pass.
	w.h.Exec(t, `UPDATE staff.employments SET end_date = NULL, end_reason = NULL WHERE person_id = $1`, rehired.Id)
	w.nextDay()
	w.cycle(t)
	l = mustExtLink(t, w, rehired.Id)
	acc, _ := w.prov.ByNumber(rehired.EmployeeNumber)
	if l.State != "enabled" || l.PassIssued == nil || !acc.HasPass {
		t.Errorf("the rehire's provider link = %+v and account %+v, want enabled with a new pass", l, acc)
	}
}

// An account the directory no longer has is failed user_gone and never
// prepared again.
func TestAccountsWorker_AnExternalAccountGoneIsNeverRecreated(t *testing.T) {
	t.Parallel()
	w := newProviderWorld(t)
	p := w.entraEmployee(t, "Vekk", "2026-09-01", nil)
	w.cycle(t)
	w.prov.Gone(p.EmployeeNumber)
	for range 2 {
		w.nextDay()
		w.cycle(t)
	}
	if l := mustExtLink(t, w, p.Id); l.State != "failed" || l.Reason != "user_gone" {
		t.Errorf("the provider link = %+v, want failed user_gone", l)
	}
	if n := slices.Index(providerMethods(w.prov)[1:], "Prepare"); n >= 0 {
		t.Errorf("provider calls = %v, want no second Prepare", providerMethods(w.prov))
	}
	items := attentionItems(t, w.hr)
	if !slices.ContainsFunc(items, func(i gen.StaffStatsAttentionItem) bool { return i.Type == gen.AccountUserGone }) {
		t.Errorf("attention = %+v, want accountUserGone", items)
	}
}

// Every code the provider reports becomes its item — the licence's once,
// its SKU in no title — as do a pass not shown within a day and an
// announcement nothing fulfilled within ninety days; no item's title
// carries an address.
func TestAccountAttention_TheProviderItems(t *testing.T) {
	t.Parallel()
	w := newProviderWorld(t)
	p := w.entraEmployee(t, "Rapport", "2026-09-01", nil)
	w.prov.SetAccount(p.EmployeeNumber, contracts.ExternalAccount{
		UserPrincipalName: "rapport.konto@fake.example", Email: "rapport.konto@fake.example", State: "disabled",
		Reported: []string{contracts.ReportedHireDateNotWritten, contracts.ReportedPrivileged,
			contracts.ReportedLicenceUnavailable + ":ENTERPRISEPACK", contracts.ReportedLicenceUnavailable + ":EMS",
			contracts.ReportedPassNotAllowed},
	})
	w.cycle(t)
	w.h.Advance(91 * 24 * time.Hour)
	items := attentionItems(t, w.hr)
	want := []gen.StaffStatsAttentionItemType{gen.HireDateNotWritten, gen.AccountPrivilegedInEntra, gen.LicenceUnavailable,
		gen.PassUnrevealed, gen.AccountNotArrived}
	for _, kind := range want {
		n := 0
		for _, i := range items {
			if i.Type == kind {
				n++
				if i.Title != p.DisplayName {
					t.Errorf("%s's title = %q, want the person's name alone", kind, i.Title)
				}
			}
		}
		if n != 1 {
			t.Errorf("attention has %d %s items, want one: %+v", n, kind, items)
		}
	}
	for _, i := range items {
		if strings.Contains(i.Title, "@") || strings.Contains(i.Title, "ENTERPRISEPACK") {
			t.Errorf("item %s's title %q carries an address or a SKU", i.Id, i.Title)
		}
	}
}

// The provider's status is recorded once per change, and a failing one with
// an expired secret raises secretExpired.
func TestAccountsWorker_TheProviderStatusIsRecorded(t *testing.T) {
	t.Parallel()
	w := newProviderWorld(t)
	w.entraEmployee(t, "Status", "2026-09-01", nil)
	w.cycle(t)
	var seen map[string]map[string]any
	if err := json.Unmarshal([]byte(modtest.One[string](t, w.h.Harness, `SELECT provider_status::text FROM staff.settings`)), &seen); err != nil {
		t.Fatal(err)
	}
	if seen["entra"]["status"] != contracts.ProviderConnected || seen["entra"]["checkedAt"] == nil {
		t.Errorf("provider_status = %v, want entra connected with checkedAt", seen)
	}
	w.prov.SetStatus(contracts.ProviderFailing, "secret_expired")
	w.nextDay()
	w.cycle(t)
	if got := modtest.One[string](t, w.h.Harness, `SELECT provider_status->'entra'->>'reason' FROM staff.settings`); got != "secret_expired" {
		t.Errorf("the recorded reason = %q, want secret_expired", got)
	}
	items := attentionItems(t, w.hr)
	if !slices.ContainsFunc(items, func(i gen.StaffStatsAttentionItem) bool { return i.Type == gen.SecretExpired }) {
		t.Errorf("attention = %+v, want secretExpired", items)
	}
}

// The account kinds are seeded from the recorded mode once — federated →
// external, the default too — and a later change of the mode seeds nothing.
func TestAccountsWorker_TheKindsAreSeededFromTheModeOnce(t *testing.T) {
	t.Parallel()
	fake := newFakeAccounts()
	fake.mode = contracts.AccountModeFederated
	h := newHarness(t, withAccounts(fake))
	hr, _ := signIn(t, h, "staff:manage")
	if got := modtest.One[string](t, h.Harness, `SELECT default_account_kind FROM staff.settings`); got != "external" {
		t.Errorf("the default kind after the mount's seed = %q, want external", got)
	}
	p := createPerson(t, hr, newPerson("Frø", "Sådd"))
	h.Exec(t, `UPDATE staff.people SET account_kind = 'local' WHERE id = $1`, p.Id)
	h.Exec(t, `UPDATE staff.settings SET account_kind_seeded = false, default_account_kind = 'local'`)
	runAccounts(t, h)
	if got := modtest.One[string](t, h.Harness, `SELECT account_kind FROM staff.people WHERE id = $1`, p.Id); got != "external" {
		t.Errorf("the person's kind after the cycle's seed = %q, want external", got)
	}
	fake.set(func(f *fakeAccounts) { f.mode = contracts.AccountModeLocal })
	runAccounts(t, h)
	if got := modtest.One[string](t, h.Harness, `SELECT account_kind FROM staff.people WHERE id = $1`, p.Id); got != "external" {
		t.Errorf("the person's kind after a second cycle = %q, want external (seeded once)", got)
	}
	if !strings.Contains(h.Logs(), "the account kinds were seeded from the authority mode") {
		t.Error("the seed was not logged")
	}
}

// attentionItems is GET /stats/attention as c reads it.
func attentionItems(t *testing.T, c *modtest.Client) []gen.StaffStatsAttentionItem {
	t.Helper()
	var items []gen.StaffStatsAttentionItem
	do(t, c, http.MethodGet, attentionPath, nil, http.StatusOK, &items)
	return items
}

// HR's Run now on an enabled account issues a new pass (Flags.Fresh on the
// provider's Activate); the worker's own cycle never sets it.
func TestAccountsRun_RunNowIssuesAFreshPass(t *testing.T) {
	t.Parallel()
	w := newProviderWorld(t)
	p := w.entraEmployee(t, "Fersk", "2026-09-01", nil)
	w.cycle(t)
	for _, c := range w.prov.Calls() {
		if c.Spec != nil && c.Spec.Flags.Fresh {
			t.Errorf("the worker's %s set Fresh", c.Method)
		}
	}
	before := len(w.prov.Calls())
	w.h.Advance(time.Minute)
	do(t, w.hr, http.MethodPost, accountRunPath(p.Id), nil, http.StatusOK, nil)
	calls := w.prov.Calls()[before:]
	if !slices.ContainsFunc(calls, func(c modtest.ProvisionerCall) bool {
		return c.Method == "Activate" && c.Spec != nil && c.Spec.Flags.Fresh
	}) {
		t.Errorf("Run now's provider calls = %+v, want a fresh Activate", calls)
	}
	if l := mustExtLink(t, w, p.Id); l.PassIssued == nil || !l.PassIssued.Equal(w.h.Now()) {
		t.Errorf("the provider link = %+v, want the new pass issued now", l)
	}
}

// The manager travels as their own external account's id; a manager with
// none is no manager in the directory, recorded manager_not_in_entra once
// and raised as managerNotInEntra.
func TestAccountsWorker_TheManagerIsTheirExternalAccount(t *testing.T) {
	t.Parallel()
	w := newProviderWorld(t)
	boss := createPerson(t, w.hr, newPerson("Sjef", "Leder"))
	seedEmploymentDated(t, w.h, boss.Id, 0, "2020-01-01", nil)
	p := createPerson(t, w.hr, with(newPerson("Ansatt", "Under"), "workEmail", "ansatt.under@example.test"))
	seedEmploymentDated(t, w.h, p.Id, boss.Id, "2026-09-01", nil)
	flagOn(t, w.hr, p.Id)
	w.h.Exec(t, `UPDATE staff.people SET account_kind = 'entra' WHERE id = $1`, p.Id)
	w.cycle(t)
	if l := mustExtLink(t, w, p.Id); !slices.Equal(l.Codes, []string{"manager_not_in_entra"}) {
		t.Errorf("the provider link's codes = %v, want [manager_not_in_entra]", l.Codes)
	}
	if spec, _ := w.prov.LastSpec(p.EmployeeNumber); spec.ManagerExternalID != "" {
		t.Errorf("the spec's manager = %q, want none", spec.ManagerExternalID)
	}
	items := attentionItems(t, w.hr)
	if !slices.ContainsFunc(items, func(i gen.StaffStatsAttentionItem) bool {
		return i.Type == gen.ManagerNotInEntra && i.EntityId != "settings"
	}) {
		t.Errorf("attention = %+v, want managerNotInEntra", items)
	}

	bossAccount := uuid.NewString()
	w.h.Exec(t, `INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, created_at, updated_at)
		VALUES ($1, 'entra', $2, 'enabled', true, $3, $3)`, boss.Id, bossAccount, w.h.Now())
	w.nextDay()
	w.cycle(t)
	if spec, _ := w.prov.LastSpec(p.EmployeeNumber); spec.ManagerExternalID != bossAccount {
		t.Errorf("the spec's manager = %q, want the boss's external account %s", spec.ManagerExternalID, bossAccount)
	}
	if l := mustExtLink(t, w, p.Id); len(l.Codes) != 0 {
		t.Errorf("the provider link's codes = %v, want none once the manager has an account", l.Codes)
	}
}
